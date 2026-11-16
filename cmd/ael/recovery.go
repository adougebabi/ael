package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// This is a disposable recovery index. Only live Server reads authorize work.
// It deliberately has no lease, token, task payload, command, or transcript.
type recoveryIndex struct {
	CLIBuild                 string                `json:"cli_build"`
	SkillPath                string                `json:"skill_path,omitempty"`
	ToolSessionFile          string                `json:"tool_session_file,omitempty"`
	Responsibility           string                `json:"responsibility_state"`
	TaskStatus               string                `json:"task_status,omitempty"`
	CurrentExecutor          string                `json:"current_executor,omitempty"`
	FailedAttempts           []recoveryFailure     `json:"failed_attempts"`
	Schema                   int                   `json:"schema"`
	Server                   string                `json:"server"`
	Profile                  string                `json:"profile"`
	Project                  string                `json:"project_id"`
	Checkout                 string                `json:"checkout"`
	Session                  string                `json:"session"`
	Runner                   string                `json:"runner"`
	Workspace                string                `json:"workspace"`
	Repository               string                `json:"repository"`
	Parent                   string                `json:"parent,omitempty"`
	CompletedChildren        []string              `json:"completed_children"`
	ParentDefinitionRevision int                   `json:"parent_definition_revision,omitempty"`
	ParentPlanRevision       int                   `json:"parent_plan_revision,omitempty"`
	Task                     string                `json:"task,omitempty"`
	Step                     string                `json:"step,omitempty"`
	Attempt                  string                `json:"attempt,omitempty"`
	Context                  string                `json:"context,omitempty"`
	Revision                 string                `json:"source_revision,omitempty"`
	DefinitionRevision       int                   `json:"definition_revision,omitempty"`
	PlanRevision             int                   `json:"plan_revision,omitempty"`
	Candidate                string                `json:"candidate,omitempty"`
	LocalCandidate           string                `json:"local_candidate,omitempty"`
	Head                     string                `json:"head,omitempty"`
	Basis                    []core.BasisReference `json:"applicable_knowledge"`
	Passed                   []recoveryEvidence    `json:"passed"`
	Unfinished               []string              `json:"unfinished"`
	UnfinishedAcceptance     []string              `json:"unfinished_acceptance"`
	Scope                    []string              `json:"scope"`
	NonGoals                 []string              `json:"non_goals"`
	Blocker                  *core.Blocker         `json:"blocker,omitempty"`
	Failure                  string                `json:"recent_failure,omitempty"`
	Handoff                  recoveryHandoff       `json:"handoff"`
	CLI                      string                `json:"cli_version"`
	Skill                    string                `json:"skill_version"`
	APIProtocol              int                   `json:"api_protocol"`
	SkillProtocol            int                   `json:"skill_protocol"`
	Generated                time.Time             `json:"generated_at"`
	Verified                 bool                  `json:"online_verified"`
	State                    string                `json:"state"`
	Next                     string                `json:"next_action"`
}

type recoveryEvidence struct {
	Check     string   `json:"check"`
	Artifacts []string `json:"artifacts"`
	Candidate string   `json:"candidate"`
}

type recoveryFailure struct {
	Attempt   string   `json:"attempt"`
	Unit      string   `json:"unit"`
	Reason    string   `json:"reason"`
	Artifacts []string `json:"artifacts"`
}

type recoveryHandoff struct {
	Next      string   `json:"next,omitempty"`
	Tried     string   `json:"tried,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
}

var recoverySecret = regexp.MustCompile(`(?i)(?:lease_|ael_token_|ael_agent_)[a-z0-9_-]+|(?:token|password|secret|authorization)\s*[:=]\s*\S+`)

func recoveryLocation(a args, config, project string, binding remote.Binding) (string, remote.Profile, string, error) {
	profile, err := remote.LoadProfile(config, project, binding.Profile)
	if err != nil {
		return "", profile, "", err
	}
	root, err := bootstrap.Canonical(project)
	if err != nil {
		return "", profile, "", err
	}
	session := a.get("session", os.Getenv("AEL_SESSION_ID"))
	if session == "" || len(session) > 200 || strings.ContainsAny(session, "\r\n\x00") {
		return "", profile, "", core.Error("RECOVERY_SESSION_REQUIRED", "provide --session HOST_SESSION_ID or AEL_SESSION_ID")
	}
	dir, err := store.SafePath(config, "recovery")
	if err != nil {
		return "", profile, "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", profile, "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(dir) != nil {
		return "", profile, "", core.Error("RECOVERY_PRIVATE_DIRECTORY_REQUIRED", "recovery directory must be private")
	}
	scope, _ := json.Marshal([]string{profile.URL, binding.Profile, binding.ProjectID, root, session, store.Hash([]byte(profile.Token))})
	path, err := store.SafePath(dir, store.Hash(scope)+".json")
	return path, profile, root, err
}

func readRecovery(path string) (recoveryIndex, error) {
	var index recoveryIndex
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return index, err
	}
	if err != nil || info.Mode().Perm()&0077 != 0 || info.Size() > 128<<10 || securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil {
		return index, core.Error("RECOVERY_INVALID", "checkpoint must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return index, err
	}
	if json.Unmarshal(raw, &index) != nil || index.Schema != 1 {
		return index, core.Error("RECOVERY_INVALID", "unsupported checkpoint")
	}
	return index, nil
}

// Remove configured credentials from every text field, including optional notes.
// Arbitrary chat/log input is never accepted by this schema.
func scrubRecovery(value reflect.Value, secret string) {
	if value.Type() == reflect.TypeFor[time.Time]() {
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			scrubRecovery(value.Elem(), secret)
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			scrubRecovery(value.Field(i), secret)
		}
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			scrubRecovery(value.Index(i), secret)
		}
	case reflect.String:
		if value.CanSet() {
			text := value.String()
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[credential removed]")
			}
			value.SetString(recoverySecret.ReplaceAllString(text, "[credential removed]"))
		}
	}
}

func saveRecovery(path string, index recoveryIndex, secret string) error {
	scrubRecovery(reflect.ValueOf(&index), secret)
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > 128<<10 {
		return core.Error("RECOVERY_TOO_LARGE", "keep handoff and recovery references short")
	}
	return store.Atomic(path, append(raw, '\n'), 0600)
}

func recoveryCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	path, profile, root, err := recoveryLocation(a, config, project, binding)
	if err != nil {
		return nil, err
	}
	unlock, err := lockAgentOperation(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	prior, err := readRecovery(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if prior.Schema == 0 {
		if cmd != "recovery save" || a.get("runner", "") == "" {
			return map[string]any{"status": "no_checkpoint", "online_verified": false, "next_action": "bind this session explicitly using recovery save --session ID --runner ID", "checkpoint": path}, nil
		}
		prior = recoveryIndex{Schema: 1, Server: profile.URL, Profile: binding.Profile, Project: binding.ProjectID, Checkout: root, Session: a.get("session", os.Getenv("AEL_SESSION_ID")), Runner: a.get("runner", ""), Parent: a.get("parent", ""), CLI: bootstrap.Version, APIProtocol: 1, SkillProtocol: 1}
		entry, entryErr := bootstrap.RemoteEntryInfo(root)
		if entryErr != nil {
			return nil, entryErr
		}
		prior.Skill = entry.Version
		prior.CLIBuild = bootstrap.Build
		toolPath, toolProfile, _, toolErr := toolSessionLocation(a, config, project, binding)
		if toolErr != nil {
			return nil, toolErr
		}
		if session, readErr := readToolSession(toolPath); readErr == nil {
			if err = validateToolSession(session, toolProfile, root, binding); err != nil {
				return nil, err
			}
			prior.CLI, prior.CLIBuild, prior.Skill, prior.SkillPath, prior.ToolSessionFile = session.Tool.Version.Version, session.Tool.Version.Build, session.SkillVersion, session.SkillPath, toolPath
		} else if !os.IsNotExist(readErr) {
			return nil, readErr
		}
	}
	if prior.Server != profile.URL || prior.Profile != binding.Profile || prior.Project != binding.ProjectID || prior.Checkout != root || prior.Session != a.get("session", os.Getenv("AEL_SESSION_ID")) || (a.get("runner", prior.Runner) != prior.Runner) {
		return nil, core.Error("RECOVERY_BINDING_CONFLICT", "checkpoint belongs to another binding or executor")
	}
	if cmd == "recovery save" && a.get("file", "") != "" {
		info, statErr := os.Lstat(a.get("file", ""))
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
			return nil, core.Error("RECOVERY_HANDOFF_INVALID", "handoff must be a private regular JSON file of at most 4 KiB")
		}
		var handoff recoveryHandoff
		if err = input(a.get("file", ""), &handoff); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(handoff)
		if len(raw) > 4096 {
			return nil, core.Error("RECOVERY_HANDOFF_TOO_LARGE", "use short next/tried/decisions notes without secrets")
		}
		prior.Handoff = handoff
	}
	current, err := refreshRecovery(prior, a, config, project, binding)
	if err != nil {
		prior.Verified, prior.State, prior.Next = false, "unverified", "preserve the workspace and restore online authority before business writes or final submission"
		scrubRecovery(reflect.ValueOf(&prior), profile.Token)
		if saveErr := saveRecovery(path, prior, profile.Token); saveErr != nil {
			return nil, saveErr
		}
		return map[string]any{"status": "unverified", "online_verified": false, "checkpoint": path, "summary": prior, "reason": recoveryFailureCode(err), "next_action": prior.Next}, nil
	}
	scrubRecovery(reflect.ValueOf(&current), profile.Token)
	if err = saveRecovery(path, current, profile.Token); err != nil {
		return nil, err
	}
	return map[string]any{"status": current.State, "online_verified": current.Verified, "checkpoint": path, "summary": current, "next_action": current.Next}, nil
}

func refreshRecovery(prior recoveryIndex, a args, config, project string, binding remote.Binding) (recoveryIndex, error) {
	index := prior
	index.Generated, index.Verified = time.Now().UTC(), false
	runner, err := remote.Call(config, project, binding, http.MethodGet, "/runners/"+url.PathEscape(index.Runner), nil, nil)
	if err != nil {
		return index, err
	}
	index.Workspace, _ = runner["workspace_id"].(string)
	index.Repository, _ = runner["repository"].(string)
	assignments, ok := runner["assignments"].([]any)
	if !ok {
		return index, core.Error("RECOVERY_SERVER_UNSUPPORTED", "Server must expose current runner assignments")
	}
	taskID := a.get("task", prior.Task)
	index.Attempt, index.Context = "", ""
	responsibility := "none"
	reportedNext := ""
	if len(assignments) > 1 {
		return index, core.Error("RECOVERY_WIP_CONFLICT", "multiple active assignments require explicit investigation")
	}
	if len(assignments) == 1 {
		assignment, ok := assignments[0].(map[string]any)
		if !ok {
			return index, core.Error("RECOVERY_INVALID", "invalid assignment")
		}
		taskID, _ = assignment["task_id"].(string)
		index.Attempt, _ = assignment["attempt_id"].(string)
		index.Context, _ = assignment["context_id"].(string)
		responsibility, _ = assignment["responsibility_state"].(string)
		reportedNext, _ = assignment["progress_next"].(string)
	}
	index.Verified, index.State, index.Next = true, "no_current_task", "wait for an explicitly authorized task selection"
	index.Responsibility, index.TaskStatus, index.CurrentExecutor = responsibility, "", ""
	if taskID == "" {
		index.Task = ""
		return index, nil
	}
	next, revision, err := runnerTask(config, project, binding, taskID)
	if err != nil {
		return index, err
	}
	task := next.Task
	index.TaskStatus = task.Status
	for _, attempt := range task.Attempts {
		if attempt.Status == "running" || attempt.Status == "interrupted" {
			index.CurrentExecutor = attempt.Executor
		}
	}
	index.Task, index.Step, index.Revision = taskID, task.Step, revision
	if prior.Revision != "" && (prior.Task != taskID || prior.Attempt != index.Attempt) {
		index.Handoff = recoveryHandoff{}
	}
	index.Parent = task.Definition.Parent
	if task.EpicMembership != nil {
		index.Parent = task.EpicMembership.Parent
	}
	if index.Parent == "" {
		index.Parent = a.get("parent", prior.Parent)
	}
	index.CompletedChildren = []string{}
	if index.Parent != "" {
		parent, parentErr := remote.Call(config, project, binding, http.MethodGet, "/tasks/"+url.PathEscape(index.Parent)+"/tree", nil, nil)
		if parentErr != nil {
			return index, parentErr
		}
		if parent["online"] != true || parent["authoritative"] != true {
			return index, core.Error("REMOTE_UNAVAILABLE", "parent must be verified online")
		}
		raw, _ := json.Marshal(parent)
		var tree core.EpicTree
		if json.Unmarshal(raw, &tree) != nil {
			return index, core.Error("RECOVERY_INVALID", "invalid parent projection")
		}
		index.ParentDefinitionRevision, index.ParentPlanRevision = tree.Root.Revision, tree.Root.PlanRevision
		for _, child := range tree.Children {
			if child.Task.Task != nil && child.Task.Task.Status == "completed" {
				index.CompletedChildren = append(index.CompletedChildren, child.Task.Task.Definition.ID)
			}
		}
	}
	index.DefinitionRevision, index.PlanRevision, index.Candidate = task.Definition.Revision, task.Plan.Revision, task.Candidate
	index.Basis, index.Scope, index.NonGoals, index.Blocker, index.Failure = task.Basis, task.Definition.Scope, task.Definition.NonGoals, task.Blocker, task.Failure
	index.FailedAttempts = []recoveryFailure{}
	for i := len(task.Attempts) - 1; i >= 0 && len(index.FailedAttempts) < 3; i-- {
		attempt := task.Attempts[i]
		if attempt.Status == "fail" {
			index.FailedAttempts = append(index.FailedAttempts, recoveryFailure{Attempt: attempt.ID, Unit: attempt.Unit, Reason: attempt.Reason, Artifacts: attempt.Outputs})
		}
	}
	index.Passed, index.Unfinished = []recoveryEvidence{}, []string{}
	covered := map[string]bool{}
	for _, check := range task.Definition.Checks {
		if evidence := core.CurrentEvidence(task, check); evidence != nil && evidence.Result == "pass" {
			index.Passed = append(index.Passed, recoveryEvidence{Check: check.ID, Artifacts: evidence.Artifacts, Candidate: evidence.Candidate})
			for _, acceptance := range check.Covers {
				covered[acceptance] = true
			}
		} else if check.Required {
			index.Unfinished = append(index.Unfinished, check.ID)
		}
	}
	index.UnfinishedAcceptance = []string{}
	for _, acceptance := range task.Definition.Acceptance {
		if !covered[acceptance] {
			index.UnfinishedAcceptance = append(index.UnfinishedAcceptance, acceptance)
		}
	}
	local, err := remote.CapturePortableCandidate(project, index.Repository)
	if err != nil {
		return index, err
	}
	index.LocalCandidate, index.Head = local.ID, local.Commit
	index.State, index.Next = "waiting_for_claim", "inspect the current Server next action and use the existing explicit claim or recovery protocol"
	if task.Status != "open" {
		index.Task, index.Step, index.Attempt, index.Context, index.State = "", "", "", "", task.Status
		index.Next = "inspect the parent task for the next unfinished item without creating or claiming work automatically"
		return index, nil
	}
	if index.Attempt != "" {
		index.State, index.Next = "responsibility_requires_recovery", "use the existing responsibility recovery rules after checking the previous executor and workspace"
		if responsibility == "active" {
			index.State, index.Next = "active", "continue the current unfinished step using its existing valid responsibility"
			if reportedNext != "" {
				index.Next = reportedNext
			}
			if index.Handoff.Next != "" {
				index.Next = index.Handoff.Next
			}
		}
	} else {
		for _, attempt := range task.Attempts {
			if attempt.Status == "running" || attempt.Status == "interrupted" {
				index.State, index.Next = "other_executor", "inspect the current executor responsibility and wait for an explicit authorized handoff"
			}
		}
	}
	if task.Blocker != nil || task.Paused || task.Confirmation != nil {
		index.State, index.Next = "blocked", "resolve the current Server blocker or confirmation before continuing this task"
	}
	if prior.Revision != "" && (prior.ParentDefinitionRevision != index.ParentDefinitionRevision || prior.ParentPlanRevision != index.ParentPlanRevision || prior.DefinitionRevision != index.DefinitionRevision || prior.PlanRevision != index.PlanRevision || prior.Candidate != index.Candidate || !reflect.DeepEqual(prior.Basis, index.Basis) || prior.Attempt != index.Attempt) {
		index.State, index.Next = "authority_changed", "read the changed current task, candidate, specifications and responsibility before continuing the unfinished step"
	}
	if prior.LocalCandidate != "" && prior.LocalCandidate != index.LocalCandidate {
		index.State, index.Next = "workspace_changed", "inspect the local HEAD and uncommitted candidate changes against the current Server task before continuing"
	}
	if index.Context != "" {
		context, contextErr := remote.Call(config, project, binding, http.MethodGet, "/contexts/"+url.PathEscape(index.Context), nil, nil)
		if contextErr != nil {
			return index, contextErr
		}
		if context["candidate_id"] != index.LocalCandidate {
			index.State, index.Next = "workspace_changed", "inspect local HEAD and uncommitted changes against the responsibility context candidate before continuing"
		}
		if context["validity"] != "current" || knowledge.CheckFresh(project) != nil {
			index.State, index.Next = "context_changed", "read the current task and specification versions and refresh context through the existing explicit protocol"
		}
	}
	if role, reported := runner["project_role"].(string); reported && role != "executor" && role != "admin" && role != "reviewer" {
		index.State, index.Next = "permission_revoked", "save the scene and request restoration or explicit handoff of the Server project responsibility"
	}
	if index.State != "permission_revoked" && index.Attempt != "" && responsibility != "active" {
		index.State, index.Next = "responsibility_requires_recovery", "use the existing responsibility recovery rules after checking the previous executor and workspace"
	}
	return index, nil
}

func recoveryFailureCode(err error) string {
	code := strings.SplitN(err.Error(), ":", 2)[0]
	if !regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`).MatchString(code) {
		return "RECOVERY_VERIFICATION_FAILED"
	}
	return code
}

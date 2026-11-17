package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

type remoteArtifactBinding struct {
	Path       string `json:"path"`
	ArtifactID string `json:"artifact_id"`
}

type remoteRunnerSubmitInput struct {
	TaskID           string                  `json:"task_id"`
	AttemptID        string                  `json:"attempt_id"`
	Token            string                  `json:"token"`
	Fence            int                     `json:"fence"`
	ExpectedRevision string                  `json:"expected_revision"`
	CandidateID      string                  `json:"candidate_id"`
	Result           core.StepResult         `json:"result"`
	Method           string                  `json:"method"`
	ExitCode         *int                    `json:"exit_code,omitempty"`
	Inputs           []string                `json:"inputs"`
	Bindings         []remoteArtifactBinding `json:"bindings"`
	OperationID      string                  `json:"operation_id"`
	IdempotencyKey   string                  `json:"idempotency_key"`
}

func remoteBindingPath(value string) bool {
	if strings.HasPrefix(value, "runtime:") {
		value = strings.TrimPrefix(value, "runtime:")
	}
	return value != "" && !strings.ContainsAny(value, "\\\x00\r\n") && !strings.HasPrefix(value, "/") && value != "." && value != ".." && !strings.HasPrefix(value, "../") && path.Clean(value) == value
}

func runnerSubmissionKey(actor, runner, key string) string {
	return "runner-" + store.Hash([]byte(actor+"\n"+runner+"\n"+key))
}

func (p *portableStore) submitRunner(principal standalonePrincipal, runnerID string, input remoteRunnerSubmitInput, artifacts *artifactStore) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.AttemptID) || input.Token == "" || input.Fence < 1 || input.ExpectedRevision == "" || !portableDigestID(input.CandidateID) || !standaloneID.MatchString(input.OperationID) || !standaloneID.MatchString(input.IdempotencyKey) || len(input.Bindings) > 32 || len(input.Inputs) > 256 || len(input.Result.Summary) > 2000 || strings.Contains(input.Result.Summary, input.Token) {
		return nil, errors.New("RUNNER_INVALID")
	}
	raw, err := json.Marshal(struct {
		Actor, Project, Runner string
		Input                  remoteRunnerSubmitInput
	}{principal.UserID, p.project, runnerID, input})
	if err != nil {
		return nil, errors.New("RUNNER_INVALID")
	}
	requestHash := store.Hash(raw)
	key := runnerSubmissionKey(principal.UserID, runnerID, input.IdempotencyKey)
	content := map[string][]byte{}
	env := &core.RemoteSubmitEnvironment{CandidateID: input.CandidateID, Fence: input.Fence, DiagnosticArtifacts: map[string]bool{}}
	env.ReadArtifact = func(_ *store.Tx, id string) ([]byte, error) {
		data, ok := content[id]
		if !ok {
			return nil, core.Error("ARTIFACT_MISSING", id)
		}
		return data, nil
	}
	env.Prepare = func(tx *store.Tx, task *core.Task, attempt *core.Attempt) (*core.Evidence, error) {
		if err := p.verifyRunnerLease(tx, principal, runnerID, input.TaskID, input.AttemptID, input.Token, input.Fence); err != nil {
			return nil, err
		}
		if _, _, err := p.service.RemoteAttemptTx(tx, input.TaskID, runnerID, input.AttemptID, input.Token, input.Fence); err != nil {
			return nil, err
		}
		var revision, output, contextID string
		if err := tx.Row("SELECT claim_revision,output_candidate,context_id FROM remote_runner_attempts WHERE attempt_id=?", input.AttemptID).Scan(&revision, &output, &contextID); err != nil {
			return nil, err
		}
		if revision != input.ExpectedRevision {
			return nil, core.Error("REVISION_CONFLICT", "expected_revision must equal attempt claim_revision "+revision)
		}
		if output != input.CandidateID {
			return nil, core.Error("CANDIDATE_CHANGED", "register candidate_id with runner observe for this attempt before submit")
		}
		if task.ContextAttempt != attempt.ID {
			return nil, core.Error("REVISION_CONFLICT", "task context belongs to a different attempt; inspect runner show before recovery")
		}
		var candidateBody string
		if err := tx.Row("SELECT body FROM portable_candidates WHERE id=?", input.CandidateID).Scan(&candidateBody); err != nil {
			return nil, errors.New("CANDIDATE_NOT_FOUND")
		}
		var candidate remote.PortableCandidate
		if json.Unmarshal([]byte(candidateBody), &candidate) != nil || remote.ValidatePortableCandidate(candidate) != nil {
			return nil, errors.New("CANDIDATE_STORAGE_ERROR")
		}
		env.Head, env.Files, env.Basis = candidate.Commit, map[string]core.CandidateFile{}, slices.Clone(task.Basis)
		for _, file := range candidate.Files {
			if !file.Deleted {
				mode := uint32(0644)
				if file.Executable {
					mode = 0755
				}
				env.Files[file.Path] = core.CandidateFile{Mode: mode, Hash: file.SHA256}
			}
		}
		rows, err := tx.Conn.QueryContext(context.Background(), "SELECT artifact_id,sha256 FROM portable_context_sources WHERE context_id=?", contextID)
		if err != nil {
			return nil, err
		}
		type contextSource struct{ id, hash string }
		var sources []contextSource
		for rows.Next() {
			var source contextSource
			if err = rows.Scan(&source.id, &source.hash); err != nil {
				break
			}
			sources = append(sources, source)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		for _, source := range sources {
			item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", source.id))
			if err != nil || item.Status != "finalized" || item.SHA256 != source.hash {
				return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
			}
			if _, err = artifacts.read(item); err != nil {
				return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
			}
		}
		byPath, byID := map[string]artifactObject{}, map[string]bool{}
		var total int64
		for _, binding := range input.Bindings {
			if !remoteBindingPath(binding.Path) || !standaloneID.MatchString(binding.ArtifactID) || byID[binding.ArtifactID] || byPath[binding.Path].ID != "" {
				return nil, errors.New("RUNNER_INVALID")
			}
			item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", binding.ArtifactID))
			if err != nil || item.Status != "finalized" || item.TaskID != input.TaskID || item.AttemptID != input.AttemptID || item.Candidate != input.CandidateID {
				return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
			}
			var owner, recordedRunner, recordedAttempt, tokenHash string
			var recordedFence int
			if err = tx.Row("SELECT u.owner_id,l.runner_id,l.attempt_id,l.fence,l.token_hash FROM artifact_uploads u JOIN remote_artifact_leases l ON l.upload_id=u.id WHERE u.artifact_id=?", item.ID).Scan(&owner, &recordedRunner, &recordedAttempt, &recordedFence, &tokenHash); err != nil || owner != principal.UserID || recordedRunner != runnerID || recordedAttempt != input.AttemptID || recordedFence != input.Fence || tokenHash != store.Hash([]byte(input.Token)) {
				return nil, errors.New("STALE_TOKEN")
			}
			data, err := artifacts.read(item)
			if err != nil {
				return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
			}
			total += int64(len(data))
			if total > 64<<20 {
				return nil, errors.New("RUNNER_INVALID")
			}
			content[item.ID], byID[item.ID], byPath[binding.Path] = data, true, item
			if strings.HasSuffix(strings.ToLower(binding.Path), ".log") {
				env.DiagnosticArtifacts[item.ID] = true
			}
		}
		for _, id := range input.Result.Artifacts {
			if !byID[id] {
				return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
			}
		}
		if input.Result.Knowledge != nil {
			for _, id := range input.Result.Knowledge.Artifacts {
				if !byID[id] {
					return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
				}
			}
		}
		for _, binding := range input.Bindings {
			used := slices.Contains(input.Result.Artifacts, binding.ArtifactID)
			if input.Result.Knowledge != nil {
				used = used || slices.Contains(input.Result.Knowledge.Artifacts, binding.ArtifactID)
			}
			if !used {
				return nil, errors.New("RUNNER_INVALID")
			}
		}
		if task.Step == "execute" && input.Result.Status == "pass" {
			matched, report := false, false
			for name, item := range byPath {
				if strings.HasPrefix(name, "runtime:") {
					if item.MIME == "text/plain" && slices.Contains(input.Result.Artifacts, item.ID) {
						report = true
					}
					continue
				}
				file, exists := env.Files[name]
				if !exists {
					return nil, core.Error("CANDIDATE_CHANGED", "binding path is absent from candidate: "+name)
				}
				if file.Hash != item.SHA256 {
					return nil, core.Error("CANDIDATE_CHANGED", fmt.Sprintf("binding %s expected sha256=%s received=%s", name, file.Hash, item.SHA256))
				}
				if slices.Contains(input.Result.Artifacts, item.ID) {
					matched = true
				}
			}
			if task.Definition.Intent == "inspect" {
				if !report {
					return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
				}
			} else if !matched {
				return nil, core.Error("ARTIFACT_REQUIRED", "execute pass needs actual candidate file bytes; use runner submit --lease-file CLAIM --repository REPO with result.artifacts containing repository paths")
			}
		}
		if task.Step == "verify" {
			for name, item := range byPath {
				if !strings.HasPrefix(name, "runtime:") {
					if file, ok := env.Files[name]; !ok || file.Hash != item.SHA256 {
						return nil, errors.New("CANDIDATE_CHANGED")
					}
				}
			}
		}
		if p.requestDigest != "" {
			if _, err = tx.Exec("INSERT INTO remote_submission_digests(submission_key,digest) VALUES(?,?)", key, p.requestDigest); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec("INSERT INTO remote_runner_result_ops(submission_key,actor,runner_id,operation_id) VALUES(?,?,?,?)", key, principal.UserID, runnerID, input.OperationID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE remote_runner_attempts SET state='finished' WHERE attempt_id=? AND state='running'", input.AttemptID); err != nil {
			return nil, err
		}
		if task.Step != "verify" {
			if input.Method != "" || input.ExitCode != nil || len(input.Inputs) != 0 {
				return nil, errors.New("RUNNER_INVALID")
			}
			return nil, nil
		}
		if !strings.HasPrefix(attempt.Unit, "check:") || input.CandidateID != attempt.Input || input.CandidateID != task.Candidate {
			return nil, errors.New("CANDIDATE_CHANGED")
		}
		checkID := strings.TrimPrefix(attempt.Unit, "check:")
		var check core.Check
		for _, item := range task.Definition.Checks {
			if item.ID == checkID {
				check = item
				break
			}
		}
		if check.ID == "" {
			return nil, errors.New("CHECK_NOT_FOUND")
		}
		expectedMethod := core.EffectiveCheckMethod(check)
		if len(check.Command) > 0 {
			if check.Method != "" && check.Method != "command" {
				return nil, errors.New("CHECK_METHOD_MISMATCH")
			}
			expectedMethod = "command"
		}
		if input.Method != expectedMethod || !slices.Contains([]string{"command", "model", "scenario"}, input.Method) {
			return nil, errors.New("CHECK_METHOD_MISMATCH")
		}
		if input.Method == "command" && input.Result.Status != "blocked" {
			if input.ExitCode == nil || (input.Result.Status == "pass" && *input.ExitCode != 0) || (input.Result.Status == "fail" && *input.ExitCode == 0) {
				return nil, errors.New("CHECK_RESULT_INVALID")
			}
		} else if input.Method != "command" && input.ExitCode != nil {
			return nil, errors.New("CHECK_RESULT_INVALID")
		}
		if input.Result.Status != "blocked" && len(input.Result.Artifacts) == 0 {
			return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
		}
		if (input.Method == "model" || input.Method == "scenario") && input.Result.Status != "blocked" && len(input.Inputs) == 0 {
			return nil, errors.New("ASSESSMENT_EVIDENCE_REQUIRED")
		}
		hashes := map[string]string{}
		for _, name := range input.Inputs {
			file, ok := env.Files[name]
			if !remoteBindingPath(name) || strings.HasPrefix(name, "runtime:") || !ok {
				return nil, errors.New("ASSESSMENT_EVIDENCE_REQUIRED")
			}
			hashes[name] = file.Hash
		}
		for _, name := range check.Artifacts {
			item, ok := byPath[name]
			if (!ok || !slices.Contains(input.Result.Artifacts, item.ID)) && input.Result.Status != "blocked" {
				return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
			}
		}
		image := false
		for _, item := range byPath {
			hashes[item.ID] = item.SHA256
			if item.MIME == "image/png" || item.MIME == "image/jpeg" {
				image = true
			}
		}
		if check.Kind == "ui" && input.Result.Status != "blocked" && !image {
			return nil, errors.New("SCREENSHOT_REQUIRED")
		}
		if check.Kind == "ux" && input.Result.Status != "blocked" && len(input.Result.Artifacts) == 0 {
			return nil, core.Error("ARTIFACT_REQUIRED", "all result artifacts must be finalized, bound, and uploaded for this task, attempt and candidate")
		}
		if input.Result.Status != "blocked" {
			item, ok := byPath["runtime:report.json"]
			var report struct {
				Candidate          string   `json:"candidate"`
				Check              string   `json:"check"`
				DefinitionRevision int      `json:"definition_revision"`
				PlanRevision       int      `json:"plan_revision"`
				Method             string   `json:"method"`
				Source             string   `json:"source"`
				Observations       string   `json:"observations"`
				Covers             []string `json:"covers"`
			}
			if !ok || !slices.Contains(input.Result.Artifacts, item.ID) || json.Unmarshal(content[item.ID], &report) != nil || report.Candidate != input.CandidateID || report.Check != check.ID || report.DefinitionRevision != task.Definition.Revision || report.PlanRevision != task.Plan.Revision || report.Method != input.Method || report.Source != "external-"+input.Method+":"+runnerID || strings.TrimSpace(report.Observations) == "" {
				return nil, errors.New("ASSESSMENT_EVIDENCE_REQUIRED")
			}
			for _, cover := range check.Covers {
				if !slices.Contains(report.Covers, cover) {
					return nil, errors.New("ASSESSMENT_EVIDENCE_REQUIRED")
				}
			}
		}
		config, _ := json.Marshal(check)
		source := "remote-command:" + runnerID
		if input.Method != "command" {
			source = input.Method + ":" + runnerID
		}
		diagnostic := []string{}
		for _, id := range input.Result.Artifacts {
			if env.DiagnosticArtifacts[id] {
				diagnostic = append(diagnostic, id)
			}
		}
		return &core.Evidence{Check: check.ID, Candidate: input.CandidateID, Revision: task.Definition.Revision, PlanRevision: task.Plan.Revision, Config: store.Hash(config), Result: input.Result.Status, Source: source, Executor: runnerID, Inputs: slices.Clone(input.Inputs), Environment: "remote-runner", Artifacts: slices.Clone(input.Result.Artifacts), DiagnosticArtifacts: diagnostic, ArtifactHashes: hashes, Reason: input.Result.Summary, At: time.Now().UTC()}, nil
	}
	sub := core.Submission{Task: input.TaskID, Attempt: input.AttemptID, Token: input.Token, Key: key, Result: input.Result}
	next, err := p.service.SubmitRemote(sub, requestHash, env)
	if err != nil {
		return nil, err
	}
	if next.Task != nil {
		safe := core.RemoteTaskView(*next.Task)
		next.Task = &safe
	}
	next.Attempt = nil
	result := map[string]any{"next": next, "operation_id": input.OperationID}
	if p.requestDigest != "" {
		result["request_digest"] = p.requestDigest
	}
	return result, nil
}

func (p *portableStore) runnerResultReceipt(principal standalonePrincipal, runnerID, key string) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(key) {
		return nil, errors.New("RUNNER_INVALID")
	}
	var owner string
	if err := p.service.Store.DB.QueryRow("SELECT actor FROM remote_runners WHERE id=?", runnerID).Scan(&owner); err != nil || owner != principal.UserID {
		return nil, errors.New("RUNNER_NOT_FOUND")
	}
	submissionKey := runnerSubmissionKey(principal.UserID, runnerID, key)
	var operation, response string
	err := p.service.Store.DB.QueryRow("SELECT o.operation_id,s.response FROM remote_runner_result_ops o JOIN submissions s ON s.key=o.submission_key WHERE o.submission_key=? AND o.actor=? AND o.runner_id=?", submissionKey, principal.UserID, runnerID).Scan(&operation, &response)
	if err == sql.ErrNoRows {
		return map[string]any{"found": false}, nil
	}
	if err != nil {
		return nil, err
	}
	var next core.Next
	if err = json.Unmarshal([]byte(response), &next); err != nil {
		return nil, err
	}
	if next.Task != nil {
		safe := core.RemoteTaskView(*next.Task)
		next.Task = &safe
	}
	next.Attempt = nil
	result := map[string]any{"found": true, "operation_id": operation, "next": next}
	var digest string
	if err := p.service.Store.DB.QueryRow("SELECT digest FROM remote_submission_digests WHERE submission_key=?", submissionKey).Scan(&digest); err == nil {
		result["request_digest"] = digest
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	return result, nil
}

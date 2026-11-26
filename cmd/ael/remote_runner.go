package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/process"
	"ael/internal/remote"
	"ael/internal/store"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func runnerOperation(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(raw), nil
}

func runnerCall(config, project string, binding remote.Binding, method, path string, body any) (map[string]any, error) {
	return remote.Call(config, project, binding, method, path, body, nil)
}

// Remote managed execution is retired. The runner protocol now describes an
// externally operated Agent client; it never enters the historical loop.
func remoteRunnerCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	ownerPID := a.get("owner-pid", os.Getenv("AEL_RUNNER_OWNER_PID"))
	if ownerPID == "" || (cmd != "runner claim" && (cmd != "runner retry" || a.get("out", "") == "")) {
		return remoteAgentCommand(cmd, a, config, project, binding)
	}
	pid, err := strconv.Atoi(ownerPID)
	if err != nil {
		return nil, core.Error("OWNER_IDENTITY_REQUIRED", "provide a persistent owner PID")
	}
	identity, err := process.Identity(pid)
	if err != nil {
		return nil, err
	}
	value, err := remoteAgentCommand(cmd, a, config, project, binding)
	if err != nil {
		return nil, err
	}
	result, ok := value.(remoteOutput)
	if !ok || result["lease_file"] == nil {
		return value, nil
	}
	// The claim is already accepted and saved; a keeper problem must not turn
	// it into a failed command with empty stdout.
	keeper, err := launchRemoteKeeper(a, config, project, identity, ownerPID)
	if keeper > 0 {
		result["keeper_pid"] = keeper
	}
	if err != nil {
		keeperWarning(result, a.get("out", ""), ownerPID, err)
	}
	return result, nil
}

func verifyRegisteredRunner(cfg remoteRunnerConfig, config, project string, binding remote.Binding) error {
	result, err := runnerCall(config, project, binding, http.MethodGet, "/runners/"+url.PathEscape(cfg.RunnerID), nil)
	if err != nil {
		return err
	}
	if result["workspace_id"] != cfg.WorkspaceID || result["checkout_id"] != cfg.CheckoutID || result["repository"] != cfg.Repository || result["version"] != bootstrap.Version {
		return core.Error("RUNNER_REGISTRATION_MISMATCH", "local authorization differs from the registered runner")
	}
	var capabilities []string
	raw, err := json.Marshal(result["capabilities"])
	if err != nil || json.Unmarshal(raw, &capabilities) != nil {
		return core.Error("REMOTE_RESPONSE_INVALID", "invalid runner capabilities")
	}
	want := slices.Clone(cfg.Capabilities)
	slices.Sort(want)
	slices.Sort(capabilities)
	if !slices.Equal(want, capabilities) {
		return core.Error("RUNNER_REGISTRATION_MISMATCH", "runner capabilities changed")
	}
	return nil
}

func runnerTask(config, project string, binding remote.Binding, id string) (core.Next, string, error) {
	var next core.Next
	result, err := runnerCall(config, project, binding, http.MethodGet, "/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return next, "", err
	}
	if result["online"] != true || result["authoritative"] != true {
		return next, "", core.Error("REMOTE_UNAVAILABLE", "a live task authority is required")
	}
	revision, ok := result["revision"].(string)
	raw, marshalErr := json.Marshal(result)
	if !ok || marshalErr != nil || json.Unmarshal(raw, &next) != nil || next.Task == nil || next.Task.Definition.ID != id {
		return next, "", core.Error("REMOTE_RESPONSE_INVALID", "missing task or revision")
	}
	return next, revision, nil
}

func runnerContext(cfg remoteRunnerConfig, config, project, workspace string, binding remote.Binding, taskID, revision string) (string, remote.PortableCandidate, map[string]any, error) {
	var candidate remote.PortableCandidate
	candidate, err := remote.CapturePortableCandidate(workspace, cfg.Repository)
	if err != nil {
		return "", candidate, nil, err
	}
	key, err := runnerOperation("observe")
	if err != nil {
		return "", candidate, nil, err
	}
	payload := map[string]any{"task_id": taskID, "expected_revision": revision, "checkout_id": cfg.CheckoutID, "workspace_id": cfg.WorkspaceID, "runner_id": cfg.RunnerID, "inputs_known": candidate.InputsKnown, "candidate": candidate, "operation_id": key, "idempotency_key": key}
	observed, err := runnerCall(config, project, binding, http.MethodPost, "/candidates", payload)
	if err != nil {
		return "", candidate, nil, err
	}
	observation, ok := observed["id"].(string)
	if !ok || observed["candidate_id"] != candidate.ID {
		return "", candidate, nil, core.Error("REMOTE_RESPONSE_INVALID", "candidate observation missing")
	}
	key, err = runnerOperation("context")
	if err != nil {
		return "", candidate, nil, err
	}
	contextArgs := args{Flags: map[string]string{"task": taskID, "observation": observation, "expected-revision": revision, "operation-id": key, "idempotency-key": key}}
	value, err := remoteContextCommandAtWorkspace("context publish", contextArgs, config, project, workspace, binding)
	if err != nil {
		return "", candidate, nil, err
	}
	published, ok := value.(remoteOutput)
	if !ok {
		return "", candidate, nil, core.Error("REMOTE_RESPONSE_INVALID", "context receipt missing")
	}
	id, ok := published["id"].(string)
	if !ok || id == "" || published["inventory_unverified"] == true {
		return "", candidate, nil, core.Error("KNOWLEDGE_UNVERIFIED", "context has unclassified or missing source inventory")
	}
	value, err = remoteContextCommandAtWorkspace("context show", args{Flags: map[string]string{"context": id}}, config, project, workspace, binding)
	if err != nil {
		return "", candidate, nil, err
	}
	read, ok := value.(remoteOutput)
	if !ok || read["workspace_matches"] != true || read["validity"] != "current" {
		return "", candidate, nil, core.Error("CONTEXT_STALE", "context no longer matches the local workspace")
	}
	return id, candidate, map[string]any(read), nil
}

func runnerClaim(cfg remoteRunnerConfig, config, project string, binding remote.Binding, taskID, parentID, contextID, revision string) (core.Task, core.Attempt, string, error) {
	var task core.Task
	var attempt core.Attempt
	key, err := runnerOperation("claim")
	if err != nil {
		return task, attempt, "", err
	}
	payload := map[string]any{"task_id": taskID, "parent_id": parentID, "context_id": contextID, "expected_revision": revision, "operation_id": key, "idempotency_key": key}
	claimsDir, err := runnerPrivateDir(cfg.RuntimeDir, "control", "claims")
	if err != nil {
		return task, attempt, "", err
	}
	claimPath := filepath.Join(claimsDir, "claim-"+key+".json")
	raw, err := json.Marshal(map[string]any{"request": payload})
	if err != nil {
		return task, attempt, "", err
	}
	f, err := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return task, attempt, "", err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return task, attempt, claimPath, err
	}
	result, err := runnerCall(config, project, binding, http.MethodPost, "/runners/"+url.PathEscape(cfg.RunnerID)+"/claim", payload)
	if err != nil && strings.Contains(err.Error(), "REMOTE_UNAVAILABLE") {
		lookup, queryErr := runnerCall(config, project, binding, http.MethodGet, "/portable-operations/"+url.PathEscape(key), nil)
		if queryErr == nil && lookup["found"] == true {
			result, _ = lookup["result"].(map[string]any)
			err = nil
		}
	}
	if err != nil {
		if !strings.Contains(err.Error(), "REMOTE_UNAVAILABLE") && !strings.Contains(err.Error(), "PROJECTION_BLOCKED") {
			_ = os.Remove(claimPath)
			return task, attempt, "", err
		}
		return task, attempt, claimPath, core.Error("RUNNER_CLAIM_PENDING", "claim request retained at "+claimPath+"; query its original operation key before retrying")
	}
	if result == nil {
		return task, attempt, claimPath, core.Error("REMOTE_RESPONSE_INVALID", "claim receipt missing")
	}
	taskRaw, taskErr := json.Marshal(result["task"])
	attemptRaw, attemptErr := json.Marshal(result["attempt"])
	if taskErr != nil || attemptErr != nil || json.Unmarshal(taskRaw, &task) != nil || json.Unmarshal(attemptRaw, &attempt) != nil || task.Definition.ID != taskID || attempt.ID == "" || attempt.Token == "" || attempt.Fence < 1 {
		return task, attempt, claimPath, core.Error("REMOTE_RESPONSE_INVALID", "claim receipt invalid")
	}
	receipt, err := json.Marshal(map[string]any{"request": payload, "receipt": result})
	if err != nil {
		return task, attempt, claimPath, err
	}
	if err = store.Atomic(claimPath, receipt, 0600); err != nil {
		return task, attempt, claimPath, err
	}
	return task, attempt, claimPath, nil
}

func runnerNextTask(cfg remoteRunnerConfig, config, project string, binding remote.Binding, parent string) (core.WorkResult, error) {
	var result core.WorkResult
	response, err := runnerCall(config, project, binding, http.MethodGet, "/runners/"+url.PathEscape(cfg.RunnerID)+"/next?parent="+url.QueryEscape(parent), nil)
	if err != nil {
		return result, err
	}
	raw, err := json.Marshal(response)
	if err != nil || json.Unmarshal(raw, &result) != nil {
		return result, core.Error("REMOTE_RESPONSE_INVALID", "invalid parent work choice")
	}
	return result, nil
}

func runnerStop(action, detail string) map[string]any {
	return map[string]any{"mode": "remote", "action": action, "stop_reason": action, "detail": detail}
}

func runnerProblem(err error, stage string) error { return fmt.Errorf("%s: %w", stage, err) }

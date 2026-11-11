package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func readRunnerPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "pending files must be private and unlinked")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "pending file is unreadable or too large")
	}
	return raw, nil
}

func resumeRunnerResult(cfg remoteRunnerConfig, config, project string, binding remote.Binding, pendingPath string) (any, error) {
	if !filepath.IsAbs(pendingPath) {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "provide an absolute pending-submission path")
	}
	info, err := os.Lstat(pendingPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "pending submission must be a regular file")
	}
	real, err := filepath.EvalSymlinks(pendingPath)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(cfg.RuntimeDir, real)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || len(parts) != 3 || parts[0] != "attempts" || !remoteRunnerID.MatchString(parts[1]) || parts[2] != "pending-submission.json" {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "pending submission is outside this runner runtime")
	}
	raw, err := readRunnerPrivateFile(real)
	if err != nil {
		return nil, err
	}
	leaseBytes, err := readRunnerPrivateFile(filepath.Join(filepath.Dir(real), "lease.json"))
	if err != nil {
		return nil, err
	}
	var lease remoteRunnerLease
	if json.Unmarshal(leaseBytes, &lease) != nil || lease.RunnerID != cfg.RunnerID || lease.AttemptID != parts[1] {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "lease does not match this runner")
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil || payload == nil {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "invalid pending result")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "trailing pending data")
	}
	fence, ok := payload["fence"].(json.Number)
	if !ok {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "missing fence")
	}
	fenceValue, err := fence.Int64()
	key, keyOK := payload["idempotency_key"].(string)
	op, opOK := payload["operation_id"].(string)
	if err != nil || int(fenceValue) != lease.Fence || !keyOK || !opOK || !remoteRunnerID.MatchString(key) || op != key || payload["task_id"] != lease.TaskID || payload["attempt_id"] != lease.AttemptID || payload["token"] != lease.Token || payload["expected_revision"] != lease.ClaimRevision {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "pending result and retained lease disagree")
	}
	lookup, err := runnerCall(config, project, binding, http.MethodGet, "/runners/"+url.PathEscape(cfg.RunnerID)+"/operations/"+url.PathEscape(key), nil)
	if err != nil {
		return nil, err
	}
	receipt := lookup
	if lookup["found"] == true {
		if lookup["operation_id"] != op {
			return nil, core.Error("IDEMPOTENCY_CONFLICT", "stored operation does not match")
		}
	} else {
		receipt, err = runnerCall(config, project, binding, http.MethodPost, "/runners/"+url.PathEscape(cfg.RunnerID)+"/submit", payload)
		if err != nil {
			return nil, core.Error("RUNNER_RESULT_PENDING", "result remains in the private attempt directory; retry the same pending file")
		}
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	if err = store.Atomic(filepath.Join(filepath.Dir(real), "receipt.json"), encoded, 0600); err != nil {
		return nil, err
	}
	if err = os.Remove(real); err != nil {
		return nil, err
	}
	if err = os.Remove(filepath.Join(filepath.Dir(real), "lease.json")); err != nil {
		return nil, err
	}
	return remoteOutput(receipt), nil
}

func recoverRunnerClaim(cfg remoteRunnerConfig, config, project string, binding remote.Binding, claimPath string) (any, error) {
	if !filepath.IsAbs(claimPath) {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "provide an absolute retained claim path")
	}
	info, err := os.Lstat(claimPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "claim must be a regular file")
	}
	real, err := filepath.EvalSymlinks(claimPath)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(cfg.RuntimeDir, real)
	if err != nil || strings.Contains(filepath.ToSlash(rel), "/") || !strings.HasPrefix(rel, "claim-claim-") || !strings.HasSuffix(rel, ".json") {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "claim is outside this runner runtime")
	}
	raw, err := readRunnerPrivateFile(real)
	if err != nil {
		return nil, err
	}
	var state struct {
		Request map[string]any `json:"request"`
	}
	if json.Unmarshal(raw, &state) != nil || state.Request == nil {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "invalid retained claim")
	}
	key, keyOK := state.Request["idempotency_key"].(string)
	op, opOK := state.Request["operation_id"].(string)
	taskID, taskOK := state.Request["task_id"].(string)
	contextID, contextOK := state.Request["context_id"].(string)
	revision, revOK := state.Request["expected_revision"].(string)
	if !keyOK || !opOK || !taskOK || !contextOK || !revOK || key != op || !remoteRunnerID.MatchString(key) {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "retained claim fields are invalid")
	}
	lookup, err := runnerCall(config, project, binding, http.MethodGet, "/portable-operations/"+url.PathEscape(key), nil)
	if err != nil {
		return nil, err
	}
	if lookup["found"] != true {
		return map[string]any{"mode": "remote", "action": "not_found", "detail": "no claim receipt exists; no process was started"}, nil
	}
	receipt, ok := lookup["result"].(map[string]any)
	if !ok || receipt["operation_id"] != op || receipt["context_id"] != contextID {
		return nil, core.Error("IDEMPOTENCY_CONFLICT", "claim receipt does not match the retained request")
	}
	var task core.Task
	var attempt core.Attempt
	taskRaw, _ := json.Marshal(receipt["task"])
	attemptRaw, _ := json.Marshal(receipt["attempt"])
	if json.Unmarshal(taskRaw, &task) != nil || json.Unmarshal(attemptRaw, &attempt) != nil || task.Definition.ID != taskID || attempt.ID == "" || attempt.Token == "" || attempt.Executor != cfg.RunnerID {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "claim receipt does not match this runner")
	}
	lease := remoteRunnerLease{TaskID: taskID, AttemptID: attempt.ID, Token: attempt.Token, Fence: attempt.Fence, ClaimRevision: revision, ContextID: contextID, RunnerID: cfg.RunnerID}
	dir, err := runnerAttemptDir(cfg, lease)
	if err != nil {
		return nil, err
	}
	if err = os.Remove(real); err != nil {
		return nil, err
	}
	return map[string]any{"mode": "remote", "action": "claim_recovered", "attempt_id": attempt.ID, "attempt_dir": dir, "detail": "no executor was started by recovery; inspect the retained attempt before continuing"}, nil
}

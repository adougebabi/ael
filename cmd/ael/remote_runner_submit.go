package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func runnerOutputFile(cfg remoteRunnerConfig, dir, ref string, candidate remote.PortableCandidate, secrets ...string) ([]byte, string, string, error) {
	root, rel := cfg.Workspace, ref
	runtimeFile := strings.HasPrefix(ref, "runtime:")
	if runtimeFile {
		root, rel = dir, strings.TrimPrefix(ref, "runtime:")
	}
	if rel == "" || filepath.IsAbs(rel) || strings.ContainsAny(rel, "\\\x00\r\n") || filepath.ToSlash(filepath.Clean(rel)) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, "", "", core.Error("ARTIFACT_INVALID", "artifact path must be relative to the authorized workspace or attempt")
	}
	file, err := store.SafePath(root, filepath.FromSlash(rel))
	if err != nil {
		return nil, "", "", core.Error("ARTIFACT_INVALID", "artifact path is unsafe")
	}
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", "", core.Error("ARTIFACT_MISSING", ref)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, "", "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, remoteArtifactLimit+1))
	_ = f.Close()
	if err != nil || len(data) == 0 || len(data) > remoteArtifactLimit {
		return nil, "", "", core.Error("ARTIFACT_INVALID", "artifact is empty, unreadable or too large")
	}
	if runtimeFile {
		data = redactRunnerText(data, secrets...)
	} else {
		found := false
		for _, item := range candidate.Files {
			if item.Path == rel && !item.Deleted && item.SHA256 == store.Hash(data) {
				found = true
				break
			}
		}
		if !found {
			return nil, "", "", core.Error("CANDIDATE_CHANGED", "deliverable changed or is outside the candidate: "+rel)
		}
		if !bytes.Equal(data, redactRunnerText(data, secrets...)) || bytes.Contains(data, []byte("-----BEGIN PRIVATE KEY-----")) || bytes.Contains(data, []byte("-----BEGIN OPENSSH PRIVATE KEY-----")) {
			return nil, "", "", core.Error("ARTIFACT_SENSITIVE", "explicit deliverable appears to contain credentials")
		}
	}
	mimeType, extension := "application/octet-stream", ".bin"
	detected := http.DetectContentType(data)
	switch {
	case detected == "image/png":
		mimeType, extension = "image/png", ".png"
	case detected == "image/jpeg":
		mimeType, extension = "image/jpeg", ".jpg"
	case detected == "video/mp4":
		mimeType, extension = "video/mp4", ".mp4"
	case strings.HasPrefix(detected, "text/plain"):
		mimeType, extension = "text/plain", ".txt"
	}
	return data, "runner-" + store.Hash([]byte(ref))[:16] + extension, mimeType, nil
}

func sanitizeRunnerResult(result core.StepResult, secrets ...string) core.StepResult {
	clean := func(value string) string { return string(redactRunnerText([]byte(value), secrets...)) }
	result.Summary, result.FailureClass = clean(result.Summary), clean(result.FailureClass)
	if result.Knowledge != nil {
		copy := *result.Knowledge
		copy.Reason = clean(copy.Reason)
		result.Knowledge = &copy
	}
	if result.Blocker != nil {
		copy := *result.Blocker
		copy.Category, copy.Reason, copy.Tried, copy.Recovery, copy.ResumeAt, copy.Unit, copy.Input, copy.Next = clean(copy.Category), clean(copy.Reason), clean(copy.Tried), clean(copy.Recovery), clean(copy.ResumeAt), clean(copy.Unit), clean(copy.Input), clean(copy.Next)
		for i := range copy.Scope {
			copy.Scope[i] = clean(copy.Scope[i])
		}
		result.Blocker = &copy
	}
	return result
}

func runnerSubmitResult(ctx context.Context, cfg remoteRunnerConfig, config, project string, binding remote.Binding, lease remoteRunnerLease, task core.Task, output remote.PortableCandidate, processResult remoteProcessResult, dir string, profileToken string) error {
	if ctx.Err() != nil {
		return core.Error("LEASE_LOST", "runner lost execution authority")
	}
	result := sanitizeRunnerResult(processResult.Result, profileToken, lease.Token)
	if strings.TrimSpace(result.Summary) == "" || (result.Status != "pass" && result.Status != "fail" && result.Status != "blocked") {
		result = core.StepResult{Status: "blocked", Summary: "local process did not produce a valid final result"}
	}
	if task.Step == "execute" && result.Status == "pass" && len(result.Artifacts) == 0 {
		result = core.StepResult{Status: "blocked", Summary: "execution result omitted a required deliverable"}
	}
	key, err := runnerOperation("output")
	if err != nil {
		return err
	}
	observed := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "token": lease.Token, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "candidate": output, "operation_id": key, "idempotency_key": key}
	if _, err = runnerCall(config, project, binding, http.MethodPost, "/runners/"+url.PathEscape(cfg.RunnerID)+"/observe", observed); err != nil {
		return runnerProblem(err, "record candidate")
	}
	refs := append([]string{}, result.Artifacts...)
	if result.Knowledge != nil {
		refs = append(refs, result.Knowledge.Artifacts...)
	}
	ids := map[string]string{}
	bindings := make([]map[string]string, 0, len(refs))
	for _, ref := range refs {
		if _, exists := ids[ref]; exists {
			continue
		}
		if ctx.Err() != nil {
			return core.Error("LEASE_LOST", "runner lost execution authority")
		}
		data, filename, mimeType, err := runnerOutputFile(cfg, dir, ref, output, profileToken, lease.Token)
		if err != nil {
			return runnerProblem(err, "read explicit artifact")
		}
		key, err := runnerOperation("artifact")
		if err != nil {
			return err
		}
		uploadArgs := args{Flags: map[string]string{"task": lease.TaskID, "attempt": lease.AttemptID, "candidate": output.ID, "expected-revision": lease.ClaimRevision, "source": "remote runner result", "mime": mimeType, "operation-id": key, "idempotency-key": key}}
		receipt, err := remoteUploadBytes(uploadArgs, config, project, binding, filename, data, &runnerUploadLease{RunnerID: cfg.RunnerID, Token: lease.Token, Fence: lease.Fence})
		if err != nil {
			return runnerProblem(err, "upload artifact")
		}
		artifactID, ok := receipt["id"].(string)
		if !ok || receipt["sha256"] != store.Hash(data) {
			return core.Error("REMOTE_RESPONSE_INVALID", "finalized artifact receipt does not match")
		}
		ids[ref] = artifactID
		bindings = append(bindings, map[string]string{"path": ref, "artifact_id": artifactID})
	}
	for i, ref := range result.Artifacts {
		result.Artifacts[i] = ids[ref]
	}
	if result.Knowledge != nil {
		for i, ref := range result.Knowledge.Artifacts {
			result.Knowledge.Artifacts[i] = ids[ref]
		}
	}
	key, err = runnerOperation("submit")
	if err != nil {
		return err
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "token": lease.Token, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "candidate_id": output.ID, "result": result, "method": processResult.Method, "inputs": processResult.Inputs, "bindings": bindings, "operation_id": key, "idempotency_key": key}
	if processResult.ExitCode != nil {
		payload["exit_code"] = *processResult.ExitCode
	}
	pending, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	pendingPath := filepath.Join(dir, "pending-submission.json")
	if err = store.Atomic(pendingPath, pending, 0600); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return core.Error("LEASE_LOST", "pending result retained in private attempt directory")
	}
	receipt, err := runnerCall(config, project, binding, http.MethodPost, "/runners/"+url.PathEscape(cfg.RunnerID)+"/submit", payload)
	if err != nil && strings.Contains(err.Error(), "REMOTE_UNAVAILABLE") {
		lookup, lookupErr := runnerCall(config, project, binding, http.MethodGet, "/runners/"+url.PathEscape(cfg.RunnerID)+"/operations/"+url.PathEscape(key), nil)
		if lookupErr == nil && lookup["found"] == true && lookup["operation_id"] == key {
			receipt, err = lookup, nil
		}
	}
	if err != nil {
		return core.Error("RUNNER_RESULT_PENDING", fmt.Sprintf("result retained at %s; query the same operation before retrying: %v", pendingPath, err))
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err = store.Atomic(filepath.Join(dir, "receipt.json"), receiptBytes, 0600); err != nil {
		return err
	}
	if err = os.Remove(pendingPath); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(dir, "lease.json")); err != nil {
		return err
	}
	return nil
}

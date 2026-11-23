package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
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
	return data, runnerArtifactName(ref, rel, mimeType, extension), mimeType, nil
}

// runnerArtifactName keeps the submitted file's own name so a reader can tell
// a report from a log or source file. The stored type still comes from the
// detected bytes; a name whose extension the Server does not accept for that
// type keeps the original name and gains the type's extension
// (recovery_test.go -> recovery_test.go.txt). Names the Server would reject
// fall back to the former content-addressed name. Download-only files keep their name.
func runnerArtifactName(ref, rel, mimeType, extension string) string {
	fallback := "runner-" + store.Hash([]byte(ref))[:16] + extension
	base := filepath.Base(filepath.FromSlash(rel))
	accepted := map[string][]string{"image/png": {".png"}, "image/jpeg": {".jpg", ".jpeg"}, "video/mp4": {".mp4"},
		"text/plain": {".txt", ".md", ".log", ".json", ".csv"}}[mimeType]
	name := base
	if mimeType != "application/octet-stream" && !slices.Contains(accepted, strings.ToLower(filepath.Ext(base))) {
		name = base + extension
	}
	if name == "" || len(name) > 180 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.HasPrefix(name, ".") || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fallback
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fallback
		}
	}
	return name
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

func runnerSubmitResult(ctx context.Context, cfg remoteRunnerConfig, config, project string, binding remote.Binding, lease remoteRunnerLease, task core.Task, output remote.PortableCandidate, processResult remoteProcessResult, paths runnerAttemptPaths, profileToken string, guard *runnerLeaseGuard, operationID string, chain *runnerWorkspaceChain) error {
	if guard == nil || ctx.Err() != nil {
		return core.Error("LEASE_LOST", "runner lost execution authority")
	}
	if err := guard.check(); err != nil {
		return err
	}
	plan, err := runnerBuildSubmissionPlan(cfg, lease, task, output, processResult, paths, profileToken, guard, operationID, chain)
	if err != nil {
		return err
	}
	if err = runnerEnsureSubmissionChain(cfg, project, binding, lease, plan, guard); err != nil {
		return err
	}
	receipt, err := runnerExecuteSubmissionPlan(cfg, config, project, binding, lease, plan, paths, guard)
	if err != nil {
		return err
	}
	return runnerFinishSubmission(cfg, project, binding, lease, paths, receipt)
}

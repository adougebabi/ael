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
	"strings"
	"unicode"
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
	filename := filepath.Base(rel)
	ext := strings.ToLower(filepath.Ext(filename))
	switch mimeType {
	case "text/plain":
		if ext != ".txt" && ext != ".md" && ext != ".log" && ext != ".json" && ext != ".csv" {
			mimeType = "application/octet-stream"
		}
	case "image/jpeg":
		if ext != ".jpg" && ext != ".jpeg" {
			filename += extension
		}
	case "image/png", "video/mp4":
		if ext != extension {
			filename += extension
		}
	}
	if len(filename) > 180 || strings.TrimSpace(filename) != filename || strings.HasPrefix(filename, ".") || strings.Contains(filename, "..") || strings.ContainsFunc(filename, unicode.IsControl) {
		filename = "runner-" + store.Hash([]byte(ref))[:16] + extension
	}
	return data, filename, mimeType, nil
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

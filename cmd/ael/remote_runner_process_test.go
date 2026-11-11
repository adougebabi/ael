package main

import (
	"ael/internal/core"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteRunnerRequiresFinalResultAndDoesNotExposeRemoteConfig(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	workspace, dir := t.TempDir(), t.TempDir()
	t.Setenv("AEL_REMOTE_CONFIG_DIR", "fixture-secret-config")
	code := `import json, os
assert "AEL_REMOTE_CONFIG_DIR" not in os.environ
assert os.environ["AEL_MANAGED_CHILD"] == "1"
with open(os.environ["AEL_RESULT"], "w") as f:
    json.dump({"status":"pass","summary":"fixture result","artifacts":["output.txt"]}, f)
`
	cfg := remoteRunnerConfig{Workspace: workspace}
	cfg.Executor.Command = []string{python, "-c", code}
	task := core.Task{Definition: core.Definition{ID: "task"}, Step: "execute"}
	attempt := core.Attempt{ID: "attempt", Unit: "execute"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, dir)
	if err != nil || result.Result.Status != "pass" || result.Result.Summary != "fixture result" {
		t.Fatalf("valid process: %+v %v", result, err)
	}
	cfg.Executor.Command = []string{python, "-c", "print('stdout is not a result')"}
	result, err = runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, t.TempDir())
	if err != nil || result.Result.Status != "blocked" || !strings.Contains(result.Result.Summary, "without a final result") {
		t.Fatalf("zero exit without result was accepted: %+v %v", result, err)
	}
}

func TestRemoteRunnerRejectsUnauthorizedCheckAndRedactsItsLog(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "unauthorized")
	command := []string{python, "-c", "from pathlib import Path; Path('unauthorized').write_text('bad')"}
	check := core.Check{ID: "unit", Kind: "functional", Method: "command", Command: command}
	task := core.Task{Definition: core.Definition{ID: "task", Checks: []core.Check{check}}, Step: "verify"}
	attempt := core.Attempt{ID: "attempt", Unit: "check:unit"}
	cfg := remoteRunnerConfig{Workspace: workspace}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, t.TempDir())
	if err != nil || result.Result.Status != "blocked" {
		t.Fatalf("unauthorized check did not block: %+v %v", result, err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unauthorized command executed: %v", err)
	}
	secret := "fixture-token-123456789"
	check.Command = []string{python, "-c", "print('" + secret + "')"}
	task.Definition.Checks[0] = check
	cfg.Checks = []remoteApprovedCheck{{ID: check.ID, Command: check.Command}}
	dir := t.TempDir()
	result, err = runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, dir, secret)
	if err != nil || result.Result.Status != "pass" || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("authorized check failed: %+v %v", result, err)
	}
	log, err := os.ReadFile(filepath.Join(dir, "check.log"))
	if err != nil || strings.Contains(string(log), secret) || !strings.Contains(string(log), "[REDACTED]") {
		t.Fatalf("check log was not redacted: %q %v", log, err)
	}
}

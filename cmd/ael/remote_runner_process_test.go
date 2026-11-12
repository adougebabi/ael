package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	result, err := runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, dir, t.TempDir())
	if err != nil || result.Result.Status != "pass" || result.Result.Summary != "fixture result" {
		t.Fatalf("valid process: %+v %v", result, err)
	}
	cfg.Executor.Command = []string{python, "-c", "print('stdout is not a result')"}
	result, err = runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, t.TempDir(), t.TempDir())
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
	result, err := runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, t.TempDir(), t.TempDir())
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
	result, err = runRemoteProcess(ctx, cfg, task, attempt, map[string]string{"task": "fixture"}, dir, t.TempDir(), secret)
	if err != nil || result.Result.Status != "pass" || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("authorized check failed: %+v %v", result, err)
	}
	log, err := os.ReadFile(filepath.Join(dir, "check.log"))
	if err != nil || strings.Contains(string(log), secret) || !strings.Contains(string(log), "[REDACTED]") {
		t.Fatalf("check log was not redacted: %q %v", log, err)
	}
	processLog, err := os.ReadFile(filepath.Join(dir, "process.log"))
	if err != nil || strings.Contains(string(processLog), secret) || !strings.Contains(string(processLog), "[REDACTED]") {
		t.Fatalf("process log was not redacted: %q %v", processLog, err)
	}
}

func TestRunnerTruncatedLogDoesNotPersistPartialSecret(t *testing.T) {
	secret := "fixture-long-secret-123456789"
	buffer := runnerLogBuffer{secrets: []string{secret}}
	_, _ = buffer.Write([]byte(strings.Repeat("x", (1<<20)-4) + secret))
	redacted, err := buffer.finish()
	if err != nil {
		t.Fatal(err)
	}
	log := string(redacted)
	if strings.Contains(log, secret[:4]) || !strings.Contains(log, "[truncated]") || len(log) < 1<<19 {
		t.Fatalf("truncated output lost its safe prefix or retained a partial secret (length %d)", len(log))
	}
}

func TestRunnerLogRedactsCredentialsSplitAcrossLines(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	buffer := runnerLogBuffer{log: file}
	_, _ = buffer.Write([]byte("password:\n"))
	before, err := os.ReadFile(file.Name())
	if err != nil || len(before) != 0 {
		t.Fatalf("unfinished credential key was written early: %q %v", before, err)
	}
	_, _ = buffer.Write([]byte("synthetic-password-123456\nAuthorization: Bearer\nsynthetic-bearer-123456\n"))
	data, err := buffer.finish()
	if err != nil || strings.Contains(string(data), "synthetic-password-123456") || strings.Contains(string(data), "synthetic-bearer-123456") || strings.Count(string(data), "[REDACTED]") != 2 {
		t.Fatalf("credentials split across lines were not redacted: %q %v", data, err)
	}
}

func TestRunnerArtifactsProtectShortKnownCredentials(t *testing.T) {
	for _, secret := range []string{"abc1234", "ACT"} {
		t.Run(secret, func(t *testing.T) {
			workspace, dir := t.TempDir(), t.TempDir()
			data := []byte("token=" + secret + "\n")
			if err := os.WriteFile(filepath.Join(workspace, "output.txt"), data, 0600); err != nil {
				t.Fatal(err)
			}
			candidate := remote.PortableCandidate{Files: []remote.PortableFile{{Path: "output.txt", SHA256: store.Hash(data)}}}
			cfg := remoteRunnerConfig{Workspace: workspace}
			if _, _, _, err := runnerOutputFile(cfg, dir, "output.txt", candidate, secret); err == nil {
				t.Fatal("short known credential was accepted in a workspace artifact")
			}
			if err := os.WriteFile(filepath.Join(dir, "log.txt"), data, 0600); err != nil {
				t.Fatal(err)
			}
			redacted, _, _, err := runnerOutputFile(cfg, dir, "runtime:log.txt", candidate, secret)
			if err != nil || strings.Contains(string(redacted), secret) || bytes.Equal(redacted, data) {
				t.Fatalf("short known credential was not redacted from runtime artifact: %q %v", redacted, err)
			}
			if secret == "abc1234" && !strings.Contains(string(redacted), "[REDACTED]") {
				t.Fatalf("ordinary redaction marker is missing: %q", redacted)
			}
		})
	}
}

func TestRunnerArtifactsProtectJSONEscapedCredentials(t *testing.T) {
	for _, item := range []struct{ secret, content string }{
		{"a&z", `{"value":"a\u0026z"}`},
		{"ael_fixture", `{"value":"\u0061el_fixture"}`},
	} {
		t.Run(item.secret, func(t *testing.T) {
			workspace, dir := t.TempDir(), t.TempDir()
			data := []byte(item.content)
			if err := os.WriteFile(filepath.Join(workspace, "output.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			candidate := remote.PortableCandidate{Files: []remote.PortableFile{{Path: "output.json", SHA256: store.Hash(data)}}}
			cfg := remoteRunnerConfig{Workspace: workspace}
			if _, _, _, err := runnerOutputFile(cfg, dir, "output.json", candidate, item.secret); err == nil {
				t.Fatal("JSON-escaped credential was accepted in a workspace artifact")
			}
			if err := os.WriteFile(filepath.Join(dir, "report.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			redacted, _, _, err := runnerOutputFile(cfg, dir, "runtime:report.json", candidate, item.secret)
			if err != nil {
				t.Fatal(err)
			}
			var decoded any
			if err = json.Unmarshal(redacted, &decoded); err != nil {
				t.Fatalf("redacted JSON is invalid: %q %v", redacted, err)
			}
			if sensitive, err := runnerMetadataContainsSecrets(decoded, item.secret); err != nil || sensitive {
				t.Fatalf("JSON-escaped credential remains after redaction: %q %v", redacted, err)
			}
		})
	}
}

func TestRunnerLogRedactsSecretsSplitAcrossWrites(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	secret := "fixture-split-secret-123456789"
	buffer := runnerLogBuffer{log: file, secrets: []string{secret}}
	_, _ = buffer.Write([]byte("ready\nvalue=" + secret[:10]))
	before, err := os.ReadFile(file.Name())
	if err != nil || string(before) != "ready\n" {
		t.Fatalf("partial line was written prematurely: %q %v", before, err)
	}
	_, _ = buffer.Write([]byte(secret[10:] + "\nAuthorization: Bearer synthetic-bearer-token\n"))
	data, err := buffer.finish()
	if err != nil || strings.Contains(string(data), secret) || strings.Contains(string(data), "synthetic-bearer-token") || strings.Count(string(data), "[REDACTED]") != 2 {
		t.Fatalf("split credentials were not redacted: %q %v", data, err)
	}
	stored, err := os.ReadFile(file.Name())
	if err != nil || string(stored) != string(data) {
		t.Fatalf("stored log differs from redacted output: %q %v", stored, err)
	}
}

func TestRemoteRunnerDetectsBackgroundChildBeforeItCanWrite(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	workspace := t.TempDir()
	cfg := remoteRunnerConfig{Workspace: workspace}
	cfg.Executor.Command = []string{python, "-c", `import json,os,subprocess,sys
subprocess.Popen([sys.executable,"-c","import pathlib,time; time.sleep(0.45); pathlib.Path('late').write_text('bad')"])
with open(os.environ["AEL_RESULT"],"w") as f: json.dump({"status":"pass","summary":"parent exited"},f)`}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runRemoteProcess(ctx, cfg, core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}, core.Attempt{ID: "attempt", Unit: "define"}, nil, t.TempDir(), t.TempDir())
	if err != nil || result.Result.Status != "blocked" || !strings.Contains(result.Result.Summary, "background") {
		t.Fatalf("background child was accepted: %+v %v", result, err)
	}
	time.Sleep(550 * time.Millisecond)
	if _, err = os.Stat(filepath.Join(workspace, "late")); !os.IsNotExist(err) {
		t.Fatalf("same-group background child wrote after the parent exited: %v", err)
	}
}

func TestRemoteRunnerPersistsRedactedCompleteLinesWhileRunning(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	cfg := remoteRunnerConfig{Workspace: t.TempDir()}
	cfg.Executor.Command = []string{python, "-c", `import json,os,time
print("Authorization: Bearer synthetic-token-123456789",flush=True)
time.sleep(1.5)
with open(os.environ["AEL_RESULT"],"w") as f: json.dump({"status":"pass","summary":"fixture"},f)`}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	controlDir := t.TempDir()
	go func() {
		result, err := runRemoteProcess(ctx, cfg, core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}, core.Attempt{ID: "attempt", Unit: "define"}, nil, dir, controlDir)
		if err == nil && result.Result.Status != "pass" {
			err = fmt.Errorf("unexpected process result: %+v", result)
		}
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	var raw []byte
	for time.Now().Before(deadline) {
		raw, _ = os.ReadFile(filepath.Join(dir, "process.log"))
		if len(raw) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(raw) == 0 || strings.Contains(string(raw), "synthetic-token-123456789") || !strings.Contains(string(raw), "[REDACTED]") {
		t.Errorf("live process log was missing or unredacted: %q", raw)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

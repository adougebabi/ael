package main

import (
	"ael/internal/core"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerSeparatesControlFromExecutorIO(t *testing.T) {
	cfg := remoteRunnerConfig{RuntimeDir: t.TempDir(), RunnerID: "runner"}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-fixture", Token: "fixture-lease-secret", Fence: 1, ClaimRevision: "revision", ContextID: "context", RunnerID: "runner"}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Control == paths.IO || strings.HasPrefix(paths.Control, paths.IO+string(filepath.Separator)) {
		t.Fatalf("control directory is inside executor I/O: %+v", paths)
	}
	if _, err := os.Stat(filepath.Join(paths.IO, "lease.json")); !os.IsNotExist(err) {
		t.Fatalf("lease was placed in executor I/O: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(paths.Control, "lease.json"))
	if err != nil || !strings.Contains(string(raw), lease.Token) {
		t.Fatalf("private lease was not retained: %v", err)
	}
	for _, dir := range []string{paths.Control, paths.IO} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("directory is not private: %s %v", dir, err)
		}
	}
}

func TestRunnerRefusesAnExistingLegacyAttempt(t *testing.T) {
	cfg := remoteRunnerConfig{RuntimeDir: t.TempDir(), RunnerID: "runner"}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-existing", Token: "fixture-lease-secret", Fence: 1, RunnerID: "runner"}
	legacy := filepath.Join(cfg.RuntimeDir, "attempts", lease.AttemptID)
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "lease.json"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runnerAttemptDirs(cfg, lease); err == nil || !strings.Contains(err.Error(), "RUNNER_RECOVERY_REQUIRED") {
		t.Fatalf("legacy attempt did not stop recovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.RuntimeDir, "control", "attempts", lease.AttemptID)); !os.IsNotExist(err) {
		t.Fatalf("a duplicate attempt was created: %v", err)
	}
}

func TestRunnerRecordsProcessIncarnationWithoutClaimingDescendantsStopped(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	workspace := t.TempDir()
	cfg := remoteRunnerConfig{RuntimeDir: t.TempDir(), Workspace: workspace, RunnerID: "runner"}
	cfg.Executor.Command = []string{python, "-c", `import json,os,time; time.sleep(0.2); json.dump({"status":"pass","summary":"fixture"},open(os.environ["AEL_RESULT"],"w"))`}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-process", Token: "fixture-lease-secret", Fence: 1, RunnerID: "runner"}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runRemoteProcess(ctx, cfg, core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}, core.Attempt{ID: lease.AttemptID, Unit: "define"}, nil, paths.IO, paths.Control)
	if err != nil || result.Result.Status != "pass" {
		t.Fatalf("fixture process: %+v %v", result, err)
	}
	raw, err := os.ReadFile(filepath.Join(paths.Control, "process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		PID                int    `json:"pid"`
		Identity           string `json:"identity"`
		IdentityStatus     string `json:"identity_status"`
		Phase              string `json:"phase"`
		DescendantsUnknown bool   `json:"descendants_unknown"`
	}
	if err = json.Unmarshal(raw, &recorded); err != nil || recorded.PID <= 1 || recorded.Identity == "" || recorded.IdentityStatus != "recorded" || recorded.Phase != "exited" || !recorded.DescendantsUnknown {
		t.Fatalf("incomplete process record: %+v %v", recorded, err)
	}
}

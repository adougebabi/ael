package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerWorkspaceChainAdvancesOnlyAfterMatchingReceipt(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "fixture", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := runnerAttemptDirs(cfg, remoteRunnerLease{TaskID: "task", AttemptID: "AT-fixture", Token: "fixture-token", Fence: 1, RunnerID: "runner"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(paths.IO, "snapshot")
	if _, err = runnerCreateWorkspace(source, snapshot, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(snapshot, "app.txt"), []byte("accepted change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := runnerWorkspaceFingerprint(snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", "AT-fixture", "submit-fixture", snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"operation_id": "wrong-operation", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: "AT-fixture", Executor: "runner", Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}}
	if err = chain.accept(receipt); err == nil {
		t.Fatal("mismatched receipt advanced the workspace")
	}
	if chain.Pending == nil || chain.CurrentRelative != "" {
		t.Fatal("mismatched receipt changed the retained state")
	}
	receipt["operation_id"] = "submit-fixture"
	if err = chain.accept(receipt); err != nil {
		t.Fatal(err)
	}
	if chain.Pending != nil || chain.CurrentCandidate != candidate.ID {
		t.Fatalf("accepted receipt did not advance the workspace: %+v", chain)
	}
	reopened, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil || reopened.CurrentCandidate != candidate.ID || reopened.CurrentRelative == "" {
		t.Fatalf("accepted workspace was not recoverable: %+v %v", reopened, err)
	}
	if _, err = reopened.currentWorkspace(); err != nil {
		t.Fatalf("accepted workspace could not be loaded: %v", err)
	}
	if err = runnerAcceptWorkspaceReceipt(cfg, source, binding, "AT-fixture", receipt); err != nil {
		t.Fatalf("accepted receipt could not be replayed idempotently: %v", err)
	}
	if err = os.WriteFile(filepath.Join(snapshot, "app.txt"), []byte("changed after receipt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.currentWorkspace(); err == nil {
		t.Fatal("changed accepted workspace was reused")
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "working\n" {
		t.Fatalf("recording a receipt changed the original checkout: %q %v", raw, err)
	}
}

func TestRunnerWorkspaceChainRecoversAcceptedReceipt(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-fixture", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(paths.IO, "snapshot")
	if _, err = runnerCreateWorkspace(source, snapshot, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(snapshot, "app.txt"), []byte("accepted result\n"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := runnerWorkspaceFingerprint(snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", lease.AttemptID, "submit-fixture", snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "operation_id": "submit-fixture", "idempotency_key": "submit-fixture"}
	raw, _ := json.Marshal(payload)
	pending := filepath.Join(paths.Control, "pending-submission.json")
	if err = os.WriteFile(pending, raw, 0600); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/meta" {
			_ = json.NewEncoder(w).Encode(map[string]any{"mode": "server", "protocol": 1, "capabilities": []string{"remote_runners"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: lease.AttemptID, Executor: cfg.RunnerID, Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}})
	})
	_, config, _, _ := runnerRecoveryFixture(t, handler)
	if _, err = resumeRunnerResult(cfg, config, source, binding, pending); err != nil {
		t.Fatal(err)
	}
	reopened, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil || reopened.Pending != nil || reopened.CurrentCandidate != candidate.ID {
		t.Fatalf("confirmed receipt was not recovered: %+v %v", reopened, err)
	}
	if _, err = os.Stat(pending); !os.IsNotExist(err) {
		t.Fatalf("recovered pending file remains: %v", err)
	}
}

func TestRunnerWorkspaceChainRejectsChangedPendingSnapshot(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "fixture", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := runnerAttemptDirs(cfg, remoteRunnerLease{TaskID: "task", AttemptID: "AT-fixture", Token: "fixture-token", Fence: 1, RunnerID: "runner"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", "AT-fixture", "submit-fixture", snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(snapshot, "app.txt"), []byte("changed after preparation\n"), 0644); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: "AT-fixture", Executor: "runner", Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}}
	if err = chain.accept(receipt); err == nil {
		t.Fatal("changed snapshot was accepted")
	}
	if chain.Pending == nil || chain.CurrentRelative != "" {
		t.Fatal("changed snapshot destroyed the pending record")
	}
}

func TestRunnerWorkspaceRuntimeAllowsOnlyOneLocalLoop(t *testing.T) {
	cfg := remoteRunnerConfig{RuntimeDir: t.TempDir()}
	if err := os.Chmod(cfg.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	unlock, err := runnerLockRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := runnerLockRuntime(cfg); err == nil {
		second()
		unlock()
		t.Fatal("a second local Runner acquired the same runtime")
	}
	unlock()
	unlock, err = runnerLockRuntime(cfg)
	if err != nil {
		t.Fatalf("runtime was not released: %v", err)
	}
	unlock()
}

func TestRunnerWorkspaceRuntimeLockSurvivesLockFileRemoval(t *testing.T) {
	cfg := remoteRunnerConfig{RuntimeDir: t.TempDir()}
	if err := os.Chmod(cfg.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	unlock, err := runnerLockRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = os.Remove(filepath.Join(cfg.RuntimeDir, "control", "runner.lock")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if second, err := runnerLockRuntime(cfg); err == nil {
		second()
		t.Fatal("removing the lock file allowed a second local Runner")
	}
}

func TestRunnerWorkspaceChainDoesNotRecreateMissingJournal(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "fixture", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(chain.path); err != nil {
		t.Fatal(err)
	}
	if _, err = runnerOpenWorkspaceChain(cfg, source, binding, "task", ""); err == nil {
		t.Fatal("missing journal was silently recreated from the original checkout")
	}
	if _, err = os.Stat(chain.path); !os.IsNotExist(err) {
		t.Fatalf("missing journal was recreated: %v", err)
	}
}

func TestRunnerWorkspaceChainRetainsPendingWhenReferenceIsMissing(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-missing", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", lease.AttemptID, "submit-fixture", snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(paths.Control, "workspace.json")); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "operation_id": "submit-fixture", "idempotency_key": "submit-fixture"}
	raw, _ := json.Marshal(payload)
	pending := filepath.Join(paths.Control, "pending-submission.json")
	if err = os.WriteFile(pending, raw, 0600); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: lease.AttemptID, Executor: cfg.RunnerID, Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}})
	})
	_, config, _, _ := runnerRecoveryFixture(t, handler)
	if _, err = resumeRunnerResult(cfg, config, source, binding, pending); err == nil {
		t.Fatal("missing new-format reference was treated as a legacy attempt")
	}
	for _, path := range []string{pending, filepath.Join(paths.Control, "lease.json")} {
		if _, err = os.Stat(path); err != nil {
			t.Fatalf("recovery state was removed: %s %v", path, err)
		}
	}
}

func TestRunnerWorkspaceChainRecoversSavedReceiptWithoutReplaying(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-saved", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", lease.AttemptID, "submit-fixture", snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(paths.Control, "workspace.json")); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: lease.AttemptID, Executor: cfg.RunnerID, Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}}
	raw, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(paths.Control, "receipt.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := runnerRecoverWorkspaceChain(cfg, "", source, binding, "task", "", chain)
	if err != nil || recovered.Pending != nil || recovered.CurrentCandidate != candidate.ID || recovered.CurrentRelative == "" {
		t.Fatalf("saved receipt did not recover the workspace: %+v %v", recovered, err)
	}
	if _, err = os.Stat(filepath.Join(paths.Control, "workspace.json")); err != nil {
		t.Fatalf("missing workspace reference was not reconstructed: %v", err)
	}
	if _, err = os.Stat(filepath.Join(paths.Control, "lease.json")); !os.IsNotExist(err) {
		t.Fatalf("confirmed retained lease was not removed: %v", err)
	}
}

func TestRunnerWorkspaceChainFinishesCleanupAfterPlanRemoval(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-cleanup", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	paths.Snapshot = filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, paths.Snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
	plan, err := runnerBuildSubmissionPlan(cfg, lease, core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}, candidate, remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture"}}, paths, "fixture-profile-token", guard, "submit-fixture", chain)
	if err != nil {
		t.Fatal(err)
	}
	if err = runnerEnsureSubmissionChain(cfg, source, binding, lease, plan, guard); err != nil {
		t.Fatal(err)
	}
	chain, err = runnerWorkspaceChainForScope(cfg, source, binding, "task", "", false)
	if err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: lease.AttemptID, Executor: cfg.RunnerID, Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}}
	raw, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(paths.Control, "receipt.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = chain.accept(receipt); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(paths.Control, "submission-plan.json")); err != nil {
		t.Fatal(err)
	}
	recovered, err := runnerRecoverWorkspaceChain(cfg, "", source, binding, "task", "", chain)
	if err != nil || recovered.Pending != nil || recovered.CurrentCandidate != candidate.ID {
		t.Fatalf("accepted cleanup did not recover: %+v %v", recovered, err)
	}
	for _, name := range []string{"submission-plan.sha256", "lease.json"} {
		if _, err = os.Stat(filepath.Join(paths.Control, name)); !os.IsNotExist(err) {
			t.Fatalf("confirmed cleanup retained %s: %v", name, err)
		}
	}
}

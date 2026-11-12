package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runnerDeliveryFixture(t *testing.T) (*runnerWorkspaceChain, string, string) {
	t.Helper()
	source, runtime := runnerWorkspaceFixture(t)
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "fixture", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := runnerAttemptDirs(cfg, remoteRunnerLease{TaskID: "task", AttemptID: "AT-delivery", Token: "fixture-token", Fence: 1, RunnerID: "runner"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(paths.IO, "snapshot")
	if _, err = runnerCreateWorkspace(source, snapshot, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(snapshot, "app.txt"), []byte("fixed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(snapshot, "app.txt"), 0600); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := runnerWorkspaceFingerprint(snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", "AT-delivery", "submit-fixture", snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: "AT-delivery", Executor: "runner", Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(paths.Control, "receipt.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = chain.accept(receipt); err != nil {
		t.Fatal(err)
	}
	return chain, source, snapshot
}

func TestRunnerDeliveryPreservesExistingGitState(t *testing.T) {
	chain, source, snapshot := runnerDeliveryFixture(t)
	beforeStage := runnerWorkspaceFixtureGit(t, source, "ls-files", "--stage")
	if err := runnerDeliverWorkspace(chain); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "fixed\n" {
		t.Fatalf("accepted change was not delivered: %q %v", raw, err)
	}
	if info, err := os.Stat(filepath.Join(source, "app.txt")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("delivered permissions differ: %v %v", info, err)
	}
	if afterStage := runnerWorkspaceFixtureGit(t, source, "ls-files", "--stage"); afterStage != beforeStage {
		t.Fatalf("delivery changed the original index: before=%q after=%q", beforeStage, afterStage)
	}
	if staged := runnerWorkspaceFixtureGit(t, source, "show", ":app.txt"); staged != "staged\n" {
		t.Fatalf("existing staged content was lost: %q", staged)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "new.txt")); err != nil || string(raw) != "untracked\n" {
		t.Fatalf("existing untracked file changed: %q %v", raw, err)
	}
	if raw, err := os.ReadFile(filepath.Join(snapshot, "app.txt")); err != nil || string(raw) != "fixed\n" {
		t.Fatalf("accepted snapshot changed: %q %v", raw, err)
	}
}

func TestRunnerDeliveryKeepsConcurrentUserEdit(t *testing.T) {
	chain, source, snapshot := runnerDeliveryFixture(t)
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("user edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runnerDeliverWorkspace(chain); err == nil || !strings.Contains(err.Error(), "RUNNER_WORKSPACE_CHANGED") {
		t.Fatalf("concurrent user edit was not reported: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "user edit\n" {
		t.Fatalf("concurrent user edit was overwritten: %q %v", raw, err)
	}
	if raw, err := os.ReadFile(filepath.Join(snapshot, "app.txt")); err != nil || string(raw) != "fixed\n" {
		t.Fatalf("accepted snapshot was not retained: %q %v", raw, err)
	}
}

func TestRunnerCompletedDeliversConfirmedWorkspace(t *testing.T) {
	chain, source, _ := runnerDeliveryFixture(t)
	result := runnerStopWithWorkspace(chain, "completed", "")
	if result["action"] != "completed" {
		t.Fatalf("confirmed workspace was not delivered: %+v", result)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "fixed\n" {
		t.Fatalf("completed result not present in authorized checkout: %q %v", raw, err)
	}
}

func TestRunnerCompletedRetainsConflictingWorkspace(t *testing.T) {
	chain, source, snapshot := runnerDeliveryFixture(t)
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("user edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := filepath.EvalSymlinks(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result := runnerStopWithWorkspace(chain, "completed", "")
	if result["action"] != "wait_workspace_delivery" || result["workspace"] != snapshot || result["delivery_error"] == nil {
		t.Fatalf("conflicting checkout was not reported: %+v", result)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "user edit\n" {
		t.Fatalf("user edit was overwritten: %q %v", raw, err)
	}
}

func TestRunnerDeliveryRequiresSavedReceipt(t *testing.T) {
	chain, source, _ := runnerDeliveryFixture(t)
	if err := os.Remove(filepath.Join(chain.runtime, "control", "attempts", chain.LastAttemptID, "receipt.json")); err != nil {
		t.Fatal(err)
	}
	if err := runnerDeliverWorkspace(chain); err == nil || !strings.Contains(err.Error(), "RUNNER_RECOVERY_REQUIRED") {
		t.Fatalf("missing receipt was not rejected: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "working\n" {
		t.Fatalf("unconfirmed output changed the original checkout: %q %v", raw, err)
	}
}

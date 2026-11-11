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

func TestRemoteRunnerRequiresExplicitPrivateWorkspaceAndCommandAuthorization(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "checkout")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "runner.json")
	cfg := remoteRunnerConfig{ProjectID: "shared", Repository: "repo", CheckoutID: "checkout", WorkspaceID: "workspace", RunnerID: "runner", Workspace: workspace, RuntimeDir: filepath.Join(root, "runtime"), Capabilities: []string{"development"}, Checks: []remoteApprovedCheck{{ID: "unit", Command: []string{"go", "test", "./..."}}}}
	cfg.Executor.Command = []string{"fixture-executor"}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	loaded, err := loadRemoteRunnerConfig(configPath, workspace, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !remoteCheckAuthorized(loaded, core.Check{ID: "unit", Command: []string{"go", "test", "./..."}}) || remoteCheckAuthorized(loaded, core.Check{ID: "unit", Command: []string{"git", "push"}}) {
		t.Fatal("check command authorization was not exact")
	}
	if _, err = loadRemoteRunnerConfig(configPath, root, binding); err == nil {
		t.Fatal("unapproved workspace accepted")
	}
	if err = os.Chmod(configPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = loadRemoteRunnerConfig(configPath, workspace, binding); err == nil {
		t.Fatal("public runner configuration accepted")
	}
}

func TestRemoteRunnerFailsClosedWithoutVerifiedIsolation(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "checkout")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "runner.json")
	cfg := remoteRunnerConfig{ProjectID: "shared", Repository: "repo", CheckoutID: "checkout", WorkspaceID: "workspace", RunnerID: "runner", Workspace: workspace, RuntimeDir: filepath.Join(root, "runtime"), Capabilities: []string{"development"}}
	cfg.Executor.Command = []string{"fixture-executor"}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := remoteRunnerCommand("runner run", args{Flags: map[string]string{"file": configPath, "task": "task"}}, filepath.Join(root, "config"), workspace, remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"})
	if err == nil || !strings.Contains(err.Error(), "RUNNER_ISOLATION_REQUIRED") {
		t.Fatalf("unverified execution was not refused: %v", err)
	}
}

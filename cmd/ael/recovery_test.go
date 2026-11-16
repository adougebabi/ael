package main

import (
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/remote"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryReconcilesAuthorityWithoutWrites(t *testing.T) {
	root, config := t.TempDir(), filepath.Join(t.TempDir(), "config")
	for _, argv := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "baseline"}} {
		command := exec.Command("git", argv...)
		command.Dir = root
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v %s", err, out)
		}
	}
	if _, err := knowledge.Index(root); err != nil {
		t.Fatal(err)
	}
	task := core.Task{Definition: core.Definition{ID: "leaf", Parent: "parent", Revision: 1, Scope: []string{"app.txt"}}, Plan: core.Plan{Revision: 1}, Status: "open", Step: "execute"}
	active, responsibility, available := true, "active", true
	contextID, contextCandidate := "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("recovery wrote %s", r.URL.Path)
			w.WriteHeader(405)
			return
		}
		if !available {
			w.WriteHeader(503)
			return
		}
		var response any
		switch r.URL.Path {
		case "/api/v1/meta":
			response = map[string]any{"mode": "server", "protocol": 1}
		case "/api/v1/projects/shared/runners/agent":
			assignments := []any{}
			if active {
				assignments = append(assignments, map[string]any{"task_id": "leaf", "attempt_id": "attempt", "responsibility_state": responsibility, "context_id": contextID})
			}
			response = map[string]any{"workspace_id": "workspace", "repository": "repository", "assignments": assignments}
		case "/api/v1/projects/shared/tasks/leaf":
			response = map[string]any{"task": task, "revision": "revision"}
		case "/api/v1/projects/shared/contexts/context":
			response = map[string]any{"validity": "current", "candidate_id": contextCandidate}
		case "/api/v1/projects/shared/tasks/parent/tree":
			response = core.EpicTree{Children: []core.EpicTreeMember{{Task: core.Next{Task: &core.Task{Definition: core.Definition{ID: "done"}, Status: "completed"}}}}}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	if err := remote.SaveProfile(config, root, "central", server.URL, "fixture-private-credential"); err != nil {
		t.Fatal(err)
	}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	options := args{Flags: map[string]string{"session": "session-a", "runner": "agent"}}
	call := func(command string) map[string]any {
		t.Helper()
		value, err := recoveryCommand(command, options, config, root, binding)
		if err != nil {
			t.Fatal(err)
		}
		return value.(map[string]any)
	}
	first := call("recovery save")
	if first["status"] != "active" {
		t.Fatalf("first %+v", first)
	}
	index := first["summary"].(recoveryIndex)
	if len(index.CompletedChildren) != 1 || index.CompletedChildren[0] != "done" || index.Task != "leaf" {
		t.Fatalf("lost partial parent %+v", index)
	}
	contextID, contextCandidate = "context", "different-candidate"
	if value := call("recovery check"); value["status"] != "workspace_changed" {
		t.Fatalf("foreign candidate accepted %+v", value)
	}
	contextID = ""
	task.Definition.Revision++
	if value := call("recovery check"); value["status"] != "authority_changed" {
		t.Fatalf("stale definition %+v", value)
	}
	responsibility = "expired"
	if value := call("recovery check"); value["status"] != "responsibility_requires_recovery" {
		t.Fatalf("expired responsibility %+v", value)
	}
	active = false
	task.Attempts = []core.Attempt{{ID: "other", Status: "running"}}
	if value := call("recovery check"); value["status"] != "authority_changed" {
		t.Fatalf("lost assignment %+v", value)
	}
	if value := call("recovery check"); value["status"] != "other_executor" {
		t.Fatalf("other executor %+v", value)
	}
	available = false
	if value := call("recovery check"); value["online_verified"] != false || value["status"] != "unverified" {
		t.Fatalf("offline %+v", value)
	}
	options.Flags["session"] = "session-b"
	if value := call("recovery check"); value["status"] != "no_checkpoint" {
		t.Fatalf("sessions mixed %+v", value)
	}
	available = true
	options.Flags["session"] = "session-a"
	task.Status = "completed"
	if value := call("recovery check"); value["status"] != "completed" || value["summary"].(recoveryIndex).Task != "" {
		t.Fatalf("completed pointer retained %+v", value)
	}
	if value := call("recovery check"); value["status"] != "no_current_task" {
		t.Fatalf("old task restarted %+v", value)
	}
	other := t.TempDir()
	pathA, _, _, err := recoveryLocation(options, config, root, binding)
	if err != nil {
		t.Fatal(err)
	}
	pathB, _, _, err := recoveryLocation(options, config, other, binding)
	if err != nil || pathA == pathB {
		t.Fatalf("checkouts mixed %s %s %v", pathA, pathB, err)
	}
}

func TestRecoveryDoesNotPersistCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	index := recoveryIndex{Schema: 1, Handoff: recoveryHandoff{Next: "fixture-secret", Tried: "lease_secret password=bad", Decisions: []string{"token=bad"}}}
	if err := saveRecovery(path, index, "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	for _, secret := range []string{"fixture-secret", "lease_secret", "password=bad", "token=bad"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("checkpoint leaked %s", secret)
		}
	}
}

func TestRecoveryHookInstallPreservesSettingsAndRejectsConflicts(t *testing.T) {
	root, bin := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(`#!/bin/sh
case "$1" in
  --help) echo include-hook-events ;;
  *) echo '2.1.154 (Claude Code)' ;;
esac
`), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".ael"), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".ael/remote.json"), []byte(`{"schema":1,"profile":"central","project_id":"shared"}`), 0644)
	os.MkdirAll(filepath.Join(root, ".claude"), 0755)
	path := filepath.Join(root, ".claude/settings.json")
	user := []byte(`{"permissions":{"allow":["Read"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo user-owned"}]}]}}`)
	os.WriteFile(path, user, 0600)
	options := args{Flags: map[string]string{"host": "claude", "project": root, "dry-run": "true"}}
	options.Flags["config-dir"] = filepath.Join(t.TempDir(), "custom-config")
	if _, err := recoveryHostCommand(options); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != string(user) {
		t.Fatal("dry-run modified settings")
	}
	delete(options.Flags, "dry-run")
	if _, err := recoveryHostCommand(options); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if !strings.Contains(string(first), options.Flags["config-dir"]) {
		t.Fatal("explicit configuration directory lost in hook")
	}
	if !strings.Contains(string(first), "user-owned") || !strings.Contains(string(first), "permissions") {
		t.Fatal("user settings lost")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("settings permissions changed")
	}
	if _, err := recoveryHostCommand(options); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(second) != string(first) {
		t.Fatal("repeated install changed settings")
	}
	os.WriteFile(path, []byte(strings.ReplaceAll(string(first), "timeout\": 5", "timeout\": 7")), 0600)
	if _, err := recoveryHostCommand(options); err == nil || !strings.Contains(err.Error(), "RECOVERY_HOOK_CONFLICT") {
		t.Fatalf("edited hook overwritten %v", err)
	}
}

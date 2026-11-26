package main

import (
	"ael/internal/remote"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCommunicationRejectsInvalidRequestsAndRetiredHosting(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "request.json")
	if err := os.WriteFile(file, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"register", "observe"} {
		_, err := remoteAgentCommand("runner "+action, args{Flags: map[string]string{"file": file, "runner": "agent", "repository": "repo", "operation-id": "op"}}, root, root, remote.Binding{})
		if err == nil || !strings.Contains(err.Error(), "AGENT_REQUEST_INVALID") {
			t.Fatalf("%s accepted null request: %v", action, err)
		}
	}
	if err := os.WriteFile(file, []byte(`{"token":"private"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := readAgentRequest(file, &request); err == nil {
		t.Fatal("public secret-bearing request accepted")
	}
	for _, action := range []string{"run", "resume-result", "recover-claim"} {
		_, err := remoteAgentCommand("runner "+action, args{Flags: map[string]string{}}, root, root, remote.Binding{})
		if err == nil || !strings.Contains(err.Error(), "REMOTE_MANAGED_UNSUPPORTED") {
			t.Fatalf("retired %s reached config or execution: %v", action, err)
		}
	}
}

func TestUncommittedExecuteCandidateWarns(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "app.txt")
	git("commit", "-m", "initial")
	execute := map[string]any{"attempt": map[string]any{"unit": "execute"}}
	check := map[string]any{"attempt": map[string]any{"unit": "check:tests"}}
	clean := map[string]any{}
	addUncommittedCandidateWarning(clean, root, execute)
	if _, warned := clean["warnings"]; warned {
		t.Fatalf("clean checkout warned: %v", clean)
	}
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("two\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dirty := map[string]any{}
	addUncommittedCandidateWarning(dirty, root, execute)
	warnings, _ := dirty["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "CANDIDATE_UNCOMMITTED") || !strings.Contains(warnings[0].(string), "app.txt") {
		t.Fatalf("dirty execute candidate did not warn: %v", dirty)
	}
	verify := map[string]any{}
	addUncommittedCandidateWarning(verify, root, check)
	if _, warned := verify["warnings"]; warned {
		t.Fatalf("check unit warned about the candidate it must not change: %v", verify)
	}
}

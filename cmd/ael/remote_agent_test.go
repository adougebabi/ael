package main

import (
	"ael/internal/remote"
	"os"
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

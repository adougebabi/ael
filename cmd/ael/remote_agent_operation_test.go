package main

import (
	"ael/internal/remote"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentOperationRejectsChangedRetainedPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	record := agentOperationRecord{Schema: 1, Action: "submit", Payload: map[string]any{"task_id": "task", "attempt_id": "old-attempt", "fence": 1, "idempotency_key": "same-key"}}
	if err := saveAgentOperation(path, record); err != nil {
		t.Fatal(err)
	}
	saved, err := readAgentOperation(path)
	if err != nil {
		t.Fatal(err)
	}
	saved.Payload["attempt_id"] = "new-attempt"
	raw, _ := json.Marshal(saved)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readAgentOperation(path); err == nil || !strings.Contains(err.Error(), "AGENT_OPERATION_INVALID") {
		t.Fatalf("modified retained request accepted: %v", err)
	}
}

func TestAgentOperationLockReleasesAfterOwnerExits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	unlock, err := lockAgentOperation(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := lockAgentOperation(path); err == nil {
		other()
		t.Fatal("concurrent operation owner accepted")
	}
	unlock()
	again, err := lockAgentOperation(path)
	if err != nil {
		t.Fatalf("retained lock file prevented recovery: %v", err)
	}
	again()
}

func TestAgentOperationPreservesTypedCandidateAcrossDiskRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observe.json")
	candidate := remote.PortableCandidate{Schema: 1, Repository: "repo", Commit: "commit", ID: "candidate", Files: []remote.PortableFile{{Path: "app.txt", SHA256: "hash"}}}
	record := agentOperationRecord{Schema: 1, Action: "observe", Payload: map[string]any{"candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}}
	if err := saveAgentOperation(path, record); err != nil {
		t.Fatal(err)
	}
	if _, err := readAgentOperation(path); err != nil {
		t.Fatalf("unchanged typed candidate failed disk checksum: %v", err)
	}
}

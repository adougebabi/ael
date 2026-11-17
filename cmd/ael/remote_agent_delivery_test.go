package main

import (
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentClaimReceiptIsDirectLeaseAndRetainsClaimRevision(t *testing.T) {
	receipt := map[string]any{"revision": "live-revision", "task": map[string]any{"definition": map[string]any{"id": "task"}}, "attempt": map[string]any{"id": "attempt", "token": "private-token", "fence": 2, "lease_until": "2030-01-01T00:00:00Z"}}
	agentClaimLease(receipt, "runner", "claim-revision")
	path := filepath.Join(t.TempDir(), "claim.json")
	raw, _ := json.Marshal(receipt)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	lease, saved, err := readAgentLease(path)
	if err != nil || lease.ExpectedRevision != "claim-revision" || lease.Token != "private-token" || lease.TaskID != "task" {
		t.Fatalf("claim receipt cannot be consumed: %+v %v", lease, err)
	}
	body := map[string]any{}
	if err := applyAgentLease(body, lease, "submit", "runner"); err != nil || body["expected_revision"] != "claim-revision" {
		t.Fatalf("submit lost claim revision: %v", err)
	}
	body["expected_revision"] = "live-revision"
	if err := applyAgentLease(body, lease, "submit", "runner"); err == nil || !strings.Contains(err.Error(), "expected_revision") {
		t.Fatalf("conflicting revision accepted: %v", err)
	}
	heartbeat := map[string]any{"attempt_id": "attempt", "fence": 2, "lease_until": "2030-01-02T00:00:00Z"}
	if err := updateAgentLease(path, saved, heartbeat); err != nil {
		t.Fatal(err)
	}
	updated, _, err := readAgentLease(path)
	if err != nil || !updated.LeaseUntil.After(lease.LeaseUntil) || updated.ExpectedRevision != lease.ExpectedRevision {
		t.Fatalf("heartbeat lost immutable claim or renewal: %+v %v", updated, err)
	}
	redactAgentSecrets(receipt)
	public, _ := json.Marshal(receipt)
	if strings.Contains(string(public), "private-token") {
		t.Fatal("public receipt exposed lease token")
	}
}

func TestAgentDeliveryJournalFreezesSelectedSourceBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivery.json")
	data := []byte("export const Page = () => null\n")
	plan := &agentDeliveryPlan{Candidate: remote.PortableCandidate{Schema: 1, Repository: "repo", ID: "candidate", Files: []remote.PortableFile{{Path: "src/Page.tsx", SHA256: store.Hash(data)}}}, Artifacts: []agentDeliveryArtifact{{Path: "src/Page.tsx", Filename: "source.bin", MIME: "application/octet-stream", Data: data}}}
	raw, _ := json.Marshal(plan)
	record := agentOperationRecord{Schema: 1, Action: "delivery", Payload: map[string]any{"idempotency_key": "delivery"}, Delivery: plan, DeliveryHash: store.Hash(raw)}
	if err := saveAgentOperation(path, record); err != nil {
		t.Fatal(err)
	}
	saved, err := readAgentOperation(path)
	if err != nil || string(saved.Delivery.Artifacts[0].Data) != string(data) {
		t.Fatalf("delivery snapshot failed: %v", err)
	}
	saved.Delivery.Artifacts[0].Data = []byte("changed\n")
	modified, _ := json.Marshal(saved)
	if err := os.WriteFile(path, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAgentOperation(path); err == nil {
		t.Fatal("modified delivery snapshot accepted")
	}
}

func TestAgentLegacyClaimReceiptRecoversOriginalRevision(t *testing.T) {
	dir := t.TempDir()
	operation := filepath.Join(dir, "original-claim-operation.json")
	receipt := map[string]any{"revision": "post-claim-live", "task": map[string]any{"definition": map[string]any{"id": "task"}}, "attempt": map[string]any{"id": "attempt", "token": "original-private-token", "fence": 1, "lease_until": "2030-01-01T00:00:00Z"}, "operation_file": operation}
	record := agentOperationRecord{Schema: 1, Action: "claim", Runner: "runner", Payload: map[string]any{"expected_revision": "original-claim-revision"}, Receipt: receipt}
	if err := saveAgentOperation(operation, record); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(receipt)
	path := filepath.Join(dir, "old-claim.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	lease, _, err := readAgentLease(path)
	if err != nil || lease.ExpectedRevision != "original-claim-revision" || lease.RunnerID != "runner" {
		t.Fatalf("legacy claim used live revision: %+v %v", lease, err)
	}
}

func TestAgentDeliveryRejectsUnrelatedSubmitWithSameKey(t *testing.T) {
	project, config := t.TempDir(), filepath.Join(t.TempDir(), "config")
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	if err := remote.SaveProfile(config, project, "central", "http://127.0.0.1:7332", "fixture-profile-token"); err != nil {
		t.Fatal(err)
	}
	path, old, err := agentOperationLocation(config, project, binding, "submit", "runner", "same-key")
	if err != nil {
		t.Fatal(err)
	}
	old.Payload = map[string]any{"idempotency_key": "same-key", "operation_id": "old-op"}
	if err := saveAgentOperation(path, old); err != nil {
		t.Fatal(err)
	}
	delivery := agentOperationRecord{Payload: map[string]any{"idempotency_key": "same-key"}, Runner: "runner", RequestHash: "new-request", DeliveryHash: "new-delivery", Delivery: &agentDeliveryPlan{}}
	if _, err := executeAgentDelivery(config, project, binding, "parent.json", delivery); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("unrelated submit replayed: %v", err)
	}
}

func TestAgentDeliveryProtectsCredentialsInMetadataAndArtifacts(t *testing.T) {
	project, runtime := runnerWorkspaceFixture(t)
	profileToken := "fixture-profile-control-token"
	attemptToken := "fixture-attempt-control-token"
	options := args{Flags: map[string]string{"repository": "repo", "artifacts-dir": runtime}}
	body := map[string]any{"token": attemptToken, "result": map[string]any{"status": "pass", "summary": "actual result", "knowledge": map[string]any{"reason": profileToken}}}
	if _, err := buildAgentDelivery(options, project, body, profileToken); err == nil || !strings.Contains(err.Error(), "ARTIFACT_SENSITIVE") {
		t.Fatalf("credential metadata accepted: %v", err)
	}
	result := body["result"].(map[string]any)
	delete(result, "knowledge")
	result["artifacts"] = []any{"runtime:report.txt"}
	if err := os.WriteFile(filepath.Join(runtime, "report.txt"), []byte("token="+profileToken+" lease="+attemptToken), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildAgentDelivery(options, project, body, profileToken)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plan.Artifacts[0].Data), profileToken) || strings.Contains(string(plan.Artifacts[0].Data), attemptToken) {
		t.Fatal("runtime artifact retained credentials")
	}
	if err := os.WriteFile(filepath.Join(project, "app.txt"), []byte(profileToken), 0644); err != nil {
		t.Fatal(err)
	}
	result["artifacts"] = []any{"app.txt"}
	if _, err := buildAgentDelivery(options, project, body, profileToken); err == nil || !strings.Contains(err.Error(), "ARTIFACT_SENSITIVE") {
		t.Fatalf("source with credential accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "app.txt"), []byte("actual source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "report-"+profileToken+".txt"), []byte("ordinary bytes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildAgentDelivery(options, project, body, profileToken); err == nil || !strings.Contains(err.Error(), "candidate metadata") {
		t.Fatalf("candidate path containing credential accepted: %v", err)
	}
}

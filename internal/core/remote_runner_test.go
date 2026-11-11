package core

import (
	"ael/internal/store"
	"testing"
	"time"
)

func TestRemoteFailureEvidenceDistinguishesReportsButNotLogs(t *testing.T) {
	data := map[string][]byte{"log-a": []byte("diagnostic one"), "log-b": []byte("diagnostic two"), "report-a": []byte("first failure"), "report-b": []byte("different failure")}
	read := func(ref string) ([]byte, error) { return data[ref], nil }
	diagnostic := map[string]bool{"log-a": true, "log-b": true}
	first := failureEvidenceWith(StepResult{Artifacts: []string{"log-a", "report-a"}}, nil, read, diagnostic)
	second := failureEvidenceWith(StepResult{Artifacts: []string{"log-b", "report-b"}}, nil, read, diagnostic)
	if len(first) != 1 || !first[store.Hash(data["report-a"])] || len(second) != 1 || !second[store.Hash(data["report-b"])] {
		t.Fatalf("failure evidence incorrectly classified: %+v %+v", first, second)
	}
	now := time.Now().UTC()
	task := Task{FailureCycles: map[string]FailureCycle{"check:unit": {Unit: "check:unit", Signature: failureSignature("check:unit", "first"), State: "counting", Count: 1, LastFailureAt: now, FailureEvidence: first}}}
	key, _ := failureKey(&task, "check:unit", "second", failureSignature("check:unit", "second"), second)
	if key == "check:unit" {
		t.Fatal("new evidence for a distinct failure was merged into the prior budget")
	}
	noNewReport := failureEvidenceWith(StepResult{Artifacts: []string{"log-b", "report-a"}}, nil, read, diagnostic)
	key, _ = failureKey(&task, "check:unit", "second", failureSignature("check:unit", "second"), noNewReport)
	if key != "check:unit" {
		t.Fatal("a changed diagnostic log reset the failure budget")
	}
}

func TestRemoteCheckProgressDoesNotTreatChangedDiagnosticLogsAsProgress(t *testing.T) {
	task := Task{}
	first := Evidence{Check: "unit", Candidate: "candidate", Config: "config", Result: "fail", Artifacts: []string{"log-a"}, DiagnosticArtifacts: []string{"log-a"}, ArtifactHashes: map[string]string{"log-a": "hash-a"}}
	second := Evidence{Check: "unit", Candidate: "candidate", Config: "config", Result: "fail", Artifacts: []string{"log-b"}, DiagnosticArtifacts: []string{"log-b"}, ArtifactHashes: map[string]string{"log-b": "hash-b"}}
	firstAt := time.Now().UTC()
	if !recordCheckProgress(&task, first, nil, firstAt) {
		t.Fatal("first check conclusion was not recorded")
	}
	if recordCheckProgress(&task, second, []Evidence{first}, firstAt.Add(time.Minute)) {
		t.Fatal("a new diagnostic log was treated as progress")
	}
}

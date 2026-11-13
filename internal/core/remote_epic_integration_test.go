package core

import (
	"ael/internal/store"
	"encoding/json"
	"testing"
)

func TestRemoteReportCheckCompletionUsesCurrentFailedEvidence(t *testing.T) {
	check := Check{ID: "first", Kind: "functional", Method: "command", Command: []string{"python3", "check.py"}, Required: true, Covers: []string{"observation"}}
	config, _ := json.Marshal(check)
	task := Task{Definition: Definition{Goal: "report", Revision: 2}, Plan: Plan{Revision: 3}, Candidate: "candidate", Evidence: []Evidence{{Check: check.ID, Result: "fail", Revision: 2, PlanRevision: 3, Candidate: "candidate", Config: store.Hash(config)}}}
	if !remoteCheckFinished(&task, check) {
		t.Fatal("current report failure did not complete its observation")
	}
	task.Definition.Goal = "verified"
	if remoteCheckFinished(&task, check) {
		t.Fatal("failed product acceptance counted as complete")
	}
	task.Definition.Goal = "report"
	task.Plan.Revision++
	if remoteCheckFinished(&task, check) {
		t.Fatal("stale report failure completed a new version")
	}
}

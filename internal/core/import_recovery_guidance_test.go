package core_test

import (
	"ael/internal/core"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func approvedImportRecoveryTask() core.Task {
	return core.Task{Definition: core.Definition{ID: "import-task", Revision: 2}, Plan: core.Plan{Revision: 2}, Status: "open", Step: "define", Paused: true, Legacy: map[string]any{"authority_import": "active-import"}, Approvals: []core.Approval{{Kind: "restore", Actor: "owner", Decision: "approve", Revision: 2, PlanRevision: 2, At: time.Now().UTC()}}}
}
func nextRecoveryAction(t *testing.T, task *core.Task) string {
	t.Helper()
	raw, err := json.Marshal(task.BaseNext())
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	action, _ := result["recovery_action"].(string)
	return action
}
func TestApprovedImportRecoveryRecommendsExecutorResume(t *testing.T) {
	task := approvedImportRecoveryTask()
	if got := nextRecoveryAction(t, &task); got != "resume" {
		t.Fatalf("approved imported task still lacks executable next action: recovery_action=%q", got)
	}
	next := task.BaseNext()
	if next.Action != "wait_environment" || !task.Paused || len(task.Approvals) != 1 {
		t.Fatal("read guidance changed authority or task state", next)
	}
	if len(next.Reasons) == 0 {
		t.Fatal("missing explanation")
	}
	for _, reason := range next.Reasons {
		if strings.TrimSpace(reason) == "" {
			t.Fatal("empty paused reason")
		}
	}
}
func TestApprovedImportRecoveryDoesNotBypassOtherHolds(t *testing.T) {
	for _, mode := range []string{"manual pause", "active responsibility", "interrupted responsibility", "failure", "pending failure review", "decision", "stale revision", "stale plan", "unapproved", "already resumed", "not imported", "accepted candidate"} {
		t.Run(mode, func(t *testing.T) {
			task := approvedImportRecoveryTask()
			switch mode {
			case "manual pause":
				task.Blocker = &core.Blocker{Category: "manual", Reason: "user paused"}
			case "active responsibility":
				task.Attempts = []core.Attempt{{Status: "running", Lease: time.Now().Add(time.Hour)}}
			case "interrupted responsibility":
				task.Attempts = []core.Attempt{{Status: "interrupted"}}
			case "failure":
				task.ActiveFailure = "failure"
			case "pending failure review":
				task.FailureCycles = map[string]core.FailureCycle{"execute": {Unit: "execute", State: "review_required"}}
			case "decision":
				task.Confirmation = &core.Confirmation{Kind: "revision"}
			case "stale revision":
				task.Definition.Revision++
			case "stale plan":
				task.Plan.Revision++
			case "unapproved":
				task.Approvals = nil
				task.Confirmation = &core.Confirmation{Kind: "restore"}
			case "already resumed":
				task.Paused = false
			case "not imported":
				task.Legacy = nil
			case "accepted candidate":
				task.Candidate = "candidate"
			}
			if action := nextRecoveryAction(t, &task); action != "" {
				t.Fatalf("protected state recommended resume: %s", action)
			}
		})
	}
}

package core_test

import (
	"ael/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskSummaryUsesExactEvidenceAndIgnoresHeartbeats(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{}
	for _, id := range []string{"one", "two", "three"} {
		checks = append(checks, core.Check{ID: id, Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"真实结果"}, Required: true})
	}
	id := create(t, s, core.Definition{Title: "并行摘要", Type: "bug", Goal: "verified", Acceptance: []string{"真实结果"}, Checks: checks})
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("real isolated output"), 0644)
	step(t, s, id, "writer")
	step(t, s, id, "writer", "result.txt")
	if _, e := s.Verify(id, "one", "checker-one"); e != nil {
		t.Fatal(e)
	}
	n, e := s.Claim(id, "two", "checker-two", "", "")
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.Summarize(n)
	if e != nil {
		t.Fatal(e)
	}
	if before.Running != 1 || before.WaitingChecks != 1 || before.Checks.Passed != 1 || before.Checks.NotRun != 1 || before.Checks.Running != 1 {
		t.Fatalf("summary lost parallel facts: %+v", before)
	}
	a := n.Attempt
	n, e = s.Heartbeat(id, a.ID, a.Token)
	if e != nil {
		t.Fatal(e)
	}
	after, e := s.Summarize(n)
	if e != nil {
		t.Fatal(e)
	}
	if before.Activity == nil || after.Activity == nil || !before.Activity.Equal(*after.Activity) {
		t.Fatal("heartbeat became apparent work progress")
	}
	// An old check must not appear passed after a plan revision changes.
	n.Task.Plan.Revision++
	stale, e := s.Summarize(n)
	if e != nil {
		t.Fatal(e)
	}
	if stale.Checks.Passed != 0 {
		t.Fatal("stale plan evidence appeared current")
	}
}

func TestTerminalSummaryDoesNotPresentOldBlockerAsCurrent(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "取消受阻任务", Type: "bug", Goal: "verified", Acceptance: []string{"保留结果"}})
	n, e := s.Claim(id, "", "fixture", "", "")
	if e != nil {
		t.Fatal(e)
	}
	a := n.Attempt
	_, e = s.Submit(core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: a.ID, Result: core.StepResult{Status: "blocked", Summary: "环境尚未准备", Blocker: &core.Blocker{Reason: "旧环境阻塞", Recovery: "旧恢复条件"}}})
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.Cancel(id, "明确取消，不再恢复旧环境")
	if e != nil {
		t.Fatal(e)
	}
	summary, e := s.Summarize(n)
	if e != nil {
		t.Fatal(e)
	}
	if summary.Blocker != "" || summary.Recovery != "" || summary.LastResult != "明确取消，不再恢复旧环境" {
		t.Fatal(summary)
	}
}

func TestSummaryUsesActualDecisionEventAndDoesNotTreatSourceConflictAsTaskApproval(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "准确版本决定", Type: "feature", Goal: "verified", Preset: "strict", Acceptance: []string{"审阅明确约束"}})
	step(t, s, id, "fixture")
	n := step(t, s, id, "fixture")
	before, e := s.Summarize(n)
	if e != nil || !before.RequiresTaskDecision {
		t.Fatal(before, e)
	}
	c := n.Task.Confirmation
	n, e = s.Confirm(id, c.Kind, "approve", "owner", "批准了当前约束", c.Candidate, c.Revision, c.PlanRevision)
	if e != nil {
		t.Fatal(e)
	}
	after, e := s.Summarize(n)
	if e != nil || after.RequiresTaskDecision || after.LastResult != "已批准：批准了当前约束" || after.Activity == nil || !after.Activity.Equal(n.Task.Approvals[len(n.Task.Approvals)-1].At) {
		t.Fatal(after, e)
	}
	n.Action = "wait_confirmation"
	n.Reasons = []string{"unresolved knowledge conflict: scoped-rule"}
	source, e := s.Summarize(n)
	if e != nil || source.RequiresTaskDecision {
		t.Fatal("scope conflict fabricated an approvable task version", source, e)
	}
}

func TestHumanReviewExactRejectsAnUnreviewedCandidate(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "准确人工通过", Type: "bug", Goal: "verified", Acceptance: []string{"人工明确判断"}, Checks: []core.Check{{ID: "owner", Kind: "review", Method: "human", Covers: []string{"人工明确判断"}, Required: true}}})
	step(t, s, id, "fixture")
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("actual output"), 0644)
	n := step(t, s, id, "fixture", "result.txt")
	os.WriteFile(filepath.Join(s.Store.Dir, "review.md"), []byte("Isolated review record for the exact fixture candidate."), 0644)
	expected := core.ReviewExpectation{Candidate: "unreviewed", DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	if _, e := s.ReviewExact(id, "owner", "owner", "明确通过当前产物", []string{"runtime:review.md"}, expected); e == nil {
		t.Fatal("human pass was rebound to another candidate")
	}
	task, e := s.Get(id)
	if e != nil || len(task.Evidence) != 0 {
		t.Fatal(task, e)
	}
	expected.Candidate = n.Task.Candidate
	if n, e = s.ReviewExact(id, "owner", "owner", "已实际阅读隔离产物并明确通过", []string{"runtime:review.md"}, expected); e != nil || n.Action != "completed" {
		t.Fatal(n, e)
	}
}

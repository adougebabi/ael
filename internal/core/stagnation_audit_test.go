package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDistinctFailuresWithNewEvidenceRemainDistinct(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "different", Title: "不同问题", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	for i, reason := range []string{"database unavailable", "syntax invalid", "dependency missing"} {
		claim, err := s.Claim(id, "", "fixture", "", "")
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("evidence-%d.md", i)
		if err = os.WriteFile(filepath.Join(s.Root, name), []byte("new actual finding: "+reason), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, core.Progress{Summary: reason, Next: "核对", Artifacts: []string{name}, EvidenceKind: "deliverable"}); err != nil {
			t.Fatal(err)
		}
		result, err := s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("distinct-"), Result: core.StepResult{Status: "fail", Summary: reason, FailureClass: reason, Artifacts: []string{name}}})
		if err != nil || result.Task.Paused {
			t.Fatalf("distinct evidenced problem incorrectly stopped: %s %+v %v", reason, result.Task.FailureCycles, err)
		}
	}
	task, err := s.Get(id)
	if err != nil || len(task.FailureCycles) != 3 {
		t.Fatalf("distinct failure histories not preserved: %+v %v", task.FailureCycles, err)
	}
}

func TestRewordedFailureWithUnrelatedArtifactsDoesNotResetBudget(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "reworded", Title: "同一故障", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	var result core.Next
	for i, reason := range []string{"database unavailable", "database unreachable", "database offline"} {
		claim, err := s.Claim(id, "", "fixture", "", "")
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("unrelated-%d.md", i)
		if err = os.WriteFile(filepath.Join(s.Root, name), []byte(fmt.Sprintf("unrelated content %d", i)), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, core.Progress{Summary: "其他文件", Next: "继续", Artifacts: []string{name}, EvidenceKind: "deliverable"}); err != nil {
			t.Fatal(err)
		}
		result, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("reworded-"), Result: core.StepResult{Status: "fail", Summary: reason}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !result.Task.Paused || result.Task.FailureCycles["define"].Count != 3 {
		t.Fatalf("unrelated files split an unclassified failure: %+v", result.Task.FailureCycles)
	}
}

func TestExplicitFailureClassesPreserveDistinctErrorCodes(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "codes", Title: "不同错误码", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	for _, code := range []string{"401", "403", "404"} {
		claim, err := s.Claim(id, "", "fixture", "", "")
		if err != nil {
			t.Fatal(err)
		}
		name := "http-" + code + ".txt"
		if err = os.WriteFile(filepath.Join(s.Root, name), []byte("actual HTTP "+code), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("code-"), Result: core.StepResult{Status: "fail", Summary: "HTTP " + code, FailureClass: "http:" + code, Artifacts: []string{name}}})
		if err != nil || result.Task.Paused {
			t.Fatalf("different error code incorrectly grouped: %+v %v", result.Task.FailureCycles, err)
		}
	}
	task, err := s.Get(id)
	if err != nil || len(task.FailureCycles) != 3 {
		t.Fatalf("distinct error codes lost: %+v %v", task.FailureCycles, err)
	}
}

func TestUnclassifiedTextLogCannotRefreshEvidenceProgress(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "agent-output.txt"), []byte("started\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := core.Progress{Summary: "重复日志", Next: "等待", Artifacts: []string{"runtime:agent-output.txt"}}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, p); err != nil {
		t.Fatal(err)
	}
	before, err := s.WorkHealthAt(id, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "agent-output.txt"), []byte("started\nheartbeat at 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, p); err != nil {
		t.Fatal(err)
	}
	after, err := s.WorkHealthAt(id, time.Now().Add(time.Hour))
	if err != nil || before.LastProgress != nil || after.LastProgress != nil || !after.NoProgress {
		t.Fatalf("unclassified log became evidence: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestUnclassifiedFactsRemainAvailableWithoutClaimingProgress(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	id := create(t, s, core.Definition{ID: "leaf", Parent: "epic", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, core.Progress{Summary: "已记录的事实", Next: "继续核对"}); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree("epic")
	if err != nil || len(tree.Progress) != 0 || len(tree.ReportedProgress) != 1 || tree.ReportedProgress[0].Summary != "已记录的事实" || tree.ReportedProgress[0].Next != "继续核对" {
		t.Fatalf("unclassified fact lost or reported as evidence: %+v %+v %v", tree.Progress, tree.ReportedProgress, err)
	}
	health, err := s.WorkHealthAt(id, time.Now())
	if err != nil || health.LastProgress != nil {
		t.Fatalf("unclassified fact became evidence: %+v %v", health, err)
	}
}

func TestReportFailureIsAnEvidenceConclusion(t *testing.T) {
	s := fixture(t)
	d := core.Definition{ID: "report", Title: "隔离报告", Type: "qa", Intent: "change", Goal: "report", Acceptance: []string{"结果"}, Checks: []core.Check{
		{ID: "first", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "exit 1"}, Covers: []string{"结果"}, Required: true},
		{ID: "second", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"结果"}, Required: true},
	}}
	id := atVerify(t, s, d)
	before, err := s.WorkHealthAt(id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(id, "first", "checker"); err != nil {
		t.Fatal(err)
	}
	after, err := s.WorkHealthAt(id, time.Now())
	if err != nil || after.LastProgress == nil || after.LastProgressKind != "check" || (before.LastProgress != nil && !after.LastProgress.After(*before.LastProgress)) {
		t.Fatalf("real failed report check omitted: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestDifferentFindingsOnSameCheckUpdateProgress(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{
		{ID: "first", Kind: "review", Method: "model", Covers: []string{"结果"}, Required: true},
		{ID: "second", Kind: "review", Method: "model", Covers: []string{"结果"}, Required: true},
	}
	id := atVerify(t, s, core.Definition{ID: "findings", Title: "不同发现", Type: "qa", Intent: "change", Goal: "report", Acceptance: []string{"结果"}, Checks: checks})
	for i, reason := range []string{"登录失败", "搜索失败"} {
		name := fmt.Sprintf("report-%d.md", i)
		if err := os.WriteFile(filepath.Join(s.Store.Dir, name), []byte("具体发现："+reason), 0600); err != nil {
			t.Fatal(err)
		}
		before, err := s.WorkHealthAt(id, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		task, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Assess(core.Assessment{Task: id, Check: "first", Candidate: task.Candidate, Method: "model", Executor: "checker", Result: "fail", Reason: reason, Inputs: []string{"result.txt"}, Artifacts: []string{"runtime:" + name}}); err != nil {
			t.Fatal(err)
		}
		after, err := s.WorkHealthAt(id, time.Now())
		if err != nil || after.LastProgress == nil || after.LastProgressKind != "check" || (before.LastProgress != nil && !after.LastProgress.After(*before.LastProgress)) {
			t.Fatalf("new finding did not advance evidence: before=%+v after=%+v err=%v", before, after, err)
		}
	}
}

func TestLegacyRepeatedLogsCannotDisplaceEvidence(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	id := create(t, s, core.Definition{ID: "leaf", Parent: "epic", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "fact.md"), []byte("real evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, core.Progress{Summary: "真实成果", Next: "继续", Artifacts: []string{"fact.md"}, EvidenceKind: "deliverable"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		raw, _ := json.Marshal(map[string]any{"summary": "旧式重复日志", "next": "等待", "artifacts": []string{"runtime:old.log"}})
		if _, err = s.Store.DB.Exec(`INSERT INTO events(task_id,kind,body,created) VALUES(?,?,?,?)`, id, "work_progress", string(raw), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := s.Tree("epic")
	if err != nil || tree.ProgressTruncated || len(tree.Progress) != 1 || tree.Progress[0].Summary != "真实成果" || len(tree.ReportedProgress) != 5 || !tree.ReportsTruncated {
		t.Fatalf("legacy diagnostics displaced evidence: progress=%+v reports=%d truncated=%t/%t err=%v", tree.Progress, len(tree.ReportedProgress), tree.ProgressTruncated, tree.ReportsTruncated, err)
	}
}

func TestParallelChecksKeepDeclaredLongStage(t *testing.T) {
	s := fixture(t)
	d := bug()
	second := d.Checks[0]
	second.ID = "second"
	d.Checks = append(d.Checks, second)
	id := atVerify(t, s, d)
	long, err := s.Claim(id, "test", "long-check", "", "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Hour)
	if _, err = s.Progress(id, long.Attempt.ID, long.Attempt.Token, core.Progress{Summary: "执行长检查", Next: "等待结果", Stage: "long-check", ExpectedUntil: &deadline}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(id, "second", "other-check", "", ""); err != nil {
		t.Fatal(err)
	}
	health, err := s.WorkHealthAt(id, time.Now())
	if err != nil || !health.HealthyLong || health.Stage != "long-check" || health.ExpectedUntil == nil {
		t.Fatalf("parallel check hid long stage: %+v %v", health, err)
	}
}

func TestOldCheckpointCannotReduceUnresolvedFailureBudget(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "stale-record", Title: "旧检查点", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	var old []byte
	for i := 0; i < 2; i++ {
		claim, err := s.Claim(id, "", "fixture", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("old-record-"), Result: core.StepResult{Status: "fail", Summary: "same failure"}}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			old, err = os.ReadFile(filepath.Join(s.Root, ".ael/records", id+".json"))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".ael/records", id+".json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	other, _, err := bootstrap.Open(s.Root)
	if err == nil {
		other.Store.Close()
		t.Fatal("older checkpoint silently reset unresolved failures")
	}
	if !strings.Contains(err.Error(), "TASK_FAILURE_CYCLE_CONFLICT") {
		t.Fatalf("unexpected recovery failure: %v", err)
	}
}

func TestUnrelatedRevisionCannotSkipPrethresholdFailedUnit(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "revision", Title: "修订隔离", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	for i := 0; i < 2; i++ {
		claim, err := s.Claim(id, "", "fixture", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("revision-fail-"), Result: core.StepResult{Status: "fail", Summary: "same failure"}}); err != nil {
			t.Fatal(err)
		}
	}
	task, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	d := task.Definition
	d.Checks = append(d.Checks, core.Check{ID: "optional", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"结果"}})
	result, err := s.Edit(id, d, "add unrelated optional check")
	if err != nil || result.Task.Step != "define" || result.Task.FailureCycles["define"].Count != 2 {
		t.Fatalf("unrelated revision skipped the failed unit: %+v %v", result.Task, err)
	}
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("revision-fail-"), Result: core.StepResult{Status: "fail", Summary: "same failure"}})
	if err != nil || !result.Task.Paused || result.Task.FailureCycles["define"].Count != 3 {
		t.Fatalf("third failure did not stop after revision: %+v %v", result.Task.FailureCycles, err)
	}
}

func TestPortableRecoveryRetainsSeenEvidenceHashes(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "portable", Title: "隔离恢复", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "report.md"), []byte("same retained result"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, core.Progress{Summary: "真实结果", Next: "继续", Artifacts: []string{"report.md"}, EvidenceKind: "deliverable"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("portable-"), Result: core.StepResult{Status: "fail", Summary: "same failure"}}); err != nil {
		t.Fatal(err)
	}
	clone := copyCheckout(t, s.Root)
	restored, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Store.Close()
	task, err := restored.Get(id)
	if err != nil || task.EvidenceProgress == nil || len(task.EvidenceProgress.SeenArtifacts) != 1 || task.FailureCycles["define"].Count != 1 {
		t.Fatalf("portable recovery lost seen evidence or budget: %+v %+v %v", task.EvidenceProgress, task.FailureCycles, err)
	}
	c := task.Confirmation
	if c == nil {
		t.Fatal("portable restore confirmation missing")
	}
	if _, err = restored.Confirm(id, c.Kind, "approve", "fixture", "reviewed restored input", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Resume(id, "", true); err != nil {
		t.Fatal(err)
	}
	claim, err = restored.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := restored.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("portable-pass-"), Result: core.StepResult{Status: "pass", Summary: "same old artifact", Artifacts: []string{"report.md"}}})
	if err != nil || result.Task.FailureCycles["define"].State == "resolved" {
		t.Fatalf("old artifact resolved a failure after restore: %+v %v", result.Task.FailureCycles, err)
	}
	if _, err = restored.Resume(id, "define", false); err != nil {
		t.Fatal(err)
	}
	claim, err = restored.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err = restored.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("portable-fail-"), Result: core.StepResult{Status: "fail", Summary: "same failure"}})
	if err != nil || result.Task.FailureCycles["define"].Count != 2 {
		t.Fatalf("restored failure budget was reset: %+v %v", result.Task.FailureCycles, err)
	}
}

func TestParallelFailuresEachRequireTheirOwnReview(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{}
	for _, id := range []string{"a", "b", "c"} {
		checks = append(checks, core.Check{ID: id, Kind: "functional", Method: "command", Command: []string{"sh", "-c", "exit 1"}, Covers: []string{"结果"}, Required: true})
	}
	id := atVerify(t, s, core.Definition{ID: "parallel", Title: "并行报告", Type: "qa", Intent: "change", Goal: "report", Acceptance: []string{"结果"}, Checks: checks})
	for _, check := range []string{"a", "a", "b", "b"} {
		if _, err := s.Verify(id, check, "checker-"+check); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.Claim(id, "a", "checker-a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Claim(id, "b", "checker-b", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyClaim(id, "a", "checker-a", a.Attempt.ID, a.Attempt.Token); err != nil {
		t.Fatal(err)
	}
	result, err := s.VerifyClaim(id, "b", "checker-b", b.Attempt.ID, b.Attempt.Token)
	if err != nil || result.Task.ActiveFailure != "check:a" || result.Task.FailureCycles["check:a"].State != "review_required" || result.Task.FailureCycles["check:b"].State != "review_required" {
		t.Fatalf("parallel failure overwrote the first review: active=%q cycles=%+v err=%v", result.Task.ActiveFailure, result.Task.FailureCycles, err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "review.txt"), []byte("bounded review"), 0600); err != nil {
		t.Fatal(err)
	}
	review := core.FailureReview{Actor: "fixture", Tried: "核对输入", Excluded: "排除缺失", Hypothesis: "检查失败", Next: "复测", Recovery: "核对完成", Evidence: []string{"runtime:review.txt"}}
	result, err = s.ReviewFailure(id, review)
	if err != nil || result.Task.ActiveFailure != "check:b" || !result.Task.Paused {
		t.Fatalf("next pending review was lost: %+v %v", result.Task, err)
	}
	if _, err = s.Resume(id, "", true); err == nil || !strings.Contains(err.Error(), "ROOT_CAUSE_REVIEW_REQUIRED") {
		t.Fatalf("second review skipped: %v", err)
	}
	if _, err = s.ReviewFailure(id, review); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(id, "", true); err != nil {
		t.Fatalf("reviewed independent failures could not resume: %v", err)
	}
}

func TestConcurrentFailureSubmissionsPreserveBothReviews(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{}
	for _, check := range []string{"a", "b", "c"} {
		checks = append(checks, core.Check{ID: check, Kind: "functional", Method: "command", Command: []string{"sh", "-c", "exit 1"}, Covers: []string{"结果"}, Required: true})
	}
	id := atVerify(t, s, core.Definition{ID: "concurrent", Title: "并发复核", Type: "qa", Intent: "change", Goal: "report", Acceptance: []string{"结果"}, Checks: checks})
	for _, check := range []string{"a", "a", "b", "b"} {
		if _, err := s.Verify(id, check, "checker-"+check); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.Claim(id, "a", "checker-a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Claim(id, "b", "checker-b", "", "")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 2)
	go func() { _, err := s.VerifyClaim(id, "a", "checker-a", a.Attempt.ID, a.Attempt.Token); finished <- err }()
	go func() { _, err := s.VerifyClaim(id, "b", "checker-b", b.Attempt.ID, b.Attempt.Token); finished <- err }()
	for i := 0; i < 2; i++ {
		if err = <-finished; err != nil {
			t.Fatal(err)
		}
	}
	task, err := s.Get(id)
	if err != nil || !task.Paused || task.FailureCycles["check:a"].State != "review_required" || task.FailureCycles["check:b"].State != "review_required" || (task.ActiveFailure != "check:a" && task.ActiveFailure != "check:b") {
		t.Fatalf("concurrent submissions lost a review: active=%q cycles=%+v err=%v", task.ActiveFailure, task.FailureCycles, err)
	}
}

func TestPendingReviewPreventsConcurrentCompletion(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{}
	for _, id := range []string{"a", "b", "c"} {
		checks = append(checks, core.Check{ID: id, Kind: "functional", Method: "command", Command: []string{"sh", "-c", "exit 1"}, Covers: []string{"结果"}, Required: true})
	}
	id := atVerify(t, s, core.Definition{ID: "pending", Title: "并行报告", Type: "qa", Intent: "change", Goal: "report", Acceptance: []string{"结果"}, Checks: checks})
	for _, check := range []string{"a", "a", "b", "b"} {
		if _, err := s.Verify(id, check, "checker-"+check); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.Claim(id, "a", "checker-a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(id, "c", "checker-c", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyClaim(id, "a", "checker-a", a.Attempt.ID, a.Attempt.Token); err != nil {
		t.Fatal(err)
	}
	result, err := s.VerifyClaim(id, "c", "checker-c", c.Attempt.ID, c.Attempt.Token)
	if err != nil || result.Task.Status != "open" || !result.Task.Paused || result.Task.ActiveFailure != "check:a" {
		t.Fatalf("concurrent result bypassed pending review: %+v %v", result.Task, err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "review.txt"), []byte("bounded review"), 0600); err != nil {
		t.Fatal(err)
	}
	review := core.FailureReview{Actor: "fixture", Tried: "核对输入", Excluded: "排除缺失", Hypothesis: "检查失败", Next: "检查报告", Recovery: "已核对", Evidence: []string{"runtime:review.txt"}}
	if _, err = s.ReviewFailure(id, review); err != nil {
		t.Fatal(err)
	}
	result, err = s.Resume(id, "", false)
	if err != nil || result.Task.Status != "completed" || result.Task.Result != "report_delivered_with_findings" {
		t.Fatalf("reviewed valid results required unnecessary rechecks: %+v %v", result.Task, err)
	}
}

func TestResumeInvalidatesEvidenceWhenCandidateChanged(t *testing.T) {
	s := fixture(t)
	d := bug()
	second := d.Checks[0]
	second.ID = "second"
	d.Checks = append(d.Checks, second)
	id := atVerify(t, s, d)
	if _, err := s.Verify(id, "test", "checker"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pause(id, "隔离暂停"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("changed during pause"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.Resume(id, "", false)
	if err != nil || result.Task.Step != "execute" || result.Task.Candidate != "" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) == 0 || result.Task.EvidenceHistory[len(result.Task.EvidenceHistory)-1].Result != "pass" {
		t.Fatalf("changed candidate kept current evidence or lost history: %+v %v", result.Task, err)
	}
}

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

func TestRepeatedFailureRequiresReviewAcrossMessageChanges(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "repeat", Title: "隔离重复失败", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, MaxRetries: 3})
	var result core.Next
	for i := 1; i <= 3; i++ {
		claim, err := s.Claim(id, "", fmt.Sprintf("fixture-%d", i), "", "")
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		result, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("failure-"), Result: core.StepResult{Status: "fail", Summary: fmt.Sprintf("same unit failure, observation %d", i)}})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if i < 3 && result.Task.Paused {
			t.Fatalf("stopped before configured threshold: %+v", result.Task)
		}
	}
	if !result.Task.Paused || result.Task.Blocker == nil || result.Task.Blocker.Tried == "" || result.Task.Blocker.Recovery == "" {
		t.Fatalf("three failures did not retain a reviewable stop: paused=%t blocker=%+v", result.Task.Paused, result.Task.Blocker)
	}
	if _, err := s.Resume(id, "", false); err == nil || !strings.Contains(err.Error(), "ROOT_CAUSE_REVIEW_REQUIRED") {
		t.Fatalf("resume skipped required root-cause review: %v", err)
	}
	cycle := result.Task.FailureCycles["define"]
	if cycle.Count != 3 || cycle.State != "review_required" || cycle.Signature == "" || cycle.Input == "" || result.Task.Blocker.Unit != "define" || result.Task.Blocker.Next == "" {
		t.Fatalf("failure details were not retained: %+v %+v", cycle, result.Task.Blocker)
	}
	clone := copyCheckout(t, s.Root)
	cloned, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	cloneTask, err := cloned.Get(id)
	if err != nil || cloneTask.FailureCycles["define"].Count != 3 || cloneTask.ActiveFailure != "define" || cloneTask.Blocker == nil || cloneTask.Blocker.Input == "" {
		t.Fatalf("portable recovery lost failure review: %+v %v", cloneTask.FailureCycles, err)
	}
	if _, err = cloned.Resume(id, "", true); err == nil || !strings.Contains(err.Error(), "ROOT_CAUSE_REVIEW_REQUIRED") {
		t.Fatalf("portable recovery skipped review: %v", err)
	}
	cloned.Store.Close()
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := bootstrap.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Store.Close()
	task, err := reopened.Get(id)
	if err != nil || task.FailureCycles["define"].Count != 3 || task.ActiveFailure != "define" || !task.Paused {
		t.Fatalf("restart reset the failure budget: %+v %v", task.FailureCycles, err)
	}
	definition := task.Definition
	definition.Title = "只改标题"
	if result, err = reopened.Edit(id, definition, "display only"); err != nil || !result.Task.Paused || result.Task.FailureCycles["define"].Count != 3 {
		t.Fatalf("title change reset the stop: %+v %v", result.Task, err)
	}
	definition = result.Task.Definition
	definition.MaxSeconds = 3600
	if result, err = reopened.Edit(id, definition, "unrelated execution budget"); err != nil || !result.Task.Paused || result.Task.Step != "define" || result.Task.FailureCycles["define"].Count != 3 {
		t.Fatalf("unrelated revision bypassed the failed unit: %+v %v", result.Task, err)
	}
	if err = os.WriteFile(filepath.Join(reopened.Store.Dir, "review.txt"), []byte("actual bounded review evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	review := core.FailureReview{Actor: "fixture-reviewer", Tried: "检查三次失败输入", Excluded: "排除路径缺失", Hypothesis: "检查配置不匹配", Next: "核对配置后重试", Recovery: "配置已核对", Evidence: []string{"runtime:review.txt"}}
	if _, err = reopened.ReviewFailure(id, review); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Resume(id, "", false); err != nil {
		t.Fatal(err)
	}
	claim, err := reopened.Claim(id, "", "fixture-after-review", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err = reopened.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("empty-pass-"), Result: core.StepResult{Status: "pass", Summary: "无新证据的口头通过"}})
	if err != nil || result.Task.FailureCycles["define"].State != "reviewed" || result.Task.FailureCycles["define"].Count != 3 {
		t.Fatalf("unsubstantiated pass reset the failure budget: %+v %v", result.Task.FailureCycles, err)
	}
	if _, err = reopened.Resume(id, "define", false); err != nil {
		t.Fatal(err)
	}
	claim, err = reopened.Claim(id, "", "fixture-after-review", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err = reopened.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("failure-"), Result: core.StepResult{Status: "fail", Summary: "same unit still fails"}})
	if err != nil || !result.Task.Paused || result.Task.FailureCycles["define"].State != "blocked" || result.Task.FailureCycles["define"].Count != 4 {
		t.Fatalf("failure after review did not stop: %+v %v", result.Task.FailureCycles, err)
	}
	review.Excluded = "修改文字不是新证据"
	if _, err = reopened.ReviewFailure(id, review); err == nil || !strings.Contains(err.Error(), "ROOT_CAUSE_NEW_EVIDENCE_REQUIRED") {
		t.Fatalf("reworded review reused the same evidence: %v", err)
	}
	if err = os.WriteFile(filepath.Join(reopened.Store.Dir, "review-2.txt"), []byte("new isolated finding"), 0600); err != nil {
		t.Fatal(err)
	}
	review.Evidence = []string{"runtime:review-2.txt"}
	if result, err = reopened.ReviewFailure(id, review); err != nil || result.Task.FailureCycles["define"].Count != 4 {
		t.Fatalf("new evidence reset the budget: %+v %v", result.Task.FailureCycles, err)
	}
}

func TestNoProgressUsesEvidenceNotHeartbeatOrRepeatedFacts(t *testing.T) {
	s := fixture(t)
	policyPath := filepath.Join(s.Root, ".ael", "policy.yaml")
	raw, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	policy["no_progress_seconds"] = 2
	raw, _ = json.Marshal(policy)
	if err = os.WriteFile(policyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	id := create(t, s, core.Definition{ID: "health", Title: "隔离进展", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "fact.txt"), []byte("first fact"), 0600); err != nil {
		t.Fatal(err)
	}
	progress := core.Progress{Summary: "已核实文件", Next: "继续", Artifacts: []string{"fact.txt"}, EvidenceKind: "deliverable"}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, progress); err != nil {
		t.Fatal(err)
	}
	firstNext, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	firstSummary, err := s.Summarize(firstNext)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(5 * time.Second)
	before, err := s.WorkHealthAt(id, future)
	if err != nil || !before.NoProgress || before.LastProgress == nil {
		t.Fatalf("missing evidence-based warning: %+v %v", before, err)
	}
	if _, err = s.Heartbeat(id, claim.Attempt.ID, claim.Attempt.Token); err != nil {
		t.Fatal(err)
	}
	progress.Summary = "重复不同措辞"
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, progress); err != nil {
		t.Fatal(err)
	}
	after, err := s.WorkHealthAt(id, future)
	if err != nil || !after.NoProgress || after.LastProgress == nil || !after.LastProgress.Equal(*before.LastProgress) || after.LastContact == nil {
		t.Fatalf("heartbeat or repeated evidence cleared warning: before=%+v after=%+v err=%v", before, after, err)
	}
	secondNext, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	secondSummary, err := s.Summarize(secondNext)
	if err != nil || firstSummary.Activity == nil || secondSummary.Activity == nil || !firstSummary.Activity.Equal(*secondSummary.Activity) {
		t.Fatalf("repeated log changed displayed progress time: before=%+v after=%+v err=%v", firstSummary.Activity, secondSummary.Activity, err)
	}
	logPath := filepath.Join(s.Store.Dir, "repeated.log")
	if err = os.WriteFile(logPath, []byte("heartbeat at time 1"), 0600); err != nil {
		t.Fatal(err)
	}
	logProgress := core.Progress{Summary: "重复日志", Next: "继续等待", Artifacts: []string{"runtime:repeated.log"}}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, logProgress); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(logPath, []byte("heartbeat at time 2"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, logProgress); err != nil {
		t.Fatal(err)
	}
	logs, err := s.WorkHealthAt(id, future)
	if err != nil || logs.LastProgress == nil || !logs.LastProgress.Equal(*before.LastProgress) || !logs.NoProgress {
		t.Fatalf("growing diagnostic logs fabricated progress: %+v %v", logs, err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "fact.txt"), []byte("new fact"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, progress); err != nil {
		t.Fatal(err)
	}
	changed, err := s.WorkHealthAt(id, time.Now())
	if err != nil || changed.NoProgress || changed.LastProgress == nil || !changed.LastProgress.After(*before.LastProgress) {
		t.Fatalf("new evidence did not update progress: %+v %v", changed, err)
	}
	deadline := time.Now().Add(time.Minute)
	progress = core.Progress{Summary: "长检查进行中", Next: "等待检查完成", Stage: "integration-test", ExpectedUntil: &deadline}
	if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, progress); err != nil {
		t.Fatal(err)
	}
	healthy, err := s.WorkHealthAt(id, time.Now().Add(5*time.Second))
	if err != nil || !healthy.NoProgress || !healthy.HealthyLong || healthy.Stage != "integration-test" || healthy.ExpectedUntil == nil {
		t.Fatalf("declared healthy long phase was misreported: %+v %v", healthy, err)
	}
	current, err := s.Get(id)
	if err != nil || current.Paused {
		t.Fatalf("no-progress warning stopped a healthy long step: %+v %v", current, err)
	}
}

func TestNewCheckConclusionUpdatesEvidenceProgress(t *testing.T) {
	s := fixture(t)
	d := bug()
	second := d.Checks[0]
	second.ID = "second"
	d.Checks = append(d.Checks, second)
	id := atVerify(t, s, d)
	before, err := s.WorkHealthAt(id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(id, "test", "checker"); err != nil {
		t.Fatal(err)
	}
	after, err := s.WorkHealthAt(id, time.Now())
	if err != nil || after.LastProgress == nil || (before.LastProgress != nil && !after.LastProgress.After(*before.LastProgress)) || after.LastProgressKind != "check" {
		t.Fatalf("new real check conclusion not recorded: before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err = s.Verify(id, "test", "checker-again"); err != nil {
		t.Fatal(err)
	}
	repeated, err := s.WorkHealthAt(id, time.Now())
	if err != nil || repeated.LastProgress == nil || !repeated.LastProgress.Equal(*after.LastProgress) {
		t.Fatalf("repeating the same check conclusion fabricated progress: after=%+v repeated=%+v err=%v", after, repeated, err)
	}
}

func TestTimedOutCheckRetainsLogAndRecoveryPoint(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Checks[0].Command = []string{"sh", "-c", "printf 'isolated long check started\\n'; sleep 3"}
	d.Checks[0].Timeout = 1
	id := atVerify(t, s, d)
	result, err := s.Verify(id, "test", "checker")
	if err != nil || !result.Task.Paused || result.Task.Blocker == nil || result.Task.Blocker.Unit != "check:test" || result.Task.Blocker.Input == "" || result.Task.Blocker.Next == "" || result.Task.Step != "verify" {
		t.Fatalf("timeout did not retain recovery point: %+v %v", result.Task, err)
	}
	if len(result.Task.Evidence) == 0 || len(result.Task.Evidence[len(result.Task.Evidence)-1].Artifacts) == 0 {
		t.Fatal("timeout evidence log missing")
	}
	ref := result.Task.Evidence[len(result.Task.Evidence)-1].Artifacts[0]
	log, err := s.TaskArtifact(id, ref)
	if err != nil || !strings.Contains(string(log), "isolated long check started") {
		t.Fatalf("timeout log not retained: %q %v", log, err)
	}
	result, err = s.Resume(id, "", true)
	if err != nil || result.Task.Paused || result.Task.Step != "verify" || result.Task.Candidate == "" {
		t.Fatalf("timeout recovery discarded the checkpoint: %+v %v", result.Task, err)
	}
}

func TestParentStopIncludesRecoveryDetails(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	id := create(t, s, core.Definition{ID: "leaf", Parent: "epic", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	for i := 0; i < 3; i++ {
		claim, err := s.Claim(id, "", "fixture", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("parent-stop-"), Result: core.StepResult{Status: "fail", Summary: "same failure"}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.NextWork(core.WorkRequest{Parent: "epic", Runner: "fixture"})
	if err != nil || result.Action != "wait_environment" || result.Checkpoint.Task != id || result.Checkpoint.Unit != "define" || result.Blocker == nil || result.Blocker.Input == "" || result.Blocker.Tried == "" || result.Blocker.Recovery == "" || result.Blocker.Next == "" {
		t.Fatalf("parent stop omitted recovery details: %+v %v", result, err)
	}
	if err = s.SaveWorkCheckpoint(result); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.WorkCheckpoint("epic", "fixture")
	if err != nil || checkpoint.Blocker == nil || checkpoint.Blocker.Input != result.Blocker.Input || checkpoint.Blocker.Next != result.Blocker.Next {
		t.Fatalf("checkpoint lost recovery details: %+v %v", checkpoint, err)
	}
}

func TestEpicTimelineKeepsEvidenceWhenRepeatedLogsAccumulate(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	id := create(t, s, core.Definition{ID: "leaf", Parent: "epic", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "fact.txt"), []byte("actual evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		progress := core.Progress{Summary: fmt.Sprintf("same evidence, message %d", i), Next: "继续", Artifacts: []string{"fact.txt"}, EvidenceKind: "deliverable"}
		if _, err = s.Progress(id, claim.Attempt.ID, claim.Attempt.Token, progress); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := s.Tree("epic")
	if err != nil || tree.ProgressTruncated || len(tree.Progress) != 1 || tree.Progress[0].Summary != "same evidence, message 0" {
		t.Fatalf("repeated logs displaced real progress: %+v truncated=%t err=%v", tree.Progress, tree.ProgressTruncated, err)
	}
}

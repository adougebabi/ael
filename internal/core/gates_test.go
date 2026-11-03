package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func bug() core.Definition {
	return core.Definition{Title: "后端 bug", Type: "bug", Goal: "verified", Acceptance: []string{"correct"}, Checks: []core.Check{{ID: "test", Kind: "functional", Command: []string{"sh", "-c", "test -f result.txt"}, Covers: []string{"correct"}, Required: true}}}
}
func atVerify(t *testing.T, s *core.Service, d core.Definition) string {
	id := create(t, s, d)
	step(t, s, id, "A")
	if d.Type == "feature" && d.Preset != "fast" {
		step(t, s, id, "architect")
	}
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("actual deliverable"), 0644)
	step(t, s, id, "B", "result.txt")
	return id
}
func TestPlansAndNoAutomaticPass(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	n, _ := s.Next(id)
	if n.Task.Plan.Preset != "fast" || len(n.Task.Definition.Checks) != 1 {
		t.Fatal(n)
	}
	d := bug()
	d.UI = true
	d.UX = true
	d.Type = "feature"
	id = create(t, s, d)
	n, _ = s.Next(id)
	if len(n.Task.Definition.Checks) != 3 || n.Task.Plan.Preset != "standard" {
		t.Fatal(n)
	}
	d = bug()
	d.Type = "research"
	d.Goal = "analysis"
	d.Checks = nil
	id = create(t, s, d)
	step(t, s, id, "one")
	os.WriteFile(filepath.Join(s.Root, "conclusion.md"), []byte("evidence-backed conclusion"), 0644)
	step(t, s, id, "one", "conclusion.md")
	n, e := s.Verify(id, "review", "check")
	if e != nil {
		t.Fatal(e)
	}
	if n.Action == "completed" || n.Task.Evidence[0].Result != "blocked" {
		t.Fatal(n)
	}
	s.Resume(id, "", true)
	n, e = s.Review(id, "review", "reviewer", "read conclusion and evidence", []string{"conclusion.md"})
	if e != nil || n.Action != "completed" {
		t.Fatal(n, e)
	}
	for _, x := range n.Task.Plan.Steps {
		if x == "delivery" {
			t.Fatal("research requires deployment")
		}
	}
}
func TestCandidateAndDefinitionInvalidation(t *testing.T) {
	s := fixture(t)
	id := atVerify(t, s, bug())
	os.WriteFile(filepath.Join(s.Root, "untracked.txt"), []byte("new input"), 0644)
	if _, e := s.Verify(id, "test", "C"); e == nil || !strings.Contains(e.Error(), "CANDIDATE_CHANGED") {
		t.Fatal(e)
	}
	s.Resume(id, "execute", true)
	step(t, s, id, "B", "result.txt")
	d := bug()
	d.Acceptance = []string{"changed"}
	d.Checks[0].Covers = d.Acceptance
	n, e := s.Edit(id, d, "requirements revised")
	if e != nil {
		t.Fatal(e)
	}
	if n.Revision != 2 || n.Task.Confirmation == nil || len(n.Task.Evidence) != 0 {
		t.Fatal(n)
	}
	if _, e = s.Confirm(id, "revision", "approve", "reviewer", "ok", "", 1, 1); e == nil {
		t.Fatal("old approval accepted")
	}
}
func TestInputChangesDuringCheck(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Checks[0].Command = []string{"sh", "-c", "echo mutated > result.txt"}
	id := atVerify(t, s, d)
	n, e := s.Verify(id, "test", "C")
	if e != nil {
		t.Fatal(e)
	}
	if n.Task.Status == "completed" || n.Task.Evidence[0].Result != "blocked" {
		t.Fatal(n)
	}
}
func TestClaimsIdempotencyAndFencing(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := []core.Next{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, e := s.Claim(id, "", "A", "", "")
			if e == nil {
				mu.Lock()
				wins = append(wins, n)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(wins) != 1 {
		t.Fatal("claim winners", len(wins))
	}
	a := wins[0].Attempt
	sub := core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: "stable-key", Result: core.StepResult{Status: "pass", Summary: "defined"}}
	n, e := s.Submit(sub)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Submit(sub)
	if e != nil || again.Step != n.Step {
		t.Fatal(again, e)
	}
	sub.Result.Summary = "different"
	if _, e = s.Submit(sub); e == nil {
		t.Fatal("idempotency conflict missing")
	}
	n, e = s.Claim(id, "", "B", "", "")
	if e != nil {
		t.Fatal(e)
	}
	a = n.Attempt
	// Simulate an expired lease without sleeping two minutes.
	task, _ := s.Get(id)
	task.Attempts[len(task.Attempts)-1].Lease = time.Now().Add(-time.Minute)
	b, _ := json.Marshal(task)
	s.Store.DB.Exec("UPDATE tasks SET body=? WHERE id=?", string(b), id)
	if _, e = s.Submit(core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: "stale", Result: core.StepResult{Status: "pass", Summary: "late", Artifacts: []string{"result.txt"}}}); e == nil {
		t.Fatal("expired token accepted")
	}
	if _, e = s.Resume(id, "", false); e == nil {
		t.Fatal("resumed without process acknowledgement")
	}
	if _, e = s.Resume(id, "", true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Heartbeat(id, a.ID, a.Token); e == nil {
		t.Fatal("old token heartbeated")
	}
}
func TestWorkspaceMutexDependenciesAndCancellation(t *testing.T) {
	s := fixture(t)
	first := create(t, s, bug())
	second := create(t, s, bug())
	if _, e := s.Claim(first, "", "A", "", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Claim(second, "", "B", "", ""); e == nil {
		t.Fatal("same workspace double writer")
	}
	s.Resume(first, "", true)
	s.Cancel(first, "user cancelled")
	d := bug()
	d.Dependencies = []string{first}
	id := create(t, s, d)
	n, _ := s.Next(id)
	if n.Action != "wait_dependency" {
		t.Fatal(n)
	}
	d = bug()
	d.Dependencies = []string{second}
	if _, e := s.Edit(second, d, "cycle test"); e == nil {
		t.Fatal("self dependency allowed")
	}
}
func TestParallelIndependentChecks(t *testing.T) {
	s := fixture(t)
	d := bug()
	c := d.Checks[0]
	c.ID = "contract"
	d.Checks = append(d.Checks, c)
	id := atVerify(t, s, d)
	n, e := s.Claim(id, "test", "C", "", "")
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Claim(id, "contract", "D", "", "")
	if e != nil {
		t.Fatal("parallel check claim", e)
	}
	if n.Attempt.Input != other.Attempt.Input {
		t.Fatal("checks cross candidate")
	}
	newid := create(t, s, bug())
	if _, e = s.Claim(newid, "", "writer", "", ""); e == nil {
		t.Fatal("writer while verification")
	}
	s.Resume(id, "", true)
	n, e = s.Verify(id, "test", "C")
	if e != nil || n.Action == "completed" {
		t.Fatal(n, e)
	}
	n, e = s.Verify(id, "contract", "D")
	if e != nil || n.Action != "completed" {
		t.Fatal(n, e)
	}
}
func TestBudgetsAndFeedback(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.MaxRetries = 1
	id := create(t, s, d)
	n, e := s.Claim(id, "", "A", "", "")
	if e != nil {
		t.Fatal(e)
	}
	a := n.Attempt
	s.Submit(core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: "failed", Result: core.StepResult{Status: "fail", Summary: "same failure"}})
	if _, e = s.Claim(id, "", "B", "", ""); e == nil {
		t.Fatal("retry budget ignored")
	}
	id = atVerify(t, s, bug())
	n, e = s.Verify(id, "test", "C")
	if e != nil {
		t.Fatal(e)
	}
	d = bug()
	d.FeedbackOf = id
	next := create(t, s, d)
	old, _ := s.Get(id)
	if old.Status != "completed" || len(old.Feedback) != 1 || old.Feedback[0] != next {
		t.Fatal("feedback rewrote history")
	}
}
func TestOutboxCrashRepairAndConflict(t *testing.T) {
	s := fixture(t)
	p := filepath.Join(s.Root, "atomic.txt")
	e := s.Store.Write(func(tx *store.Tx) error { return tx.File(p, []byte("first")) })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("file written before flush")
	}
	if e = s.Store.Flush(); e != nil {
		t.Fatal(e)
	}
	e = s.Store.Write(func(tx *store.Tx) error { return tx.File(p, []byte("second")) })
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("user edit"), 0644)
	if e = s.Store.Flush(); e == nil {
		t.Fatal("overwrote user edit")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "user edit" {
		t.Fatal(string(b))
	}
	os.WriteFile(p, []byte("first"), 0644)
	if e = s.Store.Flush(); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(p)
	if string(b) != "second" {
		t.Fatal(string(b))
	}
}

func TestDeliveryGoalReceiptAndInterruptedRecovery(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Goal = "released"
	d.Environment = "test-environment"
	id := atVerify(t, s, d)
	n, e := s.Verify(id, "test", "C")
	if e != nil || n.Action != "wait_confirmation" || n.Task.Status == "completed" {
		t.Fatal(n, e)
	}
	c := n.Task.Confirmation
	n, e = s.Confirm(id, c.Kind, "approve", "owner", "authorize test receipt only", c.Candidate, c.Revision, c.PlanRevision)
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.Claim(id, "", "delivery-bot", "", "")
	if e != nil {
		t.Fatal(e)
	}
	a := n.Attempt
	sub := core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: "delivery", Result: core.StepResult{Status: "pass", Summary: "claimed success"}}
	if _, e = s.Submit(sub); e == nil {
		t.Fatal("published with no receipt")
	}
	receipt := core.Delivery{RunID: "actual-fixture-run-1", Key: "publish-once", Candidate: n.Task.Candidate, Artifact: "runtime:artifacts/receipt.json", Environment: d.Environment, Status: "success"}
	sub.Result.Delivery = &receipt
	if _, e = s.Submit(sub); e == nil {
		t.Fatal("missing receipt artifact accepted")
	}
	os.MkdirAll(filepath.Join(s.Store.Dir, "artifacts"), 0700)
	b, _ := json.Marshal(receipt)
	os.WriteFile(filepath.Join(s.Store.Dir, "artifacts/receipt.json"), b, 0600)
	n, e = s.Submit(sub)
	if e != nil || n.Action != "completed" || n.Task.Result != "released" {
		t.Fatal(n, e)
	}
	id = atVerify(t, s, d)
	n, e = s.Verify(id, "test", "C")
	if e != nil {
		t.Fatal(e)
	}
	c = n.Task.Confirmation
	s.Confirm(id, c.Kind, "approve", "owner", "approved", c.Candidate, c.Revision, c.PlanRevision)
	n, e = s.Claim(id, "", "delivery-bot", "", "")
	if e != nil {
		t.Fatal(e)
	}
	s.Resume(id, "", true)
	n, _ = s.Next(id)
	if n.Action != "wait_confirmation" {
		t.Fatal("unknown delivery automatically repeated", n)
	}
}
func TestIsolatedWorkspaceParallelWriters(t *testing.T) {
	s := fixture(t)
	worktree := t.TempDir()
	os.MkdirAll(filepath.Join(worktree, ".ael"), 0755)
	config, _ := os.ReadFile(filepath.Join(s.Root, ".ael/project.yaml"))
	os.WriteFile(filepath.Join(worktree, ".ael/project.yaml"), config, 0644)
	if _, e := s.BindWorkspace(worktree); e != nil {
		t.Fatal(e)
	}
	first := create(t, s, bug())
	second := create(t, s, bug())
	if _, e := s.Claim(first, "", "A", s.Root, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Claim(second, "", "B", worktree, ""); e != nil {
		t.Fatal("isolated directory should be writable", e)
	}
}
func TestBackupRestoreAndPruneSummary(t *testing.T) {
	s := fixture(t)
	id := atVerify(t, s, bug())
	n, e := s.Verify(id, "test", "C")
	if e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(t.TempDir(), "export")
	if _, e = s.Backup(out); e != nil {
		t.Fatal(e)
	}
	clone := t.TempDir()
	copyFile := func(rel string) {
		p := filepath.Join(clone, rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		b, e := os.ReadFile(filepath.Join(s.Root, rel))
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(p, b, 0644)
	}
	for _, rel := range []string{".ael/project.yaml", ".ael/policy.yaml", ".ael/work-items/" + id + ".md", ".ael/outcomes/" + id + ".json", "result.txt"} {
		copyFile(rel)
	}
	oldData := os.Getenv("AEL_DATA_DIR")
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	restored, _, e := bootstrap.Open(clone)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Store.Close()
	historic, e := restored.Next(id)
	if e != nil || !historic.Task.Historical || len(historic.Task.Evidence) != 0 {
		t.Fatal("Git-only clone fabricated runtime", historic, e)
	}
	if _, e = restored.Restore(out); e != nil {
		t.Fatal(e)
	}
	r, e := restored.Get(id)
	if e != nil || len(r.Attempts) != len(n.Task.Attempts) {
		t.Fatal(r, e)
	}
	for _, a := range r.Attempts {
		if a.Token != "" || a.PID != 0 || a.Status == "running" {
			t.Fatal("revived live executor")
		}
	}
	history, _ := restored.Events(id)
	if len(history) < 5 {
		t.Fatal("runtime history lost")
	}
	t.Setenv("AEL_DATA_DIR", oldData)
	preview, e := s.Prune(time.Now().Add(time.Hour), false)
	if e != nil || preview.(map[string]any)["count"].(int) == 0 {
		t.Fatal(preview, e)
	}
	if _, e = s.Prune(time.Now().Add(time.Hour), true); e != nil {
		t.Fatal(e)
	}
	n, e = s.Next(id)
	if e != nil {
		t.Fatal(e)
	}
	for _, ev := range n.Task.Evidence {
		for _, state := range ev.ArtifactStatus {
			if !strings.Contains(state, "原件已清理") {
				t.Fatal(state)
			}
		}
	}
}

func TestStrictResumeCannotSkipApprovalAndModeChangesInvalidate(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Preset = "strict"
	id := create(t, s, d)
	if _, e := s.Resume(id, "execute", true); e == nil {
		t.Fatal("forward resume skipped approval")
	}
	step(t, s, id, "definer")
	if _, e := s.Resume(id, "execute", true); e == nil {
		t.Fatal("analysis skipped")
	}
	step(t, s, id, "analyst")
	if _, e := s.Claim(id, "", "dev", "", ""); e == nil {
		t.Fatal("unapproved strict execute")
	}
	d = bug()
	id = atVerify(t, s, d)
	before, e := core.Fingerprint(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(filepath.Join(s.Root, "result.txt"), 0755); e != nil {
		t.Fatal(e)
	}
	after, e := core.Fingerprint(s.Root)
	if e != nil || before == after {
		t.Fatal("mode not bound", e)
	}
	if _, e = s.Verify(id, "test", "check"); e == nil {
		t.Fatal("mode change kept old candidate")
	}
}
func TestDeliveryReceiptBindsTargetAndKey(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Goal = "merged"
	d.Target = "branch-B"
	id := atVerify(t, s, d)
	n, e := s.Verify(id, "test", "C")
	if e != nil {
		t.Fatal(e)
	}
	c := n.Task.Confirmation
	s.Confirm(id, c.Kind, "approve", "owner", "approved branch-B", c.Candidate, c.Revision, c.PlanRevision)
	n, e = s.Claim(id, "", "delivery", "", "")
	if e != nil {
		t.Fatal(e)
	}
	a := n.Attempt
	receipt := core.Delivery{RunID: "run-1", Key: "key-A", Target: "branch-A", Candidate: n.Task.Candidate, Artifact: "runtime:artifacts/merge-receipt.json", Status: "success"}
	os.MkdirAll(filepath.Join(s.Store.Dir, "artifacts"), 0700)
	b, _ := json.Marshal(receipt)
	os.WriteFile(filepath.Join(s.Store.Dir, "artifacts/merge-receipt.json"), b, 0600)
	claimed := receipt
	claimed.Key = "key-B"
	claimed.Target = "branch-B"
	if _, e = s.Submit(core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: "mismatch", Result: core.StepResult{Status: "pass", Summary: "merge claimed", Delivery: &claimed}}); e == nil {
		t.Fatal("receipt mismatch accepted")
	}
}
func TestRestoreRefusesExistingHumanStateBeforeWritingArtifacts(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Preset = "strict"
	id := create(t, s, d)
	out := filepath.Join(t.TempDir(), "export")
	if _, e := s.Backup(out); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Pause(id, "new human decision"); e != nil {
		t.Fatal(e)
	}
	before, _ := s.Events(id)
	if _, e := s.Restore(out); e == nil {
		t.Fatal("restore overwrote human progress")
	}
	after, _ := s.Events(id)
	if len(before) != len(after) {
		t.Fatal("restore changed history despite refusal")
	}
	create(t, s, bug())
	if _, e := s.Restore(out); e == nil {
		t.Fatal("restore accepted extra task")
	}
}

func TestRealBoardPaginationCountsAndFilters(t *testing.T) {
	s := fixture(t)
	for i := 0; i < 5; i++ {
		d := bug()
		d.Title = fmt.Sprintf("分页任务%d", i)
		create(t, s, d)
	}
	p, e := s.QueryPage(core.Query{Limit: 2})
	if e != nil || len(p.Items) != 2 || p.Counts["pending"] != 5 || p.Total != 5 {
		t.Fatal(p, e)
	}
	next, e := s.QueryPage(core.Query{Limit: 2, Offset: 2})
	if e != nil || len(next.Items) != 2 || next.Items[0].Task.Definition.ID == p.Items[0].Task.Definition.ID {
		t.Fatal(next, e)
	}
	p, e = s.QueryPage(core.Query{Text: "分页任务3", Limit: 2})
	if e != nil || p.Total != 1 {
		t.Fatal(p, e)
	}
}

func TestPortableOutcomeHasNoRuntimeCredentialsOrAbsoluteWorkspace(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Title = "支持 --- 作为标题内容"
	id := atVerify(t, s, d)
	if _, e := s.Verify(id, "test", "C"); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(s.Root, ".ael/outcomes/"+id+".json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, forbidden := range []string{"lease-", "token", "attempts", s.Root, s.Store.Dir, "workspace", "pid"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatal("runtime material leaked into portable outcome", forbidden)
		}
	}
	var o core.Outcome
	if e = json.Unmarshal(b, &o); e != nil || o.Status != "completed" || o.ID != id {
		t.Fatal(o, e)
	}
}

func TestUIAndUXCannotPassWithoutRequiredRunningEvidence(t *testing.T) {
	for _, kind := range []string{"ui", "ux"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t)
			d := bug()
			d.Checks[0].Kind = kind
			d.Checks[0].Command = []string{"sh", "-c", "exit 0"}
			id := atVerify(t, s, d)
			n, e := s.Verify(id, "test", "checker")
			if e != nil || n.Action == "completed" || n.Task.Evidence[0].Result != "blocked" {
				t.Fatal(n, e)
			}
		})
	}
}

func TestRejectedPlanIsNotCancelledTask(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Preset = "strict"
	id := create(t, s, d)
	step(t, s, id, "definer")
	step(t, s, id, "analyst")
	n, _ := s.Next(id)
	c := n.Task.Confirmation
	n, e := s.Confirm(id, c.Kind, "reject", "reviewer", "方案假设不成立，回到分析", c.Candidate, c.Revision, c.PlanRevision)
	if e != nil || n.Task.Status != "open" || n.Task.Plan.Revision != 2 || n.Step != "analysis" || n.Task.Approvals[0].Decision != "reject" {
		t.Fatal(n, e)
	}
}

func TestRiskSignalCannotBeLoweredByExplicitPreset(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Preset = "fast"
	d.Risk = []string{"security contract"}
	id := create(t, s, d)
	n, e := s.Next(id)
	if e != nil || n.Task.Plan.Preset != "strict" || n.Task.Plan.Approved {
		t.Fatal(n, e)
	}
}
func TestProjectPolicyRevisionInvalidatesExistingPlan(t *testing.T) {
	s := fixture(t)
	id := atVerify(t, s, bug())
	policy, e := s.Policy()
	if e != nil {
		t.Fatal(e)
	}
	policy.Checks = bug().Checks
	policy.Checks[0].Command = []string{"sh", "-c", "exit 1"}
	b, _ := json.Marshal(policy)
	os.WriteFile(filepath.Join(s.Root, ".ael/policy.yaml"), b, 0644)
	n, e := s.Next(id)
	if e != nil || n.Revision != 2 || n.Action != "wait_confirmation" || n.Task.Confirmation == nil || n.Task.Definition.Checks[0].Command[2] != "exit 1" || len(n.Task.Evidence) != 0 {
		t.Fatal(n, e)
	}
	if _, e = s.Verify(id, "test", "checker"); e == nil {
		t.Fatal("old task silently ignored new policy")
	}
}

func TestTerminalAndConfirmationNextActionPrecedence(t *testing.T) {
	task := core.Task{Status: "cancelled", Paused: true, Failure: "old pause"}
	if n := task.BaseNext(); n.Action != "cancelled" || n.Board != "cancelled" {
		t.Fatal(n)
	}
	task.Status = "open"
	task.Confirmation = &core.Confirmation{Kind: "revision", Reason: "approve exact revision"}
	if n := task.BaseNext(); n.Action != "wait_confirmation" || n.Board != "confirmation" {
		t.Fatal(n)
	}
}

package core_test

import (
	"ael/internal/core"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func humanReviewFixture(t *testing.T, method string, goal string, extra bool) (*core.Service, string, core.ReviewExpectation) {
	t.Helper()
	s := fixture(t)
	checks := []core.Check{{ID: "owner", Kind: "review", Method: method, Covers: []string{"actual result"}, Required: true}}
	if extra {
		checks = append(checks, core.Check{ID: "other", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"actual result"}, Required: true})
	}
	d := core.Definition{Title: "人工结果", Type: "bug", Goal: goal, Environment: "isolated", Acceptance: []string{"actual result"}, Checks: checks}
	id := atVerify(t, s, d)
	if err := os.WriteFile(filepath.Join(s.Store.Dir, "review.md"), []byte("actual review evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	return s, id, core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
}

func TestHumanReviewOutcomesAndIdempotency(t *testing.T) {
	for _, result := range []string{"pass", "fail", "blocked"} {
		t.Run(result, func(t *testing.T) {
			s, id, expected := humanReviewFixture(t, "human", "verified", false)
			refs := []string{"runtime:review.md"}
			recovery := "repair the finding before checking again"
			if result == "blocked" {
				refs = nil
				recovery = "restore the unavailable test environment"
			}
			apply := func(reason string) (core.Next, error) {
				return s.ReviewResultExact(id, "owner", "reviewer", reason, recovery, result, refs, "same-request", expected)
			}
			n, err := apply("I inspected the actual result")
			if err != nil {
				t.Fatal(err)
			}
			if len(n.Task.Evidence) != 1 || n.Task.Evidence[0].Result != result || n.Task.Evidence[0].Source != "human:reviewer" {
				t.Fatalf("result not preserved: %+v", n.Task.Evidence)
			}
			switch result {
			case "pass":
				if n.Action != "completed" {
					t.Fatal("final gate did not finish", n.Action)
				}
			case "fail":
				if n.Action != "repair" || n.Task.Step != "execute" || n.Task.Evidence[0].Recovery != recovery {
					t.Fatal("failure did not route to repair", n)
				}
			case "blocked":
				if n.Action != "wait_environment" || n.Task.Blocker == nil || n.Task.Blocker.Recovery != recovery {
					t.Fatal("blocked result lost recovery", n)
				}
			}
			replayed, err := apply("I inspected the actual result")
			if err != nil || replayed.Action != n.Action {
				t.Fatal("retry failed", replayed.Action, err)
			}
			if _, err = apply("different observation"); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
				t.Fatal("changed request reused key", err)
			}
			stored, err := s.Get(id)
			if err != nil || len(stored.Evidence) != 1 {
				t.Fatal("retry duplicated evidence", stored, err)
			}
			if result == "blocked" {
				resumed, err := s.Resume(id, "", true)
				if err != nil || resumed.Action == "completed" || core.CurrentEvidence(resumed.Task, resumed.Task.Definition.Checks[0]).Result != "blocked" {
					t.Fatal("recovery became a pass", resumed, err)
				}
			}
		})
	}
}

func TestHumanReviewEligibilityAndValidation(t *testing.T) {
	for _, method := range []string{"", "human", "model", "scenario", "command"} {
		t.Run("method_"+method, func(t *testing.T) {
			s, id, expected := humanReviewFixture(t, method, "verified", false)
			n, err := s.Next(id)
			if err != nil {
				t.Fatal(err)
			}
			access := core.ReviewAccess(n, n.Task.Definition.Checks[0])
			want := method == "" || method == "human"
			if access.CanReview != want {
				t.Fatalf("method %q: %+v", method, access)
			}
			_, err = s.ReviewResultExact(id, "owner", "reviewer", "actual observation", "", "pass", []string{"runtime:review.md"}, "method", expected)
			if (err == nil) != want {
				t.Fatalf("method %q unexpectedly accepted/rejected: %v", method, err)
			}
		})
	}
	t.Run("inputs and revisions", func(t *testing.T) {
		s, id, expected := humanReviewFixture(t, "human", "verified", true)
		for _, test := range []struct {
			name, result, actor, reason, recovery string
			refs                                  []string
			expect                                core.ReviewExpectation
		}{
			{"no explicit result", "", "reviewer", "observation", "", []string{"runtime:review.md"}, expected},
			{"blank actor", "pass", "  ", "observation", "", []string{"runtime:review.md"}, expected},
			{"no evidence", "pass", "reviewer", "observation", "", nil, expected},
			{"missing evidence", "pass", "reviewer", "observation", "", []string{"runtime:missing"}, expected},
			{"failure without recovery", "fail", "reviewer", "observation", "", []string{"runtime:review.md"}, expected},
			{"wrong candidate", "pass", "reviewer", "observation", "", []string{"runtime:review.md"}, core.ReviewExpectation{Candidate: "wrong", DefinitionRevision: expected.DefinitionRevision, PlanRevision: expected.PlanRevision}},
			{"wrong revision", "pass", "reviewer", "observation", "", []string{"runtime:review.md"}, core.ReviewExpectation{Candidate: expected.Candidate, DefinitionRevision: expected.DefinitionRevision + 1, PlanRevision: expected.PlanRevision}},
			{"wrong plan", "pass", "reviewer", "observation", "", []string{"runtime:review.md"}, core.ReviewExpectation{Candidate: expected.Candidate, DefinitionRevision: expected.DefinitionRevision, PlanRevision: expected.PlanRevision + 1}},
		} {
			t.Run(test.name, func(t *testing.T) {
				if _, err := s.ReviewResultExact(id, "owner", test.actor, test.reason, test.recovery, test.result, test.refs, test.name, test.expect); err == nil {
					t.Fatal("invalid review accepted")
				}
			})
		}
		n, err := s.ReviewResultExact(id, "owner", "reviewer", "actual observation", "", "pass", []string{"runtime:review.md"}, "valid", expected)
		if err != nil || n.Action == "completed" || n.Task.Step != "verify" {
			t.Fatal("remaining gate incorrectly completed", n, err)
		}
	})
}

func TestHumanReviewConcurrentRetryAndDeliveryGate(t *testing.T) {
	s, id, expected := humanReviewFixture(t, "human", "released", false)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.ReviewResultExact(id, "owner", "reviewer", "actual observation", "", "pass", []string{"runtime:review.md"}, "parallel", expected)
		}()
	}
	wg.Wait()
	n, err := s.ReviewResultExact(id, "owner", "reviewer", "actual observation", "", "pass", []string{"runtime:review.md"}, "parallel", expected)
	if err != nil || n.Action == "completed" || n.Task.Step != "delivery" {
		t.Fatal("delivery was bypassed or replay failed", n, err)
	}
	stored, err := s.Get(id)
	if err != nil || len(stored.Evidence) != 1 {
		t.Fatal("concurrent retries duplicated evidence", stored, err)
	}
}

func TestAttentionUsesCurrentHumanEligibility(t *testing.T) {
	s, id, _ := humanReviewFixture(t, "", "verified", false)
	page, err := s.Attention()
	if err != nil || len(page.Items) == 0 || page.Items[0].Kind != "human" || page.Items[0].Task.ID != id || len(page.Items[0].Task.ManualReviews) != 1 {
		t.Fatal("legacy manual check was not surfaced", page, err)
	}
	if _, err = s.Claim(id, "owner", "other reviewer", "", ""); err != nil {
		t.Fatal(err)
	}
	page, err = s.Attention()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Kind == "human" && item.Task.ID == id {
			t.Fatal("a running check was offered for duplicate human review")
		}
	}
}

func TestAttentionCountsDistinctTasksBeyondRepresentativeCards(t *testing.T) {
	s := fixture(t)
	for i := 0; i < 4; i++ {
		atVerify(t, s, core.Definition{Title: "等待人工检查", Type: "bug", Goal: "verified", Acceptance: []string{"result"}, Checks: []core.Check{{ID: "owner", Kind: "review", Method: "human", Covers: []string{"result"}, Required: true}}})
	}
	for i := 0; i < 2; i++ {
		id := create(t, s, core.Definition{Title: "等待环境", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
		if _, err := s.Pause(id, "等待条件"); err != nil {
			t.Fatal(err)
		}
	}
	batchCount := 0
	originalSnapshot := s.SnapshotConstraints
	s.SnapshotConstraints = func() core.ConstraintResolvers { batchCount++; return originalSnapshot() }
	page, err := s.Attention()
	if err != nil || page.Truncated || page.Total != 6 || len(page.TaskIDs) != 6 || len(page.Items) != 3 {
		t.Fatal("count and representative cards must be distinct", page, err)
	}
	if batchCount != 1 {
		t.Fatal("attention should read knowledge once per scan", batchCount)
	}
	seen := map[string]bool{}
	for _, id := range page.TaskIDs {
		if seen[id] {
			t.Fatal("duplicate task counted", id)
		}
		seen[id] = true
	}
	if page.Items[0].Kind != "human" || page.Items[1].Kind != "blocked" {
		t.Fatal("representatives should preserve different attention types", page.Items)
	}
}

func TestHumanFailureInInspectionDeliversFindingsWithoutChangingSource(t *testing.T) {
	s := fixture(t)
	file := filepath.Join(s.Root, "inspected.txt")
	if err := os.WriteFile(file, []byte("unchanged inspected source"), 0644); err != nil {
		t.Fatal(err)
	}
	d := core.Definition{Title: "只读人工验收", Type: "verify", Intent: "inspect", Goal: "report", Acceptance: []string{"review source"}, Checks: []core.Check{{ID: "human", Kind: "review", Method: "human", Covers: []string{"review source"}, Required: true}}}
	id := create(t, s, d)
	step(t, s, id, "inspector")
	if err := os.WriteFile(filepath.Join(s.Store.Dir, "findings.md"), []byte("inspection finding"), 0644); err != nil {
		t.Fatal(err)
	}
	n := step(t, s, id, "inspector", "runtime:findings.md")
	expected := core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	n, err := s.ReviewResultExact(id, "human", "inspector", "发现实际问题", "由负责实现的任务修复", "fail", []string{"runtime:findings.md"}, "inspect-fail", expected)
	if err != nil || n.Action != "completed" || n.Task.Result != "report_delivered_with_findings" || n.Task.Evidence[0].Result != "fail" {
		t.Fatal("inspection failed result lost its report semantics", n, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "unchanged inspected source" {
		t.Fatal("inspection modified the source", string(data), err)
	}
}

func TestHumanVisualReviewRequiresActualScreenshot(t *testing.T) {
	s := fixture(t)
	d := core.Definition{Title: "真实视觉证据", Type: "bug", Goal: "verified", Acceptance: []string{"visual result"}, Checks: []core.Check{{ID: "visual", Kind: "ui", Method: "human", Covers: []string{"visual result"}, Required: true}}}
	id := atVerify(t, s, d)
	if err := os.WriteFile(filepath.Join(s.Store.Dir, "review.md"), []byte("actual review"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	expected := core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	_, err = s.ReviewResultExact(id, "visual", "reviewer", "inspected", "", "pass", []string{"runtime:review.md"}, "no-image", expected)
	if err == nil || !strings.Contains(err.Error(), "SCREENSHOT_REQUIRED") {
		t.Fatal("visual review accepted without a screenshot", err)
	}
	file, err := os.Create(filepath.Join(s.Store.Dir, "screenshot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	n, err = s.ReviewResultExact(id, "visual", "reviewer", "inspected screenshot", "", "pass", []string{"runtime:screenshot.png"}, "with-image", expected)
	if err != nil || n.Action != "completed" {
		t.Fatal("valid screenshot was not accepted", n, err)
	}
}

func TestBlockedHumanReviewCanResumeItsOwnCurrentCheck(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{}
	for _, id := range []string{"one", "two", "three"} {
		checks = append(checks, core.Check{ID: id, Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"result"}, Required: true})
	}
	checks = append(checks,
		core.Check{ID: "ui", Kind: "ui", Method: "human", Covers: []string{"result"}, Required: true},
		core.Check{ID: "ux", Kind: "ux", Method: "human", Covers: []string{"result"}, Required: true})
	id := atVerify(t, s, core.Definition{Title: "等待人工验收", Type: "feature", Goal: "verified", Acceptance: []string{"result"}, Checks: checks})
	for _, name := range []string{"one", "two", "three"} {
		if _, err := s.Verify(id, name, "isolated"); err != nil {
			t.Fatal(name, err)
		}
	}
	n, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	expected := core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	if _, err = s.Assess(core.Assessment{Task: id, Check: "ui", Candidate: expected.Candidate, Method: "human", Executor: "reviewer", Result: "blocked", Reason: "awaiting human review"}); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.Next(id)
	if err != nil || !blocked.Task.Paused || core.CurrentEvidence(blocked.Task, checks[3]).Result != "blocked" {
		t.Fatal(blocked, err)
	}
	if _, err = s.ResumeBlockedHumanReview(id, "ux", expected); err == nil || !strings.Contains(err.Error(), "REVIEW_RESUME_NOT_ALLOWED") {
		t.Fatal("another check must not clear the blocker", err)
	}
	bad := expected
	bad.PlanRevision++
	if _, err = s.ResumeBlockedHumanReview(id, "ui", bad); err == nil || !strings.Contains(err.Error(), "REVIEW_VERSION_MISMATCH") {
		t.Fatal("a stale revision must not resume", err)
	}
	resumed, err := s.ResumeBlockedHumanReview(id, "ui", expected)
	if err != nil || resumed.Task.Paused || resumed.Task.Candidate != expected.Candidate || len(resumed.Task.Evidence) != 4 || core.CurrentEvidence(resumed.Task, checks[3]).Result != "blocked" {
		t.Fatal("resume must preserve the candidate and evidence", resumed, err)
	}
	file, err := os.Create(filepath.Join(s.Store.Dir, "ui.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReviewResultExact(id, "ui", "reviewer", "looked at the actual page", "", "pass", []string{"runtime:ui.png"}, "ui-result", expected); err != nil {
		t.Fatal(err)
	}
	current, err := s.Next(id)
	if err != nil || current.Task.Status != "open" || core.CurrentEvidence(current.Task, checks[3]).Result != "pass" || core.CurrentEvidence(current.Task, checks[4]) != nil {
		t.Fatal("the other human check must remain outstanding", current, err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "ux.md"), []byte("interaction review"), 0600); err != nil {
		t.Fatal(err)
	}
	final, err := s.ReviewResultExact(id, "ux", "reviewer", "checked the interaction", "", "pass", []string{"runtime:ux.md"}, "ux-result", expected)
	if err != nil || final.Task.Status != "completed" {
		t.Fatal("all required checks should complete normally", final, err)
	}
}

func TestBlockedHumanReviewDoesNotResumeStaleOrExplicitPause(t *testing.T) {
	for _, scenario := range []string{"candidate changed", "explicit pause", "explicit pause same reason"} {
		t.Run(scenario, func(t *testing.T) {
			s, id, expected := humanReviewFixture(t, "human", "verified", false)
			if _, err := s.ReviewResultExact(id, "owner", "reviewer", "waiting for review", "continue later", "blocked", nil, "blocked", expected); err != nil {
				t.Fatal(err)
			}
			if scenario == "candidate changed" {
				if err := os.WriteFile(filepath.Join(s.Root, "changed.txt"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				reason := "explicit pause"
				if scenario == "explicit pause same reason" {
					reason = "waiting for review"
				}
				if _, err := s.Pause(id, reason); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.ResumeBlockedHumanReview(id, "owner", expected)
			want := "CANDIDATE_CHANGED"
			if scenario != "candidate changed" {
				want = "REVIEW_RESUME_NOT_ALLOWED"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatal("unexpected resume outcome", err)
			}
			current, err := s.Next(id)
			if err != nil || !current.Task.Paused || current.Task.Candidate != expected.Candidate || len(current.Task.Evidence) != 1 {
				t.Fatal("a rejected resume changed the task", current, err)
			}
		})
	}
}

func TestBlockedHumanReviewTracksLatestBlockerWithSameReason(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{
		{ID: "ui", Kind: "review", Method: "human", Required: true, Covers: []string{"result"}},
		{ID: "ux", Kind: "review", Method: "human", Required: true, Covers: []string{"result"}},
	}
	id := atVerify(t, s, core.Definition{Title: "同理由阻塞", Type: "bug", Goal: "verified", Acceptance: []string{"result"}, Checks: checks})
	n, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	expected := core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	block := func(check, key string) {
		t.Helper()
		if _, err := s.ReviewResultExact(id, check, "reviewer", "waiting for review", "continue later", "blocked", nil, key, expected); err != nil {
			t.Fatal(err)
		}
	}
	block("ui", "ui-blocked")
	if _, err = s.ResumeBlockedHumanReview(id, "ui", expected); err != nil {
		t.Fatal(err)
	}
	block("ux", "ux-blocked")
	status, err := s.ReviewCandidateStatus(id)
	if err != nil || status["blocked_check"] != "ux" {
		t.Fatal("the latest blocker should own the resume entry", status, err)
	}
	if _, err = s.ResumeBlockedHumanReview(id, "ui", expected); err == nil {
		t.Fatal("the earlier blocker must not resume the newer one")
	}
	if _, err = s.ResumeBlockedHumanReview(id, "ux", expected); err != nil {
		t.Fatal(err)
	}
}

func TestBlockedHumanReviewSurvivesLaterParallelCheckResult(t *testing.T) {
	s := fixture(t)
	signal := filepath.Join(s.Store.Dir, "finish-check")
	defer os.WriteFile(signal, []byte("release"), 0600)
	checks := []core.Check{
		{ID: "command", Kind: "functional", Method: "command", Command: []string{"sh", "-c", `while [ ! -f "$1" ]; do sleep .02; done`, "sh", signal}, Required: true, Covers: []string{"result"}},
		{ID: "ui", Kind: "review", Method: "human", Required: true, Covers: []string{"result"}},
	}
	id := atVerify(t, s, core.Definition{Title: "并发检查", Type: "bug", Goal: "verified", Acceptance: []string{"result"}, Checks: checks})
	n, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	expected := core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	done := make(chan error, 1)
	go func() { _, err := s.Verify(id, "command", "parallel"); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := s.Next(id)
		if err != nil {
			t.Fatal(err)
		}
		if current.Action == "wait_executor" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("parallel check did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = s.ReviewResultExact(id, "ui", "reviewer", "waiting for review", "continue later", "blocked", nil, "parallel-blocked", expected); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(signal, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	status, err := s.ReviewCandidateStatus(id)
	if err != nil || status["blocked_check"] != "ui" {
		t.Fatal("later parallel pass hid the blocker", status, err)
	}
	resumed, err := s.ResumeBlockedHumanReview(id, "ui", expected)
	if err != nil || resumed.Task.Paused || core.CurrentEvidence(resumed.Task, checks[0]).Result != "pass" {
		t.Fatal("parallel pass or resume lost", resumed, err)
	}
}

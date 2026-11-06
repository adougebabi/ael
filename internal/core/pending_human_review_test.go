package core_test

import (
	"ael/internal/core"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPendingHumanReviewRecordsWithoutChangingWorkOrGates(t *testing.T) {
	s := fixture(t)
	d := core.Definition{Title: "人工先记录观察", Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}, Checks: []core.Check{
		{ID: "model", Kind: "review", Method: "model", Covers: []string{"实际结果"}, Required: true},
		{ID: "owner", Kind: "review", Method: "human", Covers: []string{"实际结果"}, Required: true},
	}}
	id := create(t, s, d)
	step(t, s, id, "worker")
	if err := os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("real output"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := s.Claim(id, "", "worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	attempt := n.Attempt
	expected := core.ReviewExpectation{Candidate: "", DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	apply := func(reason string) (core.HumanReviewNote, error) {
		return s.RecordPendingReview(id, "", "reviewer", reason, "", "pass", nil, "pending-key", expected)
	}
	note, err := apply("我已检查页面的实际行为")
	if err != nil || note.Status != "pending" || note.Candidate != "" {
		t.Fatal(note, err)
	}
	again, err := apply("我已检查页面的实际行为")
	if err != nil || again.ID != note.ID {
		t.Fatal("retry created another note", again, err)
	}
	if _, err = apply("different observation"); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal(err)
	}
	if _, err = s.RecordPendingReview(id, "model", "reviewer", "observation", "", "pass", nil, "wrong-method", expected); err == nil || !strings.Contains(err.Error(), "CHECK_METHOD_MISMATCH") {
		t.Fatal(err)
	}
	bad := expected
	bad.DefinitionRevision++
	if _, err = s.RecordPendingReview(id, "", "reviewer", "observation", "", "pass", nil, "wrong-revision", bad); err == nil || !strings.Contains(err.Error(), "REVIEW_VERSION_MISMATCH") {
		t.Fatal(err)
	}
	current, err := s.Next(id)
	if err != nil || current.Step != "execute" || current.Task.Candidate != "" || len(current.Task.Evidence) != 0 || len(current.Task.HumanReviewNotes) != 1 || current.Task.Attempts[len(current.Task.Attempts)-1].Status != "running" {
		t.Fatal(current, err)
	}
	summary, err := s.Summarize(current)
	if err != nil || summary.PendingReviewNotes != 1 || summary.Checks.RequiredPassed != 0 || summary.LastResult != "我已检查页面的实际行为" {
		t.Fatal(summary, err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Root, ".ael", "records", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint core.TaskRecord
	if err = json.Unmarshal(raw, &checkpoint); err != nil || len(checkpoint.HumanReviewNotes) != 1 || checkpoint.HumanReviewNotes[0].ID != note.ID {
		t.Fatal(checkpoint, err)
	}
	n, err = s.Submit(core.Submission{Task: id, Attempt: attempt.ID, Token: attempt.Token, Key: "actual-work", Result: core.StepResult{Status: "pass", Summary: "actual work", Artifacts: []string{"result.txt"}}})
	if err != nil || n.Step != "verify" {
		t.Fatal(n, err)
	}
	exact := core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	wrong := exact
	wrong.Candidate = "stale"
	if _, err = s.ConfirmPendingReview(id, note.ID, "reviewer", "bad-confirm", wrong); err == nil || !strings.Contains(err.Error(), "REVIEW_VERSION_MISMATCH") {
		t.Fatal(err)
	}
	confirmed, err := s.ConfirmPendingReview(id, note.ID, "reviewer", "confirm-key", exact)
	if err != nil || confirmed.Status != "confirmed" || confirmed.ConfirmedCandidate != exact.Candidate {
		t.Fatal(confirmed, err)
	}
	replayed, err := s.ConfirmPendingReview(id, note.ID, "reviewer", "confirm-key", exact)
	if err != nil || replayed.ID != note.ID {
		t.Fatal(replayed, err)
	}
	current, err = s.Next(id)
	if err != nil || current.Task.Status != "open" || len(current.Task.Evidence) != 0 || current.Step != "verify" {
		t.Fatal("a note must not pass checks", current, err)
	}
}

func TestPendingHumanReviewCanBeExplicitlyUsedForARealHumanCheck(t *testing.T) {
	s := fixture(t)
	d := core.Definition{Title: "人工意见转为正式检查", Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}, Checks: []core.Check{
		{ID: "owner", Kind: "review", Method: "human", Covers: []string{"实际结果"}, Required: true},
		{ID: "other", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"实际结果"}, Required: true},
	}}
	id := create(t, s, d)
	step(t, s, id, "worker")
	if err := os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("real output"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := s.Claim(id, "", "worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	a := n.Attempt
	note, err := s.RecordPendingReview(id, "", "reviewer", "已经实际查看", "", "pass", nil, "note-key", core.ReviewExpectation{DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision})
	if err != nil {
		t.Fatal(err)
	}
	n, err = s.Submit(core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: "actual-work", Result: core.StepResult{Status: "pass", Summary: "actual work", Artifacts: []string{"result.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	expected := core.ReviewExpectation{Candidate: n.Task.Candidate, DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	if err := os.WriteFile(filepath.Join(s.Store.Dir, "review.md"), []byte("real review evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err = s.ReviewResultWithNoteExact(id, "owner", "reviewer", "重新核对当前候选", "", "pass", []string{"runtime:review.md"}, "formal-key", expected, note.ID)
	if err != nil || n.Task.Status != "open" || len(n.Task.Evidence) != 1 || n.Task.Evidence[0].ReviewNoteID != note.ID || n.Task.HumanReviewNotes[0].ConfirmedCandidate != expected.Candidate {
		t.Fatal(n, err)
	}
	replayed, err := s.ReviewResultWithNoteExact(id, "owner", "reviewer", "重新核对当前候选", "", "pass", []string{"runtime:review.md"}, "formal-key", expected, note.ID)
	if err != nil || len(replayed.Task.Evidence) != 1 || replayed.Task.HumanReviewNotes[0].Status != "confirmed" {
		t.Fatal(replayed, err)
	}
	if _, err = s.ReviewResultWithNoteExact(id, "owner", "reviewer", "重新核对当前候选", "", "pass", []string{"runtime:review.md"}, "formal-key", expected, "another-note"); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal(err)
	}
}

func TestPendingHumanReviewConcurrentRetry(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "并发人工记录", Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}})
	n, err := s.Next(id)
	if err != nil {
		t.Fatal(err)
	}
	expected := core.ReviewExpectation{DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	var notes [2]core.HumanReviewNote
	var errors [2]error
	var wg sync.WaitGroup
	for i := range notes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			notes[i], errors[i] = s.RecordPendingReview(id, "", "reviewer", "同一观察", "", "blocked", nil, "same-key", expected)
		}(i)
	}
	wg.Wait()
	if errors[0] != nil || errors[1] != nil || notes[0].ID != notes[1].ID {
		t.Fatal(notes, errors)
	}
	stored, err := s.Get(id)
	if err != nil || len(stored.HumanReviewNotes) != 1 {
		t.Fatal(stored, err)
	}
}

func TestPendingHumanReviewConcurrentWithWorkSubmission(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "并发实施与人工意见", Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}, Checks: []core.Check{{ID: "owner", Kind: "review", Method: "human", Covers: []string{"实际结果"}, Required: true}}})
	step(t, s, id, "worker")
	if err := os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("real output"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := s.Claim(id, "", "worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	attempt := n.Attempt
	expected := core.ReviewExpectation{DefinitionRevision: n.Revision, PlanRevision: n.PlanRevision}
	start := make(chan struct{})
	var reviewErr, submitErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, reviewErr = s.RecordPendingReview(id, "", "reviewer", "当前观察", "", "pass", nil, "concurrent-note", expected)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, submitErr = s.Submit(core.Submission{Task: id, Attempt: attempt.ID, Token: attempt.Token, Key: "concurrent-work", Result: core.StepResult{Status: "pass", Summary: "real work", Artifacts: []string{"result.txt"}}})
	}()
	close(start)
	wg.Wait()
	if submitErr != nil {
		t.Fatal(submitErr)
	}
	if reviewErr != nil && !strings.Contains(reviewErr.Error(), "REVIEW_VERSION_MISMATCH") {
		t.Fatal(reviewErr)
	}
	current, err := s.Next(id)
	if err != nil || current.Step != "verify" || current.Task.Status != "open" || len(current.Task.Evidence) != 0 {
		t.Fatal(current, err)
	}
	if reviewErr == nil && len(current.Task.HumanReviewNotes) != 1 {
		t.Fatal("accepted opinion was lost", current.Task.HumanReviewNotes)
	}
	if reviewErr != nil && len(current.Task.HumanReviewNotes) != 0 {
		t.Fatal("stale opinion was saved", current.Task.HumanReviewNotes)
	}
}

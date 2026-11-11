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
	"testing"
)

func TestEpicWaitsForParallelRecheckBeforeClosing(t *testing.T) {
	s := fixture(t)
	marker := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(marker, []byte("pass"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := integrationParent(t, s, "epic", []core.Check{
		parentCheck("rechecked", fmt.Sprintf("test -f %q", marker)),
		parentCheck("other", "true"),
	})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	if first, err := s.Verify(parent, "rechecked", "first"); err != nil || first.Task.Status != "open" {
		t.Fatalf("first check: %+v %v", first, err)
	}
	recheck, err := s.Claim(parent, "rechecked", "rechecker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Claim(parent, "other", "checker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.VerifyClaim(parent, "other", "checker", other.Attempt.ID, other.Attempt.Token)
	if err != nil || result.Task.Status != "open" {
		t.Fatalf("parent closed during a running recheck: %+v %v", result.Task, err)
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	result, err = s.VerifyClaim(parent, "rechecked", "rechecker", recheck.Attempt.ID, recheck.Attempt.Token)
	if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 3 || result.Task.EvidenceHistory[2].Result != "fail" {
		t.Fatalf("later failed recheck lost: %+v %v", result.Task, err)
	}
	if err = os.WriteFile(marker, []byte("pass"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(parent, "rechecked", "rechecker"); err != nil {
		t.Fatal(err)
	}
	result, err = s.Verify(parent, "other", "checker")
	if err != nil || result.Task.Status != "completed" || len(result.Task.EvidenceHistory) != 3 || result.Task.EvidenceHistory[2].Result != "fail" {
		t.Fatalf("stable rechecks did not complete or altered historical failure: %+v %v", result.Task, err)
	}
}

func TestEpicPreservesChangedCandidateAssessmentAsHistory(t *testing.T) {
	for _, status := range []string{"pass", "fail"} {
		t.Run(status, func(t *testing.T) {
			s := fixture(t)
			check := core.Check{ID: "review", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true}
			parent := integrationParent(t, s, "epic", []core.Check{check})
			completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
			if _, err := s.PrepareEpicIntegration(parent); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("isolated assessment report"), 0600); err != nil {
				t.Fatal(err)
			}
			claim, err := s.Claim(parent, "review", "reviewer", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(s.Root, "leaf.txt"), []byte("changed after claim"), 0600); err != nil {
				t.Fatal(err)
			}
			r := core.Assessment{Task: parent, Check: "review", Candidate: claim.Task.Candidate, Method: "model", Executor: "reviewer", Result: status, Reason: "actual assessment", Inputs: []string{"leaf.txt"}, Artifacts: []string{"runtime:report.md"}}
			result, err := s.AssessExisting(r, claim.Attempt.ID, claim.Attempt.Token)
			if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 1 {
				t.Fatalf("changed-candidate assessment not retained as history: %+v %v", result.Task, err)
			}
			ev := result.Task.EvidenceHistory[0]
			if ev.Result != status || ev.Candidate != claim.Task.Candidate || ev.Reason != r.Reason || ev.ArtifactStatus["leaf.txt"] != "unverified_after_candidate_change" {
				t.Fatalf("wrong historical assessment: %+v", ev)
			}
			if _, ok := ev.ArtifactHashes["leaf.txt"]; ok || ev.ArtifactHashes["runtime:report.md"] == "" {
				t.Fatalf("workspace input was falsely bound to the old candidate or runtime report was lost: %+v", ev.ArtifactHashes)
			}
			if result.Task.Integration == nil || result.Task.Integration.StaleReason == "" {
				t.Fatal("changed parent candidate was not marked stale")
			}
		})
	}
}

func TestEpicDoesNotCloseWhileOtherWorkspaceWriterIsActive(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	other := create(t, s, core.Definition{ID: "other", Title: "独立任务", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	step(t, s, other, "other-worker")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	writer, err := s.Claim(other, "", "other-worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Verify(parent, "integration", "checker")
	if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 1 || result.Action != "wait_integration" {
		t.Fatalf("parent closed or discarded evidence while another writer was active: %+v %v", result.Task, err)
	}
	_, err = s.Submit(core.Submission{Task: other, Attempt: writer.Attempt.ID, Token: writer.Attempt.Token, Key: "other-writer-finished", Result: core.StepResult{Status: "pass", Summary: "writer finished", Artifacts: []string{"leaf.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	result, err = s.Verify(parent, "integration", "checker")
	if err != nil || result.Task.Status != "completed" || len(result.Task.EvidenceHistory) != 1 {
		t.Fatalf("stable parent failed to recheck after writer stopped: %+v %v", result.Task, err)
	}
}

func TestEpicWaitsForPendingCandidateFileAndDoesNotReuseOldCheck(t *testing.T) {
	s := fixture(t)
	check := core.Check{ID: "review", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true}
	parent := integrationParent(t, s, "epic", []core.Check{check})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("isolated report"), 0600); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(parent, "review", "reviewer", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Write(func(tx *store.Tx) error {
		return tx.File(filepath.Join(s.Root, "pending.txt"), []byte("pending candidate change"))
	}); err != nil {
		t.Fatal(err)
	}
	r := core.Assessment{Task: parent, Check: "review", Candidate: claim.Task.Candidate, Method: "model", Executor: "reviewer", Result: "pass", Reason: "assessment before queued write", Inputs: []string{"leaf.txt"}, Artifacts: []string{"runtime:report.md"}}
	result, err := s.AssessExisting(r, claim.Attempt.ID, claim.Attempt.Token)
	if err != nil || result.Task.Status != "open" || result.Action != "wait_integration" || len(result.Task.Evidence) != 1 {
		t.Fatalf("pending candidate update allowed completion or discarded evidence: %+v %v", result.Task, err)
	}
	result, err = s.Resume(parent, "", true)
	if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 1 {
		t.Fatalf("parent reused check after queued candidate update: %+v %v", result.Task, err)
	}
}

func TestEpicDeliveryReceiptWaitsForWriterThenResumes(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "隔离发布", Type: "epic", Goal: "released", Environment: "fixture", Acceptance: []string{"整体"}, Checks: []core.Check{parentCheck("integration", "true")}})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	other := create(t, s, core.Definition{ID: "other", Title: "独立任务", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	step(t, s, other, "other-worker")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	n, err := s.Verify(parent, "integration", "checker")
	if err != nil || n.Task.Confirmation == nil {
		t.Fatalf("delivery confirmation missing: %+v %v", n.Task, err)
	}
	c := n.Task.Confirmation
	if _, err = s.Confirm(parent, c.Kind, "approve", "owner", "synthetic delivery", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatal(err)
	}
	writer, err := s.Claim(other, "", "other-worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.Claim(parent, "", "delivery-worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	receipt := core.Delivery{RunID: "fixture-run", Key: "fixture-key", Candidate: delivery.Task.Candidate, Artifact: "runtime:receipt.json", Environment: "fixture", Status: "success"}
	raw, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "receipt.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.Submit(core.Submission{Task: parent, Attempt: delivery.Attempt.ID, Token: delivery.Attempt.Token, Key: "synthetic-delivery", Result: core.StepResult{Status: "pass", Summary: "synthetic receipt", Delivery: &receipt}})
	if err != nil || result.Task.Status != "open" || result.Task.Delivery == nil || result.Action != "wait_integration" {
		t.Fatalf("parent did not retain receipt while waiting: %+v %v", result.Task, err)
	}
	if _, err = s.Submit(core.Submission{Task: other, Attempt: writer.Attempt.ID, Token: writer.Attempt.Token, Key: "other-writer-finished", Result: core.StepResult{Status: "pass", Summary: "writer finished", Artifacts: []string{"leaf.txt"}}}); err != nil {
		t.Fatal(err)
	}
	result, err = s.Resume(parent, "", true)
	if err != nil || result.Task.Status != "completed" || result.Task.Result != "released" || result.Task.Delivery == nil || result.Task.Delivery.RunID != receipt.RunID {
		t.Fatalf("delivery did not resume from saved receipt: %+v %v", result.Task, err)
	}
}

func TestEpicWaitingOnCompletedTaskCanRecoverStoppedAttempt(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.ID = "other"
	check := d.Checks[0]
	check.ID = "contract"
	d.Checks = append(d.Checks, check)
	other := atVerify(t, s, d)
	if _, err := s.Verify(other, "test", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(other, "test", "recheck", "", ""); err != nil {
		t.Fatal(err)
	}
	completed, err := s.Verify(other, "contract", "last")
	if err != nil || completed.Task.Status != "completed" {
		t.Fatalf("legacy terminal state: %+v %v", completed.Task, err)
	}
	completedAt := *completed.Task.Completed
	parent := integrationParent(t, s, "parent", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err = s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Verify(parent, "integration", "checker")
	if err != nil || waiting.Action != "wait_integration" {
		t.Fatalf("unconfirmed old attempt was ignored: %+v %v", waiting.Task, err)
	}
	if _, err = s.Resume(other, "", false); err == nil {
		t.Fatal("old attempt recovered without stopped-process acknowledgement")
	}
	recovered, err := s.Resume(other, "", true)
	if err != nil || recovered.Task.Status != "completed" || !recovered.Task.Completed.Equal(completedAt) {
		t.Fatalf("could not recover old attempt without changing completed result: %+v %v", recovered.Task, err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	result, err := s.Verify(parent, "integration", "checker")
	if err != nil || result.Task.Status != "completed" || len(result.Task.EvidenceHistory) != 1 {
		t.Fatalf("parent did not recheck after old attempt stopped: %+v %v", result.Task, err)
	}
}

func TestEpicCanPrepareAgainAfterExplicitReturnToVerify(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "parent", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	other := create(t, s, core.Definition{ID: "other", Title: "other", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
	step(t, s, other, "writer")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	writer, err := s.Claim(other, "", "writer", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(parent, "integration", "checker"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: other, Attempt: writer.Attempt.ID, Token: writer.Attempt.Token, Key: "writer-stopped", Result: core.StepResult{Status: "pass", Summary: "stopped", Artifacts: []string{"leaf.txt"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(parent, "verify", true); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareEpicIntegration(parent)
	if err != nil || prepared.Task.Candidate == "" || prepared.Task.Integration.WaitReason != "" {
		t.Fatalf("integration was not rebuilt: %+v %v", prepared.Task, err)
	}
	result, err := s.Verify(parent, "integration", "checker")
	if err != nil || result.Task.Status != "completed" {
		t.Fatalf("rebuilt integration could not complete: %+v %v", result.Task, err)
	}
}

func TestEpicCanReconcileOldDeliveryAfterCandidateChanges(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "parent", Title: "release", Type: "epic", Goal: "released", Environment: "fixture", Acceptance: []string{"整体"}, Checks: []core.Check{parentCheck("integration", "test -s leaf.txt", "leaf")}})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	other := create(t, s, core.Definition{ID: "other", Title: "other", Type: "bug", Goal: "verified", Scope: []string{"new.txt"}, Acceptance: []string{"result"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s new.txt"}, Covers: []string{"result"}, Required: true}}})
	step(t, s, other, "writer")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	n, err := s.Verify(parent, "integration", "checker")
	if err != nil || n.Task.Confirmation == nil {
		t.Fatalf("delivery confirmation missing: %+v %v", n.Task, err)
	}
	c := n.Task.Confirmation
	if _, err = s.Confirm(parent, c.Kind, "approve", "owner", "fixture", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatal(err)
	}
	writer, err := s.Claim(other, "", "writer", "", "")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.Claim(parent, "", "delivery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	receipt := core.Delivery{RunID: "run", Key: "key", Candidate: delivery.Task.Candidate, Artifact: "runtime:receipt.json", Environment: "fixture", Status: "success"}
	raw, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "receipt.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: parent, Attempt: delivery.Attempt.ID, Token: delivery.Attempt.Token, Key: "delivery-done", Result: core.StepResult{Status: "pass", Summary: "receipt", Delivery: &receipt}}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "new.txt"), []byte("new writer output"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: other, Attempt: writer.Attempt.ID, Token: writer.Attempt.Token, Key: "writer-done", Result: core.StepResult{Status: "pass", Summary: "stopped", Artifacts: []string{"new.txt"}}}); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Verify(other, "unit", "other-checker"); err != nil || result.Task.Status != "completed" {
		t.Fatalf("new work was not independently verified: %+v %v", result.Task, err)
	}
	if _, err = s.LinkEpic(parent, other, false, ""); err != nil {
		t.Fatal(err)
	}
	stale, err := s.Resume(parent, "", true)
	if err != nil || stale.Task.Confirmation == nil || stale.Task.Delivery == nil {
		t.Fatalf("changed candidate did not retain receipt for reconciliation: %+v %v", stale.Task, err)
	}
	c = stale.Task.Confirmation
	if _, err = s.Confirm(parent, c.Kind, "approve", "owner", "not valid for new candidate", c.Candidate, c.Revision, c.PlanRevision); err == nil {
		t.Fatal("approved old delivery against a changed candidate")
	}
	if _, err = s.Confirm(parent, c.Kind, "reject", "owner", "old run reconciled", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatalf("could not reconcile old run: %v", err)
	}
	prepared, err := s.PrepareEpicIntegration(parent)
	if err != nil || prepared.Task.Candidate == receipt.Candidate || prepared.Task.Delivery == nil || prepared.Task.Delivery.RunID != receipt.RunID {
		t.Fatalf("new candidate or old receipt was lost: %+v %v", prepared.Task, err)
	}
	n, err = s.Verify(parent, "integration", "checker")
	if err != nil || n.Task.Status != "open" || n.Step != "delivery" || n.Task.Confirmation == nil {
		t.Fatalf("new candidate bypassed fresh delivery decision: %+v %v", n.Task, err)
	}
}

func TestEpicDoesNotCloseWhileParallelCheckHasBlockedTask(t *testing.T) {
	s := fixture(t)
	optional := core.Check{ID: "optional", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: false}
	parent := integrationParent(t, s, "parent", []core.Check{parentCheck("required", "true"), optional})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	required, err := s.Claim(parent, "required", "checker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	check, err := s.Claim(parent, "optional", "reviewer", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r := core.Assessment{Task: parent, Check: "optional", Candidate: check.Task.Candidate, Method: "model", Executor: "reviewer", Result: "blocked", Reason: "checker unavailable"}
	blocked, err := s.AssessExisting(r, check.Attempt.ID, check.Attempt.Token)
	if err != nil || !blocked.Task.Paused {
		t.Fatalf("blocked check was not retained: %+v %v", blocked.Task, err)
	}
	result, err := s.VerifyClaim(parent, "required", "checker", required.Attempt.ID, required.Attempt.Token)
	if err != nil || result.Task.Status != "open" || !result.Task.Paused {
		t.Fatalf("paused parent completed after a peer check: %+v %v", result.Task, err)
	}
}

func TestEpicAssessmentRetainsResultWhenInspectedInputIsRemoved(t *testing.T) {
	for _, inspect := range []bool{false, true} {
		t.Run(fmt.Sprint("inspect-", inspect), func(t *testing.T) {
			s := fixture(t)
			check := core.Check{ID: "review", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true}
			d := core.Definition{ID: "parent", Title: "parent", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}, Checks: []core.Check{check}}
			if inspect {
				d.Intent, d.Goal = "inspect", "report"
			}
			parent := create(t, s, d)
			completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
			if _, err := s.PrepareEpicIntegration(parent); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("real report"), 0600); err != nil {
				t.Fatal(err)
			}
			claim, err := s.Claim(parent, "review", "reviewer", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(filepath.Join(s.Root, "leaf.txt")); err != nil {
				t.Fatal(err)
			}
			r := core.Assessment{Task: parent, Check: "review", Candidate: claim.Task.Candidate, Method: "model", Executor: "reviewer", Result: "fail", Reason: "observed findings", Inputs: []string{"leaf.txt"}, Artifacts: []string{"runtime:report.md"}}
			result, err := s.AssessExisting(r, claim.Attempt.ID, claim.Attempt.Token)
			if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 1 {
				t.Fatalf("assessment with removed input was discarded: %+v %v", result.Task, err)
			}
			ev := result.Task.EvidenceHistory[0]
			if ev.Result != "fail" || ev.ArtifactStatus["leaf.txt"] != "missing_after_candidate_change" || ev.ArtifactHashes["runtime:report.md"] == "" {
				t.Fatalf("historical result does not describe missing input: %+v", ev)
			}
		})
	}
}

func TestEpicChangedCandidateRetainsActualCommandExit(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(fmt.Sprint("exit-", code), func(t *testing.T) {
			s := fixture(t)
			parent := integrationParent(t, s, "parent", []core.Check{parentCheck("integration", fmt.Sprintf("printf changed > leaf.txt; exit %d", code))})
			completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
			if _, err := s.PrepareEpicIntegration(parent); err != nil {
				t.Fatal(err)
			}
			result, err := s.Verify(parent, "integration", "checker")
			if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 1 {
				t.Fatalf("changed check not kept as history: %+v %v", result.Task, err)
			}
			want := "pass"
			if code != 0 {
				want = "fail"
			}
			if ev := result.Task.EvidenceHistory[0]; ev.Result != want || ev.Recovery == "" || len(ev.Artifacts) != 1 {
				t.Fatalf("lost command exit or invalidation reason: %+v", ev)
			}
		})
	}
}

func TestEpicRejectsEvidenceFromTemporarilyChangedWorkspace(t *testing.T) {
	for _, mode := range []string{"writer-before-check", "writer-after-check", "writer-deleted"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t)
			check := core.Check{ID: "review", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true}
			parent := integrationParent(t, s, "parent", []core.Check{check})
			completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
			other := create(t, s, core.Definition{ID: "other", Title: "other", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
			step(t, s, other, "writer")
			if _, err := s.PrepareEpicIntegration(parent); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Root, "leaf.txt")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var writer core.Next
			if mode == "writer-before-check" {
				writer, err = s.Claim(other, "", "writer", "", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			claim, err := s.Claim(parent, "review", "reviewer", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if mode != "writer-before-check" {
				writer, err = s.Claim(other, "", "writer", "", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(path, []byte("temporarily different content"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("reviewer observed different content"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Submit(core.Submission{Task: other, Attempt: writer.Attempt.ID, Token: writer.Attempt.Token, Key: "writer-finished", Result: core.StepResult{Status: "pass", Summary: "writer stopped", Artifacts: []string{"leaf.txt"}}}); err != nil {
				t.Fatal(err)
			}
			if mode == "writer-deleted" {
				if _, err = s.Delete(other, "isolated test cleanup"); err != nil {
					t.Fatal(err)
				}
			}
			r := core.Assessment{Task: parent, Check: "review", Candidate: claim.Task.Candidate, Method: "model", Executor: "reviewer", Result: "pass", Reason: "report from overlapping activity", Inputs: []string{"leaf.txt"}, Artifacts: []string{"runtime:report.md"}}
			result, err := s.AssessExisting(r, claim.Attempt.ID, claim.Attempt.Token)
			if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 1 {
				t.Fatalf("overlapping writer allowed unverified candidate evidence: %+v %v", result.Task, err)
			}
			if result.Task.Integration == nil || result.Task.Integration.StaleReason == "" || result.Task.EvidenceHistory[0].Recovery == "" {
				t.Fatal("overlap and revalidation reason were not retained")
			}
		})
	}
}

func TestEpicRejectsHumanReviewAfterOverlappingWorkspaceAttempt(t *testing.T) {
	s := fixture(t)
	check := core.Check{ID: "human", Kind: "manual", Method: "human", Covers: []string{"整体"}, Required: true}
	parent := integrationParent(t, s, "parent", []core.Check{check})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	other := create(t, s, core.Definition{ID: "other", Title: "other", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
	step(t, s, other, "writer")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(parent, "human", "reviewer", "", "")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := s.Claim(other, "", "writer", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: other, Attempt: writer.Attempt.ID, Token: writer.Attempt.Token, Key: "writer-finished", Result: core.StepResult{Status: "pass", Summary: "writer stopped", Artifacts: []string{"leaf.txt"}}}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "human.md"), []byte("human review report"), 0600); err != nil {
		t.Fatal(err)
	}
	r := core.Assessment{Task: parent, Check: "human", Candidate: claim.Task.Candidate, Method: "human", Executor: "reviewer", Result: "pass", Reason: "reviewed", Inputs: []string{"leaf.txt"}, Artifacts: []string{"runtime:human.md"}}
	if _, err = s.AssessExisting(r, claim.Attempt.ID, claim.Attempt.Token); err == nil {
		t.Fatal("human review accepted despite overlapping workspace activity")
	}
	current, err := s.Get(parent)
	if err != nil || current.Status != "open" || len(current.Evidence) != 0 {
		t.Fatalf("rejected review affected the parent gate: %+v %v", current, err)
	}
}

func TestEpicDetectsConcurrentClaimsAcrossStores(t *testing.T) {
	s := fixture(t)
	otherStore, _, err := bootstrap.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer otherStore.Store.Close()
	check := core.Check{ID: "review", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true}
	parent := integrationParent(t, s, "parent", []core.Check{check})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	other := create(t, s, core.Definition{ID: "other", Title: "other", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
	step(t, s, other, "writer")
	if _, err = s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		next core.Next
		err  error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	go func() { <-start; n, e := s.Claim(parent, "review", "reviewer", "", ""); results <- claimResult{n, e} }()
	go func() { <-start; n, e := otherStore.Claim(other, "", "writer", "", ""); results <- claimResult{n, e} }()
	close(start)
	claims := map[string]core.Next{}
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		claims[r.next.Task.Definition.ID] = r.next
	}
	parentState, err := s.Get(parent)
	if err != nil || len(parentState.Attempts) != 1 || parentState.Attempts[0].WorkspaceConflict == "" {
		t.Fatalf("cross-store overlap was not recorded: %+v %v", parentState.Attempts, err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("concurrent report"), 0600); err != nil {
		t.Fatal(err)
	}
	writer := claims[other].Attempt
	if _, err = otherStore.Submit(core.Submission{Task: other, Attempt: writer.ID, Token: writer.Token, Key: "writer-finished", Result: core.StepResult{Status: "pass", Summary: "writer stopped", Artifacts: []string{"leaf.txt"}}}); err != nil {
		t.Fatal(err)
	}
	claim := claims[parent]
	r := core.Assessment{Task: parent, Check: "review", Candidate: claim.Task.Candidate, Method: "model", Executor: "reviewer", Result: "pass", Reason: "concurrent assessment", Inputs: []string{"leaf.txt"}, Artifacts: []string{"runtime:report.md"}}
	result, err := s.AssessExisting(r, claim.Attempt.ID, claim.Attempt.Token)
	if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 || len(result.Task.EvidenceHistory) != 1 {
		t.Fatalf("cross-store overlap passed parent gate: %+v %v", result.Task, err)
	}
}

func TestEpicCheckMustUsePreparedParentWorkspace(t *testing.T) {
	s := fixture(t)
	check := core.Check{ID: "review", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true}
	parent := integrationParent(t, s, "parent", []core.Check{check})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	other := create(t, s, core.Definition{ID: "other", Title: "other", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
	step(t, s, other, "writer")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	workspace := copyCheckoutWithModes(t, s.Root)
	if _, err := s.BindWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(other, "", "writer", workspace, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(parent, "review", "reviewer", workspace, ""); err == nil || !strings.Contains(err.Error(), "EPIC_CHECK_WORKSPACE_MISMATCH") {
		t.Fatalf("parent check accepted another workspace: %v", err)
	}
}

func TestEpicParallelChecksRetainResultsButRequireStableRecheck(t *testing.T) {
	s := fixture(t)
	checks := []core.Check{
		{ID: "a", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true},
		{ID: "b", Kind: "functional", Method: "model", Covers: []string{"整体"}, Required: true},
	}
	parent := integrationParent(t, s, "parent", checks)
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	claimA, err := s.Claim(parent, "a", "checker-a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	claimB, err := s.Claim(parent, "b", "checker-b", "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, "leaf.txt")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("temporary other check output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("inspection of temporary content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []core.Next{claimB, claimA} {
		checkID := strings.TrimPrefix(claim.Attempt.Unit, "check:")
		r := core.Assessment{Task: parent, Check: checkID, Candidate: claim.Task.Candidate, Method: "model", Executor: claim.Attempt.Executor, Result: "pass", Reason: "concurrent assessment", Inputs: []string{"leaf.txt"}, Artifacts: []string{"runtime:report.md"}}
		result, err := s.AssessExisting(r, claim.Attempt.ID, claim.Attempt.Token)
		if err != nil || result.Task.Status != "open" || len(result.Task.Evidence) != 0 {
			t.Fatalf("parallel checks passed without isolation: %+v %v", result.Task, err)
		}
	}
	current, err := s.Get(parent)
	if err != nil || len(current.EvidenceHistory) != 2 || current.Integration.StaleReason == "" {
		t.Fatalf("parallel results or revalidation reason lost: %+v %v", current, err)
	}
}

package core_test

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRemoteMutationReceiptAndExpectedRevision(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.ID = "remote-task"
	createOp := core.RemoteOperation{ID: "op-create", Key: "key-create", RequestHash: "hash-create", TaskID: d.ID, Expected: "absent"}
	created, err := s.WithRemoteOperation(createOp).Create(d)
	if err != nil || created.Task == nil {
		t.Fatalf("remote create failed: %+v %v", created, err)
	}
	receipt, found, err := s.RemoteReceipt(createOp.Key, createOp.RequestHash)
	if err != nil || !found || receipt.OperationID != createOp.ID || receipt.Task.Definition.ID != d.ID || receipt.Revision == "" {
		t.Fatalf("operation receipt missing: %+v %v %v", receipt, found, err)
	}
	if _, _, err = s.RemoteReceipt(createOp.Key, "different-hash"); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("same key accepted different content: %v", err)
	}
	base := receipt.Revision
	type result struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func(index int) {
			<-start
			change := d
			change.Title = fmt.Sprintf("concurrent edit %d", index)
			op := core.RemoteOperation{ID: fmt.Sprintf("edit-%d", index), Key: fmt.Sprintf("edit-key-%d", index), RequestHash: fmt.Sprintf("edit-hash-%d", index), TaskID: d.ID, Expected: base}
			_, err := s.WithRemoteOperation(op).Edit(d.ID, change, "isolated concurrent edit")
			results <- result{index: index, err: err}
		}(i)
	}
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			successes++
		} else if !strings.Contains(r.err.Error(), "REVISION_CONFLICT") {
			t.Fatalf("unexpected concurrent edit error: %v", r.err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected one revision winner, got %d", successes)
	}
	current, err := s.Get(d.ID)
	if err != nil || core.RemoteRevision(current) == base {
		t.Fatalf("display edit did not advance remote revision: %+v %v", current, err)
	}
}

func TestRemoteRevisionIgnoresReadTimeArtifactAvailability(t *testing.T) {
	task := core.Task{Definition: core.Definition{ID: "task"}, Evidence: []core.Evidence{{Check: "check", Result: "pass"}}}
	before := core.RemoteRevision(task)
	task.Evidence[0].ArtifactStatus = map[string]string{"runtime:log": "available"}
	if core.RemoteRevision(task) != before {
		t.Fatal("read-time artifact display changed the authoritative revision")
	}
	task.Evidence[0].Result = "fail"
	if core.RemoteRevision(task) == before {
		t.Fatal("a real evidence change did not change the authoritative revision")
	}
}

func TestRemotePlanScopeFenceCoversFeedbackAndUnlinkedLeaf(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "parent", Title: "Parent", Type: "epic", Goal: "verified", Acceptance: []string{"overall"}})
	leaf := create(t, s, core.Definition{ID: "unlinked", Title: "Unlinked", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
	current, err := s.Get(leaf)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"remote-plan-parent:" + store.Hash([]byte(parent)), "remote-plan-task:" + store.Hash([]byte(leaf))} {
		if _, err = s.Store.DB.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", key, "plan-op"); err != nil {
			t.Fatal(err)
		}
	}
	feedback := core.Definition{ID: "feedback", Title: "Feedback", Type: "bug", Goal: "verified", Acceptance: []string{"result"}, FeedbackOf: parent}
	feedbackOp := core.RemoteOperation{ID: "feedback-op", Key: "feedback-key", RequestHash: "feedback-hash", TaskID: feedback.ID, Expected: "absent", Actor: "fixture"}
	if _, err = s.WithRemoteOperation(feedbackOp).Create(feedback); err == nil || !strings.Contains(err.Error(), "PLAN_APPLY_IN_PROGRESS") {
		t.Fatalf("feedback modified a locked plan parent: %v", err)
	}
	change := current.Definition
	change.Title = "Changed while plan pending"
	editOp := core.RemoteOperation{ID: "edit-op", Key: "edit-key", RequestHash: "edit-hash", TaskID: leaf, Expected: core.RemoteRevision(current), Actor: "fixture"}
	if _, err = s.WithRemoteOperation(editOp).Edit(leaf, change, "fixture edit"); err == nil || !strings.Contains(err.Error(), "PLAN_APPLY_IN_PROGRESS") {
		t.Fatalf("unlinked planned leaf was not fenced: %v", err)
	}
}

func TestRemotePlanCanIncludeFeedbackToItsParent(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Definition.FeedbackOf = proposal.Parent
	bindProposal(t, s, &proposal)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.Get(proposal.Parent)
	if err != nil {
		t.Fatal(err)
	}
	op := core.RemoteOperation{ID: proposal.ID, Key: proposal.ID, RequestHash: "feedback-plan-hash", TaskID: proposal.Parent, Expected: core.RemoteRevision(parent), Actor: "fixture"}
	result, err := s.WithRemoteOperation(op).ApplyPlan(proposal, preview.Digest)
	if err != nil || result.Status != "applied" {
		t.Fatalf("plan containing parent feedback did not finish: %+v %v", result, err)
	}
	parent, err = s.Get(proposal.Parent)
	if err != nil || len(parent.Feedback) != 1 {
		t.Fatalf("feedback association was not retained: %+v %v", parent.Feedback, err)
	}
}

func TestRemotePlanAbandonRetainsPartialWorkAndReleasesReservations(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Definition.ID = "retained-one"
	proposal.Leaves[1].Definition.ID = "retained-two"
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.Get(proposal.Parent)
	if err != nil {
		t.Fatal(err)
	}
	apply := core.RemoteOperation{ID: proposal.ID, Key: proposal.ID, RequestHash: "apply-hash", TaskID: proposal.Parent, Expected: core.RemoteRevision(parent), Actor: "fixture"}
	if _, err = s.Store.DB.Exec(`CREATE TRIGGER test_remote_abandon BEFORE INSERT ON tasks WHEN NEW.id='retained-two' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WithRemoteOperation(apply).ApplyPlan(proposal, preview.Digest); err == nil {
		t.Fatal("injected plan failure did not stop")
	}
	if _, found, err := s.RemotePendingByKey(apply.Key, apply.Actor, false); err != nil || !found {
		t.Fatalf("pending operation missing: %v %v", found, err)
	}
	if _, err = s.Store.DB.Exec(`DROP TRIGGER test_remote_abandon`); err != nil {
		t.Fatal(err)
	}
	abandon := core.RemoteOperation{ID: "abandon-op", Key: "abandon-key", RequestHash: "abandon-hash", TaskID: proposal.Parent, Expected: core.RemoteRevision(parent), Actor: "fixture", ReleasePlan: proposal.ID}
	result, err := s.WithRemoteOperation(abandon).AbandonPlan(proposal.Parent, proposal.ID, "review partial work")
	if err != nil || result["status"] != "abandoned" {
		t.Fatalf("remote plan abandon failed: %+v %v", result, err)
	}
	if _, found, err := s.RemotePendingByKey(apply.Key, apply.Actor, false); err != nil || found {
		t.Fatalf("pending reservation was not released: %v %v", found, err)
	}
	receipt, found, err := s.RemoteReceipt(apply.Key, apply.RequestHash)
	if err != nil || !found || !strings.Contains(string(receipt.Result), `"status":"abandoned"`) {
		t.Fatalf("abandoned original operation did not retain a receipt: %+v %v %v", receipt, found, err)
	}
	tree, err := s.Tree(proposal.Parent)
	if err != nil || len(tree.Children) != 1 || tree.Children[0].Task.Task.Definition.ID != "retained-one" {
		t.Fatalf("partial work was lost: %+v %v", tree, err)
	}
	change := parent.Definition
	change.Title = "Editing after abandon"
	edit := core.RemoteOperation{ID: "edit-after", Key: "edit-after", RequestHash: "edit-after", TaskID: proposal.Parent, Expected: core.RemoteRevision(parent), Actor: "fixture"}
	if _, err = s.WithRemoteOperation(edit).Edit(proposal.Parent, change, "reviewed partial work"); err != nil {
		t.Fatalf("parent remained locked after abandon: %v", err)
	}
}

func TestRemotePlanDefersFeedbackToLaterExistingLeaf(t *testing.T) {
	s, proposal := proposalFixture(t)
	existing := create(t, s, core.Definition{ID: "existing", Title: "Existing", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
	proposal.Leaves[0].Definition.FeedbackOf = existing
	proposal.Leaves[1].ExistingID = existing
	proposal.Leaves[1].Definition = core.Definition{}
	bindProposal(t, s, &proposal)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.Get(proposal.Parent)
	if err != nil {
		t.Fatal(err)
	}
	op := core.RemoteOperation{ID: proposal.ID, Key: proposal.ID, RequestHash: "later-feedback", TaskID: proposal.Parent, Expected: core.RemoteRevision(parent), Actor: "fixture"}
	result, err := s.WithRemoteOperation(op).ApplyPlan(proposal, preview.Digest)
	if err != nil || result.Status != "applied" {
		t.Fatalf("plan could not associate feedback with a later existing leaf: %+v %v", result, err)
	}
	old, err := s.Get(existing)
	if err != nil || len(old.Feedback) != 1 {
		t.Fatalf("feedback association was lost: %+v %v", old.Feedback, err)
	}
}

func TestRemotePlanAbandonRetainsFeedbackAssociation(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Definition.ID = "retained-feedback"
	proposal.Leaves[0].Definition.FeedbackOf = proposal.Parent
	proposal.Leaves[1].Definition.ID = "failing-leaf"
	bindProposal(t, s, &proposal)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.Get(proposal.Parent)
	if err != nil {
		t.Fatal(err)
	}
	apply := core.RemoteOperation{ID: proposal.ID, Key: proposal.ID, RequestHash: "feedback-apply", TaskID: proposal.Parent, Expected: core.RemoteRevision(parent), Actor: "fixture"}
	if _, err = s.Store.DB.Exec(`CREATE TRIGGER test_feedback_abandon BEFORE INSERT ON tasks WHEN NEW.id='failing-leaf' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WithRemoteOperation(apply).ApplyPlan(proposal, preview.Digest); err == nil {
		t.Fatal("injected failure did not stop plan")
	}
	if _, err = s.Store.DB.Exec(`DROP TRIGGER test_feedback_abandon`); err != nil {
		t.Fatal(err)
	}
	abandon := core.RemoteOperation{ID: "abandon-feedback", Key: "abandon-feedback", RequestHash: "abandon-feedback", TaskID: proposal.Parent, Expected: core.RemoteRevision(parent), Actor: "fixture", ReleasePlan: proposal.ID}
	if _, err = s.WithRemoteOperation(abandon).AbandonPlan(proposal.Parent, proposal.ID, "review partial work"); err != nil {
		t.Fatal(err)
	}
	parent, err = s.Get(proposal.Parent)
	if err != nil || len(parent.Feedback) != 1 || parent.Feedback[0] != "retained-feedback" {
		t.Fatalf("abandon lost retained feedback association: %+v %v", parent.Feedback, err)
	}
}

func TestRemotePlanOperationsHaveDurableReceipts(t *testing.T) {
	s, proposal := proposalFixture(t)
	parent, err := s.Get(proposal.Parent)
	if err != nil {
		t.Fatal(err)
	}
	expected := core.RemoteRevision(parent)
	prepareOp := core.RemoteOperation{ID: "prepare-op", Key: "prepare-key", RequestHash: "prepare-hash", TaskID: proposal.Parent, Expected: expected, Actor: "fixture"}
	prepared, err := s.WithRemoteOperation(prepareOp).PreparePlan(proposal.Parent, core.PlanInput{Request: proposal.Request, Items: proposal.UserItems})
	if err != nil {
		t.Fatal(err)
	}
	preparedReceipt, found, err := s.RemoteReceipt(prepareOp.Key, prepareOp.RequestHash)
	if err != nil || !found || preparedReceipt.OperationID != prepareOp.ID {
		t.Fatalf("missing preparation receipt: %+v %v %v", preparedReceipt, found, err)
	}
	var restored core.PlanPreparation
	if err = json.Unmarshal(preparedReceipt.Result, &restored); err != nil || restored.InputHash != prepared.InputHash {
		t.Fatalf("preparation replay lost its result: %+v %v", restored, err)
	}
	proposal.InputHash = prepared.InputHash
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	applyOp := core.RemoteOperation{ID: proposal.ID, Key: proposal.ID, RequestHash: "apply-hash", TaskID: proposal.Parent, Expected: expected, Actor: "fixture"}
	result, err := s.WithRemoteOperation(applyOp).ApplyPlan(proposal, preview.Digest)
	if err != nil || result.Status != "applied" {
		t.Fatalf("remote plan failed: %+v %v", result, err)
	}
	receipt, found, err := s.RemoteReceipt(applyOp.Key, applyOp.RequestHash)
	if err != nil || !found || receipt.OperationID != applyOp.ID {
		t.Fatalf("plan receipt not committed: %+v %v %v", receipt, found, err)
	}
	var restoredResult core.PlanApplyResult
	if err = json.Unmarshal(receipt.Result, &restoredResult); err != nil || restoredResult.PlanID != proposal.ID {
		t.Fatalf("plan receipt lost result: %+v %v", restoredResult, err)
	}
	again, err := s.WithRemoteOperation(applyOp).ApplyPlan(proposal, preview.Digest)
	if err != nil || again.PlanID != result.PlanID {
		t.Fatalf("plan replay failed: %+v %v", again, err)
	}
}

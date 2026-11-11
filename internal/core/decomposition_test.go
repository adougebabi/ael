package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

func proposalFixture(t *testing.T) (*core.Service, core.PlanProposal) {
	t.Helper()
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "大任务", Type: "epic", Goal: "verified", Acceptance: []string{"父验收一", "父验收二"}})
	leaf := func(key string, order int) core.PlanLeaf {
		return core.PlanLeaf{Key: key, Definition: core.Definition{Title: key, Type: "bug", Goal: "verified", Scope: []string{key + ".txt"}, NonGoals: []string{"不发布"}, Acceptance: []string{"实际结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"实际结果"}, Required: true}}}, Deliverables: []string{key + ".txt"}, Basis: []string{"用户明确要求"}, Capabilities: []string{"开发"}, Order: order, Required: true}
	}
	p := core.PlanProposal{Version: 1, ID: "plan-1", Parent: "epic", ParentRevision: 1, Request: "实现两个明确结果", Leaves: []core.PlanLeaf{leaf("one", 1), leaf("two", 2)}, Coverage: []core.PlanCoverage{{Acceptance: "父验收一", Leaves: []string{"one"}}, {Acceptance: "父验收二", Leaves: []string{"two"}}}}
	bindProposal(t, s, &p)
	return s, p
}

func bindProposal(t *testing.T, s *core.Service, p *core.PlanProposal) {
	t.Helper()
	prepared, err := s.PreparePlan(p.Parent, core.PlanInput{Request: p.Request, Items: p.UserItems})
	if err != nil {
		t.Fatal(err)
	}
	p.InputHash = prepared.InputHash
}

func TestPlanPreviewRejectsCoverageCyclesAndInvalidTasks(t *testing.T) {
	s, valid := proposalFixture(t)
	if _, err := s.PreviewPlan(valid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*core.PlanProposal)
		code   string
	}{
		{"parent coverage", func(p *core.PlanProposal) { p.Coverage = p.Coverage[:1] }, "PARENT_COVERAGE_MISSING"},
		{"leaf coverage", func(p *core.PlanProposal) { p.Leaves[0].Definition.Checks[0].Covers = []string{"other"} }, "LEAF_COVERAGE_MISSING"},
		{"cycle", func(p *core.PlanProposal) {
			p.Leaves[0].DependsOn = []string{"two"}
			p.Leaves[1].DependsOn = []string{"one"}
		}, "DEPENDENCY_CYCLE"},
		{"missing plan dependency", func(p *core.PlanProposal) { p.Leaves[0].DependsOn = []string{"missing"} }, "PLAN_DEPENDENCY_MISSING"},
		{"missing external dependency", func(p *core.PlanProposal) { p.Leaves[0].Definition.Dependencies = []string{"missing"} }, "DEPENDENCY_MISSING"},
		{"duplicate responsibility", func(p *core.PlanProposal) { p.Leaves[1].Definition.Title = p.Leaves[0].Definition.Title }, "PLAN_DUPLICATE_RESPONSIBILITY"},
		{"unknown task type", func(p *core.PlanProposal) { p.Leaves[0].Definition.Type = "imaginary" }, "INVALID_TYPE"},
		{"invalid preset", func(p *core.PlanProposal) { p.Leaves[0].Definition.Preset = "imaginary" }, "INVALID_PRESET"},
		{"ui without coverage", func(p *core.PlanProposal) { p.Leaves[0].Definition.UI = true }, "CHECK_COVERAGE_REQUIRED"},
		{"external ID order", func(p *core.PlanProposal) {
			p.Leaves[1].Definition.ID = "planned-two"
			p.Leaves[0].Definition.Dependencies = []string{"planned-two"}
		}, "PLAN_ORDER_CONFLICT"},
		{"unrequested UX", func(p *core.PlanProposal) { p.Leaves[0].Definition.Checks[0].Kind = "ux" }, "PLAN_UNDECLARED_UI_CHECK"},
		{"inspection cannot authorize change", func(p *core.PlanProposal) {
			p.Leaves[0].Definition.Type = "qa"
			p.Leaves[0].Definition.Goal = "verified"
		}, "INSPECTION_GOAL_REQUIRED"},
		{"user item omitted", func(p *core.PlanProposal) {
			p.UserItems = []core.PlanInputItem{{ID: "provided", Description: "保留原分项"}}
		}, "USER_ITEM_UNMAPPED"},
		{"duplicate user item", func(p *core.PlanProposal) {
			p.UserItems = []core.PlanInputItem{{ID: "provided", Description: "保留原分项"}}
			p.Leaves[0].SourceIDs = []string{"provided"}
			p.Leaves[1].SourceIDs = []string{"provided"}
		}, "DUPLICATE_USER_ITEM_MAPPING"},
		{"optional-only coverage", func(p *core.PlanProposal) {
			p.Leaves[1].Required = false
		}, "PARENT_COVERAGE_OPTIONAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Copy nested values, so each case tests an independent proposal.
			p := valid
			p.Leaves = append([]core.PlanLeaf(nil), valid.Leaves...)
			p.Coverage = append([]core.PlanCoverage(nil), valid.Coverage...)
			for i := range p.Leaves {
				p.Leaves[i].Definition.Checks = append([]core.Check(nil), valid.Leaves[i].Definition.Checks...)
			}
			tc.mutate(&p)
			bindProposal(t, s, &p)
			if _, err := s.PreviewPlan(p); err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
}

func TestApplyPlanReusesCompletedTaskAndIsIdempotent(t *testing.T) {
	s, proposal := proposalFixture(t)
	oldID := create(t, s, core.Definition{ID: "provided-task", Title: "已有交付", Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f existing.txt"}, Covers: []string{"实际结果"}, Required: true}}})
	step(t, s, oldID, "fixture")
	if err := os.WriteFile(filepath.Join(s.Root, "existing.txt"), []byte("actual completed result"), 0644); err != nil {
		t.Fatal(err)
	}
	step(t, s, oldID, "fixture", "existing.txt")
	done, err := s.Verify(oldID, "proof", "checker")
	if err != nil || done.Action != "completed" {
		t.Fatal(done, err)
	}
	before := *done.Task
	proposal.UserItems = []core.PlanInputItem{{ID: "user-original-1", Description: "复用已完成的结果", ExistingID: oldID}}
	proposal.Leaves[0].ExistingID = oldID
	proposal.Leaves[0].SourceIDs = []string{"user-original-1"}
	proposal.Leaves[0].Definition = core.Definition{}
	proposal.Leaves[1].DependsOn = []string{"one"}
	bindProposal(t, s, &proposal)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ApplyPlan(proposal, preview.Digest)
	if err != nil || result.Status != "applied" {
		t.Fatalf("apply: %+v %v", result, err)
	}
	again, err := s.ApplyPlan(proposal, preview.Digest)
	if err != nil || !reflect.DeepEqual(result, again) {
		t.Fatalf("replay changed result: %+v %+v %v", result, again, err)
	}
	after, err := s.Get(oldID)
	if err != nil || after.Completed == nil || !after.Completed.Equal(*before.Completed) || after.DefinitionHash != before.DefinitionHash || after.Result != before.Result || len(after.Evidence) != len(before.Evidence) {
		t.Fatalf("completed task was reset: %+v %v", after, err)
	}
	tree, err := s.Tree("epic")
	if err != nil || len(tree.Children) != 2 {
		t.Fatalf("tasks duplicated or lost: %+v %v", tree, err)
	}
	for _, child := range tree.Children {
		if child.Task.Task.Definition.ID != oldID && !slices.Contains(child.Task.Task.Definition.Dependencies, oldID) {
			t.Fatalf("planned dependency missing: %+v", child)
		}
	}
	clone := copyCheckout(t, s.Root)
	other, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Store.Close()
	clonedReplay, err := other.ApplyPlan(proposal, preview.Digest)
	if err != nil || !reflect.DeepEqual(result, clonedReplay) {
		t.Fatalf("portable replay changed result: %+v %v", clonedReplay, err)
	}
	changed := proposal
	changed.Request = "different request"
	if _, err = s.ApplyPlan(changed, preview.Digest); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("same key accepted changed plan: %v", err)
	}
	if _, err = other.ApplyPlan(changed, preview.Digest); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("portable checkpoint accepted a changed plan with the same ID: %v", err)
	}
}

func TestPlanApplyResumesAfterPartialFailure(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Definition.ID = "planned-one"
	proposal.Leaves[1].Definition.ID = "planned-two"
	proposal.Leaves[1].DependsOn = []string{"one"}
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec(`CREATE TRIGGER test_plan_failure BEFORE INSERT ON tasks WHEN NEW.id='planned-two' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil || !strings.Contains(err.Error(), "PLAN_APPLY_INCOMPLETE") {
		t.Fatalf("failure did not preserve a resumable plan: %v", err)
	}
	tree, err := s.Tree("epic")
	if err != nil || len(tree.Children) != 1 || tree.Children[0].Task.Task.Definition.ID != "planned-one" {
		t.Fatalf("partial state not retained: %+v %v", tree, err)
	}
	if _, err = s.Store.DB.Exec(`DROP TRIGGER test_plan_failure`); err != nil {
		t.Fatal(err)
	}
	root := s.Root
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s, _, err = bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	result, err := s.ApplyPlan(proposal, preview.Digest)
	if err != nil || result.Status != "applied" {
		t.Fatalf("resume failed: %+v %v", result, err)
	}
	tree, err = s.Tree("epic")
	if err != nil || len(tree.Children) != 2 {
		t.Fatalf("resume duplicated or lost work: %+v %v", tree, err)
	}
}

func TestReplanChangesOnlyAffectedOpenLeaf(t *testing.T) {
	s, proposal := proposalFixture(t)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ApplyPlan(proposal, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest := preview.Digest
	ids := map[string]string{}
	for _, change := range first.Changes {
		ids[change.Key] = change.TaskID
	}
	unchanged, err := s.Get(ids["one"])
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.Get(ids["two"])
	if err != nil {
		t.Fatal(err)
	}
	next := proposal
	next.ID = "plan-2"
	next.Leaves = append([]core.PlanLeaf(nil), proposal.Leaves...)
	for i := range next.Leaves {
		current, err := s.Get(ids[next.Leaves[i].Key])
		if err != nil {
			t.Fatal(err)
		}
		next.Leaves[i].ExistingID = current.Definition.ID
		next.Leaves[i].Definition = core.Definition{}
	}
	next.Leaves[1].ExpectedRevision = changed.Definition.Revision
	next.Leaves[1].Definition = changed.Definition
	next.Leaves[1].Definition.Title = "调整后的第二项"
	preview, err = s.PreviewPlan(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 2 || preview.Changes[0].Action != "reuse" || preview.Changes[1].Action != "revise" || !slices.Contains(preview.Changes[1].Fields, "title") {
		t.Fatalf("incorrect diff: %+v", preview.Changes)
	}
	if _, err = s.ApplyPlan(next, preview.Digest); err != nil {
		t.Fatal(err)
	}
	afterOne, err := s.Get(ids["one"])
	if err != nil || afterOne.DefinitionHash != unchanged.DefinitionHash || afterOne.Definition.Revision != unchanged.Definition.Revision {
		t.Fatalf("unaffected task was modified: %+v %v", afterOne, err)
	}
	afterTwo, err := s.Get(ids["two"])
	if err != nil || afterTwo.Definition.Title != "调整后的第二项" {
		t.Fatalf("affected task was not revised: %+v %v", afterTwo, err)
	}
	clone := copyCheckout(t, s.Root)
	recovered, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Store.Close()
	oldReplay, err := recovered.ApplyPlan(proposal, firstDigest)
	if err != nil || !reflect.DeepEqual(first, oldReplay) {
		t.Fatalf("older plan could not be replayed after replan and recovery: %+v %v", oldReplay, err)
	}
	conflict := proposal
	conflict.Request = "reused ID with different content"
	if _, err = recovered.ApplyPlan(conflict, firstDigest); err == nil || !strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("older plan ID accepted different content after recovery: %v", err)
	}
}

func TestPlannedInspectionCannotModifyBusinessFiles(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].InspectionOnly = true
	proposal.Leaves[0].Definition.Type = "qa"
	proposal.Leaves[0].Definition.Goal = "report"
	proposal.Leaves[0].Definition.Scope = []string{"business.txt"}
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ApplyPlan(proposal, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, change := range result.Changes {
		if change.Key == "one" {
			id = change.TaskID
		}
	}
	task, err := s.Get(id)
	if err != nil || task.Definition.Intent != "inspect" || task.Definition.Goal != "report" {
		t.Fatalf("inspection task obtained change permission: %+v %v", task, err)
	}
	claim, err := s.Claim(id, "", "inspector", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "business.txt"), []byte("unauthorized change"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: "inspection-test", Result: core.StepResult{Status: "pass", Summary: "inspection"}})
	if err == nil || !strings.Contains(err.Error(), "INSPECTION_WRITE_DETECTED") {
		t.Fatalf("inspection accepted changed business files: %v", err)
	}
}

func TestAbandonedPlanKeepsPartialTasksAndCannotResume(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Definition.ID = "retained-one"
	proposal.Leaves[1].Definition.ID = "retained-two"
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec(`CREATE TRIGGER test_abandon BEFORE INSERT ON tasks WHEN NEW.id='retained-two' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil {
		t.Fatal("injected failure did not stop application")
	}
	if _, err = s.Store.DB.Exec(`DROP TRIGGER test_abandon`); err != nil {
		t.Fatal(err)
	}
	abandoned, err := s.AbandonPlan("epic", proposal.ID, "review partial results first")
	if err != nil || abandoned["status"] != "abandoned" {
		t.Fatalf("abandon: %+v %v", abandoned, err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil || !strings.Contains(err.Error(), "PLAN_NOT_RESUMABLE") {
		t.Fatalf("abandoned plan resumed: %v", err)
	}
	tree, err := s.Tree("epic")
	if err != nil || len(tree.Children) != 1 || tree.Children[0].Task.Task.Definition.ID != "retained-one" {
		t.Fatalf("abandon removed partial work: %+v %v", tree, err)
	}
}

func TestConcurrentPlanApplyDoesNotDuplicateLeaves(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Definition.ID = "concurrent-one"
	proposal.Leaves[1].Definition.ID = "concurrent-two"
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ApplyPlan(proposal, preview.Digest)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent identical application failed: %v", err)
		}
	}
	tree, err := s.Tree("epic")
	if err != nil || len(tree.Children) != 2 {
		t.Fatalf("concurrent application duplicated work: %+v %v", tree, err)
	}
}

func TestPlanPreparationDoesNotModifyTasksOrExposeLease(t *testing.T) {
	s, proposal := proposalFixture(t)
	child := create(t, s, core.Definition{ID: "running", Parent: "epic", Title: "正在工作", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	claim, err := s.Claim(child, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err = s.Store.DB.QueryRow("SELECT count(*) FROM events").Scan(&before); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PreparePlan("epic", core.PlanInput{Request: "只读预览", Items: []core.PlanInputItem{{ID: "old", Description: "已有任务", ExistingID: child}}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(prepared)
	if err != nil || strings.Contains(string(body), claim.Attempt.Token) {
		t.Fatalf("preparation leaked active lease: %s %v", body, err)
	}
	proposal.Leaves[0].ExistingID = child
	proposal.Leaves[0].Definition = core.Definition{}
	if _, err = s.PreviewPlan(proposal); err != nil {
		t.Fatal(err)
	}
	var after int
	if err = s.Store.DB.QueryRow("SELECT count(*) FROM events").Scan(&after); err != nil || after != before {
		t.Fatalf("preview wrote events: before %d after %d err %v", before, after, err)
	}
}

func TestPlanSnapshotCannotBeReplacedByOlderRecord(t *testing.T) {
	s, proposal := proposalFixture(t)
	path := filepath.Join(s.Root, ".ael/records/epic.json")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, old, 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err = bootstrap.Open(s.Root)
	if err == nil || !strings.Contains(err.Error(), "TASK_PLAN_CONFLICT") {
		t.Fatalf("old record replaced plan: %v", err)
	}
	parent, err := s.Get("epic")
	if err != nil || parent.EpicPlan == nil || parent.EpicPlan.Proposal.ID != proposal.ID {
		t.Fatalf("current plan lost: %+v %v", parent, err)
	}
}

func TestPreparedUserItemsCannotBeOmittedFromProposal(t *testing.T) {
	s, proposal := proposalFixture(t)
	old := create(t, s, core.Definition{ID: "user-existing", Title: "已有分项", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	proposal.UserItems = []core.PlanInputItem{{ID: "user-1", Description: "保留已有分项", ExistingID: old}}
	bindProposal(t, s, &proposal)
	proposal.UserItems = nil
	if _, err := s.PreviewPlan(proposal); err == nil || !strings.Contains(err.Error(), "PLAN_INPUT_MISMATCH") {
		t.Fatalf("omitted original user item accepted: %v", err)
	}
}

func TestPlanReuseRequiresCoverageAndInspectionPermission(t *testing.T) {
	s, proposal := proposalFixture(t)
	uncovered := create(t, s, core.Definition{ID: "uncovered", Title: "缺失检查", Type: "bug", Goal: "verified", Acceptance: []string{"A", "B"}, Checks: []core.Check{{ID: "a", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"A"}, Required: true}}})
	proposal.Leaves[0].ExistingID = uncovered
	proposal.Leaves[0].Definition = core.Definition{}
	if _, err := s.PreviewPlan(proposal); err == nil || !strings.Contains(err.Error(), "LEAF_COVERAGE_MISSING") {
		t.Fatalf("reused leaf without complete checks accepted: %v", err)
	}
	qa := create(t, s, core.Definition{ID: "unsafe-qa", Title: "旧验收", Type: "qa", Goal: "report", Intent: "change", Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"结果"}, Required: true}}})
	proposal.Leaves[0].ExistingID = qa
	if _, err := s.PreviewPlan(proposal); err == nil || !strings.Contains(err.Error(), "INSPECTION_AUTHORIZATION_CONFLICT") {
		t.Fatalf("QA leaf retained change permission: %v", err)
	}
}

func TestPlanScopeExpansionRequiresConfirmation(t *testing.T) {
	s, proposal := proposalFixture(t)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ApplyPlan(proposal, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, change := range first.Changes {
		ids[change.Key] = change.TaskID
	}
	next := proposal
	next.ID = "scope-plan"
	next.Leaves = append([]core.PlanLeaf(nil), proposal.Leaves...)
	for i := range next.Leaves {
		current, err := s.Get(ids[next.Leaves[i].Key])
		if err != nil {
			t.Fatal(err)
		}
		next.Leaves[i].ExistingID = current.Definition.ID
		next.Leaves[i].Definition = core.Definition{}
	}
	current, err := s.Get(ids["two"])
	if err != nil {
		t.Fatal(err)
	}
	next.Leaves[1].ExpectedRevision = current.Definition.Revision
	next.Leaves[1].Definition = current.Definition
	next.Leaves[1].Definition.Scope = append(slices.Clone(current.Definition.Scope), "another-module/**")
	preview, err = s.PreviewPlan(next)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Changes[1].RequiresConfirmation || !slices.Contains(preview.Changes[1].Fields, "scope") {
		t.Fatalf("scope expansion not flagged: %+v", preview.Changes)
	}
	if _, err = s.ApplyPlan(next, preview.Digest); err != nil {
		t.Fatal(err)
	}
	revised, err := s.Get(ids["two"])
	if err != nil || revised.Confirmation == nil || revised.Confirmation.Kind != "revision" {
		t.Fatalf("scope expansion bypassed confirmation: %+v %v", revised, err)
	}
}

func TestPlanRejectsReuseChangedAfterRegistration(t *testing.T) {
	s, proposal := proposalFixture(t)
	old := create(t, s, core.Definition{ID: "existing", Title: "已有", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	proposal.Leaves[0].Definition.ID = "interrupt-first"
	proposal.Leaves[1].ExistingID = old
	proposal.Leaves[1].Definition = core.Definition{}
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec(`CREATE TRIGGER pause_plan BEFORE INSERT ON tasks WHEN NEW.id='interrupt-first' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil {
		t.Fatal("injection did not stop plan")
	}
	if _, err = s.Store.DB.Exec(`DROP TRIGGER pause_plan`); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(old)
	if err != nil {
		t.Fatal(err)
	}
	d := current.Definition
	d.Title = "用户在预览后编辑"
	if _, err = s.Edit(old, d, "user edit after preview"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil || !strings.Contains(err.Error(), "PLAN_TASK_CHANGED") {
		t.Fatalf("changed task was accepted without a fresh preview: %v", err)
	}
	current, err = s.Get(old)
	if err != nil || current.Definition.Title != "用户在预览后编辑" || current.EpicMembership != nil {
		t.Fatalf("user edit overwritten or linked: %+v %v", current, err)
	}
}

func TestPlanDoesNotOverrideConcurrentRejectedConfirmation(t *testing.T) {
	s, proposal := proposalFixture(t)
	old := create(t, s, core.Definition{ID: "existing", Title: "已有", Type: "bug", Goal: "verified", NonGoals: []string{"不发布"}, Acceptance: []string{"结果"}})
	current, err := s.Get(old)
	if err != nil {
		t.Fatal(err)
	}
	d := current.Definition
	d.Risk = []string{"需要审阅"}
	changed, err := s.Edit(old, d, "risk found")
	if err != nil || changed.Task.Confirmation == nil {
		t.Fatal(changed, err)
	}
	proposal.Leaves[0].Definition.ID = "interrupt-first"
	proposal.Leaves[1].ExistingID = old
	proposal.Leaves[1].ExpectedRevision = changed.Task.Definition.Revision
	proposal.Leaves[1].Definition = changed.Task.Definition
	proposal.Leaves[1].Definition.Title = "计划中的展示名称"
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec(`CREATE TRIGGER pause_confirm BEFORE INSERT ON tasks WHEN NEW.id='interrupt-first' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil {
		t.Fatal("injection did not stop plan")
	}
	if _, err = s.Store.DB.Exec(`DROP TRIGGER pause_confirm`); err != nil {
		t.Fatal(err)
	}
	c := changed.Task.Confirmation
	rejected, err := s.Confirm(old, c.Kind, "reject", "owner", "do not approve", c.Candidate, c.Revision, c.PlanRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil || !strings.Contains(err.Error(), "PLAN_TASK_CHANGED") {
		t.Fatalf("concurrent rejection overwritten: %v", err)
	}
	after, err := s.Get(old)
	if err != nil || after.Plan.Revision != rejected.Task.Plan.Revision || after.Plan.Approved || after.Definition.Title != changed.Task.Definition.Title {
		t.Fatalf("rejected state lost: %+v %v", after, err)
	}
}

func TestRestoredPartialPlanRequiresReviewThenResumes(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Definition.ID = "backup-one"
	proposal.Leaves[1].Definition.ID = "backup-two"
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec(`CREATE TRIGGER test_backup_plan BEFORE INSERT ON tasks WHEN NEW.id='backup-two' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err == nil {
		t.Fatal("injection did not stop application")
	}
	if _, err = s.Store.DB.Exec(`DROP TRIGGER test_backup_plan`); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err = s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	clone := copyCheckout(t, s.Root)
	restored, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Store.Close()
	if _, err = restored.Restore(backup); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.ApplyPlan(proposal, preview.Digest); err == nil || !strings.Contains(err.Error(), "PLAN_RESTORE_REVIEW_REQUIRED") {
		t.Fatalf("restored journal resumed without review: %v", err)
	}
	parent, err := restored.Get("epic")
	if err != nil || parent.Confirmation == nil || parent.Confirmation.Kind != "restore" {
		t.Fatal(parent, err)
	}
	c := parent.Confirmation
	if _, err = restored.Confirm("epic", c.Kind, "approve", "reviewer", "reconciled restored parent", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Resume("epic", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.RecoverRestoredPlan("epic", proposal.ID, "reviewed backup and retained leaf"); err != nil {
		t.Fatal(err)
	}
	result, err := restored.ApplyPlan(proposal, preview.Digest)
	if err != nil || result.Status != "applied" {
		t.Fatalf("restored plan did not resume: %+v %v", result, err)
	}
	tree, err := restored.Tree("epic")
	if err != nil || len(tree.Children) != 2 {
		t.Fatalf("restored plan lost or duplicated leaves: %+v %v", tree, err)
	}
}

func TestIncompletePortablePlanCannotReplayAsApplied(t *testing.T) {
	s, proposal := proposalFixture(t)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ApplyPlan(proposal, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	clone := copyCheckout(t, s.Root)
	missing := result.Changes[0].TaskID
	for _, rel := range []string{".ael/work-items/" + missing + ".md", ".ael/records/" + missing + ".json"} {
		if err = os.Remove(filepath.Join(clone, rel)); err != nil {
			t.Fatal(err)
		}
	}
	other, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Store.Close()
	if _, err = other.ApplyPlan(proposal, preview.Digest); err == nil || !strings.Contains(err.Error(), "PLAN_SNAPSHOT_INCOMPLETE") {
		t.Fatalf("incomplete clone replayed as applied: %v", err)
	}
	if _, err = other.PreviewPlan(proposal); err == nil || !strings.Contains(err.Error(), "PLAN_SNAPSHOT_INCOMPLETE") {
		t.Fatalf("incomplete clone previewed as applied: %v", err)
	}
}

func TestPlanRejectsCancelledRequiredTaskAndDependency(t *testing.T) {
	s, proposal := proposalFixture(t)
	id := create(t, s, core.Definition{ID: "cancelled", Title: "取消的任务", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	if _, err := s.Cancel(id, "cancelled fixture"); err != nil {
		t.Fatal(err)
	}
	proposal.Leaves[0].ExistingID = id
	proposal.Leaves[0].Definition = core.Definition{}
	if _, err := s.PreviewPlan(proposal); err == nil || !strings.Contains(err.Error(), "PLAN_REQUIRED_TASK_CANCELLED") {
		t.Fatalf("cancelled required task accepted: %v", err)
	}
	proposal.Leaves[0].ExistingID = ""
	proposal.Leaves[0].Definition = core.Definition{Title: "one", Type: "bug", Goal: "verified", Scope: []string{"one.txt"}, NonGoals: []string{"不发布"}, Acceptance: []string{"实际结果"}, Dependencies: []string{id}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"实际结果"}, Required: true}}}
	if _, err := s.PreviewPlan(proposal); err == nil || !strings.Contains(err.Error(), "PLAN_DEPENDENCY_CANCELLED") {
		t.Fatalf("cancelled dependency accepted: %v", err)
	}
}

func TestReplanMustReusePreviouslyMappedUserItem(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.UserItems = []core.PlanInputItem{{ID: "user-1", Description: "用户原有分项"}}
	proposal.Leaves[0].SourceIDs = []string{"user-1"}
	bindProposal(t, s, &proposal)
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err != nil {
		t.Fatal(err)
	}
	next := proposal
	next.ID = "another-plan"
	if _, err = s.PreviewPlan(next); err == nil || !strings.Contains(err.Error(), "USER_TASK_MISMATCH") {
		t.Fatalf("previous user item generated another task: %v", err)
	}
}

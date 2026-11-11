package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEpicTreeRealLeafCompletionAndRecovery(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "大任务", Type: "epic", Goal: "verified", Scope: []string{"fixture"}, Acceptance: []string{"整体验收"}})
	makeLeaf := func(id, parentID string) string {
		return create(t, s, core.Definition{ID: id, Parent: parentID, Title: id, Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f result-" + id}, Covers: []string{"实际结果"}, Required: true}}})
	}
	a := makeLeaf("leaf-a", parent)
	b := makeLeaf("leaf-b", "")
	c := makeLeaf("leaf-c", "")
	if _, err := s.LinkEpic(parent, b, true, "阶段一"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkEpic(parent, b, true, "阶段一"); err == nil || !strings.Contains(err.Error(), "TASK_ALREADY_LINKED") {
		t.Fatal("duplicate association accepted", err)
	}
	if _, err := s.LinkEpic(parent, parent, true, ""); err == nil || !strings.Contains(err.Error(), "PARENT_CYCLE") {
		t.Fatal("self cycle accepted", err)
	}
	if _, err := s.Claim(parent, "", "writer", "", ""); err == nil {
		t.Fatal("epic was claimable for ordinary code work")
	}
	complete := func(id string) core.Task {
		step(t, s, id, "isolated-executor")
		path := filepath.Join(s.Root, "result-"+id)
		if err := os.WriteFile(path, []byte("actual result"), 0644); err != nil {
			t.Fatal(err)
		}
		step(t, s, id, "isolated-executor", "result-"+id)
		n, err := s.Verify(id, "proof", "isolated-checker")
		if err != nil || n.Action != "completed" || n.Task.Status != "completed" {
			t.Fatalf("leaf %s did not complete through checks: %+v %v", id, n, err)
		}
		return *n.Task
	}
	before := complete(c)
	if before.Completed == nil {
		t.Fatal("completed leaf lacks timestamp")
	}
	if _, err := s.LinkEpic(parent, c, false, "可选阶段"); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(c)
	if err != nil || after.Completed == nil || !after.Completed.Equal(*before.Completed) || after.DefinitionHash != before.DefinitionHash || after.Result != before.Result {
		t.Fatalf("link changed historical result: before=%+v after=%+v err=%v", before, after, err)
	}
	tree, err := s.Tree(parent)
	if err != nil || len(tree.Children) != 3 || tree.Required != 2 || tree.RequiredCompleted != 0 || tree.Root.Task.Status != "open" {
		t.Fatalf("initial tree: %+v %v", tree, err)
	}
	complete(a)
	complete(b)
	tree, err = s.Tree(parent)
	if err != nil || tree.RequiredCompleted != 2 || tree.Root.Action != "wait_integration" || tree.Root.Task.Status != "open" {
		t.Fatalf("parent prematurely closed or missing leaves: %+v %v", tree, err)
	}
	if _, err := s.Delete(a, "attempt to remove required scope"); err == nil {
		t.Fatal("linked required leaf deleted")
	}
	root := s.Root
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Store.Close()
	tree, err = reopened.Tree(parent)
	if err != nil || len(tree.Children) != 3 || tree.RequiredCompleted != 2 {
		t.Fatalf("tree lost on restart: %+v %v", tree, err)
	}
	clone := copyCheckout(t, root)
	recovered, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Store.Close()
	historical, err := recovered.Get(c)
	if err != nil || historical.Completed == nil || !historical.Completed.Equal(*before.Completed) || !historical.Historical || historical.EpicMembership == nil {
		t.Fatalf("portable record lost association or historical completion: %+v %v", historical, err)
	}
	tree, err = recovered.Tree(parent)
	if err != nil || len(tree.Children) != 3 || tree.RequiredCompleted != 0 || tree.Summary.NotStarted != 0 {
		t.Fatalf("historical checks were reused as current completion: %+v %v", tree, err)
	}
}

func TestEpicRejectsInvalidRelationsAndRetainsCancelledRequiredLeaf(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	child := create(t, s, core.Definition{ID: "leaf", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	other := create(t, s, core.Definition{ID: "ordinary", Title: "普通任务", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	for _, attempt := range []struct{ parent, child string }{{"missing", child}, {other, child}, {parent, "missing"}, {parent, parent}} {
		if _, err := s.LinkEpic(attempt.parent, attempt.child, true, ""); err == nil {
			t.Fatalf("invalid link accepted: %+v", attempt)
		}
	}
	if _, err := s.Create(core.Definition{ID: "nested", Parent: parent, Title: "无效多层", Type: "epic", Goal: "verified", Acceptance: []string{"结果"}}); err == nil {
		t.Fatal("epic accepted as a child")
	}
	if _, err := s.LinkEpic(parent, child, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(child, "取消时保留范围"); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree(parent)
	if err != nil || tree.Required != 1 || tree.RequiredCompleted != 0 || tree.Root.Task.Status != "open" || len(tree.Children) != 1 || tree.Children[0].Task.Task.Status != "cancelled" {
		t.Fatalf("cancelled required child disappeared or satisfied parent: %+v %v", tree, err)
	}
	if _, err := s.Delete(child, "must not silently remove scope"); err == nil {
		t.Fatal("cancelled required leaf silently removed")
	}
}

func TestEpicAssociationSurvivesOlderCheckpoint(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"范围"}})
	child := create(t, s, core.Definition{ID: "leaf", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	record := filepath.Join(s.Root, ".ael/records/leaf.json")
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.LinkEpic(parent, child, true, ""); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(record, before, 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err = bootstrap.Open(s.Root)
	if err == nil || !strings.Contains(err.Error(), "MEMBERSHIP_CONFLICT") {
		t.Fatalf("old checkpoint silently replaced membership: %v", err)
	}
	tree, err := s.Tree(parent)
	if err != nil || len(tree.Children) != 1 || !tree.Children[0].Required {
		t.Fatalf("local required member was lost: %+v %v", tree, err)
	}
}

func TestEpicAssociationRejectsOlderRuntimeBackup(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"范围"}})
	child := create(t, s, core.Definition{ID: "leaf", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	backup := filepath.Join(t.TempDir(), "old-backup")
	if _, err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkEpic(parent, child, true, ""); err != nil {
		t.Fatal(err)
	}
	clone := copyCheckout(t, s.Root)
	restored, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Store.Close()
	if _, err = restored.Restore(backup); err == nil || !strings.Contains(err.Error(), "MEMBERSHIP_CONFLICT") {
		t.Fatalf("old backup silently replaced membership: %v", err)
	}
	tree, err := restored.Tree(parent)
	if err != nil || len(tree.Children) != 1 || !tree.Children[0].Required {
		t.Fatalf("restored project lost required member: %+v %v", tree, err)
	}
}

func TestLegacyParentCanBePreservedWhileLinkingExistingLeaf(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "新父", Type: "epic", Goal: "verified", Acceptance: []string{"范围"}})
	d := core.Definition{ID: "legacy-leaf", Parent: "old-group", Title: "旧叶子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, Revision: 1}
	plan := core.MakePlan(&d)
	if err := s.ImportTask(core.Task{Definition: d, Plan: plan, Step: "define", Status: "open", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkEpic(parent, d.ID, true, ""); err != nil {
		t.Fatalf("legacy parent blocked explicit association: %v", err)
	}
	linked, err := s.Get(d.ID)
	if err != nil || linked.Definition.Parent != "old-group" || linked.EpicMembership == nil || linked.EpicMembership.Parent != parent {
		t.Fatalf("legacy parent or new association lost: %+v %v", linked, err)
	}
}

func TestEpicCannotAssociateTasksFromAnotherProject(t *testing.T) {
	first := fixture(t)
	parent := create(t, first, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	second := fixture(t)
	foreign := create(t, second, core.Definition{ID: "foreign", Title: "其他项目", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	if _, err := first.LinkEpic(parent, foreign, true, ""); err == nil || !strings.Contains(err.Error(), "TASK_MISSING") {
		t.Fatalf("cross-project association accepted: %v", err)
	}
	tree, err := first.Tree(parent)
	if err != nil || len(tree.Children) != 0 {
		t.Fatalf("foreign task appeared in project: %+v %v", tree, err)
	}
}

func TestEpicMembershipSurvivesImportedTaskReplacementAndDeletion(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"范围"}})
	child := create(t, s, core.Definition{ID: "leaf", Title: "叶子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	old, err := s.Get(child)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := os.ReadFile(filepath.Join(s.Root, ".ael/work-items/leaf.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.LinkEpic(parent, child, true, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ImportTask(old); err == nil || !strings.Contains(err.Error(), "MEMBERSHIP_CONFLICT") {
		t.Fatalf("legacy import replaced membership: %v", err)
	}
	if err = s.RestoreImportedSnapshot(old, definition, nil); err == nil || !strings.Contains(err.Error(), "MEMBERSHIP_CONFLICT") {
		t.Fatalf("migration snapshot replaced membership: %v", err)
	}
	if err = s.DeleteImported(child); err == nil {
		t.Fatal("migration deleted associated leaf")
	}
	tree, err := s.Tree(parent)
	if err != nil || len(tree.Children) != 1 || !tree.Children[0].Required {
		t.Fatalf("required leaf disappeared: %+v %v", tree, err)
	}
}

package migration

import (
	"ael/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func TestRollbackPreservesLeafAssociatedWithEpic(t *testing.T) {
	s, p := setup(t)
	digest, err := Validate(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := Apply(s, p, nil, digest)
	if err != nil {
		t.Fatal(err)
	}
	var taskID string
	for _, change := range run.Changes {
		if change.TaskID != "" {
			taskID = change.TaskID
			break
		}
	}
	if taskID == "" {
		t.Fatal("fixture did not import a task")
	}
	// This fixture only changes the in-memory service, never a real project.
	s.Active = true
	parent, err := s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.LinkEpic(parent.Task.Definition.ID, taskID, true, ""); err != nil {
		t.Fatal(err)
	}
	definition := filepath.Join(s.Root, ".ael/work-items/"+taskID+".md")
	before, err := os.ReadFile(definition)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Rollback(s, run.ID)
	if err != nil || result.State != "rollback_conflict" {
		t.Fatalf("rollback silently removed associated task: %+v %v", result, err)
	}
	after, err := os.ReadFile(definition)
	if err != nil || string(after) != string(before) {
		t.Fatalf("associated task definition changed: %v", err)
	}
	tree, err := s.Tree("epic")
	if err != nil || len(tree.Children) != 1 || tree.Children[0].Task.Task.Definition.ID != taskID {
		t.Fatalf("associated member was lost: %+v %v", tree, err)
	}
}

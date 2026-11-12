package core_test

import (
	"ael/internal/core"
	"ael/internal/store"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteTaskCreationCanRollbackWithItsAssociation(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "parent", Title: "Parent", Type: "epic", Goal: "verified", Acceptance: []string{"overall"}})
	source := create(t, s, bug())
	sourceBefore, err := s.Get(source)
	if err != nil {
		t.Fatal(err)
	}
	definition := bug()
	definition.ID = "repair"
	definition.Parent = parent
	definition.FeedbackOf = source
	op := core.RemoteOperation{ID: "repair-create", Key: "repair-key", RequestHash: "repair-hash", TaskID: definition.ID, Expected: "absent", Actor: "authorized-executor"}
	scoped := s.WithRemoteOperation(op)
	interrupted := errors.New("association rejected")
	err = s.Store.Write(func(tx *store.Tx) error {
		task, err := scoped.CreateRemoteTaskTx(tx, definition)
		if err != nil {
			return err
		}
		if task.EpicMembership == nil || !task.EpicMembership.Required || task.EpicMembership.Parent != parent {
			t.Fatal("creation did not inherit required membership")
		}
		return interrupted
	})
	if !errors.Is(err, interrupted) {
		t.Fatal(err)
	}
	var count int
	if err = s.Store.DB.QueryRow("SELECT count(*) FROM tasks WHERE id='repair'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial repair task survived rollback", err, count)
	}
	sourceAfter, err := s.Get(source)
	if err != nil || core.RemoteRevision(sourceAfter) != core.RemoteRevision(sourceBefore) {
		t.Fatal("partial feedback survived rollback", err)
	}
	if _, found, err := s.RemoteReceipt(op.Key, op.RequestHash); err != nil || found {
		t.Fatal("receipt survived rollback", err)
	}
	if err = s.Store.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Root, ".ael/work-items/repair.md")); !os.IsNotExist(err) {
		t.Fatal("definition projection survived rollback", err)
	}
	err = s.Store.Write(func(tx *store.Tx) error { _, err := scoped.CreateRemoteTaskTx(tx, definition); return err })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.RemoteReceipt(op.Key, op.RequestHash); err != nil || !found {
		t.Fatal("committed receipt missing", err)
	}
	sourceAfter, err = s.Get(source)
	if err != nil || len(sourceAfter.Feedback) != 1 || sourceAfter.Feedback[0] != "repair" {
		t.Fatal("committed feedback missing", err)
	}
}

func TestRemoteEpicLinkCanRollbackAndRetainsRevisionFence(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "parent", Title: "Parent", Type: "epic", Goal: "verified", Acceptance: []string{"overall"}})
	leaf := create(t, s, bug())
	original, err := s.Get(leaf)
	if err != nil {
		t.Fatal(err)
	}
	op := core.RemoteOperation{ID: "link", Key: "link-key", RequestHash: "link-hash", TaskID: leaf, Expected: core.RemoteRevision(original)}
	interrupted := errors.New("finding link rejected")
	err = s.Store.Write(func(tx *store.Tx) error {
		_, err := s.WithRemoteOperation(op).LinkRemoteEpicTx(tx, parent, leaf, true, "repair")
		if err != nil {
			return err
		}
		return interrupted
	})
	if !errors.Is(err, interrupted) {
		t.Fatal(err)
	}
	current, err := s.Get(leaf)
	if err != nil || current.EpicMembership != nil || core.RemoteRevision(current) != op.Expected {
		t.Fatal("partial membership survived rollback", err)
	}
	stale := op
	stale.Expected = "stale"
	err = s.Store.Write(func(tx *store.Tx) error {
		_, err := s.WithRemoteOperation(stale).LinkRemoteEpicTx(tx, parent, leaf, true, "repair")
		return err
	})
	if err == nil {
		t.Fatal("stale association accepted")
	}
	err = s.Store.Write(func(tx *store.Tx) error {
		_, err := s.WithRemoteOperation(op).LinkRemoteEpicTx(tx, parent, leaf, true, "repair")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Flush(); err != nil {
		t.Fatal(err)
	}
	current, err = s.Get(leaf)
	if err != nil || current.EpicMembership == nil || !current.EpicMembership.Required || current.Definition.Parent != original.Definition.Parent {
		t.Fatal("link changed historical definition or lost membership", err)
	}
}

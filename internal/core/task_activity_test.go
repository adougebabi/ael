package core_test

import (
	"ael/internal/core"
	"ael/internal/store"
	"testing"
)

func TestTaskActivityReturnsRealStateChanges(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "业务进展", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	if _, err := s.Pause(id, "等待实际条件"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(id, "", false); err != nil {
		t.Fatal(err)
	}
	items, truncated, err := s.TaskActivity(id)
	if err != nil || truncated || len(items) != 2 || items[0].Kind != "resumed" || items[1].Kind != "paused" || items[1].Summary != "等待实际条件" {
		t.Fatal(items, truncated, err)
	}
	if err = s.Store.Write(func(tx *store.Tx) error { return tx.Event("", "runtime_restored", map[string]any{"tasks": 1}) }); err != nil {
		t.Fatal(err)
	}
	items, _, err = s.TaskActivity(id)
	if err != nil || !items[0].Historical || !items[1].Historical {
		t.Fatal("restored events must be marked as historical", items, err)
	}
}

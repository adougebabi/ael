package core_test

import (
	"ael/internal/core"
	"fmt"
	"testing"
)

func TestQueryPageDoesNotDuplicateTaskTransitioningDuringHydration(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "查询期间流转", Type: "bug", Goal: "verified", Acceptance: []string{"只属于一个主分组"}})
	moved := false
	s.ConstraintCheck = func(d core.Definition) []string {
		if d.ID == id && !moved {
			moved = true
			if _, e := s.Cancel(id, "查询中的真实取消"); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	}
	p, e := s.QueryPage(core.Query{Limit: 20})
	if e != nil {
		t.Fatal(e)
	}
	if !moved || len(p.Items) != 1 || p.Items[0].Task.Definition.ID != id {
		t.Fatal("one task appeared in multiple groups", p)
	}
}

func TestQueryPageOffsetsAreIndependentAcrossGroups(t *testing.T) {
	s := fixture(t)
	for i := 0; i < 25; i++ {
		create(t, s, core.Definition{ID: fmt.Sprintf("pending-%02d", i), Title: "待处理", Type: "bug", Goal: "verified", Acceptance: []string{"独立分页"}})
	}
	id := create(t, s, core.Definition{Title: "取消记录", Type: "bug", Goal: "verified", Acceptance: []string{"独立分页"}})
	if _, e := s.Cancel(id, "真实取消夹具"); e != nil {
		t.Fatal(e)
	}
	p, e := s.QueryPage(core.Query{Limit: 10, Offsets: map[string]int{"pending": 10, "cancelled": 0}})
	if e != nil {
		t.Fatal(e)
	}
	pending, cancelled := 0, 0
	for _, n := range p.Items {
		if n.Board == "pending" {
			pending++
		}
		if n.Board == "cancelled" {
			cancelled++
		}
	}
	if pending != 10 || cancelled != 1 || p.Counts["pending"] != 25 || p.Counts["cancelled"] != 1 {
		t.Fatal(p)
	}
}

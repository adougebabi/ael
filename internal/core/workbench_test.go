package core_test

import (
	"ael/internal/core"
	"fmt"
	"testing"
)

func TestWorkbenchAggregatesBeforePagingAndSearchesChildren(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{Title: "主要事项", Type: "epic", Goal: "verified", Acceptance: []string{"整体交付"}})
	for i := 0; i < 25; i++ {
		create(t, s, core.Definition{Title: fmt.Sprintf("子任务-%02d", i), Type: "bug", Parent: parent, Goal: "verified", Acceptance: []string{"实际成果"}})
	}
	p, e := s.Workbench(core.Query{Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 || p.ProjectTotal != 1 || p.ProjectChildren != 25 || len(p.Items) != 1 || p.Items[0].Children != 25 {
		t.Fatalf("%+v", p)
	}
	p, e = s.Workbench(core.Query{Text: "子任务-24", Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 || p.Items[0].Task.ID != parent || p.MatchedChildren != 1 {
		t.Fatalf("%+v", p)
	}
	children, e := s.Workbench(core.Query{Parent: parent, Text: "子任务-24", Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if children.Total != 1 || len(children.Items[0].Path) != 1 || children.Items[0].Task.Title != "子任务-24" {
		t.Fatalf("%+v", children)
	}
	p, e = s.Workbench(core.Query{Parent: parent, Limit: 20, Offset: 20})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 25 || len(p.Items) != 5 {
		t.Fatalf("%+v", p)
	}
}

func TestWorkbenchKeepsMissingParentAndCancelledChildrenReadable(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{Title: "父事项", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	child := create(t, s, core.Definition{Title: "取消子项", Type: "bug", Parent: parent, Goal: "verified", Acceptance: []string{"结果"}})
	if _, e := s.Cancel(child, "真实取消"); e != nil {
		t.Fatal(e)
	}
	orphan := create(t, s, core.Definition{Title: "旧关系", Type: "bug", Goal: "verified", Acceptance: []string{"原需求"}})
	if _, e := s.Store.DB.Exec(`UPDATE tasks SET body=json_set(body,'$.definition.parent','missing-parent') WHERE id=?`, orphan); e != nil {
		t.Fatal(e)
	}
	p, e := s.Workbench(core.Query{})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 2 || p.Anomalies != 1 {
		t.Fatalf("%+v", p)
	}
	p, e = s.Workbench(core.Query{Board: "cancelled"})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 || p.Items[0].Task.ID != parent || p.Items[0].Task.Board == "cancelled" || p.Items[0].Matches != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestWorkbenchFindsHumanAndBlockedChildWithoutChangingParent(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{Title: "父级工作", Type: "epic", Goal: "verified", Acceptance: []string{"集成"}})
	leaf := atVerify(t, s, core.Definition{Title: "待观察子任务", Type: "bug", Parent: parent, Goal: "verified", Acceptance: []string{"观察结果"}, Checks: []core.Check{{ID: "owner", Kind: "review", Method: "human", Covers: []string{"观察结果"}, Required: true}}})
	p, e := s.Workbench(core.Query{Board: "human"})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 || p.Items[0].Task.ID != parent || p.MatchedChildren != 1 || len(p.Items[0].Task.ManualReviews) != 0 {
		t.Fatalf("%+v", p)
	}
	before, err := s.Workbench(core.Query{Board: "blocked"})
	if err != nil || before.Total != 0 {
		t.Fatal("waiting for children is not itself an environment blocker", before, err)
	}
	if _, e = s.Pause(leaf, "缺少实际测试环境"); e != nil {
		t.Fatal(e)
	}
	p, e = s.Workbench(core.Query{Board: "blocked", Parent: parent})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 || p.Items[0].Task.ID != leaf {
		t.Fatalf("%+v", p)
	}
	human, e := s.HumanAttention()
	if e != nil || human.Total != 0 {
		t.Fatal(human, e)
	}
}

func TestWorkbenchSeparatesOptionalAndRequiredMembers(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{Title: "区分成员", Type: "epic", Goal: "verified", Acceptance: []string{"集成"}})
	required := create(t, s, core.Definition{Title: "必需", Type: "bug", Parent: parent, Goal: "verified", Acceptance: []string{"结果"}})
	optional := create(t, s, core.Definition{Title: "可选", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	if _, e := s.LinkEpic(parent, optional, false, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Cancel(required, "明确取消仍不满足父级门禁"); e != nil {
		t.Fatal(e)
	}
	p, e := s.Workbench(core.Query{})
	if e != nil {
		t.Fatal(e)
	}
	row := p.Items[0]
	if row.Required != 1 || row.Optional != 1 || row.RequiredCancelled != 1 || row.Satisfied != 0 {
		t.Fatal(row)
	}
}

func TestWorkbenchFamilyIncludesLegacyNonEpicAndNestedParents(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{Title: "普通功能父事项", Type: "feature", Goal: "verified", Acceptance: []string{"原需求"}})
	child := create(t, s, core.Definition{Title: "普通子事项", Type: "feature", Goal: "verified", Acceptance: []string{"子需求"}})
	leaf := create(t, s, core.Definition{Title: "深层子项", Type: "bug", Goal: "verified", Acceptance: []string{"深层需求"}})
	for _, relation := range [][2]string{{child, parent}, {leaf, child}} {
		if _, e := s.Store.DB.Exec(`UPDATE tasks SET body=json_set(body,'$.definition.parent',?) WHERE id=?`, relation[1], relation[0]); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.Workbench(core.Query{Parent: parent, Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if page.Total != 2 || page.Family.Direct != 1 || page.Family.Descendants != 2 || page.Family.Formal || page.Family.Required != 0 {
		t.Fatalf("%+v", page)
	}
	nested, e := s.Workbench(core.Query{Parent: child, Limit: 8})
	if e != nil {
		t.Fatal(e)
	}
	if nested.Total != 1 || nested.Items[0].Task.ID != leaf || nested.Family.Direct != 1 {
		t.Fatalf("%+v", nested)
	}
	actual, e := s.Get(parent)
	if e != nil || actual.Definition.Type != "feature" {
		t.Fatal(actual, e)
	}
}

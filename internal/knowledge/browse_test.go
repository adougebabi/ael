package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowseBoundsBodyAndKeepsAuthorityStatus(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge")
	os.MkdirAll(dir, 0755)
	for i := 0; i < 1001; i++ {
		r := Record{ID: fmt.Sprint(i), Kind: "reference", Status: "historical", Body: "# 可读主标题\n" + strings.Repeat("large body needle", 100)}
		if i == 0 {
			r.Kind = "spec"
			r.Status = "current"
		}
		if i == 1 {
			r.Kind = "spec"
			r.Status = "conflict"
		}
		if e := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.md", i)), Encode(r), 0644); e != nil {
			t.Fatal(e)
		}
	}
	p, e := Browse(root, BrowseQuery{View: "history", Limit: 25, Offset: 25})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 999 || len(p.Items) != 25 {
		t.Fatal(p.Total, len(p.Items))
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), `"body"`) || len(raw) > 25000 {
		t.Fatal("list leaked document bodies")
	}
	p, e = Browse(root, BrowseQuery{View: "current"})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 2 || p.Items[0].Status != "current" || p.Items[1].Status != "conflict" || p.Items[0].Title != "可读主标题" {
		t.Fatal(p)
	}
}

func TestBrowseProjectsRealScopeCountsAndTaskReferences(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	records := []Record{
		{ID: "frontend", Title: "前端规则", Kind: "spec", Status: "current", Summary: "真实摘要", Paths: []string{"apps/web", "shared"}, Tasks: []string{"explicit", "explicit"}, Body: "正文"},
		{ID: "backend", Title: "后端规则", Kind: "spec", Status: "current", Paths: []string{"apps/core"}, Body: "正文"},
		{ID: "general", Title: "项目规则", Kind: "spec", Status: "current", Body: "正文"},
		{ID: "decision", Title: "设计决定", Kind: "decision", Status: "accepted", Paths: []string{"apps/web"}, Body: "正文"},
	}
	for _, r := range records {
		if err := os.WriteFile(filepath.Join(dir, r.ID+".md"), Encode(r), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, task := range []core.Task{
		{Definition: core.Definition{ID: "explicit", Knowledge: []string{"frontend"}}},
		{Definition: core.Definition{ID: "implicit", Knowledge: []string{"frontend"}}},
	} {
		raw, _ := json.Marshal(task)
		if _, err = db.DB.Exec("INSERT INTO tasks(id,body,status,step,updated) VALUES(?,?,?,?,?)", task.Definition.ID, string(raw), "open", "execute", ""); err != nil {
			t.Fatal(err)
		}
	}
	page, err := BrowseWithUsage(&core.Service{Root: root, Store: db}, BrowseQuery{View: "current", Scope: "apps/web"})
	if err != nil || page.Total != 1 || page.Counts["current"] != 1 || page.ViewCounts["current"] != 3 || page.ViewCounts["decisions"] != 1 || page.ScopeCounts.All != 3 || page.ScopeCounts.Unclassified != 1 || page.ScopeCounts.Paths["apps/web"] != 1 || page.ScopeCounts.Paths["apps/core"] != 1 {
		t.Fatal(page, err)
	}
	if page.Items[0].ID != "frontend" || page.Items[0].TaskCount != 2 || len(page.Items[0].Hash) != 64 || page.Items[0].Description != "真实摘要" {
		t.Fatal("list must use actual references and version fingerprint", page.Items[0])
	}
	page, err = BrowseWithUsage(&core.Service{Root: root, Store: db}, BrowseQuery{View: "decisions"})
	if err != nil || page.ScopeCounts.All != 1 || page.ScopeCounts.Paths["apps/web"] != 1 {
		t.Fatal(page, err)
	}
	page, err = BrowseWithUsage(&core.Service{Root: root, Store: db}, BrowseQuery{View: "current", Text: "前端规则"})
	if err != nil || page.Total != 1 || page.ScopeCounts.All != 3 || page.ScopeCounts.Paths["apps/core"] != 1 {
		t.Fatal("scope navigation must count the whole selected category", page, err)
	}
}

func TestAggregatedChangesKeepDeliveryDatesAndCurrentCandidateArtifacts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "decision.md"), Encode(Record{ID: "multi", Kind: "decision", Status: "accepted", Summary: "明确的多任务变化", Tasks: []string{"a", "b"}}), 0644)
	db, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &core.Service{Root: root, Store: db}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b"} {
		task := core.Task{Definition: core.Definition{ID: id, Title: id, Revision: 2}, Plan: core.Plan{Revision: 3}, Status: "completed", Result: "verified", Candidate: "current", Completed: &at, Attempts: []core.Attempt{
			{Unit: "execute", Status: "pass", Candidate: "old", DefinitionRevision: 1, PlanRevision: 1, Ended: &at, Reason: "旧内容", Outputs: []string{"old.txt"}},
			{Unit: "execute", Status: "fail", Candidate: "current", DefinitionRevision: 2, PlanRevision: 3, Ended: &at, Reason: "失败内容", Outputs: []string{"failed.txt"}},
			{Unit: "execute", Status: "pass", Candidate: "current", DefinitionRevision: 2, PlanRevision: 3, Ended: &at, Reason: "最终实现", Outputs: []string{"current.txt"}},
		}}
		raw, _ := json.Marshal(task)
		if _, e := db.DB.Exec("INSERT INTO tasks(id,body,status,step,updated) VALUES(?,?,?,?,?)", id, string(raw), task.Status, "verify", at.Format(time.RFC3339)); e != nil {
			t.Fatal(e)
		}
	}
	p, e := Changes(s, BrowseQuery{}, "", "2026-10-20", "2026-10-20")
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 || p.Items[0].SourceAt != nil || p.Items[0].At == nil || p.Items[0].Result != "decided" || len(p.Items[0].Facts) != 2 {
		t.Fatalf("delivery facts collapsed into decision: %+v", p)
	}
	for _, fact := range p.Items[0].Facts {
		if fact.Result != "verified" || fact.At == nil || fact.Content != "最终实现" || len(fact.Artifacts) != 1 || fact.Artifacts[0] != "current.txt" {
			t.Fatalf("old candidate mixed into delivery: %+v", fact)
		}
	}
}

func TestDatedChangesHaveStablePaginationForEqualTimestamps(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge")
	os.MkdirAll(dir, 0755)
	for i := 0; i < 60; i++ {
		r := Record{ID: fmt.Sprintf("decision-%02d", i), Kind: "decision", Status: "accepted", OccurredAt: "2026-10-20T00:00:00Z", Body: "明确采纳的决策"}
		os.WriteFile(filepath.Join(dir, r.ID+".md"), Encode(r), 0644)
	}
	db, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &core.Service{Root: root, Store: db}
	for attempt := 0; attempt < 4; attempt++ {
		seen := map[string]bool{}
		for _, offset := range []int{0, 25, 50} {
			p, e := Changes(s, BrowseQuery{Limit: 25, Offset: offset}, "", "", "")
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range p.Items {
				if seen[c.ID] {
					t.Fatal("duplicate item across pages", c.ID)
				}
				seen[c.ID] = true
			}
		}
		if len(seen) != 60 {
			t.Fatal("missing changes", len(seen))
		}
	}
}

func TestChangeDateFilterUsesExplicitClientDayBoundaries(t *testing.T) {
	at, e := time.Parse(time.RFC3339, "2026-11-04T18:00:00Z")
	if e != nil {
		t.Fatal(e)
	}
	if !inDateRange(&at, "2026-11-04T16:00:00Z", "2026-11-05T15:59:59.999Z") {
		t.Fatal("Shanghai November 5 omitted an actual November 5 change")
	}
	if inDateRange(&at, "2026-11-03T16:00:00Z", "2026-11-04T15:59:59.999Z") {
		t.Fatal("November 5 included in Shanghai November 4")
	}
}

func TestImportedFilenameTitleUsesSourceHeadingBeforeFilename(t *testing.T) {
	r := Record{Title: "rules.md", Path: ".ael/knowledge/legacy-hash.md", Source: ".trellis/spec/app/rules.md#sha256=source", Body: "# 可读的项目规则\n保留证据。"}
	if Title(r) != "可读的项目规则" {
		t.Fatal(Title(r))
	}
	r.Body = "没有主标题"
	if Title(r) != "rules.md" {
		t.Fatal(Title(r))
	}
	r.Title = "维护过的规则标题"
	if Title(r) != r.Title {
		t.Fatal(Title(r))
	}
}

func TestReaderLinksOnlyResolveKnownSafeSources(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge")
	os.MkdirAll(dir, 0755)
	for _, id := range []string{"one", "two"} {
		os.WriteFile(filepath.Join(dir, id+".md"), Encode(Record{ID: id, Title: id, Kind: "reference", Status: "historical", Body: "# " + id}), 0644)
	}
	db, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &core.Service{Root: root, Store: db}
	if _, e := ResolveLink(s, "one", "two.md#section"); e != nil {
		t.Fatal(e)
	}
	for _, href := range []string{"missing.md", "../../../../etc/passwd", "https://external.example/", "file:///etc/passwd"} {
		if _, e := ResolveLink(s, "one", href); e == nil {
			t.Fatal("resolved forbidden link", href)
		}
	}
	os.Symlink("/etc/passwd", filepath.Join(dir, "unsafe.md"))
	if _, e := ResolveLink(s, "one", "unsafe.md"); e == nil {
		t.Fatal("followed symlink")
	}
}

func TestChangesExcludeObservationsAndDoNotInventDatesOrRelease(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge")
	os.MkdirAll(dir, 0755)
	for _, r := range []Record{{ID: "choice", Kind: "decision", Status: "rejected", Body: "拒绝远程依赖", Rationale: "离线要求"}, {ID: "raw", Kind: "reference", Status: "historical", Body: "只有导入记录"}, {ID: "old-decision", Kind: "decision", Status: "historical", Body: "未记载明确结论"}} {
		os.WriteFile(filepath.Join(dir, r.ID+".md"), Encode(r), 0644)
	}
	db, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &core.Service{Root: root, Store: db}
	if e := Sync(s); e != nil {
		t.Fatal(e)
	}
	p, e := Changes(s, BrowseQuery{}, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 || p.Items[0].Result != "rejected" || p.Items[0].At != nil || p.Items[0].Reason != "离线要求" {
		t.Fatalf("invented evolution facts: %+v", p)
	}
	p, e = Changes(s, BrowseQuery{}, "", "2026-01-01", "")
	if e != nil || p.Total != 0 {
		t.Fatal("unknown date entered dated history", p, e)
	}
}

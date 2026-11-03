package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestUnicodePathSymbolAndSupersession(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge/spec")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "a.md"), Encode(Record{ID: "a", Title: "约束", Kind: "spec", Status: "current", Paths: []string{"src/api"}, Body: "中文检索：取消不满足依赖。English acceptance. Symbol ClaimLease()"}), 0644)
	for _, q := range []string{"取消不满足", "English", "src/api", "ClaimLease()"} {
		r, e := Search(root, q)
		if e != nil || len(r) != 1 {
			t.Fatal(q, r, e)
		}
	}
	if e := Validate([]Record{{ID: "a", SupersededBy: "b"}, {ID: "b", SupersededBy: "a"}}); e == nil {
		t.Fatal("cycle allowed")
	}
	if e := Validate([]Record{{ID: "a", SupersededBy: "missing"}}); e == nil {
		t.Fatal("dangling replacement")
	}
	os.Symlink("/etc/passwd", filepath.Join(dir, "unsafe.md"))
	if _, e := ReadAll(root); e == nil {
		t.Fatal("followed symlink")
	}
}

func TestEvolutionUsesContentVersionsNotMtime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ael/knowledge/decisions")
	os.MkdirAll(dir, 0755)
	p := filepath.Join(dir, "choice.md")
	r := Record{ID: "decision", Title: "重要取舍", Kind: "decision", Status: "rejected", Body: "否决无限重试，因为可能重复发布"}
	os.WriteFile(p, Encode(r), 0644)
	db, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &core.Service{Root: root, Store: db, Active: true}
	if e = Sync(svc); e != nil {
		t.Fatal(e)
	}
	if e = Sync(svc); e != nil {
		t.Fatal(e)
	}
	events, _ := svc.Timeline(10, 0)
	if len(events) != 1 {
		t.Fatal("same content created events")
	}
	r.Body += "。替代：先核对回执。"
	os.WriteFile(p, Encode(r), 0644)
	if e = Sync(svc); e != nil {
		t.Fatal(e)
	}
	events, _ = svc.Timeline(10, 0)
	if len(events) != 2 || events[0]["kind"] != "knowledge_observed" {
		t.Fatal(events)
	}
}

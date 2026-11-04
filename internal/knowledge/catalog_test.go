package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowSourceLedgerAndFailures(t *testing.T) {
	root := t.TempDir()
	if Status(root).State != "not_indexed" {
		t.Fatal(Status(root))
	}
	if _, e := Index(root); e != nil {
		t.Fatal(e)
	}
	if Status(root).State != "empty" {
		t.Fatal(Status(root))
	}
	os.MkdirAll(filepath.Join(root, "docs/spec"), 0755)
	src := filepath.Join(root, "docs/spec/api.md")
	os.WriteFile(src, []byte("---\nid: api\nkind: spec\nstatus: current\npaths: [src/api]\n---\nExisting contract\n"), 0644)
	os.MkdirAll(filepath.Join(root, "docs/adr"), 0755)
	os.WriteFile(filepath.Join(root, "docs/adr/old.md"), []byte("---\nid: old\nkind: decision\nstatus: rejected\n---\nAvoid duplicate deployment.\n"), 0644)
	c, e := Index(root)
	if e != nil || len(c.Sources) != 2 || c.Counts["connected"] != 2 {
		t.Fatal(c, e)
	}
	all, e := ReadAll(root)
	if e != nil || len(all) != 2 {
		t.Fatal(all, e)
	}
	if inScope(all[1], core.Definition{Scope: []string{"src/apix"}}) {
		t.Fatal("path prefix crossed directory boundary")
	}
	os.WriteFile(src, []byte("Changed without reindex"), 0644)
	if _, e = ReadAll(root); e == nil || !strings.Contains(e.Error(), "KNOWLEDGE_SOURCE_CHANGED") {
		t.Fatal(e)
	}
	os.Remove(src)
	if _, e = ReadAll(root); e == nil || !strings.Contains(e.Error(), "KNOWLEDGE_SOURCE_READ_FAILED") {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, ".ael/knowledge-sources.json"), []byte("corrupt"), 0644)
	if Status(root).State != "index_error" {
		t.Fatal(Status(root))
	}
}

func TestWorkflowCriticalMissingWithoutKnowledgeDirectory(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".ael"), 0755)
	os.WriteFile(filepath.Join(root, ".ael/policy.yaml"), []byte("critical_knowledge: [missing]\n"), 0644)
	if _, e := ReadAll(root); e == nil || !strings.Contains(e.Error(), "CRITICAL_KNOWLEDGE_MISSING") {
		t.Fatal(e)
	}
}

func TestWorkflowIgnoredSourceChangesAndExplicitMigratedAuthority(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "docs/spec"), 0755)
	path := filepath.Join(root, "docs/spec/empty.md")
	os.WriteFile(path, nil, 0644)
	if _, e := Index(root); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(path, []byte("new real rule"), 0644)
	if _, e := ReadAll(root); e == nil || !strings.Contains(e.Error(), "KNOWLEDGE_SOURCE_CHANGED") {
		t.Fatal("ignored source bypassed hash", e)
	}
	Index(root)
	raw, _ := os.ReadFile(path)
	target := ".ael/knowledge/spec/imported.md"
	os.MkdirAll(filepath.Dir(filepath.Join(root, target)), 0755)
	os.WriteFile(filepath.Join(root, target), Encode(Record{ID: "migrated", Title: "migrated rule", Kind: "spec", Status: "current", Source: "docs/spec/empty.md#sha256=" + store.Hash(raw), Body: "new real rule"}), 0644)
	if _, e := ReadAll(root); e == nil || !strings.Contains(e.Error(), "KNOWLEDGE_AUTHORITY_CONFLICT") {
		t.Fatal("authority switch was implicit", e)
	}
	c, e := Index(root)
	if e != nil || c.Sources[0].Action != "retired" || c.Sources[0].Target != target {
		t.Fatal(c, e)
	}
	os.WriteFile(path, []byte("retired legacy source changes"), 0644)
	all, e := ReadAll(root)
	if e != nil || len(all) != 1 || all[0].Path != target || all[0].Body != "new real rule" {
		t.Fatal(all, e)
	}
}

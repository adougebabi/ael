package main

import (
	"ael/internal/bootstrap"
	"os"
	"path/filepath"
	"testing"
)

// The V2-02 entry point is exercised against a disposable project and runtime.
func TestEpicCLIEntryPoint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	definition := filepath.Join(t.TempDir(), "epic.json")
	if err := os.WriteFile(definition, []byte(`{"id":"epic-parent","title":"大任务","type":"epic","goal":"verified","acceptance":["整体结果"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{"task", "create", "--project", root, "--file", definition, "--json"}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if _, err := run([]string{"task", "tree", "--project", root, "--task", "epic-parent", "--json"}); err != nil {
		t.Fatalf("read epic tree: %v", err)
	}
}

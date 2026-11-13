package main

import (
	"ael/internal/bootstrap"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteUpdateUsesLightEntryWithoutLocalRuntime(t *testing.T) {
	root := t.TempDir()
	runtime := t.TempDir()
	t.Setenv("AEL_DATA_DIR", runtime)
	os.Mkdir(filepath.Join(root, ".ael"), 0755)
	os.WriteFile(filepath.Join(root, ".ael/remote.json"), []byte(`{"schema":1,"profile":"central","project_id":"shared"}`), 0644)
	value, err := run([]string{"update", "--project", root, "--dry-run", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(bootstrap.UpdateReport)
	if !ok || result.Status != "preview" {
		t.Fatalf("unexpected preview %+v", value)
	}
	if _, err := os.Stat(filepath.Join(root, "ael")); !os.IsNotExist(err) {
		t.Fatal("preview wrote entry")
	}
	if _, err := run([]string{"update", "--project", root, "--json"}); err != nil {
		t.Fatal(err)
	}
	if info, err := bootstrap.RemoteEntryInfo(root); err != nil || info.Status != "current" {
		t.Fatalf("entry %+v %v", info, err)
	}
	entries, _ := os.ReadDir(runtime)
	if len(entries) != 0 {
		t.Fatal("new Remote entry created a Local runtime")
	}
}

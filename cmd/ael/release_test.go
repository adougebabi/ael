package main

import (
	"ael/internal/release"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseCheckBypassesRemoteBindingAndRuntime(t *testing.T) {
	project := t.TempDir()
	runtime := t.TempDir()
	t.Setenv("AEL_DATA_DIR", runtime)
	os.Mkdir(filepath.Join(project, ".ael"), 0700)
	os.WriteFile(filepath.Join(project, ".ael", "remote.json"), []byte(`{"schema":1,"profile":"missing","project_id":"shared"}`), 0600)
	value, err := run([]string{"release", "check", "--project", project, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(release.Selection)
	if !ok || result.Available || result.Status != "no_configured_source" {
		t.Fatalf("unexpected check %+v", value)
	}
	entries, err := os.ReadDir(runtime)
	if err != nil || len(entries) != 0 {
		t.Fatalf("created runtime: %v %v", entries, err)
	}
}

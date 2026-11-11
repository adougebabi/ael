package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteBindingRejectsLocalOpenAndInit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if err := os.Mkdir(filepath.Join(root, ".ael"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ael", "project.yaml"), []byte(`{"schema":1,"project_id":"local","mode":"active","asset_version":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ael", "remote.json"), []byte(`corrupt binding must still prevent local fallback`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(root); err == nil || !strings.Contains(err.Error(), "REMOTE_MODE") {
		t.Fatalf("local Open accepted remote binding: %v", err)
	}
	if _, err := Init(root, false, false); err == nil || !strings.Contains(err.Error(), "REMOTE_MODE") {
		t.Fatalf("local Init accepted remote binding: %v", err)
	}
	if _, err := os.Stat(DataDir(root)); !os.IsNotExist(err) {
		t.Fatalf("remote project opened a local runtime: %v", err)
	}
}

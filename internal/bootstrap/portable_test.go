package bootstrap

import (
	"ael/internal/store"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func testBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := []byte("fixture binary, not an execution test")
	notice := []byte("fixture notice")
	os.WriteFile(filepath.Join(dir, "ael-darwin-arm64"), body, 0755)
	os.WriteFile(filepath.Join(dir, "NOTICE"), notice, 0644)
	lock := fmt.Sprintf("version %s\ndarwin-arm64 %s ael-darwin-arm64\nnotice %s NOTICE\n", Version, store.Hash(body), store.Hash(notice))
	os.WriteFile(filepath.Join(dir, "ael-toolchain.lock"), []byte(lock), 0644)
	return dir
}
func TestPortableInitCopiesLockedAssetsAndPreservesIdentity(t *testing.T) {
	source := testBundle(t)
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	r, e := InitProject(root, true, false, source)
	if e != nil || r.Bundle == nil {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(filepath.Join(root, "ael")); !os.IsNotExist(e) {
		t.Fatal("dry-run wrote launcher")
	}
	r, e = InitProject(root, false, false, source)
	if e != nil {
		t.Fatal(e)
	}
	for _, rel := range []string{"ael", ".ael/toolchain.lock", ".ael/bin/darwin-arm64/ael", ".ael/THIRD_PARTY_NOTICES.txt"} {
		if _, e = os.Stat(filepath.Join(root, rel)); e != nil {
			t.Fatal(rel, e)
		}
	}
	again, e := InitProject(root, false, false, source)
	if e != nil || again.Config.ProjectID != r.Config.ProjectID {
		t.Fatal(again, e)
	}
	os.WriteFile(filepath.Join(root, "ael"), []byte("user launcher"), 0755)
	if _, e = InitProject(root, false, true, source); e == nil {
		t.Fatal("overwrote user launcher")
	}
}
func TestPortableManifestRejectsCorruptionAndTraversal(t *testing.T) {
	dir := testBundle(t)
	os.WriteFile(filepath.Join(dir, "ael-darwin-arm64"), []byte("corruption"), 0755)
	if _, e := ReadBundle(dir); e == nil {
		t.Fatal("bad payload accepted")
	}
	os.WriteFile(filepath.Join(dir, "ael-toolchain.lock"), []byte(fmt.Sprintf("version %s\ndarwin-arm64 %s ../outside\n", Version, store.Hash([]byte("x")))), 0644)
	if _, e := ReadBundle(dir); e == nil {
		t.Fatal("path traversal accepted")
	}
}

package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitDryRunIdempotentAndUserConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	original := "# 用户规则\n保持这个内容。\n"
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(original), 0644)
	r, e := Init(root, true, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".ael")); !os.IsNotExist(e) {
		t.Fatal("dry run wrote project")
	}
	r, e = Init(root, false, false)
	if e != nil {
		t.Fatal(e)
	}
	again, e := Init(root, false, false)
	if e != nil || r.Config.ProjectID != again.Config.ProjectID {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), original) || strings.Count(string(b), begin) != 1 {
		t.Fatal(string(b))
	}
	skill := filepath.Join(root, ".agents/skills/ael-work/SKILL.md")
	os.WriteFile(skill, []byte("user edit"), 0644)
	if _, e = Init(root, false, true); e == nil {
		t.Fatal("overwrote edited skill")
	}
	b, _ = os.ReadFile(skill)
	if string(b) != "user edit" {
		t.Fatal("lost user edit")
	}
}
func TestShadowAndSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	os.Mkdir(filepath.Join(root, ".trellis"), 0755)
	r, e := Init(root, false, false)
	if e != nil || r.Config.Mode != "shadow" {
		t.Fatal(r, e)
	}
	s, _, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	s.Store.Close()
	other := t.TempDir()
	os.Symlink(other, filepath.Join(other, ".ael"))
	if _, e = Init(other, false, false); e == nil {
		t.Fatal("symlink write allowed")
	}
}

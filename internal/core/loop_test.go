package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) *core.Service {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, e := bootstrap.Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	s, _, e := bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Store.Close() })
	return s
}
func create(t *testing.T, s *core.Service, d core.Definition) string {
	t.Helper()
	n, e := s.Create(d)
	if e != nil {
		t.Fatal(e)
	}
	return n.Task.Definition.ID
}
func step(t *testing.T, s *core.Service, id, actor string, refs ...string) core.Next {
	t.Helper()
	n, e := s.Claim(id, "", actor, "", "")
	if e != nil {
		t.Fatal(e)
	}
	a := n.Attempt
	n, e = s.Submit(core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: core.ID("test-"), Result: core.StepResult{Status: "pass", Summary: "real step deliverable", Artifacts: refs}})
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func TestP1RealFailureRepairLoop(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{Title: "修复真实文件", Type: "bug", Goal: "verified", Acceptance: []string{"内容为fixed"}, Checks: []core.Check{{ID: "content", Kind: "functional", Command: []string{"sh", "-c", "test \"$(cat deliverable.txt)\" = fixed"}, Covers: []string{"内容为fixed"}, Required: true}}})
	step(t, s, id, "one")
	os.WriteFile(filepath.Join(s.Root, "deliverable.txt"), []byte("broken"), 0644)
	n := step(t, s, id, "one", "deliverable.txt")
	if n.Step != "verify" {
		t.Fatal(n)
	}
	n, e := s.Verify(id, "content", "checker")
	if e != nil {
		t.Fatal(e)
	}
	if n.Action != "repair" || n.Step != "execute" || n.Task.Status == "completed" {
		t.Fatalf("failure lost %+v", n)
	}
	os.WriteFile(filepath.Join(s.Root, "deliverable.txt"), []byte("fixed"), 0644)
	step(t, s, id, "two", "deliverable.txt")
	n, e = s.Verify(id, "content", "checker")
	if e != nil {
		t.Fatal(e)
	}
	if n.Action != "completed" || len(n.Task.Attempts) != 5 {
		t.Fatalf("not closed %+v", n)
	}
	if _, e = os.Stat(filepath.Join(s.Root, ".ael/outcomes/"+id+".json")); e != nil {
		t.Fatal(e)
	}
	events, e := s.Events(id)
	if e != nil || len(events) < 10 {
		t.Fatal(events, e)
	}
	if n.Task.Evidence[0].Source != "command" {
		t.Fatal("not command evidence")
	}
}

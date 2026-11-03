package execution

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func managedFixture(t *testing.T) (*core.Service, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	bootstrap.Init(root, false, false)
	s, _, e := bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Store.Close() })
	n, e := s.Create(core.Definition{Title: "确定性测试执行器，不是 Codex", Type: "bug", Goal: "verified", Acceptance: []string{"fixed file"}, Checks: []core.Check{{ID: "functional", Kind: "functional", Command: []string{"sh", "-c", "test \"$(cat result.txt)\" = fixed"}, Covers: []string{"fixed file"}, Required: true}}})
	if e != nil {
		t.Fatal(e)
	}
	return s, n.Task.Definition.ID
}
func TestManagedCommandContinuousFailureRepair(t *testing.T) {
	s, id := managedFixture(t)
	script := `if [ "$AEL_STEP" = execute ]; then
 if [ -f result.txt ]; then printf fixed > result.txt; else printf broken > result.txt; fi
fi
printf '%s' '{"status":"pass","summary":"fixture executor result","artifacts":["result.txt"],"return_to":"","delivery":null}' > "$AEL_RESULT"`
	n, e := Run(s, id, Config{Command: []string{"sh", "-c", script}, MaxSteps: 10})
	if e != nil {
		t.Fatal(e)
	}
	if n.Action != "completed" || len(n.Task.Attempts) != 5 {
		b, _ := json.MarshalIndent(n.Task, "", "  ")
		t.Fatalf("%+v\n%s", n, b)
	}
	if n.Task.Attempts[0].PID == 0 {
		t.Fatal("process not recorded")
	}
}
func TestProcessZeroExitWithoutResultAndRecursion(t *testing.T) {
	s, id := managedFixture(t)
	n, e := Run(s, id, Config{Command: []string{"sh", "-c", "exit 0"}})
	if e != nil || n.Action == "completed" || !n.Task.Paused {
		t.Fatal(n, e)
	}
	if _, e = Run(s, id, Config{Command: []string{"ael", "loop", "run"}}); e == nil {
		t.Fatal("recursive loop allowed")
	}
	t.Setenv("AEL_MANAGED_CHILD", "1")
	if _, e = Run(s, id, Config{Command: []string{"sh"}}); e == nil {
		t.Fatal("child started parent loop")
	}
}
func TestDirectedSessionParse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	os.WriteFile(p, []byte("raw stderr\n{\"type\":\"thread.started\",\"thread_id\":\"exact-id\"}\n"), 0600)
	if SessionFromLog(p) != "exact-id" {
		t.Fatal("session parse failed")
	}
}

func TestBackgroundProcessNeverReleasesWorkspaceAsSuccess(t *testing.T) {
	s, id := managedFixture(t)
	script := `(sleep 5; printf late > result.txt) &
printf '%s' '{"status":"pass","summary":"parent exited","artifacts":[],"return_to":"","delivery":null}' > "$AEL_RESULT"`
	n, e := Run(s, id, Config{Command: []string{"sh", "-c", script}, TimeoutSeconds: 10})
	if e != nil {
		t.Fatal(e)
	}
	if n.Action != "wait_environment" || n.Task.Attempts[0].Status != "interrupted" {
		t.Fatal(n)
	}
	other, e := s.Create(core.Definition{Title: "another writer", Type: "bug", Goal: "verified", Acceptance: []string{"ok"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Claim(other.Task.Definition.ID, "", "writer", "", ""); e == nil {
		t.Fatal("workspace released with unacknowledged background group")
	}
}

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
	checkpoint, e := s.WorkCheckpoint("", "single:"+id)
	if e != nil || checkpoint.Blocker == nil || checkpoint.Checkpoint.Unit == "" || checkpoint.Blocker.Input == "" || checkpoint.Blocker.Next == "" {
		t.Fatalf("single-task checkpoint lost recovery details: %+v %v", checkpoint, e)
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

func TestBackgroundProcessPausesOnlyItsTask(t *testing.T) {
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
	if _, e = s.Claim(id, "", "replacement", "", ""); e == nil {
		t.Fatal("interrupted task resumed without recovery")
	}
	if _, e = s.Claim(other.Task.Definition.ID, "", "writer", "", ""); e != nil {
		t.Fatal("independent task blocked by interrupted executor", e)
	}
}

func TestManagedReviewFailuresPersistBlocked(t *testing.T) {
	for _, script := range []string{"exit 1", "exit 0", "printf invalid > \"$AEL_RESULT\""} {
		t.Run(script, func(t *testing.T) {
			s, id := managedFixture(t)
			// Prepare an actual candidate, then test only the review executor failure.
			for i := 0; i < 2; i++ {
				n, e := s.Claim(id, "", "fixture", "", "")
				if e != nil {
					t.Fatal(e)
				}
				refs := []string{}
				if i == 1 {
					os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("fixed"), 0644)
					refs = []string{"result.txt"}
				}
				if _, e = s.Submit(core.Submission{Task: id, Attempt: n.Attempt.ID, Token: n.Attempt.Token, Key: n.Attempt.ID, Result: core.StepResult{Status: "pass", Summary: "fixture preparation", Artifacts: refs}}); e != nil {
					t.Fatal(e)
				}
			}
			n, _ := s.Next(id)
			d := n.Task.Definition
			d.Checks = []core.Check{{ID: "functional", Kind: "review", Method: "model", Covers: d.Acceptance, Required: true}}
			n, e := s.Edit(id, d, "review capability fixture")
			if e != nil || n.Step != "verify" {
				t.Fatal(n, e)
			}
			n, e = Run(s, id, Config{Command: []string{"sh", "-c", script}})
			if e != nil || n.Action != "wait_environment" || n.Task.Blocker == nil {
				t.Fatal(n, e)
			}
			if len(n.Task.Evidence) == 0 || n.Task.Evidence[len(n.Task.Evidence)-1].Result != "blocked" {
				t.Fatal("review failure has no blocked evidence")
			}
			for _, a := range n.Task.Attempts {
				if a.Status == "running" {
					t.Fatal("failed child left live attempt")
				}
			}
		})
	}
}

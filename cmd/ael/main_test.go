package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCLISubprocess(t *testing.T) {
	if os.Getenv("AEL_CLI_HELPER") != "1" {
		return
	}
	at := 0
	for i, a := range os.Args {
		if a == "--" {
			at = i + 1
			break
		}
	}
	v, e := run(os.Args[at:])
	if e != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"error": e.Error()})
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(v)
	os.Exit(0)
}

func TestUpdateCommandScopesAndRemovedRefresh(t *testing.T) {
	parsed := parse([]string{"--dry-run", "upgrade"})
	if strings.Join(parsed.Words, " ") != "upgrade" || !parsed.has("dry-run") {
		t.Fatal("boolean flag consumed command", parsed)
	}
	for _, args := range [][]string{{"refresh"}, {"upgrade", "--project", "."}, {"update", "--from"}, {"update", "--dry-run=invalid"}, {"upgrade", "--unknown"}} {
		if _, e := run(args); e == nil {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
	help, e := run([]string{"help"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(help)
	if strings.Contains(string(b), `"refresh"`) || !strings.Contains(string(b), `upgrade [`) || !strings.Contains(string(b), `update [`) {
		t.Fatal(string(b))
	}
	old, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	defer os.Chdir(old)
	root := t.TempDir()
	if e = os.Chdir(root); e != nil {
		t.Fatal(e)
	}
	if _, e = run([]string{"update"}); e == nil || !strings.Contains(e.Error(), "NOT_INITIALIZED") {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".ael")); !os.IsNotExist(e) {
		t.Fatal("update initialized current directory")
	}
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, e = bootstrap.Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	if _, e = run([]string{"update", "--from", filepath.Join(root, "missing")}); e == nil || strings.Contains(e.Error(), "NOT_INITIALIZED") {
		t.Fatal("update did not detect current project", e)
	}
}
func TestMultiProcessProtocol(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	bootstrap.Init(root, false, false)
	s, _, e := bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	n, e := s.Create(core.Definition{Title: "multiprocess", Type: "bug", Goal: "verified", Acceptance: []string{"result"}})
	if e != nil {
		t.Fatal(e)
	}
	id := n.Task.Definition.ID
	s.Store.Close()
	var mu sync.Mutex
	var wg sync.WaitGroup
	wins := []core.Next{}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestCLISubprocess", "--", "claim", "--task", id, "--executor", "worker", "--project", root)
			cmd.Env = append(os.Environ(), "AEL_CLI_HELPER=1")
			b, e := cmd.CombinedOutput()
			if e == nil {
				var n core.Next
				if err := json.Unmarshal(b, &n); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				wins = append(wins, n)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(wins) != 1 {
		t.Fatal("multi-process winners", len(wins))
	}
	a := wins[0].Attempt
	sub := core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: "multi-submit", Result: core.StepResult{Status: "pass", Summary: "defined"}}
	b, _ := json.Marshal(sub)
	path := filepath.Join(t.TempDir(), "submit.json")
	os.WriteFile(path, b, 0600)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestCLISubprocess", "--", "submit", "--file", path, "--project", root)
			cmd.Env = append(os.Environ(), "AEL_CLI_HELPER=1")
			b, e := cmd.CombinedOutput()
			if e != nil {
				t.Errorf("idempotent submit %v %s", e, b)
			}
		}()
	}
	wg.Wait()
	s, _, e = bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Store.Close()
	task, e := s.Get(id)
	if e != nil || task.Step != "execute" || len(task.Attempts) != 1 {
		t.Fatal(task, e)
	}
}

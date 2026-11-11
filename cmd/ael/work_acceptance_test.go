package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestParentNextWorkCLIEntryPoint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []core.Definition{
		{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}},
		{ID: "leaf", Parent: "epic", Title: "叶子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}},
	} {
		if _, err = s.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = run([]string{"work", "next", "--project", root, "--task", "epic", "--runner", "runner-1", "--json"}); err != nil {
		t.Fatalf("parent next-work unavailable: %v", err)
	}
}

func TestConcurrentProcessNextWorkClaim(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ael")
	if output, err := exec.Command("go", "build", "-mod=readonly", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []core.Definition{
		{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}},
		{ID: "leaf", Parent: "epic", Title: "叶子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}},
	} {
		if _, err = s.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		value  core.WorkResult
		err    error
		stderr string
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, runner := range []string{"runner-a", "runner-b"} {
		wg.Add(1)
		go func(runner string) {
			defer wg.Done()
			cmd := exec.Command(binary, "work", "claim", "--project", root, "--parent", "epic", "--runner", runner, "--json")
			cmd.Dir, cmd.Env = root, os.Environ()
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			var value core.WorkResult
			if err == nil {
				err = json.Unmarshal(out.Bytes(), &value)
			}
			results <- outcome{value, err, stderr.String()}
		}(runner)
	}
	wg.Wait()
	close(results)
	claimed := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("claim: %v %s", result.err, result.stderr)
		}
		if result.value.Action == "claimed" {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("expected one claimed attempt, got %d", claimed)
	}
	s, _, err = bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	task, err := s.Get("leaf")
	if err != nil || len(task.Attempts) != 1 {
		t.Fatalf("duplicate attempts: %+v %v", task.Attempts, err)
	}
}

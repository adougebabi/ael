package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestConcurrentProcessPlanApply(t *testing.T) {
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
	if _, err = s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}}); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PreparePlan("epic", core.PlanInput{Request: "合成请求"})
	if err != nil {
		t.Fatal(err)
	}
	makeLeaf := func(key string, order int) core.PlanLeaf {
		return core.PlanLeaf{Key: key, Order: order, Required: true, Deliverables: []string{key}, Basis: []string{"原文"}, Capabilities: []string{"开发"},
			Definition: core.Definition{ID: "process-" + key, Title: key, Type: "bug", Goal: "verified", Scope: []string{key}, NonGoals: []string{"不发布"}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"结果"}, Required: true}}}}
	}
	p := core.PlanProposal{Version: 1, ID: "process-plan", Parent: "epic", ParentRevision: 1, InputHash: prepared.InputHash, Request: "合成请求", Leaves: []core.PlanLeaf{makeLeaf("one", 1), makeLeaf("two", 2)}, Coverage: []core.PlanCoverage{{Acceptance: "整体", Leaves: []string{"one", "two"}}}}
	preview, err := s.PreviewPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	proposal := filepath.Join(t.TempDir(), "proposal.json")
	raw, _ := json.Marshal(p)
	if err = os.WriteFile(proposal, raw, 0600); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result core.PlanApplyResult
		err    error
		stderr string
	}
	results := make(chan outcome, 3)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(binary, "plan", "apply", "--project", root, "--file", proposal, "--digest", preview.Digest, "--json")
			cmd.Dir, cmd.Env = root, os.Environ()
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			var result core.PlanApplyResult
			if err == nil {
				err = json.Unmarshal(out.Bytes(), &result)
			}
			results <- outcome{result, err, stderr.String()}
		}()
	}
	wg.Wait()
	close(results)
	var first core.PlanApplyResult
	for result := range results {
		if result.err != nil {
			t.Fatalf("process apply: %v %s", result.err, result.stderr)
		}
		if first.Status == "" {
			first = result.result
		} else if !reflect.DeepEqual(first, result.result) {
			t.Fatalf("different receipts: %+v %+v", first, result.result)
		}
	}
	s, _, err = bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	tree, err := s.Tree("epic")
	if err != nil || len(tree.Children) != 2 {
		t.Fatalf("duplicate or missing tasks: %+v %v", tree, err)
	}
}

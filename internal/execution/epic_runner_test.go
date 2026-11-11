package execution

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEpicFixtureExecutor(t *testing.T) {
	if os.Getenv("AEL_V204_FIXTURE") != "1" {
		return
	}
	var input struct {
		Next struct {
			Definition core.Definition `json:"definition"`
		} `json:"next"`
	}
	body, err := os.ReadFile(os.Getenv("AEL_CONTEXT"))
	if err != nil || json.Unmarshal(body, &input) != nil {
		os.Exit(3)
	}
	id := input.Next.Definition.ID
	artifacts := []string{}
	if os.Getenv("AEL_STEP") == "execute" {
		name := "result-" + id + ".txt"
		value := "fixed"
		if id == "02-second" {
			if _, err := os.Stat(name); os.IsNotExist(err) {
				value = "broken"
			}
		}
		if err = os.WriteFile(name, []byte(value), 0644); err != nil {
			os.Exit(4)
		}
		artifacts = append(artifacts, name)
	}
	result := core.StepResult{Status: "pass", Summary: "deterministic isolated executor", Artifacts: artifacts,
		Knowledge: &core.KnowledgeOutcome{Status: "unchanged", Reason: "fixture only", Artifacts: []string{}}}
	body, err = json.Marshal(result)
	if err != nil || os.WriteFile(os.Getenv("AEL_RESULT"), body, 0600) != nil {
		os.Exit(5)
	}
	os.Exit(0)
}

func TestEpicRunnerRepairsBeforeAdvancingAndResumes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	t.Setenv("AEL_V204_FIXTURE", "1")
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"01-first", "02-second", "03-third"} {
		if i == 0 {
			if _, err = s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}}); err != nil {
				t.Fatal(err)
			}
		}
		d := core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Scope: []string{"result-" + id + ".txt"}, Acceptance: []string{"fixed"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test \"$(cat result-" + id + ".txt)\" = fixed"}, Covers: []string{"fixed"}, Required: true}}}
		if i > 0 {
			d.Dependencies = []string{[]string{"01-first", "02-second"}[i-1]}
		}
		if _, err = s.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	config := Config{Runner: "runner-1", Command: []string{os.Args[0], "-test.run=^TestEpicFixtureExecutor$"}, MaxSteps: 6}
	result, err := RunEpic(s, "epic", config)
	if err != nil || result.StopReason != "max_steps" || result.Checkpoint.Task != "02-second" {
		t.Fatalf("first stop: %+v %v", result, err)
	}
	first, _ := s.Get("01-first")
	second, _ := s.Get("02-second")
	third, _ := s.Get("03-third")
	if first.Status != "completed" || second.Route != "repair" || second.Step != "execute" || len(third.Attempts) != 0 {
		t.Fatalf("failure skipped: first=%+v second=%+v third=%+v", first, second, third)
	}
	rootDir := s.Root
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s, _, err = bootstrap.Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	checkpoint, err := s.WorkCheckpoint("epic", "runner-1")
	if err != nil || checkpoint.StopReason != "max_steps" || checkpoint.Checkpoint.Task != "02-second" {
		t.Fatalf("checkpoint lost: %+v %v", checkpoint, err)
	}
	config.MaxSteps = 1
	result, err = RunEpic(s, "epic", config)
	if err != nil || result.StopReason != "max_steps" || result.Checkpoint.Task != "02-second" {
		t.Fatalf("repair stop: %+v %v", result, err)
	}
	third, _ = s.Get("03-third")
	if len(third.Attempts) != 0 {
		t.Fatal("third started before second verified")
	}
	config.MaxSteps = 3
	result, err = RunEpic(s, "epic", config)
	if err != nil || result.StopReason != "max_steps" {
		t.Fatalf("continuation: %+v %v", result, err)
	}
	second, _ = s.Get("02-second")
	third, _ = s.Get("03-third")
	if second.Status != "completed" || len(third.Attempts) == 0 {
		t.Fatalf("did not advance after repair: second=%+v third=%+v", second, third)
	}
}

func TestEpicRunnerWaitsWithoutExecutor(t *testing.T) {
	s, _ := managedFixture(t)
	if _, err := s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(core.Definition{ID: "leaf", Parent: "epic", Title: "叶子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}}); err != nil {
		t.Fatal(err)
	}
	result, err := RunEpic(s, "epic", Config{Runner: "runner"})
	if err != nil || result.Action != "wait_capability" || result.StopReason != "wait_capability" {
		t.Fatalf("missing executor: %+v %v", result, err)
	}
	checkpoint, err := s.WorkCheckpoint("epic", "runner")
	if err != nil || checkpoint.StopReason != "wait_capability" {
		t.Fatalf("missing checkpoint: %+v %v", checkpoint, err)
	}
	result, err = RunEpic(s, "epic", Config{Runner: "runner", Command: []string{"ael-intentionally-missing-v204-executor"}})
	if err != nil || result.Action != "wait_capability" {
		t.Fatalf("missing executable did not wait: %+v %v", result, err)
	}
	leaf, err := s.Get("leaf")
	if err != nil || len(leaf.Attempts) != 0 {
		t.Fatalf("missing executor created work: %+v %v", leaf.Attempts, err)
	}
}

func TestSingleTaskLoopBoundedExitHasCheckpoint(t *testing.T) {
	s, id := managedFixture(t)
	script := `printf '%s' '{"status":"pass","summary":"defined","artifacts":[],"return_to":"","delivery":null}' > "$AEL_RESULT"`
	n, err := Run(s, id, Config{Command: []string{"sh", "-c", script}, MaxSteps: 1})
	if err != nil || n.StopReason != "max_steps" || n.Checkpoint == nil || n.Checkpoint.Task != id {
		t.Fatalf("bounded exit: %+v %v", n, err)
	}
	checkpoint, err := s.WorkCheckpoint("", "single:"+id)
	if err != nil || checkpoint.StopReason != "max_steps" || checkpoint.Checkpoint.Task != id {
		t.Fatalf("stored checkpoint: %+v %v", checkpoint, err)
	}
}

func TestEpicRunnerPreservesWaitReasonAtStepLimit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	t.Setenv("AEL_V204_FIXTURE", "1")
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	if _, err = s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}}); err != nil {
		t.Fatal(err)
	}
	input, err := s.PreparePlan("epic", core.PlanInput{Request: "两个结果"})
	if err != nil {
		t.Fatal(err)
	}
	leaf := func(key, id, capability string, order int) core.PlanLeaf {
		name := "result-" + id + ".txt"
		return core.PlanLeaf{Key: key, Order: order, Required: true, Deliverables: []string{name}, Basis: []string{"请求"}, Capabilities: []string{capability},
			Definition: core.Definition{ID: id, Title: id, Type: "bug", Goal: "verified", Scope: []string{name}, NonGoals: []string{"不发布"}, Acceptance: []string{"fixed"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f " + name}, Covers: []string{"fixed"}, Required: true}}}}
	}
	proposal := core.PlanProposal{Version: 1, ID: "plan", Parent: "epic", ParentRevision: 1, InputHash: input.InputHash, Request: "两个结果", Leaves: []core.PlanLeaf{leaf("one", "01-first", "common", 1), leaf("two", "02-second", "database", 2)}, Coverage: []core.PlanCoverage{{Acceptance: "整体", Leaves: []string{"one", "two"}}}}
	proposal.Leaves[1].DependsOn = []string{"one"}
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err != nil {
		t.Fatal(err)
	}
	result, err := RunEpic(s, "epic", Config{Runner: "runner", Capabilities: []string{"common"}, Command: []string{os.Args[0], "-test.run=^TestEpicFixtureExecutor$"}, MaxSteps: 3})
	if err != nil || result.StopReason != "wait_capability" || result.Checkpoint.Task != "02-second" || !strings.Contains(result.Detail, "database") {
		t.Fatalf("step limit hid missing capability: %+v %v", result, err)
	}
	checkpoint, err := s.WorkCheckpoint("epic", "runner")
	if err != nil || checkpoint.StopReason != "wait_capability" {
		t.Fatalf("stored reason lost: %+v %v", checkpoint, err)
	}
}

func TestEpicRunnerContinuesAcrossLeavesWithoutUserIntervention(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	t.Setenv("AEL_V204_FIXTURE", "1")
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	if _, err = s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"01-first", "02-second", "03-third"} {
		d := core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Scope: []string{"result-" + id + ".txt"}, Acceptance: []string{"fixed"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test \"$(cat result-" + id + ".txt)\" = fixed"}, Covers: []string{"fixed"}, Required: true}}}
		if i > 0 {
			d.Dependencies = []string{[]string{"01-first", "02-second"}[i-1]}
		}
		if _, err = s.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	result, err := RunEpic(s, "epic", Config{Runner: "runner", Command: []string{os.Args[0], "-test.run=^TestEpicFixtureExecutor$"}, MaxSteps: 9})
	if err != nil || result.StopReason != "max_steps" || result.Checkpoint.Task != "03-third" {
		t.Fatalf("continuous run: %+v %v", result, err)
	}
	first, _ := s.Get("01-first")
	second, _ := s.Get("02-second")
	third, _ := s.Get("03-third")
	if first.Status != "completed" || second.Status != "completed" || len(third.Attempts) != 1 {
		t.Fatalf("continuous sequence missing: %s %s %d", first.Status, second.Status, len(third.Attempts))
	}
	failedBeforeThird := false
	for _, evidence := range second.EvidenceHistory {
		if evidence.Result == "fail" {
			failedBeforeThird = true
		}
	}
	if !failedBeforeThird {
		t.Fatal("second leaf's actual failed check was not retained")
	}
}

func TestSingleLoopReportsBudgetBeforeStepLimit(t *testing.T) {
	s, id := managedFixture(t)
	if _, err := s.SetTimeLimit(id, 1, "fixture budget"); err != nil {
		t.Fatal(err)
	}
	script := `sleep 2; printf '%s' '{"status":"pass","summary":"defined","artifacts":[],"return_to":"","delivery":null}' > "$AEL_RESULT"`
	n, err := Run(s, id, Config{Command: []string{"sh", "-c", script}, MaxSteps: 1})
	if err != nil || n.StopReason != "budget_stopped" {
		t.Fatalf("budget hidden by step limit: %+v %v", n, err)
	}
	checkpoint, err := s.WorkCheckpoint("", "single:"+id)
	if err != nil || checkpoint.StopReason != "budget_stopped" {
		t.Fatalf("budget checkpoint: %+v %v", checkpoint, err)
	}
}

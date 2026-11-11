package core_test

import (
	"ael/internal/core"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func workFixture(t *testing.T) (*core.Service, core.WorkRequest) {
	t.Helper()
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	for i, id := range []string{"01-first", "02-second", "03-third"} {
		d := core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Scope: []string{id}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"结果"}, Required: true}}}
		if i > 0 {
			d.Dependencies = []string{[]string{"01-first", "02-second"}[i-1]}
		}
		create(t, s, d)
	}
	return s, core.WorkRequest{Parent: "epic", Runner: "runner-1"}
}

func TestNextWorkClaimsInStableOrderAndEnforcesWIP(t *testing.T) {
	s, request := workFixture(t)
	choice, err := s.NextWork(request)
	if err != nil || choice.Action != "ready" || choice.Checkpoint.Task != "01-first" {
		t.Fatalf("selection: %+v %v", choice, err)
	}
	claim, err := s.ClaimNextWork(request)
	if err != nil || claim.Action != "claimed" || claim.Next == nil || claim.Next.Attempt == nil || claim.Checkpoint.Task != "01-first" {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	waiting, err := s.ClaimNextWork(request)
	if err != nil || waiting.Action != "wait_executor" {
		t.Fatalf("duplicate claim: %+v %v", waiting, err)
	}
	other := request
	other.Runner = "runner-2"
	waiting, err = s.ClaimNextWork(other)
	if err != nil || waiting.Action != "wait_wip" {
		t.Fatalf("WIP not enforced: %+v %v", waiting, err)
	}
	if _, err = s.ReleaseWork("epic", request.Runner, "handoff"); err == nil || !strings.Contains(err.Error(), "RUNNING_ATTEMPT") {
		t.Fatalf("active owner released: %v", err)
	}
	a := claim.Next.Attempt
	submission := core.Submission{Task: "01-first", Attempt: a.ID, Token: a.Token, Key: "step-one", Result: core.StepResult{Status: "pass", Summary: "defined"}}
	_, err = s.Submit(submission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(submission); err != nil {
		t.Fatalf("idempotent submit failed: %v", err)
	}
	next, err := s.ClaimNextWork(request)
	if err != nil || next.Action != "claimed" || next.Checkpoint.Task != "01-first" || next.Checkpoint.Step != "execute" {
		t.Fatalf("skipped unfinished leaf: %+v %v", next, err)
	}
	if _, err = s.Submit(submission); err != nil {
		t.Fatalf("late submit replay failed: %v", err)
	}
	first, err := s.Get("01-first")
	if err != nil || len(first.Attempts) != 2 {
		t.Fatalf("late replay duplicated a claim: %+v %v", first.Attempts, err)
	}
	third, err := s.Get("03-third")
	if err != nil || len(third.Attempts) != 0 {
		t.Fatalf("late replay started a successor: %+v %v", third.Attempts, err)
	}
}

func TestConcurrentNextWorkClaimsOnlyOneAttempt(t *testing.T) {
	s, request := workFixture(t)
	var wg sync.WaitGroup
	results := make(chan core.WorkResult, 2)
	errors := make(chan error, 2)
	for _, runner := range []string{"runner-a", "runner-b"} {
		wg.Add(1)
		go func(runner string) {
			defer wg.Done()
			r := request
			r.Runner = runner
			result, err := s.ClaimNextWork(r)
			results <- result
			errors <- err
		}(runner)
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	claimed := 0
	for r := range results {
		if r.Action == "claimed" {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("claims=%d", claimed)
	}
	task, err := s.Get("01-first")
	if err != nil || len(task.Attempts) != 1 {
		t.Fatalf("duplicate attempt: %+v %v", task.Attempts, err)
	}
}

func TestNextWorkWaitsForRequiredCapabilityWithoutSpinning(t *testing.T) {
	s, proposal := proposalFixture(t)
	proposal.Leaves[0].Capabilities = []string{"database"}
	proposal.Leaves[1].DependsOn = []string{"one"}
	preview, err := s.PreviewPlan(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(proposal, preview.Digest); err != nil {
		t.Fatal(err)
	}
	request := core.WorkRequest{Parent: "epic", Runner: "runner", Capabilities: []string{"frontend"}}
	var before int
	if err = s.Store.DB.QueryRow("SELECT count(*) FROM events").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		result, err := s.ClaimNextWork(request)
		if err != nil || result.Action != "wait_capability" || !strings.Contains(result.Detail, "database") {
			t.Fatalf("missing capability: %+v %v", result, err)
		}
	}
	var after int
	if err = s.Store.DB.QueryRow("SELECT count(*) FROM events").Scan(&after); err != nil || before != after {
		t.Fatalf("waiting wrote events: %d %d %v", before, after, err)
	}
	request.Capabilities = []string{"database"}
	result, err := s.ClaimNextWork(request)
	if err != nil || result.Action != "claimed" || result.Checkpoint.Task == "" {
		t.Fatalf("capable runner did not claim: %+v %v", result, err)
	}
}

func TestNextWorkReportsOfflineRunnerAndRequiresRecovery(t *testing.T) {
	s, request := workFixture(t)
	claim, err := s.ClaimNextWork(request)
	if err != nil || claim.Action != "claimed" {
		t.Fatal(claim, err)
	}
	if _, err = s.Store.DB.Exec(`UPDATE tasks SET body=json_set(body,'$.attempts[0].lease_until','2000-01-01T00:00:00Z') WHERE id='01-first'`); err != nil {
		t.Fatal(err)
	}
	result, err := s.NextWork(request)
	if err != nil || result.Action != "runner_offline" || result.Checkpoint.Task != "01-first" {
		t.Fatalf("offline runner not distinguished: %+v %v", result, err)
	}
	other := request
	other.Runner = "replacement"
	result, err = s.NextWork(other)
	if err != nil || result.Action != "runner_offline" {
		t.Fatalf("offline owner was hidden as generic WIP: %+v %v", result, err)
	}
	if _, err = s.ReleaseWork("epic", request.Runner, "handoff"); err == nil {
		t.Fatal("expired writer was silently released")
	}
	if _, err = s.Resume("01-first", "", true); err != nil {
		t.Fatal(err)
	}
	result, err = s.NextWork(request)
	if err != nil || result.Action != "ready" || result.Checkpoint.Task != "01-first" {
		t.Fatalf("recovery skipped leaf: %+v %v", result, err)
	}
}

func TestParallelWorkRequiresExplicitDisjointResources(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	for _, id := range []string{"a", "b", "c"} {
		create(t, s, core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Scope: []string{id + "/file"}, Acceptance: []string{"结果"}})
	}
	first := core.WorkRequest{Parent: "epic", Runner: "first", Parallel: true, Resources: []string{"resource-a"}}
	if got, err := s.ClaimNextWork(first); err != nil || got.Action != "claimed" || got.Checkpoint.Task != "a" {
		t.Fatal(got, err)
	}
	second := core.WorkRequest{Parent: "epic", Runner: "second", Parallel: true, Resources: []string{"resource-b"}}
	if got, err := s.ClaimNextWork(second); err != nil || got.Action != "claimed" || got.Checkpoint.Task != "b" {
		t.Fatal(got, err)
	}
	changed := first
	changed.Resources = []string{"resource-b"}
	if got, err := s.NextWork(changed); err != nil || got.Action != "wait_wip" {
		t.Fatalf("runner changed held resource declaration: %+v %v", got, err)
	}
	conflict := core.WorkRequest{Parent: "epic", Runner: "third", Parallel: true, Resources: []string{"resource-a"}}
	if got, err := s.ClaimNextWork(conflict); err != nil || got.Action != "wait_wip" {
		t.Fatalf("resource conflict accepted: %+v %v", got, err)
	}
}

func TestRunnerHandoffLetsSuccessorClaimSameUnfinishedLeaf(t *testing.T) {
	s, request := workFixture(t)
	claim, err := s.ClaimNextWork(request)
	if err != nil || claim.Action != "claimed" {
		t.Fatal(claim, err)
	}
	a := claim.Next.Attempt
	if _, err = s.Submit(core.Submission{Task: "01-first", Attempt: a.ID, Token: a.Token, Key: "handoff", Result: core.StepResult{Status: "pass", Summary: "defined"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReleaseWork("epic", request.Runner, "handoff after submit"); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.WorkCheckpoint("epic", request.Runner)
	if err != nil || checkpoint.StopReason != "handoff" || checkpoint.Checkpoint.Task != "01-first" {
		t.Fatalf("handoff checkpoint missing: %+v %v", checkpoint, err)
	}
	request.Runner = "successor"
	next, err := s.ClaimNextWork(request)
	if err != nil || next.Action != "claimed" || next.Checkpoint.Task != "01-first" || next.Checkpoint.Step != "execute" {
		t.Fatalf("successor skipped unfinished work: %+v %v", next, err)
	}
}

func TestNextWorkDistinguishesHumanDependencyAndBudgetStops(t *testing.T) {
	t.Run("parent environment", func(t *testing.T) {
		s, request := workFixture(t)
		if _, err := s.Pause("epic", "environment unavailable"); err != nil {
			t.Fatal(err)
		}
		result, err := s.NextWork(request)
		if err != nil || result.Action != "wait_environment" {
			t.Fatalf("parent environment: %+v %v", result, err)
		}
	})
	t.Run("human", func(t *testing.T) {
		s, request := workFixture(t)
		leaf, err := s.Get("01-first")
		if err != nil {
			t.Fatal(err)
		}
		d := leaf.Definition
		d.Scope = append(d.Scope, "expanded")
		if _, err = s.Edit("01-first", d, "scope changed"); err != nil {
			t.Fatal(err)
		}
		result, err := s.NextWork(request)
		if err != nil || result.Action != "wait_human" {
			t.Fatalf("human gate: %+v %v", result, err)
		}
	})
	t.Run("dependency", func(t *testing.T) {
		s := fixture(t)
		create(t, s, core.Definition{ID: "outside", Title: "外部前置", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
		create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
		create(t, s, core.Definition{ID: "leaf", Parent: "epic", Dependencies: []string{"outside"}, Title: "叶", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
		result, err := s.NextWork(core.WorkRequest{Parent: "epic", Runner: "runner"})
		if err != nil || result.Action != "wait_dependency" {
			t.Fatalf("dependency: %+v %v", result, err)
		}
	})
	t.Run("budget", func(t *testing.T) {
		s, request := workFixture(t)
		if _, err := s.SetTimeLimit("01-first", 1, "fixture limit"); err != nil {
			t.Fatal(err)
		}
		claim, err := s.ClaimNextWork(request)
		if err != nil {
			t.Fatal(err)
		}
		a := claim.Next.Attempt
		if _, err = s.Submit(core.Submission{Task: "01-first", Attempt: a.ID, Token: a.Token, Key: "budget", Result: core.StepResult{Status: "pass", Summary: "defined"}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Store.DB.Exec(`UPDATE tasks SET body=json_set(body,'$.attempts[0].started','2000-01-01T00:00:00Z','$.attempts[0].ended','2000-01-01T00:00:02Z') WHERE id='01-first'`); err != nil {
			t.Fatal(err)
		}
		result, err := s.NextWork(request)
		if err != nil || result.Action != "budget_stopped" {
			t.Fatalf("budget: %+v %v", result, err)
		}
	})
}

func TestManualWorkOrderRespectsDependenciesBeforeIDs(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	create(t, s, core.Definition{ID: "z-prerequisite", Parent: "epic", Title: "前置", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	create(t, s, core.Definition{ID: "a-dependent", Parent: "epic", Dependencies: []string{"z-prerequisite"}, Title: "后继", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	result, err := s.NextWork(core.WorkRequest{Parent: "epic", Runner: "runner"})
	if err != nil || result.Action != "ready" || result.Checkpoint.Task != "z-prerequisite" {
		t.Fatalf("dependency order ignored: %+v %v", result, err)
	}
}

func TestLegacyClaimCannotBypassRunnerAssignment(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	for _, id := range []string{"a", "b"} {
		create(t, s, core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	}
	if got, err := s.ClaimNextWork(core.WorkRequest{Parent: "epic", Runner: "runner"}); err != nil || got.Action != "claimed" {
		t.Fatal(got, err)
	}
	for _, executor := range []string{"runner", "other"} {
		if _, err := s.Claim("b", "", executor, "", ""); err == nil || !strings.Contains(err.Error(), "RUNNER_WIP") {
			t.Fatalf("legacy claim bypassed WIP for %s: %v", executor, err)
		}
	}
}

func TestParallelRunnerCannotSkipFailedOrOfflineLeaf(t *testing.T) {
	for _, scenario := range []string{"failed", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			s := fixture(t)
			create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
			for _, id := range []string{"a", "b"} {
				create(t, s, core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Scope: []string{id + "/file"}, Acceptance: []string{"结果"}})
			}
			first := core.WorkRequest{Parent: "epic", Runner: "first", Parallel: true, Resources: []string{"resource-a"}}
			claim, err := s.ClaimNextWork(first)
			if err != nil || claim.Action != "claimed" {
				t.Fatal(claim, err)
			}
			if scenario == "failed" {
				a := claim.Next.Attempt
				if _, err = s.Submit(core.Submission{Task: "a", Attempt: a.ID, Token: a.Token, Key: "failure", Result: core.StepResult{Status: "fail", Summary: "needs repair"}}); err != nil {
					t.Fatal(err)
				}
			} else if _, err = s.Store.DB.Exec(`UPDATE tasks SET body=json_set(body,'$.attempts[0].lease_until','2000-01-01T00:00:00Z') WHERE id='a'`); err != nil {
				t.Fatal(err)
			}
			second := core.WorkRequest{Parent: "epic", Runner: "second", Parallel: true, Resources: []string{"resource-b"}}
			result, err := s.ClaimNextWork(second)
			if err != nil || result.Checkpoint.Task == "b" || (result.Action == "claimed" && result.Checkpoint.Task != "a") {
				t.Fatalf("skipped failed/offline leaf: %+v %v", result, err)
			}
			if scenario == "offline" && result.Action != "runner_offline" {
				t.Fatalf("offline reason lost: %+v", result)
			}
		})
	}
}

func TestAttachedAssessmentUsesAlreadyClaimedAttempt(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	id := atVerify(t, s, core.Definition{ID: "leaf", Title: "模型检查", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "review", Kind: "functional", Method: "model", Covers: []string{"结果"}, Required: true}}})
	if _, err := s.LinkEpic("epic", id, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("isolated assessment"), 0644); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextWork(core.WorkRequest{Parent: "epic", Runner: "reviewer", Capabilities: []string{"model"}})
	if err != nil || claim.Action != "claimed" || claim.Checkpoint.Unit != "review" {
		t.Fatal(claim, err)
	}
	a := claim.Next.Attempt
	review := core.Assessment{Task: id, Check: "review", Candidate: claim.Next.Task.Candidate, Method: "model", Executor: "reviewer", Result: "pass", Reason: "inspected actual output", Inputs: []string{"result.txt"}, Artifacts: []string{"runtime:report.md"}}
	if _, err = s.AssessExisting(review, a.ID, "wrong-token"); err == nil {
		t.Fatal("wrong token accepted")
	}
	next, err := s.AssessExisting(review, a.ID, a.Token)
	if err != nil || next.Action != "completed" {
		t.Fatalf("claimed assessment failed: %+v %v", next, err)
	}
}

func TestLegacyClaimsCannotRunTwoParentLeavesWithoutCoordination(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	for _, id := range []string{"a", "b"} {
		create(t, s, core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	}
	if _, err := s.Claim("a", "", "legacy-runner", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, runner := range []string{"legacy-runner", "another-runner"} {
		if _, err := s.Claim("b", "", runner, "", ""); err == nil || !strings.Contains(err.Error(), "RUNNER_WIP") {
			t.Fatalf("uncoordinated legacy claim accepted: %s %v", runner, err)
		}
	}
}

func TestManagedChildCannotClaimOrSubmitParentWork(t *testing.T) {
	s, request := workFixture(t)
	claim, err := s.ClaimNextWork(request)
	if err != nil || claim.Action != "claimed" {
		t.Fatal(claim, err)
	}
	a := claim.Next.Attempt
	t.Setenv("AEL_MANAGED_CHILD", "1")
	if _, err = s.ClaimNextWork(request); err == nil || !strings.Contains(err.Error(), "MANAGED_CHILD_FORBIDDEN") {
		t.Fatalf("child claimed work: %v", err)
	}
	if _, err = s.Claim("02-second", "", "child", "", ""); err == nil || !strings.Contains(err.Error(), "MANAGED_CHILD_FORBIDDEN") {
		t.Fatalf("child claimed directly: %v", err)
	}
	if _, err = s.Submit(core.Submission{Task: "01-first", Attempt: a.ID, Token: a.Token, Key: "child", Result: core.StepResult{Status: "pass", Summary: "result"}}); err == nil || !strings.Contains(err.Error(), "MANAGED_CHILD_FORBIDDEN") {
		t.Fatalf("child submitted directly: %v", err)
	}
}

func TestLegacyClaimKeepsUnfinishedLeafAssignedAfterSubmit(t *testing.T) {
	for _, status := range []string{"pass", "fail"} {
		t.Run(status, func(t *testing.T) {
			s := fixture(t)
			create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
			for _, id := range []string{"a", "b"} {
				create(t, s, core.Definition{ID: id, Parent: "epic", Title: id, Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
			}
			n, err := s.Claim("a", "", "legacy", "", "")
			if err != nil {
				t.Fatal(err)
			}
			a := n.Attempt
			if _, err = s.Submit(core.Submission{Task: "a", Attempt: a.ID, Token: a.Token, Key: "legacy-" + status, Result: core.StepResult{Status: status, Summary: "fixture"}}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Claim("b", "", "legacy", "", ""); err == nil || !strings.Contains(err.Error(), "RUNNER_WIP") {
				t.Fatalf("unfinished leaf skipped after %s: %v", status, err)
			}
		})
	}
}

func TestSuccessorClaimsSameLeafAfterSubmitWithoutScheduling(t *testing.T) {
	s, request := workFixture(t)
	claim, err := s.ClaimNextWork(request)
	if err != nil || claim.Action != "claimed" {
		t.Fatal(claim, err)
	}
	a := claim.Next.Attempt
	if _, err = s.Submit(core.Submission{Task: "01-first", Attempt: a.ID, Token: a.Token, Key: "handoff-auto", Result: core.StepResult{Status: "pass", Summary: "defined"}}); err != nil {
		t.Fatal(err)
	}
	successor := request
	successor.Runner = "successor"
	next, err := s.ClaimNextWork(successor)
	if err != nil || next.Action != "claimed" || next.Checkpoint.Task != "01-first" || next.Checkpoint.Step != "execute" {
		t.Fatalf("successor did not claim same leaf: %+v %v", next, err)
	}
	checkpoint, err := s.WorkCheckpoint("epic", request.Runner)
	if err != nil || checkpoint.StopReason != "handoff" {
		t.Fatalf("old owner checkpoint not updated: %+v %v", checkpoint, err)
	}
	blocked, err := s.ClaimNextWork(request)
	if err != nil || blocked.Action == "claimed" {
		t.Fatalf("two runners claimed same unit: %+v %v", blocked, err)
	}
}

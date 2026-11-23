package core

import "testing"

func TestQueueStaleFollowsPendingResponsibility(t *testing.T) {
	base := Task{Status: "open", Step: "execute", Definition: Definition{Revision: 2}, Plan: Plan{Revision: 3}}
	record := QueueRecord{Step: "execute", DefinitionRevision: 2, PlanRevision: 3}
	if QueueStale(base, record) != "" {
		t.Fatal("current record reported stale")
	}
	cases := map[string]func(*Task){
		"completed":          func(t *Task) { t.Status = "completed" },
		"cancelled":          func(t *Task) { t.Status = "cancelled" },
		"paused":             func(t *Task) { t.Paused = true },
		"started":            func(t *Task) { t.Attempts = []Attempt{{Status: "running"}} },
		"step_changed":       func(t *Task) { t.Step = "verify" },
		"definition_changed": func(t *Task) { t.Definition.Revision = 4 },
		"plan_changed":       func(t *Task) { t.Plan.Revision = 9 },
	}
	for want, mutate := range cases {
		task := base
		mutate(&task)
		if got := QueueStale(task, record); got != want {
			t.Fatalf("want %s, got %q", want, got)
		}
	}
	verify := base
	verify.Step, verify.Candidate = "verify", "cand-b"
	if QueueStale(verify, QueueRecord{Step: "verify", DefinitionRevision: 2, PlanRevision: 3, Candidate: "cand-a"}) != "candidate_changed" {
		t.Fatal("verify record survived a new candidate")
	}
	// A finished earlier attempt does not count as a current responsibility.
	done := base
	done.Attempts = []Attempt{{Status: "pass"}}
	if QueueStale(done, record) != "" {
		t.Fatal("finished attempt cleared a new queue record")
	}
	if !(QueueRecord{Runner: "a", Actor: "u", Scope: "executor"}).Permits("b", "u") || (QueueRecord{Runner: "a", Actor: "u", Scope: "runner"}).Permits("b", "u") {
		t.Fatal("scope rules wrong")
	}
}

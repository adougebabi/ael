package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowDisplayAndCheckImpact(t *testing.T) {
	s := fixture(t)
	d := bug()
	other := d.Checks[0]
	other.ID = "other"
	d.Checks = append(d.Checks, other)
	id := atVerify(t, s, d)
	n, e := s.Verify(id, "test", "checker")
	if e != nil {
		t.Fatal(e)
	}
	original := n.Task.Candidate
	d = n.Task.Definition
	d.Title = "Display only"
	n, e = s.Edit(id, d, "rename only")
	if e != nil || n.Revision != 1 || n.Task.Candidate != original || n.Task.Confirmation != nil || len(n.Task.Evidence) != 1 {
		t.Fatalf("display invalidation: %+v %v", n, e)
	}
	// Whitespace in policy cannot revoke an approval or acceptance evidence.
	raw, _ := os.ReadFile(filepath.Join(s.Root, ".ael/policy.yaml"))
	os.WriteFile(filepath.Join(s.Root, ".ael/policy.yaml"), append(raw, '\n'), 0644)
	n, e = s.Next(id)
	if e != nil || n.Revision != 1 {
		t.Fatal(n, e)
	}
	d = n.Task.Definition
	d.Checks[1].Command = []string{"sh", "-c", "test -s result.txt"}
	n, e = s.Edit(id, d, "update one checker")
	if e != nil || n.Step != "verify" || n.Task.Confirmation != nil || len(n.Task.Evidence) != 1 || n.Task.Evidence[0].ReusedFrom == "" {
		t.Fatalf("check impact: %+v %v", n, e)
	}
	n, e = s.Verify(id, "other", "checker")
	if e != nil || n.Action != "completed" {
		t.Fatal(n, e)
	}
}

func TestWorkflowInspectionReportAndNoSourceWrites(t *testing.T) {
	s := fixture(t)
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("broken"), 0644)
	d := bug()
	d.Type = "verify"
	d.Intent = "inspect"
	d.Goal = "report"
	d.Checks[0].Command = []string{"sh", "-c", "test \"$(cat result.txt)\" = fixed"}
	id := create(t, s, d)
	step(t, s, id, "inspector")
	n, e := s.Claim(id, "", "inspector", "", "")
	if e != nil {
		t.Fatal(e)
	}
	report := filepath.Join(s.Store.Dir, "report.md")
	os.WriteFile(report, []byte("inspection found broken file; no source change authorized"), 0600)
	sub := core.Submission{Task: id, Attempt: n.Attempt.ID, Token: n.Attempt.Token, Key: "inspect", Result: core.StepResult{Status: "pass", Summary: "inspection report prepared", Artifacts: []string{"runtime:report.md"}}}
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("unauthorized"), 0644)
	if _, e = s.Submit(sub); e == nil || !strings.Contains(e.Error(), "INSPECTION_WRITE_DETECTED") {
		t.Fatal(e)
	}
	sub.Result.Status = "fail"
	if _, e = s.Submit(sub); e == nil || !strings.Contains(e.Error(), "INSPECTION_WRITE_DETECTED") {
		t.Fatal("failed result accepted source mutation", e)
	}
	if _, e = s.Resume(id, "", true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Claim(id, "", "new-inspector", "", ""); e == nil || !strings.Contains(e.Error(), "INSPECTION_WRITE_DETECTED") {
		t.Fatal("retry accepted contaminated baseline", e)
	}
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("broken"), 0644)
	n, e = s.Claim(id, "", "inspector", "", "")
	if e != nil {
		t.Fatal(e)
	}
	sub.Attempt = n.Attempt.ID
	sub.Token = n.Attempt.Token
	sub.Key = "restored-inspection"
	sub.Result.Status = "pass"
	if _, e = s.Submit(sub); e != nil {
		t.Fatal(e)
	}
	n, e = s.Verify(id, "test", "checker")
	if e != nil || n.Action != "completed" || n.Task.Result != "report_delivered_with_findings" || n.Task.Evidence[0].Result != "fail" {
		t.Fatal(n, e)
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, "result.txt"))
	if string(b) != "broken" {
		t.Fatal("inspection mutated source")
	}
}

func TestWorkflowContextAndKnowledgeConvergence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	os.MkdirAll(filepath.Join(root, "docs/spec"), 0755)
	os.WriteFile(filepath.Join(root, "docs/spec/api.md"), []byte("---\nid: api\nkind: spec\nstatus: current\npaths: [result.txt]\n---\nNever invent a passing result.\n"), 0644)
	if _, e := bootstrap.Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	s, _, e := bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Store.Close()
	id := create(t, s, bug())
	step(t, s, id, "agent")
	n, e := s.Claim(id, "", "agent", "", "")
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "result.txt"), []byte("actual output"), 0644)
	sub := core.Submission{Task: id, Attempt: n.Attempt.ID, Token: n.Attempt.Token, Key: "work", Result: core.StepResult{Status: "pass", Summary: "actual work", Artifacts: []string{"result.txt"}}}
	if _, e = s.Submit(sub); e == nil || !strings.Contains(e.Error(), "TASK_CONTEXT_REQUIRED") {
		t.Fatal(e)
	}
	if _, e = knowledge.Context(s, id, "", 100); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Submit(sub); e == nil || !strings.Contains(e.Error(), "KNOWLEDGE_OUTCOME_REQUIRED") {
		t.Fatal(e)
	}
	sub.Result.Knowledge = &core.KnowledgeOutcome{Status: "unchanged", Reason: "bug fix restores existing API; contract unchanged"}
	n, e = s.Submit(sub)
	if e != nil || len(n.Task.Basis) != 1 || n.Task.Basis[0].ID != "api" {
		t.Fatal(n, e)
	}
	n, e = s.Verify(id, "test", "checker")
	if e != nil || n.Action != "completed" {
		t.Fatal(n, e)
	}
}

func TestWorkflowModelAssessmentAndCapabilityBlock(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Checks = []core.Check{{ID: "semantic", Kind: "review", Method: "model", Required: true, Covers: d.Acceptance}}
	id := atVerify(t, s, d)
	n, _ := s.Next(id)
	r := core.Assessment{Task: id, Check: "semantic", Candidate: n.Task.Candidate, Method: "model", Executor: "model-test-fixture", Result: "pass", Reason: "fixture semantic judgement; not a real model"}
	if _, e := s.Assess(r); e == nil {
		t.Fatal("self assertion accepted")
	}
	os.WriteFile(filepath.Join(s.Store.Dir, "review.md"), []byte("fixture model review: read result.txt, output exists"), 0600)
	r.Inputs = []string{"result.txt"}
	r.Artifacts = []string{"runtime:review.md"}
	n, e := s.Assess(r)
	if e != nil || n.Action != "completed" || n.Task.Evidence[0].Source != "model:model-test-fixture" {
		t.Fatal(n, e)
	}
	id = atVerify(t, s, d)
	n, _ = s.Next(id)
	r.Task = id
	r.Candidate = n.Task.Candidate
	r.Result = "blocked"
	r.Reason = "browser missing; restore browser and resume semantic"
	r.Inputs = nil
	r.Artifacts = nil
	n, e = s.Assess(r)
	if e != nil || n.Action != "wait_environment" || n.Task.Blocker == nil {
		t.Fatal(n, e)
	}
}

func TestWorkflowRepairPersistsAndProgressIsNotDefinition(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Checks[0].Command = []string{"sh", "-c", "exit 1"}
	id := atVerify(t, s, d)
	if _, e := s.Verify(id, "test", "checker"); e != nil {
		t.Fatal(e)
	}
	n, e := s.Next(id)
	if e != nil || n.Action != "repair" {
		t.Fatal(n, e)
	}
	n, e = s.Claim(id, "", "next-agent", "", "")
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.Progress(id, n.Attempt.ID, n.Attempt.Token, core.Progress{Summary: "read actual failure log", Next: "repair result.txt"})
	if e != nil || n.Revision != 1 || n.Step != "execute" {
		t.Fatal(n, e)
	}
	b, _ := json.Marshal(n.Task)
	if strings.Contains(string(b), "definition changed") {
		t.Fatal("progress revised definition")
	}
}

func TestWorkflowLatestReportEvidenceAndPeerFencing(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.Intent = "inspect"
	d.Goal = "report"
	d.Checks = []core.Check{{ID: "a", Kind: "review", Method: "model", Required: true, Covers: d.Acceptance}, {ID: "b", Kind: "review", Method: "model", Required: true, Covers: d.Acceptance}}
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("read-only input"), 0644)
	id := create(t, s, d)
	step(t, s, id, "inspector")
	os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("fixture, not a real model review"), 0600)
	step(t, s, id, "inspector", "runtime:report.md")
	n, _ := s.Next(id)
	r := core.Assessment{Task: id, Candidate: n.Task.Candidate, Method: "model", Executor: "model-fixture", Reason: "fixture finding", Inputs: []string{"result.txt"}, Artifacts: []string{"runtime:report.md"}}
	for _, pair := range [][2]string{{"a", "pass"}, {"a", "fail"}, {"b", "pass"}} {
		r.Check = pair[0]
		r.Result = pair[1]
		var e error
		n, e = s.Assess(r)
		if e != nil {
			t.Fatal(e)
		}
	}
	if n.Task.Result != "report_delivered_with_findings" {
		t.Fatal(n.Task.Result)
	}
	// Verified changes route premise failures back; a peer must not finish a new responsibility.
	d.Intent = ""
	d.Goal = "verified"
	id = create(t, s, d)
	step(t, s, id, "agent")
	step(t, s, id, "agent", "result.txt")
	a, e := s.Claim(id, "a", "first-reviewer", "", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Claim(id, "b", "peer-reviewer", "", "")
	if e != nil {
		t.Fatal(e)
	}
	r.Task = id
	r.Candidate = a.Task.Candidate
	r.Check = "a"
	r.Result = "fail"
	r.ReturnTo = "define"
	n, e = s.AssessClaim(r, a.Attempt)
	if e != nil || n.Step != "define" || !n.Task.Paused {
		t.Fatal(n, e)
	}
	r.Check = "b"
	r.Result = "pass"
	r.ReturnTo = ""
	if _, e = s.AssessClaim(r, b.Attempt); e == nil {
		t.Fatal("stale peer advanced define")
	}
	for _, attempt := range n.Task.Attempts {
		if attempt.ID == b.Attempt.ID && attempt.Status != "interrupted" {
			t.Fatal("peer not fenced")
		}
	}
}

func TestWorkflowLargeBasisQueryAndDeletedHistory(t *testing.T) {
	s := fixture(t)
	dir := filepath.Join(s.Root, "docs/spec")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "large.md")
	os.WriteFile(path, []byte("---\nid: large\nkind: spec\nstatus: current\npaths: [result.txt]\n---\n"+strings.Repeat("contract ", 2000)), 0644)
	os.WriteFile(filepath.Join(dir, "outside.md"), []byte("---\nid: outside\nkind: spec\nstatus: current\npaths: [unrelated]\n---\nquery-only-keyword"), 0644)
	if _, e := knowledge.Index(s.Root); e != nil {
		t.Fatal(e)
	}
	d := bug()
	d.Scope = []string{"result.txt"}
	id := create(t, s, d)
	step(t, s, id, "agent")
	n, e := s.Claim(id, "", "agent", "", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = knowledge.Context(s, id, "query-only-keyword", 64); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("actual output"), 0644)
	_, e = s.Submit(core.Submission{Task: id, Attempt: n.Attempt.ID, Token: n.Attempt.Token, Key: "large-basis", Result: core.StepResult{Status: "pass", Summary: "loaded scoped constraint despite reference budget", Artifacts: []string{"result.txt"}, Knowledge: &core.KnowledgeOutcome{Status: "unchanged", Reason: "existing contract restored"}}})
	if e != nil {
		t.Fatal(e)
	}
	os.Remove(path)
	knowledge.Index(s.Root)
	view, e := knowledge.TaskView(s, id)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(view)
	if !strings.Contains(string(b), "contract contract") {
		t.Fatal("deleted provided source lost its snapshot")
	}
}

func TestProjectToolUpdateFencesNewWriter(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	before, e := core.Fingerprint(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	lock := filepath.Join(s.Root, ".ael/tool-update.lock")
	os.Mkdir(lock, 0700)
	os.WriteFile(filepath.Join(lock, "owner.json"), []byte(`{"pid":123}`), 0600)
	after, e := core.Fingerprint(s.Root)
	if e != nil || before != after {
		t.Fatal("maintenance marker became a source candidate input", e)
	}
	if _, e = s.Claim(id, "", "new-writer", "", ""); e == nil || !strings.Contains(e.Error(), "PROJECT_UPDATING") {
		t.Fatal(e)
	}
	os.Remove(filepath.Join(lock, "owner.json"))
	os.Remove(lock)
	if _, e = s.Claim(id, "", "writer", "", ""); e != nil {
		t.Fatal(e)
	}
}

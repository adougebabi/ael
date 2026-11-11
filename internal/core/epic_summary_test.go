package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEpicTreeReportsDistinctLeafProgress(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	create(t, s, core.Definition{ID: "waiting", Parent: "epic", Title: "无人接手", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	check := func(id string) core.Check {
		return core.Check{ID: id, Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"结果"}, Required: true}
	}
	atVerify(t, s, core.Definition{ID: "implemented", Parent: "epic", Title: "实施未验收", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, Checks: []core.Check{check("one")}})
	if _, err := s.ReleaseWork("epic", "A", "fixture handoff"); err != nil {
		t.Fatal(err)
	}
	partial := atVerify(t, s, core.Definition{ID: "partial", Parent: "epic", Title: "部分通过", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, Checks: []core.Check{check("one"), check("two")}})
	if _, err := s.Verify(partial, "one", "checker"); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree("epic")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(tree)
	var decoded map[string]any
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	summary, ok := decoded["summary"].(map[string]any)
	if !ok {
		t.Fatalf("shared epic summary missing: %s", data)
	}
	for key, want := range map[string]float64{"total": 3, "implemented": 2, "accepted": 0, "partial_checks": 1, "not_started": 1} {
		if summary[key] != want {
			t.Fatalf("%s = %v, want %v: %s", key, summary[key], want, data)
		}
	}
	parent, err := s.Summarize(tree.Root)
	if err != nil || parent.Epic == nil || !reflect.DeepEqual(*parent.Epic, tree.Summary) {
		t.Fatalf("parent and tree summaries disagree: %+v %+v %v", parent.Epic, tree.Summary, err)
	}
	if _, err = s.Verify(partial, "two", "checker"); err != nil {
		t.Fatal(err)
	}
	tree, err = s.Tree("epic")
	if err != nil || tree.Summary.Accepted != 1 || tree.Summary.PartialChecks != 0 || tree.RequiredCompleted != 1 {
		t.Fatalf("parent summary did not update after leaf acceptance: %+v %v", tree.Summary, err)
	}
}

func TestEpicSummaryLinksFailedCheckToEvidence(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	leaf := atVerify(t, s, core.Definition{ID: "broken", Parent: "epic", Title: "失败项", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "exit 1"}, Covers: []string{"结果"}, Required: true}}})
	if _, err := s.Verify(leaf, "proof", "checker"); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree("epic")
	if err != nil || tree.Summary.Failed != 1 || len(tree.Summary.Issues) != 1 || tree.Summary.Issues[0].Task != leaf || tree.Summary.Issues[0].Check != "proof" {
		t.Fatalf("failed evidence is not reachable from epic summary: %+v %v", tree.Summary, err)
	}
}

func TestEpicSummarySeparatesAcceptanceFromDeliveryReceipt(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	d := bug()
	d.ID, d.Parent, d.Goal, d.Environment = "leaf", "epic", "released", "isolated-test"
	id := atVerify(t, s, d)
	n, err := s.Verify(id, "test", "checker")
	if err != nil || n.Task.Confirmation == nil {
		t.Fatalf("acceptance did not reach delivery confirmation: %+v %v", n, err)
	}
	tree, err := s.Tree("epic")
	if err != nil || tree.Summary.Implemented != 1 || tree.Summary.Accepted != 1 || tree.Summary.Delivered != 0 {
		t.Fatalf("acceptance incorrectly implied delivery: %+v %v", tree.Summary, err)
	}
	c := n.Task.Confirmation
	if _, err = s.Confirm(id, c.Kind, "approve", "fixture-owner", "synthetic receipt only", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatal(err)
	}
	n, err = s.Claim(id, "", "fixture-delivery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	receipt := core.Delivery{RunID: "fixture-run", Key: "fixture-key", Candidate: n.Task.Candidate, Artifact: "runtime:receipt.json", Environment: d.Environment, Status: "success"}
	raw, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "receipt.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: n.Attempt.ID, Token: n.Attempt.Token, Key: "fixture-delivery", Result: core.StepResult{Status: "pass", Summary: "synthetic fixture receipt", Delivery: &receipt}}); err != nil {
		t.Fatal(err)
	}
	tree, err = s.Tree("epic")
	if err != nil || tree.Summary.Accepted != 1 || tree.Summary.Delivered != 1 || tree.RequiredCompleted != 1 || tree.Root.Task.Status != "open" {
		t.Fatalf("delivery or parent integration misreported: %+v %v", tree.Summary, err)
	}
}

func TestEpicSummaryReportsCurrentReportFindings(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	leaf := atVerify(t, s, core.Definition{ID: "report", Parent: "epic", Title: "验收报告", Type: "qa", Goal: "report", Intent: "change", Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "proof", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "exit 1"}, Covers: []string{"结果"}, Required: true}}})
	result, err := s.Verify(leaf, "proof", "checker")
	if err != nil || result.Task.Status != "completed" || result.Task.Result != "report_delivered_with_findings" {
		t.Fatalf("report fixture: %+v %v", result, err)
	}
	tree, err := s.Tree("epic")
	if err != nil || tree.Summary.Failed != 1 || len(tree.Summary.Issues) != 1 || tree.Summary.Issues[0].Check != "proof" || tree.Summary.Accepted != 0 {
		t.Fatalf("report finding omitted from epic: %+v %v", tree.Summary, err)
	}
}

func TestEpicSummaryDropsSupersededReportFailure(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	marker := filepath.Join(s.Store.Dir, "allow-check")
	checks := []core.Check{
		{ID: "first", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f '" + marker + "'"}, Covers: []string{"结果"}, Required: true},
		{ID: "second", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"结果"}, Required: true},
	}
	leaf := atVerify(t, s, core.Definition{ID: "report", Parent: "epic", Title: "验收报告", Type: "qa", Goal: "report", Intent: "change", Acceptance: []string{"结果"}, Checks: checks})
	if _, err := s.Verify(leaf, "first", "checker"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(leaf, "first", "checker"); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree("epic")
	if err != nil || tree.Summary.Failed != 0 || len(tree.Summary.Issues) != 0 || tree.Summary.PartialChecks != 1 {
		t.Fatalf("superseded failure reported as current: %+v %v", tree.Summary, err)
	}
}

func TestEpicSummaryRetainsCancelledScopeWhenLinkedLater(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	create(t, s, core.Definition{ID: "leaf", Title: "旧叶子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	if _, err := s.Cancel("leaf", "已有取消记录"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkEpic("epic", "leaf", true, ""); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree("epic")
	if err != nil || len(tree.ScopeChanges) != 1 || tree.ScopeChanges[0].Kind != "added" || len(tree.Summary.Issues) != 1 || tree.Summary.Issues[0].Task != "leaf" {
		t.Fatalf("cancelled scope misreported: scope=%+v summary=%+v err=%v", tree.ScopeChanges, tree.Summary, err)
	}
}

func TestEpicTreePreservesScopeChangesAndRealProgress(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}})
	create(t, s, core.Definition{ID: "first", Parent: "epic", Title: "首项", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	claim, err := s.Claim("first", "", "worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "fact.txt"), []byte("fact"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Progress("first", claim.Attempt.ID, claim.Attempt.Token, core.Progress{Summary: "核实了事实", Next: "完成定义", Artifacts: []string{"fact.txt"}, EvidenceKind: "deliverable"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: "first", Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("scope-test-"), Result: core.StepResult{Status: "pass", Summary: "定义完成"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Cancel("first", "范围由人调整"); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Tree("epic")
	if err != nil || tree.Required != 1 {
		t.Fatalf("cancelled required leaf must remain in denominator: %+v %v", tree, err)
	}
	create(t, s, core.Definition{ID: "second", Parent: "epic", Title: "新增项", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}})
	tree, err = s.Tree("epic")
	if err != nil || tree.Required != 2 {
		t.Fatalf("new required leaf must increase denominator: %+v %v", tree, err)
	}
	raw, _ := json.Marshal(tree)
	var projection struct {
		Scope []struct {
			Task   string `json:"task"`
			Kind   string `json:"kind"`
			Delta  int    `json:"required_delta"`
			Reason string `json:"reason"`
		} `json:"scope_changes"`
		Progress []struct {
			Task      string   `json:"task"`
			Summary   string   `json:"summary"`
			Next      string   `json:"next"`
			Artifacts []string `json:"artifacts"`
		} `json:"progress"`
	}
	if err = json.Unmarshal(raw, &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.Scope) != 3 || projection.Scope[0].Task != "first" || projection.Scope[0].Delta != 1 || projection.Scope[1].Kind != "cancelled" || projection.Scope[1].Delta != 0 || projection.Scope[1].Reason != "范围由人调整" || projection.Scope[2].Task != "second" || projection.Scope[2].Delta != 1 {
		t.Fatalf("scope changes missing or misrepresented: %+v", projection.Scope)
	}
	found := false
	for _, event := range projection.Progress {
		if event.Task == "first" && event.Summary == "核实了事实" && event.Next == "完成定义" && len(event.Artifacts) == 1 && event.Artifacts[0] == "fact.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("progress facts, artifact, and next action missing: %+v", projection.Progress)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err = s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	clone := copyCheckout(t, s.Root)
	restored, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Store.Close()
	if _, err = restored.Restore(backup); err != nil {
		t.Fatal(err)
	}
	tree, err = restored.Tree("epic")
	if err != nil || len(tree.Progress) == 0 {
		t.Fatalf("restored progress missing: %+v %v", tree.Progress, err)
	}
	for _, event := range tree.Progress {
		if !event.Historical {
			t.Fatalf("pre-restore event presented as current: %+v", event)
		}
	}
}

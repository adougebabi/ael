package core_test

import (
	"ael/internal/core"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func integrationImpactFixture(t *testing.T, s *core.Service) *core.IntegrationImpact {
	t.Helper()
	const ref = ".ael/integration-evidence/impact.md"
	path := filepath.Join(s.Root, ref)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("隔离夹具：仅声明的叶子需要受影响复验。"), 0600); err != nil {
		t.Fatal(err)
	}
	return &core.IntegrationImpact{Complete: true, Evidence: ref}
}

func completeIntegrationLeaf(t *testing.T, s *core.Service, parent, id, filename string) core.Task {
	t.Helper()
	create(t, s, core.Definition{ID: id, Parent: parent, Title: id, Type: "bug", Goal: "verified", Scope: []string{filename}, Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s " + filename}, Covers: []string{"结果存在"}, Required: true}}})
	step(t, s, id, "fixture-"+id)
	claim, err := s.Claim(id, "", "fixture-"+id, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, filename), []byte("isolated output "+id), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("integration-fixture-"), Result: core.StepResult{Status: "pass", Summary: "真实隔离产物", Artifacts: []string{filename}}})
	if err != nil || n.Step != "verify" {
		t.Fatalf("leaf execute: %+v %v", n, err)
	}
	n, err = s.Verify(id, "unit", "checker-"+id)
	if err != nil || n.Task.Status != "completed" {
		t.Fatalf("leaf unit check: %+v %v", n, err)
	}
	return *n.Task
}

func TestEpicRejectsMissingLeafContentAndStaleCandidate(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "epic", Title: "集成候选", Type: "epic", Goal: "verified", Acceptance: []string{"集成通过"}, Checks: []core.Check{{ID: "integration", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"集成通过"}, Required: true}}})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	file := filepath.Join(s.Root, "leaf.txt")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") {
		t.Fatalf("missing leaf content accepted: %v", err)
	}
	if err = os.WriteFile(file, []byte("wrong content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") {
		t.Fatalf("changed leaf content accepted: %v", err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareEpicIntegration(parent)
	if err != nil || prepared.Task.Candidate == "" {
		t.Fatalf("valid parent candidate rejected: %+v %v", prepared, err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "new-change.txt"), []byte("candidate changed"), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := s.Next(parent)
	if err != nil || n.Action != "wait_integration" {
		t.Fatalf("changed parent candidate remained runnable: %+v %v", n, err)
	}
	if _, err = s.Verify(parent, "integration", "checker"); err == nil {
		t.Fatal("verified stale parent candidate")
	}
}

func TestEpicRejectsExcludedArtifactAsInclusionProof(t *testing.T) {
	s := fixture(t)
	policyPath := filepath.Join(s.Root, ".ael", "policy.yaml")
	raw, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	policy["candidate_exclude"] = []string{"excluded.txt"}
	raw, _ = json.Marshal(policy)
	if err = os.WriteFile(policyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	parent := create(t, s, core.Definition{ID: "epic", Title: "排除内容", Type: "epic", Goal: "verified", Acceptance: []string{"集成通过"}, Checks: []core.Check{{ID: "integration", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"集成通过"}, Required: true}}})
	completeIntegrationLeaf(t, s, parent, "leaf", "excluded.txt")
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_ARTIFACT_NOT_BOUND") {
		t.Fatalf("excluded artifact falsely proved inclusion: %v", err)
	}
}

func TestEpicReleasedRequiresReceiptAndReportRetainsFindings(t *testing.T) {
	s := fixture(t)
	parent := create(t, s, core.Definition{ID: "release", Title: "隔离发布目标", Type: "epic", Goal: "released", Environment: "isolated-test", Acceptance: []string{"集成通过"}, Checks: []core.Check{{ID: "integration", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"集成通过"}, Required: true}}})
	completeIntegrationLeaf(t, s, parent, "release-leaf", "release-leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	n, err := s.Verify(parent, "integration", "checker")
	if err != nil || n.Task.Status != "open" || n.Step != "delivery" || n.Task.Confirmation == nil {
		t.Fatalf("released parent bypassed delivery: %+v %v", n, err)
	}
	c := n.Task.Confirmation
	if _, err = s.Confirm(parent, c.Kind, "approve", "fixture-owner", "synthetic receipt only", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(parent, "", "fixture-delivery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	sub := core.Submission{Task: parent, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: "synthetic-delivery", Result: core.StepResult{Status: "pass", Summary: "合成回执"}}
	if _, err = s.Submit(sub); err == nil || !strings.Contains(err.Error(), "DELIVERY_RECEIPT_REQUIRED") {
		t.Fatalf("released parent accepted no receipt: %v", err)
	}
	receipt := core.Delivery{RunID: "fixture-run", Key: "fixture-key", Candidate: claim.Task.Candidate, Artifact: "runtime:receipt.json", Environment: "isolated-test", Status: "success"}
	raw, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "receipt.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	sub.Result.Delivery = &receipt
	done, err := s.Submit(sub)
	if err != nil || done.Task.Status != "completed" || done.Task.Result != "released" {
		t.Fatalf("valid isolated receipt did not close parent: %+v %v", done, err)
	}

	report := create(t, s, core.Definition{ID: "report", Title: "隔离报告", Type: "epic", Goal: "report", Acceptance: []string{"报告检查"}, Checks: []core.Check{{ID: "finding", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "exit 1"}, Covers: []string{"报告检查"}, Required: true}}})
	completeIntegrationLeaf(t, s, report, "report-leaf", "report-leaf.txt")
	if _, err = s.PrepareEpicIntegration(report); err != nil {
		t.Fatal(err)
	}
	findings, err := s.Verify(report, "finding", "checker")
	if err != nil || findings.Task.Status != "completed" || findings.Task.Result != "report_delivered_with_findings" || len(findings.Task.Evidence) == 0 || findings.Task.Evidence[len(findings.Task.Evidence)-1].Result != "fail" {
		t.Fatalf("report findings were confused with a passing product check: %+v %v", findings.Task, err)
	}
}

func TestEpicIntegrationNeedsRealCrossModuleCheck(t *testing.T) {
	s := fixture(t)
	impact := integrationImpactFixture(t, s)
	parent := create(t, s, core.Definition{ID: "epic", Title: "集成夹具", Type: "epic", Goal: "verified", IntegrationImpact: impact, Acceptance: []string{"模块可以协同"}, Checks: []core.Check{
		{ID: "integration", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f bridge.txt"}, Covers: []string{"模块可以协同"}, Affected: []string{"leaf-2"}, Required: true},
		{ID: "recheck-leaf-2", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s leaf-2.txt"}, Covers: []string{"模块可以协同"}, Affected: []string{"leaf-2"}, Required: true},
	}})
	before := map[string]core.Task{}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("leaf-%d", i)
		before[id] = completeIntegrationLeaf(t, s, parent, id, id+".txt")
	}
	tree, err := s.Tree(parent)
	if err != nil || tree.RequiredCompleted != 3 || tree.Root.Task.Status != "open" || tree.Root.Action != "wait_integration" {
		t.Fatalf("leaf completion incorrectly closed parent: %+v %v", tree.Root, err)
	}
	start := func() core.Next {
		t.Helper()
		result, err := s.PrepareEpicIntegration(parent)
		if err != nil {
			t.Fatalf("prepare integration: %v", err)
		}
		return result
	}
	prepared := start()
	if prepared.Step != "verify" || prepared.Task.Candidate == "" {
		t.Fatalf("no final candidate for integration checks: %+v", prepared)
	}
	preparedTree, err := s.Tree(parent)
	if err != nil || preparedTree.Summary.Integrated != 0 {
		t.Fatalf("unverified parent claimed integrated leaves: %+v %v", preparedTree.Summary, err)
	}
	failed, err := s.Verify(parent, "integration", "integrator")
	if err != nil || failed.Task.Status != "open" || len(failed.Task.Evidence) == 0 || failed.Task.Evidence[len(failed.Task.Evidence)-1].Result != "fail" || len(failed.Task.Definition.Checks[0].Affected) != 1 || failed.Task.Definition.Checks[0].Affected[0] != "leaf-2" {
		t.Fatalf("cross-module failure was not retained: %+v %v", failed, err)
	}
	completeIntegrationLeaf(t, s, parent, "bridge", "bridge.txt")
	prepared = start()
	if prepared.Task.Status != "open" {
		t.Fatal("parent closed before integration recheck")
	}
	n, err := s.Verify(parent, "integration", "integrator")
	if err != nil || n.Task.Status != "open" {
		t.Fatalf("affected recheck was skipped: %+v %v", n, err)
	}
	done, err := s.Verify(parent, "recheck-leaf-2", "integrator")
	if err != nil || done.Task.Status != "completed" || done.Task.Result != "verified" {
		t.Fatalf("verified parent did not finish after real integration check: %+v %v", done, err)
	}
	doneTree, err := s.Tree(parent)
	if err != nil || doneTree.Summary.Integrated != 4 {
		t.Fatalf("verified parent did not report integrated leaves: %+v %v", doneTree.Summary, err)
	}
	for id, original := range before {
		after, err := s.Get(id)
		if err != nil || after.Completed == nil || !after.Completed.Equal(*original.Completed) || after.Candidate != original.Candidate || len(after.Attempts) != len(original.Attempts) || !reflect.DeepEqual(after.Evidence, original.Evidence) {
			t.Fatalf("unaffected leaf %s was rewritten: %+v %v", id, after, err)
		}
	}
	completed := *done.Task.Completed
	feedback := create(t, s, core.Definition{ID: "feedback", Title: "后续反馈", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, FeedbackOf: parent})
	old, err := s.Get(parent)
	if err != nil || old.Completed == nil || !old.Completed.Equal(completed) || old.Status != "completed" || !reflect.DeepEqual(old.Feedback, []string{feedback}) {
		t.Fatalf("feedback rewrote parent completion: %+v %v", old, err)
	}
}

func TestLeafCandidateProofRecordsAddedChangedAndDeletedContent(t *testing.T) {
	s := fixture(t)
	for name, body := range map[string]string{"changed.txt": "before", "deleted.txt": "remove me"} {
		if err := os.WriteFile(filepath.Join(s.Root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	id := create(t, s, core.Definition{ID: "leaf-proof", Title: "候选内容", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f added.txt"}, Covers: []string{"结果"}, Required: true}}})
	step(t, s, id, "fixture")
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "changed.txt"), []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(s.Root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "added.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("proof-"), Result: core.StepResult{Status: "pass", Summary: "真实隔离变更", Artifacts: []string{"added.txt"}}})
	if err != nil || n.Task.CandidateProof == nil || n.Task.CandidateProof.Input != claim.Attempt.Input || n.Task.CandidateProof.Candidate != n.Task.Candidate {
		t.Fatalf("candidate proof missing: %+v %v", n.Task.CandidateProof, err)
	}
	changes := map[string]core.CandidateChange{}
	for _, change := range n.Task.CandidateProof.Changes {
		changes[change.Path] = change
	}
	if len(changes) != 3 || changes["added.txt"].Before != nil || changes["added.txt"].After == nil || changes["deleted.txt"].Before == nil || changes["deleted.txt"].After != nil || changes["changed.txt"].Before == nil || changes["changed.txt"].After == nil || changes["changed.txt"].Before.Hash == changes["changed.txt"].After.Hash {
		t.Fatalf("actual file changes not retained: %+v", changes)
	}
}

func TestLeafCandidateProofAccumulatesAcrossRepair(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "repair", Title: "隔离修复", Type: "bug", Goal: "verified", Acceptance: []string{"两个产物"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f first.txt && test -f second.txt"}, Covers: []string{"两个产物"}, Required: true}}})
	step(t, s, id, "fixture")
	for _, name := range []string{"first.txt", "second.txt"} {
		claim, err := s.Claim(id, "", "fixture", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.Root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("repair-proof-"), Result: core.StepResult{Status: "pass", Summary: "隔离产物", Artifacts: []string{name}}}); err != nil {
			t.Fatal(err)
		}
		result, err := s.Verify(id, "unit", "checker")
		if err != nil {
			t.Fatal(err)
		}
		if name == "first.txt" && result.Action != "repair" {
			t.Fatalf("fixture did not fail first check: %+v", result)
		}
		if name == "second.txt" {
			if result.Task.Status != "completed" || result.Task.CandidateProof == nil {
				t.Fatalf("repaired candidate proof missing: %+v", result.Task)
			}
			paths := map[string]bool{}
			for _, change := range result.Task.CandidateProof.Changes {
				paths[change.Path] = true
			}
			if !paths["first.txt"] || !paths["second.txt"] {
				t.Fatalf("repair dropped an earlier change: %+v", result.Task.CandidateProof.Changes)
			}
		}
	}
}

package core

import (
	"ael/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanRegistrationRejectsMemberAddedAfterPreview(t *testing.T) {
	root, runtime := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".ael"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ael", "policy.yaml"), []byte(`{"schema":1,"max_retries":3,"max_seconds":0,"lease_seconds":1800,"checks":[],"critical_knowledge":[],"delivery_requires_confirmation":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Service{Store: db, Root: root, Active: true, RemoteAuthority: true}
	parent := Definition{ID: "parent", Title: "Parent", Type: "epic", Goal: "verified", Acceptance: []string{"overall"}}
	if _, err = s.Create(parent); err != nil {
		t.Fatal(err)
	}
	input := PlanInput{Request: "one isolated result"}
	prepared, err := s.PreparePlan("parent", input)
	if err != nil {
		t.Fatal(err)
	}
	leaf := PlanLeaf{Key: "one", Definition: Definition{Title: "One", Type: "bug", Goal: "verified", Scope: []string{"one.txt"}, NonGoals: []string{"no release"}, Acceptance: []string{"result"}, Checks: []Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"result"}, Required: true}}}, Deliverables: []string{"one.txt"}, Basis: []string{"fixture"}, Capabilities: []string{"development"}, Order: 1, Required: true}
	p := PlanProposal{Version: 1, ID: "plan", Parent: "parent", ParentRevision: 1, InputHash: prepared.InputHash, Request: input.Request, Leaves: []PlanLeaf{leaf}, Coverage: []PlanCoverage{{Acceptance: "overall", Leaves: []string{"one"}, Integration: "check integration"}}}
	preview, err := s.PreviewPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(Definition{ID: "late", Parent: "parent", Title: "Late", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}); err != nil {
		t.Fatal(err)
	}
	j := planJournal{Proposal: p, ProposalHash: preview.ProposalHash, Digest: preview.Digest, PolicyHash: preview.parent.PolicyHash, ParentHash: preview.parent.DefinitionHash, ParentStateHash: taskStateHash(preview.parent), TaskIDs: preview.ids, Status: "applying"}
	if _, err = s.registerPlan(planJournalKey(p.Parent, p.ID), j, preview); err == nil || !strings.Contains(err.Error(), "PLAN_PARENT_CHANGED") {
		t.Fatalf("registration ignored a newly linked member: %v", err)
	}
}

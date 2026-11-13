package core_test

import (
	"ael/internal/core"
	"ael/internal/store"
	"strings"
	"testing"
)

func TestRemoteIntegrationPreservesAcceptedOutputContent(t *testing.T) {
	s := fixture(t)
	parentID := create(t, s, core.Definition{ID: "epic", Title: "Remote output guards", Type: "epic", Goal: "verified", Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "parent-unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s leaf.txt"}, Covers: []string{"结果存在"}, Required: true, Affected: []string{"leaf"}}}})
	leaf := completeIntegrationLeaf(t, s, parentID, "leaf", "leaf.txt")
	parent, err := s.Get(parentID)
	if err != nil {
		t.Fatal(err)
	}
	s.RemoteAuthority = true
	// This domain test supplies an already validated transport snapshot. The
	// server callback has separate inventory and workspace guard tests.
	s.ValidateRemoteIntegration = func(*store.Tx, *core.Task) error { return nil }
	accepted := leaf.CandidateProof.Files["leaf.txt"]
	snapshot := core.RemoteIntegrationSnapshot{Candidate: "parent-candidate", Repository: "repository", Project: "project", Workspace: "remote:runner/workspace", Files: map[string]core.CandidateFile{"leaf.txt": accepted}, LeafRepositories: map[string]string{"leaf": "repository"}, LeafOutputs: map[string]map[string]core.RemoteIntegrationOutput{"leaf": {"source-output": {Files: map[string]core.CandidateFile{"leaf.txt": accepted}}}}}
	for _, test := range []struct {
		name string
		file core.CandidateFile
	}{
		{"replaced bytes", core.CandidateFile{Hash: "other-content", Mode: accepted.Mode}},
		{"changed execution bit", core.CandidateFile{Hash: accepted.Hash, Mode: accepted.Mode ^ 0111}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot.Files["leaf.txt"] = test.file
			err := s.Store.Write(func(tx *store.Tx) error {
				_, err := s.PrepareRemoteEpicIntegrationTx(tx, parentID, core.RemoteRevision(parent), snapshot)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") {
				t.Fatalf("changed output accepted: %v", err)
			}
		})
	}
	snapshot.Files["leaf.txt"] = accepted
	err = s.Store.Write(func(tx *store.Tx) error {
		prepared, err := s.PrepareRemoteEpicIntegrationTx(tx, parentID, core.RemoteRevision(parent), snapshot)
		if err == nil && (!prepared.Integration.ImpactUnknown || len(prepared.Integration.Leaves[0].Rechecks) != 1 || prepared.Integration.Leaves[0].GitAncestryVerified) {
			t.Fatalf("incorrect remote proof: %+v", prepared.Integration)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

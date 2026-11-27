package core_test

import (
	"ael/internal/core"
	"ael/internal/store"
	"errors"
	"os"
	"path/filepath"
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

func TestRemoteIntegrationAcceptsLeafResultSupersededByLaterLeaf(t *testing.T) {
	s := fixture(t)
	parentID := create(t, s, core.Definition{ID: "epic", Title: "Remote rewrite", Type: "epic", Goal: "verified", Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "parent-unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"结果存在"}, Required: true, Affected: []string{"a", "b"}}}})
	for _, id := range []string{"a", "b"} {
		create(t, s, core.Definition{ID: id, Parent: parentID, Title: id, Type: "bug", Goal: "verified", Scope: []string{"shared.txt"}, Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"结果存在"}, Required: true}}})
		step(t, s, id, "fixture-"+id)
		claim, err := s.Claim(id, "", "fixture-"+id, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("remote-rewrite-"), Result: core.StepResult{Status: "pass", Summary: "rewrite", Artifacts: []string{"shared.txt"}}}); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Verify(id, "unit", "checker-"+id); err != nil || n.Task.Status != "completed" {
			t.Fatalf("leaf %s: %+v %v", id, n, err)
		}
	}
	parent, err := s.Get(parentID)
	if err != nil {
		t.Fatal(err)
	}
	s.RemoteAuthority = true
	s.ValidateRemoteIntegration = func(*store.Tx, *core.Task) error { return nil }
	base := core.CandidateFile{Hash: "base", Mode: 0644}
	aOut := core.CandidateFile{Hash: "a-result", Mode: 0644}
	bOut := core.CandidateFile{Hash: "b-result", Mode: 0644}
	other := core.CandidateFile{Hash: "unrelated", Mode: 0644}
	prepare := func(lineage map[string]core.RemoteIntegrationFiles, final core.CandidateFile) error {
		snapshot := core.RemoteIntegrationSnapshot{Candidate: "parent-candidate", Repository: "repository", Project: "project", Workspace: "remote:runner/workspace", Files: map[string]core.CandidateFile{"shared.txt": final}, LeafRepositories: map[string]string{"a": "repository", "b": "repository"},
			LeafOutputs: map[string]map[string]core.RemoteIntegrationOutput{"a": {"a-output": {Files: map[string]core.CandidateFile{"shared.txt": aOut}}}},
			LeafFiles:   map[string]core.RemoteIntegrationFiles{"b": lineage["b"]}, LeafLineage: lineage}
		return s.Store.Write(func(tx *store.Tx) error {
			if _, err := s.PrepareRemoteEpicIntegrationTx(tx, parentID, core.RemoteRevision(parent), snapshot); err != nil {
				return err
			}
			return errors.New("rollback")
		})
	}
	parallel := map[string]core.RemoteIntegrationFiles{
		"a": {Before: map[string]core.CandidateFile{"shared.txt": base}, After: map[string]core.CandidateFile{"shared.txt": aOut}},
		"b": {Before: map[string]core.CandidateFile{"shared.txt": base}, After: map[string]core.CandidateFile{"shared.txt": bOut}},
	}
	if err = prepare(parallel, bOut); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") {
		t.Fatalf("a later leaf that never saw the earlier result erased it silently: %v", err)
	}
	sequential := map[string]core.RemoteIntegrationFiles{
		"a": parallel["a"],
		"b": {Before: map[string]core.CandidateFile{"shared.txt": aOut}, After: map[string]core.CandidateFile{"shared.txt": bOut}},
	}
	if err = prepare(sequential, other); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") {
		t.Fatalf("unexplained final content accepted: %v", err)
	}
	if err = prepare(sequential, bOut); err == nil || err.Error() != "rollback" {
		t.Fatalf("superseded remote leaf still blocks integration: %v", err)
	}
}

func TestRemoteIntegrationUsesWorkspaceContinuityForPreClaimEdits(t *testing.T) {
	s := fixture(t)
	parentID := create(t, s, core.Definition{ID: "epic", Title: "Remote continuity", Type: "epic", Goal: "verified", Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "parent-unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"结果存在"}, Required: true, Affected: []string{"a", "b"}}}})
	for _, id := range []string{"a", "b"} {
		create(t, s, core.Definition{ID: id, Parent: parentID, Title: id, Type: "bug", Goal: "verified", Scope: []string{"shared.txt"}, Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"结果存在"}, Required: true}}})
		step(t, s, id, "fixture-"+id)
		claim, err := s.Claim(id, "", "fixture-"+id, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("remote-continuity-"), Result: core.StepResult{Status: "pass", Summary: "rewrite", Artifacts: []string{"shared.txt"}}}); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Verify(id, "unit", "checker-"+id); err != nil || n.Task.Status != "completed" {
			t.Fatalf("leaf %s: %+v %v", id, n, err)
		}
	}
	parent, err := s.Get(parentID)
	if err != nil {
		t.Fatal(err)
	}
	s.RemoteAuthority = true
	s.ValidateRemoteIntegration = func(*store.Tx, *core.Task) error { return nil }
	base := core.CandidateFile{Hash: "base", Mode: 0644}
	aOut := core.CandidateFile{Hash: "a-result", Mode: 0644}
	draft := core.CandidateFile{Hash: "b-edited-before-claim", Mode: 0644}
	bOut := core.CandidateFile{Hash: "b-result", Mode: 0644}
	file := func(f core.CandidateFile) map[string]core.CandidateFile {
		return map[string]core.CandidateFile{"shared.txt": f}
	}
	lineage := map[string]core.RemoteIntegrationFiles{"a": {Before: file(base), After: file(aOut)}, "b": {Before: file(draft), After: file(bOut)}}
	prepare := func(continuity map[string]core.RemoteIntegrationFiles) error {
		snapshot := core.RemoteIntegrationSnapshot{Candidate: "parent-candidate", Repository: "repository", Project: "project", Workspace: "remote:runner/workspace", Files: file(bOut), LeafRepositories: map[string]string{"a": "repository", "b": "repository"}, LeafFiles: lineage, LeafLineage: lineage, LeafContinuity: continuity}
		return s.Store.Write(func(tx *store.Tx) error {
			if _, err := s.PrepareRemoteEpicIntegrationTx(tx, parentID, core.RemoteRevision(parent), snapshot); err != nil {
				return err
			}
			return errors.New("rollback")
		})
	}
	if err = prepare(nil); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") || !strings.Contains(err.Error(), "a shared.txt") {
		t.Fatalf("claim input that already held b's edits must not prove lineage alone: %v", err)
	}
	if err = prepare(map[string]core.RemoteIntegrationFiles{"b": {Before: file(aOut), After: file(bOut)}}); err == nil || err.Error() != "rollback" {
		t.Fatalf("workspace continuity did not close the pre-claim edit gap: %v", err)
	}
}

func TestRemoteIntegrationDeclaresChangesOutsideParent(t *testing.T) {
	s := fixture(t)
	parentID := create(t, s, core.Definition{ID: "epic", Title: "Remote external", Type: "epic", Goal: "verified", Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "parent-unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"结果存在"}, Required: true, Affected: []string{"a", "b"}}}})
	leaves := map[string]core.Task{}
	for _, id := range []string{"a", "b"} {
		create(t, s, core.Definition{ID: id, Parent: parentID, Title: id, Type: "bug", Goal: "verified", Scope: []string{"shared.txt"}, Acceptance: []string{"结果存在"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"结果存在"}, Required: true}}})
		step(t, s, id, "fixture-"+id)
		claim, err := s.Claim(id, "", "fixture-"+id, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("remote-external-"), Result: core.StepResult{Status: "pass", Summary: "rewrite", Artifacts: []string{"shared.txt"}}}); err != nil {
			t.Fatal(err)
		}
		n, err := s.Verify(id, "unit", "checker-"+id)
		if err != nil || n.Task.Status != "completed" {
			t.Fatalf("leaf %s: %+v %v", id, n, err)
		}
		leaves[id] = *n.Task
	}
	parent, err := s.Get(parentID)
	if err != nil {
		t.Fatal(err)
	}
	s.RemoteAuthority = true
	s.ValidateRemoteIntegration = func(*store.Tx, *core.Task) error { return nil }
	f := func(hash string) core.CandidateFile { return core.CandidateFile{Hash: hash, Mode: 0644} }
	aOut, bOut := f("a-result"), f("b-result")
	lineage := map[string]core.RemoteIntegrationFiles{
		"a": {Before: map[string]core.CandidateFile{"shared.txt": f("base")}, After: map[string]core.CandidateFile{"shared.txt": aOut}},
		"b": {Before: map[string]core.CandidateFile{"shared.txt": aOut}, After: map[string]core.CandidateFile{"shared.txt": bOut, "b-only.txt": f("b-only")}},
	}
	merged := map[string]core.CandidateFile{"shared.txt": f("after-merge"), "b-only.txt": f("b-only"), "mobile.tsx": f("mobile")}
	prepare := func(final map[string]core.CandidateFile, external *core.ExternalIntegration, base map[string]core.CandidateFile) (*core.EpicIntegration, error) {
		snapshot := core.RemoteIntegrationSnapshot{Candidate: "parent-candidate", Repository: "repository", Project: "project", Workspace: "remote:runner/workspace", Files: final, LeafRepositories: map[string]string{"a": "repository", "b": "repository"}, LeafFiles: lineage, LeafLineage: lineage, External: external, ExternalBaseFiles: base}
		var integration *core.EpicIntegration
		err := s.Store.Write(func(tx *store.Tx) error {
			prepared, err := s.PrepareRemoteEpicIntegrationTx(tx, parentID, core.RemoteRevision(parent), snapshot)
			if err != nil {
				return err
			}
			integration = prepared.Integration
			return errors.New("rollback")
		})
		if err != nil && err.Error() == "rollback" {
			err = nil
		}
		return integration, err
	}
	declared := &core.ExternalIntegration{Base: "b", Sources: []string{"merge 05790dc2 (mobile)", "cfa0cd2c unit test speedup"}, Reason: "merged after b was accepted; parent checks rerun every leaf check on the final candidate"}
	if _, err = prepare(merged, nil, nil); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") || !strings.Contains(err.Error(), "external base") {
		t.Fatalf("outside change was not explained as a refusal with next step: %v", err)
	}
	integration, err := prepare(merged, declared, lineage["b"].After)
	if err != nil {
		t.Fatalf("declared outside changes still block integration: %v", err)
	}
	record := integration.External
	if record == nil || record.Base != "b" || record.BaseCandidate != leaves["b"].Candidate || len(record.Sources) != 2 || len(record.Changes) != 2 || record.Changes[0].Path != "mobile.tsx" || record.Changes[1].Path != "shared.txt" {
		t.Fatalf("outside changes were not recorded for review: %+v", record)
	}
	for _, leaf := range integration.Leaves {
		if len(leaf.Rechecks) == 0 {
			t.Fatalf("leaf %s accepted without a parent recheck", leaf.ID)
		}
	}
	early := &core.ExternalIntegration{Base: "a", Sources: declared.Sources, Reason: declared.Reason}
	if _, err = prepare(merged, early, lineage["a"].After); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") || !strings.Contains(err.Error(), "b ") {
		t.Fatalf("an earlier base hid a later leaf's lost work: %v", err)
	}
	if _, err = prepare(map[string]core.CandidateFile{"shared.txt": bOut, "b-only.txt": f("b-only")}, declared, lineage["b"].After); err == nil || !strings.Contains(err.Error(), "EPIC_EXTERNAL_UNUSED") {
		t.Fatalf("empty outside declaration accepted: %v", err)
	}
	if _, err = prepare(merged, &core.ExternalIntegration{Base: "b", Reason: "x"}, lineage["b"].After); err == nil || !strings.Contains(err.Error(), "EPIC_EXTERNAL_INVALID") {
		t.Fatalf("declaration without sources accepted: %v", err)
	}
	if _, err = prepare(merged, &core.ExternalIntegration{Base: "epic", Sources: declared.Sources, Reason: "x"}, lineage["b"].After); err == nil || !strings.Contains(err.Error(), "EPIC_EXTERNAL_INVALID") {
		t.Fatalf("non-leaf base accepted: %v", err)
	}
}

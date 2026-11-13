package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRemoteIntegrationRejectsChangedInventoryAndUnknownWorkspace(t *testing.T) {
	f := newArtifactFixture(t)
	candidate := portableFixtureCandidate(t)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", map[string]any{"task_id": "task", "expected_revision": f.revision, "checkout_id": "checkout", "workspace_id": "workspace", "runner_id": "runner", "candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	var parent core.Task
	err = service.Store.Write(func(tx *store.Tx) error {
		_, files, err := integrationCandidate(tx, candidate.ID)
		if err != nil {
			return err
		}
		parent = core.Task{Definition: core.Definition{ID: "task"}, Candidate: candidate.ID, CandidateWorkspace: "remote:runner/workspace", Integration: &core.EpicIntegration{ProjectID: "shared", RemoteRepository: candidate.Repository, GitHead: candidate.Commit, Files: files, Leaves: []core.IntegratedLeaf{{ID: "leaf", Candidate: candidate.ID, Method: "parent_rechecked", Rechecks: []core.IntegrationRecheck{{LeafCheck: "unit", ParentCheck: "integration"}}}}}}
		return validateRemoteEpicIntegration(tx, &parent, "shared")
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(parent)
	for _, test := range []struct {
		name, code string
		change     func(*core.Task)
		sql        string
	}{
		{name: "project", code: "EPIC_INTEGRATION_STALE", change: func(p *core.Task) { p.Integration.ProjectID = "other" }},
		{name: "repository", code: "EPIC_INTEGRATION_STALE", change: func(p *core.Task) { p.Integration.RemoteRepository = "other" }},
		{name: "inventory", code: "EPIC_INTEGRATION_STALE", change: func(p *core.Task) { p.Integration.Files["extra"] = core.CandidateFile{Hash: "changed"} }},
		{name: "leaf repository", code: "EPIC_REPOSITORY_UNVERIFIED", change: func(p *core.Task) { p.Integration.Leaves[0].Candidate = "missing" }},
		{name: "required rechecks", code: "EPIC_RECHECK_REQUIRED", change: func(p *core.Task) { p.Integration.Leaves[0].Rechecks = nil }},
		{name: "changed observation", code: "EPIC_CANDIDATE_CHANGED", sql: "UPDATE portable_observations SET candidate_id='changed' WHERE task_id='task'"},
		{name: "unknown workspace", code: "EPIC_WORKSPACE_BUSY", sql: "INSERT INTO remote_workspace_holds(repository,workspace_id,attempt_id,task_id,state) VALUES('repo-one','workspace','old','leaf','unknown')"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var changed core.Task
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			if test.change != nil {
				test.change(&changed)
			}
			err := service.Store.Write(func(tx *store.Tx) error {
				if test.sql != "" {
					if _, err := tx.Exec(test.sql); err != nil {
						return err
					}
				}
				return validateRemoteEpicIntegration(tx, &changed, "shared")
			})
			if err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}
}

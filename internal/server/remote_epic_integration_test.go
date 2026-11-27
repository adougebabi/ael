package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestWorkspacePredecessorUsesObservationHistory(t *testing.T) {
	f := newArtifactFixture(t)
	candidate := portableFixtureCandidate(t)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", map[string]any{"task_id": "task", "expected_revision": f.revision, "checkout_id": "checkout", "workspace_id": "workspace", "runner_id": "runner", "candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	accepted := map[string]string{"c-a": "a", "c-b": "b"}
	run := func(observations [][3]string) (string, error) {
		var found string
		err := service.Store.Write(func(tx *store.Tx) error {
			for _, statement := range []string{"DELETE FROM portable_observations", "DELETE FROM remote_runners", "INSERT INTO remote_runners(id,actor,workspace_id,checkout_id,repository,version,capabilities,created) VALUES('runner','u','workspace','checkout','repo-one','1','[]','x'),('elsewhere','u','other','checkout','repo-one','1','[]','x')"} {
				if _, err := tx.Exec(statement); err != nil {
					return err
				}
			}
			for i, o := range observations {
				if _, err := tx.Exec("INSERT INTO portable_observations(id,task_id,task_revision,candidate_id,checkout_id,workspace_id,runner_id,inputs_known,actor,created) VALUES(?,?,'r',?,'checkout',?,?,1,'u',?)", "o"+string(rune('a'+i)), o[0], o[1], o[2], map[string]string{"workspace": "runner", "other": "elsewhere"}[o[2]], "2026-11-26T00:00:0"+string(rune('0'+i))+"Z"); err != nil {
					return err
				}
			}
			var err error
			found, err = workspacePredecessor(tx, "b", "c-b-input", accepted)
			return err
		})
		return found, err
	}
	for _, test := range []struct {
		name         string
		observations [][3]string
		want         string
	}{
		{"edits before claim start from earlier leaf", [][3]string{{"a", "c-a", "workspace"}, {"b", "c-b-draft", "workspace"}, {"b", "c-b-input", "workspace"}}, "c-a"},
		{"parent observing the same result", [][3]string{{"a", "c-a", "workspace"}, {"epic", "c-a", "workspace"}, {"b", "c-b-input", "workspace"}}, "c-a"},
		{"other task touched the workspace in between", [][3]string{{"a", "c-a", "workspace"}, {"x", "c-x", "workspace"}, {"b", "c-b-input", "workspace"}}, ""},
		{"earlier result in another workspace", [][3]string{{"a", "c-a", "other"}, {"b", "c-b-input", "workspace"}}, ""},
		{"no observation of the claim input", [][3]string{{"a", "c-a", "workspace"}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			found, err := run(test.observations)
			if err != nil || found != test.want {
				t.Fatalf("predecessor = %q, %v; want %q", found, err, test.want)
			}
		})
	}
}

func TestEpicRefusalKeepsActionableDetail(t *testing.T) {
	w := httptest.NewRecorder()
	standaloneTaskError(w, core.Error("EPIC_LEAF_NOT_INCLUDED", "03 docs/a.md; lost"))
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusConflict || body["code"] != "EPIC_LEAF_NOT_INCLUDED" || body["detail"] != "03 docs/a.md; lost" {
		t.Fatalf("detail hidden: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	standaloneTaskError(w, core.Error("EPIC_LEAF_NOT_INCLUDED", "07 "+strings.Repeat("页面/", 200)))
	body = nil
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !strings.HasPrefix(body["detail"], "07 页面/") || !strings.HasSuffix(body["detail"], "...") || len(body["detail"]) > 500 || !utf8.ValidString(body["detail"]) {
		t.Fatalf("long detail not shortened safely: %s", w.Body.String())
	}
}

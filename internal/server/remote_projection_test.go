package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteCommittedReceiptReportsBlockedProjectionAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	if err = s.createUser("worker", "fixture-worker-password"); err != nil {
		t.Fatal(err)
	}
	if err = s.createProject("project", "Project"); err != nil {
		t.Fatal(err)
	}
	if err = s.addMember("project", "worker", "executor"); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.issueToken("worker", "project", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	create := map[string]any{"operation_id": "op", "idempotency_key": "key", "expected_revision": "absent", "definition": core.Definition{ID: "task", Title: "Task", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}}
	standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/project/tasks", create, nil, "", token, "", "1"), http.StatusCreated)
	service, err := s.projectService("project")
	if err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(service.Root, "projection-conflict.txt")
	if err = os.WriteFile(conflict, []byte("external change"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,'pending')", conflict, store.Hash([]byte("before")), []byte("after")); err != nil {
		t.Fatal(err)
	}
	checkBlocked := func(handler http.Handler) {
		t.Helper()
		w := standaloneRequest(t, handler, "GET", "/api/v1/projects/project/operations/key", nil, nil, "", token, "", "")
		value := standaloneJSON(t, w, http.StatusOK)
		if value["operation_id"] != "op" || value["status"] != "projection_blocked" {
			t.Fatalf("committed but unprojected operation was reported as complete: %+v", value)
		}
		task := standaloneJSON(t, standaloneRequest(t, handler, "GET", "/api/v1/projects/project/tasks/task", nil, nil, "", token, "", ""), http.StatusOK)
		if task["projection_status"] != "blocked" {
			t.Fatalf("task read concealed the pending projection: %+v", task)
		}
		w = standaloneRequest(t, handler, "POST", "/api/v1/projects/project/tasks", create, nil, "", token, "", "1")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("replay concealed pending projection: %d %s", w.Code, w.Body.String())
		}
	}
	checkBlocked(h)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err = s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	checkBlocked(h)
	if err = os.WriteFile(conflict, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered := standaloneJSON(t, standaloneRequest(t, h, "GET", "/api/v1/projects/project/operations/key", nil, nil, "", token, "", ""), http.StatusOK)
	if recovered["operation_id"] != "op" || recovered["revision"] == "" {
		t.Fatalf("operation did not recover its original receipt: %+v", recovered)
	}
	if raw, err := os.ReadFile(conflict); err != nil || string(raw) != "after" {
		t.Fatalf("pending projection did not recover: %s %v", raw, err)
	}
}

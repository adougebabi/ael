package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestFindingContextAttachmentDoesNotCreateAcceptanceAuthority(t *testing.T) {
	f := newArtifactFixture(t)
	candidate := portableFixtureCandidate(t)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	observed := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", map[string]any{"task_id": "task", "expected_revision": f.revision, "checkout_id": "checkout", "workspace_id": "workspace", "runner_id": "runner", "candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	checkID := current.Definition.Checks[0].ID
	context := map[string]any{"task_revision": f.revision, "candidate_id": candidate.ID, "task_id": "task", "id": "finding-context", "observation_id": observed["id"], "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
	raw, _ := json.Marshal(context)
	if _, err = service.Store.DB.Exec("INSERT INTO portable_contexts VALUES(?,?,?,?,?)", "finding-context", "task", candidate.ID, string(raw), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	data := []byte("contextual observation\n")
	body := map[string]any{"operation_id": "contextual-upload", "idempotency_key": "contextual-upload", "expected_revision": f.revision, "task_id": "task", "context_id": "finding-context", "candidate": candidate.ID, "filename": "report.txt", "mime": "text/plain", "size": len(data), "sha256": store.Hash(data), "source": "context-check:finding-context:" + checkID}
	started := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", body, nil, "", f.uploader, "", "1"), http.StatusCreated)
	upload := started["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "contextual-upload", "idempotency_key": "contextual-upload"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	request := map[string]any{"context_id": "finding-context", "task_id": "task", "candidate_id": candidate.ID, "check_id": checkID, "expected_revision": f.revision, "artifact_id": started["artifact_id"], "source": map[string]string{"method": "model", "tool": "fixture"}, "findings": []any{map[string]any{"rule": "rule", "title": "Issue", "kind": "functional", "severity": "low", "route": "/fixture", "location": map[string]string{"selector": "#sample"}, "expected": "A", "actual": "B", "impact": "Mismatch", "reproduction": []string{"open"}}}, "operation_id": "finding", "idempotency_key": "finding"}
	reported := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings", request, nil, "", f.uploader, "", "1"), http.StatusCreated)
	if reported["gate_effect"] != "none" {
		t.Fatal(reported)
	}
	task, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	if task.Candidate != "" || len(task.Evidence) != 0 || core.RemoteRevision(task) != f.revision {
		t.Fatal("diagnostic upload or finding changed acceptance authority")
	}
	request["source"] = map[string]string{"method": "human"}
	request["operation_id"], request["idempotency_key"] = "fake-human", "fake-human"
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings", request, nil, "", f.uploader, "", "1")
	if w.Code != http.StatusConflict {
		t.Fatalf("token impersonated human source: %d %s", w.Code, w.Body.String())
	}
	body["context_id"], body["operation_id"], body["idempotency_key"] = "other-context", "invalid-context", "invalid-context"
	w = standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", body, nil, "", f.uploader, "", "1")
	if w.Code < 400 {
		t.Fatal("unrelated context authorized a diagnostic upload")
	}
}

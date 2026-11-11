package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func portableFixtureCandidate(t *testing.T) remote.PortableCandidate {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "GIT_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Fixture")
	run("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "spec"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spec", "project.md"), []byte("# Project constraints\nUse the shared fixture.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "app.txt", "spec/project.md")
	run("commit", "-m", "fixture")
	candidate, err := remote.CapturePortableCandidate(root, "repo-one")
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestPortableContextUsesImmutableScopedSources(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	task.Definition.Checks = []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"result"}, Required: true}}
	taskBody, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec("UPDATE tasks SET body=? WHERE id='task'", string(taskBody)); err != nil {
		t.Fatal(err)
	}
	f.revision = core.RemoteRevision(task)
	candidate := portableFixtureCandidate(t)
	register := map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", register, nil, "", f.uploader, "", "1"), http.StatusCreated)
	observation := map[string]any{"task_id": "task", "expected_revision": f.revision, "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}
	seen := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", observation, nil, "", f.uploader, "", "1"), http.StatusCreated)
	content := []byte("# Project constraints\nUse the shared fixture.\n")
	start := artifactStart(t, f, "project.md", "text/plain", content, "spec-upload")
	upload, artifact := start["upload_id"].(string), start["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": content}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "spec-upload", "idempotency_key": "spec-upload"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	inventory := []any{map[string]any{"id": "source:spec/project.md", "path": "spec/project.md", "sha256": store.Hash(content), "status": "current", "kind": "spec", "paths": []string{}, "tasks": []string{}, "critical": false, "action": "connected"}}
	empty := map[string]any{"task_id": "task", "expected_revision": f.revision, "observation_id": seen["id"], "sources": []any{}, "inventory": []any{}, "operation_id": "empty", "idempotency_key": "empty"}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts", empty, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("omitted source inventory accepted: %d %s", w.Code, w.Body.String())
	}
	empty["inventory"] = inventory
	empty["operation_id"], empty["idempotency_key"] = "missing-source", "missing-source"
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts", empty, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("omitted required source accepted: %d %s", w.Code, w.Body.String())
	}
	context := map[string]any{"task_id": "task", "expected_revision": f.revision, "observation_id": seen["id"], "sources": []any{map[string]any{"id": "source:spec/project.md", "path": "spec/project.md", "artifact_id": artifact, "reason": "scope or explicit reference"}}, "inventory": inventory, "operation_id": "context", "idempotency_key": "context"}
	first := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts", context, nil, "", f.uploader, "", "1")
	created := standaloneJSON(t, first, http.StatusCreated)
	contextID := created["id"].(string)
	replay := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts", context, nil, "", f.uploader, "", "1")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("context replay differed: %d %s", replay.Code, replay.Body.String())
	}
	read := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/contexts/"+contextID, nil, nil, "", f.reader, "", ""), http.StatusOK)
	sources := read["sources"].([]any)
	source := sources[0].(map[string]any)
	if source["sha256"] != store.Hash(content) || source["status"] != "current" || read["candidate_id"] != candidate.ID || read["validity"] != "current" {
		t.Fatalf("wrong snapshot: %+v", read)
	}
	bytes := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/"+artifact+"/content", nil, nil, "", f.reader, "", "")
	if bytes.Code != http.StatusOK || bytes.Body.String() != string(content) {
		t.Fatalf("wrong snapshot bytes: %d %q", bytes.Code, bytes.Body.String())
	}
	if w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/contexts/"+contextID, nil, nil, "", f.other, "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("cross-project context exposed: %d", w.Code)
	}
	report := []byte("isolated check actually ran\n")
	unbound := map[string]any{"task_id": "task", "expected_revision": f.revision, "candidate_id": candidate.ID, "check_id": "unit", "artifact_id": artifact, "inputs_known": false, "operation_id": "unbound", "idempotency_key": "unbound"}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts/"+contextID+"/checks", unbound, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("unbound old report accepted: %d %s", w.Code, w.Body.String())
	}
	startReport := map[string]any{"operation_id": "check-report", "idempotency_key": "check-report", "expected_revision": f.revision, "task_id": "task", "filename": "check.txt", "mime": "text/plain", "size": len(report), "sha256": store.Hash(report), "source": "context-check:" + contextID + ":unit"}
	reportStart := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", startReport, nil, "", f.uploader, "", "1"), http.StatusCreated)
	reportUpload, reportID := reportStart["upload_id"].(string), reportStart["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+reportUpload+"/chunks", map[string]any{"offset": 0, "data": report}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+reportUpload+"/finalize", map[string]string{"operation_id": "check-report", "idempotency_key": "check-report"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	check := map[string]any{"task_id": "task", "expected_revision": f.revision, "candidate_id": candidate.ID, "check_id": "unit", "artifact_id": reportID, "inputs_known": false, "operation_id": "check", "idempotency_key": "check"}
	checkResponse := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts/"+contextID+"/checks", check, nil, "", f.uploader, "", "1"), http.StatusCreated)
	if checkResponse["candidate_id"] != candidate.ID || checkResponse["artifact_sha256"] != store.Hash(report) || checkResponse["gate_effect"] != "none" {
		t.Fatalf("check report lost its candidate or implied a gate pass: %+v", checkResponse)
	}
	check["candidate_id"], check["operation_id"], check["idempotency_key"] = strings.Repeat("0", 64), "bad-check", "bad-check"
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts/"+contextID+"/checks", check, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("different candidate accepted for check: %d %s", w.Code, w.Body.String())
	}
	bad := map[string]any{"task_id": "task", "expected_revision": f.revision, "observation_id": seen["id"], "sources": []any{map[string]any{"id": "source:spec/project.md", "path": "app.txt", "artifact_id": artifact, "reason": "scope"}}, "inventory": inventory, "operation_id": "wrong-context", "idempotency_key": "wrong-context"}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts", bad, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("source inconsistent with candidate accepted: %d %s", w.Code, w.Body.String())
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/cancel", map[string]any{"operation_id": "cancel", "idempotency_key": "cancel", "expected_revision": f.revision, "reason": "fixture"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	stale := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/contexts/"+contextID, nil, nil, "", f.reader, "", ""), http.StatusOK)
	if stale["validity"] != "stale" {
		t.Fatalf("old task revision appeared current: %+v", stale)
	}
	before := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	preview := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/prune", map[string]any{"before": before}, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	if preview["count"].(float64) != 1 {
		t.Fatalf("immutable specification selected for cleanup: %+v", preview)
	}
}

func TestPortableCandidateRegistrationAndProjectIsolation(t *testing.T) {
	f := newArtifactFixture(t)
	candidate := portableFixtureCandidate(t)
	register := map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo-op", "idempotency_key": "repo-key"}
	first := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", register, nil, "", f.uploader, "", "1")
	standaloneJSON(t, first, http.StatusCreated)
	replay := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", register, nil, "", f.uploader, "", "1")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("repository replay differed: %d %s", replay.Code, replay.Body.String())
	}
	publication := map[string]any{"task_id": "task", "expected_revision": f.revision, "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "candidate": candidate, "operation_id": "candidate-op", "idempotency_key": "candidate-key"}
	first = standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", publication, nil, "", f.uploader, "", "1")
	result := standaloneJSON(t, first, http.StatusCreated)
	if result["candidate_id"] != candidate.ID || result["checkout_id"] != "checkout-a" {
		t.Fatalf("incorrect candidate receipt: %+v", result)
	}
	replay = standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", publication, nil, "", f.uploader, "", "1")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("candidate replay differed: %d %s", replay.Code, replay.Body.String())
	}
	metadata := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/candidates/"+candidate.ID, nil, nil, "", f.reader, "", ""), http.StatusOK)
	if metadata["id"] != candidate.ID {
		t.Fatalf("reader saw different candidate: %+v", metadata)
	}
	if w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/candidates/"+candidate.ID, nil, nil, "", f.other, "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("cross-project candidate exposed: %d", w.Code)
	}
	publication["checkout_id"], publication["idempotency_key"], publication["operation_id"] = "checkout-b", "candidate-b", "candidate-b"
	second := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", publication, nil, "", f.uploader, "", "1"), http.StatusCreated)
	if second["candidate_id"] != candidate.ID || second["id"] == result["id"] {
		t.Fatalf("checkout identity collapsed into candidate: %+v", second)
	}
	publication["idempotency_key"], publication["operation_id"], publication["expected_revision"] = "stale", "stale", "stale-revision"
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", publication, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("stale task accepted: %d %s", w.Code, w.Body.String())
	}
}

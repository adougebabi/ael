package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaintenanceDrainsRevokesAndPreservesReceipts(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.portable("shared", service); err != nil {
		t.Fatal(err)
	}
	principal, err := f.s.token(f.uploader)
	if err != nil {
		t.Fatal(err)
	}
	var attempt core.Attempt
	if err = service.Store.Write(func(tx *store.Tx) error {
		_, claimed, err := service.RemoteClaimTx(tx, core.RemoteClaimInput{TaskID: "task", ExpectedRevision: f.revision, Runner: "runner", Workspace: "workspace", Candidate: strings.Repeat("a", 64), Token: "fixture-maintenance-lease"})
		if err != nil {
			return err
		}
		attempt = claimed
		if _, err = tx.Exec("INSERT INTO remote_runners(id,actor,workspace_id,checkout_id,repository,version,capabilities,created) VALUES(?,?,?,?,?,?,?,?)", "runner", principal.UserID, "workspace", "checkout", "repo", "fixture", "[]", "now"); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO remote_runner_attempts(attempt_id,task_id,runner_id,actor,context_id,candidate_id,claim_revision,state,fence,token_hash) VALUES(?,?,?,?,?,?,?,'running',?,?)", attempt.ID, "task", "runner", principal.UserID, "context", attempt.Input, f.revision, attempt.Fence, store.Hash([]byte(attempt.Token)))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(time.Minute)
	drain := map[string]any{"action": "drain", "reason": "fixture maintenance", "deadline": deadline, "request_key": "drain"}
	if w := standaloneRequest(t, f.h, http.MethodPost, "/api/v1/admin/maintenance", drain, nil, "", f.uploader, "", "1"); w.Code != http.StatusForbidden {
		t.Fatalf("project token changed global maintenance %d", w.Code)
	}
	first := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, "/api/v1/admin/maintenance", drain, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	if first["state"].(map[string]any)["mode"] != "draining" {
		t.Fatal(first)
	}
	if w := standaloneRequest(t, f.h, http.MethodPost, "/api/v1/projects/shared/runners/runner/claim", map[string]string{}, nil, "", f.uploader, "", "1"); w.Code != 503 {
		t.Fatalf("draining accepted claim %d %s", w.Code, w.Body.String())
	}
	heartbeat := map[string]any{"task_id": "task", "attempt_id": attempt.ID, "token": attempt.Token, "fence": attempt.Fence, "operation_id": "before", "idempotency_key": "before"}
	before := standaloneRequest(t, f.h, http.MethodPost, "/api/v1/projects/shared/runners/runner/heartbeat", heartbeat, nil, "", f.uploader, "", "1")
	standaloneJSON(t, before, 200)
	if !strings.Contains(before.Header().Get("X-AEL-Maintenance"), "draining") {
		t.Fatal("live executor missed maintenance notice")
	}
	quiesce := map[string]any{"action": "quiesce", "reason": "fixture stopped API writes", "request_key": "quiesce"}
	result := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, "/api/v1/admin/maintenance", quiesce, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	state := result["state"].(map[string]any)
	if state["mode"] != "quiesced" || state["revoked_responsibilities"] != float64(1) {
		t.Fatalf("wrong quiesce %+v", state)
	}
	if w := standaloneRequest(t, f.h, http.MethodPost, "/api/v1/projects/shared/runners/runner/heartbeat", heartbeat, nil, "", f.uploader, "", "1"); w.Code != 503 {
		t.Fatalf("quiesced write accepted %d", w.Code)
	}
	receipt := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodGet, "/api/v1/projects/shared/portable-operations/before", nil, nil, "", f.uploader, "", ""), 200)
	if receipt["found"] != true {
		t.Fatal("old immutable receipt disappeared")
	}
	task, err := service.Get("task")
	if err != nil || task.Attempts[0].Status != "interrupted" || !task.Attempts[0].Lease.IsZero() {
		t.Fatalf("old authority alive %+v %v", task, err)
	}
	var holds int
	service.Store.DB.QueryRow("SELECT count(*) FROM remote_workspace_holds WHERE state='unknown'").Scan(&holds)
	if holds != 1 {
		t.Fatal("unknown workspace protection lost")
	}
	replay := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, "/api/v1/admin/maintenance", quiesce, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	if replay["replayed"] != true {
		t.Fatal("maintenance replay changed")
	}
	resume := map[string]any{"action": "resume", "reason": "fixture resume", "request_key": "resume"}
	standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, "/api/v1/admin/maintenance", resume, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	heartbeat["operation_id"], heartbeat["idempotency_key"] = "after", "after"
	after := standaloneRequest(t, f.h, http.MethodPost, "/api/v1/projects/shared/runners/runner/heartbeat", heartbeat, nil, "", f.uploader, "", "1")
	if after.Code != 409 {
		t.Fatalf("old lease revived after resume %d %s", after.Code, after.Body.String())
	}
	raw, _ := json.Marshal(task.Blocker)
	if strings.Contains(string(raw), "host stopped") {
		t.Fatal("host termination was invented")
	}
}

func TestMaintenanceQuiesceWaitsForInflightAuthorizationAndResumesPartialWork(t *testing.T) {
	f := newArtifactFixture(t)
	deadline := time.Now().Add(time.Minute)
	drain := maintenanceInput{Action: "drain", Reason: "concurrent maintenance", Deadline: &deadline, RequestKey: "fenced-drain"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/admin/maintenance", drain, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	if err := f.s.createProject("zzz", "failure fixture"); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(f.s.store.Dir, "projects", store.Hash([]byte("zzz"))[:32], "workspace", ".ael", "policy.yaml")
	os.MkdirAll(filepath.Dir(policy), 0700)
	os.WriteFile(policy, []byte("changed operator file"), 0600)
	held, err := f.s.lockAuthorization(false)
	if err != nil {
		t.Fatal(err)
	}
	input := maintenanceInput{Action: "quiesce", Reason: "concurrent maintenance", RequestKey: "fenced-quiesce"}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- standaloneRequest(t, f.h, "POST", "/api/v1/admin/maintenance", input, f.admin, f.csrf, "", "http://server.test", "1")
	}()
	select {
	case <-result:
		held()
		t.Fatal("maintenance passed an in-flight authorization fence")
	case <-time.After(40 * time.Millisecond):
	}
	held()
	response := <-result
	if response.Code != 409 {
		t.Fatalf("expected retained partial maintenance %d %s", response.Code, response.Body.String())
	}
	state, err := f.s.Maintenance()
	if err != nil || state.Mode != "quiescing" {
		t.Fatalf("partial state lost %+v %v", state, err)
	}
	wrong := input
	wrong.RequestKey = "other-operation"
	if response := standaloneRequest(t, f.h, "POST", "/api/v1/admin/maintenance", wrong, f.admin, f.csrf, "", "http://server.test", "1"); response.Code != 409 || !strings.Contains(response.Body.String(), "MAINTENANCE_RECOVERY_REQUIRED") {
		t.Fatal("different operation resumed partial quiesce")
	}
	os.WriteFile(policy, []byte(bootstrap.DefaultPolicy), 0600)
	final := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/admin/maintenance", input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	if final["state"].(map[string]any)["mode"] != "quiesced" {
		t.Fatal(final)
	}
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = service.Store.DB.QueryRow("SELECT count(*) FROM events WHERE kind='server_maintenance_epoch'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial retry duplicated completed project %d %v", count, err)
	}
}

func TestMaintenanceConditionalStateRejectsStaleWriterAndPreservesReplay(t *testing.T) {
	f := newArtifactFixture(t)
	path := "/api/v1/admin/maintenance"
	status := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodGet, path, nil, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	revision := status["revision"].(string)
	input := map[string]any{"action": "drain", "reason": "first conditional controller", "request_key": "conditional-first", "deadline": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), "expected_state": revision}
	first := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	replay := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	if replay["replayed"] != true {
		t.Fatal("original conditional request did not replay")
	}
	input["request_key"] = "conditional-second"
	input["reason"] = "stale second operator"
	response := standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1")
	if response.Code != 409 {
		t.Fatalf("stale state accepted %d %s", response.Code, response.Body.String())
	}
	current, err := f.s.Maintenance()
	if err != nil || current.Reason != "first conditional controller" {
		t.Fatal("stale writer changed ownership", current, err)
	}
	input = map[string]any{"action": "quiesce", "reason": "first conditional controller", "request_key": "conditional-quiesce", "expected_state": first["current_revision"]}
	quiesced := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	input = map[string]any{"action": "resume", "reason": "first conditional controller", "request_key": "conditional-resume", "expected_state": quiesced["current_revision"]}
	standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
}

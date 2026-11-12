package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteExpiredResponsibilityOverHTTP(t *testing.T) {
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
	candidate := portableFixtureCandidate(t)
	var attempt core.Attempt
	err = service.Store.Write(func(tx *store.Tx) error {
		_, claimed, err := service.RemoteClaimTx(tx, core.RemoteClaimInput{TaskID: "task", ExpectedRevision: f.revision, Runner: "runner-expiry", Workspace: "workspace-expiry", Candidate: candidate.ID, Token: "fixture-expiring-lease"})
		if err != nil {
			return err
		}
		attempt = claimed
		if _, err = tx.Exec("INSERT INTO remote_runners VALUES(?,?,?,?,?,?,?,?)", "runner-expiry", principal.UserID, "workspace-expiry", "checkout", "repo-one", "fixture", `["development"]`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO remote_runner_attempts(attempt_id,task_id,runner_id,actor,context_id,candidate_id,claim_revision,output_candidate,fence,token_hash) VALUES(?,?,?,?,?,?,?,?,?,?)", attempt.ID, "task", "runner-expiry", principal.UserID, "fixture-context", candidate.ID, f.revision, candidate.ID, attempt.Fence, store.Hash([]byte(attempt.Token)))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.h)
	defer server.Close()
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, server.URL+"/api/v1/projects/shared"+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "server.test"
		req.Header.Set("Authorization", "Bearer "+f.uploader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-AEL-Protocol", "1")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("HTTP %d want %d: %s %v", response.StatusCode, want, data, err)
		}
		var result map[string]any
		if json.Unmarshal(data, &result) != nil {
			t.Fatalf("bad HTTP response: %s", data)
		}
		return result
	}
	lease := map[string]any{"task_id": "task", "attempt_id": attempt.ID, "token": attempt.Token, "fence": attempt.Fence, "operation_id": "before-expiry", "idempotency_key": "before-expiry"}
	call("POST", "/runners/runner-expiry/heartbeat", lease, http.StatusOK)
	// Move only the test fixture's persisted deadline into the past. The clients
	// still use real TCP HTTP and the production lease checks, with no bypass flag.
	task, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	task.Attempts[len(task.Attempts)-1].Lease = time.Now().Add(-time.Second)
	data, _ := json.Marshal(task)
	if _, err = service.Store.DB.Exec("UPDATE tasks SET body=? WHERE id='task'", string(data)); err != nil {
		t.Fatal(err)
	}
	lease["operation_id"], lease["idempotency_key"] = "expired-heartbeat", "expired-heartbeat"
	result := call("POST", "/runners/runner-expiry/heartbeat", lease, http.StatusConflict)
	if result["code"] != "STALE_TOKEN" {
		t.Fatalf("expired heartbeat: %+v", result)
	}
	submit := map[string]any{"task_id": "task", "attempt_id": attempt.ID, "token": attempt.Token, "fence": attempt.Fence, "expected_revision": f.revision, "candidate_id": candidate.ID, "result": map[string]any{"status": "pass", "summary": "late result"}, "operation_id": "expired-submit", "idempotency_key": "expired-submit"}
	result = call("POST", "/runners/runner-expiry/submit", submit, http.StatusConflict)
	if result["code"] != "STALE_TOKEN" {
		t.Fatalf("expired submission: %+v", result)
	}
	view := call("GET", "/runners/runner-expiry", nil, http.StatusOK)
	if view["connection_state"] != "lost" || view["host_execution_state"] != "unknown" {
		t.Fatalf("expiry misreported host execution: %+v", view)
	}
	if raw, _ := json.Marshal(view); strings.Contains(string(raw), attempt.Token) {
		t.Fatal("runner view exposed its lease")
	}
}

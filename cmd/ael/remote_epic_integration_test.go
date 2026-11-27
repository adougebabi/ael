package main

import (
	"ael/internal/remote"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteEpicIntegrationUsesFormalContextAndRevision(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/meta" {
			json.NewEncoder(w).Encode(map[string]any{"mode": "server", "protocol": 1, "capabilities": []string{"remote_tasks", "remote_integration"}})
			return
		}
		if r.URL.Path != "/api/v1/projects/shared/tasks/parent/integrate" || r.Method != "POST" {
			t.Errorf("wrong endpoint %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["context_id"] != "context-one" || body["expected_revision"] != "exact-revision" || body["impact_artifact"] != "artifact-one" {
			t.Error("integration request lost its bindings")
		}
		external, _ := body["external"].(map[string]any)
		if external["base"] != "leaf-08" || external["reason"] != "merged after leaf-08" || len(external["sources"].([]any)) != 1 {
			t.Error("integration request lost its bindings")
		}
		called = true
		json.NewEncoder(w).Encode(map[string]any{"prepared": true})
	}))
	defer server.Close()
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := remote.SaveProfile(config, root, "central", server.URL, "ael_fixture_token"); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(t.TempDir(), "external.json")
	if err := os.WriteFile(externalFile, []byte(`{"base":"leaf-08","sources":["merge 05790dc2"],"reason":"merged after leaf-08"}`), 0600); err != nil {
		t.Fatal(err)
	}
	args := args{Flags: map[string]string{"project": root, "config-dir": config, "task": "parent", "context": "context-one", "impact-artifact": "artifact-one", "external-file": externalFile, "expected-revision": "exact-revision", "operation-id": "integrate", "idempotency-key": "integrate"}}
	_, err := remoteCommand("task integrate", args, remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"})
	if err != nil || !called {
		t.Fatalf("formal Remote parent integration unavailable %v", err)
	}
}

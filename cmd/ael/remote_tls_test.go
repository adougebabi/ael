package main

import (
	"ael/internal/remote"
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentAndGuardedCommunicationUseExplicitAuthority(t *testing.T) {
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/meta" {
			io.WriteString(w, `{"mode":"server","protocol":1,"capabilities":["remote_tasks"]}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer ael_fixture" {
			http.Error(w, `{"code":"AUTH_REQUIRED"}`, 401)
			return
		}
		io.WriteString(w, `{"items":[]}`)
	}))
	defer endpoint.Close()
	authority := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: endpoint.Certificate().Raw}))
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := remote.SaveProfileWithAuthority(config, project, "tls", endpoint.URL, "ael_fixture", authority); err != nil {
		t.Fatal(err)
	}
	binding := remote.Binding{Schema: 1, Profile: "tls", ProjectID: "shared"}
	if result, err := agentCommunicationCall(config, project, binding, http.MethodGet, "/tasks", nil); err != nil || result["authoritative"] != true {
		t.Fatalf("retained operation lost CA %+v %v", result, err)
	}
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
	if result, err := runnerGuardedCall(config, project, binding, guard, http.MethodGet, "/tasks", nil); err != nil || result["authoritative"] != true {
		t.Fatalf("guarded operation lost CA %+v %v", result, err)
	}
}

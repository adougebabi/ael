package remote

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientWritesNeverFallbackWhenOffline(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err != nil {
		t.Fatal(err)
	}
	binding := Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/meta" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"mode":"server","protocol":1,"capabilities":["remote_tasks"]}`))}, nil
		}
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer ael_synthetic" || r.Header.Get("X-AEL-Protocol") != "1" || r.URL.Path != "/api/v1/projects/shared/tasks" {
			t.Fatalf("incorrect remote write: %s %s", r.Method, r.URL.Path)
		}
		return nil, errors.New("synthetic network interruption")
	})}
	_, err := Call(config, project, binding, http.MethodPost, "/tasks", map[string]string{"operation_id": "op", "idempotency_key": "key"}, client)
	if err == nil || !strings.Contains(err.Error(), "REMOTE_UNAVAILABLE") || strings.Contains(err.Error(), "ael_synthetic") {
		t.Fatalf("offline write was not safely rejected: %v", err)
	}
	read := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/meta" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"mode":"server","protocol":1,"capabilities":["remote_tasks"]}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusConflict, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"REVISION_CONFLICT","error":"private server path"}`))}, nil
	})}
	_, err = Call(config, project, binding, http.MethodPost, "/tasks", map[string]string{"operation_id": "op", "idempotency_key": "key"}, read)
	if err == nil || !strings.Contains(err.Error(), "REVISION_CONFLICT") || strings.Contains(err.Error(), "private server path") {
		t.Fatalf("server conflict was not sanitized: %v", err)
	}
}

func TestOfflineReadIsExplicitlyMarkedAsStaleCache(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err != nil {
		t.Fatal(err)
	}
	binding := Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	online := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"items":[{"task":{"status":"completed"}}]}`))}, nil
	})}
	first, err := Call(config, project, binding, http.MethodGet, "/tasks", nil, online)
	if err != nil || first["online"] != true || first["observed_at"] == "" {
		t.Fatalf("online read failed: %+v %v", first, err)
	}
	offline := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic offline") })}
	cached, err := Call(config, project, binding, http.MethodGet, "/tasks", nil, offline)
	if err != nil || cached["online"] != false || cached["authoritative"] != false || cached["cached_at"] != first["observed_at"] {
		t.Fatalf("offline cache was not explicitly marked stale: %+v %v", cached, err)
	}
	revoked := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"AUTH_REQUIRED"}`))}, nil
	})}
	if _, err = Call(config, project, binding, http.MethodGet, "/tasks", nil, revoked); err == nil || !strings.Contains(err.Error(), "AUTH_REQUIRED") {
		t.Fatalf("revoked token was hidden behind offline cache: %v", err)
	}
	if err = SaveProfile(config, project, "other", "http://127.0.0.1:7332", "ael_other_user"); err != nil {
		t.Fatal(err)
	}
	other := Binding{Schema: 1, Profile: "other", ProjectID: "shared"}
	if _, err = Call(config, project, other, http.MethodGet, "/tasks", nil, offline); err == nil || !strings.Contains(err.Error(), "REMOTE_UNAVAILABLE") {
		t.Fatalf("another credential received the first user's offline cache: %v", err)
	}
}

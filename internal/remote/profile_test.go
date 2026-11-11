package remote

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProfileAndBindingAreSeparateAndOfflineDoesNotFallback(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "client-config")
	const token = "ael_synthetic_test_token"
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", token); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(config, "profiles", "central.json")
	info, err := os.Stat(profilePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("profile credentials are not private: %v %v", info, err)
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && r.URL.Path == "/api/v1/meta" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"mode":"server","protocol":1,"capabilities":["remote_tasks"]}`))}, nil
		}
		if r.Method != "GET" || r.URL.Path != "/api/v1/projects/shared" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("unexpected remote project lookup: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"shared","name":"Shared","role":"executor"}`))}, nil
	})}
	binding, err := Connect(config, project, "central", "shared", client)
	if err != nil || binding.ProjectID != "shared" {
		t.Fatalf("connect failed: %+v %v", binding, err)
	}
	raw, err := os.ReadFile(filepath.Join(project, ".ael", "remote.json"))
	if err != nil || strings.Contains(string(raw), token) || strings.Contains(string(raw), config) {
		t.Fatalf("binding contains credentials or client config location: %s %v", raw, err)
	}
	loaded, exists, err := ReadBinding(project)
	if err != nil || !exists || loaded != binding {
		t.Fatalf("binding did not survive read: %+v %v %v", loaded, exists, err)
	}
	if _, err = os.Stat(filepath.Join(project, ".ael", "work-items")); !os.IsNotExist(err) {
		t.Fatal("remote connection initialized a local task authority")
	}

	offlineProject := t.TempDir()
	offline := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic offline")
	})}
	if _, err = Connect(config, offlineProject, "central", "shared", offline); err == nil {
		t.Fatal("offline connect was reported successful")
	}
	if _, exists, err = ReadBinding(offlineProject); err != nil || exists {
		t.Fatal("offline connect created a local authority or binding", err)
	}
}

func TestRemoteConnectCannotRaceLocalInitialization(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	t.Setenv("AEL_DATA_DIR", filepath.Join(t.TempDir(), "runtimes"))
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err != nil {
		t.Fatal(err)
	}
	reached, proceed := make(chan struct{}), make(chan struct{})
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/meta" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"mode":"server","protocol":1,"capabilities":["remote_tasks"]}`))}, nil
		}
		close(reached)
		<-proceed
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"shared","role":"executor"}`))}, nil
	})}
	result := make(chan error, 1)
	go func() { _, err := Connect(config, project, "central", "shared", client); result <- err }()
	<-reached
	if _, err := bootstrap.Init(project, false, false); err != nil {
		close(proceed)
		t.Fatal(err)
	}
	service, _, err := bootstrap.Open(project)
	if err != nil {
		close(proceed)
		t.Fatal(err)
	}
	defer service.Store.Close()
	close(proceed)
	if err = <-result; err == nil {
		t.Fatal("remote connect succeeded while local authority was active")
	}
	if _, exists, err := ReadBinding(project); err != nil || exists {
		t.Fatalf("competing remote binding was created: %v %v", exists, err)
	}
	if _, err = service.Create(core.Definition{ID: "local", Title: "Local", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}); err != nil {
		t.Fatalf("local service was damaged by refused connect: %v", err)
	}
}

func TestConnectRejectsServerWithoutRemoteTasksCapability(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/meta" {
			t.Fatal("client contacted an unsupported task API")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"mode":"server","protocol":1,"capabilities":["projects"]}`))}, nil
	})}
	if _, err := Connect(config, project, "central", "shared", client); err == nil || !strings.Contains(err.Error(), "REMOTE_PROTOCOL_UNSUPPORTED") {
		t.Fatalf("unsupported Server accepted: %v", err)
	}
	if _, exists, err := ReadBinding(project); err != nil || exists {
		t.Fatalf("unsupported Server wrote a binding: %v %v", exists, err)
	}
}

func TestProfileRefusesCredentialsInsideCheckoutAndUnsafeURLs(t *testing.T) {
	project := t.TempDir()
	if err := SaveProfile(filepath.Join(project, "private"), project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err == nil {
		t.Fatal("credentials accepted inside checkout")
	}
	if _, err := os.Stat(filepath.Join(project, "private")); !os.IsNotExist(err) {
		t.Fatal("rejected credential directory was created")
	}
	if err := SaveProfile(filepath.Join(t.TempDir(), "config"), project, "central", "http://example.test:7332", "ael_synthetic"); err == nil {
		t.Fatal("plaintext token transport to non-loopback host accepted")
	}
}

func TestProfileRejectsCredentialFileHardLinkedIntoCheckout(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "profiles", "central.json")
	if err := os.Link(path, filepath.Join(project, "linked-profile.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(config, project, "central"); err == nil {
		t.Fatal("credential file with an external hard link was accepted")
	}
}

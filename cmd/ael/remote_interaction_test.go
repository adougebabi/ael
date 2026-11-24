package main

import (
	"ael/internal/remote"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestChatDecisionUsesReviewerSession(t *testing.T) {
	for _, test := range []struct{ command, endpoint string }{{"task confirm", "/tasks/leaf/confirm"}, {"task review", "/reviews"}, {"finding review", "/findings/issue/decisions"}} {
		t.Run(test.command, func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("runner credential used for human decision")
				}
				switch r.URL.Path {
				case "/api/v1/auth/login":
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "reviewer", Path: "/"})
					io.WriteString(w, `{"csrf":"csrf"}`)
				case "/api/v1/projects/shared" + test.endpoint:
					cookie, err := r.Cookie("session")
					if err != nil || cookie.Value != "reviewer" || r.Header.Get("X-AEL-CSRF") != "csrf" || r.Header.Get("Origin") != "http://"+r.Host {
						t.Error("missing reviewer session binding")
					}
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["candidate"] != "reviewed" || body["plan_revision"] != float64(2) {
						t.Error("lost decision version")
					}
					called = true
					io.WriteString(w, `{"accepted":true}`)
				case "/api/v1/auth/logout":
					io.WriteString(w, `{}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			project, config := t.TempDir(), filepath.Join(t.TempDir(), "private")
			if err := remote.SaveProfile(config, project, "test", server.URL, "ael_fixture"); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "request")
			if err := os.WriteFile(file, []byte(`{"candidate":"reviewed","plan_revision":2}`), 0600); err != nil {
				t.Fatal(err)
			}
			pass := filepath.Join(t.TempDir(), "stdin")
			if err := os.WriteFile(pass, []byte("test-password\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reader, err := os.Open(pass)
			if err != nil {
				t.Fatal(err)
			}
			old := os.Stdin
			os.Stdin = reader
			defer func() { os.Stdin = old; reader.Close() }()
			a := args{Flags: map[string]string{"username": "reviewer", "password-stdin": "true", "project": project, "config-dir": config, "file": file}}
			if test.command == "finding review" {
				a.Flags["finding"] = "issue"
			}
			if test.command == "task confirm" {
				a.Flags["task"] = "leaf"
			}
			if _, err := remoteCommand(test.command, a, remote.Binding{Schema: 1, Profile: "test", ProjectID: "shared"}); err != nil || !called {
				t.Fatal("decision not delivered", err)
			}
		})
	}
}

func TestRemoteKeeperStopsWithOwner(t *testing.T) {
	owner := exec.Command("sleep", "30")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { owner.Process.Kill(); owner.Wait() }()
	var renewals int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/meta" {
			io.WriteString(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
			return
		}
		if r.URL.Path != "/api/v1/projects/shared/runners/agent/heartbeat" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["attempt_id"] != "attempt" || body["fence"] != float64(1) || body["token"] != "lease" {
			t.Error("lost lease identity")
		}
		renewals++
		if renewals == 2 {
			owner.Process.Kill()
		}
		now := time.Now()
		json.NewEncoder(w).Encode(map[string]any{"attempt_id": "attempt", "fence": 1, "lease_issued_at": now, "lease_until": now.Add(300 * time.Millisecond)})
	}))
	defer server.Close()
	project, config := t.TempDir(), filepath.Join(t.TempDir(), "private")
	if err := remote.SaveProfile(config, project, "test", server.URL, "ael_fixture"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "claim")
	if err := os.WriteFile(file, []byte(`{"runner_id":"agent","task_id":"leaf","attempt_id":"attempt","fence":1,"token":"lease","expected_revision":"r1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := args{Flags: map[string]string{"owner-pid": strconv.Itoa(owner.Process.Pid), "project": project, "config-dir": config, "lease-file": file}}
	_, err := remoteCommand("runner keepalive", a, remote.Binding{Schema: 1, Profile: "test", ProjectID: "shared"})
	if err != nil || renewals != 2 {
		t.Fatal("owner-bound heartbeat", renewals, err)
	}
	lease, _, err := readAgentLease(file)
	if err != nil || lease.LeaseUntil.IsZero() {
		t.Fatal("receipt did not record renewal", err)
	}
}

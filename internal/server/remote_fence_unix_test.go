//go:build darwin || linux

package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRemoteRevocationWaitsForAlreadyFencedWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
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
	tokenID, token, err := s.issueToken("worker", "project", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	other, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	otherHandler, err := other.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	admin, csrf := standaloneLogin(t, otherHandler, "owner", "fixture-admin-password")
	service, err := s.projectService("project")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := service.Store.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	released := false
	releaseDB := func() {
		if !released {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
			_ = conn.Close()
			released = true
		}
	}
	defer releaseDB()
	probe, err := os.OpenFile(filepath.Join(dir, "authorization.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	writeDone := make(chan int, 1)
	go func() {
		body := map[string]any{"operation_id": "op", "idempotency_key": "key", "expected_revision": "absent", "definition": map[string]any{"id": "task", "title": "Task", "type": "bug", "goal": "verified", "acceptance": []string{"result"}}}
		response := standaloneRequest(t, h, "POST", "/api/v1/projects/project/tasks", body, nil, "", token, "", "1")
		writeDone <- response.Code
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err != nil {
			if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
				t.Fatal(err)
			}
			break
		}
		_ = unix.Flock(int(probe.Fd()), unix.LOCK_UN)
		if time.Now().After(deadline) {
			t.Fatal("write never acquired its authorization fence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	revokeDone := make(chan int, 1)
	go func() {
		response := standaloneRequest(t, otherHandler, "POST", "/api/v1/tokens/revoke", map[string]string{"id": tokenID}, admin, csrf, "", "http://server.test", "1")
		revokeDone <- response.Code
	}()
	select {
	case code := <-revokeDone:
		t.Fatalf("revocation completed before fenced write: %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	releaseDB()
	if code := <-writeDone; code != 201 {
		t.Fatalf("already fenced write failed: %d", code)
	}
	if code := <-revokeDone; code != 200 {
		t.Fatalf("revocation did not finish after write: %d", code)
	}
}

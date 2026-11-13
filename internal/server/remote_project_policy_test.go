package server

import (
	"ael/internal/bootstrap"
	"ael/internal/securityfs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandalonePolicyPublicationIsExclusiveAndRetainsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- initializeStandalonePolicy(path) }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != bootstrap.DefaultPolicy {
		t.Fatal("published policy incomplete")
	}
	for _, changed := range []string{"", `{"schema":1,"lease_seconds":1}`} {
		if err = os.WriteFile(path, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		if err = initializeStandalonePolicy(path); err == nil || !strings.Contains(err.Error(), "SERVER_POLICY_CHANGED") {
			t.Fatalf("changed policy accepted %v", err)
		}
		body, err = os.ReadFile(path)
		if err != nil || string(body) != changed {
			t.Fatal("changed policy was overwritten")
		}
	}
}

func TestStandalonePolicyRecoversInterruptedPublicationAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	stage := filepath.Join(dir, ".policy-bootstrap-interrupted")
	if err := os.WriteFile(stage, []byte(bootstrap.DefaultPolicy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(stage, path); err != nil {
		t.Fatal(err)
	}
	if err := initializeStandalonePolicy(path); err != nil {
		t.Fatal(err)
	}
	if err := securityfs.SingleLinkRegular(path); err != nil {
		t.Fatalf("interrupted initialization left an unsafe policy alias %v", err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatal("known initialization alias retained")
	}
}

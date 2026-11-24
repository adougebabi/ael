package main

import (
	"ael/internal/remote"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneRunnerDisplayName(t *testing.T) {
	for _, host := range []string{"", " \t", "bad\nname", strings.Repeat("x", 161)} {
		if got := cloneAlias(host, "runner-unique"); got != "runner-unique" {
			t.Errorf("invalid host yielded %q", got)
		}
	}
	if got := cloneAlias(" test.lan ", "runner-unique"); got != "test.lan" {
		t.Fatal(got)
	}
}

func TestCloneReviewerRejectsRepositorySuppliedOrigin(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := remote.SaveProfile(config, root, "clone-example", "https://repo-selected.example", "ael_example"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "decision")
	if err := os.WriteFile(file, []byte(`{"reason":"explicit decision"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AEL_REVIEWER_ORIGIN", "https://reviewer-selected.example")
	_, err := remoteDecision("task confirm", args{Flags: map[string]string{"task": "task", "file": file, "username": "reviewer", "password-stdin": "true"}}, config, root, remote.Binding{Profile: "clone-example", ProjectID: "shared"})
	if err == nil || !strings.Contains(err.Error(), "REVIEWER_ORIGIN_REQUIRED") {
		t.Fatal("unchecked credential destination", err)
	}
}

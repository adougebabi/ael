//go:build darwin || linux

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileExpectedPreservesEditsBeforeAndAfterIntent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "definition.md")
	original := []byte("original")
	if err = os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	expected := Hash(original)
	if err = os.WriteFile(path, []byte("user-before"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.Write(func(tx *Tx) error { return tx.FileExpected(path, []byte("proposal"), expected) }); err == nil || !strings.Contains(err.Error(), "FILE_CONFLICT") {
		t.Fatalf("changed input was accepted: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "user-before" {
		t.Fatal("user edit overwritten")
	}
	if err = os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.Write(func(tx *Tx) error { return tx.FileExpected(path, []byte("proposal"), expected) }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("user-after"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(); err == nil || !strings.Contains(err.Error(), "FILE_CONFLICT") {
		t.Fatalf("late edit was accepted: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "user-after" {
		t.Fatal("late user edit overwritten")
	}
}

func TestFileExpectedDoesNotReplaceNewUserFile(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "new-definition.md")
	if err = s.Write(func(tx *Tx) error { return tx.FileExpected(path, []byte("proposed"), "") }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("user-created"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(); err == nil || !strings.Contains(err.Error(), "FILE_CONFLICT") {
		t.Fatalf("new user file was overwritten: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "user-created" {
		t.Fatal("new user file content changed")
	}
}

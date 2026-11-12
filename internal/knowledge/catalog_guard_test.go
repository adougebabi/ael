package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexGuardPreventsWritingAfterAuthorityLoss(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("fixture before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Index(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".ael", "knowledge-sources.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("fixture after\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = IndexGuarded(root, func() error { return errors.New("fixture lease expired") }); err == nil {
		t.Fatal("expired authority was allowed to write the index")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("existing index was modified after authority loss: %v", err)
	}
}

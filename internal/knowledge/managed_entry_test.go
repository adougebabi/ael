package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedOnlyEntryKeepsKnowledgeHealthEmpty(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	managed := "<!-- AEL:BEGIN -->\nRead the AEL tool Skill.\n<!-- AEL:END -->\n"
	if err := os.WriteFile(path, []byte(managed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Index(root); err != nil {
		t.Fatal(err)
	}
	if health := Status(root); health.State != "empty" {
		t.Fatalf("managed-only entry made empty project ready: %+v", health)
	}
	if err := os.WriteFile(path, []byte("Keep project rules.\n"+managed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Index(root); err != nil {
		t.Fatal(err)
	}
	if health := Status(root); health.State != "ready" {
		t.Fatalf("user rules were not ready: %+v", health)
	}
}

func TestManagedEntryWithoutProjectRulesIsNotCriticalKnowledge(t *testing.T) {
	managed := "<!-- AEL:BEGIN -->\nRead the AEL tool Skill.\n<!-- AEL:END -->\n"
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		entry, err := parseSource(name, []byte(managed))
		if err != nil || entry.Critical || entry.Status != "historical" || entry.Body != "" {
			t.Fatalf("managed prompt became project rules %+v %v", entry, err)
		}
		entry, err = parseSource(name, []byte("Keep user-specific project rules.\n"+managed))
		if err != nil || !entry.Critical || entry.Status != "current" || entry.Body != "Keep user-specific project rules." {
			t.Fatalf("user rules lost %+v %v", entry, err)
		}
	}
}

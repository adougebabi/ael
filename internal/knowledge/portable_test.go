package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableFreshnessRejectsAlteredCatalogClassification(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "spec"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spec", "rules.md"), []byte("# Current rule\n"), 0644); err != nil {
		t.Fatal(err)
	}
	catalog, err := Index(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckFresh(root); err != nil {
		t.Fatal(err)
	}
	catalog.Sources[0].Action = "ignored"
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".ael", "knowledge-sources.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err = CheckFresh(root); err == nil || !strings.Contains(err.Error(), "KNOWLEDGE_INDEX_STALE") {
		t.Fatalf("altered classification passed freshness: %v", err)
	}
}

package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// A reviewed migration installs an explicit knowledge authority. Indexing names
// that authority and retires the legacy path, rather than silently preferring a copy.
func migratedAuthorities(root string) (map[string]string, error) {
	out := map[string]string{}
	base := filepath.Join(root, ".ael/knowledge")
	e := filepath.WalkDir(base, func(p string, d fs.DirEntry, e error) error {
		if os.IsNotExist(e) && p == base {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		safe, e := store.SafePath(root, rel)
		if e != nil {
			return e
		}
		b, e := os.ReadFile(safe)
		if e != nil {
			return e
		}
		if !strings.HasPrefix(string(b), "---\n") {
			return nil
		}
		parts := strings.SplitN(strings.TrimPrefix(string(b), "---\n"), "\n---\n", 2)
		if len(parts) != 2 {
			return core.Error("INVALID_KNOWLEDGE", rel)
		}
		var r Record
		if e = yaml.Unmarshal([]byte(parts[0]), &r); e != nil {
			return e
		}
		if !strings.Contains(r.Source, "#sha256=") {
			return nil
		}
		source := strings.SplitN(r.Source, "#sha256=", 2)[0]
		if _, e = store.SafePath(root, source); e != nil {
			return e
		}
		if old := out[source]; old != "" && old != filepath.ToSlash(rel) {
			return core.Error("KNOWLEDGE_AUTHORITY_CONFLICT", source)
		}
		out[source] = filepath.ToSlash(rel)
		return nil
	})
	return out, e
}

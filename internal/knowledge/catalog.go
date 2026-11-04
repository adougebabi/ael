package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Catalog is an index of authoritative source files, never another editable copy.
type Catalog struct {
	Schema  int            `json:"schema"`
	Sources []Source       `json:"sources"`
	Counts  map[string]int `json:"counts"`
}
type Source struct {
	Target string `json:"authority_path,omitempty"`
	Path   string `json:"path"`
	Hash   string `json:"hash"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	Record Record `json:"record"`
}
type Health struct {
	State   string         `json:"state"`
	Reason  string         `json:"reason"`
	Counts  map[string]int `json:"counts"`
	Sources []Source       `json:"sources"`
}

func parseSource(rel string, b []byte) (Record, error) {
	r := Record{ID: "source:" + rel, Title: filepath.Base(rel), Kind: "reference", Status: "historical", Source: rel, Path: rel, Hash: store.Hash(b)}
	body := string(b)
	if start := strings.Index(body, "<!-- AEL:BEGIN -->"); start >= 0 {
		if end := strings.Index(body[start:], "<!-- AEL:END -->"); end >= 0 {
			body = body[:start] + body[start+end+len("<!-- AEL:END -->"):]
		}
	}
	lower := strings.ToLower(rel)
	if rel == "AGENTS.md" || rel == "CLAUDE.md" || strings.Contains(lower, "/spec/") || strings.HasPrefix(lower, "spec/") || strings.HasSuffix(lower, "/spec.md") {
		r.Kind = "spec"
		r.Status = "current"
		if rel == "AGENTS.md" || rel == "CLAUDE.md" {
			r.Critical = true
		}
		if strings.HasPrefix(rel, ".trellis/spec/") {
			rest := strings.TrimPrefix(rel, ".trellis/spec/")
			if i := strings.Index(rest, "/"); i >= 0 {
				r.Paths = []string{rest[:i]}
			}
		}
	}
	if strings.Contains(lower, "/adr/") || strings.Contains(lower, "/decisions/") || strings.HasPrefix(lower, "notes/") {
		r.Kind = "decision"
	}
	if strings.HasPrefix(body, "---\n") {
		parts := strings.SplitN(strings.TrimPrefix(body, "---\n"), "\n---\n", 2)
		if len(parts) != 2 {
			return r, core.Error("INVALID_KNOWLEDGE", rel)
		}
		if e := yaml.Unmarshal([]byte(parts[0]), &r); e != nil {
			return r, e
		}
		body = parts[1]
	}
	r.Path = rel
	r.Source = rel
	r.Hash = store.Hash(b)
	r.Body = strings.TrimSpace(body)
	r.Reason = "inherited baseline; implementation compliance has not been verified"
	if r.Kind == "spec" && !strings.HasPrefix(string(b), "---\n") && (strings.Contains(body, "未实施计划") || strings.Contains(body, "尚未实施") || strings.Contains(body, "TODO") || strings.Contains(body, "待确认")) {
		r.Status = "conflict"
		r.Reason = "mixed current rules and unresolved plans; resolve only the relevant scope"
	}
	return r, nil
}

func InheritedStatus(path string, b []byte) string {
	r, e := parseSource(path, b)
	if e != nil {
		return "conflict"
	}
	return r.Status
}

func Discover(root string) (Catalog, error) {
	c := Catalog{Schema: 1, Sources: []Source{}, Counts: map[string]int{}}
	seen := map[string]bool{}
	add := func(rel string) {
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			return
		}
		seen[rel] = true
		src := Source{Path: rel, Action: "read_failed"}
		p, e := store.SafePath(root, rel)
		if e == nil {
			var b []byte
			b, e = os.ReadFile(p)
			if e == nil {
				src.Hash = store.Hash(b)
				src.Record, e = parseSource(rel, b)
				if e == nil {
					src.Action = "connected"
					if strings.TrimSpace(src.Record.Body) == "" {
						src.Action = "ignored"
					}
					if src.Record.Status == "historical" || src.Record.Status == "superseded" {
						src.Action = "historical"
					}
					if src.Record.Status == "conflict" {
						src.Action = "conflict"
					}
					src.Record.Body = ""
				}
			}
		}
		if e != nil {
			src.Reason = e.Error()
		} else {
			src.Reason = "original file remains the sole authoritative source"
		}
		c.Sources = append(c.Sources, src)
		c.Counts[src.Action]++
	}
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", "README.md", "spec", "specs", "docs", "notes", ".agents/notes", ".trellis/spec", ".trellis/notes"} {
		p := filepath.Join(root, rel)
		st, e := os.Lstat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			add(rel)
			continue
		}
		if !st.IsDir() {
			add(rel)
			continue
		}
		e = filepath.WalkDir(p, func(p string, d fs.DirEntry, e error) error {
			relative, _ := filepath.Rel(root, p)
			if e != nil {
				add(relative)
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				add(relative)
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				add(relative)
			}
			return nil
		})
		if e != nil {
			return c, e
		}
	}
	slices.SortFunc(c.Sources, func(a, b Source) int { return strings.Compare(a.Path, b.Path) })
	return c, nil
}

func Index(root string) (Catalog, error) {
	c, e := Discover(root)
	if e != nil {
		return c, e
	}
	authorities, e := migratedAuthorities(root)
	if e != nil {
		return c, e
	}
	for i := range c.Sources {
		src := &c.Sources[i]
		if target := authorities[src.Path]; target != "" {
			c.Counts[src.Action]--
			src.Action = "retired"
			src.Target = target
			src.Reason = "explicit migrated authority: " + target + "; original retained as read-only history, never dual-written"
			c.Counts["retired"]++
		}
	}
	p, e := store.SafePath(root, ".ael/knowledge-sources.json")
	if e != nil {
		return c, e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return c, e
	}
	return c, store.Atomic(p, append(b, '\n'), 0644)
}
func readCatalog(root string) (Catalog, error) {
	var c Catalog
	p, e := store.SafePath(root, ".ael/knowledge-sources.json")
	if e != nil {
		return c, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, core.Error("KNOWLEDGE_INDEX_CORRUPT", e.Error())
	}
	if c.Schema != 1 || c.Sources == nil {
		return c, core.Error("KNOWLEDGE_INDEX_CORRUPT", "schema/sources")
	}
	return c, nil
}
func Status(root string) Health {
	c, e := readCatalog(root)
	h := Health{State: "ready", Counts: c.Counts, Sources: c.Sources}
	if os.IsNotExist(e) {
		h.State = "not_indexed"
		h.Reason = "run ./ael knowledge index; no source inventory exists"
		return h
	}
	if e != nil {
		h.State = "index_error"
		h.Reason = e.Error()
		return h
	}
	if c.Counts["read_failed"] > 0 {
		h.State = "read_failed"
		h.Reason = "source inventory contains read failures"
		return h
	}
	if c.Counts["conflict"] > 0 {
		h.State = "conflict"
		h.Reason = "scoped conflicts require resolution"
		return h
	}
	all, e := ReadAll(root)
	if e != nil {
		h.State = "load_failed"
		h.Reason = e.Error()
		return h
	}
	if len(all) == 0 {
		h.State = "empty"
		h.Reason = "inventory completed; no existing engineering rules; source-derived rules must remain proposed"
	}
	return h
}

func indexedRecords(root string, owned map[string]string) ([]Record, error) {
	c, e := readCatalog(root)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	all := []Record{}
	for _, src := range c.Sources {
		if src.Action == "retired" {
			if owned[src.Path] != src.Target {
				return nil, core.Error("KNOWLEDGE_AUTHORITY_MISSING", src.Target)
			}
			continue
		}
		if target := owned[src.Path]; target != "" {
			return nil, core.Error("KNOWLEDGE_AUTHORITY_CONFLICT", src.Path+" -> "+target+"; review migration and reindex to declare its authority")
		}

		if src.Action == "read_failed" {
			return nil, core.Error("KNOWLEDGE_SOURCE_READ_FAILED", src.Path+": "+src.Reason)
		}
		p, e := store.SafePath(root, src.Path)
		if e != nil {
			return nil, e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, core.Error("KNOWLEDGE_SOURCE_READ_FAILED", src.Path+": "+e.Error())
		}
		if store.Hash(b) != src.Hash {
			return nil, core.Error("KNOWLEDGE_SOURCE_CHANGED", src.Path+"; inspect changes then run knowledge index")
		}
		if src.Action == "ignored" {
			continue
		}
		r, e := parseSource(src.Path, b)
		if e != nil {
			return nil, e
		}
		all = append(all, r)
	}
	return all, nil
}

package migration

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Activate intentionally has no force switch: ambiguous hooks remain a review conflict.
func Activate(s *core.Service, id, digest string, ack bool) (any, error) {
	if !ack {
		return nil, core.Error("DRAIN_ACK_REQUIRED", "confirm old executors stopped before activation")
	}
	r, e := getRun(s, id)
	if e != nil {
		return nil, e
	}
	if r.State != "applied" || r.Digest != digest {
		return nil, core.Error("MIGRATION_NOT_VERIFIED", id)
	}
	if _, e = Validate(r.Plan, r.Proposal); e != nil {
		return nil, e
	}
	v, e := Verify(s, id)
	if e != nil {
		return nil, e
	}
	if !v.(map[string]any)["valid"].(bool) {
		return v, core.Error("MIGRATION_DRIFT", id)
	}
	// Recognized runtime pointers cannot be assumed to be drained merely by age.
	for _, src := range r.Plan.Sources {
		if strings.HasPrefix(src.Path, ".trellis/.runtime/") && strings.TrimSpace(src.Body) != "" && strings.TrimSpace(src.Body) != "{}" && strings.TrimSpace(src.Body) != "null" {
			return nil, core.Error("OLD_RUNTIME_UNRESOLVED", src.Path+" must be reconciled in an authorized legacy shutdown, then rescan")
		}
	}
	changes := map[string][]byte{}
	agents, e := store.SafePath(s.Root, "AGENTS.md")
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(agents)
	if e != nil {
		return nil, e
	}
	text := string(b)
	for _, markers := range [][2]string{{"<!-- TRELLIS:START -->", "<!-- TRELLIS:END -->"}, {"<!-- TRELLIS:BEGIN -->", "<!-- TRELLIS:END -->"}} {
		start := strings.Index(text, markers[0])
		if start >= 0 {
			end := strings.Index(text[start:], markers[1])
			if end < 0 {
				return nil, core.Error("UNRECOGNIZED_WORKFLOW", "unterminated Trellis managed entry")
			}
			text = text[:start] + text[start+end+len(markers[1]):]
		}
	}
	// No uncontrolled instructions mentioning Trellis may silently remain active.
	stripped := text
	if start := strings.Index(stripped, "<!-- AEL:BEGIN -->"); start >= 0 {
		end := strings.Index(stripped, "<!-- AEL:END -->")
		if end >= start {
			stripped = stripped[:start] + stripped[end+len("<!-- AEL:END -->"):]
		}
	}
	if strings.Contains(strings.ToLower(stripped), "trellis") {
		return nil, core.Error("CUSTOM_WORKFLOW_CONFLICT", "unmanaged Trellis text in AGENTS.md; review separately")
	}
	text = strings.Replace(text, "AEL project (shadow)", "AEL project (active)", 1)
	changes["AGENTS.md"] = []byte(text)
	hooksPath, e := store.SafePath(s.Root, ".claude/settings.json")
	if e != nil {
		return nil, e
	}
	if raw, err := os.ReadFile(hooksPath); err == nil {
		var settings map[string]any
		if e = json.Unmarshal(raw, &settings); e != nil {
			return nil, core.Error("CUSTOM_HOOK_CONFLICT", "unparseable .claude/settings.json")
		}
		if hooks, ok := settings["hooks"].(map[string]any); ok {
			for event, value := range hooks {
				groups, ok := value.([]any)
				if !ok {
					return nil, core.Error("CUSTOM_HOOK_CONFLICT", event)
				}
				kept := []any{}
				for _, group := range groups {
					g, ok := group.(map[string]any)
					if !ok {
						return nil, core.Error("CUSTOM_HOOK_CONFLICT", event)
					}
					list, ok := g["hooks"].([]any)
					if !ok {
						return nil, core.Error("CUSTOM_HOOK_CONFLICT", event)
					}
					newList := []any{}
					for _, hook := range list {
						h, ok := hook.(map[string]any)
						if !ok {
							return nil, core.Error("CUSTOM_HOOK_CONFLICT", event)
						}
						cmd, _ := h["command"].(string)
						if strings.Contains(cmd, ".trellis/") {
							recognized := false
							for _, known := range []string{"python3 .trellis/scripts/session-start.py", "python3 .trellis/scripts/inject-subagent-context.py", "python3 .trellis/scripts/check-subagent-stop.py"} {
								if cmd == known {
									recognized = true
								}
							}
							if !recognized {
								return nil, core.Error("CUSTOM_HOOK_CONFLICT", cmd)
							}
							continue
						}
						newList = append(newList, hook)
					}
					if len(newList) > 0 {
						g["hooks"] = newList
						kept = append(kept, g)
					}
				}
				hooks[event] = kept
			}
		}
		out, _ := json.MarshalIndent(settings, "", "  ")
		changes[".claude/settings.json"] = out
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// Other custom host integrations are not guessed or rewritten.
	for _, src := range r.Plan.Sources {
		if src.Kind == "integration" && (src.Path == "CLAUDE.md" || strings.HasPrefix(src.Path, ".codex/") || strings.HasPrefix(src.Path, ".agents/skills/") || (strings.HasPrefix(src.Path, ".claude/") && src.Path != ".claude/settings.json")) {
			return nil, core.Error("CUSTOM_WORKFLOW_CONFLICT", src.Path)
		}
	}
	c, e := bootstrap.Read(s.Root)
	if e != nil {
		return nil, e
	}
	c.Mode = "active"
	b, _ = json.MarshalIndent(c, "", "  ")
	changes[".ael/project.yaml"] = b
	// Update managed manifest to avoid a later refresh treating activation as a user edit.
	manifestPath, e := store.SafePath(s.Root, ".ael/assets.json")
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(manifestPath)
	if e != nil {
		return nil, e
	}
	var manifest map[string]string
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return nil, e
	}
	manifest[".ael/project.yaml"] = store.Hash(b)
	start := strings.Index(text, "<!-- AEL:BEGIN -->")
	end := strings.Index(text, "<!-- AEL:END -->")
	if start >= 0 && end >= start {
		manifest["AGENTS:block"] = store.Hash([]byte(text[start : end+len("<!-- AEL:END -->")]))
	}
	b, _ = json.MarshalIndent(manifest, "", "  ")
	changes[".ael/assets.json"] = b
	for rel, data := range changes {
		path, e := store.SafePath(s.Root, rel)
		if e != nil {
			return nil, e
		}
		old, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		backup := filepath.Join(s.Store.Dir, "migrations", id, "activate-"+store.Hash([]byte(rel))[:16])
		if e = store.Atomic(backup, old, 0600); e != nil {
			return nil, e
		}
		r.Changes = append(r.Changes, Change{Target: rel, Before: store.Hash(old), After: store.Hash(data), Backup: backup, SourceID: "activation:" + rel})
	}
	if e = saveRun(s, r); e != nil {
		return nil, e
	}
	e = s.Store.Write(func(tx *store.Tx) error {
		for rel, data := range changes {
			p, e := store.SafePath(s.Root, rel)
			if e != nil {
				return e
			}
			if e = tx.File(p, data); e != nil {
				return e
			}
		}
		return tx.Event("", "workflow_activated", map[string]string{"migration": id, "digest": digest})
	})
	if e == nil {
		e = s.Store.Flush()
	}
	if e != nil {
		return nil, e
	}
	s.Active = true
	return map[string]any{"mode": "active", "migration": id, "changed": changes, "legacy_sources": "retained untouched"}, nil
}

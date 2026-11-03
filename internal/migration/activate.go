package migration

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type activationIntent struct {
	Writes  map[string][]byte `json:"writes"`
	Deletes []string          `json:"deletes"`
}

// Activate recognizes owned controller assets; unfamiliar commands remain a conflict.
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
	if r.Activated {
		v, e := Verify(s, id)
		if e != nil {
			return nil, e
		}
		if !v.(map[string]any)["valid"].(bool) {
			_ = suspendActivation(s, &r)
			return v, core.Error("ACTIVATION_DRIFT", id)
		}
		return map[string]any{"mode": "active", "migration": id, "already_active": true}, nil
	}
	var active int
	if e = s.Store.DB.QueryRow("SELECT count(*) FROM tasks,json_each(tasks.body,'$.attempts') a WHERE json_extract(a.value,'$.status') IN ('running','interrupted')").Scan(&active); e != nil {
		return nil, e
	}
	if active > 0 {
		return nil, core.Error("EXECUTION_NOT_DRAINED", "AEL attempts must stop before controller cutover")
	}
	intentPath := filepath.Join(s.Store.Dir, "migrations", id, "activation-intent.json")
	var intent activationIntent
	if !r.Activating {
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
		intent, e = prepareActivation(s, r)
		if e != nil {
			return nil, e
		}
		paths := []string{}
		for rel := range intent.Writes {
			paths = append(paths, rel)
		}
		paths = append(paths, intent.Deletes...)
		sort.Strings(paths)
		deleted := map[string]bool{}
		for _, rel := range intent.Deletes {
			deleted[rel] = true
		}
		for _, rel := range paths {
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
			st, e := os.Stat(path)
			if e != nil {
				return nil, e
			}
			after := ""
			if !deleted[rel] {
				after = store.Hash(intent.Writes[rel])
			}
			r.Changes = append(r.Changes, Change{Target: rel, Before: store.Hash(old), After: after, Backup: backup, SourceID: "activation:" + rel, Deleted: deleted[rel], Mode: uint32(st.Mode().Perm())})
		}
		b, _ := json.Marshal(intent)
		if e = store.Atomic(intentPath, b, 0600); e != nil {
			return nil, e
		}
		r.ActivationHash = store.Hash(b)
		r.Activating = true
		if e = saveRun(s, r); e != nil {
			return nil, e
		}
	} else {
		raw, e := os.ReadFile(intentPath)
		if e != nil {
			return nil, e
		}
		if store.Hash(raw) != r.ActivationHash {
			return nil, core.Error("ACTIVATION_INTENT_CHANGED", id)
		}
		if e = json.Unmarshal(raw, &intent); e != nil {
			return nil, e
		}
	}
	if fresh := newDiscoveryIssues(s, r); len(fresh) > 0 {
		_ = suspendActivation(s, &r)
		return nil, core.Error("DISCOVERY_CHANGED", strings.Join(fresh, "; "))
	}
	// Each owned path must still be its backed-up version or the exact installed version.
	for _, c := range r.Changes {
		if !strings.HasPrefix(c.SourceID, "activation:") {
			continue
		}
		path, e := store.SafePath(s.Root, c.Target)
		if e != nil {
			return nil, e
		}
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) && c.Deleted {
			continue
		}
		if e != nil {
			return nil, e
		}
		h := store.Hash(b)
		if h != c.Before && h != c.After && !(c.Target == ".ael/project.yaml" && r.PausedConfigHash != "" && h == r.PausedConfigHash) {
			_ = suspendActivation(s, &r)
			return nil, core.Error("ACTIVATION_FILE_CONFLICT", c.Target)
		}
	}
	e = s.Store.Write(func(tx *store.Tx) error {
		paths := []string{}
		for rel := range intent.Writes {
			paths = append(paths, rel)
		}
		sort.Strings(paths)
		for _, rel := range paths {
			p, e := store.SafePath(s.Root, rel)
			if e != nil {
				return e
			}
			if e = tx.File(p, intent.Writes[rel]); e != nil {
				return e
			}
		}
		for _, rel := range intent.Deletes {
			p, e := store.SafePath(s.Root, rel)
			if e != nil {
				return e
			}
			if e = tx.DeleteFile(p); e != nil {
				return e
			}
		}
		return nil
	})
	if e == nil {
		e = s.Store.Flush()
	}
	if e != nil {
		return nil, e
	}
	for _, rel := range intent.Deletes {
		if strings.HasPrefix(rel, ".agents/skills/mini-trellis-") {
			dir := filepath.Dir(filepath.Join(s.Root, rel))
			limit := filepath.Join(s.Root, ".agents/skills")
			for dir != limit && strings.HasPrefix(dir, limit+string(os.PathSeparator)) {
				if e := os.Remove(dir); e != nil {
					break
				}
				dir = filepath.Dir(dir)
			}
		}
	}
	v, e := Verify(s, id)
	if e != nil {
		return nil, e
	}
	if !v.(map[string]any)["valid"].(bool) {
		_ = suspendActivation(s, &r)
		return v, core.Error("ACTIVATION_VERIFY_FAILED", id)
	}
	r.Activated = true
	r.Activating = false
	if e = saveRun(s, r); e != nil {
		return nil, e
	}
	s.Active = true
	e = s.Store.Write(func(tx *store.Tx) error {
		return tx.Event("", "workflow_activated", map[string]any{"migration": id, "digest": digest, "retired": intent.Deletes})
	})
	return map[string]any{"mode": "active", "migration": id, "changed": intent.Writes, "retired": intent.Deletes, "legacy_sources": "retained untouched; owned discovery assets backed up and retired"}, e
}

func prepareActivation(s *core.Service, r Run) (activationIntent, error) {
	out := activationIntent{Writes: map[string][]byte{}, Deletes: []string{}}
	sources := map[string]Source{}
	for _, src := range r.Plan.Sources {
		sources[src.Path] = src
		if strings.HasPrefix(src.Path, ".trellis/.runtime/") && !idleLegacyRuntime(src) {
			return out, core.Error("OLD_RUNTIME_UNRESOLVED", src.Path+" has active or unknown engineering execution state")
		}
	}
	agents, e := store.SafePath(s.Root, "AGENTS.md")
	if e != nil {
		return out, e
	}
	raw, e := os.ReadFile(agents)
	if e != nil {
		return out, e
	}
	text := string(raw)
	for _, markers := range [][2]string{{"<!-- TRELLIS:START -->", "<!-- TRELLIS:END -->"}, {"<!-- TRELLIS:BEGIN -->", "<!-- TRELLIS:END -->"}} {
		start := strings.Index(text, markers[0])
		if start >= 0 {
			end := strings.Index(text[start:], markers[1])
			if end < 0 {
				return out, core.Error("UNRECOGNIZED_WORKFLOW", "unterminated managed entry")
			}
			text = text[:start] + text[start+end+len(markers[1]):]
		}
	}
	// Rewrite only exact document links into reviewed imported records; owner prose stays intact.
	text = linkPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := linkPattern.FindStringSubmatch(match)
		ref, fragment, _ := strings.Cut(parts[1], "#")
		key := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(ref)), "./")
		if src, ok := sources[key]; ok && src.Target != "" {
			target := src.Target
			if fragment != "" {
				target += "#" + fragment
			}
			return "](" + target + ")"
		}
		return match
	})
	stripped := text
	if start := strings.Index(stripped, "<!-- AEL:BEGIN -->"); start >= 0 {
		end := strings.Index(stripped, "<!-- AEL:END -->")
		if end >= start {
			stripped = stripped[:start] + stripped[end+len("<!-- AEL:END -->"):]
		}
	}
	if strings.Contains(strings.ToLower(stripped), "trellis") {
		return out, core.Error("CUSTOM_WORKFLOW_CONFLICT", "unmanaged Trellis instructions in AGENTS.md")
	}
	text = strings.Replace(text, "AEL project (shadow)", "AEL project (active)", 1)
	out.Writes["AGENTS.md"] = []byte(text)
	for _, rel := range []string{".claude/settings.json", ".claude/settings.local.json", ".codex/hooks.json"} {
		src, ok := sources[rel]
		if !ok {
			continue
		}
		replacement, changed, e := disableKnownHooks(src.Body, sources)
		if e != nil {
			return out, e
		}
		if changed {
			out.Writes[rel] = replacement
		}
	}
	knownSkills := map[string]bool{"mini-trellis-remember": true, "mini-trellis-update-spec": true, "mini-trellis-session-insight": true}
	owned := map[string]bool{}
	for _, src := range r.Plan.Sources {
		parts := strings.Split(src.Path, "/")
		if len(parts) != 4 || parts[0] != ".agents" || parts[1] != "skills" || parts[3] != "SKILL.md" || !knownSkills[parts[2]] {
			continue
		}
		front := strings.SplitN(strings.TrimPrefix(src.Body, "---\n"), "\n---", 2)
		var metadata struct {
			Name string `yaml:"name"`
		}
		if len(front) != 2 || yaml.Unmarshal([]byte(front[0]), &metadata) != nil || metadata.Name != parts[2] {
			return out, core.Error("CUSTOM_SKILL_CONFLICT", src.Path)
		}
		owned[".agents/skills/"+parts[2]+"/"] = true
	}
	for _, src := range r.Plan.Sources {
		if strings.HasPrefix(src.Path, ".agents/skills/") {
			recognized := false
			for prefix := range owned {
				if strings.HasPrefix(src.Path, prefix) {
					out.Deletes = append(out.Deletes, src.Path)
					recognized = true
					break
				}
			}
			if !recognized {
				return out, core.Error("CUSTOM_SKILL_CONFLICT", src.Path)
			}
		}
		if src.Kind != "integration" {
			continue
		}
		switch {
		case src.Path == ".codex/hooks/session-start.py":
			if !strings.Contains(src.Body, "TRELLIS_DISABLE_HOOKS") || !strings.Contains(strings.ToLower(src.Body), "trellis") {
				return out, core.Error("CUSTOM_HOOK_CONFLICT", src.Path)
			}
			out.Deletes = append(out.Deletes, src.Path)
		case src.Path == ".codex/config.toml":
			for _, line := range strings.Split(src.Body, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") && strings.Contains(strings.ToLower(line), "trellis") {
					return out, core.Error("CUSTOM_WORKFLOW_CONFLICT", src.Path)
				}
			}
		case src.Path == "CLAUDE.md":
			return out, core.Error("CUSTOM_WORKFLOW_CONFLICT", src.Path)
		case strings.HasPrefix(src.Path, ".codex/") && src.Path != ".codex/hooks.json":
			return out, core.Error("CUSTOM_WORKFLOW_CONFLICT", src.Path)
		case strings.HasPrefix(src.Path, ".claude/") && src.Path != ".claude/settings.json" && src.Path != ".claude/settings.local.json":
			return out, core.Error("CUSTOM_WORKFLOW_CONFLICT", src.Path)
		}
	}
	sort.Strings(out.Deletes)
	config, e := bootstrap.Read(s.Root)
	if e != nil {
		return out, e
	}
	config.Mode = "active"
	config.Version = bootstrap.Version
	b, _ := json.MarshalIndent(config, "", "  ")
	out.Writes[".ael/project.yaml"] = b
	manifestPath, e := store.SafePath(s.Root, ".ael/assets.json")
	if e != nil {
		return out, e
	}
	raw, e = os.ReadFile(manifestPath)
	if e != nil {
		return out, e
	}
	var manifest map[string]string
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return out, e
	}
	manifest[".ael/project.yaml"] = store.Hash(b)
	start := strings.Index(text, "<!-- AEL:BEGIN -->")
	end := strings.Index(text, "<!-- AEL:END -->")
	if start < 0 || end < start {
		return out, core.Error("AEL_ENTRY_MISSING", s.Root)
	}
	manifest["AGENTS:block"] = store.Hash([]byte(text[start : end+len("<!-- AEL:END -->")]))
	b, _ = json.MarshalIndent(manifest, "", "  ")
	out.Writes[".ael/assets.json"] = b
	return out, nil
}
func idleLegacyRuntime(src Source) bool {
	if src.Action == "ignore" {
		return true
	}
	body := strings.TrimSpace(src.Body)
	if body == "" {
		return src.Size == 0
	}
	if body == "{}" || body == "null" {
		return true
	}
	if strings.HasPrefix(filepath.Base(src.Path), "update-check-") && strings.HasSuffix(src.Path, ".marker") && len(body) <= 64 {
		return true
	}
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return false
	}
	for key := range m {
		if key != "platform" && key != "last_seen_at" && key != "current_task" && key != "current_run" {
			return false
		}
	}
	task, hasTask := m["current_task"]
	run, hasRun := m["current_run"]
	return hasTask && hasRun && task == nil && run == nil
}
func disableKnownHooks(body string, sources map[string]Source) ([]byte, bool, error) {
	var settings map[string]any
	if e := json.Unmarshal([]byte(body), &settings); e != nil {
		return nil, false, core.Error("CUSTOM_HOOK_CONFLICT", "invalid hook configuration")
	}
	value, ok := settings["hooks"]
	if !ok {
		return nil, false, nil
	}
	hooks, ok := value.(map[string]any)
	if !ok {
		return nil, false, core.Error("CUSTOM_HOOK_CONFLICT", "hooks must be an object")
	}
	changed := false
	for event, value := range hooks {
		groups, ok := value.([]any)
		if !ok {
			return nil, false, core.Error("CUSTOM_HOOK_CONFLICT", event)
		}
		kept := []any{}
		for _, group := range groups {
			g, ok := group.(map[string]any)
			if !ok {
				return nil, false, core.Error("CUSTOM_HOOK_CONFLICT", event)
			}
			list, ok := g["hooks"].([]any)
			if !ok {
				return nil, false, core.Error("CUSTOM_HOOK_CONFLICT", event)
			}
			next := []any{}
			for _, entry := range list {
				h, ok := entry.(map[string]any)
				if !ok {
					return nil, false, core.Error("CUSTOM_HOOK_CONFLICT", event)
				}
				command, _ := h["command"].(string)
				known := false
				for _, legacy := range []string{"python3 .trellis/scripts/session-start.py", "python3 .trellis/scripts/inject-subagent-context.py", "python3 .trellis/scripts/check-subagent-stop.py"} {
					if command == legacy {
						known = true
					}
				}
				if command == "python3 -X utf8 .codex/hooks/session-start.py" || command == "python3 .codex/hooks/session-start.py" {
					src, ok := sources[".codex/hooks/session-start.py"]
					known = ok && strings.Contains(src.Body, "TRELLIS_DISABLE_HOOKS") && strings.Contains(strings.ToLower(src.Body), "trellis")
				}
				if known {
					changed = true
					continue
				}
				if strings.Contains(strings.ToLower(command), "trellis") {
					return nil, false, core.Error("CUSTOM_HOOK_CONFLICT", command)
				}
				next = append(next, entry)
			}
			if len(next) > 0 {
				g["hooks"] = next
				kept = append(kept, g)
			}
		}
		hooks[event] = kept
	}
	if !changed {
		return nil, false, nil
	}
	b, e := json.MarshalIndent(settings, "", "  ")
	return b, true, e
}

func discoveryPath(path string) bool {
	return strings.HasPrefix(path, ".codex/") || strings.HasPrefix(path, ".claude/") || strings.HasPrefix(path, ".agents/skills/") || path == "CLAUDE.md"
}
func newDiscoveryIssues(s *core.Service, r Run) []string {
	current, e := Scan(s.Root)
	if e != nil {
		return []string{e.Error()}
	}
	known := map[string]bool{}
	for _, src := range r.Plan.Sources {
		known[src.Path] = true
	}
	issues := []string{}
	for _, src := range current.Sources {
		if src.Action != "ignore" && discoveryPath(src.Path) && !known[src.Path] {
			issues = append(issues, "new discovery entry: "+src.Path)
		}
	}
	return issues
}
func discoveryIssues(s *core.Service, r Run) []string {
	issues := newDiscoveryIssues(s, r)
	sources := map[string]Source{}
	for _, src := range r.Plan.Sources {
		sources[src.Path] = src
	}
	for _, rel := range []string{".codex/hooks.json", ".claude/settings.json", ".claude/settings.local.json"} {
		p, e := store.SafePath(s.Root, rel)
		if e != nil {
			issues = append(issues, e.Error())
			continue
		}
		b, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			issues = append(issues, e.Error())
			continue
		}
		_, changed, e := disableKnownHooks(string(b), sources)
		if e != nil || changed {
			issues = append(issues, "legacy or unknown hook still registered: "+rel)
		}
	}
	for _, name := range []string{"mini-trellis-remember", "mini-trellis-update-spec", "mini-trellis-session-insight"} {
		p := filepath.Join(s.Root, ".agents/skills", name, "SKILL.md")
		if _, e := os.Lstat(p); !os.IsNotExist(e) {
			issues = append(issues, "legacy skill still discoverable: "+name)
		}
	}
	return issues
}

// An incomplete cutover never leaves AEL active alongside a discovered old controller.
func suspendActivation(s *core.Service, r *Run) error {
	path, e := store.SafePath(s.Root, ".ael/project.yaml")
	if e != nil {
		return e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	known := false
	for _, c := range r.Changes {
		if c.Target == ".ael/project.yaml" && (store.Hash(b) == c.Before || store.Hash(b) == c.After) {
			known = true
		}
	}
	var config map[string]any
	if e = json.Unmarshal(b, &config); e != nil {
		return e
	}
	config["mode"] = "shadow"
	next, _ := json.MarshalIndent(config, "", "  ")
	current, e := store.HashFile(path)
	if e != nil {
		return e
	}
	if current != store.Hash(b) {
		return core.Error("ACTIVATION_FILE_CONFLICT", "project config changed while suspending")
	}
	if e = store.Atomic(path, next, 0644); e != nil {
		return e
	}
	if known {
		r.PausedConfigHash = store.Hash(next)
	} else {
		r.PausedConfigHash = ""
	}
	r.Activated = false
	r.Activating = true
	s.Active = false
	return saveRun(s, *r)
}

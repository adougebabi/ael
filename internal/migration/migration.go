package migration

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

type Source struct {
	ID        string         `json:"id"`
	Path      string         `json:"path"`
	Size      int64          `json:"size"`
	Hash      string         `json:"hash"`
	Kind      string         `json:"kind"`
	OldID     string         `json:"old_id"`
	OldStatus string         `json:"old_status"`
	TaskDir   string         `json:"task_dir"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Risks     []string       `json:"risks"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Body      string         `json:"body"`
}
type Inventory struct {
	ID       string    `json:"id"`
	Project  string    `json:"project"`
	At       time.Time `json:"at"`
	Sources  []Source  `json:"sources"`
	Warnings []string  `json:"warnings"`
}
type Plan struct {
	ID      string            `json:"id"`
	Digest  string            `json:"digest"`
	Project string            `json:"project"`
	Sources []Source          `json:"sources"`
	Targets map[string]string `json:"target_hashes"`
	Created time.Time         `json:"created"`
}
type Suggestion struct {
	SupersededBy string `json:"superseded_by,omitempty"`
	SourceID     string `json:"source_id"`
	SourceHash   string `json:"source_hash"`
	Quote        string `json:"quote"`
	Action       string `json:"action"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	Target       string `json:"target_ref"`
	Reason       string `json:"reason"`
	Pending      string `json:"pending"`
}
type Proposal struct {
	PlanID string       `json:"plan_id"`
	Items  []Suggestion `json:"items"`
}
type Change struct {
	PriorTask    *core.Task `json:"prior_task,omitempty"`
	PriorOutcome []byte     `json:"prior_outcome,omitempty"`
	Target       string     `json:"target"`
	Before       string     `json:"before_hash"`
	After        string     `json:"after_hash"`
	Backup       string     `json:"backup"`
	SourceID     string     `json:"source_id"`
	TaskID       string     `json:"task_id"`
}
type Run struct {
	ID       string    `json:"id"`
	State    string    `json:"state"`
	Count    int       `json:"count"`
	Plan     Plan      `json:"plan"`
	Proposal *Proposal `json:"proposal,omitempty"`
	Changes  []Change  `json:"changes"`
	Warnings []string  `json:"warnings"`
	Applied  []string  `json:"applied"`
	Digest   string    `json:"approved_digest"`
}

func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return store.Atomic(path, b, 0644)
}
func ReadJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func Scan(root string) (Inventory, error) {
	root, e := bootstrap.Canonical(root)
	inv := Inventory{Project: root, At: time.Now().UTC(), Sources: []Source{}, Warnings: []string{}}
	if e != nil {
		return inv, e
	}
	roots := []string{".trellis/spec", ".trellis/tasks", ".trellis/research", ".trellis/workspace", ".trellis/workflow.md", ".trellis/scripts", ".trellis/.runtime", ".agents/notes", "notes", "docs/adr", "AGENTS.md", "CLAUDE.md", ".claude", ".codex", ".agents/skills"}
	seen := map[string]bool{}
	for _, sourceRoot := range roots {
		p, e := store.SafePath(root, sourceRoot)
		if e != nil {
			inv.Warnings = append(inv.Warnings, sourceRoot+": "+e.Error())
			continue
		}
		if _, e = os.Lstat(p); os.IsNotExist(e) {
			continue
		}
		e = filepath.WalkDir(p, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				return nil
			}
			if seen[rel] {
				return nil
			}
			seen[rel] = true
			src := Source{Path: rel, ID: store.Hash([]byte(rel))[:24], Kind: "reference", Action: "historical", Risks: []string{}}
			if _, e := store.SafePath(root, rel); e != nil {
				src.Action = "ignore"
				src.Risks = append(src.Risks, e.Error())
				inv.Sources = append(inv.Sources, src)
				return nil
			}
			st, e := d.Info()
			if e != nil {
				return e
			}
			src.Size = st.Size()
			if st.Size() > 16<<20 {
				src.Hash, e = store.HashFile(path)
				if e != nil {
					return e
				}
				src.Action = "preserve"
				src.Risks = append(src.Risks, "large file: explicit review required")
				inv.Sources = append(inv.Sources, src)
				return nil
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			src.Hash = store.Hash(b)
			src.Body = string(b)
			if strings.HasPrefix(rel, ".agents/skills/ael-") {
				return nil
			}
			if (strings.HasPrefix(rel, ".agents/skills/") || strings.HasPrefix(rel, ".claude/") || strings.HasPrefix(rel, ".codex/") || rel == "AGENTS.md" || rel == "CLAUDE.md") && !strings.Contains(strings.ToLower(string(b)), "trellis") {
				return nil
			}
			switch {
			case strings.HasPrefix(rel, ".trellis/spec/"):
				src.Kind = "spec"
				src.Action = "pending"
				src.Risks = append(src.Risks, "spec baseline requires explicit classification; mixed plans are not current facts")
			case strings.HasPrefix(rel, ".trellis/research/"):
				src.Kind = "research"
			case strings.HasPrefix(rel, "docs/adr/"):
				src.Kind = "decision"
			case strings.HasPrefix(rel, ".trellis/scripts/") || strings.HasPrefix(rel, ".trellis/.runtime/") || rel == ".trellis/workflow.md" || strings.HasPrefix(rel, ".claude/") || rel == "AGENTS.md" || rel == "CLAUDE.md" || strings.HasPrefix(rel, ".codex/"):
				src.Kind = "integration"
				src.Action = "preserve"
				src.Risks = append(src.Risks, "never execute or activate imported integration")
			}
			if strings.HasPrefix(rel, ".trellis/tasks/") {
				dir := filepath.Dir(path)
				for strings.HasPrefix(dir, filepath.Join(root, ".trellis/tasks")) && dir != filepath.Join(root, ".trellis/tasks") {
					taskJSON := filepath.Join(dir, "task.json")
					if _, e := store.SafePath(root, strings.TrimPrefix(taskJSON, root+string(os.PathSeparator))); e == nil {
						if raw, e := os.ReadFile(taskJSON); e == nil {
							var m map[string]any
							if json.Unmarshal(raw, &m) == nil {
								src.OldID = stringField(m, "id")
								if src.OldID == "" {
									src.OldID = filepath.Base(dir)
								}
								src.OldStatus = stringField(m, "status")
								src.TaskDir, _ = filepath.Rel(root, dir)
								relative, _ := filepath.Rel(dir, path)
								src.ID = store.Hash([]byte("task:" + src.OldID + "/" + filepath.ToSlash(relative)))[:24]
								if relative == "task.json" {
									src.Kind = "task"
									src.Metadata = m
									for key := range m {
										if !slices.Contains([]string{"id", "name", "title", "status", "parent", "parent_id", "children", "dependencies", "depends_on", "acceptance", "acceptance_criteria", "description", "created_at", "updated_at", "completed_at", "completedAt", "finished_at"}, key) {
											src.Risks = append(src.Risks, "unknown legacy field retained: "+key)
										}
									}
								}
								break
							}
						}
					}
					dir = filepath.Dir(dir)
				}
				if filepath.Base(path) == "task.json" && src.Metadata == nil {
					src.Action = "pending"
					src.Risks = append(src.Risks, "broken task JSON or missing identity")
				}
				if strings.HasSuffix(path, ".jsonl") {
					for i, line := range strings.Split(string(b), "\n") {
						if strings.TrimSpace(line) == "" {
							continue
						}
						var m map[string]any
						if json.Unmarshal([]byte(line), &m) != nil {
							src.Risks = append(src.Risks, fmt.Sprintf("broken context JSONL line %d", i+1))
						} else {
							for _, key := range []string{"file", "path"} {
								if ref, ok := m[key].(string); ok {
									p, e := store.SafePath(root, ref)
									if e != nil {
										src.Risks = append(src.Risks, "unsafe context reference: "+ref)
									} else if _, e = os.Stat(p); e != nil {
										src.Risks = append(src.Risks, "missing context reference: "+ref)
									}
								}
							}
						}
					}
				}
			}
			if src.Kind == "task" {
				src.Target = ".ael/work-items/LEG-" + store.Hash([]byte(src.OldID))[:20] + ".md"
				switch src.OldStatus {
				case "done", "completed":
					src.Action = "historical"
				case "pending", "todo", "in_progress", "planning", "open":
					src.Action = "import"
				case "cancelled", "canceled":
					src.Action = "historical"
				default:
					src.Action = "pending"
					src.Risks = append(src.Risks, "ambiguous task status: "+src.OldStatus)
				}
			} else if src.Kind != "integration" {
				folder := src.Kind
				if folder == "reference" {
					folder = "research"
				}
				src.Target = ".ael/knowledge/" + folder + "/legacy-" + src.ID + ".md"
			}
			inv.Sources = append(inv.Sources, src)
			return nil
		})
		if e != nil {
			return inv, e
		}
	}
	sort.Slice(inv.Sources, func(i, j int) bool { return inv.Sources[i].Path < inv.Sources[j].Path })
	ids := map[string]bool{}
	for i := range inv.Sources {
		src := &inv.Sources[i]
		if ids[src.ID] {
			src.Action = "pending"
			src.Risks = append(src.Risks, "duplicate stable legacy identity")
		}
		ids[src.ID] = true
	}
	b, _ := json.Marshal(inv.Sources)
	inv.ID = "migration-" + store.Hash(b)[:20]
	return inv, nil
}
func stringField(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func MakePlan(inv Inventory) (Plan, error) {
	p := Plan{ID: inv.ID, Project: inv.Project, Sources: inv.Sources, Targets: map[string]string{}, Created: time.Now().UTC()}
	for _, s := range p.Sources {
		if s.Target == "" {
			continue
		}
		path, e := store.SafePath(p.Project, s.Target)
		if e != nil {
			return p, e
		}
		b, e := os.ReadFile(path)
		if e == nil {
			p.Targets[s.Target] = store.Hash(b)
		} else if os.IsNotExist(e) {
			p.Targets[s.Target] = ""
		} else {
			return p, e
		}
	}
	p.Digest = digest(p, nil)
	return p, nil
}
func digest(p Plan, proposal *Proposal) string {
	p.Digest = ""
	b, _ := json.Marshal(struct {
		P        Plan
		Proposal *Proposal
	}{p, proposal})
	return store.Hash(b)
}
func Validate(p Plan, proposal *Proposal) (string, error) {
	if p.Digest != digest(p, nil) {
		return "", core.Error("PLAN_TAMPERED", p.ID)
	}
	ids := map[string]Source{}
	targets := map[string]bool{}
	for _, s := range p.Sources {
		if _, ok := ids[s.ID]; ok {
			return "", core.Error("DUPLICATE_SOURCE", s.ID)
		}
		ids[s.ID] = s
		if s.Target != "" {
			if targets[s.Target] {
				return "", core.Error("DUPLICATE_TARGET", s.Target)
			}
			targets[s.Target] = true
			if !strings.HasPrefix(s.Target, ".ael/knowledge/") && !strings.HasPrefix(s.Target, ".ael/work-items/") {
				return "", core.Error("UNSAFE_TARGET", s.Target)
			}
			if _, e := store.SafePath(p.Project, s.Target); e != nil {
				return "", e
			}
		}
		if s.Action == "ignore" {
			continue
		}
		path, e := store.SafePath(p.Project, s.Path)
		if e != nil {
			return "", e
		}
		hash, e := store.HashFile(path)
		if e != nil {
			return "", e
		}
		if s.Hash != "" && hash != s.Hash {
			return "", core.Error("SOURCE_CHANGED", s.Path)
		}
		if s.Body != "" && store.Hash([]byte(s.Body)) != s.Hash {
			return "", core.Error("SOURCE_BODY_MISMATCH", s.Path)
		}
	}
	if proposal != nil {
		if proposal.PlanID != p.ID {
			return "", core.Error("PROPOSAL_PLAN_MISMATCH", p.ID)
		}
		seen := map[string]bool{}
		for _, x := range proposal.Items {
			s, ok := ids[x.SourceID]
			if !ok || seen[x.SourceID] || s.Hash != x.SourceHash || x.Quote == "" || !strings.Contains(s.Body, x.Quote) || x.Reason == "" {
				return "", core.Error("INVALID_PROPOSAL_SOURCE", x.SourceID)
			}
			seen[x.SourceID] = true
			if x.Action == "task_status" && s.Kind == "task" {
				if x.Kind != "task" || x.Target != s.Target || !slices.Contains([]string{"open", "historical_completed", "cancelled", "conflict"}, x.Status) {
					return "", core.Error("INVALID_TASK_PROPOSAL", x.SourceID)
				}
				if x.Status == "historical_completed" && s.OldStatus != "done" && s.OldStatus != "completed" {
					return "", core.Error("CANNOT_INVENT_HISTORICAL_COMPLETION", x.SourceID)
				}
				continue
			}
			if x.Action != "classify" || s.Kind == "task" || s.Kind == "integration" || x.Target != s.Target || !slices.Contains([]string{"spec", "decision", "research", "reference"}, x.Kind) || !slices.Contains([]string{"current", "historical", "conflict"}, x.Status) {
				return "", core.Error("INVALID_PROPOSAL_ACTION", x.SourceID)
			}
			if x.SupersededBy != "" && x.Status == "current" {
				return "", core.Error("SUPERSESSION_STATUS_CONFLICT", x.SourceID)
			}
			if x.Pending != "" && x.Status == "current" {
				return "", core.Error("UNRESOLVED_PROPOSAL", x.SourceID)
			}
		}
	}
	graph := map[string]string{}
	existing, e := knowledge.ReadAll(p.Project)
	if e != nil {
		return "", e
	}
	for _, r := range existing {
		graph[r.ID] = r.SupersededBy
	}
	for _, source := range p.Sources {
		if source.Target != "" && source.Kind != "task" {
			graph["legacy-"+source.ID] = ""
		}
	}
	if proposal != nil {
		for _, x := range proposal.Items {
			if x.Action == "classify" {
				graph["legacy-"+x.SourceID] = x.SupersededBy
			}
		}
	}
	for id, next := range graph {
		seen := map[string]bool{id: true}
		for next != "" {
			if seen[next] {
				return "", core.Error("SUPERSESSION_CYCLE", next)
			}
			seen[next] = true
			ref, ok := graph[next]
			if !ok {
				return "", core.Error("SUPERSESSION_MISSING", next)
			}
			next = ref
		}
	}
	return digest(p, proposal), nil
}
func SaveInventory(inv Inventory, out string) error {
	if filepath.Clean(out) == filepath.Clean(inv.Project) || strings.HasPrefix(filepath.Clean(out), filepath.Join(inv.Project, ".trellis")) {
		return core.Error("REPORT_OVER_SOURCE", out)
	}
	return writeJSON(filepath.Join(out, "inventory.json"), inv)
}
func SavePlan(p Plan, out string) error { return writeJSON(filepath.Join(out, "plan.json"), p) }
func getRun(s *core.Service, id string) (Run, error) {
	var r Run
	var b string
	e := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", "migration:"+id).Scan(&b)
	if e == nil {
		e = json.Unmarshal([]byte(b), &r)
	}
	return r, e
}
func saveRun(s *core.Service, r Run) error {
	b, _ := json.Marshal(r)
	_, e := s.Store.DB.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", "migration:"+r.ID, string(b))
	return e
}
func List(s *core.Service) ([]Run, error) {
	rows, e := s.Store.DB.Query("SELECT body FROM metadata WHERE key LIKE 'migration:%' ORDER BY key")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	all := []Run{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var r Run
		if e = json.Unmarshal([]byte(b), &r); e != nil {
			return nil, e
		}
		all = append(all, r)
	}
	return all, rows.Err()
}

var linkPattern = regexp.MustCompile(`\]\(([^)]+)\)`)

func mapLinks(src Source, all []Source) (string, []string) {
	warnings := []string{}
	mapping := map[string]string{}
	for _, s := range all {
		if s.Target != "" {
			mapping[s.Path] = s.Target
		}
	}
	body := linkPattern.ReplaceAllStringFunc(src.Body, func(match string) string {
		raw := linkPattern.FindStringSubmatch(match)[1]
		if strings.Contains(raw, "://") || strings.HasPrefix(raw, "#") {
			return match
		}
		ref, frag, _ := strings.Cut(raw, "#")
		old := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(src.Path), ref)))
		if target, ok := mapping[old]; ok {
			relative, e := filepath.Rel(filepath.Dir(src.Target), target)
			if e == nil {
				if frag != "" {
					relative += "#" + frag
				}
				return "](" + filepath.ToSlash(relative) + ")"
			}
		}
		warnings = append(warnings, "unresolved link: "+src.Path+" -> "+raw)
		return match
	})
	return body, warnings
}
func importedTask(src Source, all []Source) core.Task {
	m := map[string]any{}
	rawMetadata, _ := json.Marshal(src.Metadata)
	_ = json.Unmarshal(rawMetadata, &m)
	title := stringField(m, "name")
	if title == "" {
		title = stringField(m, "title")
	}
	if title == "" {
		title = src.OldID
	}
	id := "LEG-" + store.Hash([]byte(src.OldID))[:20]
	d := core.Definition{ID: id, Title: title, Type: "chore", Goal: "verified", Source: src.Path, Acceptance: []string{"导入资料待重新定义验收"}, Revision: 1, MaxRetries: 3, MaxSeconds: 3600}
	known := map[string]string{}
	for _, x := range all {
		if x.Kind == "task" {
			known[x.OldID] = "LEG-" + store.Hash([]byte(x.OldID))[:20]
		}
	}
	missing := []string{}
	if parent := stringField(m, "parent"); parent != "" {
		d.Parent = known[parent]
		if d.Parent == "" {
			missing = append(missing, "unresolved parent: "+parent)
		}
	}
	for _, key := range []string{"dependencies", "depends_on"} {
		if deps, ok := m[key].([]any); ok {
			for _, dep := range deps {
				if old, ok := dep.(string); ok {
					if mapped, ok := known[old]; ok {
						d.Dependencies = append(d.Dependencies, mapped)
					} else {
						missing = append(missing, "unresolved dependency: "+old)
					}
				}
			}
		}
	}
	references := map[string]string{}
	for _, x := range all {
		if x.Kind != "task" && x.Target != "" {
			references[x.Path] = "legacy-" + x.ID
		}
	}
	for _, x := range all {
		if x.TaskDir == src.TaskDir && strings.HasSuffix(x.Path, ".jsonl") {
			for _, line := range strings.Split(x.Body, "\n") {
				var ref map[string]any
				if json.Unmarshal([]byte(line), &ref) == nil {
					for _, key := range []string{"file", "path"} {
						if p, ok := ref[key].(string); ok {
							if id, ok := references[p]; ok && !slices.Contains(d.Knowledge, id) {
								d.Knowledge = append(d.Knowledge, id)
							}
						}
					}
				}
			}
		}
	}
	d.Legacy = m
	plan := core.MakePlan(&d)
	t := core.Task{Definition: d, Plan: plan, Step: "define", Status: "open", Historical: true, Paused: true, Failure: "legacy task imported; no running agent or new verification", Created: time.Now().UTC(), Legacy: m}
	switch src.OldStatus {
	case "done", "completed":
		t.Status = "completed"
		t.Result = "历史完成 · 导入未复验"
		t.Paused = false
		for _, key := range []string{"completed_at", "completedAt", "finished_at"} {
			if value := stringField(m, key); value != "" {
				if tm, e := time.Parse(time.RFC3339, value); e == nil {
					t.Completed = &tm
					break
				}
			}
		}
	case "cancelled", "canceled":
		t.Status = "cancelled"
		t.Result = "历史取消"
		t.Paused = false
	default:
		if src.Action == "pending" {
			t.Confirmation = &core.Confirmation{Kind: "migration", Reason: "ambiguous imported task status; review original metadata", Revision: 1, PlanRevision: 1}
		}
	}
	if len(missing) > 0 {
		t.Legacy["ael_mapping_warnings"] = missing
		if t.Status == "open" {
			t.Confirmation = &core.Confirmation{Kind: "migration", Reason: strings.Join(missing, "; "), Revision: 1, PlanRevision: 1}
		}
	}
	return t
}
func Apply(s *core.Service, p Plan, proposal *Proposal, approved string) (Run, error) {
	d, e := Validate(p, proposal)
	if e != nil {
		return Run{}, e
	}
	if approved != d {
		return Run{}, core.Error("MIGRATION_APPROVAL_REQUIRED", "review inventory/plan/proposal and supply --approved-digest "+d)
	}
	if p.Project != s.Root {
		return Run{}, core.Error("PROJECT_MISMATCH", p.Project)
	}
	run, e := getRun(s, p.ID)
	if e != nil {
		run = Run{ID: p.ID, State: "applying", Count: len(p.Sources), Plan: p, Proposal: proposal, Changes: []Change{}, Warnings: []string{}, Applied: []string{}, Digest: d}
		if e = saveRun(s, run); e != nil {
			return run, e
		}
	} else if run.Digest != d {
		return run, core.Error("MIGRATION_REVISION_CONFLICT", p.ID)
	}
	if run.State == "rolled_back" {
		return run, core.Error("MIGRATION_ROLLED_BACK", "create a new reviewed plan")
	}
	if e = s.Store.Flush(); e != nil {
		return run, e
	}
	suggestions := map[string]Suggestion{}
	if proposal != nil {
		for _, x := range proposal.Items {
			suggestions[x.SourceID] = x
		}
	}
	backupDir := filepath.Join(s.Store.Dir, "migrations", p.ID)
	if e = os.MkdirAll(backupDir, 0700); e != nil {
		return run, e
	}
	for _, src := range p.Sources {
		if slices.Contains(run.Applied, src.ID) {
			continue
		}
		if src.Action != "ignore" {
			path, e := store.SafePath(s.Root, src.Path)
			if e != nil {
				return run, e
			}
			backupPath := filepath.Join(backupDir, "source-"+src.ID)
			if e = store.CopyAtomic(path, backupPath, 0600); e != nil {
				return run, e
			}
			hash, e := store.HashFile(backupPath)
			if e != nil {
				return run, e
			}
			if src.Hash != "" && hash != src.Hash {
				return run, core.Error("SOURCE_CHANGED_DURING_BACKUP", src.Path)
			}
			if e != nil {
				return run, e
			}
		}
		if src.Target != "" {
			path, e := store.SafePath(s.Root, src.Target)
			if e != nil {
				return run, e
			}
			old, err := os.ReadFile(path)
			oldHash := ""
			if err == nil {
				oldHash = store.Hash(old)
			} else if !os.IsNotExist(err) {
				return run, err
			}
			existing := false
			var change Change
			for _, c := range run.Changes {
				if c.SourceID == src.ID {
					change = c
					existing = true
					break
				}
			}
			var body []byte
			var task *core.Task
			if src.Kind == "task" {
				t := importedTask(src, p.Sources)
				if x, ok := suggestions[src.ID]; ok && x.Action == "task_status" {
					switch x.Status {
					case "open":
						t.Status = "open"
						t.Completed = nil
						t.Result = ""
					case "conflict":
						t.Status = "open"
						t.Completed = nil
						t.Result = ""
						t.Confirmation = &core.Confirmation{Kind: "migration", Reason: x.Reason, Revision: 1, PlanRevision: 1}
					case "cancelled":
						t.Status = "cancelled"
						t.Completed = nil
						t.Result = "historical cancellation"
					}
				}
				task = &t
				body = core.DefinitionDocument(t.Definition)
			} else {
				kind, status := src.Kind, "historical"
				superseded := ""
				if src.Action == "pending" {
					status = "conflict"
				}
				if x, ok := suggestions[src.ID]; ok {
					kind = x.Kind
					status = x.Status
					superseded = x.SupersededBy
					if superseded != "" {
						status = "superseded"
					}
				}
				text, warnings := mapLinks(src, p.Sources)
				run.Warnings = append(run.Warnings, warnings...)
				paths := []string{}
				if src.Kind == "spec" {
					paths = []string{strings.TrimPrefix(filepath.ToSlash(filepath.Dir(src.Path)), ".trellis/spec/")}
				}
				body = knowledge.Encode(knowledge.Record{ID: "legacy-" + src.ID, Title: filepath.Base(src.Path), Kind: kind, Status: status, SupersededBy: superseded, Paths: paths, Source: src.Path + "#sha256=" + src.Hash, Body: text})
			}
			after := store.Hash(body)
			if existing {
				if change.After != after {
					return run, core.Error("REPAIR_CONTENT_CONFLICT", src.Target)
				}
				if oldHash != change.Before && oldHash != change.After {
					return run, core.Error("TARGET_CHANGED", src.Target)
				}
			} else {
				if oldHash != p.Targets[src.Target] {
					return run, core.Error("TARGET_CHANGED", src.Target)
				}
				if oldHash != "" {
					prior, e := List(s)
					if e != nil {
						return run, e
					}
					managed := false
					for _, r := range prior {
						for _, c := range r.Changes {
							if c.Target == src.Target && c.After == oldHash && r.State == "applied" {
								managed = true
							}
						}
					}
					if !managed {
						return run, core.Error("TARGET_NOT_MANAGED", src.Target)
					}
				}
				change = Change{Target: src.Target, Before: oldHash, After: after, Backup: filepath.Join(backupDir, "before-"+src.ID), SourceID: src.ID}
				if task != nil {
					change.TaskID = task.Definition.ID
					if prior, err := s.Get(task.Definition.ID); err == nil {
						change.PriorTask = &prior
						out, e := store.SafePath(s.Root, ".ael/outcomes/"+task.Definition.ID+".json")
						if e != nil {
							return run, e
						}
						if raw, err := os.ReadFile(out); err == nil {
							change.PriorOutcome = raw
						} else if !os.IsNotExist(err) {
							return run, err
						}
					}
				}
				if oldHash != "" {
					if e = store.Atomic(change.Backup, old, 0600); e != nil {
						return run, e
					}
				}
				run.Changes = append(run.Changes, change)
				if e = saveRun(s, run); e != nil {
					return run, e
				}
			}
			if task != nil {
				if prior, err := s.Get(task.Definition.ID); err == nil && (len(prior.Attempts) > 0 || len(prior.Approvals) > 0 || len(prior.Feedback) > 0 || (prior.Status == "open" && !prior.Paused)) {
					return run, core.Error("TARGET_RUNTIME_CHANGED", "legacy reimport cannot overwrite AEL task progress")
				}
				if oldHash != after {
					if e = s.ImportTask(*task); e != nil {
						return run, e
					}
				} else {
					if _, e = s.Get(task.Definition.ID); e != nil {
						if e = s.ImportTask(*task); e != nil {
							return run, e
						}
					}
				}
			} else {
				e = s.Store.Write(func(tx *store.Tx) error { return tx.File(path, body) })
				if e == nil {
					e = s.Store.Flush()
				}
				if e != nil {
					return run, e
				}
			}
		}
		run.Applied = append(run.Applied, src.ID)
		if e = saveRun(s, run); e != nil {
			return run, e
		}
	}
	run.State = "applied"
	if e = saveRun(s, run); e != nil {
		return run, e
	}
	mapping := []map[string]string{}
	for _, c := range run.Changes {
		mapping = append(mapping, map[string]string{"source_id": c.SourceID, "target": c.Target, "before_hash": c.Before, "after_hash": c.After, "task_id": c.TaskID})
	}
	summary := map[string]any{"id": run.ID, "sources": len(p.Sources), "mapping": mapping, "warnings": run.Warnings, "digest": run.Digest}
	e = writeJSON(filepath.Join(s.Root, ".ael/imports", run.ID+".json"), summary)
	return run, e
}
func Verify(s *core.Service, id string) (any, error) {
	r, e := getRun(s, id)
	if e != nil {
		return nil, e
	}
	counts := map[string]int{}
	issues := []string{}
	for _, src := range r.Plan.Sources {
		counts[src.Action]++
		if !slices.Contains(r.Applied, src.ID) {
			issues = append(issues, "not accounted: "+src.Path)
		}
	}
	for _, c := range r.Changes {
		p, e := store.SafePath(s.Root, c.Target)
		if e != nil {
			issues = append(issues, e.Error())
			continue
		}
		b, e := os.ReadFile(p)
		if e != nil || store.Hash(b) != c.After {
			issues = append(issues, "target drift: "+c.Target)
		}
	}
	if len(r.Applied) != r.Count {
		issues = append(issues, "count reconciliation mismatch")
	}
	return map[string]any{"id": id, "total": r.Count, "classified": counts, "issues": issues, "valid": len(issues) == 0, "state": r.State}, nil
}
func Repair(s *core.Service, id string) (Run, error) {
	r, e := getRun(s, id)
	if e != nil {
		return r, e
	}
	return Apply(s, r.Plan, r.Proposal, r.Digest)
}
func Rollback(s *core.Service, id string) (Run, error) {
	if e := s.Store.Flush(); e != nil {
		return Run{}, e
	}
	r, e := getRun(s, id)
	if e != nil {
		return r, e
	}
	conflicts := []string{}
	for i := len(r.Changes) - 1; i >= 0; i-- {
		c := r.Changes[i]
		p, e := store.SafePath(s.Root, c.Target)
		if e != nil {
			return r, e
		}
		b, e := os.ReadFile(p)
		if os.IsNotExist(e) && c.Before == "" {
			continue
		}
		if e != nil {
			return r, e
		}
		if store.Hash(b) == c.Before {
			continue
		}
		if store.Hash(b) != c.After {
			conflicts = append(conflicts, c.Target)
			continue
		}
		if c.TaskID != "" {
			t, e := s.Get(c.TaskID)
			if e == nil && len(t.Attempts) > 0 {
				conflicts = append(conflicts, "runtime changed: "+c.TaskID)
				continue
			}
		}
		if c.TaskID != "" {
			out, e := store.SafePath(s.Root, ".ael/outcomes/"+c.TaskID+".json")
			if e != nil {
				return r, e
			}
			if raw, e := os.ReadFile(out); e == nil {
				task, err := s.Get(c.TaskID)
				expected := core.OutcomeDocument(task)
				if err != nil || store.Hash(raw) != store.Hash(expected) {
					conflicts = append(conflicts, "outcome changed: "+c.TaskID)
					continue
				}
				if err = os.Remove(out); err != nil {
					return r, err
				}
			}
		}
		if c.TaskID != "" && c.Before != "" {
			if c.PriorTask == nil {
				return r, core.Error("MIGRATION_SNAPSHOT_MISSING", c.TaskID)
			}
			definition, e := os.ReadFile(c.Backup)
			if e != nil {
				return r, e
			}
			if e = s.RestoreImportedSnapshot(*c.PriorTask, definition, c.PriorOutcome); e != nil {
				return r, e
			}
			continue
		}
		if c.Before == "" {
			e = os.Remove(p)
		} else {
			old, err := os.ReadFile(c.Backup)
			if err != nil {
				return r, err
			}
			e = store.Atomic(p, old, 0644)
		}
		if e != nil {
			return r, e
		}
		if c.TaskID != "" {
			if e = s.DeleteImported(c.TaskID); e != nil {
				return r, e
			}
		}
	}
	r.Warnings = append(r.Warnings, conflicts...)
	if len(conflicts) > 0 {
		r.State = "rollback_conflict"
	} else {
		r.State = "rolled_back"
	}
	e = saveRun(s, r)
	return r, e
}

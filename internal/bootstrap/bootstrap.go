package bootstrap

import (
	"ael/assets"
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.1.13"

type Config struct {
	Schema    int    `json:"schema"`
	ProjectID string `json:"project_id"`
	Mode      string `json:"mode"`
	Version   string `json:"asset_version"`
}
type Report struct {
	ServiceVersion string           `json:"service_version"`
	ServiceBuild   string           `json:"service_build"`
	Executable     string           `json:"executable"`
	Knowledge      knowledge.Health `json:"knowledge_health"`
	Bundle         *Bundle          `json:"bundle,omitempty"`
	Candidates     []map[string]any `json:"check_candidates"`
	Root           string           `json:"project"`
	Data           string           `json:"data_dir"`
	Config         Config           `json:"config"`
	Files          []string         `json:"files"`
	Detected       []string         `json:"detected"`
	Warnings       []string         `json:"warnings"`
}

func Canonical(root string) (string, error) {
	p, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}
func DataDir(root string) string {
	if canonical, e := Canonical(root); e == nil {
		root = canonical
	}
	base := os.Getenv("AEL_DATA_DIR")
	if base == "" {
		dir, e := os.UserConfigDir()
		if e != nil {
			dir = os.TempDir()
		}
		base = filepath.Join(dir, "ael", "runtime")
	}
	return filepath.Join(base, store.Hash([]byte(root))[:24])
}
func Read(root string) (Config, error) {
	var c Config
	p, e := store.SafePath(root, ".ael/project.yaml")
	if e != nil {
		return c, e
	}
	b, e := os.ReadFile(p)
	if e == nil {
		e = json.Unmarshal(b, &c)
	}
	return c, e
}

var Build = "dev"

const begin = "<!-- AEL:BEGIN -->"
const end = "<!-- AEL:END -->"

func block(mode string) string {
	return begin + "\nAEL project (" + mode + "). For an engineering work request, use $ael-work; resume an existing task first. From the project root run `./ael doctor` and `./ael task list`; load exact task rules with `./ael context --task <id>`. Discussion/query alone creates no task.\nUse $ael-work for authorized task execution; $ael-migrate-legacy only for reviewed legacy proposals.\nDo not treat historical imports as current constraints or verified checks.\n" + end
}
func Init(root string, dry, updating bool) (Report, error) {
	root, e := Canonical(root)
	r := Report{Root: root, Data: DataDir(root), Files: []string{}, Detected: []string{}, Warnings: []string{}}
	if e != nil {
		return r, e
	}
	if e = ValidateDataLocation(root, r.Data); e != nil {
		return r, e
	}
	for _, p := range []string{".trellis", ".agents/notes", "notes", "AGENTS.md", "CLAUDE.md", ".codex", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "go.mod", "pom.xml", "build.gradle", "Cargo.toml"} {
		if _, e = os.Lstat(filepath.Join(root, p)); e == nil {
			r.Detected = append(r.Detected, p)
		}
	}
	c, e := Read(root)
	newProject := os.IsNotExist(e)
	if e != nil && !newProject {
		return r, e
	}
	if newProject {
		c = Config{1, core.ID("project-"), "active", Version}
		for _, p := range r.Detected {
			if p == ".trellis" {
				c.Mode = "shadow"
			}
		}
	}
	r.Config = c
	r.Candidates = DetectChecks(root)
	var manifest map[string]string
	manifest = map[string]string{}
	p, e := store.SafePath(root, ".ael/assets.json")
	if e != nil {
		return r, e
	}
	b, e := os.ReadFile(p)
	if e == nil {
		if e = json.Unmarshal(b, &manifest); e != nil {
			return r, e
		}
	} else if !os.IsNotExist(e) {
		return r, e
	}
	targets := map[string][]byte{}
	if newProject {
		b, _ := json.MarshalIndent(c, "", "  ")
		targets[".ael/project.yaml"] = b
		targets[".ael/policy.yaml"] = []byte("{\"schema\":1,\"max_retries\":3,\"max_seconds\":0,\"lease_seconds\":1800,\"checks\":[],\"critical_knowledge\":[],\"delivery_requires_confirmation\":true}\n")
		targets[".ael/executor.example.json"] = []byte(`{"command":[],"codex":false,"workspace":"","session":"","max_steps":20,"timeout_seconds":0}` + "\n")
		targets[".ael/project.md"] = []byte("# 项目定义\n\n请填写目标、非目标与关键边界。尚未配置自动验收或发布命令。\n")
	}
	if updating && !newProject {
		// Upgrade only the exact, untouched former defaults. User policy is retained.
		oldDefaults := map[string]string{
			".ael/policy.yaml":           "{\"schema\":1,\"max_retries\":3,\"max_seconds\":3600,\"checks\":[],\"critical_knowledge\":[],\"delivery_requires_confirmation\":true}\n",
			".ael/executor.example.json": `{"command":[],"codex":false,"workspace":"","session":"","max_steps":20,"timeout_seconds":300}` + "\n",
		}
		for rel, oldDefault := range oldDefaults {
			b, e := os.ReadFile(filepath.Join(root, rel))
			if e != nil {
				return r, e
			}
			if string(b) == oldDefault && manifest[rel] == store.Hash(b) {
				targets[rel] = []byte(strings.ReplaceAll(strings.ReplaceAll(oldDefault, "\"max_seconds\":3600", "\"max_seconds\":0,\"lease_seconds\":1800"), "\"timeout_seconds\":300", "\"timeout_seconds\":0"))
			}
		}
	}
	for _, file := range []string{"workflow.md", "commands.md"} {
		b, e := assets.Files.ReadFile("skills/ael-work/references/" + file)
		if e != nil {
			return r, e
		}
		targets[".agents/skills/ael-work/references/"+file] = b
	}
	for _, name := range []string{"ael-work", "ael-migrate-legacy"} {
		b, e := assets.Files.ReadFile("skills/" + name + "/SKILL.md")
		if e != nil {
			return r, e
		}
		targets[".agents/skills/"+name+"/SKILL.md"] = b
	}
	agentPath, e := store.SafePath(root, "AGENTS.md")
	if e != nil {
		return r, e
	}
	old, e := os.ReadFile(agentPath)
	if e != nil && !os.IsNotExist(e) {
		return r, e
	}
	text := string(old)
	managed := block(c.Mode)
	oldBlock := ""
	start := strings.Index(text, begin)
	finish := strings.Index(text, end)
	if start >= 0 || finish >= 0 {
		if start < 0 || finish < start {
			return r, core.Error("ASSET_CONFLICT", "malformed AGENTS managed block")
		}
		finish += len(end)
		oldBlock = text[start:finish]
		if h, ok := manifest["AGENTS:block"]; !ok || store.Hash([]byte(oldBlock)) != h {
			return r, core.Error("ASSET_CONFLICT", "user edited AGENTS managed block; review before update")
		}
		text = text[:start] + managed + text[finish:]
	} else {
		text = strings.TrimRight(text, "\n") + "\n\n" + managed + "\n"
	}
	targets["AGENTS.md"] = []byte(text)
	for rel, want := range targets {
		p, e := store.SafePath(root, rel)
		if e != nil {
			return r, e
		}
		old, e := os.ReadFile(p)
		if e == nil && string(old) != string(want) && rel != "AGENTS.md" {
			h, ok := manifest[rel]
			if !ok || store.Hash(old) != h {
				return r, core.Error("ASSET_CONFLICT", rel+" user content preserved")
			}
		} else if e != nil && !os.IsNotExist(e) {
			return r, e
		}
	}
	if dry {
		for rel := range targets {
			r.Files = append(r.Files, rel)
		}
		r.Warnings = append(r.Warnings, "dry-run: full managed-asset checks completed; no project or runtime changes")
		return r, nil
	}
	s, e := store.Open(r.Data)
	if e != nil {
		return r, e
	}
	defer s.Close()
	if e = s.Flush(); e != nil {
		return r, e
	}
	e = s.Write(func(tx *store.Tx) error {
		for rel, b := range targets {
			p, e := store.SafePath(root, rel)
			if e != nil {
				return e
			}
			if e = tx.File(p, b); e != nil {
				return e
			}
			if rel != "AGENTS.md" {
				manifest[rel] = store.Hash(b)
			}
			r.Files = append(r.Files, rel)
		}
		manifest["AGENTS:block"] = store.Hash([]byte(managed))
		b, _ := json.MarshalIndent(manifest, "", "  ")
		p, e := store.SafePath(root, ".ael/assets.json")
		if e != nil {
			return e
		}
		return tx.File(p, b)
	})
	if e == nil {
		e = s.Flush()
	}
	if e == nil {
		_, e = knowledge.Index(root)
	}
	r.Warnings = append(r.Warnings, "check commands and delivery environment require explicit project configuration")
	return r, e
}
func Open(root string) (*core.Service, Report, error) {
	root, e := Canonical(root)
	r := Report{Root: root, Data: DataDir(root)}
	if e != nil {
		return nil, r, e
	}
	if e = ValidateDataLocation(root, r.Data); e != nil {
		return nil, r, e
	}
	c, e := Read(root)
	r.Config = c
	if e != nil {
		return nil, r, core.Error("NOT_INITIALIZED", "run ael init --project PATH")
	}
	s, e := store.Open(r.Data)
	if e != nil {
		return nil, r, e
	}
	svc := &core.Service{Store: s, Root: root, Active: c.Mode == "active"}
	svc.ResolveBasis = func(d core.Definition) ([]core.BasisReference, error) { return knowledge.Basis(root, d) }
	svc.ConstraintCheck = func(d core.Definition) []string { return knowledge.Conflicts(root, d) }
	if e = s.Flush(); e == nil {
		e = svc.RecoverDefinitions()
	}
	if e != nil {
		s.Close()
		return nil, r, e
	}
	return svc, r, nil
}
func Doctor(root string) (Report, error) {
	root, e := Canonical(root)
	r := Report{Root: root, Data: DataDir(root)}
	if e != nil {
		return r, e
	}
	r.ServiceVersion = Version
	r.ServiceBuild = Build
	r.Executable, _ = os.Executable()
	r.Knowledge = knowledge.Status(root)
	r.Candidates = DetectChecks(root)
	r.Config, e = Read(root)
	if e != nil {
		return r, e
	}
	r.Warnings = []string{"No automatic checks unless configured in task definitions", "Runtime DB/WAL/logs/artifacts are outside business repository"}
	return r, nil
}

var _ = fmt.Sprint

func DetectChecks(root string) []map[string]any {
	out := []map[string]any{}
	add := func(kind string, cmd []string, source string) {
		out = append(out, map[string]any{"kind": kind, "command": cmd, "source": source, "status": "suggestion_only_not_executed"})
	}
	if _, e := os.Stat(filepath.Join(root, "go.mod")); e == nil {
		add("build", []string{"go", "build", "./..."}, "go.mod")
		add("unit", []string{"go", "test", "./..."}, "go.mod")
	}
	if b, e := os.ReadFile(filepath.Join(root, "package.json")); e == nil {
		var p struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(b, &p) == nil {
			manager := "npm"
			for _, pair := range [][2]string{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}} {
				if _, e := os.Stat(filepath.Join(root, pair[0])); e == nil {
					manager = pair[1]
				}
			}
			for _, key := range []string{"build", "test", "lint", "typecheck"} {
				if script := p.Scripts[key]; script != "" {
					add(key, []string{manager, "run", key}, "package.json#scripts."+key)
				}
			}
		}
	}
	if _, e := os.Stat(filepath.Join(root, "pom.xml")); e == nil {
		add("unit", []string{"mvn", "test"}, "pom.xml")
	}
	return out
}

func ValidateDataLocation(root, data string) error {
	if !filepath.IsAbs(data) {
		return core.Error("DATA_DIR_ABSOLUTE_REQUIRED", "AEL_DATA_DIR must be an absolute directory")
	}
	probe := filepath.Clean(data)
	suffix := []string{}
	for {
		if _, e := os.Lstat(probe); e == nil {
			break
		} else if !os.IsNotExist(e) {
			return e
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return core.Error("DATA_DIR_INVALID", data)
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	resolved, e := filepath.EvalSymlinks(probe)
	if e != nil {
		return e
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return core.Error("DATA_DIR_INSIDE_PROJECT", "runtime data must remain outside the project")
	}
	return nil
}

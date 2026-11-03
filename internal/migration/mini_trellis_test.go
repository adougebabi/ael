package migration

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func miniFixture(t *testing.T) (*core.Service, Plan) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	files := map[string]string{
		"AGENTS.md":                                     "<!-- TRELLIS:START -->\n# mini-trellis memory\n<!-- TRELLIS:END -->\n# Owner rule\nKeep user text and [contract](.trellis/spec/backend/rules.md).\n",
		".trellis/spec/backend/rules.md":                "# Backend rules\nKeep verified evidence.\n",
		".trellis/research/old-topic.md":                "# Folded task\nHistorical, not a new active work item.\n",
		".trellis/research/output.log":                  "raw old process output",
		".trellis/research/capture.jsonl":               "{\"type\":\"historical-only\"}\n",
		".trellis/.runtime/sessions/codex-session.json": "{\"platform\":\"codex\",\"last_seen_at\":\"2026-10-28T00:00:00Z\",\"current_task\":null,\"current_run\":null}",
		".trellis/.runtime/update-check-abc.marker":     "0.3.2\n",
		".codex/config.toml":                            "# mini-trellis generated\nproject_doc_fallback_filenames = [\"AGENTS.md\"]\n# user defaults retained\n",
		".codex/hooks/session-start.py":                 "# mini-trellis\n# TRELLIS_DISABLE_HOOKS\nraise RuntimeError('never execute during migration')\n",
		".codex/hooks.json":                             `{"custom":"retain","hooks":{"SessionStart":[{"matcher":"startup|resume|clear|compact","hooks":[{"type":"command","command":"python3 -X utf8 .codex/hooks/session-start.py","statusMessage":"Loading mini-trellis context"},{"type":"command","command":"echo unrelated-user-hook"}]}]}}`,
		".claude/settings.local.json":                   `{"permissions":{"allow":["Read","Bash(cat .trellis/tasks/old/task.json)"]}}`,
		".agents/skills/unrelated/SKILL.md":             "---\nname: unrelated\ndescription: unrelated utility\n---\nKeep this skill.\n",
	}
	for _, name := range []string{"mini-trellis-remember", "mini-trellis-update-spec", "mini-trellis-session-insight"} {
		files[".agents/skills/"+name+"/SKILL.md"] = "---\nname: " + name + "\ndescription: mini-trellis memory\n---\nLegacy memory instructions.\n"
		files[".agents/skills/"+name+"/references/note.md"] = "Private helper without a product-name marker.\n"
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(body), 0644)
	}
	os.WriteFile(filepath.Join(root, ".trellis/research/cache.pyc"), []byte{0xff, 0, 0xfe, 1}, 0644)
	os.WriteFile(filepath.Join(root, ".trellis/.runtime/.DS_Store"), []byte{0xff, 0, 0xfe}, 0644)
	os.Chmod(filepath.Join(root, ".codex/hooks/session-start.py"), 0755)
	if _, e := bootstrap.Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	s, _, e := bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Store.Close() })
	inv, e := Scan(root)
	if e != nil {
		t.Fatal(e)
	}
	p, e := MakePlan(inv)
	if e != nil {
		t.Fatal(e)
	}
	return s, p
}
func TestMiniTrellisCutoverPreservesUserSettingsAndRollsBack(t *testing.T) {
	s, p := miniFixture(t)
	local, _ := os.ReadFile(filepath.Join(s.Root, ".claude/settings.local.json"))
	config, _ := os.ReadFile(filepath.Join(s.Root, ".codex/config.toml"))
	for _, source := range p.Sources {
		if strings.HasSuffix(source.Path, ".log") || strings.HasSuffix(source.Path, ".jsonl") {
			if source.Action != "preserve" || source.Target != "" || source.Body != "" {
				t.Fatal("raw logs became knowledge", source)
			}
		}
	}
	digest, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	run, e := Apply(s, p, nil, digest)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Activate(s, p.ID, digest, true); e != nil {
		t.Fatal(e)
	}
	if _, e = Activate(s, p.ID, digest, true); e != nil {
		t.Fatal("activation not idempotent", e)
	}
	verify, e := Verify(s, p.ID)
	if e != nil || !verify.(map[string]any)["valid"].(bool) {
		t.Fatal(verify, e)
	}
	for _, rel := range []string{".codex/hooks/session-start.py", ".agents/skills/mini-trellis-remember/SKILL.md", ".agents/skills/mini-trellis-session-insight/references/note.md"} {
		if _, e = os.Stat(filepath.Join(s.Root, rel)); !os.IsNotExist(e) {
			t.Fatal("legacy entry not retired", rel)
		}
	}
	hooks, _ := os.ReadFile(filepath.Join(s.Root, ".codex/hooks.json"))
	if strings.Contains(string(hooks), "session-start.py") || !strings.Contains(string(hooks), "unrelated-user-hook") {
		t.Fatal(string(hooks))
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, ".claude/settings.local.json"))
	if string(b) != string(local) {
		t.Fatal("changed unrelated permissions")
	}
	b, _ = os.ReadFile(filepath.Join(s.Root, ".codex/config.toml"))
	if string(b) != string(config) {
		t.Fatal("changed generic Codex defaults")
	}
	b, _ = os.ReadFile(filepath.Join(s.Root, "AGENTS.md"))
	if strings.Contains(string(b), ".trellis/") || !strings.Contains(string(b), "Owner rule") || !strings.Contains(string(b), ".ael/knowledge/") {
		t.Fatal(string(b))
	}
	run, e = Rollback(s, run.ID)
	if e != nil || run.State != "rolled_back" || s.Active {
		t.Fatal(run, e)
	}
	st, e := os.Stat(filepath.Join(s.Root, ".codex/hooks/session-start.py"))
	if e != nil || st.Mode().Perm() != 0755 {
		t.Fatal("retired script mode not restored", st, e)
	}
	hooks, _ = os.ReadFile(filepath.Join(s.Root, ".codex/hooks.json"))
	if !strings.Contains(string(hooks), "session-start.py") {
		t.Fatal("old registration not restored")
	}
}
func TestMiniTrellisNonemptyExecutionPointerBlocks(t *testing.T) {
	s, _ := miniFixture(t)
	os.WriteFile(filepath.Join(s.Root, ".trellis/.runtime/sessions/codex-session.json"), []byte(`{"current_task":"active","current_run":"running"}`), 0644)
	inv, e := Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	p, e := MakePlan(inv)
	if e != nil {
		t.Fatal(e)
	}
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(s, p, nil, d); e != nil {
		t.Fatal(e)
	}
	if _, e = Activate(s, p.ID, d, true); e == nil {
		t.Fatal("active engineering pointer ignored")
	}
}
func TestProposalScopeAndReviewedNoteKeepSource(t *testing.T) {
	s, p := miniFixture(t)
	var src, ref Source
	for _, x := range p.Sources {
		if x.Kind == "spec" {
			src = x
		}
		if strings.HasSuffix(x.Path, "old-topic.md") {
			ref = x
		}
	}
	proposal := &Proposal{PlanID: p.ID, Items: []Suggestion{{SourceID: src.ID, SourceHash: src.Hash, Quote: "Keep verified evidence.", Action: "classify", Kind: "spec", Status: "current", Target: src.Target, Reason: "inherit normative baseline, not implementation proof", Paths: []string{"apps/core-go"}, ReviewNote: "Historical descriptions do not certify current implementation.", References: []string{ref.ID}}}}
	d, e := Validate(p, proposal)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(s, p, proposal, d); e != nil {
		t.Fatal(e)
	}
	all, e := knowledge.ReadAll(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range all {
		if r.ID == "legacy-"+src.ID {
			found = true
			if !strings.Contains(r.Body, "Keep verified evidence.") || !strings.Contains(r.Body, "迁移审阅说明") || r.Paths[0] != "apps/core-go" {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal("reviewed spec absent")
	}
	proposal.Items[0].Paths = []string{"../escape"}
	if _, e = Validate(p, proposal); e == nil {
		t.Fatal("scope traversal accepted")
	}
}
func TestRetirementDeleteOutboxRecoveryAndConflict(t *testing.T) {
	s, _ := miniFixture(t)
	p := filepath.Join(s.Root, "owned.txt")
	os.WriteFile(p, []byte("old"), 0644)
	if e := s.Store.Write(func(tx *store.Tx) error { return tx.DeleteFile(p) }); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("user edit"), 0644)
	if e := s.Store.Flush(); e == nil {
		t.Fatal("deleted newer user file")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "user edit" {
		t.Fatal("lost user edit")
	}
	os.WriteFile(p, []byte("old"), 0644)
	if e := s.Store.Flush(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("delete intent not recovered")
	}
	if e := s.Store.Write(func(tx *store.Tx) error { return tx.File(p, []byte("restore")) }); e != nil {
		t.Fatal(e)
	}
	if e := s.Store.Flush(); e != nil {
		t.Fatal(e)
	}
}

var _ = json.Marshal

func TestUnknownRuntimePayloadIsNotTreatedAsIdle(t *testing.T) {
	if idleLegacyRuntime(Source{Path: ".trellis/.runtime/unknown.bin", Size: 12, Action: "preserve"}) {
		t.Fatal("opaque nonempty runtime treated as empty")
	}
	if idleLegacyRuntime(Source{Path: ".trellis/.runtime/session.json", Body: `{"current_task":null,"current_run":null,"pid":123}`}) {
		t.Fatal("unrecognized process metadata ignored")
	}
}

func TestActivationRejectsNewDiscoveryEntryAfterPlan(t *testing.T) {
	s, p := miniFixture(t)
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(s, p, nil, d); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(s.Root, ".claude/settings.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"command":"python3 .trellis/scripts/session-start.py"}]}]}}`), 0644)
	if _, e = Activate(s, p.ID, d, true); e == nil {
		t.Fatal("new old-controller registration was not reviewed")
	}
	c, _ := bootstrap.Read(s.Root)
	if c.Mode != "shadow" {
		t.Fatal("activated after discovery changed")
	}
}
func TestRollbackConfigConflictDoesNotRestoreOldController(t *testing.T) {
	s, p := miniFixture(t)
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	Apply(s, p, nil, d)
	if _, e = Activate(s, p.ID, d, true); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.Root, ".ael/project.yaml")
	b, _ := os.ReadFile(path)
	os.WriteFile(path, append(b, '\n'), 0644)
	r, e := Rollback(s, p.ID)
	if e != nil || r.State != "rollback_conflict" {
		t.Fatal(r, e)
	}
	hooks, _ := os.ReadFile(filepath.Join(s.Root, ".codex/hooks.json"))
	if strings.Contains(string(hooks), "session-start.py") {
		t.Fatal("old controller restored while AEL stayed active")
	}
	if _, e = os.Stat(filepath.Join(s.Root, ".agents/skills/mini-trellis-remember/SKILL.md")); !os.IsNotExist(e) {
		t.Fatal("old skill restored after config conflict")
	}
}
func TestActiveControllerDriftSuspendsAEL(t *testing.T) {
	s, p := miniFixture(t)
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	Apply(s, p, nil, d)
	if _, e = Activate(s, p.ID, d, true); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(s.Root, ".claude/settings.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"command":"python3 .trellis/scripts/session-start.py"}]}]}}`), 0644)
	if _, e = Activate(s, p.ID, d, true); e == nil {
		t.Fatal("new old controller not detected")
	}
	c, e := bootstrap.Read(s.Root)
	if e != nil || c.Mode != "shadow" {
		t.Fatal("AEL did not suspend", c, e)
	}
}

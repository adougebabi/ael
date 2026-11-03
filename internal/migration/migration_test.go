package migration

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/store"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := "../../testdata/trellis"
	e := filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(src, p)
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		return os.WriteFile(dst, b, 0644)
	})
	if e != nil {
		t.Fatal(e)
	}
	return root
}
func setup(t *testing.T) (*core.Service, Plan) {
	t.Helper()
	root := copyFixture(t)
	t.Setenv("AEL_DATA_DIR", t.TempDir())
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
func TestReadOnlyInventoryAndLosslessIdempotentApply(t *testing.T) {
	s, p := setup(t)
	before, e := core.Fingerprint(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	inv, e := Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := core.Fingerprint(s.Root)
	if before != after {
		t.Fatal("scan wrote source")
	}
	if len(inv.Sources) < 15 {
		t.Fatal("fixture not complete", len(inv.Sources))
	}
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(s, p, nil, ""); e == nil {
		t.Fatal("unreviewed apply allowed")
	}
	r, e := Apply(s, p, nil, d)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Applied) != len(p.Sources) {
		t.Fatal("lost source")
	}
	again, e := Apply(s, p, nil, d)
	if e != nil || len(again.Changes) != len(r.Changes) {
		t.Fatal(e)
	}
	v, e := Verify(s, r.ID)
	if e != nil || !v.(map[string]any)["valid"].(bool) {
		t.Fatal(v, e)
	}
	list, e := s.List()
	if e != nil {
		t.Fatal(e)
	}
	if len(list) != 2 {
		t.Fatal("unexpected task count", len(list))
	}
	for _, n := range list {
		if len(n.Task.Evidence) != 0 || n.Board == "active" {
			t.Fatal("fabricated running/verified state")
		}
		if n.Task.Definition.Title == "旧已完成无时间" && (n.Task.Completed != nil || n.Board != "completed") {
			t.Fatal("invented completion timestamp")
		}
		if n.Task.Definition.Title == "中文活动任务" && n.Task.Legacy["custom_extension"] == nil {
			t.Fatal("lost unknown fields")
		}
	}
	all, e := knowledge.ReadAll(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	for _, k := range all {
		if k.Kind == "spec" && k.Status != "conflict" {
			t.Fatal("spec automatically current")
		}
		if k.Source == "" || k.Body == "" {
			t.Fatal("lost original/source")
		}
	}
	r, e = Rollback(s, r.ID)
	if e != nil || r.State != "rolled_back" {
		t.Fatal(r, e)
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, ".trellis/scripts/session-start.py"))
	if !strings.Contains(string(b), "NEVER") {
		t.Fatal("source modified")
	}
}
func TestProposalValidationSourceDriftAndUserRollbackConflict(t *testing.T) {
	s, p := setup(t)
	var src Source
	for _, x := range p.Sources {
		if x.Kind == "spec" {
			src = x
			break
		}
	}
	prop := &Proposal{PlanID: p.ID, Items: []Suggestion{{SourceID: src.ID, SourceHash: src.Hash, Quote: "#", Action: "classify", Kind: "spec", Status: "historical", Target: src.Target, Reason: "混合计划保留历史"}}}
	d, e := Validate(p, prop)
	if e != nil {
		t.Fatal(e)
	}
	prop.Items[0].Target = "../../escape"
	if _, e = Validate(p, prop); e == nil {
		t.Fatal("proposal escaped target")
	}
	prop.Items[0].Target = src.Target
	prop.Items[0].Quote = "not in source"
	if _, e = Validate(p, prop); e == nil {
		t.Fatal("invented quote")
	}
	prop.Items[0].Quote = "#"
	r, e := Apply(s, p, prop, d)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.Root, src.Target)
	os.WriteFile(path, []byte("new user edits"), 0644)
	r, e = Rollback(s, r.ID)
	if e != nil || r.State != "rollback_conflict" {
		t.Fatal(r, e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "new user edits" {
		t.Fatal("overwrote new content")
	}
	os.WriteFile(filepath.Join(s.Root, src.Path), []byte("changed original"), 0644)
	if _, e = Validate(p, prop); e == nil {
		t.Fatal("stale plan accepted")
	}
}
func TestArchiveMoveIdentityAndUnsafeInputs(t *testing.T) {
	s, p := setup(t)
	var old Source
	for _, x := range p.Sources {
		if x.OldID == "legacy-active" && x.Kind == "task" {
			old = x
		}
	}
	from := filepath.Join(s.Root, ".trellis/tasks/active")
	to := filepath.Join(s.Root, ".trellis/tasks/archive/moved")
	if e := os.Rename(from, to); e != nil {
		t.Fatal(e)
	}
	inv, e := Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, x := range inv.Sources {
		if x.Kind == "task" && x.OldID == old.OldID {
			found = true
			if x.ID != old.ID || x.Target != old.Target {
				t.Fatal("archive move changed stable identity")
			}
		}
	}
	if !found {
		t.Fatal("lost moved task")
	}
	os.Symlink("/etc/passwd", filepath.Join(s.Root, ".trellis/spec/escape.md"))
	inv, e = Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range inv.Sources {
		if strings.HasSuffix(x.Path, "escape.md") && x.Action != "ignore" {
			t.Fatal("symlink followed")
		}
	}
}
func TestInterruptedApplyRepairAndSafeActivation(t *testing.T) {
	s, p := setup(t)
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	r := Run{ID: p.ID, State: "applying", Count: len(p.Sources), Plan: p, Digest: d, Applied: []string{}, Changes: []Change{}}
	if e = saveRun(s, r); e != nil {
		t.Fatal(e)
	}
	r, e = Repair(s, p.ID)
	if e != nil || r.State != "applied" {
		t.Fatal(r, e)
	}
	if _, e = Activate(s, p.ID, d, false); e == nil {
		t.Fatal("activated without drain ack")
	}
	if _, e = Activate(s, p.ID, d, true); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, "AGENTS.md"))
	if strings.Contains(string(b), "TRELLIS:START") || !strings.Contains(string(b), "保留用户说明") {
		t.Fatal(string(b))
	}
	b, _ = os.ReadFile(filepath.Join(s.Root, ".claude/settings.json"))
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "session-start.py") || !strings.Contains(string(b), "user-unrelated-hook") || v["permissions"] == nil {
		t.Fatal(string(b))
	}
	if _, e = bootstrap.Init(s.Root, false, true); e != nil {
		t.Fatal("refresh after activation", e)
	}
	r, e = Rollback(s, p.ID)
	if e != nil || r.State != "rolled_back" {
		t.Fatal(r, e)
	}
}
func TestUnknownHooksAndActiveOldRuntimeBlockActivation(t *testing.T) {
	for _, kind := range []string{"hooks", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			s, p := setup(t)
			if kind == "hooks" {
				os.WriteFile(filepath.Join(s.Root, ".claude/settings.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"command":"sh .trellis/custom.sh && deploy"}]}]}}`), 0644)
			} else {
				os.WriteFile(filepath.Join(s.Root, ".trellis/.runtime/pointer.json"), []byte(`{"pid":123,"state":"running"}`), 0644)
			}
			inv, e := Scan(s.Root)
			if e != nil {
				t.Fatal(e)
			}
			p, e = MakePlan(inv)
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
				t.Fatal("unsafe activation allowed")
			}
		})
	}
}
func TestPlanRejectsTamperedTargets(t *testing.T) {
	s, p := setup(t)
	_ = s
	p.Sources[0].Target = "../../evil"
	p.Digest = digest(p, nil)
	if _, e := Validate(p, nil); e == nil {
		t.Fatal("unsafe target allowed")
	}
	if _, e := store.SafePath(s.Root, "../escape"); e == nil {
		t.Fatal("traversal allowed")
	}
}

func TestRollbackSecondRevisionRestoresPriorRuntimeAndOutcome(t *testing.T) {
	s, p := setup(t)
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	first, e := Apply(s, p, nil, d)
	if e != nil {
		t.Fatal(e)
	}
	var taskID string
	for _, c := range first.Changes {
		if c.TaskID != "" {
			old, e := s.Get(c.TaskID)
			if e == nil && old.Status == "completed" {
				taskID = c.TaskID
			}
		}
	}
	before, e := s.Get(taskID)
	if e != nil {
		t.Fatal(e)
	}
	outcome, _ := os.ReadFile(filepath.Join(s.Root, ".ael/outcomes/"+taskID+".json"))
	src := filepath.Join(s.Root, ".trellis/tasks/archive/completed/task.json")
	raw, _ := os.ReadFile(src)
	os.WriteFile(src, []byte(strings.Replace(string(raw), "旧已完成无时间", "第二次改名", 1)), 0644)
	inv, e := Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	secondPlan, e := MakePlan(inv)
	if e != nil {
		t.Fatal(e)
	}
	d, e = Validate(secondPlan, nil)
	if e != nil {
		t.Fatal(e)
	}
	second, e := Apply(s, secondPlan, nil, d)
	if e != nil {
		t.Fatal(e)
	}
	if second.ID == first.ID {
		t.Fatal("source revision not tracked")
	}
	r, e := Rollback(s, second.ID)
	if e != nil || r.State != "rolled_back" {
		t.Fatal(r, e)
	}
	after, e := s.Get(taskID)
	if e != nil || after.Definition.Title != before.Definition.Title || after.Status != "completed" || after.Legacy["unknown_flag"] != true {
		t.Fatal(after, e)
	}
	restored, _ := os.ReadFile(filepath.Join(s.Root, ".ael/outcomes/"+taskID+".json"))
	if string(restored) != string(outcome) {
		t.Fatal("prior outcome lost")
	}
}

func TestProposalSupersessionCyclesAndLongChinesePaths(t *testing.T) {
	s, p := setup(t)
	specs := []Source{}
	for _, src := range p.Sources {
		if src.Kind == "spec" {
			specs = append(specs, src)
		}
	}
	prop := &Proposal{PlanID: p.ID, Items: []Suggestion{}}
	for i, src := range specs {
		prop.Items = append(prop.Items, Suggestion{SourceID: src.ID, SourceHash: src.Hash, Quote: "#", Action: "classify", Kind: "spec", Status: "historical", Target: src.Target, Reason: "检查替代环", SupersededBy: "legacy-" + specs[(i+1)%len(specs)].ID})
	}
	if _, e := Validate(p, prop); e == nil {
		t.Fatal("proposal replacement cycle accepted")
	}
	dir := filepath.Join(s.Root, ".trellis/spec", strings.Repeat("长路径目录", 8), strings.Repeat("继续保持原文", 8))
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, strings.Repeat("中文", 24)+".md")
	if e := os.WriteFile(path, []byte("# 长路径\n完整保留来源与正文。"), 0644); e != nil {
		t.Fatal(e)
	}
	inv, e := Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, src := range inv.Sources {
		if strings.Contains(src.Path, "中文中文") {
			found = true
			if src.Hash == "" || !strings.Contains(src.Body, "完整保留") {
				t.Fatal(src)
			}
		}
	}
	if !found {
		t.Fatal("long Chinese source missing")
	}
}

func TestReplanAfterRollbackUsesNewRunAndStableTargets(t *testing.T) {
	s, p := setup(t)
	d, e := Validate(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Apply(s, p, nil, d)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Rollback(s, r.ID); e != nil {
		t.Fatal(e)
	}
	inv, e := Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	again, e := MakePlan(inv)
	if e != nil {
		t.Fatal(e)
	}
	if again.ID == p.ID || again.InventoryID != p.InventoryID {
		t.Fatal("new review must have a distinct run, same source inventory")
	}
	d, e = Validate(again, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(s, again, nil, d); e != nil {
		t.Fatal("replanned apply after rollback", e)
	}
}

func TestInventoryStableAcrossUnknownMetadataMapOrder(t *testing.T) {
	s, _ := setup(t)
	p := filepath.Join(s.Root, ".trellis/tasks/active/task.json")
	b, _ := os.ReadFile(p)
	var m map[string]any
	json.Unmarshal(b, &m)
	m["unknown_z"] = 1
	m["unknown_a"] = 2
	b, _ = json.Marshal(m)
	os.WriteFile(p, b, 0644)
	first, e := Scan(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 10; i++ {
		again, e := Scan(s.Root)
		if e != nil || again.ID != first.ID {
			t.Fatal("inventory changed for identical sources", again.ID, first.ID, e)
		}
	}
}

package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func markPlanApplying(t *testing.T, s *core.Service, parent string) {
	t.Helper()
	key := "decomposition-active:" + store.Hash([]byte(parent))
	if _, err := s.Store.DB.Exec(`INSERT INTO metadata(key,body) VALUES(?,?)`, key, "isolated unfinished plan"); err != nil {
		t.Fatal(err)
	}
}

func TestApplyingPlanBlocksParentPreparation(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	markPlanApplying(t, s, parent)
	if _, err := s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "PLAN_APPLY_INCOMPLETE") {
		t.Fatalf("parent ignored an unfinished plan: %v", err)
	}
}

func TestApplyingPlanDuringCheckRetainsEvidenceWithoutClosing(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(parent, "integration", "checker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	markPlanApplying(t, s, parent)
	result, err := s.VerifyClaim(parent, "integration", "checker", claim.Attempt.ID, claim.Attempt.Token)
	if err != nil || result.Task.Status != "open" || len(result.Task.EvidenceHistory) != 1 || result.Task.EvidenceHistory[0].Result != "pass" {
		t.Fatalf("unfinished plan closed parent or discarded check: %+v %v", result.Task, err)
	}
}

func TestScopeChangeDuringCheckRetainsEvidenceWithoutClosing(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(parent, "integration", "checker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	create(t, s, core.Definition{ID: "new-required", Parent: parent, Title: "新增必要范围", Type: "bug", Goal: "verified", Scope: []string{"new.txt"}, Acceptance: []string{"结果"}})
	result, err := s.VerifyClaim(parent, "integration", "checker", claim.Attempt.ID, claim.Attempt.Token)
	if err != nil || result.Task.Status != "open" || len(result.Task.EvidenceHistory) != 1 || result.Task.EvidenceHistory[0].Result != "pass" {
		t.Fatalf("scope change closed parent or discarded check: %+v %v", result.Task, err)
	}
}

func integrationParent(t *testing.T, s *core.Service, id string, checks []core.Check) string {
	t.Helper()
	return create(t, s, core.Definition{ID: id, Title: "隔离集成", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}, Checks: checks})
}

func parentCheck(id, command string, affected ...string) core.Check {
	return core.Check{ID: id, Kind: "functional", Method: "command", Command: []string{"sh", "-c", command}, Covers: []string{"整体"}, Affected: affected, Required: true}
}

func copyCheckoutWithModes(t *testing.T, root string) string {
	t.Helper()
	clone := copyCheckout(t, root)
	if err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(filepath.Join(clone, rel), info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	return clone
}

func TestFailedExecutionChangesRemainInInclusionProof(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	if err := os.WriteFile(filepath.Join(s.Root, "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	id := create(t, s, core.Definition{ID: "leaf", Parent: parent, Title: "失败后修复", Type: "bug", Goal: "verified", Scope: []string{"old.txt", "result.txt"}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f result.txt && test ! -f old.txt"}, Covers: []string{"结果"}, Required: true}}})
	step(t, s, id, "fixture")
	first, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(s.Root, "old.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: first.Attempt.ID, Token: first.Attempt.Token, Key: core.ID("first-fail-"), Result: core.StepResult{Status: "fail", Summary: "部分执行失败"}}); err != nil {
		t.Fatal(err)
	}
	second, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: second.Attempt.ID, Token: second.Attempt.Token, Key: core.ID("second-pass-"), Result: core.StepResult{Status: "pass", Summary: "完成修复", Artifacts: []string{"result.txt"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(id, "unit", "checker"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") {
		t.Fatalf("failed execution deletion disappeared from proof: %v", err)
	}
}

func TestLeafBaselineMustBePresentInParentCandidate(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	if err := os.WriteFile(filepath.Join(s.Root, "auth.go"), []byte("old dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	id := create(t, s, core.Definition{ID: "leaf", Parent: parent, Title: "跨工作区", Type: "bug", Goal: "verified", Scope: []string{"route.go"}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f route.go"}, Covers: []string{"结果"}, Required: true}}})
	step(t, s, id, "fixture")
	workspace := copyCheckout(t, s.Root)
	if _, err := s.BindWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "auth.go"), []byte("new dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(id, "", "fixture", workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "route.go"), []byte("uses new dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("workspace-pass-"), Result: core.StepResult{Status: "pass", Summary: "隔离产物", Artifacts: []string{"route.go"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(id, "unit", "checker"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "route.go"), []byte("uses new dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_BASELINE_MISMATCH") {
		t.Fatalf("missing leaf input baseline falsely accepted: %v", err)
	}
}

func TestSameFingerprintAcrossNonGitWorkspacesRequiresRecheck(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true", "leaf")})
	if err := os.WriteFile(filepath.Join(s.Root, "route.txt"), []byte("same content"), 0600); err != nil {
		t.Fatal(err)
	}
	id := create(t, s, core.Definition{ID: "leaf", Parent: parent, Title: "跨工作区", Type: "bug", Goal: "verified", Scope: []string{"route.txt"}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f node_modules/allow"}, Covers: []string{"结果"}, Required: true}}})
	step(t, s, id, "fixture")
	workspace := copyCheckout(t, s.Root)
	if err := filepath.WalkDir(s.Root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(s.Root, name)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(filepath.Join(workspace, rel), info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "node_modules/allow"), []byte("only in leaf workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(id, "", "fixture", workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("same-workspace-"), Result: core.StepResult{Status: "pass", Summary: "隔离产物", Artifacts: []string{"route.txt"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(id, "unit", "checker"); err != nil {
		t.Fatal(err)
	}
	rootFP, err := core.Fingerprint(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := s.Get(id)
	if err != nil || leaf.Candidate != rootFP {
		t.Fatalf("fixture fingerprints differ: %s %s %v", leaf.Candidate, rootFP, err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_RECHECK_REQUIRED") {
		t.Fatalf("cross-workspace same fingerprint skipped recheck: %v", err)
	}
}

func TestRelativeAndBuiltInExcludedScopesCannotProveInclusion(t *testing.T) {
	for _, tc := range []struct {
		name, scope, file string
		policyExclude     bool
	}{
		{"relative policy exclusion", "./excluded.txt", "excluded.txt", true},
		{"built in skipped directory", "node_modules/pkg/index.js", "node_modules/pkg/index.js", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			if tc.policyExclude {
				path := filepath.Join(s.Root, ".ael", "policy.yaml")
				raw, _ := os.ReadFile(path)
				var policy map[string]any
				if err := json.Unmarshal(raw, &policy); err != nil {
					t.Fatal(err)
				}
				policy["candidate_exclude"] = []string{"excluded.txt"}
				raw, _ = json.Marshal(policy)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
			id := create(t, s, core.Definition{ID: "leaf", Parent: parent, Title: "排除范围", Type: "bug", Goal: "verified", Scope: []string{tc.scope}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f output.txt"}, Covers: []string{"结果"}, Required: true}}})
			step(t, s, id, "fixture")
			claim, err := s.Claim(id, "", "fixture", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(filepath.Join(s.Root, tc.file)), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(s.Root, tc.file), []byte("excluded change"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(s.Root, "output.txt"), []byte("normal artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("excluded-"), Result: core.StepResult{Status: "pass", Summary: "隔离产物", Artifacts: []string{"output.txt"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Verify(id, "unit", "checker"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_EXCLUDED_SCOPE_UNKNOWN") {
				t.Fatalf("excluded scope falsely proved inclusion: %v", err)
			}
		})
	}
}

func TestEpicIntegrationCheckpointRecoversInNewRuntime(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	clone := copyCheckout(t, s.Root)
	restored, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatalf("open prepared epic checkpoint: %v", err)
	}
	current, err := restored.Get(parent)
	if err != nil || !current.Paused || current.Step != "integrate" || current.Candidate != "" {
		t.Fatalf("open epic restored as live candidate: %+v %v", current, err)
	}
	restored.Store.Close()
	if _, err = s.Verify(parent, "integration", "checker"); err != nil {
		t.Fatal(err)
	}
	clone = copyCheckout(t, s.Root)
	restored, _, err = bootstrap.Open(clone)
	if err != nil {
		t.Fatalf("open completed epic checkpoint: %v", err)
	}
	defer restored.Store.Close()
	completed, err := restored.Get(parent)
	if err != nil || !completed.Historical || completed.Status != "completed" {
		t.Fatalf("completed epic history missing: %+v %v", completed, err)
	}
}

func TestOverlappingLeafChangesRequireOnlyAffectedRecheck(t *testing.T) {
	s := fixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "isolated fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(s.Root, ".gitignore"), []byte(".ael/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".gitignore")
	git("commit", "-qm", "isolated base")
	impact := integrationImpactFixture(t, s)
	parent := create(t, s, core.Definition{ID: "epic", Title: "隔离集成", Type: "epic", Goal: "verified", IntegrationImpact: impact, Acceptance: []string{"整体"}, Checks: []core.Check{parentCheck("integration", "true", "a")}})
	complete := func(id, content string) core.Task {
		t.Helper()
		create(t, s, core.Definition{ID: id, Parent: parent, Title: id, Type: "bug", Goal: "verified", Scope: []string{"shared.txt"}, Acceptance: []string{"文件非空"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"文件非空"}, Required: true}}})
		step(t, s, id, "fixture-"+id)
		claim, err := s.Claim(id, "", "fixture-"+id, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		git("add", "shared.txt")
		git("commit", "-qm", "isolated "+id)
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("overlap-"), Result: core.StepResult{Status: "pass", Summary: "真实隔离变更", Artifacts: []string{"shared.txt"}}}); err != nil {
			t.Fatal(err)
		}
		result, err := s.Verify(id, "unit", "checker-"+id)
		if err != nil || result.Task.Status != "completed" {
			t.Fatalf("leaf: %+v %v", result, err)
		}
		return *result.Task
	}
	first := complete("a", "A\n")
	second := complete("b", "A\nB\n")
	if _, err := s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_RECHECK_REQUIRED") {
		t.Fatalf("overlapping change did not request affected recheck: %v", err)
	}
	current, err := s.Get(parent)
	if err != nil {
		t.Fatal(err)
	}
	d := current.Definition
	d.Checks = append(d.Checks, parentCheck("recheck-a", "test -s shared.txt", "a"))
	if _, err = s.Edit(parent, d, "add affected leaf recheck"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	n, err := s.Verify(parent, "integration", "checker")
	if err != nil || n.Task.Status != "open" {
		t.Fatalf("parent skipped affected recheck: %+v %v", n, err)
	}
	n, err = s.Verify(parent, "recheck-a", "checker")
	if err != nil || n.Task.Status != "completed" {
		t.Fatalf("affected recheck did not finish: %+v %v", n, err)
	}
	for id, original := range map[string]core.Task{"a": first, "b": second} {
		after, err := s.Get(id)
		if err != nil || after.Completed == nil || !after.Completed.Equal(*original.Completed) || after.Candidate != original.Candidate || len(after.Attempts) != len(original.Attempts) {
			t.Fatalf("historical leaf %s was rewritten: %+v %v", id, after, err)
		}
	}
}

func TestOverlappingChangesCannotAllBeRevertedAndClaimedIntegrated(t *testing.T) {
	s := fixture(t)
	impact := integrationImpactFixture(t, s)
	parent := create(t, s, core.Definition{ID: "epic", Title: "撤销变更", Type: "epic", Goal: "verified", IntegrationImpact: impact, Acceptance: []string{"整体"}, Checks: []core.Check{
		parentCheck("integration", "true", "a", "b"), parentCheck("recheck-a", "test -s shared.txt", "a"), parentCheck("recheck-b", "test -s shared.txt", "b"),
	}})
	if err := os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		create(t, s, core.Definition{ID: id, Parent: parent, Title: id, Type: "bug", Goal: "verified", Scope: []string{"shared.txt"}, Acceptance: []string{"文件非空"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"文件非空"}, Required: true}}})
		step(t, s, id, "fixture-"+id)
		claim, err := s.Claim(id, "", "fixture-"+id, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte(strings.ToUpper(id)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("reverted-"), Result: core.StepResult{Status: "pass", Summary: "隔离变更", Artifacts: []string{"shared.txt"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Verify(id, "unit", "checker-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_LEAF_NOT_INCLUDED") {
		t.Fatalf("reverted changes falsely accepted as integrated: %v", err)
	}
}

func TestUnknownImpactRequiresRecheckOnFinalCandidate(t *testing.T) {
	s := fixture(t)
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	completeIntegrationLeaf(t, s, parent, "a", "a.txt")
	completeIntegrationLeaf(t, s, parent, "b", "b.txt")
	if _, err := s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_RECHECK_REQUIRED") || !strings.Contains(err.Error(), "a") {
		t.Fatalf("unknown impact silently reused earlier acceptance: %v", err)
	}
}

func TestCompleteImpactRequiresEvidenceAndDeclaredTargets(t *testing.T) {
	s := fixture(t)
	impact := integrationImpactFixture(t, s)
	parent := create(t, s, core.Definition{ID: "epic", Title: "影响分析", Type: "epic", Goal: "verified", IntegrationImpact: impact, Acceptance: []string{"整体"}, Checks: []core.Check{parentCheck("integration", "true")}})
	completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if _, err := s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_IMPACT_INCOMPLETE") {
		t.Fatalf("incomplete affected mapping was treated as complete: %v", err)
	}
}

func TestOptionalSupportingProofCannotChangeBeforeParentCloses(t *testing.T) {
	s := fixture(t)
	impact := integrationImpactFixture(t, s)
	parent := create(t, s, core.Definition{ID: "epic", Title: "可选证明", Type: "epic", Goal: "verified", IntegrationImpact: impact, Acceptance: []string{"整体"}, Checks: []core.Check{parentCheck("integration", "true", "optional")}})
	completeIntegrationLeaf(t, s, parent, "required", "required.txt")
	completeIntegrationLeaf(t, s, "", "optional", "optional.txt")
	if _, err := s.LinkEpic(parent, "optional", false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareEpicIntegration(parent); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(parent, "integration", "checker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	optional, err := s.Get("optional")
	if err != nil {
		t.Fatal(err)
	}
	optional.Status = "cancelled"
	raw, _ := json.Marshal(optional)
	if _, err = s.Store.DB.Exec(`UPDATE tasks SET body=?,status=? WHERE id=?`, string(raw), "cancelled", "optional"); err != nil {
		t.Fatal(err)
	}
	result, err := s.VerifyClaim(parent, "integration", "checker", claim.Attempt.ID, claim.Attempt.Token)
	if err != nil || result.Task.Status != "open" || len(result.Task.EvidenceHistory) == 0 {
		t.Fatalf("changed optional supporting proof still closed parent: %+v %v", result.Task, err)
	}
}

func TestUnmergedGitCommitRequiresFinalCandidateRecheck(t *testing.T) {
	s := fixture(t)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "isolated fixture")
	git("config", "user.email", "fixture@example.invalid")
	git("checkout", "-b", "fixture-main")
	if err := os.WriteFile(filepath.Join(s.Root, ".gitignore"), []byte(".ael/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".gitignore")
	git("commit", "-qm", "isolated base")
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true", "leaf")})
	id := create(t, s, core.Definition{ID: "leaf", Parent: parent, Title: "独立分支", Type: "bug", Goal: "verified", Scope: []string{"route.txt"}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s route.txt"}, Covers: []string{"结果"}, Required: true}}})
	git("checkout", "-b", "fixture-leaf")
	step(t, s, id, "fixture")
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "route.txt"), []byte("actual feature"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "route.txt")
	git("commit", "-qm", "isolated leaf")
	leafHead := git("rev-parse", "HEAD")
	if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("git-leaf-"), Result: core.StepResult{Status: "pass", Summary: "真实隔离提交", Artifacts: []string{"route.txt"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(id, "unit", "checker"); err != nil {
		t.Fatal(err)
	}
	git("checkout", "fixture-main")
	if err = os.WriteFile(filepath.Join(s.Root, "route.txt"), []byte("actual feature"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "route.txt")
	git("commit", "-qm", "independent content copy")
	parentHead := git("rev-parse", "HEAD")
	if leafHead == parentHead {
		t.Fatal("fixture did not create independent commits")
	}
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_RECHECK_REQUIRED") {
		t.Fatalf("unmerged commit was treated as already integrated: %v", err)
	}
	current, err := s.Get(parent)
	if err != nil {
		t.Fatal(err)
	}
	d := current.Definition
	d.Checks = append(d.Checks, parentCheck("recheck-leaf", "test -s route.txt", "leaf"))
	if _, err = s.Edit(parent, d, "add final candidate recheck"); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareEpicIntegration(parent)
	if err != nil || prepared.Task.Integration == nil || prepared.Task.Integration.GitHead != parentHead || prepared.Task.Integration.LocalRepository == "" || prepared.Task.Integration.ProjectID == "" || len(prepared.Task.Integration.Leaves[0].Rechecks) != 1 {
		t.Fatalf("git candidate/recheck not retained: %+v %v", prepared.Task.Integration, err)
	}
	if n, err := s.Verify(parent, "integration", "checker"); err != nil || n.Task.Status != "open" {
		t.Fatalf("recheck skipped: %+v %v", n, err)
	}
	if n, err := s.Verify(parent, "recheck-leaf", "checker"); err != nil || n.Task.Status != "completed" {
		t.Fatalf("verified candidate did not close: %+v %v", n, err)
	}
}

func TestGitProbeFailureCannotMasqueradeAsNonGit(t *testing.T) {
	s := fixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "isolated fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(s.Root, "base.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "base.txt")
	git("commit", "-qm", "isolated base")
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing-git-dir"))
	if _, err := s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_GIT_IDENTITY_UNAVAILABLE") {
		t.Fatalf("failed Git probe was presented as non-Git: %v", err)
	}
}

func TestGitBackupRestoreCanProveSameCommitAtNewPath(t *testing.T) {
	s := fixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "isolated fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(s.Root, "base.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "base.txt")
	git("commit", "-qm", "isolated base")
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	leaf := completeIntegrationLeaf(t, s, parent, "leaf", "leaf.txt")
	if leaf.CandidateProof == nil || leaf.CandidateProof.LocalRepository == "" {
		t.Fatal("leaf local Git identity missing")
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	clone := copyCheckoutWithModes(t, s.Root)
	restored, _, err := bootstrap.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Store.Close()
	if _, err = restored.Restore(backup); err != nil {
		t.Fatal(err)
	}
	root, err := restored.Get(parent)
	if err != nil || root.Confirmation == nil {
		t.Fatalf("restore confirmation missing: %+v %v", root, err)
	}
	c := root.Confirmation
	if _, err = restored.Confirm(parent, c.Kind, "approve", "fixture", "核对隔离备份", c.Candidate, c.Revision, c.PlanRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Resume(parent, "", true); err != nil {
		t.Fatal(err)
	}
	prepared, err := restored.PrepareEpicIntegration(parent)
	if err != nil {
		t.Fatalf("restored same Git history could not be integrated: %v", err)
	}
	if prepared.Task == nil || prepared.Task.Integration == nil || prepared.Task.Integration.GitHead == "" || prepared.Task.Integration.LocalRepository == leaf.CandidateProof.LocalRepository {
		t.Fatalf("restored Git identity missing: %+v", prepared.Task)
	}
}

func TestFailedUnboundArtifactCannotDisappearWhenGitIsInitialized(t *testing.T) {
	s := fixture(t)
	policyPath := filepath.Join(s.Root, ".ael", "policy.yaml")
	raw, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	policy["candidate_exclude"] = []string{"excluded.txt"}
	raw, _ = json.Marshal(policy)
	if err = os.WriteFile(policyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	parent := integrationParent(t, s, "epic", []core.Check{parentCheck("integration", "true")})
	id := create(t, s, core.Definition{ID: "leaf", Parent: parent, Title: "失败产物", Type: "bug", Goal: "verified", Scope: []string{"output.txt"}, Acceptance: []string{"结果"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -f output.txt"}, Covers: []string{"结果"}, Required: true}}})
	step(t, s, id, "fixture")
	claim, err := s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "excluded.txt"), []byte("unbound"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("unbound-fail-"), Result: core.StepResult{Status: "fail", Summary: "失败但留下产物", Artifacts: []string{"excluded.txt"}}}); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.Root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "isolated fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err = os.WriteFile(filepath.Join(s.Root, "base.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "base.txt")
	git("commit", "-qm", "isolated base")
	claim, err = s.Claim(id, "", "fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, "output.txt"), []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("unbound-pass-"), Result: core.StepResult{Status: "pass", Summary: "将实际结果写入 output.txt", Artifacts: []string{"output.txt"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(id, "unit", "checker"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_ARTIFACT_NOT_BOUND") {
		t.Fatalf("unbound failed artifact was forgotten: %v", err)
	}
}

// A later leaf that rewrites an earlier completed leaf's lines must not strand
// the parent: the earlier leaf stays completed and is re-proven on the parent.
func TestRewrittenLeafChangeClosesThroughParentRecheck(t *testing.T) {
	s := fixture(t)
	impact := integrationImpactFixture(t, s)
	parent := create(t, s, core.Definition{ID: "epic", Title: "改写闭环", Type: "epic", Goal: "verified", IntegrationImpact: impact, Acceptance: []string{"整体"}, Checks: []core.Check{parentCheck("integration", "true", "a", "b")}})
	originals := map[string]core.Task{}
	for _, id := range []string{"a", "b"} {
		create(t, s, core.Definition{ID: id, Parent: parent, Title: id, Type: "bug", Goal: "verified", Scope: []string{"shared.txt"}, Acceptance: []string{"文件非空"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "test -s shared.txt"}, Covers: []string{"文件非空"}, Required: true}}})
		step(t, s, id, "fixture-"+id)
		claim, err := s.Claim(id, "", "fixture-"+id, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.Root, "shared.txt"), []byte(strings.ToUpper(id)+" rewritten\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(core.Submission{Task: id, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("rewrite-"), Result: core.StepResult{Status: "pass", Summary: "顺序改写", Artifacts: []string{"shared.txt"}}}); err != nil {
			t.Fatal(err)
		}
		n, err := s.Verify(id, "unit", "checker-"+id)
		if err != nil || n.Task.Status != "completed" {
			t.Fatalf("leaf %s: %+v %v", id, n, err)
		}
		originals[id] = *n.Task
	}
	if _, err := s.PrepareEpicIntegration(parent); err == nil || !strings.Contains(err.Error(), "EPIC_RECHECK_REQUIRED") || !strings.Contains(err.Error(), "affected_tasks") {
		t.Fatalf("rewritten leaf did not ask for a parent recheck: %v", err)
	}
	current, err := s.Get(parent)
	if err != nil {
		t.Fatal(err)
	}
	d := current.Definition
	d.Checks = append(d.Checks, parentCheck("recheck", "test -s shared.txt", "a", "b"))
	if _, err = s.Edit(parent, d, "recheck rewritten leaf on the parent"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareEpicIntegration(parent); err != nil {
		t.Fatalf("superseded leaf still blocks integration: %v", err)
	}
	prepared, err := s.Get(parent)
	if err != nil || prepared.Integration.Leaves[0].ID != "a" || prepared.Integration.Leaves[0].Method != "content_changes_rechecked" {
		t.Fatalf("superseded leaf was not rechecked: %+v %v", prepared.Integration, err)
	}
	if _, err = s.Verify(parent, "integration", "checker"); err != nil {
		t.Fatal(err)
	}
	n, err := s.Verify(parent, "recheck", "checker")
	if err != nil || n.Task.Status != "completed" {
		t.Fatalf("parent did not close: %+v %v", n, err)
	}
	for id, original := range originals {
		after, err := s.Get(id)
		if err != nil || after.Status != "completed" || !after.Completed.Equal(*original.Completed) || len(after.Attempts) != len(original.Attempts) {
			t.Fatalf("leaf %s was reopened or rewritten: %+v %v", id, after, err)
		}
	}
}

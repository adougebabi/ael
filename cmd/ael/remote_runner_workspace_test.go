package main

import (
	"ael/internal/knowledge"
	"ael/internal/remote"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runnerWorkspaceFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, raw)
	}
	return string(raw)
}

func runnerWorkspaceFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source, runtime := filepath.Join(root, "source"), filepath.Join(root, "runtime")
	for _, path := range []string{source, runtime, filepath.Join(source, "spec")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	runnerWorkspaceFixtureGit(t, source, "init", "-b", "main")
	runnerWorkspaceFixtureGit(t, source, "config", "user.name", "Fixture")
	runnerWorkspaceFixtureGit(t, source, "config", "user.email", "fixture@example.invalid")
	files := map[string]string{".gitignore": ".ael/\nignored.log\n", "app.txt": "original\n", "delete.txt": "to delete\n", "spec/rules.md": "# Fixture rules\n\nWork only in the fixture.\n"}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	runnerWorkspaceFixtureGit(t, source, "add", "--", ".gitignore", "app.txt", "delete.txt", "spec/rules.md")
	runnerWorkspaceFixtureGit(t, source, "commit", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("staged\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runnerWorkspaceFixtureGit(t, source, "add", "--", "app.txt")
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("working\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "new.txt"), []byte("untracked\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledge.Index(source); err != nil {
		t.Fatal(err)
	}
	return source, runtime
}

func TestRunnerWorkspacePreservesCandidateAndSeparatesWrites(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	before, err := remote.CapturePortableCandidate(source, "fixture-repo")
	if err != nil || !before.InputsKnown {
		t.Fatalf("source candidate: %+v %v", before, err)
	}
	destination := filepath.Join(runtime, "responsibility-1")
	got, err := runnerCreateWorkspace(source, destination, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != before.ID || !got.InputsKnown {
		t.Fatalf("candidate changed in isolated checkout: source=%s copy=%s", before.ID, got.ID)
	}
	if err = knowledge.CheckFresh(destination); err != nil {
		t.Fatalf("knowledge index did not survive the copy: %v", err)
	}
	if status := runnerWorkspaceFixtureGit(t, destination, "status", "--porcelain"); !strings.Contains(status, "MM app.txt") || !strings.Contains(status, " D delete.txt") || !strings.Contains(status, "?? new.txt") {
		t.Fatalf("staged, unstaged, deleted or untracked state changed: %q", status)
	}
	if gitDir, err := os.Lstat(filepath.Join(destination, ".git")); err != nil || !gitDir.IsDir() {
		t.Fatalf("checkout shares a linked worktree metadata file: %v", err)
	}
	if remotes := runnerWorkspaceFixtureGit(t, destination, "remote"); strings.TrimSpace(remotes) != "" {
		t.Fatalf("temporary checkout retained a remote: %q", remotes)
	}
	if err = os.WriteFile(filepath.Join(destination, "app.txt"), []byte("copy only\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "working\n" {
		t.Fatalf("copy modified the source checkout: %q %v", raw, err)
	}
}

func TestRunnerWorkspaceRejectsUnknownInputsAndExistingDestination(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	existing := filepath.Join(runtime, "existing")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "keep")
	if err := os.WriteFile(marker, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runnerCreateWorkspace(source, existing, "fixture-repo"); err == nil {
		t.Fatal("existing workspace was accepted")
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "retain" {
		t.Fatalf("existing workspace was modified: %q %v", raw, err)
	}
	if err := os.WriteFile(filepath.Join(source, "ignored.log"), []byte("unknown input"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runnerCreateWorkspace(source, filepath.Join(runtime, "unknown"), "fixture-repo"); err == nil {
		t.Fatal("unclassified ignored input was silently dropped")
	}
}

func TestRunnerWorkspaceRejectsExcludedTrackedFilesAndSpecialIndexFlags(t *testing.T) {
	for _, kind := range []string{"tracked-runtime", "skip-worktree", "intent-to-add"} {
		t.Run(kind, func(t *testing.T) {
			source, runtime := runnerWorkspaceFixture(t)
			switch kind {
			case "tracked-runtime":
				path := filepath.Join(source, ".ael", "work-items", "retained")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				runnerWorkspaceFixtureGit(t, source, "add", "-f", "--", ".ael/work-items/retained")
				runnerWorkspaceFixtureGit(t, source, "commit", "-m", "fixture tracked runtime")
			case "skip-worktree":
				runnerWorkspaceFixtureGit(t, source, "update-index", "--skip-worktree", "--", "delete.txt")
			case "intent-to-add":
				if err := os.WriteFile(filepath.Join(source, "intent.txt"), []byte("fixture"), 0644); err != nil {
					t.Fatal(err)
				}
				runnerWorkspaceFixtureGit(t, source, "add", "-N", "--", "intent.txt")
			}
			dest := filepath.Join(runtime, "unsupported")
			if _, err := runnerCreateWorkspace(source, dest, "fixture-repo"); err == nil {
				t.Fatalf("%s was silently copied with different Git semantics", kind)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("%s was rejected only after creating a workspace: %v", kind, err)
			}
		})
	}
}

func TestRunnerWorkspacePreservesRepositoryLineEndingRules(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	path := filepath.Join(source, "eol.txt")
	if err := os.WriteFile(path, []byte("line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runnerWorkspaceFixtureGit(t, source, "-c", "core.autocrlf=false", "add", "--", "eol.txt")
	runnerWorkspaceFixtureGit(t, source, "commit", "-m", "fixture line endings")
	runnerWorkspaceFixtureGit(t, source, "config", "core.autocrlf", "true")
	if err := os.WriteFile(path, []byte("line\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(runtime, "eol")
	if _, err := runnerCreateWorkspace(source, dest, "fixture-repo"); err != nil {
		sourceStatus := runnerWorkspaceFixtureGit(t, source, "status", "--porcelain=v2", "--untracked-files=all")
		copyStatus := runnerWorkspaceFixtureGit(t, dest, "status", "--porcelain=v2", "--untracked-files=all")
		t.Fatalf("%v; source status=%q copy status=%q", err, sourceStatus, copyStatus)
	}
	runnerWorkspaceFixtureGit(t, source, "add", "--", "eol.txt")
	runnerWorkspaceFixtureGit(t, dest, "add", "--", "eol.txt")
	want := runnerWorkspaceFixtureGit(t, source, "show", ":eol.txt")
	got := runnerWorkspaceFixtureGit(t, dest, "show", ":eol.txt")
	if want != got || want != "line\n" {
		t.Fatalf("staging semantics changed: source=%q copy=%q", want, got)
	}
}

func TestRunnerWorkspacePreservesStagedMode(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	runnerWorkspaceFixtureGit(t, source, "update-index", "--chmod=+x", "--", "app.txt")
	want := runnerWorkspaceFixtureGit(t, source, "ls-files", "--stage", "--", "app.txt")
	if !strings.HasPrefix(want, "100755 ") {
		t.Fatalf("fixture did not stage the mode: %q", want)
	}
	dest := filepath.Join(runtime, "mode")
	if _, err := runnerCreateWorkspace(source, dest, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	if got := runnerWorkspaceFixtureGit(t, dest, "ls-files", "--stage", "--", "app.txt"); got != want {
		t.Fatalf("staged mode changed: source=%q copy=%q", want, got)
	}
}

func TestRunnerWorkspaceRejectsUncapturedLocalGitRules(t *testing.T) {
	for _, kind := range []string{"filter", "attributes"} {
		t.Run(kind, func(t *testing.T) {
			source, runtime := runnerWorkspaceFixture(t)
			if kind == "filter" {
				runnerWorkspaceFixtureGit(t, source, "config", "filter.fixture.clean", "fixture-filter")
			} else {
				if err := os.WriteFile(filepath.Join(source, ".git", "info", "attributes"), []byte("*.txt text\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(runtime, "unsupported-rules")
			if _, err := runnerCreateWorkspace(source, dest, "fixture-repo"); err == nil {
				t.Fatalf("uncaptured %s was accepted", kind)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("%s was rejected only after creating a workspace: %v", kind, err)
			}
		})
	}
}

func TestRunnerWorkspaceRejectsIncludedAndWorktreeConfig(t *testing.T) {
	for _, kind := range []string{"include", "worktree"} {
		t.Run(kind, func(t *testing.T) {
			source, runtime := runnerWorkspaceFixture(t)
			if kind == "include" {
				included := filepath.Join(source, ".git", "fixture-include")
				if err := os.WriteFile(included, []byte("[core]\n\tautocrlf = true\n"), 0600); err != nil {
					t.Fatal(err)
				}
				runnerWorkspaceFixtureGit(t, source, "config", "--local", "include.path", included)
			} else {
				runnerWorkspaceFixtureGit(t, source, "config", "--local", "extensions.worktreeConfig", "true")
				runnerWorkspaceFixtureGit(t, source, "config", "--worktree", "core.autocrlf", "true")
			}
			dest := filepath.Join(runtime, "unsupported-config")
			if _, err := runnerCreateWorkspace(source, dest, "fixture-repo"); err == nil {
				t.Fatalf("%s configuration was silently omitted", kind)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("%s was rejected only after creating a workspace: %v", kind, err)
			}
		})
	}
}

func TestRunnerWorkspaceChecksFiltersBeforeReadingCandidate(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	if err := os.WriteFile(filepath.Join(source, ".gitattributes"), []byte("app.txt filter=fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	marker, script := filepath.Join(runtime, "filter-ran"), filepath.Join(runtime, "filter.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf executed > '"+marker+"'\ncat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runnerWorkspaceFixtureGit(t, source, "config", "--local", "filter.fixture.clean", script)
	dest := filepath.Join(runtime, "filter-workspace")
	if _, err := runnerCreateWorkspace(source, dest, "fixture-repo"); err == nil {
		t.Fatal("unapproved Git filter was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Git filter ran before rejection: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("filter was rejected only after creating a workspace: %v", err)
	}
}

func TestRunnerWorkspaceCloneDoesNotRunSourceGitHook(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	marker := filepath.Join(runtime, "hook-ran")
	hook := filepath.Join(source, ".git", "hooks", "pre-upload-pack")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf executed > '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := runnerCreateWorkspace(source, filepath.Join(runtime, "hook-copy"), "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("source Git hook ran during the copy: %v", err)
	}
}

func TestRunnerWorkspaceRejectsUnreadableKnowledgeBeforeCopy(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	if err := os.WriteFile(filepath.Join(source, "spec", "rules.md"), []byte("---\ninvalid: [\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledge.Index(source); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(runtime, "bad-knowledge")
	if _, err := runnerCreateWorkspace(source, dest, "fixture-repo"); err == nil {
		t.Fatal("unreadable knowledge was accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("knowledge was rejected only after creating a workspace: %v", err)
	}
}

func TestRunnerWorkspaceContextUsesExecutionCopyAndOriginalProfile(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	workspace := filepath.Join(runtime, "context-copy")
	if _, err := runnerCreateWorkspace(source, workspace, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("different candidate\n"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate, err := remote.CapturePortableCandidate(workspace, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"repository": "fixture-repo", "candidate_id": candidate.ID, "validity": "current", "inputs_known": true, "sources": []any{}})
	})
	_, config, project, binding := runnerRecoveryFixture(t, handler)
	value, err := remoteContextCommandAtWorkspace("context show", args{Flags: map[string]string{"context": "fixture-context"}}, config, project, workspace, binding)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(remoteOutput)
	if result["workspace_matches"] != true || result["usable_for_context"] != true {
		t.Fatalf("context did not use the isolated workspace: %+v", result)
	}
}

func TestRunnerWorkspaceKeepsOldRelativeWriterOutOfNextCopy(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	source, runtime := runnerWorkspaceFixture(t)
	accepted := filepath.Join(runtime, "accepted")
	if _, err = runnerCreateWorkspace(source, accepted, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(runtime, "old")
	if _, err = runnerCreateWorkspace(accepted, old, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	code := `import os,pathlib,time
os.setsid()
pathlib.Path('writer-ready').write_text('ready')
deadline=time.monotonic()+15
while not pathlib.Path('writer-go').exists() and time.monotonic()<deadline: time.sleep(.01)
if pathlib.Path('writer-go').exists(): pathlib.Path('app.txt').write_text('late old write\n')
`
	cmd := exec.Command(python, "-c", code)
	cmd.Dir = old
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(old, "writer-ready")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = os.Stat(filepath.Join(old, "writer-ready")); err != nil {
		t.Fatalf("old writer did not start: %v", err)
	}
	next := filepath.Join(runtime, "next")
	if _, err = runnerCreateWorkspace(accepted, next, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(old, "writer-go"), []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(old, "app.txt")); err != nil || !strings.HasPrefix(string(raw), "late old write") {
		t.Fatalf("old fixture writer did not perform its write: %q %v", raw, err)
	}
	for _, workspace := range []string{source, accepted, next} {
		raw, err := os.ReadFile(filepath.Join(workspace, "app.txt"))
		if err != nil || string(raw) != "working\n" {
			t.Fatalf("old writer changed another workspace %s: %q %v", workspace, raw, err)
		}
	}
}

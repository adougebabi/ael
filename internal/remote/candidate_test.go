package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func candidateRepos(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	a, b := filepath.Join(root, "checkout-a"), filepath.Join(root, "checkout-b")
	if err := os.Mkdir(a, 0700); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, a, "init", "-b", "main")
	fixtureGit(t, a, "config", "user.name", "Fixture")
	fixtureGit(t, a, "config", "user.email", "fixture@example.invalid")
	for name, body := range map[string]string{"app.txt": "original\n", ".gitignore": "ignored.log\n.ael/\n"} {
		if err := os.WriteFile(filepath.Join(a, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, a, "add", "--", "app.txt", ".gitignore")
	fixtureGit(t, a, "commit", "-m", "initial")
	fixtureGit(t, root, "clone", "--no-local", a, b)
	fixtureGit(t, b, "config", "user.name", "Fixture")
	fixtureGit(t, b, "config", "user.email", "fixture@example.invalid")
	return a, b
}

func TestPortableCandidateAcrossCheckoutsAndCommits(t *testing.T) {
	a, b := candidateRepos(t)
	if err := os.MkdirAll(filepath.Join(a, ".ael", "outcomes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b, ".ael", "outcomes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, ".ael", "remote.json"), []byte(`{"profile":"one"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".ael", "remote.json"), []byte(`{"profile":"two"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".ael", "outcomes", "report.txt"), []byte("runtime report"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "ignored.log"), []byte("log"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(b, "app.txt"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CapturePortableCandidate(b, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Commit != second.Commit {
		t.Fatalf("equivalent checkouts differ: %+v %+v", first, second)
	}
	other, err := CapturePortableCandidate(b, "repository-two")
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID {
		t.Fatal("distinct explicit repositories aligned")
	}
	fixtureGit(t, b, "commit", "--allow-empty", "-m", "new commit on same branch")
	newer, err := CapturePortableCandidate(b, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if newer.ID == first.ID || newer.Commit == first.Commit {
		t.Fatal("different commits were treated as equivalent")
	}
}

func TestPortableCandidateReportsWorktreeChangesAndRejectsUnknownInputs(t *testing.T) {
	a, _ := candidateRepos(t)
	before, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a, "app.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	changes := ComparePortableCandidates(before, after)
	if after.ID == before.ID || len(changes.Paths) != 2 || changes.Paths[0] != "app.txt" || changes.Paths[1] != "new.txt" {
		t.Fatalf("changes not identified: %+v", changes)
	}
	if err = os.WriteFile(filepath.Join(a, ".env"), []byte("synthetic-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = CapturePortableCandidate(a, "repository-one"); err == nil {
		t.Fatal("unignored secret was included in a portable manifest")
	}
}

func TestPortableCandidateSupportsLinkedWorktreeAndRejectsForgedManifest(t *testing.T) {
	a, _ := candidateRepos(t)
	worktree := filepath.Join(t.TempDir(), "linked")
	fixtureGit(t, a, "worktree", "add", "--detach", worktree)
	first, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CapturePortableCandidate(worktree, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("linked worktree metadata changed candidate: %s != %s", first.ID, second.ID)
	}
	second.Files[0].SHA256 = "forged"
	if err = ValidatePortableCandidate(second); err == nil {
		t.Fatal("forged manifest accepted")
	}
	if ComparePortableCandidates(second, second).ExactlyEqual {
		t.Fatal("invalid manifests were considered equivalent")
	}
}

func TestPortableCandidateDoesNotHideIgnoredCommittedInputs(t *testing.T) {
	a, b := candidateRepos(t)
	for _, dir := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.log\n.ael/\napp.txt\n.env\n"), 0644); err != nil {
			t.Fatal(err)
		}
		fixtureGit(t, dir, "rm", "--cached", "app.txt")
		if err := os.MkdirAll(filepath.Join(dir, ".ael"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("synthetic-secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(a, "app.txt"), []byte("first\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "app.txt"), []byte("second\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, ".ael", "policy.yaml"), []byte("policy: a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".ael", "policy.yaml"), []byte("policy: b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CapturePortableCandidate(b, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	diff := ComparePortableCandidates(first, second)
	if first.ID == second.ID || len(diff.Paths) != 2 || diff.Paths[0] != ".ael/policy.yaml" || diff.Paths[1] != "app.txt" {
		t.Fatalf("ignored committed or policy inputs were hidden: %+v", diff)
	}
	if first.InputsKnown || second.InputsKnown {
		t.Fatal("ignored secret was treated as a proved input set")
	}
}

func TestPortableCandidateRejectsChangesDuringCapture(t *testing.T) {
	a, _ := candidateRepos(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"rev-parse --verify HEAD^{commit}"*)
    if [ -f "$AEL_TEST_HEAD_MARKER" ]; then
      printf 'changed during capture\n' > "$AEL_TEST_SOURCE"
    else
      : > "$AEL_TEST_HEAD_MARKER"
    fi
    ;;
esac
exec "$AEL_TEST_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AEL_TEST_HEAD_MARKER", filepath.Join(t.TempDir(), "marker"))
	t.Setenv("AEL_TEST_SOURCE", filepath.Join(a, "app.txt"))
	t.Setenv("AEL_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := CapturePortableCandidate(a, "repository-one"); err == nil || !strings.Contains(err.Error(), "CANDIDATE_CHANGED_DURING_CAPTURE") {
		t.Fatalf("concurrent change was not detected: %v", err)
	}
}

func TestPortableCandidateNeverClaimsContinuousStabilityFromScans(t *testing.T) {
	a, _ := candidateRepos(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"rev-parse --verify HEAD^{commit}"*)
    count=0
    if [ -f "$AEL_TEST_COUNTER" ]; then count=$(cat "$AEL_TEST_COUNTER"); fi
    count=$((count + 1))
    printf '%s' "$count" > "$AEL_TEST_COUNTER"
    if [ "$count" -eq 4 ]; then printf 'late external change\n' > "$AEL_TEST_SOURCE"; fi
    ;;
esac
exec "$AEL_TEST_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AEL_TEST_COUNTER", filepath.Join(t.TempDir(), "counter"))
	t.Setenv("AEL_TEST_SOURCE", filepath.Join(a, "app.txt"))
	t.Setenv("AEL_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	candidate, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.StabilityProven || ComparePortableCandidates(candidate, candidate).ExactlyEqual {
		t.Fatal("point-in-time scans claimed continuous stability")
	}
	data, err := os.ReadFile(filepath.Join(a, "app.txt"))
	if err != nil || string(data) != "late external change\n" {
		t.Fatalf("fixture did not change after the last file read: %q %v", data, err)
	}
}

func TestPortableCandidateUsesGitModeWhenFileModeCannotBeTrusted(t *testing.T) {
	a, b := candidateRepos(t)
	fixtureGit(t, b, "config", "core.filemode", "false")
	if err := os.Chmod(filepath.Join(b, "app.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	first, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CapturePortableCandidate(b, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("filemode-disabled checkout changed manifest: %s != %s", first.ID, second.ID)
	}
	if second.InputsKnown || ComparePortableCandidates(first, second).ExactlyEqual {
		t.Fatal("unreliable file permissions were treated as proved equivalent")
	}
}

func TestPortableCandidateIncludesStagedContent(t *testing.T) {
	a, _ := candidateRepos(t)
	before, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a, "app.txt"), []byte("staged change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, a, "add", "app.txt")
	if err = os.WriteFile(filepath.Join(a, "app.txt"), []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	diff := ComparePortableCandidates(before, after)
	if before.ID == after.ID || len(diff.Paths) != 1 || diff.Paths[0] != "app.txt" || diff.ExactlyEqual {
		t.Fatalf("staged input was hidden: %+v", diff)
	}
}

func TestPortableCandidateMarksIgnoredNonMarkdownKnowledgeUnknown(t *testing.T) {
	a, _ := candidateRepos(t)
	if err := os.MkdirAll(filepath.Join(a, ".ael", "knowledge"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, ".ael", "knowledge", "config.yaml"), []byte("mode: one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	candidate, err := CapturePortableCandidate(a, "repository-one")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.InputsKnown || ComparePortableCandidates(candidate, candidate).ExactlyEqual {
		t.Fatal("ignored non-Markdown knowledge was treated as known input")
	}
}

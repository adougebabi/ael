package main

import (
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/remote"
	"ael/internal/store"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// runnerWorkspaceGit runs only locally selected Git commands. It does not
// inherit caller-provided Git overrides or use configured hooks and pagers.
func runnerWorkspaceGit(dir string, input []byte, args ...string) ([]byte, error) {
	argv := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.pager=cat"}, args...)
	cmd := exec.Command("git", argv...)
	cmd.Dir = dir
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if name == "PATH" || name == "TMPDIR" || name == "LANG" || strings.HasPrefix(name, "LC_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_LFS_SKIP_SMUDGE=1", "GIT_OPTIONAL_LOCKS=0")
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	return cmd.Output()
}

type runnerWorkspaceOption struct{ key, value string }

func runnerWorkspaceLocalOption(source, key string) (string, bool, error) {
	raw, err := runnerWorkspaceGit(source, nil, "config", "--local", "--get-all", key)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", false, nil
		}
		return "", false, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect local Git configuration")
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", false, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "unsupported local Git configuration")
	}
	return value, true, nil
}

func runnerWorkspaceOptions(source string) ([]runnerWorkspaceOption, error) {
	var result []runnerWorkspaceOption
	_, err := runnerWorkspaceGit(source, nil, "config", "--local", "--name-only", "--get-regexp", `^include(if)?[.]`)
	if err == nil {
		return nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "included Git configuration needs explicit handling")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return nil, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect Git configuration includes")
	}
	if _, present, err := runnerWorkspaceLocalOption(source, "extensions.worktreeconfig"); err != nil {
		return nil, err
	} else if present {
		return nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "per-worktree Git configuration needs explicit handling")
	}
	allowed := map[string][]string{
		"core.autocrlf":          {"true", "false", "input"},
		"core.eol":               {"lf", "crlf", "native"},
		"core.safecrlf":          {"true", "false", "warn"},
		"core.ignorecase":        {"true", "false"},
		"core.precomposeunicode": {"true", "false"},
		"core.symlinks":          {"true", "false"},
	}
	for _, key := range []string{"core.autocrlf", "core.eol", "core.safecrlf", "core.ignorecase", "core.precomposeunicode", "core.symlinks"} {
		value, present, err := runnerWorkspaceLocalOption(source, key)
		if err != nil {
			return nil, err
		}
		if present {
			value = strings.ToLower(value)
			if !slices.Contains(allowed[key], value) {
				return nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "unsupported local Git conversion setting")
			}
			result = append(result, runnerWorkspaceOption{key, value})
		}
	}
	if _, present, err := runnerWorkspaceLocalOption(source, "core.attributesfile"); err != nil {
		return nil, err
	} else if present {
		return nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "local Git attributes outside the candidate need explicit handling")
	}
	_, err = runnerWorkspaceGit(source, nil, "config", "--local", "--name-only", "--get-regexp", `^filter[.].*[.](clean|smudge|process|required)$`)
	if err == nil {
		return nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "local Git filters need explicit handling")
	}
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return nil, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect local Git filters")
	}
	path, err := runnerWorkspaceGit(source, nil, "rev-parse", "--git-path", "info/attributes")
	if err != nil {
		return nil, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect local Git attributes")
	}
	attributes := strings.TrimSpace(string(path))
	if !filepath.IsAbs(attributes) {
		attributes = filepath.Join(source, attributes)
	}
	info, err := os.Lstat(attributes)
	if err == nil && (!info.Mode().IsRegular() || info.Size() != 0) {
		return nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "local Git attributes need explicit handling")
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return result, nil
}

func runnerWorkspaceIndex(source string, known map[string]bool) ([]byte, []byte, []byte, error) {
	stage, err := runnerWorkspaceGit(source, nil, "ls-files", "--stage", "-z", "--")
	if err != nil {
		return nil, nil, nil, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect the Git index")
	}
	flags, err := runnerWorkspaceGit(source, nil, "ls-files", "-v", "-z", "--")
	if err != nil {
		return nil, nil, nil, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect Git index flags")
	}
	for _, item := range bytes.Split(flags, []byte{0}) {
		if len(item) == 0 {
			continue
		}
		if len(item) < 3 || item[0] != 'H' || item[1] != ' ' || !known[string(item[2:])] {
			return nil, nil, nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "tracked runtime files or special Git index flags need explicit handling")
		}
	}
	status, err := runnerWorkspaceGit(source, nil, "-c", "status.renames=false", "status", "--porcelain=v2", "--untracked-files=all", "-z")
	if err != nil {
		return nil, nil, nil, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect workspace status")
	}
	for _, item := range bytes.Split(status, []byte{0}) {
		if bytes.HasPrefix(item, []byte("1 .A ")) {
			return nil, nil, nil, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "intent-to-add index entries need explicit handling")
		}
	}
	// Porcelain status may differ only because the source index has stale
	// stat-cache entries; compare the actual unstaged patch instead.
	unstaged, err := runnerWorkspaceGit(source, nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-renames", "--")
	if err != nil {
		return nil, nil, nil, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect unstaged changes")
	}
	return stage, flags, unstaged, nil
}

func runnerCopyWorkspaceFile(source, destination, rel, expected string) error {
	from, err := store.SafePath(source, filepath.FromSlash(rel))
	if err != nil {
		return core.Error("RUNNER_WORKSPACE_UNSAFE", "source path is not safe")
	}
	before, err := os.Lstat(from)
	if err != nil || !before.Mode().IsRegular() {
		return core.Error("RUNNER_WORKSPACE_UNSAFE", "source file is missing or not regular")
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "source file changed before copying")
	}
	to, err := store.SafePath(destination, filepath.FromSlash(rel))
	if err != nil {
		return core.Error("RUNNER_WORKSPACE_UNSAFE", "destination path is not safe")
	}
	if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, before.Mode().Perm())
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), in)
	if copyErr == nil {
		copyErr = out.Chmod(before.Mode().Perm())
	}
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	after, err := in.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() || (expected != "" && hex.EncodeToString(hash.Sum(nil)) != expected) {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "source file changed while copying")
	}
	return nil
}

// runnerCreateWorkspace creates a separate checkout representing exactly the
// observed candidate. It does not by itself restrict a same-user process from
// accessing other host paths and is not an OS security sandbox.
func runnerCreateWorkspace(source, destination, repository string) (remote.PortableCandidate, error) {
	var result remote.PortableCandidate
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil || !filepath.IsAbs(source) {
		return result, core.Error("RUNNER_WORKSPACE_UNAVAILABLE", "source checkout is unavailable")
	}
	destination, err = resolvedExternal(destination, source)
	if err != nil {
		return result, err
	}
	// Inspect configuration before candidate capture: git diff may invoke a
	// configured clean filter while merely checking a dirty source file.
	options, err := runnerWorkspaceOptions(source)
	if err != nil {
		return result, err
	}
	result, err = remote.CapturePortableCandidate(source, repository)
	if err != nil {
		return result, err
	}
	if !result.InputsKnown {
		return result, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "ignored or unverified inputs cannot be copied safely")
	}
	if err = knowledge.CheckFresh(source); err != nil {
		return result, err
	}
	if _, err = knowledge.ReadAll(source); err != nil {
		return result, err
	}
	paths, err := runnerWorkspaceGit(source, nil, "diff", "--cached", "--name-only", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		return result, core.Error("RUNNER_WORKSPACE_GIT", "cannot inspect staged files")
	}
	known := make(map[string]bool, len(result.Files))
	for _, file := range result.Files {
		known[file.Path] = true
	}
	stage, flags, unstaged, err := runnerWorkspaceIndex(source, known)
	if err != nil {
		return result, err
	}
	for _, path := range bytes.Split(paths, []byte{0}) {
		if len(path) > 0 && !known[string(path)] {
			return result, core.Error("RUNNER_WORKSPACE_INPUTS_UNKNOWN", "staged files include excluded runtime content")
		}
	}
	patch, err := runnerWorkspaceGit(source, nil, "diff", "--cached", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-renames", "HEAD", "--")
	if err != nil {
		return result, core.Error("RUNNER_WORKSPACE_GIT", "cannot capture staged changes")
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return result, core.Error("RUNNER_RECOVERY_REQUIRED", "workspace already exists or cannot be created; inspect it before retrying")
	}
	if _, err = runnerWorkspaceGit(filepath.Dir(destination), nil, "clone", "--no-local", "--no-hardlinks", "--no-checkout", "--", source, destination); err != nil {
		return result, core.Error("RUNNER_WORKSPACE_GIT", "cannot create isolated checkout; inspect the retained directory")
	}
	head, err := runnerWorkspaceGit(destination, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || strings.TrimSpace(string(head)) != result.Commit {
		return result, core.Error("RUNNER_WORKSPACE_CHANGED", "source HEAD changed while creating the checkout")
	}
	if _, err = runnerWorkspaceGit(destination, nil, "remote", "remove", "origin"); err != nil {
		return result, core.Error("RUNNER_WORKSPACE_GIT", "cannot remove the temporary checkout remote")
	}
	for _, option := range options {
		if _, err = runnerWorkspaceGit(destination, nil, "config", "--local", option.key, option.value); err != nil {
			return result, core.Error("RUNNER_WORKSPACE_GIT", "cannot preserve local Git conversion settings")
		}
	}
	if _, err = runnerWorkspaceGit(destination, nil, "read-tree", "HEAD"); err != nil {
		return result, core.Error("RUNNER_WORKSPACE_GIT", "cannot initialize the temporary index")
	}
	if len(patch) > 0 {
		if _, err = runnerWorkspaceGit(destination, patch, "apply", "--cached", "--binary", "-"); err != nil {
			return result, core.Error("RUNNER_WORKSPACE_GIT", "cannot recreate the staged changes")
		}
	}
	for _, file := range result.Files {
		if !file.Deleted {
			if err = runnerCopyWorkspaceFile(source, destination, file.Path, file.SHA256); err != nil {
				return result, err
			}
		}
	}
	const catalog = ".ael/knowledge-sources.json"
	if err = runnerCopyWorkspaceFile(source, destination, catalog, ""); err != nil {
		return result, core.Error("RUNNER_WORKSPACE_UNAVAILABLE", "cannot copy the verified knowledge inventory")
	}
	if err = knowledge.CheckFresh(destination); err != nil {
		return result, err
	}
	if _, err = knowledge.ReadAll(destination); err != nil {
		return result, err
	}
	copyCandidate, err := remote.CapturePortableCandidate(destination, repository)
	if err != nil {
		return result, err
	}
	current, err := remote.CapturePortableCandidate(source, repository)
	if err != nil {
		return result, err
	}
	if err = knowledge.CheckFresh(source); err != nil {
		return result, err
	}
	if _, err = knowledge.ReadAll(source); err != nil {
		return result, err
	}
	currentOptions, err := runnerWorkspaceOptions(source)
	if err != nil {
		return result, err
	}
	if !slices.Equal(options, currentOptions) {
		return result, core.Error("RUNNER_WORKSPACE_CHANGED", "local Git conversion settings changed during copying")
	}
	if !copyCandidate.InputsKnown || !current.InputsKnown || copyCandidate.ID != result.ID || current.ID != result.ID {
		return result, errors.New("RUNNER_WORKSPACE_CHANGED: copied or source candidate differs from the observed candidate")
	}
	copyStage, copyFlags, copyUnstaged, err := runnerWorkspaceIndex(destination, known)
	if err != nil {
		return result, err
	}
	currentStage, currentFlags, currentUnstaged, err := runnerWorkspaceIndex(source, known)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(stage, copyStage) || !bytes.Equal(stage, currentStage) || !bytes.Equal(flags, copyFlags) || !bytes.Equal(flags, currentFlags) || !bytes.Equal(unstaged, copyUnstaged) || !bytes.Equal(unstaged, currentUnstaged) {
		return result, core.Error("RUNNER_WORKSPACE_CHANGED", "Git index or unstaged changes differ after copying")
	}
	return copyCandidate, nil
}

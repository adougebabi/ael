package main

import (
	"ael/internal/core"
	"ael/internal/execution"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type remoteApprovedCheck struct {
	ID      string   `json:"id"`
	Command []string `json:"command"`
}

type remoteRunnerConfig struct {
	ProjectID    string                `json:"project_id"`
	Repository   string                `json:"repository"`
	CheckoutID   string                `json:"checkout_id"`
	WorkspaceID  string                `json:"workspace_id"`
	RunnerID     string                `json:"runner_id"`
	Workspace    string                `json:"workspace"`
	RuntimeDir   string                `json:"runtime_dir"`
	Capabilities []string              `json:"capabilities"`
	Executor     execution.Config      `json:"executor"`
	Checks       []remoteApprovedCheck `json:"checks"`
}

var remoteRunnerID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func resolvedExternal(pathname, workspace string) (string, error) {
	if !filepath.IsAbs(pathname) {
		return "", core.Error("RUNNER_CONFIG_INVALID", "config and runtime paths must be absolute")
	}
	pathname = filepath.Clean(pathname)
	ancestor := pathname
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", core.Error("RUNNER_CONFIG_INVALID", "cannot resolve external path")
		}
		ancestor = parent
	}
	real, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	rest, err := filepath.Rel(ancestor, pathname)
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(real, rest)
	rel, err := filepath.Rel(workspace, resolved)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", core.Error("RUNNER_CONFIG_INVALID", "config and runtime must be outside the checkout")
	}
	return resolved, nil
}

func loadRemoteRunnerConfig(configPath, project string, binding remote.Binding) (remoteRunnerConfig, error) {
	var cfg remoteRunnerConfig
	workspace, err := filepath.Abs(project)
	if err != nil {
		return cfg, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return cfg, core.Error("RUNNER_WORKSPACE_UNAVAILABLE", "checkout is not accessible")
	}
	original, err := os.Lstat(configPath)
	if err != nil || !original.Mode().IsRegular() {
		return cfg, core.Error("RUNNER_CONFIG_PERMISSIONS", "configuration must be a regular file, not a link")
	}
	configPath, err = resolvedExternal(configPath, workspace)
	if err != nil {
		return cfg, err
	}
	info, err := os.Lstat(configPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(configPath) != nil || securityfs.SingleLinkRegular(configPath) != nil {
		return cfg, core.Error("RUNNER_CONFIG_PERMISSIONS", "use a private, unlinked configuration file outside the checkout")
	}
	f, err := os.Open(configPath)
	if err != nil {
		return cfg, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	_ = f.Close()
	if readErr != nil || len(raw) > 1<<20 {
		return cfg, core.Error("RUNNER_CONFIG_INVALID", "runner JSON is unreadable or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&cfg)
	var extra any
	if err == nil && decoder.Decode(&extra) != io.EOF {
		err = core.Error("RUNNER_CONFIG_INVALID", "trailing data")
	}
	if err != nil {
		return cfg, core.Error("RUNNER_CONFIG_INVALID", "invalid runner JSON")
	}
	declared, err := filepath.EvalSymlinks(cfg.Workspace)
	if err != nil || !filepath.IsAbs(cfg.Workspace) || declared != workspace || cfg.ProjectID != binding.ProjectID {
		return cfg, core.Error("RUNNER_WORKSPACE_UNAUTHORIZED", "configuration does not authorize this bound project and checkout")
	}
	for _, value := range []string{cfg.Repository, cfg.CheckoutID, cfg.WorkspaceID, cfg.RunnerID} {
		if !remoteRunnerID.MatchString(value) {
			return cfg, core.Error("RUNNER_CONFIG_INVALID", "repository, checkout, workspace and runner IDs are required")
		}
	}
	if len(cfg.Capabilities) == 0 || len(cfg.Capabilities) > 32 {
		return cfg, core.Error("RUNNER_CONFIG_INVALID", "declare runner capabilities")
	}
	seen := map[string]bool{}
	for _, capability := range cfg.Capabilities {
		if !remoteRunnerID.MatchString(capability) || seen[capability] {
			return cfg, core.Error("RUNNER_CONFIG_INVALID", "invalid or duplicate capability")
		}
		seen[capability] = true
	}
	if err = execution.PrepareConfig(&cfg.Executor); err != nil {
		return cfg, err
	}
	if !cfg.Executor.Codex && runnerForbiddenCommand(cfg.Executor.Command) {
		return cfg, core.Error("RUNNER_COMMAND_FORBIDDEN", "runner cannot start a loop or publish")
	}
	if cfg.Executor.MaxSteps > 100 || cfg.Executor.TimeoutSeconds > 86400 || (cfg.Executor.Workspace != "" && cfg.Executor.Workspace != cfg.Workspace) {
		return cfg, core.Error("RUNNER_CONFIG_INVALID", "invalid executor bounds or workspace")
	}
	checks := map[string]bool{}
	for _, check := range cfg.Checks {
		if !remoteRunnerID.MatchString(check.ID) || checks[check.ID] || len(check.Command) == 0 {
			return cfg, core.Error("RUNNER_CONFIG_INVALID", "invalid or duplicate approved check")
		}
		checks[check.ID] = true
	}
	if info, statErr := os.Lstat(cfg.RuntimeDir); statErr == nil && !info.IsDir() {
		return cfg, core.Error("RUNNER_CONFIG_PERMISSIONS", "runtime directory must not be a link")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return cfg, statErr
	}
	runtimePath, err := resolvedExternal(cfg.RuntimeDir, workspace)
	if err != nil {
		return cfg, err
	}
	if err = os.MkdirAll(runtimePath, 0700); err != nil {
		return cfg, err
	}
	info, err = os.Lstat(runtimePath)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(runtimePath) != nil {
		return cfg, core.Error("RUNNER_CONFIG_PERMISSIONS", "runner runtime directory must be private")
	}
	cfg.Workspace, cfg.RuntimeDir = workspace, runtimePath
	return cfg, nil
}

func remoteCheckAuthorized(config remoteRunnerConfig, check core.Check) bool {
	if runnerForbiddenCommand(check.Command) {
		return false
	}
	for _, approved := range config.Checks {
		if approved.ID == check.ID && slices.Equal(approved.Command, check.Command) {
			return true
		}
	}
	return false
}

func runnerForbiddenCommand(command []string) bool {
	if len(command) == 0 {
		return true
	}
	base := strings.ToLower(filepath.Base(command[0]))
	for _, arg := range command[1:] {
		word := strings.ToLower(strings.TrimSpace(arg))
		if base == "ael" && (word == "loop" || word == "runner") {
			return true
		}
		if base == "git" && (word == "push" || word == "tag") {
			return true
		}
		if base == "gh" && word == "release" {
			return true
		}
		if (base == "npm" || base == "cargo") && word == "publish" {
			return true
		}
		if base == "docker" && word == "push" {
			return true
		}
	}
	return false
}

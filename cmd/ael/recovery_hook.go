package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/projectmode"
	"ael/internal/remote"
	"ael/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const recoveryRule = "After compaction, session resume, task switch, or uncertain progress, run the current project's recovery check first. Verify Server/project, parent/leaf task, step, versions, passed checks and latest blocker. Server records govern the next action. Continue the unfinished item. Preserve accepted work, task identity and scope. Until online verification succeeds, limit work to read-only inspection and saving the scene."

var recoverySessionID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,200}$`)

func recoveryHook(a args) (any, error) {
	if a.get("host", "") != "codex" && a.get("host", "") != "claude" {
		return nil, core.Error("RECOVERY_HOST_REQUIRED", "provide --host codex or claude")
	}
	var event struct {
		Session   string `json:"session_id"`
		Directory string `json:"cwd"`
		Event     string `json:"hook_event_name"`
		Source    string `json:"source"`
	}
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10))
	if decoder.Decode(&event) != nil || !recoverySessionID.MatchString(event.Session) || !filepath.IsAbs(event.Directory) {
		return nil, core.Error("RECOVERY_HOOK_INPUT_INVALID", "expected bounded host event with cwd and session_id")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, core.Error("RECOVERY_HOOK_INPUT_INVALID", "trailing host event data")
	}
	if event.Event != "PreCompact" && event.Event != "SessionStart" {
		return nil, core.Error("RECOVERY_HOOK_EVENT_UNSUPPORTED", "expected PreCompact or SessionStart")
	}
	root, err := bootstrap.Canonical(a.get("project", event.Directory))
	if err != nil {
		return nil, err
	}
	cwd, err := bootstrap.Canonical(event.Directory)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, cwd)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, core.Error("RECOVERY_HOOK_BINDING_CONFLICT", "host cwd is outside the selected checkout")
	}
	binding, exists, err := remote.ReadBinding(root)
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]any{"continue": true}, nil
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	a.Flags["session"] = event.Session
	path, profile, _, err := recoveryLocation(a, config, root, binding)
	if err != nil {
		return nil, err
	}
	if event.Event == "PreCompact" {
		unlock, err := lockAgentOperation(path)
		if err != nil {
			return nil, err
		}
		defer unlock()
		index, err := readRecovery(path)
		if os.IsNotExist(err) {
			return map[string]any{"continue": true, "systemMessage": "AEL has no checkpoint for this session. Save one explicitly before continuing."}, nil
		}
		if err != nil {
			return nil, err
		}
		index.Verified = false
		if err = saveRecovery(path, index, profile.Token); err != nil {
			return nil, err
		}
		return map[string]any{"continue": true}, nil
	}
	// Only fixed rules and a bounded identifier enter privileged hook context.
	// Neither task text nor the checkpoint body is promoted to instructions.
	launcher := "'" + strings.ReplaceAll(filepath.Join(root, "ael"), "'", "'\\''") + "'"
	configArgument := "'" + strings.ReplaceAll(config, "'", "'\\''") + "'"
	command := fmt.Sprintf("%s recovery check --session %s --config-dir %s --json", launcher, event.Session, configArgument)
	contextText := recoveryRule + " Run `" + command + "`. If no checkpoint exists, prepare tools using session start --session " + event.Session + ", then bind task recovery using recovery save --session " + event.Session + " --runner RUNNER. The checkpoint is an index outside the checkout."
	toolPath, toolProfile, _, toolErr := toolSessionLocation(a, config, root, binding)
	if toolErr != nil {
		return nil, toolErr
	}
	if session, readErr := readToolSession(toolPath); readErr == nil {
		if err = validateToolSession(session, toolProfile, root, binding); err != nil {
			return nil, err
		}
		contextText += " Use the frozen AEL Skill referenced by the current tool session at " + session.SkillPath + "."
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "additionalContext": contextText}}, nil
}

func recoveryHostCommand(a args) (any, error) {
	host := a.get("host", "")
	if host != "codex" && host != "claude" {
		return nil, core.Error("RECOVERY_HOST_REQUIRED", "provide --host codex or claude")
	}
	root, err := bootstrap.Canonical(a.get("project", "."))
	if err != nil {
		return nil, err
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	if _, exists, err := remote.ReadBinding(root); err != nil || !exists {
		return nil, core.Error("REMOTE_BINDING_REQUIRED", "connect this checkout before installing recovery hooks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, host, "--version").Output()
	result := map[string]any{"host": host, "fallback": "./ael recovery check --session HOST_SESSION_ID --json", "status": "unsupported_or_unavailable", "automatic_injection_verified": false}
	if err != nil {
		return result, nil
	}
	result["host_version"] = strings.TrimSpace(string(version))
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return nil, homeErr
	}
	userDir := os.Getenv("CODEX_HOME")
	if userDir == "" {
		userDir = filepath.Join(home, ".codex")
	}
	userFiles := []string{"hooks.json", "config.toml"}
	if host == "claude" {
		userDir = os.Getenv("CLAUDE_CONFIG_DIR")
		if userDir == "" {
			userDir = filepath.Join(home, ".claude")
		}
		userFiles = []string{"settings.json"}
	}
	for _, filename := range userFiles {
		file, openErr := os.Open(filepath.Join(userDir, filename))
		if os.IsNotExist(openErr) {
			continue
		}
		if openErr != nil {
			return nil, openErr
		}
		body, readErr := io.ReadAll(io.LimitReader(file, 1<<20))
		file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if strings.Contains(string(body), "recovery hook") {
			return nil, core.Error("RECOVERY_HOOK_CONFLICT", "a user-level recovery hook already exists, review it before adding project hooks")
		}
	}
	// Capability evidence is limited to locally detected supported families.
	// Trust and actual lifecycle execution remain separate observable states.
	help, helpErr := exec.CommandContext(ctx, host, "--help").Output()
	if helpErr != nil {
		return result, nil
	}
	if host == "codex" && !strings.Contains(string(help), "hook-trust") {
		return result, nil
	}
	if host == "claude" && (!strings.HasPrefix(strings.TrimSpace(string(version)), "2.") || !strings.Contains(string(help), "include-hook-events")) {
		return result, nil
	}
	filename := ".codex/hooks.json"
	if host == "claude" {
		filename = ".claude/settings.json"
	}
	path, err := store.SafePath(root, filename)
	if err != nil {
		return nil, err
	}
	unlock, err := projectmode.Lock(root, !a.has("dry-run"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	settings := map[string]any{}
	raw, err := os.ReadFile(path)
	mode := os.FileMode(0644)
	if err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, statErr
		}
		mode = info.Mode().Perm()
		if len(raw) > 1<<20 || json.Unmarshal(raw, &settings) != nil || settings == nil {
			return nil, core.Error("RECOVERY_HOOK_CONFLICT", "existing host settings must be a JSON object")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		if settings["hooks"] != nil {
			return nil, core.Error("RECOVERY_HOOK_CONFLICT", "existing hooks must be an object")
		}
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	// Resolve from the project root without executing arbitrary Server config.
	command := "'" + strings.ReplaceAll(filepath.Join(root, "ael"), "'", "'\\''") + "' recovery hook --host " + host + " --config-dir '" + strings.ReplaceAll(config, "'", "'\\''") + "' --json"
	changed := false
	for _, event := range []string{"PreCompact", "SessionStart"} {
		groups, ok := hooks[event].([]any)
		if !ok && hooks[event] != nil {
			return nil, core.Error("RECOVERY_HOOK_CONFLICT", "existing event configuration must be an array")
		}
		matcher := "manual|auto"
		if event == "SessionStart" {
			matcher = "startup|resume|clear|compact|fork"
		}
		wanted := map[string]any{"matcher": matcher, "hooks": []any{map[string]any{"type": "command", "command": command, "timeout": float64(5)}}}
		want, _ := json.Marshal(wanted)
		found := false
		for _, group := range groups {
			encoded, _ := json.Marshal(group)
			if strings.Contains(string(encoded), "recovery hook") {
				if string(encoded) != string(want) {
					return nil, core.Error("RECOVERY_HOOK_CONFLICT", "existing recovery hook differs, review it before installing another")
				}
				if found {
					return nil, core.Error("RECOVERY_HOOK_CONFLICT", "duplicate recovery hooks require review")
				}
				found = true
			}
		}
		if !found {
			hooks[event], changed = append(groups, wanted), true
		}
	}
	if changed && !a.has("dry-run") {
		latest, readErr := os.ReadFile(path)
		if (readErr != nil && !os.IsNotExist(readErr)) || string(latest) != string(raw) {
			return nil, core.Error("RECOVERY_HOOK_CONFLICT", "host settings changed during installation")
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
		body, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = store.Atomic(path, append(body, '\n'), mode); err != nil {
			return nil, err
		}
	}
	result["status"], result["settings"], result["changed"] = "pending_host_trust", path, changed
	result["next_action"] = "review project and hook trust in the host, then test a real compact or exact-session resume"
	if a.has("dry-run") {
		result["status"] = "preview"
	}
	return result, nil
}

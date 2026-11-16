package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type toolSession struct {
	Schema        int                  `json:"schema"`
	Server        string               `json:"server"`
	Profile       string               `json:"profile"`
	Project       string               `json:"project_id"`
	Checkout      string               `json:"checkout"`
	IdentityHash  string               `json:"identity_hash"`
	Session       string               `json:"session"`
	Runner        string               `json:"runner,omitempty"`
	Prefix        string               `json:"installation"`
	Managed       bool                 `json:"managed_installation"`
	Tool          bootstrap.ClientTool `json:"cli"`
	SkillVersion  string               `json:"skill_version"`
	SkillHash     string               `json:"skill_sha256"`
	SkillPath     string               `json:"skill_path"`
	SkillFiles    map[string]string    `json:"skill_files"`
	APIProtocol   int                  `json:"api_protocol"`
	SkillProtocol int                  `json:"skill_protocol"`
	Created       time.Time            `json:"created_at"`
	Ended         *time.Time           `json:"ended_at,omitempty"`
}

func toolSessionLocation(a args, config, project string, binding remote.Binding) (string, remote.Profile, string, error) {
	session := a.get("session", os.Getenv("AEL_SESSION_ID"))
	if !recoverySessionID.MatchString(session) {
		return "", remote.Profile{}, "", core.Error("SESSION_REQUIRED", "provide --session HOST_SESSION_ID or AEL_SESSION_ID")
	}
	checkpoint, profile, root, err := recoveryLocation(a, config, project, binding)
	if err != nil {
		return "", profile, "", err
	}
	config, err = filepath.EvalSymlinks(config)
	if err != nil {
		return "", profile, "", err
	}
	dir, err := store.SafePath(config, "tool-sessions")
	if err != nil {
		return "", profile, "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", profile, "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(dir) != nil {
		return "", profile, "", core.Error("SESSION_PRIVATE_DIRECTORY_REQUIRED", "tool session indexes must be private")
	}
	path, err := store.SafePath(dir, filepath.Base(checkpoint))
	return path, profile, root, err
}

func readToolSession(path string) (toolSession, error) {
	var session toolSession
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return session, err
	}
	if err != nil || info.Mode().Perm()&0077 != 0 || info.Size() > 128<<10 || securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil {
		return session, core.Error("SESSION_INDEX_INVALID", "tool session index must be private and regular")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return session, err
	}
	if json.Unmarshal(raw, &session) != nil || session.Schema != 1 || session.APIProtocol != 1 || session.SkillProtocol != 1 || !recoverySessionID.MatchString(session.Session) {
		return session, core.Error("SESSION_INDEX_INVALID", "unsupported tool session index")
	}
	if !filepath.IsAbs(session.Tool.Executable) || len(session.Tool.SHA256) != 64 || !filepath.IsAbs(session.SkillPath) || len(session.SkillFiles) != 5 || session.SkillFiles["SKILL.md"] == "" || session.Created.IsZero() {
		return session, core.Error("SESSION_INDEX_INVALID", "incomplete CLI or Skill binding")
	}
	return session, nil
}

func validateToolSession(session toolSession, profile remote.Profile, root string, binding remote.Binding) error {
	if session.Server != profile.URL || session.Profile != binding.Profile || session.Project != binding.ProjectID || session.Checkout != root || session.IdentityHash != store.Hash([]byte(profile.Token)) {
		return core.Error("SESSION_BINDING_CONFLICT", "tool session belongs to another Server, checkout or credential identity")
	}
	if session.Ended != nil {
		return core.Error("SESSION_ENDED", "use a new host session identifier for new work")
	}
	if hash, err := store.HashFile(session.Tool.Executable); err != nil || hash != session.Tool.SHA256 {
		return core.Error("SESSION_CLI_UNAVAILABLE", "restore the exact cached CLI before continuing this session")
	}
	for relative, wanted := range session.SkillFiles {
		path, err := store.SafePath(filepath.Dir(session.SkillPath), relative)
		if err != nil {
			return err
		}
		if actual, err := store.HashFile(path); err != nil || actual != wanted {
			return core.Error("SESSION_SKILL_CONFLICT", "frozen Skill assets changed, preserve and reconcile this session")
		}
	}
	return nil
}

func remoteSessionCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	path, profile, root, err := toolSessionLocation(a, config, project, binding)
	if err != nil {
		return nil, err
	}
	unlock, err := lockAgentOperation(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	session, readErr := readToolSession(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	if readErr == nil {
		if err = validateToolSession(session, profile, root, binding); err != nil {
			if strings.HasPrefix(err.Error(), "SESSION_ENDED") && cmd != "session start" {
				return map[string]any{"status": "ended", "session": session, "tool_session_file": path}, nil
			}
			return nil, err
		}
		if session.Runner != "" && a.get("runner", session.Runner) != session.Runner {
			return nil, core.Error("SESSION_BINDING_CONFLICT", "the session is already bound to another runner")
		}
		if cmd == "session end" {
			if session.Runner != "" {
				runner, err := remote.Call(config, root, binding, http.MethodGet, "/runners/"+url.PathEscape(session.Runner), nil, nil)
				if err != nil && !strings.HasPrefix(err.Error(), "RUNNER_NOT_FOUND") {
					return nil, err
				}
				if err != nil {
					runner = map[string]any{"assignments": []any{}}
				}
				if assignments, ok := runner["assignments"].([]any); ok {
					for _, item := range assignments {
						assignment, ok := item.(map[string]any)
						if !ok {
							return nil, core.Error("REMOTE_RESPONSE_INVALID", "invalid runner assignment")
						}
						state, _ := assignment["responsibility_state"].(string)
						if state != "ended" && state != "cancelled" && state != "superseded" {
							return nil, core.Error("SESSION_ACTIVE_RESPONSIBILITY", "finish or explicitly hand off the Server responsibility before ending tool binding")
						}
					}
				} else {
					return nil, core.Error("REMOTE_RESPONSE_INVALID", "Server did not report current assignments")
				}
			}
			now := time.Now().UTC()
			session.Ended = &now
			if err = bootstrap.UnbindRemoteSession(root, session.Session, path); err != nil {
				return nil, err
			}
			data, _ := json.MarshalIndent(session, "", "  ")
			if err = store.Atomic(path, data, 0600); err != nil {
				return nil, err
			}
			return map[string]any{"status": "ended", "session": session, "tool_session_file": path}, nil
		}
		if cmd == "session start" {
			project, err := remote.Call(config, root, binding, http.MethodGet, "", nil, nil)
			if err != nil {
				return nil, err
			}
			if project["online"] != true || project["authoritative"] != true {
				return nil, core.Error("REMOTE_UNAVAILABLE", "session preparation requires live project authority")
			}
			meta, err := remote.Metadata(nil, profile)
			if err != nil {
				return nil, err
			}
			if session.Runner == "" && a.get("runner", "") != "" {
				release, err := lockRunnerToolBinding(path, profile, root, binding, a.get("runner", ""))
				if err != nil {
					return nil, err
				}
				defer release()
				if err = ensureRunnerIdleForTools(config, root, binding, a.get("runner", "")); err != nil {
					return nil, err
				}
				if err = ensureUniqueRunnerToolSession(path, profile, root, binding, a.get("runner", ""), session.Session); err != nil {
					return nil, err
				}
				session.Runner = a.get("runner", "")
				data, _ := json.MarshalIndent(session, "", "  ")
				if err = store.Atomic(path, data, 0600); err != nil {
					return nil, err
				}
			}
			if _, err = bootstrap.BindRemoteSession(root, session.Session, session.Tool.Executable, config, path); err != nil {
				return nil, err
			}
			reported := reportToolSession(session, config, root, binding, meta)
			return map[string]any{"status": "pinned", "session": session, "tool_session_file": path, "version_reported": reported, "compatible_for_new_work": toolCompatible(session.Tool.Version.Version, meta), "next_action": "read the frozen Skill path and run the current project recovery check before continuing"}, nil
		}
		return map[string]any{"status": "pinned", "session": session, "tool_session_file": path, "next_action": "read the frozen Skill path and run the current project recovery check before continuing"}, nil
	}
	if cmd != "session start" {
		return map[string]any{"status": "no_session", "next_action": "prepare a new session using session start --session HOST_SESSION_ID"}, nil
	}
	if runner := a.get("runner", ""); runner != "" {
		release, err := lockRunnerToolBinding(path, profile, root, binding, runner)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if err = ensureRunnerIdleForTools(config, root, binding, a.get("runner", "")); err != nil {
		return nil, err
	}
	if err = ensureUniqueRunnerToolSession(path, profile, root, binding, a.get("runner", ""), a.get("session", os.Getenv("AEL_SESSION_ID"))); err != nil {
		return nil, err
	}
	preflight, err := checkTaskTools(a, config, root, binding)
	if err != nil {
		return nil, err
	}
	version, skillHash, files, err := bootstrap.RemoteSessionAssets(root)
	if err != nil {
		return nil, err
	}
	assetDir := strings.TrimSuffix(path, ".json") + ".assets"
	if err = os.MkdirAll(assetDir, 0700); err != nil {
		return nil, err
	}
	fileHashes := map[string]string{}
	for relative, data := range files {
		assetPath, err := store.SafePath(assetDir, relative)
		if err != nil {
			return nil, err
		}
		wanted := store.Hash(data)
		actual, readErr := store.HashFile(assetPath)
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		if readErr == nil && actual != wanted {
			return nil, core.Error("SESSION_SKILL_CONFLICT", "interrupted session assets differ from the current source")
		}
		if os.IsNotExist(readErr) {
			if err = store.Atomic(assetPath, data, 0600); err != nil {
				return nil, err
			}
		}
		fileHashes[relative] = wanted
	}
	session = toolSession{Schema: 1, Server: profile.URL, Profile: binding.Profile, Project: binding.ProjectID, Checkout: root, IdentityHash: store.Hash([]byte(profile.Token)), Session: a.get("session", os.Getenv("AEL_SESSION_ID")), Runner: a.get("runner", ""), Prefix: preflight.Prefix, Managed: preflight.Managed, Tool: preflight.Tool, SkillVersion: version, SkillHash: skillHash, SkillPath: filepath.Join(assetDir, "SKILL.md"), SkillFiles: fileHashes, APIProtocol: 1, SkillProtocol: 1, Created: time.Now().UTC()}
	data, _ := json.MarshalIndent(session, "", "  ")
	if err = store.Atomic(path, data, 0600); err != nil {
		return nil, err
	}
	if _, err = bootstrap.BindRemoteSession(root, session.Session, session.Tool.Executable, config, path); err != nil {
		return nil, err
	}
	reported := reportToolSession(session, config, root, binding, preflight.Server)
	return map[string]any{"status": "prepared", "session": session, "tool_session_file": path, "version_reported": reported, "preflight": preflight, "next_action": "read the frozen Skill path, register the executor if needed, then run recovery check and explicitly claim authorized work"}, nil
}

func reportToolSession(session toolSession, config, root string, binding remote.Binding, meta remote.ServerMetadata) bool {
	if !session.Managed || !slices.Contains(meta.Capabilities, "client_releases") {
		return false
	}
	machineID, err := initMachineID(session.Prefix)
	if err != nil {
		return false
	}
	policy, err := readClientPolicy(session.Prefix)
	if err != nil {
		return false
	}
	report := map[string]any{"client_id": store.Hash([]byte(machineID + "\n" + root + "\n" + session.Session)), "session_id": session.Session, "runner": session.Runner, "cli_version": session.Tool.Version.Version, "cli_build": session.Tool.Version.Build, "skill_version": session.SkillVersion, "update_policy": policy.Mode, "specification_state": "session_prepared", "hooks": map[string]string{}}
	_, err = remote.Call(config, root, binding, http.MethodPost, "/client-onboarding", report, nil)
	return err == nil
}

func ensureRunnerIdleForTools(config, root string, binding remote.Binding, runnerID string) error {
	if runnerID == "" {
		return nil
	}
	runner, err := remote.Call(config, root, binding, http.MethodGet, "/runners/"+url.PathEscape(runnerID), nil, nil)
	if err != nil && strings.HasPrefix(err.Error(), "RUNNER_NOT_FOUND") {
		return nil
	}
	if err != nil {
		return err
	}
	assignments, ok := runner["assignments"].([]any)
	if !ok {
		return core.Error("REMOTE_SESSION_BINDING_UNSUPPORTED", "Server must report current runner assignments before another tool session")
	}
	if len(assignments) > 0 {
		return core.Error("SESSION_ACTIVE_RUNNER", "resume the runner's existing tool session from its recovery checkpoint before preparing different tools")
	}
	return nil
}

func lockRunnerToolBinding(path string, profile remote.Profile, root string, binding remote.Binding, runner string) (func(), error) {
	scope, _ := json.Marshal([]string{profile.URL, binding.Profile, binding.ProjectID, root, store.Hash([]byte(profile.Token)), runner})
	lockPath, err := store.SafePath(filepath.Dir(path), "runner-"+store.Hash(scope))
	if err != nil {
		return nil, err
	}
	return lockAgentOperation(lockPath)
}

func openRunnerToolSession(path string, profile remote.Profile, root string, binding remote.Binding, runner string) (toolSession, bool, error) {
	var found toolSession
	if runner == "" {
		return found, false, nil
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return found, false, err
	}
	matched := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		candidate, err := readToolSession(filepath.Join(filepath.Dir(path), entry.Name()))
		if err != nil || candidate.Ended != nil || candidate.Runner != runner || candidate.Server != profile.URL || candidate.Profile != binding.Profile || candidate.Project != binding.ProjectID || candidate.Checkout != root || candidate.IdentityHash != store.Hash([]byte(profile.Token)) {
			continue
		}
		if matched && found.Session != candidate.Session {
			return found, false, core.Error("SESSION_BINDING_CONFLICT", "multiple open tool sessions refer to the same runner")
		}
		found, matched = candidate, true
	}
	return found, matched, nil
}

func ensureUniqueRunnerToolSession(path string, profile remote.Profile, root string, binding remote.Binding, runner, id string) error {
	found, ok, err := openRunnerToolSession(path, profile, root, binding, runner)
	if err != nil {
		return err
	}
	if ok && found.Session != id {
		return core.Error("SESSION_RUNNER_IN_USE", "runner tools are already bound to session "+found.Session+", continue it or end that tool binding after responsibility is clear")
	}
	return nil
}

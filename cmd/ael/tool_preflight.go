package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

type toolPreflight struct {
	Prefix        string                `json:"installation"`
	Tool          bootstrap.ClientTool  `json:"selected_cli"`
	Managed       bool                  `json:"managed_installation"`
	Status        string                `json:"status"`
	TargetVersion string                `json:"target_version,omitempty"`
	Warnings      []string              `json:"warnings"`
	Server        remote.ServerMetadata `json:"server"`
}

type toolUpdateCheck struct {
	Key      string    `json:"key"`
	At       time.Time `json:"checked_at"`
	Status   string    `json:"status"`
	Target   string    `json:"target"`
	Warnings []string  `json:"warnings"`
}

func toolCompatible(version string, meta remote.ServerMetadata) bool {
	return semver.IsValid("v"+version) && (meta.MinimumClientVersion == "" || (semver.IsValid("v"+meta.MinimumClientVersion) && semver.Compare("v"+version, "v"+meta.MinimumClientVersion) >= 0))
}

func currentClientTool(prefix string) (bootstrap.ClientTool, bool, error) {
	tool, err := bootstrap.InstalledClient(prefix)
	if err == nil {
		return tool, true, nil
	}
	if !os.IsNotExist(err) {
		return tool, false, err
	}
	executable, err := os.Executable()
	if err != nil {
		return tool, false, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return tool, false, err
	}
	hash, err := store.HashFile(executable)
	if err != nil {
		return tool, false, err
	}
	tool, err = bootstrap.FreezeStandaloneClient(prefix, bootstrap.ClientTool{Executable: executable, SHA256: hash, Version: bootstrap.BinaryVersion{Version: bootstrap.Version, Build: bootstrap.Build, APIProtocol: 1, SkillProtocol: 1, OutputProtocol: 1}})
	return tool, false, err
}

func checkTaskTools(a args, config, project string, binding remote.Binding) (toolPreflight, error) {
	result := toolPreflight{Warnings: []string{}}
	profile, err := remote.LoadProfile(config, project, binding.Profile)
	if err != nil {
		return result, err
	}
	result.Server, err = remote.MetadataForUpdates(nil, profile)
	if err != nil {
		return result, err
	}
	identity, err := remote.Call(config, project, binding, http.MethodGet, "", nil, nil)
	if err != nil {
		return result, err
	}
	if identity["online"] != true || identity["authoritative"] != true {
		return result, core.Error("REMOTE_UNAVAILABLE", "new tool sessions require live project authority")
	}
	result.Prefix, err = bootstrap.InstallationPrefix(a.get("prefix", ""))
	if err != nil {
		return result, err
	}
	policy, err := readClientPolicy(result.Prefix)
	if err != nil {
		return result, err
	}
	_, installationErr := os.Stat(filepath.Join(result.Prefix, "share/ael/installation.json"))
	if bootstrap.RunningProjectRuntime(project) && (policy.Mode == "notify" || os.IsNotExist(installationErr)) {
		if policy.Mode == "pinned" && policy.Version != bootstrap.Version {
			return result, core.Error("CLIENT_VERSION_UNAVAILABLE", "project runtime does not match the pinned client version")
		}
		exe, err := os.Executable()
		if err != nil {
			return result, err
		}
		hash, err := store.HashFile(exe)
		if err != nil {
			return result, err
		}
		if filepath.Base(filepath.Dir(exe)) != hash {
			return result, core.Error("CLONE_RUNTIME_MISMATCH", "running project executable differs from its content address")
		}
		result.Tool = bootstrap.ClientTool{Executable: exe, SHA256: hash, Version: bootstrap.BinaryVersion{Version: bootstrap.Version, Build: build, APIProtocol: 1, SkillProtocol: 1, OutputProtocol: 1}}
		result.Status = "project_runtime"
		return finishToolCompatibility(result)
	}
	result.Tool, result.Managed, err = currentClientTool(result.Prefix)
	if err != nil {
		return result, err
	}
	if policy.Mode == "pinned" {
		if policy.Version != result.Tool.Version.Version {
			result.Tool, err = bootstrap.CachedClient(result.Prefix, policy.Version, "")
			if err != nil {
				return result, err
			}
		}
		result.Status = "fixed_version"
		return finishToolCompatibility(result)
	}
	if policy.Source == "" {
		result.Status = "no_configured_source"
		result.Warnings = append(result.Warnings, "no trusted online release source is configured for this installation")
		return finishToolCompatibility(result)
	}
	source, err := trustedPolicySource(policy)
	if err != nil {
		return compatibleToolFailure(result, err)
	}
	lockPath, err := store.SafePath(result.Prefix, "share/ael/task-update-check")
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var unlock func()
	for {
		unlock, err = lockAgentOperation(lockPath)
		if err == nil {
			break
		}
		if !strings.HasPrefix(err.Error(), "AGENT_OPERATION_BUSY") {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, core.Error("UPDATE_CHECK_BUSY", "another foreground check is still using this installation, retry after it finishes")
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer unlock()
	// Another process may have completed the authorized installation while this
	// command waited. Re-read it before consulting the shared short-lived cache.
	result.Tool, result.Managed, err = currentClientTool(result.Prefix)
	if err != nil {
		return result, err
	}
	cachePath, err := store.SafePath(result.Prefix, "share/ael/task-update-check.json")
	if err != nil {
		return result, err
	}
	key := toolUpdateKey(policy, profile, result)
	var cached toolUpdateCheck
	if raw, err := os.ReadFile(cachePath); err == nil && json.Unmarshal(raw, &cached) == nil && cached.Key == key && time.Since(cached.At) < 2*time.Minute {
		result.Status, result.TargetVersion, result.Warnings = cached.Status, cached.Target, cached.Warnings
		return finishToolCompatibility(result)
	}
	selected, err := source.CheckVersion(ctx, "client", runtime.GOOS+"-"+runtime.GOARCH, "stable", result.Server.RecommendedClientVersion)
	if err != nil {
		result, err = compatibleToolFailure(result, err)
	} else if !selected.Available {
		result.Status = selected.Status
		result.Warnings = append(result.Warnings, "trusted catalog has no compatible published client for this platform")
	} else {
		result.TargetVersion = selected.Manifest.Version
		if !toolCompatible(selected.Manifest.Version, result.Server) {
			return result, core.Error("CLIENT_VERSION_INCOMPATIBLE", "trusted catalog has no selected stable client meeting Server minimum "+result.Server.MinimumClientVersion)
		}
		result.Status = "up_to_date"
		if semver.Compare("v"+selected.Manifest.Version, "v"+result.Tool.Version.Version) > 0 {
			result.Status = "update_available"
			if policy.Mode == "notify" {
				result.Warnings = append(result.Warnings, "a compatible stable client is available, use upgrade for this installation")
			} else {
				stage, stageErr := os.MkdirTemp("", "ael-task-update-")
				if stageErr != nil {
					return result, stageErr
				}
				defer os.RemoveAll(stage)
				archive := filepath.Join(stage, selected.Asset.Path)
				updateErr := source.Download(ctx, selected, archive)
				if updateErr == nil {
					_, updateErr = trustedPolicySource(policy)
				}
				if updateErr == nil {
					_, updateErr = bootstrap.Upgrade(bootstrap.UpdateOptions{Prefix: result.Prefix, From: archive, TargetVersion: selected.Manifest.Version, ExpectedBuild: selected.Manifest.Build, ExpectedCommit: selected.Manifest.Commit})
				}
				if updateErr != nil {
					result, err = compatibleToolFailure(result, updateErr)
				} else {
					result.Tool, result.Managed, err = currentClientTool(result.Prefix)
					result.Status = "updated"
				}
			}
		}
	}
	if err != nil {
		return result, err
	}
	result, err = finishToolCompatibility(result)
	if err != nil {
		return result, err
	}
	cache := toolUpdateCheck{Key: toolUpdateKey(policy, profile, result), At: time.Now().UTC(), Status: result.Status, Target: result.TargetVersion, Warnings: result.Warnings}
	raw, _ := json.Marshal(cache)
	if err = store.Atomic(cachePath, raw, 0600); err != nil {
		return result, err
	}
	return result, nil
}

func toolUpdateKey(policy clientUpdatePolicy, profile remote.Profile, result toolPreflight) string {
	raw, _ := json.Marshal([]string{policy.Mode, policy.SourceFingerprint, policy.Version, profile.URL, result.Server.MinimumClientVersion, result.Server.RecommendedClientVersion, result.Tool.Version.Version, result.Tool.Version.Build})
	return store.Hash(raw)
}

func compatibleToolFailure(result toolPreflight, cause error) (toolPreflight, error) {
	result.Status = "update_failed_using_compatible_cli"
	result.Warnings = append(result.Warnings, recoveryFailureCode(cause))
	if hash, err := store.HashFile(result.Tool.Executable); err != nil || hash != result.Tool.SHA256 {
		return result, core.Error("SESSION_CLI_UNAVAILABLE", "the previously selected executable changed during failed update")
	}
	return finishToolCompatibility(result)
}

func finishToolCompatibility(result toolPreflight) (toolPreflight, error) {
	if result.Tool.Version.APIProtocol != 1 || result.Tool.Version.SkillProtocol != 1 || result.Tool.Version.OutputProtocol != 1 {
		return result, core.Error("CLIENT_PROTOCOL_UNSUPPORTED", "the selected CLI does not support the bound API, Skill and output protocols")
	}
	if !toolCompatible(result.Tool.Version.Version, result.Server) {
		return result, core.Error("CLIENT_VERSION_INCOMPATIBLE", "current CLI "+result.Tool.Version.Version+" is below Server minimum "+result.Server.MinimumClientVersion+"; upgrade the selected installation using an explicit trusted source before new responsibility")
	}
	if semver.Compare("v"+result.Tool.Version.Version, "v0.1.23") < 0 {
		return result, core.Error("CLIENT_SESSION_VERSION_UNSUPPORTED", "tool version pinning requires a client with session support, install 0.1.23 or newer before starting this session")
	}
	return result, nil
}

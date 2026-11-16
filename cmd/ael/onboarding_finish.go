package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/release"
	"ael/internal/remote"
	"ael/internal/store"
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type clientUpdatePolicy struct {
	Source            string `json:"source,omitempty"`
	SourceFingerprint string `json:"source_fingerprint,omitempty"`
	Schema            int    `json:"schema"`
	Mode              string `json:"mode"`
	Version           string `json:"version,omitempty"`
	SelectedBy        string `json:"selected_by"`
}

func finishRemoteInit(a args, config, root string, binding remote.Binding, meta remote.ServerMetadata, reader *bufio.Reader, interactive bool) (any, error) {
	profile, err := remote.LoadProfile(config, root, binding.Profile)
	if err != nil {
		return nil, err
	}
	configuration, err := remote.Call(config, root, binding, http.MethodGet, "/onboarding", nil, nil)
	if err != nil {
		return nil, err
	}
	project, ok := configuration["project"].(map[string]any)
	if !ok || project["id"] != binding.ProjectID {
		return nil, core.Error("REMOTE_RESPONSE_INVALID", "configuration does not identify the bound project")
	}
	prefix, err := bootstrap.InstallationPrefix(a.get("prefix", ""))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, prefix)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return nil, core.Error("INIT_SHARED_INSTALL_REQUIRED", "Remote CLI installation must be outside the project checkout")
	}
	options := bootstrap.UpdateOptions{Prefix: prefix, From: a.get("from", a.get("bundle-dir", "")), DryRun: true}
	preview, err := bootstrap.Upgrade(options)
	if err != nil {
		return nil, err
	}
	if preview.Available.Version != bootstrap.Version || preview.Available.Build != bootstrap.Build {
		return nil, core.Error("INIT_CLI_SOURCE_MISMATCH", "run init from the same client package selected for machine installation")
	}
	options.DryRun = false
	options.TargetVersion = bootstrap.Version
	installation, err := bootstrap.Upgrade(options)
	if err != nil {
		return nil, err
	}
	entry, err := installRemoteEntry(root, false, false)
	if err != nil {
		return nil, err
	}
	mapping, err := bootstrap.BindRemoteClient(root, installation.Target, config)
	if err != nil {
		return nil, err
	}
	policy, err := initUpdatePolicy(a, prefix, reader, interactive)
	if err != nil {
		return nil, err
	}
	host := a.get("host", "auto")
	if host != "auto" && host != "both" && host != "codex" && host != "claude" && host != "none" {
		return nil, core.Error("INIT_HOST_INVALID", "choose auto, codex, claude, both or none")
	}
	hosts := []string{}
	switch host {
	case "auto", "both":
		hosts = []string{"codex", "claude"}
	case "codex", "claude":
		hosts = []string{host}
	}
	recovery := map[string]any{}
	for _, host := range hosts {
		value, err := recoveryHostCommand(args{Flags: map[string]string{"project": root, "host": host, "config-dir": config}})
		if err != nil {
			return nil, err
		}
		recovery[host] = value
	}
	if len(hosts) == 0 {
		recovery["status"] = "explicit_command_and_skill"
	}
	catalog, err := knowledge.Index(root)
	if err != nil {
		return nil, err
	}
	differences := []string{}
	sources, ok := configuration["knowledge"].([]any)
	if !ok {
		return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing readable specification index")
	}
	for _, item := range sources {
		source, ok := item.(map[string]any)
		if !ok {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "invalid specification index")
		}
		path, ok := source["path"].(string)
		if !ok {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "invalid specification path")
		}
		local, err := store.SafePath(root, path)
		if err != nil {
			differences = append(differences, path)
			continue
		}
		hash, err := store.HashFile(local)
		if err != nil || hash != source["sha256"] {
			differences = append(differences, path)
		}
	}
	specificationState := configuration["specification_state"]
	if len(differences) > 0 {
		specificationState = "checkout_differs_from_server_snapshots"
	}
	cachePath, err := store.SafePath(config, filepath.Join("onboarding", store.Hash([]byte(root+"\n"+binding.ProjectID))+".configuration.json"))
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(configuration)
	if err = store.Atomic(cachePath, data, 0600); err != nil {
		return nil, err
	}
	machineID, err := initMachineID(prefix)
	if err != nil {
		return nil, err
	}
	hookStatuses := map[string]string{}
	for host, value := range recovery {
		if detail, ok := value.(map[string]any); ok {
			if status, ok := detail["status"].(string); ok {
				hookStatuses[host] = status
			}
		}
	}
	report := map[string]any{"client_id": store.Hash([]byte(machineID + "\n" + root)), "cli_version": bootstrap.Version, "skill_version": bootstrap.Version, "update_policy": policy.Mode, "specification_state": specificationState, "hooks": hookStatuses}
	if _, err = remote.Call(config, root, binding, http.MethodPost, "/client-onboarding", report, nil); err != nil {
		return nil, err
	}
	return map[string]any{"mode": "remote", "status": "connected", "connected": true, "server": meta.Version, "server_url": profile.URL, "server_build": meta.Build, "api_protocol": meta.Protocol, "skill_protocol": meta.SkillProtocol, "project_id": binding.ProjectID, "project_name": project["name"], "project_role": project["role"], "profile": binding.Profile, "directory": root, "cli_installation": installation.Target, "cli_version": installation.Available.Version, "machine_mapping": mapping, "entry_status": entry.Status, "configuration_readable": true, "materials_readable": false, "readable_source_references": len(sources), "local_source_counts": catalog.Counts, "specification_state": specificationState, "source_differences": differences, "configuration_cache": cachePath, "task_executable": false, "task_access": "requires_current_candidate_context_and_explicit_responsibility", "recovery_integration": recovery, "update_policy": policy, "next_action": "read ael-work, verify the current project, then register the executor and prepare candidate/context before explicit task claim"}, nil
}

func initMachineID(prefix string) (string, error) {
	path, err := store.SafePath(prefix, "share/ael/client-id")
	if err != nil {
		return "", err
	}
	unlock, err := lockAgentOperation(path)
	if err != nil {
		return "", err
	}
	defer unlock()
	if raw, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(raw))
		if !recoverySessionID.MatchString(id) {
			return "", core.Error("INIT_CLIENT_ID_INVALID", "existing machine client identity requires review")
		}
		return id, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	id, err := runnerOperation("client")
	if err != nil {
		return "", err
	}
	return id, store.Atomic(path, []byte(id+"\n"), 0600)
}

func initUpdatePolicy(a args, prefix string, reader *bufio.Reader, interactive bool) (clientUpdatePolicy, error) {
	policy := clientUpdatePolicy{Schema: 1, Mode: "notify", SelectedBy: "default_notify"}
	path, err := store.SafePath(prefix, "share/ael/update-policy.json")
	if err != nil {
		return policy, err
	}
	raw, readErr := os.ReadFile(path)
	if readErr == nil {
		if json.Unmarshal(raw, &policy) != nil || policy.Schema != 1 {
			return policy, core.Error("UPDATE_POLICY_CONFLICT", "existing update policy requires review")
		}
	} else if !os.IsNotExist(readErr) {
		return policy, readErr
	}
	mode := a.get("update-policy", "")
	if mode == "" && os.IsNotExist(readErr) && interactive {
		mode, err = initPrompt(reader, "CLI 更新策略 notify / auto / pinned（默认 notify）")
		if err != nil {
			return policy, err
		}
		if mode == "" {
			mode = "notify"
		}
	}
	if mode != "" {
		policy.Mode, policy.SelectedBy = mode, "explicit_choice"
		policy.Version = ""
		if mode == "pinned" {
			policy.Version = a.get("pin-version", bootstrap.Version)
		}
	}
	if policy.Mode != "notify" && policy.Mode != "auto" && policy.Mode != "pinned" {
		return policy, core.Error("UPDATE_POLICY_INVALID", "choose notify, auto or pinned")
	}
	if policy.Mode == "pinned" && a.get("pin-version", "") != "" {
		policy.Version = a.get("pin-version", "")
		policy.SelectedBy = "explicit_choice"
	}
	if policy.Mode == "pinned" && !release.ValidVersion(policy.Version) {
		return policy, core.Error("UPDATE_POLICY_INVALID", "pin an exact semantic version")
	}
	if a.get("pin-version", "") != "" && policy.Mode != "pinned" {
		return policy, core.Error("UPDATE_POLICY_INVALID", "--pin-version requires pinned policy")
	}
	sourcePath := a.get("release-source", "")
	if sourcePath != "" {
		sourcePath, err = filepath.Abs(sourcePath)
		if err != nil {
			return policy, err
		}
		source, err := release.ReadSource(sourcePath)
		if err != nil {
			return policy, err
		}
		fingerprint, err := source.Fingerprint()
		if err != nil {
			return policy, err
		}
		policy.Source, policy.SourceFingerprint = sourcePath, fingerprint
	}
	if a.has("clear-source") {
		policy.Source, policy.SourceFingerprint = "", ""
	}
	if readErr == nil && mode == "" && a.get("pin-version", "") == "" && sourcePath == "" && !a.has("clear-source") {
		return policy, nil
	}
	fields := map[string]any{}
	if readErr == nil {
		if json.Unmarshal(raw, &fields) != nil {
			return policy, core.Error("UPDATE_POLICY_CONFLICT", "existing update policy requires review")
		}
	}
	fields["schema"], fields["mode"], fields["selected_by"] = policy.Schema, policy.Mode, policy.SelectedBy
	delete(fields, "source")
	delete(fields, "source_fingerprint")
	if policy.Source != "" {
		fields["source"], fields["source_fingerprint"] = policy.Source, policy.SourceFingerprint
	}
	delete(fields, "version")
	if policy.Version != "" {
		fields["version"] = policy.Version
	}
	data, _ := json.MarshalIndent(fields, "", "  ")
	return policy, store.Atomic(path, append(data, '\n'), 0600)
}

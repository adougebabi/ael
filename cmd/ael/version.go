package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
	"os"
)

func versionCommand(a args) (any, error) {
	result := map[string]any{"version": bootstrap.Version, "build": build, "cli_output_protocol": 1, "api_protocol": 1, "skill_protocol": 1}
	for flag, value := range a.Flags {
		if flag != "check" && flag != "project" && flag != "config-dir" && flag != "json" && flag != "verbose" && flag != "no-color" {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		isBoolean := flag == "check" || booleanFlag(flag)
		if isBoolean && value != "true" && value != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", flag)
		}
		if !isBoolean && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	if !a.has("check") {
		return result, nil
	}
	root := a.get("project", ".")
	binding, exists, err := remote.ReadBinding(root)
	if err != nil {
		return nil, err
	}
	if !exists {
		config, readErr := bootstrap.Read(root)
		if os.IsNotExist(readErr) {
			result["project_mode"] = "unconnected"
			return result, nil
		}
		if readErr != nil {
			return nil, readErr
		}
		if config.Schema != 1 || config.ProjectID == "" || config.Version == "" {
			return nil, core.Error("INVALID_PROJECT_CONFIG", "cannot identify the Local project entry version")
		}
		result["project_mode"], result["project_entry_version"] = "local", config.Version
		return result, nil
	}

	if err = remote.CheckClientProtocols(binding); err != nil {
		return nil, err
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	profile, err := remote.LoadProfile(config, root, binding.Profile)
	if err != nil {
		return nil, err
	}
	meta, err := remote.Metadata(nil, profile)
	if err != nil {
		return nil, err
	}
	result["project_mode"], result["project_requirements"], result["server"] = "remote", binding, meta
	entry, err := bootstrap.RemoteEntryInfo(root)
	if err != nil {
		return nil, err
	}
	result["project_entry"] = entry
	result["server_version_status"] = "reported"
	if meta.Version == "" {
		result["server_version_status"] = "unreported"
	}
	result["compatible"], result["active_session_instructions_changed"] = true, false
	return result, nil
}

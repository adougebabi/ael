package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
)

func remoteEntryCommand(a args) (any, error) {
	allowed := map[string]bool{"project": true, "dry-run": true, "migrate-entry": true, "json": true, "verbose": true, "no-color": true}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if booleanFlag(flag) {
			if value != "true" && value != "false" {
				return nil, core.Error("INVALID_FLAG_VALUE", flag)
			}
		} else if value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	return installRemoteEntry(a.get("project", "."), a.has("dry-run"), a.has("migrate-entry"))
}

func installRemoteEntry(root string, dry, migrate bool) (bootstrap.UpdateReport, error) {
	_, exists, hash, err := remote.ReadBindingSnapshot(root)
	if err != nil {
		return bootstrap.UpdateReport{}, err
	}
	if !exists {
		return bootstrap.UpdateReport{}, core.Error("REMOTE_BINDING_REQUIRED", "connect the selected project first")
	}
	return bootstrap.UpdateRemoteEntry(root, dry, migrate, hash)
}

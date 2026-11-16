package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/release"
	"context"
	"os"
	"path/filepath"
	"runtime"
)

func machineUpgradeCommand(a args) (any, error) {
	if a.get("project", "") != "" {
		return nil, core.Error("INVALID_UPDATE_SCOPE", "upgrade updates the machine installation; use update --project for project assets")
	}
	allowed := map[string]bool{"rollback": true, "from": true, "bundle-dir": true, "prefix": true, "dry-run": true, "check": true, "version": true, "source": true, "channel": true, "ack-stopped": true, "json": true, "verbose": true, "no-color": true}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if flag == "check" || booleanFlag(flag) {
			if value != "true" && value != "false" {
				return nil, core.Error("INVALID_FLAG_VALUE", flag)
			}
		} else if value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	from := a.get("from", a.get("bundle-dir", ""))
	if a.get("from", "") != "" && a.get("bundle-dir", "") != "" && a.get("from", "") != a.get("bundle-dir", "") {
		return nil, core.Error("CONFLICTING_SOURCE", "choose one distribution source")
	}
	options := bootstrap.UpdateOptions{From: from, Prefix: a.get("prefix", ""), DryRun: a.has("dry-run"), Check: a.has("check"), TargetVersion: a.get("version", ""), AckStopped: a.has("ack-stopped")}
	if options.TargetVersion != "" && !release.ValidVersion(options.TargetVersion) {
		return nil, core.Error("INVALID_RELEASE_VERSION", "use an exact semantic version")
	}
	if a.has("rollback") {
		if a.get("source", "") != "" || a.get("version", "") != "" {
			return nil, core.Error("CONFLICTING_SOURCE", "rollback selects its exact version from matching backup metadata")
		}
		return bootstrap.Rollback(options)
	}
	if a.get("source", "") == "" && from == "" {
		prefix, err := bootstrap.InstallationPrefix(options.Prefix)
		if err != nil {
			return nil, err
		}
		policy, err := readClientPolicy(prefix)
		if err != nil {
			return nil, err
		}
		if policy.Source != "" {
			if _, err = trustedPolicySource(policy); err != nil {
				return nil, err
			}
			a.Flags["source"] = policy.Source
		}
	}
	if a.get("source", "") == "" {
		return bootstrap.Upgrade(options)
	}
	if from != "" {
		return nil, core.Error("CONFLICTING_SOURCE", "choose --source or --from")
	}
	source, err := release.ReadSource(a.get("source", ""))
	if err != nil {
		return nil, err
	}
	selected, err := source.CheckVersion(context.Background(), "client", runtime.GOOS+"-"+runtime.GOARCH, a.get("channel", "stable"), options.TargetVersion)
	if err != nil {
		return nil, err
	}
	if !selected.Available {
		return map[string]any{"status": selected.Status, "selection": selected, "installation_changed": false}, nil
	}
	if options.Check || options.DryRun {
		available := bootstrap.BinaryVersion{Version: selected.Manifest.Version, Build: selected.Manifest.Build, OutputProtocol: 1, APIProtocol: 1, SkillProtocol: 1}
		target, err := bootstrap.CheckUpgradeTarget(options.Prefix, available)
		if err != nil {
			return nil, err
		}
		status := target.Status
		if options.DryRun {
			status = "preview"
		}
		return map[string]any{"status": status, "installation": target, "selection": selected, "running_cli": map[string]string{"version": bootstrap.Version, "build": build}, "installation_changed": false, "asset_downloaded": false, "next_invocation_only": true}, nil
	}
	stage, err := os.MkdirTemp("", "ael-client-download-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, selected.Asset.Path)
	if err = source.Download(context.Background(), selected, archive); err != nil {
		return nil, err
	}
	options.From = archive
	options.TargetVersion = selected.Manifest.Version
	options.ExpectedBuild = selected.Manifest.Build
	options.ExpectedCommit = selected.Manifest.Commit
	result, err := bootstrap.Upgrade(options)
	result.Source = a.get("source", "")
	if err == nil {
		result.PublisherIdentity = "verified"
	}
	result.Warnings = append(result.Warnings, "online release publisher and asset content verified before local installation")
	return result, err
}

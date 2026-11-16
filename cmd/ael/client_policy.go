package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/release"
	"ael/internal/store"
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

func readClientPolicy(prefix string) (clientUpdatePolicy, error) {
	policy := clientUpdatePolicy{Schema: 1, Mode: "notify", SelectedBy: "legacy_default_notify"}
	path, err := store.SafePath(prefix, "share/ael/update-policy.json")
	if err != nil {
		return policy, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	if len(raw) > 64<<10 || json.Unmarshal(raw, &policy) != nil || policy.Schema != 1 || (policy.Mode != "notify" && policy.Mode != "auto" && policy.Mode != "pinned") {
		return policy, core.Error("UPDATE_POLICY_CONFLICT", "review the installation update policy")
	}
	return policy, nil
}

func trustedPolicySource(policy clientUpdatePolicy) (release.Source, error) {
	source, err := release.ReadSource(policy.Source)
	if err != nil {
		return source, err
	}
	fingerprint, err := source.Fingerprint()
	if err != nil {
		return source, err
	}
	if fingerprint != policy.SourceFingerprint || fingerprint == "" {
		return source, core.Error("RELEASE_TRUST_CHANGED", "review the changed source and publisher using client policy --source before automatic use")
	}
	return source, nil
}

func clientPolicyCommand(a args) (any, error) {
	allowed := map[string]bool{"prefix": true, "mode": true, "source": true, "clear-source": true, "version": true, "json": true, "verbose": true, "no-color": true}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	prefix, err := bootstrap.InstallationPrefix(a.get("prefix", ""))
	if err != nil {
		return nil, err
	}
	if _, err = bootstrap.InstalledClient(prefix); err != nil {
		return nil, core.Error("CLIENT_INSTALLATION_REQUIRED", "install the shared client package at this prefix before configuring its policy")
	}
	changed := a.get("mode", "") != "" || a.get("source", "") != "" || a.has("clear-source") || a.get("version", "") != ""
	var policy clientUpdatePolicy
	if changed {
		if a.has("clear-source") && a.get("source", "") != "" {
			return nil, core.Error("UPDATE_POLICY_INVALID", "choose source or clear-source")
		}
		options := args{Flags: map[string]string{"update-policy": a.get("mode", ""), "pin-version": a.get("version", ""), "release-source": a.get("source", "")}}
		if a.has("clear-source") {
			options.Flags["clear-source"] = "true"
		}
		policy, err = initUpdatePolicy(options, prefix, bufio.NewReader(strings.NewReader("")), false)
	} else {
		policy, err = readClientPolicy(prefix)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"installation": prefix, "policy": policy, "changed": changed, "scope": "shared_installation", "effective_for": "new_tool_sessions", "active_sessions_changed": false}, nil
}

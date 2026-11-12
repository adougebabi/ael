package main

import (
	"ael/internal/core"
	"ael/internal/release"
	"context"
	"runtime"
)

func releaseCommand(cmd string, a args) (any, error) {
	allowed := map[string]bool{"manifest": true, "source": true, "kind": true, "platform": true, "channel": true, "out": true, "json": true, "verbose": true, "no-color": true, "project": true}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	if cmd == "release verify" {
		if a.get("manifest", "") == "" {
			return nil, core.Error("RELEASE_MANIFEST_REQUIRED", "provide --manifest FILE")
		}
		return release.Verify(a.get("manifest", ""))
	}
	if cmd != "release check" && cmd != "release download" {
		return nil, core.Error("UNKNOWN_COMMAND", cmd)
	}
	if a.get("source", "") == "" {
		if cmd == "release download" {
			return nil, core.Error("RELEASE_SOURCE_REQUIRED", "provide an explicitly trusted --source FILE")
		}
		return release.Selection{Status: "no_configured_source", PublisherIdentity: "unverified"}, nil
	}
	source, err := release.ReadSource(a.get("source", ""))
	if err != nil {
		return nil, err
	}
	selected, err := source.Check(context.Background(), a.get("kind", "client"), a.get("platform", runtime.GOOS+"-"+runtime.GOARCH), a.get("channel", "stable"))
	if err != nil {
		return nil, err
	}
	if cmd == "release download" {
		if !selected.Available {
			return selected, nil
		}
		if a.get("out", "") == "" {
			return nil, core.Error("RELEASE_OUTPUT_REQUIRED", "provide --out NEW_FILE")
		}
		if err = source.Download(context.Background(), selected, a.get("out", "")); err != nil {
			return nil, err
		}
		return map[string]any{"status": "downloaded", "version": selected.Manifest.Version, "asset": selected.Asset, "output": a.get("out", ""), "publisher_identity": "verified", "content_integrity": "verified"}, nil
	}
	return selected, nil
}

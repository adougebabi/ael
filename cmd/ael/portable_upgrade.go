package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/release"
	"ael/internal/store"
	"context"
	"fmt"
	"golang.org/x/mod/semver"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func upgradePortable(a args) (any, error) {
	for key := range a.Flags {
		switch key {
		case "project", "from", "source", "version", "channel", "check", "dry-run", "json", "no-color", "verbose":
		default:
			return nil, core.Error("UNKNOWN_FLAG", key)
		}
	}
	root := a.get("project", ".")
	from := a.get("from", "")
	if from != "" {
		if a.get("source", "") != "" {
			return nil, core.Error("CONFLICTING_SOURCE", "choose a distribution directory or a signed source")
		}
		return bootstrap.UpgradeProjectRuntime(root, from, a.get("version", ""), a.has("dry-run") || a.has("check"))
	}
	var source release.Source
	var err error
	if path := a.get("source", ""); path != "" {
		source, err = release.ReadSource(path)
	} else {
		prefix, e := bootstrap.InstallationPrefix("")
		if e != nil {
			return nil, e
		}
		policy, e := readClientPolicy(prefix)
		if e != nil {
			return nil, e
		}
		if policy.Source != "" {
			source, err = trustedPolicySource(policy)
		} else {
			source, err = release.OfficialSource()
		}
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platforms := []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"}
	channel := a.get("channel", "stable")
	first, err := source.CheckVersion(ctx, "client", platforms[0], channel, a.get("version", ""))
	if err != nil {
		return nil, err
	}
	if !first.Available {
		return map[string]any{"status": first.Status, "installation_changed": false}, nil
	}
	m := first.Manifest
	current, err := bootstrap.CloneRuntimeVersion(root)
	if err != nil {
		return nil, err
	}
	status := "up_to_date"
	if semver.Compare("v"+current, "v"+m.Version) < 0 {
		status = "update_available"
	}
	if a.has("check") || a.has("dry-run") || status == "up_to_date" {
		return map[string]any{"status": status, "current_version": current, "available_version": m.Version, "installation_changed": false, "publisher_identity": first.PublisherIdentity}, nil
	}
	stage, err := os.MkdirTemp("", "ael-project-upgrade-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err = stageSignedDistribution(ctx, source, first, channel, stage); err != nil {
		return nil, err
	}
	r, err := bootstrap.UpgradeProjectRuntime(root, stage, m.Version, false)
	r.Source = source.ReleasesURL
	r.PublisherIdentity = first.PublisherIdentity
	return r, err
}

func stageSignedDistribution(ctx context.Context, source release.Source, first release.Selection, channel, stage string) error {
	m := first.Manifest
	var err error
	var lock strings.Builder
	fmt.Fprintln(&lock, "version", m.Version)
	noticeHash := ""
	for i, platform := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"} {
		selected := first
		if i != 0 {
			selected, err = source.CheckVersion(ctx, "client", platform, channel, m.Version)
			if err != nil {
				return err
			}
		}
		wanted, err := m.Select("client", platform)
		if err != nil {
			return err
		}
		if !selected.Available || selected.Manifest.Build != m.Build || selected.Manifest.Commit != m.Commit || selected.Asset.SHA256 != wanted.SHA256 {
			return core.Error("RELEASE_SOURCE_CHANGED", "platform release differs from the selected manifest")
		}
		archive := filepath.Join(stage, platform+".tar.gz")
		if err = source.Download(ctx, selected, archive); err != nil {
			return err
		}
		dir := filepath.Join(stage, platform)
		if err = os.Mkdir(dir, 0700); err != nil {
			return err
		}
		pkg, err := release.ExtractClientPackage(archive, dir)
		if err != nil {
			return err
		}
		if pkg.Version != m.Version || pkg.Build != m.Build || pkg.Commit != m.Commit || pkg.Platform != platform {
			return core.Error("RELEASE_PACKAGE_MISMATCH", platform)
		}
		binary := filepath.Join(dir, "ael")
		digest, err := store.HashFile(binary)
		if err != nil {
			return err
		}
		fmt.Fprintln(&lock, platform, digest, platform+"/ael")
		notice := filepath.Join(dir, "THIRD_PARTY_NOTICES.txt")
		hash, err := store.HashFile(notice)
		if err != nil {
			return err
		}
		if i == 0 {
			noticeHash = hash
			if err = store.CopyAtomic(notice, filepath.Join(stage, "NOTICE"), 0644); err != nil {
				return err
			}
		} else if hash != noticeHash {
			return core.Error("RELEASE_PACKAGE_MISMATCH", "platform notices differ")
		}
	}
	fmt.Fprintln(&lock, "notice", noticeHash, "NOTICE")
	if err = os.WriteFile(filepath.Join(stage, "ael-toolchain.lock"), []byte(lock.String()), 0600); err != nil {
		return err
	}
	return nil
}

package bootstrap

import (
	"ael/internal/release"
	"ael/internal/store"
	"os"
	"path/filepath"
)

func clientBundle(dir string) (Bundle, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return Bundle{}, err
	}
	metadata, hash, err := release.ReadClientPackage(root)
	if err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{Version: metadata.Version, Root: root, Client: true, PackageBuild: metadata.Build, PackageCommit: metadata.Commit, PackageSHA256: hash, Notice: "THIRD_PARTY_NOTICES.txt"}
	for _, file := range metadata.Files {
		if file.Path == "ael" {
			bundle.Assets = []RuntimeAsset{{Platform: metadata.Platform, Path: "ael", SHA256: file.SHA256}}
		}
		if file.Path == bundle.Notice {
			bundle.NoticeSHA256 = file.SHA256
		}
	}
	return bundle, nil
}

func machineBundle(dir string) (Bundle, error) {
	for _, name := range []string{"ael-toolchain.lock", "toolchain.lock"} {
		path, err := store.SafePath(dir, name)
		if err != nil {
			return Bundle{}, err
		}
		if _, err = os.Lstat(path); err == nil {
			return updateBundle(dir)
		} else if !os.IsNotExist(err) {
			return Bundle{}, err
		}
	}
	path, err := store.SafePath(dir, "package.json")
	if err != nil {
		return Bundle{}, err
	}
	if _, err = os.Lstat(path); err == nil {
		return clientBundle(dir)
	} else if !os.IsNotExist(err) {
		return Bundle{}, err
	}
	return updateBundle(dir)
}

func rereadBundle(bundle Bundle) (Bundle, error) {
	if bundle.Client {
		return clientBundle(bundle.Root)
	}
	return updateBundle(bundle.Root)
}

// A machine client installation preserves the complete Local source registered
// by prior versions. Missing registries can use their existing immutable cache.
func retainedLocalDistribution(prefix, clientCache string) (string, error) {
	prior, err := readInstallation(prefix)
	if err == nil && prior.Bundle != "" && prior.Bundle != clientCache {
		return prior.Bundle, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return latestLegacyDistribution(prefix)
}

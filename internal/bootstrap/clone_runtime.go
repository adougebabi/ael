package bootstrap

import (
	"ael/internal/core"
	"ael/internal/projectmode"
	"ael/internal/store"
	"bytes"
	"os"
	"path/filepath"
)

// Publish the lock last. Earlier checkouts keep resolving their old hashes
// while a new distribution is copied, including after an interrupted update.
func InstallCloneRuntime(root, prefix string) error {
	b, err := installedCloneBundle(prefix)
	if err != nil {
		return err
	}
	return installCloneBundle(root, b)
}

func InstallProjectOnlyRuntime(root, from string) error {
	b, err := DiscoverBundle(from)
	if err != nil {
		return err
	}
	b, err = updateBundle(b.Root)
	if err != nil {
		return err
	}
	native, v, err := sourceVersion(b)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	hash, err := store.HashFile(exe)
	if err != nil {
		return err
	}
	if hash != native.SHA256 || v.Version != Version || v.Build != Build {
		return core.Error("INIT_CLI_SOURCE_MISMATCH", "initialize with the client in the full distribution")
	}
	return installCloneBundle(root, b)
}

func installCloneBundle(root string, b Bundle) error {
	unlock, err := projectmode.Lock(root, true)
	if err != nil {
		return err
	}
	defer unlock()
	if version, err := CloneRuntimeVersion(root); err == nil {
		if err = checkCloneReplacement(root, version, b); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := copyCloneBinaries(root, b, false); err != nil {
		return err
	}
	notice, err := store.SafePath(root, ".ael/clone-runtime/THIRD_PARTY_NOTICES.txt")
	if err != nil {
		return err
	}
	if err = store.CopyAtomic(filepath.Join(b.Root, b.Notice), notice, 0644); err != nil {
		return err
	}
	lock, err := store.SafePath(root, ".ael/clone-runtime/toolchain.lock")
	if err != nil {
		return err
	}
	return store.Atomic(lock, projectLock(b), 0644)
}

func installedCloneBundle(prefix string) (Bundle, error) {
	in, err := readInstallation(prefix)
	if err != nil {
		return Bundle{}, err
	}
	b, err := updateBundle(in.Bundle)
	if in.Bundle == "" || err != nil || b.Version != Version || in.Build != Build {
		return Bundle{}, core.Error("CLONE_BUNDLE_REQUIRED", "install the complete four-platform distribution with upgrade --from DIR")
	}
	native, err := nativeAsset(b)
	if err != nil || native.SHA256 != in.SHA256 {
		return Bundle{}, core.Error("CLONE_BUNDLE_MISMATCH", "complete distribution must match the initializing client build")
	}
	return b, nil
}

func copyCloneBinaries(root string, b Bundle, dry bool) ([]string, error) {
	var added []string
	for _, a := range b.Assets {
		dst, err := store.SafePath(root, filepath.Join(".ael/clone-runtime", a.SHA256, "ael"))
		if err != nil {
			return nil, err
		}
		if digest, err := store.HashFile(dst); err == nil {
			if digest != a.SHA256 {
				return nil, core.Error("CLONE_RUNTIME_CONFLICT", "stored runtime differs from its content hash")
			}
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		added = append(added, filepath.Join(".ael/clone-runtime", a.SHA256, "ael"))
		if dry {
			continue
		}
		if err := store.CopyAtomic(filepath.Join(b.Root, a.Path), dst, 0755); err != nil {
			return nil, err
		}
		if digest, err := store.HashFile(dst); err != nil || digest != a.SHA256 {
			return nil, core.Error("CLONE_RUNTIME_MISMATCH", a.Platform)
		}
	}
	return added, nil
}

func checkCloneReplacement(root, previous string, b Bundle) error {
	if err := rejectDowngrade(previous, b.Version); err != nil {
		return err
	}
	if previous != b.Version {
		return nil
	}
	old, err := readEntryBytes(root, ".ael/clone-runtime/toolchain.lock", 1<<20)
	if err != nil {
		return err
	}
	if !bytes.Equal(old, projectLock(b)) {
		return core.Error("DISTRIBUTION_VERSION_CONFLICT", "same version has different runtime contents")
	}
	return nil
}

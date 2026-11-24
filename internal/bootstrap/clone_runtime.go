package bootstrap

import (
	"ael/internal/core"
	"ael/internal/store"
	"os"
	"path/filepath"
)

// Publish the lock last. Earlier checkouts keep resolving their old hashes
// while a new distribution is copied, including after an interrupted update.
func InstallCloneRuntime(root, prefix string) error {
	in, err := readInstallation(prefix)
	if err != nil {
		return err
	}
	b, err := updateBundle(in.Bundle)
	if in.Bundle == "" || err != nil || b.Version != Version {
		return core.Error("CLONE_BUNDLE_REQUIRED", "initialize from a complete four-platform AEL distribution using --from DIR")
	}
	var nativeOK bool
	for _, a := range b.Assets {
		if a.SHA256 == in.SHA256 {
			nativeOK = true
		}
	}
	if !nativeOK {
		return core.Error("CLONE_BUNDLE_MISMATCH", "complete distribution must match the initializing client build")
	}
	for _, a := range b.Assets {
		dst, err := store.SafePath(root, filepath.Join(".ael/clone-runtime", a.SHA256, "ael"))
		if err != nil {
			return err
		}
		if digest, err := store.HashFile(dst); err == nil {
			if digest != a.SHA256 {
				return core.Error("CLONE_RUNTIME_CONFLICT", "stored runtime differs from its content hash")
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := store.CopyAtomic(filepath.Join(b.Root, a.Path), dst, 0755); err != nil {
			return err
		}
		if digest, err := store.HashFile(dst); err != nil || digest != a.SHA256 {
			return core.Error("CLONE_RUNTIME_MISMATCH", a.Platform)
		}
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

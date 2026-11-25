package bootstrap

import (
	"ael/internal/core"
	"ael/internal/projectmode"
	"ael/internal/store"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// The checkout owns immutable binaries selected by a small lock file. Sessions
// can continue using their old executable after a new lock has been published.
func UpgradeProjectRuntime(root, from, expectedVersion string, preview bool) (UpdateReport, error) {
	r := UpdateReport{NextAction: "./ael update", Operation: "upgrade", Target: root, Source: from, Warnings: []string{"Run ./ael update to synchronize AGENTS.md, Skills and project entry files."}}
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return r, core.Error("MANAGED_CHILD_FORBIDDEN", "a managed child cannot replace its host toolchain")
	}
	b, err := updateBundle(from)
	if err != nil {
		return r, err
	}
	_, version, err := sourceVersion(b)
	if err != nil {
		return r, err
	}
	if expectedVersion != "" && expectedVersion != version.Version {
		return r, core.Error("UPGRADE_VERSION_MISMATCH", "selected bundle differs from requested version")
	}
	r.Available = version
	unlock, err := projectmode.Lock(root, !preview)
	if err != nil {
		return r, err
	}
	defer unlock()
	if _, err = readEntryBytes(root, ".ael/runner-access.json", 2<<20); err != nil {
		return r, err
	}
	const rel = ".ael/clone-runtime/toolchain.lock"
	old, err := readEntryBytes(root, rel, 1<<20)
	if err != nil {
		return r, err
	}
	header, _, _ := strings.Cut(string(old), "\n")
	previous, ok := strings.CutPrefix(header, "version ")
	if !ok {
		return r, core.Error("CLONE_RUNTIME_INVALID", "invalid runtime lock version")
	}
	if err = rejectDowngrade(previous, version.Version); err != nil {
		return r, err
	}
	r.Previous.Version = previous
	wanted := projectLock(b)
	if previous == version.Version && !bytes.Equal(old, wanted) {
		return r, core.Error("DISTRIBUTION_VERSION_CONFLICT", "same version has different runtime contents")
	}
	r.Files, err = copyCloneBinaries(root, b, preview)
	if err != nil {
		return r, err
	}
	notice, err := store.SafePath(root, ".ael/clone-runtime/THIRD_PARTY_NOTICES.txt")
	if err != nil {
		return r, err
	}
	noticeHash, err := store.HashFile(notice)
	if err != nil && !os.IsNotExist(err) {
		return r, err
	}
	if noticeHash != b.NoticeSHA256 {
		r.Files = append(r.Files, ".ael/clone-runtime/THIRD_PARTY_NOTICES.txt")
		if !preview {
			if err = store.CopyAtomic(filepath.Join(b.Root, b.Notice), notice, 0644); err != nil {
				return r, err
			}
		}
	}
	if !bytes.Equal(old, wanted) {
		r.Files = append(r.Files, rel)
		if !preview {
			path, err := store.SafePath(root, rel)
			if err != nil {
				return r, err
			}
			if err = store.Atomic(path, wanted, 0644); err != nil {
				return r, err
			}
		}
	}
	switch {
	case preview:
		r.Status = "preview"
	case len(r.Files) == 0:
		r.Status = "up_to_date"
	default:
		r.Status = "updated"
	}
	return r, nil
}

func RunningProjectRuntime(root string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}
	root, err = Canonical(root)
	if err != nil {
		return false
	}
	hash := filepath.Base(filepath.Dir(exe))
	return len(hash) == 64 && exe == filepath.Join(root, ".ael", "clone-runtime", hash, "ael")
}

func CloneRuntimeVersion(root string) (string, error) {
	raw, err := readEntryBytes(root, ".ael/clone-runtime/toolchain.lock", 1<<20)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	version, ok := strings.CutPrefix(line, "version ")
	if !ok || !releaseVersion.MatchString(version) {
		return "", core.Error("CLONE_RUNTIME_INVALID", "invalid runtime version")
	}
	return version, nil
}

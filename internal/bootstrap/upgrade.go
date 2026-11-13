package bootstrap

import (
	"ael/internal/core"
	"ael/internal/release"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type upgradeIntent struct {
	State        string        `json:"state"`
	PreviousHash string        `json:"previous_hash"`
	Previous     BinaryVersion `json:"previous"`
	Next         installation  `json:"next"`
	BackupDir    string        `json:"backup_dir"`
}

func readUpgradeIntent(prefix string) (upgradeIntent, error) {
	var in upgradeIntent
	p, e := store.SafePath(prefix, "share/ael/upgrade-intent.json")
	if e != nil {
		return in, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return in, e
	}
	e = json.Unmarshal(b, &in)
	return in, e
}
func metadataPreflight(prefix string) error {
	for _, rel := range []string{"share/ael/install.sha256", "share/ael/installation.json", "share/ael/upgrade-intent.json"} {
		p, e := store.SafePath(prefix, rel)
		if e != nil {
			return e
		}
		st, e := os.Lstat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return core.Error("INSTALLATION_METADATA_CONFLICT", rel)
		}
	}
	return nil
}

func installedHash(prefix string) (string, error) {
	target, e := store.SafePath(prefix, "bin/ael")
	if e != nil {
		return "", e
	}
	hash, e := store.HashFile(target)
	if os.IsNotExist(e) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	marker, e := store.SafePath(prefix, "share/ael/install.sha256")
	if e != nil {
		return "", e
	}
	b, e := os.ReadFile(marker)
	if e != nil {
		intent, err := readUpgradeIntent(prefix)
		if os.IsNotExist(e) && err == nil && intent.State == "installing" && intent.PreviousHash == "" && intent.Next.SHA256 == hash {
			return hash, nil
		}
		return "", core.Error("UNMANAGED_INSTALLATION", "existing binary has no owned install.sha256; preserve it before installing")
	}
	if strings.TrimSpace(string(b)) != hash {
		intent, e := readUpgradeIntent(prefix)
		if e == nil && intent.State == "installing" && intent.Next.SHA256 == hash && intent.PreviousHash == strings.TrimSpace(string(b)) {
			return hash, nil
		}
		return "", core.Error("INSTALLATION_MODIFIED", "existing binary differs from install.sha256; user content preserved")
	}
	return hash, nil
}
func distributionLock(b Bundle) []byte {
	text := "version " + b.Version + "\n"
	if b.Client {
		text += "client_metadata " + b.PackageSHA256 + "\n"
	}
	for _, a := range b.Assets {
		text += a.Platform + " " + a.SHA256 + " " + filepath.ToSlash(a.Path) + "\n"
	}
	hash := b.NoticeSHA256
	return []byte(text + "notice " + hash + " " + filepath.ToSlash(b.Notice) + "\n")
}
func installedBundlePath(prefix string, b Bundle) (string, error) {
	if b.Client {
		return store.SafePath(prefix, "share/ael/clients/"+b.Version)
	}
	return store.SafePath(prefix, "share/ael/distribution/"+b.Version)
}
func sameDistribution(a, b Bundle) bool {
	return string(distributionLock(a)) == string(distributionLock(b))
}
func checkDistribution(prefix string, b Bundle) error {
	dest, e := installedBundlePath(prefix, b)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(dest); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	old, e := machineBundle(dest)
	if e != nil {
		return core.Error("INSTALLED_BUNDLE_CONFLICT", dest+": "+e.Error())
	}
	if !sameDistribution(old, b) {
		return core.Error("DISTRIBUTION_VERSION_CONFLICT", b.Version+" already has different payloads; publish a new version rather than overwriting a release")
	}
	return nil
}
func installDistribution(prefix string, b Bundle) (string, error) {
	if e := validateBundleSnapshot(b); e != nil {
		return "", e
	}
	dest, e := installedBundlePath(prefix, b)
	if e != nil {
		return "", e
	}
	if e = checkDistribution(prefix, b); e != nil {
		return "", e
	}
	if _, e = os.Stat(dest); e == nil {
		return dest, nil
	}
	parent := filepath.Dir(dest)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return "", e
	}
	stage, e := os.MkdirTemp(parent, ".ael-bundle-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(stage)
	for _, a := range b.Assets {
		p, e := store.SafePath(stage, a.Path)
		if e != nil {
			return "", e
		}
		if e = store.CopyAtomic(filepath.Join(b.Root, a.Path), p, 0755); e != nil {
			return "", e
		}
	}
	notice, e := store.SafePath(stage, b.Notice)
	if e != nil {
		return "", e
	}
	if e = store.CopyAtomic(filepath.Join(b.Root, b.Notice), notice, 0644); e != nil {
		return "", e
	}
	if b.Client {
		for _, name := range []string{"package.json", "README.md"} {
			if e = store.CopyAtomic(filepath.Join(b.Root, name), filepath.Join(stage, name), 0644); e != nil {
				return "", e
			}
		}
	} else if e = store.Atomic(filepath.Join(stage, "ael-toolchain.lock"), distributionLock(b), 0644); e != nil {
		return "", e
	}
	staged, e := machineBundle(stage)
	if e != nil {
		return "", e
	}
	if !sameDistribution(staged, b) {
		return "", core.Error("SOURCE_CHANGED_DURING_UPDATE", b.Root)
	}
	if e = os.Rename(stage, dest); e != nil {
		return "", e
	}
	return dest, nil
}
func Upgrade(o UpdateOptions) (UpdateReport, error) {
	if o.From != "" {
		info, err := os.Stat(o.From)
		if err != nil {
			return UpdateReport{}, err
		}
		if info.Mode().IsRegular() {
			stage, err := os.MkdirTemp("", "ael-client-install-")
			if err != nil {
				return UpdateReport{}, err
			}
			defer os.RemoveAll(stage)
			if _, err = release.ExtractClientPackage(o.From, stage); err != nil {
				return UpdateReport{}, err
			}
			source := o.From
			o.From = stage
			result, err := upgrade(o)
			result.Source = source
			return result, err
		}
	}
	return upgrade(o)
}

func upgrade(o UpdateOptions) (UpdateReport, error) {
	r := UpdateReport{Operation: "upgrade", Files: []string{}, Warnings: []string{"local installation only; run update in each initialized project to update its pinned toolchain", "release source is local; no online update endpoint is configured"}}
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return r, core.Error("MANAGED_CHILD_FORBIDDEN", "a managed task child cannot replace its host tool")
	}
	prefix, e := prefixPath(o.Prefix)
	if e != nil {
		return r, e
	}
	r.Target = filepath.Join(prefix, "bin/ael")
	if e = metadataPreflight(prefix); e != nil {
		return r, e
	}
	unlock, e := lockInstalledUpgrade(prefix, o.DryRun || o.Check)
	if e != nil {
		return r, e
	}
	defer unlock()
	if e = prepareUpgradeRecovery(prefix, o.AckStopped && !o.DryRun && !o.Check); e != nil {
		return r, e
	}
	b, e := sourceBundle(o, true)
	if e != nil {
		return r, e
	}
	r.Source = b.Root
	if (o.ExpectedBuild != "" && o.ExpectedBuild != b.PackageBuild) || (o.ExpectedCommit != "" && o.ExpectedCommit != b.PackageCommit) {
		return r, core.Error("BUNDLE_SOURCE_MISMATCH", "client package differs from the selected signed release")
	}
	if b.Client {
		r.Warnings = []string{"machine CLI applies to subsequent invocations; compatible Remote project instructions stay pinned", "active Agents keep their acquired instructions and are not restarted", "Local updates use the retained complete distribution; select --from to change it"}
	}
	if o.TargetVersion != "" && o.TargetVersion != b.Version {
		return r, core.Error("UPGRADE_VERSION_MISMATCH", "source does not match the requested version")
	}
	native, version, e := sourceVersion(b)
	if e != nil {
		return r, e
	}
	r.Available = version
	r.ContentIntegrity = "verified"
	r.PublisherIdentity = "unverified"
	pending, pendingErr := readUpgradeIntent(prefix)
	if pendingErr != nil && !os.IsNotExist(pendingErr) {
		return r, pendingErr
	}
	recovering := pendingErr == nil && pending.State == "installing"
	if recovering && (pending.Next.SHA256 != native.SHA256 || pending.Next.Version != version.Version) {
		return r, core.Error("UPGRADE_RECOVERY_REQUIRED", "interrupted upgrade expects "+pending.Next.Source+"; inspect upgrade-intent.json and rerun with that --from source")
	}
	current, e := installedHash(prefix)
	if e != nil {
		return r, e
	}
	if current != "" {
		r.Previous, e = probeBinary(r.Target)
		if e != nil {
			return r, e
		}
		if e = rejectDowngrade(r.Previous.Version, b.Version); e != nil {
			return r, e
		}
	}
	if current != "" && !recovering && r.Previous.Version == b.Version && current != native.SHA256 {
		return r, core.Error("DISTRIBUTION_VERSION_CONFLICT", "the installed version already has different executable bytes")
	}
	if recovering {
		r.Previous = pending.Previous
		r.BackupDir = pending.BackupDir
		r.Warnings = append(r.Warnings, "finishing an interrupted upgrade from its exact distribution")
	}
	if e = checkDistribution(prefix, b); e != nil {
		return r, e
	}
	destination, _ := installedBundlePath(prefix, b)
	_, cachedErr := os.Stat(destination)
	registeredSource := b.Root
	if b.Client {
		registeredSource = destination
	}
	priorInstallation, priorError := readInstallation(prefix)
	expectedBundle := destination
	expectedClientBundle := ""
	if b.Client {
		expectedClientBundle = destination
		expectedBundle, e = retainedLocalDistribution(prefix, destination)
		if e != nil {
			return r, e
		}
	}

	if !recovering && cachedErr == nil && priorError == nil && current == native.SHA256 && priorInstallation.Version == version.Version && priorInstallation.Build == version.Build && priorInstallation.Source == registeredSource && priorInstallation.Bundle == expectedBundle && priorInstallation.ClientBundle == expectedClientBundle {
		r.Status = "up_to_date"
		if o.DryRun {
			r.Status = "preview"
		}
		return r, nil
	}
	if o.Check {
		r.Status = "update_available"
		return r, nil
	}
	cachePath := "share/ael/distribution/" + b.Version
	if b.Client {
		cachePath = "share/ael/clients/" + b.Version
	}
	r.Files = []string{"bin/ael", "share/ael/install.sha256", "share/ael/installation.json", cachePath}
	if o.DryRun {
		r.Status = "preview"
		return r, nil
	}
	release, e := updateLock(prefix, "share/ael/upgrade.lock")
	if e != nil {
		return r, e
	}
	defer release()
	latest, e := installedHash(prefix)
	if e != nil {
		return r, e
	}
	if latest != current {
		return r, core.Error("INSTALLATION_CHANGED", "retry after the competing update finishes")
	}
	if current != "" && !recovering {
		metadata, _ := os.ReadFile(filepath.Join(prefix, "share/ael/installation.json"))
		r.BackupDir = filepath.Join(prefix, "share/ael/backups", current+"-"+store.Hash(metadata)[:12])
		source, e := store.SafePath(prefix, "bin/ael")
		if e != nil {
			return r, e
		}
		backup, e := store.SafePath(r.BackupDir, "ael")
		if e != nil {
			return r, e
		}
		if actual, e := store.HashFile(backup); e == nil {
			if actual != current {
				return r, core.Error("BACKUP_CONFLICT", backup)
			}
		} else if os.IsNotExist(e) {
			if e = store.CopyAtomic(source, backup, 0755); e != nil {
				return r, e
			}
		} else {
			return r, e
		}
		for _, rel := range []string{"share/ael/install.sha256", "share/ael/installation.json"} {
			p, e := store.SafePath(prefix, rel)
			if e != nil {
				return r, e
			}
			data, e := os.ReadFile(p)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return r, e
			}
			name := filepath.Base(rel)
			dest, e := store.SafePath(r.BackupDir, name)
			if e != nil {
				return r, e
			}
			if prior, e := os.ReadFile(dest); e == nil {
				if string(prior) != string(data) {
					return r, core.Error("BACKUP_CONFLICT", dest)
				}
			} else if os.IsNotExist(e) {
				if e = store.Atomic(dest, data, 0600); e != nil {
					return r, e
				}
			} else {
				return r, e
			}
		}
	}
	installed, e := installDistribution(prefix, b)
	if e != nil {
		return r, e
	}
	sourceRoot := b.Root
	if b.Client {
		sourceRoot = installed
		r.Warnings = []string{"machine CLI updated for subsequent invocations; compatible Remote projects keep their Skill and protocol", "active Agent sessions keep their acquired instructions; no Agent is restarted", "Local project updates retain their previous complete distribution source"}
	}
	in := installation{Version: version.Version, Build: version.Build, SHA256: native.SHA256, Source: sourceRoot, Bundle: installed, Updated: time.Now().UTC()}
	if b.Client {
		retained, err := retainedLocalDistribution(prefix, installed)
		if err != nil {
			return r, err
		}
		in.Bundle = retained
		in.ClientBundle = installed
	}

	intent := upgradeIntent{State: "installing", PreviousHash: current, Previous: r.Previous, Next: in, BackupDir: r.BackupDir}
	if recovering {
		intent.PreviousHash = pending.PreviousHash
	}
	intentPath, e := store.SafePath(prefix, "share/ael/upgrade-intent.json")
	if e != nil {
		return r, e
	}
	intentData, _ := json.MarshalIndent(intent, "", "  ")
	if e = store.Atomic(intentPath, intentData, 0600); e != nil {
		return r, e
	}
	source := filepath.Join(installed, native.Path)
	// Recheck immediately before replacing the managed executable.
	if latest, e := installedHash(prefix); e != nil || latest != current {
		if e != nil {
			return r, e
		}
		return r, core.Error("INSTALLATION_CHANGED", r.Target)
	}
	if current != native.SHA256 {
		if e = store.CopyAtomic(source, r.Target, 0755); e != nil {
			return r, e
		}
	}
	if actual, e := store.HashFile(r.Target); e != nil || actual != native.SHA256 {
		return r, core.Error("UPDATE_VERIFY_FAILED", r.Target)
	}
	data, _ := json.MarshalIndent(in, "", "  ")
	marker, e := store.SafePath(prefix, "share/ael/install.sha256")
	if e != nil {
		return r, e
	}
	if e = store.Atomic(marker, []byte(native.SHA256+"\n"), 0600); e != nil {
		return r, e
	}
	registry, e := store.SafePath(prefix, "share/ael/installation.json")
	if e != nil {
		return r, e
	}
	if e = store.Atomic(registry, data, 0600); e != nil {
		return r, e
	}
	intent.State = "complete"
	intentData, _ = json.MarshalIndent(intent, "", "  ")
	if e = store.Atomic(intentPath, intentData, 0600); e != nil {
		return r, e
	}
	r.Status = "updated"
	return r, nil
}

package bootstrap

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func UpdateProject(root string, o UpdateOptions) (UpdateReport, error) {
	r := UpdateReport{Operation: "update", Files: []string{}, Warnings: []string{"restart old AEL services after updating; no service or Agent is stopped automatically"}}
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return r, core.Error("MANAGED_CHILD_FORBIDDEN", "a managed task child cannot replace its toolchain")
	}
	root, e := Canonical(root)
	if e != nil {
		return r, e
	}
	r.Target = root
	r.DataDir = DataDir(root)
	config, e := Read(root)
	if os.IsNotExist(e) {
		return r, core.Error("NOT_INITIALIZED", "current target has no .ael/project.yaml; use init to connect it, or update --project PATH")
	}
	if e != nil {
		return r, e
	}
	if config.Schema != 1 || config.ProjectID == "" {
		return r, core.Error("INVALID_PROJECT_CONFIG", root)
	}
	r.Previous.Version = config.Version
	recovery, e := prepareMaintenance(root, o.AckStopped && !o.DryRun)
	if e != nil {
		return r, e
	}
	if e = projectIdleFor(root, recovery != nil); e != nil {
		return r, e
	}
	bundle, e := sourceBundle(o, false)
	if e != nil {
		return r, e
	}
	r.Source = bundle.Root
	native, version, e := sourceVersion(bundle)
	if e != nil {
		return r, e
	}
	r.Available = version
	if e = rejectDowngrade(config.Version, version.Version); e != nil {
		return r, e
	}
	if cmp, _ := compareVersions(bundle.Version, "0.1.5"); cmp < 0 {
		return r, core.Error("UPDATE_INTERFACE_UNSUPPORTED", "use a 0.1.5+ distribution containing the update command")
	}
	exe, e := os.Executable()
	if e != nil {
		return r, e
	}
	hash, e := store.HashFile(exe)
	if e != nil {
		return r, e
	}
	if hash != native.SHA256 || version.Version != Version || version.Build != Build {
		// The target release owns its embedded Skill and migrations. Do not mix old
		// executable assets with new runtime files, even for equal version numbers.
		if os.Getenv("AEL_UPDATE_DELEGATED") == "1" {
			return r, core.Error("RECURSIVE_UPDATE", "source executable does not match its distribution")
		}
		source, e := store.SafePath(bundle.Root, native.Path)
		if e != nil {
			return r, e
		}
		args := []string{"update", "--project", root, "--from", bundle.Root}
		// Old releases already return JSON and reject unknown update flags.
		// New releases advertise support in their version probe.
		if version.OutputProtocol >= 1 {
			args = append(args, "--json")
		}
		if o.DryRun {
			args = append(args, "--dry-run")
		}
		if o.AckStopped {
			args = append(args, "--ack-stopped")
		}
		if o.Prefix != "" {
			args = append(args, "--prefix", o.Prefix)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, source, args...)
		cmd.Env = append(os.Environ(), "AEL_UPDATE_DELEGATED=1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		raw, e := cmd.Output()
		if e != nil {
			return r, core.Error("UPDATE_DELEGATION_FAILED", fmt.Sprintf("%v; %s", e, stderr.String()))
		}
		var child UpdateReport
		if e = json.Unmarshal(raw, &child); e != nil {
			return r, core.Error("INVALID_UPDATE_RESPONSE", e.Error())
		}
		if child.Operation != "update" || child.Target != root || child.Available.Version != bundle.Version || (child.Status != "updated" && child.Status != "up_to_date" && child.Status != "preview") || (o.DryRun && child.Status != "preview") {
			return r, core.Error("INVALID_UPDATE_RESPONSE", "scope, version or result mismatch")
		}
		if !o.DryRun {
			if e = verifyValidation(root, bundle, config, child.Validation); e != nil {
				return child, e
			}
		}
		return child, nil
	}
	return applyProjectUpdate(root, o, bundle, config, r)
}

func applyProjectUpdate(root string, o UpdateOptions, bundle Bundle, config Config, r UpdateReport) (UpdateReport, error) {
	if e := validateBundleSnapshot(bundle); e != nil {
		return r, e
	}
	preview, e := InitProject(root, true, true, bundle.Root)
	if e != nil {
		return r, e
	}
	if o.DryRun {
		r.Status = "preview"
		r.Files = preview.Files
		return r, nil
	}
	recovery, e := prepareMaintenance(root, o.AckStopped)
	if e != nil {
		return r, e
	}
	release, e := updateLock(root, ".ael/tool-update.lock")
	if e != nil {
		return r, e
	}
	defer release()
	gate, owner, e := beginMaintenance(root, bundle, recovery)
	if e != nil {
		return r, e
	}
	defer gate.Close()
	mutating, success := recovery != nil, false
	defer func() {
		if !success && !mutating {
			_ = endMaintenance(gate, owner)
		}
	}()
	var before map[string]string
	if owner.BackupDir != "" {
		before, e = oldBackupHashes(root, owner.BackupDir)
		r.BackupDir = owner.BackupDir
	} else {
		before, e = managedHashes(root)
		if e == nil {
			r.BackupDir, e = backupAssets(root, before)
		}
		if e == nil {
			e = saveMaintenanceBackup(gate, owner, r.BackupDir)
		}
	}
	if e != nil {
		return r, e
	}
	mutating = true
	if recovery != nil {
		if e = gate.Flush(); e != nil {
			return r, core.Error("PROJECT_UPDATE_FAILED", e.Error()+"; exact-source maintenance retained")
		}
	}
	if _, e = InitProject(root, false, true, bundle.Root); e != nil {
		return r, core.Error("PROJECT_UPDATE_FAILED", e.Error()+"; backup: "+r.BackupDir+"; retry exact source with --ack-stopped after updater exits")
	}
	r.Validation, e = validateOwnAssets(root, bundle, config)
	if e != nil {
		return r, e
	}
	after, e := managedHashes(root)
	if e != nil {
		return r, e
	}
	r.Files = changedFiles(before, after)
	r.Status = "up_to_date"
	if len(r.Files) > 0 {
		r.Status = "updated"
		r.RestartRequired = true
	}
	if e = endMaintenance(gate, owner); e != nil {
		return r, core.Error("PROJECT_UPDATE_FAILED", "assets verified but maintenance release failed: "+e.Error())
	}
	success = true
	return r, nil
}
func validateUpdatedProject(root string, b Bundle, before Config) error {
	after, e := Read(root)
	if e != nil {
		return e
	}
	if after.ProjectID != before.ProjectID || after.Mode != before.Mode || after.Version != b.Version {
		return core.Error("PROJECT_IDENTITY_CHANGED", "project identity/mode/version mismatch")
	}
	for _, asset := range b.Assets {
		p, e := store.SafePath(root, filepath.Join(".ael/bin", asset.Platform, "ael"))
		if e != nil {
			return e
		}
		actual, e := store.HashFile(p)
		if e != nil || actual != asset.SHA256 {
			return core.Error("UPDATE_VERIFY_FAILED", asset.Platform)
		}
	}
	return nil
}

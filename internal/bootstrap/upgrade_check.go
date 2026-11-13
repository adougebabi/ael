package bootstrap

import (
	"ael/internal/core"
	"os"
	"path/filepath"
)

// CheckUpgradeTarget inspects the selected machine installation without staging
// incoming bytes. Downloaded content is still verified by the actual upgrade.
func CheckUpgradeTarget(prefix string, available BinaryVersion) (UpdateReport, error) {
	result := UpdateReport{Operation: "upgrade", Available: available, Files: []string{}, Warnings: []string{"version check uses the signed release metadata; asset bytes are not downloaded"}}
	root, err := prefixPath(prefix)
	if err != nil {
		return result, err
	}
	result.Target = filepath.Join(root, "bin/ael")
	if err = metadataPreflight(root); err != nil {
		return result, err
	}
	unlock, err := lockInstalledUpgrade(root, true)
	if err != nil {
		return result, err
	}
	defer unlock()
	if err = noUpdateLock(root, "share/ael/upgrade.lock"); err != nil {
		return result, err
	}
	pending, pendingErr := readUpgradeIntent(root)
	if pendingErr != nil && !os.IsNotExist(pendingErr) {
		return result, pendingErr
	}
	if pendingErr == nil && pending.State == "installing" {
		return result, core.Error("UPGRADE_RECOVERY_REQUIRED", "complete the exact interrupted source before checking another version")
	}
	hash, err := installedHash(root)
	if err != nil {
		return result, err
	}
	result.Status = "install_available"
	if hash == "" {
		return result, nil
	}
	result.Previous, err = probeBinary(result.Target)
	if err != nil {
		return result, err
	}
	if err = rejectDowngrade(result.Previous.Version, available.Version); err != nil {
		return result, err
	}
	result.Status = "update_available"
	if comparison, err := compareVersions(available.Version, result.Previous.Version); err != nil {
		return result, err
	} else if comparison == 0 {
		if result.Previous.Build != available.Build {
			return result, core.Error("DISTRIBUTION_VERSION_CONFLICT", "selected version has a different build than the installed version")
		}
		result.Status = "up_to_date"
	}
	return result, nil
}

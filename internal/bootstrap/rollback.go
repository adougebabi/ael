package bootstrap

import (
	"ael/internal/core"
	"ael/internal/store"
	"path/filepath"
	"strings"
)

// Rollback validates a matching old executable, metadata and retained bundle,
// then commits through the existing upgrade lock, backup and recovery journal.
func Rollback(options UpdateOptions) (UpdateReport, error) {
	var result UpdateReport
	if options.From == "" {
		return result, core.Error("ROLLBACK_BACKUP_REQUIRED", "select the reported backup_dir with --from")
	}
	prefix, err := prefixPath(options.Prefix)
	if err != nil {
		return result, err
	}
	backup, err := filepath.Abs(options.From)
	if err != nil {
		return result, err
	}
	var previous installation
	if err = readEntryJSON(backup, "installation.json", &previous); err != nil {
		return result, core.Error("ROLLBACK_BACKUP_INVALID", "backup must include matching executable and installation metadata")
	}
	marker, err := readEntryBytes(backup, "install.sha256", 256)
	if err != nil || strings.TrimSpace(string(marker)) != previous.SHA256 {
		return result, core.Error("ROLLBACK_BACKUP_INVALID", "backup installation marker differs")
	}
	path, err := store.SafePath(backup, "ael")
	if err != nil {
		return result, err
	}
	hash, err := store.HashFile(path)
	if err != nil || hash != previous.SHA256 {
		return result, core.Error("ROLLBACK_BACKUP_INVALID", "backup executable hash differs")
	}
	cache := previous.ClientBundle
	if cache == "" {
		cache = previous.Bundle
	}
	rel, err := filepath.Rel(prefix, cache)
	if err != nil || (!strings.HasPrefix(filepath.ToSlash(rel), "share/ael/clients/") && !strings.HasPrefix(filepath.ToSlash(rel), "share/ael/distribution/")) {
		return result, core.Error("ROLLBACK_BACKUP_INVALID", "backup refers to another installation cache")
	}
	bundle, err := machineBundle(cache)
	if err != nil {
		return result, err
	}
	asset, version, err := sourceVersion(bundle)
	if err != nil {
		return result, err
	}
	if asset.SHA256 != previous.SHA256 || version.Version != previous.Version || version.Build != previous.Build {
		return result, core.Error("ROLLBACK_BACKUP_INVALID", "retained distribution differs from backup")
	}
	options.From, options.Prefix, options.TargetVersion, options.rollback = cache, prefix, previous.Version, true
	result, err = Upgrade(options)
	result.Operation, result.Source = "rollback", backup
	return result, err
}

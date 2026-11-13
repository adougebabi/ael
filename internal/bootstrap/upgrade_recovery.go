package bootstrap

import (
	"ael/internal/core"
	"ael/internal/projectmode"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
)

// Existing installations use the metadata directory as a process-owned flock.
// This serializes recovery without creating any files during a read-only preview.
func lockInstalledUpgrade(prefix string, preview bool) (func(), error) {
	directory, err := store.SafePath(prefix, "share/ael")
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(directory); os.IsNotExist(err) {
		return func() {}, nil
	} else if err != nil {
		return nil, err
	}
	return projectmode.Lock(directory, !preview)
}

func prepareUpgradeRecovery(prefix string, acknowledged bool) error {
	lock, err := store.SafePath(prefix, "share/ael/upgrade.lock")
	if err != nil {
		return err
	}
	if _, err = os.Lstat(lock); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if !acknowledged {
		return core.Error("UPDATE_BUSY", "inspect the old updater, then retry its exact source with --ack-stopped")
	}
	path, err := store.SafePath(lock, "owner.json")
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return core.Error("UPDATE_OWNER_UNKNOWN", "cannot read the interrupted updater owner")
	}
	var owner struct {
		PID      int    `json:"pid"`
		Identity string `json:"identity"`
	}
	if json.Unmarshal(raw, &owner) != nil {
		return core.Error("UPDATE_OWNER_UNKNOWN", "invalid updater owner")
	}
	if err = stoppedOwner(owner.PID, owner.Identity); err != nil {
		return err
	}
	latest, err := os.ReadFile(path)
	if err != nil || string(latest) != string(raw) {
		return core.Error("UPDATE_BUSY", "updater owner changed")
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return os.Remove(filepath.Clean(lock))
}

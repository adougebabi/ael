package server

import (
	"ael/internal/store"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const restorePlanName = "server-restore.json"
const restoreStageName = ".ael-server-restore"

type serverRestorePlan struct {
	Schema     int                `json:"schema"`
	BackupHash string             `json:"backup_hash"`
	Phase      string             `json:"phase"`
	Files      []ServerBackupFile `json:"files,omitempty"`
}

func writeRestorePlan(root string, plan serverRestorePlan) error {
	raw, _ := json.MarshalIndent(plan, "", "  ")
	return store.Atomic(filepath.Join(root, restorePlanName), raw, 0600)
}

func prepareRestoredData(stage, sourceRoot, target string) error {
	service, err := openStandalone(stage, true)
	if err != nil {
		return err
	}
	defer service.Close()
	state, err := service.Maintenance()
	if err != nil {
		return err
	}
	state.Schema, state.Mode, state.Epoch = 1, "quiescing", state.Epoch+1
	state.Actor, state.Reason, state.Updated = "filesystem-operator", "full server restore; old API responsibility revoked; host state unknown", time.Now().UTC()
	state.Operation, state.InputHash = "", ""
	if err = service.store.Write(func(tx *store.Tx) error {
		if err := writeMaintenance(tx, state); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM server_sessions"); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM server_clone_grants"); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE server_tokens SET revoked=1")
		return err
	}); err != nil {
		return err
	}
	projects, err := service.projects(standalonePrincipal{Admin: true})
	if err != nil {
		return err
	}
	databases := []*store.Store{service.store}
	for _, project := range projects {
		projectService, err := service.projectService(project.ID)
		if err != nil {
			return err
		}
		databases = append(databases, projectService.Store)
	}
	// Existing pending projections first move to the private working copy.
	// Interruption then updates those same intents through the shared core save.
	for _, database := range databases {
		if err = rebaseServerOutbox(database, sourceRoot, stage); err != nil {
			return err
		}
	}
	count, err := service.interruptMaintenanceResponsibilities(state.Actor, state.Reason, state.Epoch)
	if err != nil {
		return err
	}
	state.Mode, state.Revoked = "quiesced", count
	for _, database := range databases {
		if err = rebaseServerOutbox(database, stage, target); err != nil {
			return err
		}
	}
	if err = service.store.Write(func(tx *store.Tx) error {
		if err := writeMaintenance(tx, state); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO metadata(key,body) VALUES('server:format','2') ON CONFLICT(key) DO UPDATE SET body='2'"); err != nil {
			return err
		}
		return tx.Event("", "server_restored", map[string]any{"actor": "filesystem-operator", "epoch": state.Epoch, "credentials": "sessions cleared and project tokens revoked", "host_execution_state": "unknown"})
	}); err != nil {
		return err
	}
	for _, database := range databases {
		var busy, frames, checkpointed int
		if err = database.DB.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); err != nil {
			return err
		}
		if busy != 0 {
			return errors.New("SERVER_RESTORE_DATABASE_BUSY")
		}
	}
	return store.Atomic(filepath.Join(stage, serverMarker), []byte("AEL-STANDALONE-2\n"), 0600)
}

// RestoreStandalone resumes into a selected empty volume. The private plan owns
// partial files, so the mount point itself never needs an atomic directory rename.
func RestoreStandalone(from, target string) (map[string]any, error) {
	manifest, hash, err := VerifyServerBackup(from)
	if err != nil {
		return nil, err
	}
	target, err = canonicalServerPath(target)
	if err != nil || target == filepath.Dir(target) {
		return nil, errors.New("SERVER_RESTORE_PATH_INVALID")
	}
	backupRoot, pathErr := canonicalServerPath(from)
	if pathErr != nil {
		return nil, pathErr
	}
	if !outsideServerRoot(backupRoot, target) || !outsideServerRoot(target, backupRoot) {
		return nil, errors.New("SERVER_RESTORE_PATH_OVERLAP")
	}
	if err = os.MkdirAll(target, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("SERVER_RESTORE_DIRECTORY_PERMISSIONS")
	}
	if err = standaloneNoACL(target); err != nil {
		return nil, err
	}
	if err = standaloneLocalFilesystem(target); err != nil {
		return nil, err
	}
	var plan serverRestorePlan
	_, readErr := readServerJSON(target, restorePlanName, &plan)
	if readErr == nil {
		if plan.Schema != 1 || plan.BackupHash != hash || (plan.Phase != "staging" && plan.Phase != "prepared" && plan.Phase != "committed") {
			return nil, errors.New("SERVER_RESTORE_RECOVERY_MISMATCH")
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	} else {
		entries, err := os.ReadDir(target)
		if err != nil || len(entries) != 0 {
			return nil, errors.New("SERVER_RESTORE_TARGET_NOT_EMPTY")
		}
		if err = os.Chmod(target, 0700); err != nil {
			return nil, err
		}
		plan = serverRestorePlan{Schema: 1, BackupHash: hash, Phase: "staging"}
		if err = writeRestorePlan(target, plan); err != nil {
			return nil, err
		}
	}
	if err = os.Chmod(target, 0700); err != nil {
		return nil, err
	}
	stage := filepath.Join(target, restoreStageName)
	if plan.Phase == "staging" {
		if err = os.RemoveAll(stage); err != nil {
			return nil, err
		}
		if err = os.Mkdir(stage, 0700); err != nil {
			return nil, err
		}
		for _, file := range manifest.Files {
			source, err := store.SafePath(from, filepath.FromSlash(file.Path))
			if err != nil {
				return nil, err
			}
			destination, err := store.SafePath(stage, filepath.FromSlash(file.Path))
			if err != nil {
				return nil, err
			}
			if err = store.CopyAtomic(source, destination, 0600); err != nil {
				return nil, err
			}
		}
		if err = prepareRestoredData(stage, manifest.SourceRoot, target); err != nil {
			return nil, err
		}
		plan.Files, err = serverInventory(stage)
		if err != nil {
			return nil, err
		}
		plan.Phase = "prepared"
		if err = writeRestorePlan(target, plan); err != nil {
			return nil, err
		}
	}
	if plan.Phase == "prepared" {
		if err = verifyServerFiles(stage, plan.Files); err != nil {
			return nil, err
		}
		files := append([]ServerBackupFile{}, plan.Files...)
		sort.SliceStable(files, func(i, j int) bool { return files[i].Path != serverMarker && files[j].Path == serverMarker })
		for _, file := range files {
			destination, err := store.SafePath(target, filepath.FromSlash(file.Path))
			if err != nil {
				return nil, err
			}
			_, statErr := os.Lstat(destination)
			if statErr == nil {
				if err = serverSnapshotFileSafe(destination); err != nil {
					return nil, err
				}
			} else if !os.IsNotExist(statErr) {
				return nil, statErr
			}
			actual, readErr := store.HashFile(destination)
			if readErr == nil {
				if actual != file.SHA256 {
					return nil, errors.New("SERVER_RESTORE_FILE_CONFLICT")
				}
				continue
			}
			if !os.IsNotExist(readErr) {
				return nil, readErr
			}
			if err = store.CopyAtomic(filepath.Join(stage, filepath.FromSlash(file.Path)), destination, 0600); err != nil {
				return nil, err
			}
		}
		if err = verifyServerFiles(target, plan.Files); err != nil {
			return nil, err
		}
		plan.Phase = "committed"
		if err = writeRestorePlan(target, plan); err != nil {
			return nil, err
		}
	}
	if err = verifyServerFiles(target, plan.Files); err != nil {
		return nil, err
	}
	if err = os.RemoveAll(stage); err != nil {
		return nil, err
	}
	if err = os.Remove(filepath.Join(target, restorePlanName)); err != nil {
		return nil, err
	}
	return map[string]any{"status": "restored_quiesced", "data_format": 2, "files": len(plan.Files), "credentials": "log in again and issue new project tokens", "old_host_execution_state": "unknown"}, nil
}

package server

import (
	"ael/internal/store"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const serverDataFormatVersion = 2
const serverMarker = "ael-server.marker"

func serverDataFormat(dir string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(dir, serverMarker))
	if err != nil {
		return 0, err
	}
	switch string(raw) {
	case "AEL-STANDALONE-1\n":
		return 1, nil
	case "AEL-STANDALONE-2\n":
		return 2, nil
	}
	return 0, errors.New("SERVER_SCHEMA_UNSUPPORTED")
}

// MigrateStandalone is an explicit offline operator action for pre-maintenance
// servers. The new marker is committed before any format-2 state is written.
func MigrateStandalone(dir string, acknowledged bool) (map[string]any, error) {
	if !acknowledged {
		return nil, errors.New("SERVER_STOP_CONFIRMATION_REQUIRED")
	}
	service, err := openStandalone(dir, true)
	if err != nil {
		return nil, err
	}
	defer service.Close()
	unlock, err := service.lockAuthorization(true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	format, err := serverDataFormat(dir)
	if err != nil {
		return nil, err
	}
	pending := filepath.Join(dir, "server-migration.json")
	if _, err = os.Lstat(pending); err == nil {
		var intent struct {
			From int `json:"from"`
			To   int `json:"to"`
		}
		if _, err = readServerJSON(dir, "server-migration.json", &intent); err != nil || intent.To != 2 || intent.From != 1 {
			return nil, errors.New("SERVER_MIGRATION_INVALID")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if format == 2 {
		return map[string]any{"status": "up_to_date", "data_format": 2}, nil
	} else {
		body, _ := json.Marshal(map[string]int{"from": 1, "to": 2})
		if err = store.Atomic(pending, body, 0600); err != nil {
			return nil, err
		}
	}
	if err = store.Atomic(filepath.Join(dir, serverMarker), []byte("AEL-STANDALONE-2\n"), 0600); err != nil {
		return nil, err
	}
	state, err := service.Maintenance()
	if err != nil {
		return nil, err
	}
	if state.Mode != "quiescing" {
		state.Epoch++
		state.Mode = "quiescing"
		state.Actor = "filesystem-operator"
		state.Reason = "server format migration; host execution state unknown"
		state.Updated = time.Now().UTC()
	}
	if err = service.store.Write(func(tx *store.Tx) error { return writeMaintenance(tx, state) }); err != nil {
		return nil, err
	}
	count, err := service.interruptMaintenanceResponsibilities(state.Actor, state.Reason, state.Epoch)
	if err != nil {
		return nil, err
	}
	state.Mode, state.Revoked = "quiesced", count
	if err = service.store.Write(func(tx *store.Tx) error {
		if err := writeMaintenance(tx, state); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO metadata(key,body) VALUES('server:format','2') ON CONFLICT(key) DO UPDATE SET body='2'")
		return err
	}); err != nil {
		return nil, err
	}
	if err = os.Remove(pending); err != nil {
		return nil, err
	}
	return map[string]any{"status": "migrated_quiesced", "data_format": 2, "epoch": state.Epoch, "revoked_responsibilities": count}, nil
}

func checkServerFormatFiles(dir string, operator bool) error {
	for _, name := range []string{"server-migration.json", "server-restore.json"} {
		path, err := store.SafePath(dir, name)
		if err != nil {
			return err
		}
		if _, err = os.Lstat(path); err == nil && (!operator || name == "server-restore.json") {
			return errors.New("SERVER_RECOVERY_REQUIRED")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func rebaseServerOutbox(database *store.Store, source, target string) error {
	return database.Write(func(tx *store.Tx) error {
		rows, err := tx.Conn.QueryContext(context.Background(), "SELECT path FROM outbox")
		if err != nil {
			return err
		}
		paths := []string{}
		for rows.Next() {
			var path string
			if err = rows.Scan(&path); err != nil {
				rows.Close()
				return err
			}
			paths = append(paths, path)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, path := range paths {
			resolved, err := canonicalServerPath(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(source, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return errors.New("SERVER_OUTBOX_PATH_UNSUPPORTED")
			}
			slash := filepath.ToSlash(relative)
			parts := strings.Split(slash, "/")
			if len(parts) < 5 || parts[0] != "projects" || len(parts[1]) != 32 || (parts[2] != "workspace" && parts[2] != "runtime") {
				return errors.New("SERVER_OUTBOX_PATH_UNSUPPORTED")
			}
			destination, err := store.SafePath(target, relative)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("UPDATE outbox SET path=? WHERE path=?", destination, path); err != nil {
				return err
			}
		}
		return nil
	})
}

func canonicalServerPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("SERVER_ABSOLUTE_PATH_REQUIRED")
	}
	probe := filepath.Clean(path)
	suffix := []string{}
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", errors.New("SERVER_PATH_INVALID")
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	for index := len(suffix) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, suffix[index])
	}
	return resolved, nil
}

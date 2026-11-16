package bootstrap

import (
	"ael/internal/core"
	"ael/internal/process"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type maintenance struct {
	PID       int    `json:"pid"`
	Identity  string `json:"identity"`
	Token     string `json:"token"`
	SourceSHA string `json:"source_sha256"`
	Source    string `json:"source"`
	BackupDir string `json:"backup_dir"`
}

func stoppedOwner(pid int, identity string) error {
	if pid <= 0 {
		return core.Error("UPDATE_OWNER_UNKNOWN", "cannot prove the old updater stopped")
	}
	current, e := process.Identity(pid)
	if e == nil && (identity == "" || identity == current) {
		return core.Error("UPDATE_OWNER_ALIVE", fmt.Sprint(pid))
	}
	if e != nil && process.Alive(pid) {
		return core.Error("UPDATE_OWNER_UNKNOWN", fmt.Sprint(pid))
	}
	return nil
}
func readMaintenance(root string) (*maintenance, error) {
	db, e := projectReadDB(root)
	if e != nil || db == nil {
		return nil, e
	}
	defer db.Close()
	var body string
	e = db.QueryRow("SELECT body FROM metadata WHERE key='tool-update'").Scan(&body)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var m maintenance
	if e = json.Unmarshal([]byte(body), &m); e != nil {
		return nil, e
	}
	if m.Token == "" || m.PID <= 0 {
		return nil, core.Error("UPDATE_OWNER_UNKNOWN", "invalid maintenance record")
	}
	return &m, nil
}
func prepareMaintenance(root string, ack bool) (*maintenance, error) {
	m, e := readMaintenance(root)
	if e != nil {
		return nil, e
	}
	if m != nil {
		if !ack {
			return nil, core.Error("UPDATE_RECOVERY_REQUIRED", "inspect the interrupted updater, then retry the exact source with --ack-stopped")
		}
		if e = stoppedOwner(m.PID, m.Identity); e != nil {
			return nil, e
		}
	}
	p, e := store.SafePath(root, ".ael/tool-update.lock")
	if e != nil {
		return nil, e
	}
	if _, e = os.Stat(p); e == nil {
		if !ack {
			return nil, core.Error("UPDATE_BUSY", p)
		}
		ownerPath, e := store.SafePath(root, ".ael/tool-update.lock/owner.json")
		if e != nil {
			return nil, e
		}
		raw, e := os.ReadFile(ownerPath)
		if e != nil {
			return nil, core.Error("UPDATE_OWNER_UNKNOWN", p)
		}
		var owner struct {
			PID      int    `json:"pid"`
			Identity string `json:"identity"`
		}
		if e = json.Unmarshal(raw, &owner); e != nil {
			return nil, e
		}
		if e = stoppedOwner(owner.PID, owner.Identity); e != nil {
			return nil, e
		}
		latest, e := os.ReadFile(ownerPath)
		if e != nil || string(latest) != string(raw) {
			return nil, core.Error("UPDATE_BUSY", "owner changed")
		}
		if e = os.Remove(ownerPath); e != nil {
			return nil, e
		}
		if e = os.Remove(p); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return m, nil
}

// The metadata switch and legacy-compatible triggers commit under the same
// BEGIN IMMEDIATE as the idle check. No writer can pass between those operations.
func beginMaintenance(root string, b Bundle, old *maintenance) (*store.Store, *maintenance, error) {
	s, e := store.Open(DataDir(root))
	if e != nil {
		return nil, nil, e
	}
	identity, e := process.Identity(os.Getpid())
	if e != nil {
		s.Close()
		return nil, nil, e
	}
	m := &maintenance{PID: os.Getpid(), Identity: identity, Token: core.ID("maintenance-"), SourceSHA: store.Hash(distributionLock(b)), Source: b.Root}
	e = s.Write(func(tx *store.Tx) error {
		var raw string
		err := tx.Row("SELECT body FROM metadata WHERE key='tool-update'").Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if old == nil && err == nil {
			return core.Error("UPDATE_BUSY", "maintenance owner changed")
		}
		if old != nil {
			var current maintenance
			if err != nil || json.Unmarshal([]byte(raw), &current) != nil || current.Token != old.Token {
				return core.Error("UPDATE_BUSY", "maintenance owner changed")
			}
			if e := stoppedOwner(current.PID, current.Identity); e != nil {
				return e
			}
			if current.SourceSHA != m.SourceSHA {
				return core.Error("UPDATE_RECOVERY_REQUIRED", "retry the exact distribution recorded at "+current.Source)
			}
			m.BackupDir = current.BackupDir
		}
		active, e := blockingUpdateWriters(tx.Conn.QueryContext(context.Background(), updateWritersQuery))
		if e != nil {
			return e
		}
		var locks, pending int
		if e := tx.Row("SELECT count(*) FROM workspace_locks").Scan(&locks); e != nil {
			return e
		}
		if len(active) > 0 || locks > 0 {
			return core.Error("PROJECT_BUSY", "writer appeared before maintenance transaction")
		}
		if e := tx.Row("SELECT count(*) FROM outbox WHERE state IN ('pending','delete_pending')").Scan(&pending); e != nil {
			return e
		}
		if pending > 0 && old == nil {
			return core.Error("PROJECT_REPAIR_REQUIRED", "pending file intents")
		}
		for _, table := range []string{"tasks", "workspace_locks", "submissions", "events"} {
			for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
				name := "ael_tool_maintenance_" + table + "_" + action
				statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s BEFORE %s ON %s WHEN EXISTS(SELECT 1 FROM metadata WHERE key='tool-update') BEGIN SELECT RAISE(ABORT,'PROJECT_UPDATING'); END`, name, action, table)
				if _, e := tx.Exec(statement); e != nil {
					return e
				}
			}
		}
		body, _ := json.Marshal(m)
		if _, e := tx.Exec("INSERT INTO metadata(key,body) VALUES('tool-update',?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", string(body)); e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO metadata(key,body) VALUES('tool-generation',?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", m.Token)
		return e
	})
	if e != nil {
		s.Close()
		return nil, nil, e
	}
	return s, m, nil
}
func saveMaintenanceBackup(s *store.Store, m *maintenance, dir string) error {
	m.BackupDir = dir
	body, _ := json.Marshal(m)
	return s.Write(func(tx *store.Tx) error {
		result, e := tx.Exec("UPDATE metadata SET body=? WHERE key='tool-update' AND json_extract(body,'$.token')=?", string(body), m.Token)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return core.Error("UPDATE_BUSY", "maintenance token changed")
		}
		return nil
	})
}
func endMaintenance(s *store.Store, m *maintenance) error {
	return s.Write(func(tx *store.Tx) error {
		result, e := tx.Exec("DELETE FROM metadata WHERE key='tool-update' AND json_extract(body,'$.token')=?", m.Token)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return core.Error("UPDATE_BUSY", "maintenance token changed before release")
		}
		return nil
	})
}
func oldBackupHashes(root, dir string) (map[string]string, error) {
	rel, e := filepath.Rel(DataDir(root), dir)
	if e != nil {
		return nil, e
	}
	p, e := store.SafePath(DataDir(root), rel+"/manifest.json")
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var hashes map[string]string
	if e = json.Unmarshal(b, &hashes); e != nil {
		return nil, e
	}
	for rel, want := range hashes {
		p, e := store.SafePath(dir, filepath.Join("project", rel))
		if e != nil {
			return nil, e
		}
		hash, e := store.HashFile(p)
		if e != nil || hash != want {
			return nil, core.Error("BACKUP_CONFLICT", rel)
		}
	}
	return hashes, nil
}

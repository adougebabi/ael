package bootstrap

import (
	"ael/internal/core"
	"ael/internal/process"
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func updateLock(root, rel string) (func(), error) {
	p, e := store.SafePath(root, rel)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return nil, e
	}
	if e = os.Mkdir(p, 0700); e != nil {
		return nil, core.Error("UPDATE_BUSY", p+"; inspect the owner and any interrupted updater before removing a stale lock")
	}
	identity, _ := process.Identity(os.Getpid())
	b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "identity": identity, "token": core.ID("update-"), "started": time.Now().UTC()})
	cleanup := func() {
		old, e := os.ReadFile(filepath.Join(p, "owner.json"))
		if e == nil && string(old) != string(b) {
			return
		}
		_ = os.Remove(filepath.Join(p, "owner.json"))
		_ = os.Remove(p)
	}
	if e = store.Atomic(filepath.Join(p, "owner.json"), b, 0600); e != nil {
		cleanup()
		return nil, e
	}
	return cleanup, nil
}
func noUpdateLock(root, rel string) error {
	p, e := store.SafePath(root, rel)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(p); e == nil {
		return core.Error("UPDATE_BUSY", p)
	} else if !os.IsNotExist(e) {
		return e
	}
	return nil
}

// Read-only: do not call store.Open (which initializes schemas and flushes files).
func projectReadDB(root string) (*sql.DB, error) {
	if e := ValidateDataLocation(root, DataDir(root)); e != nil {
		return nil, e
	}
	dir := DataDir(root)
	p, e := store.SafePath(dir, "runtime.db")
	if e != nil {
		return nil, e
	}
	if _, e = os.Stat(p); os.IsNotExist(e) {
		return nil, nil
	} else if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: p}).String()+"?mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func projectIdle(root string) error { return projectIdleFor(root, false) }

// Expired leases cannot submit or renew. Preserve their records during tool
// maintenance, while still protecting any live bound host process.
func blockingUpdateWriters(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := []string{}
	now := time.Now()
	for rows.Next() {
		var task, raw string
		if err := rows.Scan(&task, &raw); err != nil {
			return nil, err
		}
		var attempt core.Attempt
		if err := json.Unmarshal([]byte(raw), &attempt); err != nil {
			return nil, err
		}
		live := attempt.Lease.IsZero() || attempt.Lease.After(now)
		if attempt.PID > 0 && (process.Alive(attempt.PID) || process.GroupAlive(attempt.PID)) {
			live = true
		}
		if attempt.OwnerPID > 0 {
			identity, err := process.Identity(attempt.OwnerPID)
			if err == nil && (attempt.OwnerIdentity == "" || identity == attempt.OwnerIdentity) || err != nil && process.Alive(attempt.OwnerPID) {
				live = true
			}
		}
		if live {
			active = append(active, task+"/"+attempt.ID+" ("+attempt.Status+")")
		}
	}
	return active, rows.Err()
}

const updateWritersQuery = `SELECT tasks.id,a.value FROM tasks,json_each(tasks.body,'$.attempts') a WHERE json_extract(a.value,'$.status') IN ('running','interrupted')`

func projectIdleFor(root string, recovering bool) error {
	db, e := projectReadDB(root)
	if e != nil || db == nil {
		return e
	}
	defer db.Close()
	active, e := blockingUpdateWriters(db.Query(updateWritersQuery))
	if e != nil {
		return core.Error("RUNTIME_INSPECTION_FAILED", e.Error())
	}
	var locks, pending int
	if e = db.QueryRow("SELECT count(*) FROM workspace_locks").Scan(&locks); e != nil {
		return e
	}
	if len(active) > 0 || locks > 0 {
		return core.Error("PROJECT_BUSY", strings.Join(active, ", ")+fmt.Sprintf("; %d workspace fences; finish or safely recover old writers before updating", locks))
	}
	if e = db.QueryRow("SELECT count(*) FROM outbox WHERE state IN ('pending','delete_pending')").Scan(&pending); e != nil {
		return e
	}
	if pending > 0 && !recovering {
		return core.Error("PROJECT_REPAIR_REQUIRED", "pending file intents exist; repair with the owning version before updating")
	}
	return nil
}

func managedHashes(root string) (map[string]string, error) {
	paths := map[string]bool{"ael": true, "AGENTS.md": true, ".ael/project.yaml": true, ".ael/assets.json": true, ".ael/toolchain.lock": true, ".ael/knowledge-sources.json": true}
	manifestPath, e := store.SafePath(root, ".ael/assets.json")
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(manifestPath)
	if e != nil {
		return nil, e
	}
	var manifest map[string]string
	if e = json.Unmarshal(b, &manifest); e != nil {
		return nil, e
	}
	for rel := range manifest {
		if rel != "AGENTS:block" {
			paths[rel] = true
		}
	}
	out := map[string]string{}
	for rel := range paths {
		p, e := store.SafePath(root, rel)
		if e != nil {
			return nil, e
		}
		hash, e := store.HashFile(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		out[rel] = hash
	}
	return out, nil
}
func backupAssets(root string, hashes map[string]string) (string, error) {
	b, _ := json.Marshal(hashes)
	dir := filepath.Join(DataDir(root), "updates", "backups", store.Hash(b))
	if e := ValidateDataLocation(root, dir); e != nil {
		return "", e
	}
	for rel, hash := range hashes {
		source, e := store.SafePath(root, rel)
		if e != nil {
			return "", e
		}
		dest, e := store.SafePath(dir, filepath.Join("project", rel))
		if e != nil {
			return "", e
		}
		if old, e := store.HashFile(dest); e == nil {
			if old != hash {
				return "", core.Error("BACKUP_CONFLICT", dest)
			}
			continue
		} else if !os.IsNotExist(e) {
			return "", e
		}
		st, e := os.Stat(source)
		if e != nil {
			return "", e
		}
		if e = store.CopyAtomic(source, dest, st.Mode().Perm()); e != nil {
			return "", e
		}
		if actual, e := store.HashFile(dest); e != nil || actual != hash {
			return "", core.Error("SOURCE_CHANGED_DURING_BACKUP", rel)
		}
	}
	if e := store.Atomic(filepath.Join(dir, "manifest.json"), b, 0600); e != nil {
		return "", e
	}
	return dir, nil
}
func changedFiles(before, after map[string]string) []string {
	all := map[string]bool{}
	for p := range before {
		all[p] = true
	}
	for p := range after {
		all[p] = true
	}
	out := []string{}
	for p := range all {
		if before[p] != after[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

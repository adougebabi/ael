package core

import (
	"ael/internal/store"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) StorageStats() (any, error) {
	files := map[string]int64{}
	var total int64
	e := filepath.WalkDir(s.Store.Dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		r, _ := filepath.Rel(s.Store.Dir, p)
		files[filepath.ToSlash(r)] = st.Size()
		total += st.Size()
		return nil
	})
	return map[string]any{"data_dir": s.Store.Dir, "files": files, "total_bytes": total, "includes": "db, WAL, SHM, logs, artifacts, migration backups"}, e
}
func (s *Service) Backup(out string) (any, error) {
	out, e := filepath.Abs(out)
	if e != nil {
		return nil, e
	}
	if out == s.Root || strings.HasPrefix(out, s.Root+string(os.PathSeparator)) || out == s.Store.Dir || strings.HasPrefix(out, s.Store.Dir+string(os.PathSeparator)) {
		return nil, Error("BACKUP_OUTSIDE_REQUIRED", "choose a separate export directory")
	}
	if _, e = os.Lstat(out); e == nil {
		return nil, Error("BACKUP_EXISTS", out)
	}
	if e = os.MkdirAll(out, 0700); e != nil {
		return nil, e
	}
	if e = s.Store.Flush(); e != nil {
		return nil, e
	}
	if e = s.Store.Backup(filepath.Join(out, "runtime.db")); e != nil {
		return nil, e
	}
	hashes := map[string]string{}
	e = filepath.WalkDir(s.Store.Dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(s.Store.Dir, p)
		if rel == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return Error("SYMLINK_BACKUP", rel)
		}
		if d.IsDir() {
			return nil
		}
		if rel == "runtime.db" || rel == "runtime.db-wal" || rel == "runtime.db-shm" {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if e = store.Atomic(filepath.Join(out, rel), b, 0600); e != nil {
			return e
		}
		hashes[filepath.ToSlash(rel)] = store.Hash(b)
		return nil
	})
	if e != nil {
		return nil, e
	}
	db, e := os.ReadFile(filepath.Join(out, "runtime.db"))
	if e != nil {
		return nil, e
	}
	hashes["runtime.db"] = store.Hash(db)
	manifest := map[string]any{"schema": 1, "created": time.Now().UTC(), "runtime_source": s.Store.Dir, "files": hashes, "restoration": "requires matching definitions; leases/tokens/PIDs/approvals are not revived"}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	if e = store.Atomic(filepath.Join(out, "manifest.json"), b, 0600); e != nil {
		return nil, e
	}
	return manifest, nil
}
func (s *Service) Restore(from string) (any, error) {
	b, e := os.ReadFile(filepath.Join(from, "manifest.json"))
	if e != nil {
		return nil, e
	}
	var manifest struct {
		Schema int               `json:"schema"`
		Source string            `json:"runtime_source"`
		Files  map[string]string `json:"files"`
	}
	if e = json.Unmarshal(b, &manifest); e != nil {
		return nil, e
	}
	if manifest.Schema != 1 {
		return nil, Error("BACKUP_SCHEMA", "")
	}
	for rel, hash := range manifest.Files {
		p, e := store.SafePath(from, rel)
		if e != nil {
			return nil, e
		}
		b, e := os.ReadFile(p)
		if e != nil || store.Hash(b) != hash {
			return nil, Error("BACKUP_HASH_MISMATCH", rel)
		}
	}
	// Open only the consistent copy, never copy an active WAL database.
	temp, e := os.MkdirTemp("", "ael-restore-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(temp)
	b, e = os.ReadFile(filepath.Join(from, "runtime.db"))
	if e != nil {
		return nil, e
	}
	if e = store.Atomic(filepath.Join(temp, "runtime.db"), b, 0600); e != nil {
		return nil, e
	}
	backup, e := store.Open(temp)
	if e != nil {
		return nil, e
	}
	defer backup.Close()
	rows, e := backup.DB.Query("SELECT body FROM tasks")
	if e != nil {
		return nil, e
	}
	tasks := []Task{}
	for rows.Next() {
		var body string
		if e = rows.Scan(&body); e != nil {
			rows.Close()
			return nil, e
		}
		var t Task
		if e = json.Unmarshal([]byte(body), &t); e != nil {
			rows.Close()
			return nil, e
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	if e = s.restoreFresh(tasks); e != nil {
		return nil, e
	}
	for i := range tasks {
		t := &tasks[i]
		p, e := s.taskPath(t.Definition.ID)
		if e != nil {
			return nil, e
		}
		_, raw, e := readDefinition(p)
		if e != nil || store.Hash(raw) != t.DefinitionHash {
			return nil, Error("RESTORE_DEFINITION_MISMATCH", t.Definition.ID)
		}
		cur, e := s.Get(t.Definition.ID)
		if e == nil && len(cur.Attempts) > 0 {
			return nil, Error("RESTORE_NONEMPTY_RUNTIME", "restore only into fresh runtime; existing attempts preserved")
		}
		for j := range t.Attempts {
			a := &t.Attempts[j]
			a.Token = ""
			a.PID = 0
			a.OwnerPID = 0
			a.OwnerIdentity = ""
			a.Workspace = s.Root
			if a.Status == "running" {
				a.Status = "interrupted"
			}
			a.Lease = time.Time{}
		}
		t.CandidateWorkspace = s.Root
		t.Approvals = nil
		if t.Status == "open" {
			t.Paused = true
			t.Plan.Approved = false
			t.Confirmation = &Confirmation{"restore", "runtime restored; reconcile checkpoints, candidate and any delivery run before resuming", t.Definition.Revision, t.Plan.Revision, ""}
		}
	}
	for rel := range manifest.Files {
		if rel == "runtime.db" {
			continue
		}
		p, e := store.SafePath(s.Store.Dir, rel)
		if e != nil {
			return nil, e
		}
		b, e := os.ReadFile(filepath.Join(from, rel))
		if e != nil {
			return nil, e
		}
		if old, e := os.ReadFile(p); e == nil && store.Hash(old) != store.Hash(b) {
			return nil, Error("RESTORE_ARTIFACT_CONFLICT", rel)
		}
		if e = store.Atomic(p, b, 0600); e != nil {
			return nil, e
		}
	}
	type event struct {
		ID                        int
		Task, Kind, Body, Created string
	}
	audit := []event{}
	erows, err := backup.DB.Query("SELECT id,task_id,kind,body,created FROM events ORDER BY id")
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var ev event
		if err = erows.Scan(&ev.ID, &ev.Task, &ev.Kind, &ev.Body, &ev.Created); err != nil {
			erows.Close()
			return nil, err
		}
		audit = append(audit, ev)
	}
	erows.Close()
	metadata := map[string]string{}
	mrows, err := backup.DB.Query("SELECT key,body FROM metadata WHERE key LIKE 'migration:%'")
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var key, body string
		if err = mrows.Scan(&key, &body); err != nil {
			mrows.Close()
			return nil, err
		}
		var run map[string]any
		if json.Unmarshal([]byte(body), &run) == nil {
			run["state"] = "restored_review_required"
			if changes, ok := run["changes"].([]any); ok {
				for _, item := range changes {
					if c, ok := item.(map[string]any); ok {
						if path, ok := c["backup"].(string); ok && manifest.Source != "" && strings.HasPrefix(path, manifest.Source+string(os.PathSeparator)) {
							c["backup"] = filepath.Join(s.Store.Dir, strings.TrimPrefix(path, manifest.Source+string(os.PathSeparator)))
						}
					}
				}
			}
			raw, _ := json.Marshal(run)
			metadata[key] = string(raw)
		}
	}
	mrows.Close()
	e = s.Store.Write(func(tx *store.Tx) error {
		for key, body := range metadata {
			if _, e := tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", key, body); e != nil {
				return e
			}
		}
		var existing int
		if e := tx.Row("SELECT (SELECT count(*) FROM tasks WHERE json_array_length(json_extract(body,'$.attempts'))>0 OR json_array_length(json_extract(body,'$.approvals'))>0)+(SELECT count(*) FROM events WHERE kind NOT IN ('definition_recovered','record_recovered'))").Scan(&existing); e != nil {
			return e
		}
		if existing > 0 {
			return Error("RESTORE_NONEMPTY_RUNTIME", "existing attempts preserved")
		}
		if _, e := tx.Exec("DELETE FROM events"); e != nil {
			return e
		}
		for _, ev := range audit {
			if _, e := tx.Exec("INSERT INTO events(id,task_id,kind,body,created) VALUES(?,?,?,?,?)", ev.ID, ev.Task, ev.Kind, ev.Body, ev.Created); e != nil {
				return e
			}
		}
		for _, t := range tasks {
			if e := s.save(tx, &t); e != nil {
				return e
			}
		}
		return tx.Event("", "runtime_restored", map[string]any{"tasks": len(tasks), "lease_policy": "not revived"})
	})
	return map[string]any{"restored_tasks": len(tasks), "leases_revived": false, "approvals_revived": false}, e
}
func (s *Service) Prune(before time.Time, apply bool) (any, error) {
	all, e := s.List()
	if e != nil {
		return nil, e
	}
	terminal := map[string]bool{}
	for _, n := range all {
		t := n.Task
		if (t.Status == "completed" || t.Status == "cancelled") && t.Confirmation == nil {
			for _, a := range t.Attempts {
				terminal[a.ID] = true
			}
		}
	}
	paths := []string{}
	base := filepath.Join(s.Store.Dir, "artifacts")
	e = filepath.WalkDir(base, func(p string, d fs.DirEntry, e error) error {
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		attempt := strings.Split(filepath.ToSlash(rel), "/")[0]
		attempt = strings.TrimSuffix(attempt, ".log")
		st, e := d.Info()
		if e != nil {
			return e
		}
		if terminal[attempt] && st.ModTime().Before(before) {
			paths = append(paths, p)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if apply {
		for _, p := range paths {
			if e = os.Remove(p); e != nil {
				return nil, e
			}
		}
		e = s.Store.Write(func(tx *store.Tx) error {
			return tx.Event("", "artifacts_pruned", map[string]any{"paths": paths, "policy": "terminal only; hashes and summaries retained"})
		})
	}
	return map[string]any{"dry_run": !apply, "files": paths, "count": len(paths), "summary": "evidence hashes retained; missing originals are reported by artifact API"}, e
}

func (s *Service) restoreFresh(incoming []Task) error {
	known := map[string]bool{}
	for _, t := range incoming {
		known[t.Definition.ID] = true
	}
	var events int
	if e := s.Store.DB.QueryRow("SELECT count(*) FROM events WHERE kind NOT IN ('definition_recovered','record_recovered')").Scan(&events); e != nil {
		return e
	}
	if events > 0 {
		return Error("RESTORE_NONEMPTY_RUNTIME", "only untouched Git-recovered definitions may receive a runtime restore")
	}
	rows, e := s.Store.DB.Query("SELECT id FROM tasks")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return e
		}
		if !known[id] {
			return Error("RESTORE_EXTRA_TASK", "backup does not contain "+id)
		}
	}
	return rows.Err()
}

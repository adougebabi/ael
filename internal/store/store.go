// Package store owns the only production runtime backend: local SQLite WAL.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB  *sql.DB
	Dir string
}
type Tx struct{ Conn *sql.Conn }

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.Join(dir, "runtime.db")}).String()+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db, dir}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY, body TEXT NOT NULL, status TEXT NOT NULL, step TEXT NOT NULL, updated TEXT NOT NULL, completed TEXT NOT NULL DEFAULT '');
 CREATE INDEX IF NOT EXISTS task_status ON tasks(status,updated);
 CREATE INDEX IF NOT EXISTS task_completed ON tasks(completed);
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY,task_id TEXT NOT NULL,kind TEXT NOT NULL,body TEXT NOT NULL,created TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS event_task ON events(task_id,id);
 CREATE TABLE IF NOT EXISTS submissions(key TEXT PRIMARY KEY,hash TEXT NOT NULL,response TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS workspace_locks(workspace TEXT PRIMARY KEY,attempt TEXT NOT NULL,task TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS outbox(path TEXT PRIMARY KEY,old_hash TEXT NOT NULL,body BLOB NOT NULL,state TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,body TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Write(fn func(*Tx) error) (err error) {
	c, err := s.DB.Conn(context.Background())
	if err != nil {
		return
	}
	defer c.Close()
	if _, err = c.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return
	}
	defer func() {
		if err != nil {
			c.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err = fn(&Tx{c}); err != nil {
		return
	}
	_, err = c.ExecContext(context.Background(), "COMMIT")
	return
}
func (t *Tx) Exec(q string, args ...any) (sql.Result, error) {
	return t.Conn.ExecContext(context.Background(), q, args...)
}
func (t *Tx) Row(q string, args ...any) *sql.Row {
	return t.Conn.QueryRowContext(context.Background(), q, args...)
}
func (t *Tx) Event(id, kind string, body any) error {
	b, _ := json.Marshal(body)
	_, e := t.Exec("INSERT INTO events(task_id,kind,body,created) VALUES(?,?,?,?)", id, kind, string(b), time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (t *Tx) File(path string, body []byte) error {
	old, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	hash := ""
	if e == nil {
		hash = Hash(old)
	}
	hash, e = t.intentBaseline(path, hash)
	if e != nil {
		return e
	}
	_, e = t.Exec("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,'pending') ON CONFLICT(path) DO UPDATE SET old_hash=excluded.old_hash,body=excluded.body,state='pending'", path, hash, body)
	return e
}

// intentBaseline recognizes an intent already applied before a crash or failed
// DB commit. Other unexpected disk changes retain the old conflict guard.
func (t *Tx) intentBaseline(path, current string) (string, error) {
	var old, state string
	var body []byte
	err := t.Row("SELECT old_hash,body,state FROM outbox WHERE path=?", path).Scan(&old, &body, &state)
	if err == sql.ErrNoRows {
		return current, nil
	}
	if err != nil {
		return "", err
	}
	if state == "done" || state == "pending" && current != "" && current == Hash(body) || state == "delete_pending" && current == "" {
		return current, nil
	}
	return old, nil
}

// Flush serializes each intent's current version and filesystem update using a
// SQLite write lock, including across processes. Earlier successful intents
// remain committed if a later file has a genuine conflict.
func (s *Store) Flush() error {
	rows, e := s.DB.Query("SELECT path FROM outbox WHERE state IN ('pending','delete_pending') ORDER BY CASE WHEN path LIKE '%/.ael/project.yaml' THEN 3 WHEN path LIKE '%/AGENTS.md' THEN 2 WHEN state='delete_pending' THEN 1 ELSE 0 END,rowid")
	if e != nil {
		return e
	}
	var paths []string
	for rows.Next() {
		var path string
		if e = rows.Scan(&path); e != nil {
			rows.Close()
			return e
		}
		paths = append(paths, path)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, path := range paths {
		if e = s.Write(func(tx *Tx) error { return s.flushOne(tx, path) }); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) flushOne(tx *Tx, path string) error {
	var old, state string
	var body []byte
	e := tx.Row("SELECT old_hash,body,state FROM outbox WHERE path=? AND state IN ('pending','delete_pending')", path).Scan(&old, &body, &state)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	b, err := os.ReadFile(path)
	current := ""
	if err == nil {
		current = Hash(b)
	} else if !os.IsNotExist(err) {
		return err
	}
	if state == "delete_pending" {
		if current != "" {
			if current != old {
				return fmt.Errorf("FILE_CONFLICT: %s; delete intent retained", path)
			}
			if e = os.Remove(path); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("UPDATE outbox SET state='done' WHERE path=? AND state='delete_pending'", path); e != nil {
			return e
		}
		return nil
	}
	if current != Hash(body) && current != old {
		return fmt.Errorf("FILE_CONFLICT: %s; pending intent retained", path)
	}
	if current != Hash(body) {
		mode := os.FileMode(0644)
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
		if e = Atomic(path, body, mode); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE outbox SET state='done' WHERE path=? AND body=?", path, body); e != nil {
		return e
	}
	return nil
}
func Atomic(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".ael-stage-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(n, path)
}

// SafePath rejects traversal and all symlink components, including an existing final path.
func SafePath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || rel == "" {
		return "", errors.New("UNSAFE_PATH")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("UNSAFE_PATH")
	}
	p := root
	for _, part := range strings.Split(clean, string(os.PathSeparator)) {
		p = filepath.Join(p, part)
		st, e := os.Lstat(p)
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("SYMLINK_PATH")
		}
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
	}
	return p, nil
}
func (s *Store) Backup(path string) error {
	_, e := s.DB.Exec("VACUUM INTO '" + strings.ReplaceAll(path, "'", "''") + "'")
	return e
}

func HashFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func CopyAtomic(from, to string, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(to), 0700); e != nil {
		return e
	}
	in, e := os.Open(from)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.CreateTemp(filepath.Dir(to), ".ael-copy-*")
	if e != nil {
		return e
	}
	defer os.Remove(out.Name())
	if e = out.Chmod(mode); e == nil {
		_, e = io.Copy(out, in)
	}
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(out.Name(), to)
}

// DeleteFile records a recoverable deletion of an owned file, never a directory tree.
func (t *Tx) DeleteFile(path string) error {
	old, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		// A queued create must not recreate a file after a later delete.
		_, e = t.Exec("UPDATE outbox SET body=?,state='done' WHERE path=? AND state IN ('pending','delete_pending')", []byte{}, path)
		return e
	}
	if e != nil {
		return e
	}
	hash, e := t.intentBaseline(path, Hash(old))
	if e != nil {
		return e
	}
	_, e = t.Exec("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,'delete_pending') ON CONFLICT(path) DO UPDATE SET old_hash=excluded.old_hash,body=excluded.body,state='delete_pending'", path, hash, []byte{})
	return e
}

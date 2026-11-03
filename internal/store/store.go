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
	_, e = t.Exec("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,'pending') ON CONFLICT(path) DO UPDATE SET old_hash=CASE WHEN outbox.state='done' THEN excluded.old_hash ELSE outbox.old_hash END,body=excluded.body,state='pending'", path, hash, body)
	return e
}

// Flush uses compare-and-swap and atomic rename. Conflicting user edits are retained.
func (s *Store) Flush() error {
	rows, e := s.DB.Query("SELECT path,old_hash,body FROM outbox WHERE state='pending'")
	if e != nil {
		return e
	}
	type intent struct {
		path, old string
		b         []byte
	}
	var all []intent
	for rows.Next() {
		var i intent
		if e = rows.Scan(&i.path, &i.old, &i.b); e != nil {
			rows.Close()
			return e
		}
		all = append(all, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, i := range all {
		b, err := os.ReadFile(i.path)
		current := ""
		if err == nil {
			current = Hash(b)
		} else if !os.IsNotExist(err) {
			return err
		}
		if current != Hash(i.b) && current != i.old {
			return fmt.Errorf("FILE_CONFLICT: %s; pending intent retained", i.path)
		}
		if current != Hash(i.b) {
			if e = Atomic(i.path, i.b, 0644); e != nil {
				return e
			}
		}
		if _, e = s.DB.Exec("UPDATE outbox SET state='done' WHERE path=? AND body=?", i.path, i.b); e != nil {
			return e
		}
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

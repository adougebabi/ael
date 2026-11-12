package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const standalonePasswordRounds = 600000

var standaloneID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type Standalone struct {
	store           *store.Store
	projectMu       sync.Mutex
	projectServices map[string]*core.Service
	dashboardEpoch  string
}

type standalonePrincipal struct {
	UserID    string
	Username  string
	Admin     bool
	ProjectID string
	TokenID   string
	CSRFHash  []byte
}

type standaloneProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type standaloneTokenInfo struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	ProjectID string `json:"project_id"`
	Label     string `json:"label"`
	Revoked   bool   `json:"revoked"`
}

// OpenStandalone uses a dedicated persistent directory and never opens a
// checkout. Refusing unmarked nonempty directories protects existing data.
func OpenStandalone(dir string) (*Standalone, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("SERVER_DATA_DIR_REQUIRED: use an absolute dedicated data directory")
	}
	ancestor := filepath.Clean(dir)
	for {
		if _, err := os.Stat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return nil, errors.New("SERVER_DATA_DIR_UNAVAILABLE")
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil, errors.New("SERVER_DATA_DIR_UNAVAILABLE")
		}
		ancestor = parent
	}
	if err := standaloneLocalFilesystem(ancestor); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := standaloneLocalFilesystem(dir); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("SERVER_DATA_DIR_PERMISSIONS: directory must not be writable by other users")
	}
	if err = standaloneNoACL(dir); err != nil {
		return nil, err
	}
	marker := filepath.Join(dir, "ael-server.marker")
	if existing, statErr := os.Lstat(marker); statErr == nil && !existing.Mode().IsRegular() {
		return nil, errors.New("SERVER_DATA_DIR_INVALID: invalid server marker")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	raw, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil || len(entries) != 0 {
			return nil, errors.New("SERVER_DATA_DIR_NOT_EMPTY: choose an empty dedicated directory")
		}
		f, createErr := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			return nil, createErr
		}
		_, writeErr := f.WriteString("AEL-STANDALONE-1\n")
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if err != nil || string(raw) != "AEL-STANDALONE-1\n" {
		return nil, errors.New("SERVER_DATA_DIR_INVALID: invalid server marker")
	}
	if err = os.MkdirAll(filepath.Join(dir, "attachments"), 0700); err != nil {
		return nil, err
	}
	attachmentInfo, err := os.Lstat(filepath.Join(dir, "attachments"))
	if err != nil || !attachmentInfo.IsDir() || attachmentInfo.Mode().Perm()&0077 != 0 {
		return nil, errors.New("SERVER_ATTACHMENT_DIR_INVALID")
	}
	if err = standaloneNoACL(filepath.Join(dir, "attachments")); err != nil {
		return nil, err
	}
	// SQLite and its WAL inherit the private database file permissions.
	databasePath := filepath.Join(dir, "runtime.db")
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err = standaloneSafeDatabaseFile(databasePath + suffix); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(databasePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = standaloneSafeDatabaseFile(databasePath); err != nil {
		return nil, err
	}
	db, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	_, err = db.DB.Exec(`
CREATE TABLE IF NOT EXISTS server_users(id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, salt BLOB NOT NULL, password_hash BLOB NOT NULL, rounds INTEGER NOT NULL, admin INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS server_projects(id TEXT PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS server_members(project_id TEXT NOT NULL REFERENCES server_projects(id), user_id TEXT NOT NULL REFERENCES server_users(id), role TEXT NOT NULL, PRIMARY KEY(project_id,user_id));
CREATE TABLE IF NOT EXISTS server_sessions(secret_hash BLOB PRIMARY KEY, user_id TEXT NOT NULL REFERENCES server_users(id), csrf_hash BLOB NOT NULL, expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS server_tokens(id TEXT PRIMARY KEY, secret_hash BLOB NOT NULL UNIQUE, user_id TEXT NOT NULL REFERENCES server_users(id), project_id TEXT NOT NULL REFERENCES server_projects(id), label TEXT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Standalone{store: db, projectServices: map[string]*core.Service{}, dashboardEpoch: core.ID("server-")}, nil
}

func (s *Standalone) Close() error {
	s.projectMu.Lock()
	defer s.projectMu.Unlock()
	var first error
	for _, project := range s.projectServices {
		if err := project.Store.Close(); err != nil && first == nil {
			first = err
		}
	}
	if err := s.store.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

func (s *Standalone) Bootstrapped() (bool, error) {
	var count int
	err := s.store.DB.QueryRow("SELECT count(*) FROM server_users WHERE admin=1").Scan(&count)
	return count > 0, err
}

func standaloneRandom(prefix string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func standaloneHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func standalonePassword(password string, salt []byte, rounds int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, rounds, 32)
}

func standaloneUserInput(username, password string) (string, []byte, []byte, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !standaloneID.MatchString(username) || len(password) < 12 || len(password) > 1024 {
		return "", nil, nil, errors.New("INVALID_USER: username or password does not meet requirements")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", nil, nil, err
	}
	hash, err := standalonePassword(password, salt, standalonePasswordRounds)
	return username, salt, hash, err
}

func (s *Standalone) BootstrapAdmin(username, password string) error {
	username, salt, hash, err := standaloneUserInput(username, password)
	if err != nil {
		return err
	}
	id, err := standaloneRandom("user_")
	if err != nil {
		return err
	}
	return s.store.Write(func(tx *store.Tx) error {
		var count int
		if err := tx.Row("SELECT count(*) FROM server_users").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("SERVER_ALREADY_BOOTSTRAPPED")
		}
		_, err := tx.Exec("INSERT INTO server_users(id,username,salt,password_hash,rounds,admin) VALUES(?,?,?,?,?,1)", id, username, salt, hash, standalonePasswordRounds)
		return err
	})
}

func (s *Standalone) createUser(username, password string) error {
	username, salt, hash, err := standaloneUserInput(username, password)
	if err != nil {
		return err
	}
	id, err := standaloneRandom("user_")
	if err != nil {
		return err
	}
	_, err = s.store.DB.Exec("INSERT INTO server_users(id,username,salt,password_hash,rounds,admin) VALUES(?,?,?,?,?,0)", id, username, salt, hash, standalonePasswordRounds)
	return err
}

func (s *Standalone) createProject(id, name string) error {
	name = strings.TrimSpace(name)
	if !standaloneID.MatchString(id) || name == "" || len(name) > 200 {
		return errors.New("INVALID_PROJECT")
	}
	_, err := s.store.DB.Exec("INSERT INTO server_projects(id,name) VALUES(?,?)", id, name)
	return err
}

func (s *Standalone) addMember(project, username, role string) error {
	if role != "executor" && role != "reviewer" && role != "reader" {
		return errors.New("INVALID_ROLE")
	}
	username = strings.ToLower(strings.TrimSpace(username))
	result, err := s.store.DB.Exec(`INSERT INTO server_members(project_id,user_id,role)
SELECT p.id,u.id,? FROM server_projects p,server_users u WHERE p.id=? AND u.username=?
ON CONFLICT(project_id,user_id) DO UPDATE SET role=excluded.role`, role, project, username)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.New("MEMBERSHIP_TARGET_MISSING")
	}
	return nil
}

func (s *Standalone) issueToken(username, project, label string) (string, string, error) {
	username, label = strings.ToLower(strings.TrimSpace(username)), strings.TrimSpace(label)
	if label == "" || len(label) > 100 {
		return "", "", errors.New("INVALID_TOKEN_LABEL")
	}
	id, err := standaloneRandom("token_")
	if err != nil {
		return "", "", err
	}
	secret, err := standaloneRandom("ael_")
	if err != nil {
		return "", "", err
	}
	result, err := s.store.DB.Exec(`INSERT INTO server_tokens(id,secret_hash,user_id,project_id,label)
SELECT ?,?,u.id,m.project_id,? FROM server_users u JOIN server_members m ON m.user_id=u.id WHERE u.username=? AND m.project_id=?`, id, standaloneHash(secret), label, username, project)
	if err != nil {
		return "", "", err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return "", "", errors.New("MEMBERSHIP_TARGET_MISSING")
	}
	return id, secret, nil
}

func (s *Standalone) revokeToken(id string) error {
	result, err := s.store.DB.Exec("UPDATE server_tokens SET revoked=1 WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.New("TOKEN_NOT_FOUND")
	}
	return nil
}

func (s *Standalone) tokens() ([]standaloneTokenInfo, error) {
	rows, err := s.store.DB.Query(`SELECT t.id,u.username,t.project_id,t.label,t.revoked FROM server_tokens t JOIN server_users u ON u.id=t.user_id ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []standaloneTokenInfo{}
	for rows.Next() {
		var item standaloneTokenInfo
		var revoked int
		if err = rows.Scan(&item.ID, &item.Username, &item.ProjectID, &item.Label, &revoked); err != nil {
			return nil, err
		}
		item.Revoked = revoked != 0
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Standalone) login(username, password string) (string, string, error) {
	var id string
	var salt, expected []byte
	var rounds int
	err := s.store.DB.QueryRow("SELECT id,salt,password_hash,rounds FROM server_users WHERE username=?", strings.ToLower(strings.TrimSpace(username))).Scan(&id, &salt, &expected, &rounds)
	if err != nil {
		salt, expected, rounds = make([]byte, 16), make([]byte, 32), standalonePasswordRounds
	}
	if len(password) > 1024 {
		password = ""
	}
	actual, hashErr := standalonePassword(password, salt, rounds)
	if err != nil || hashErr != nil || subtle.ConstantTimeCompare(actual, expected) != 1 {
		return "", "", errors.New("INVALID_CREDENTIALS")
	}
	session, err := standaloneRandom("session_")
	if err != nil {
		return "", "", err
	}
	csrf, err := standaloneRandom("csrf_")
	if err != nil {
		return "", "", err
	}
	_, err = s.store.DB.Exec("INSERT INTO server_sessions(secret_hash,user_id,csrf_hash,expires) VALUES(?,?,?,?)", standaloneHash(session), id, standaloneHash(csrf), time.Now().Add(12*time.Hour).Unix())
	return session, csrf, err
}

func (s *Standalone) session(secret string) (standalonePrincipal, error) {
	var p standalonePrincipal
	var admin int
	err := s.store.DB.QueryRow(`SELECT u.id,u.username,u.admin,s.csrf_hash FROM server_sessions s JOIN server_users u ON u.id=s.user_id WHERE s.secret_hash=? AND s.expires>?`, standaloneHash(secret), time.Now().Unix()).Scan(&p.UserID, &p.Username, &admin, &p.CSRFHash)
	p.Admin = admin == 1
	return p, err
}

func (s *Standalone) token(secret string) (standalonePrincipal, error) {
	var p standalonePrincipal
	err := s.store.DB.QueryRow(`SELECT u.id,u.username,t.project_id,t.id FROM server_tokens t JOIN server_users u ON u.id=t.user_id JOIN server_members m ON m.user_id=u.id AND m.project_id=t.project_id WHERE t.secret_hash=? AND t.revoked=0`, standaloneHash(secret)).Scan(&p.UserID, &p.Username, &p.ProjectID, &p.TokenID)
	return p, err
}

func (s *Standalone) role(p standalonePrincipal, project string) (string, error) {
	if p.ProjectID != "" && p.ProjectID != project {
		return "", sql.ErrNoRows
	}
	if p.Admin {
		var exists string
		if err := s.store.DB.QueryRow("SELECT id FROM server_projects WHERE id=?", project).Scan(&exists); err != nil {
			return "", err
		}
		return "admin", nil
	}
	var role string
	err := s.store.DB.QueryRow("SELECT role FROM server_members WHERE project_id=? AND user_id=?", project, p.UserID).Scan(&role)
	return role, err
}

func (s *Standalone) projects(p standalonePrincipal) ([]standaloneProject, error) {
	query, args := `SELECT id,name,'admin' FROM server_projects ORDER BY id`, []any{}
	if !p.Admin {
		query = `SELECT p.id,p.name,m.role FROM server_projects p JOIN server_members m ON m.project_id=p.id WHERE m.user_id=?`
		args = append(args, p.UserID)
		if p.ProjectID != "" {
			query += " AND p.id=?"
			args = append(args, p.ProjectID)
		}
		query += " ORDER BY p.id"
	}
	rows, err := s.store.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []standaloneProject{}
	for rows.Next() {
		var p standaloneProject
		if err = rows.Scan(&p.ID, &p.Name, &p.Role); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *Standalone) project(p standalonePrincipal, id string) (standaloneProject, error) {
	role, err := s.role(p, id)
	if err != nil {
		return standaloneProject{}, err
	}
	result := standaloneProject{ID: id, Role: role}
	err = s.store.DB.QueryRow("SELECT name FROM server_projects WHERE id=?", id).Scan(&result.Name)
	return result, err
}

func (s *Standalone) logout(secret string) error {
	_, err := s.store.DB.Exec("DELETE FROM server_sessions WHERE secret_hash=?", standaloneHash(secret))
	return err
}

func standalonePublicError(code string) error { return fmt.Errorf("%s", code) }

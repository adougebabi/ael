package server

import (
	"ael/internal/bootstrap"
	"ael/internal/securityfs"
	"ael/internal/store"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const serverBackupSchema = 1

type ServerBackupFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ServerBackupManifest struct {
	Schema      int                `json:"schema"`
	DataFormat  int                `json:"data_format"`
	SourceRoot  string             `json:"source_root"`
	Version     string             `json:"version"`
	Build       string             `json:"build"`
	Created     time.Time          `json:"created"`
	Maintenance MaintenanceState   `json:"maintenance"`
	Files       []ServerBackupFile `json:"files"`
}

func readServerJSON(root, relative string, result any) ([]byte, error) {
	path, err := store.SafePath(root, relative)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 16<<20 {
		return nil, errors.New("SERVER_MANIFEST_TOO_LARGE")
	}
	if err = json.Unmarshal(raw, result); err != nil {
		return nil, errors.New("SERVER_MANIFEST_INVALID")
	}
	return raw, nil
}

func serverTransientFile(relative string) bool {
	name := filepath.Base(relative)
	return name == "authorization.lock" || name == "server-restore.json" || name == "server-migration.json" ||
		strings.HasSuffix(name, "runtime.db-wal") || strings.HasSuffix(name, "runtime.db-shm") || strings.HasSuffix(name, "runtime.db-journal")
}

func serverInventory(root string) ([]ServerBackupFile, error) {
	files := []ServerBackupFile{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("SERVER_BACKUP_UNSAFE_FILE")
		}
		if entry.IsDir() {
			return nil
		}
		if serverTransientFile(relative) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("SERVER_BACKUP_UNSAFE_FILE")
		}
		if err = securityfs.SingleLinkRegular(path); err != nil {
			return err
		}
		if err = standaloneNoACL(path); err != nil {
			return err
		}
		if _, err = store.SafePath(root, relative); err != nil {
			return err
		}
		hash, err := store.HashFile(path)
		if err != nil {
			return err
		}
		files = append(files, ServerBackupFile{Path: filepath.ToSlash(relative), Size: info.Size(), SHA256: hash})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func serverSnapshotFileSafe(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || securityfs.SingleLinkRegular(path) != nil || standaloneNoACL(path) != nil {
		return errors.New("SERVER_BACKUP_UNSAFE_FILE")
	}
	return nil
}

func verifyServerFiles(root string, files []ServerBackupFile) error {
	seen := map[string]bool{}
	for _, file := range files {
		if seen[file.Path] || file.Size < 0 || len(file.SHA256) != 64 || serverTransientFile(file.Path) {
			return errors.New("SERVER_MANIFEST_INVALID")
		}
		seen[file.Path] = true
		path, err := store.SafePath(root, filepath.FromSlash(file.Path))
		if err != nil {
			return err
		}
		if err = serverSnapshotFileSafe(path); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return errors.New("SERVER_BACKUP_CHECKSUM_MISMATCH")
		}
		hash, err := store.HashFile(path)
		if err != nil || hash != file.SHA256 {
			return errors.New("SERVER_BACKUP_CHECKSUM_MISMATCH")
		}
	}
	if !seen["ael-server.marker"] || !seen["runtime.db"] {
		return errors.New("SERVER_MANIFEST_INVALID")
	}
	return nil
}

func outsideServerRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && (relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

// BackupStandalone captures every central table and its paired managed files at
// the same authorization fence. Local selective task restore is a separate API.
func BackupStandalone(dir, out string, legacyStopped bool) (ServerBackupManifest, error) {
	var manifest ServerBackupManifest
	dir, err := canonicalServerPath(dir)
	if err != nil {
		return manifest, err
	}
	service, err := openStandalone(dir, legacyStopped)
	if err != nil {
		return manifest, err
	}
	defer service.Close()
	unlock, err := service.lockAuthorization(true)
	if err != nil {
		return manifest, err
	}
	defer unlock()
	state, err := service.Maintenance()
	if err != nil {
		return manifest, err
	}
	format, err := serverDataFormat(dir)
	if err != nil {
		return manifest, err
	}
	if state.Mode != "quiesced" && !(format == 1 && legacyStopped) {
		return manifest, errors.New("SERVER_MAINTENANCE_REQUIRED")
	}
	target, err := canonicalServerPath(out)
	if err != nil || !outsideServerRoot(service.store.Dir, target) {
		return manifest, errors.New("SERVER_BACKUP_TARGET_INVALID")
	}
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return manifest, errors.New("SERVER_BACKUP_TARGET_EXISTS")
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return manifest, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".ael-server-backup-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(stage)
	databases := map[string]*store.Store{"runtime.db": service.store}
	projects, err := service.projects(standalonePrincipal{Admin: true})
	if err != nil {
		return manifest, err
	}
	for _, project := range projects {
		relative := filepath.Join("projects", store.Hash([]byte(project.ID))[:32], "runtime", "runtime.db")
		path, err := store.SafePath(dir, relative)
		if err != nil {
			return manifest, err
		}
		if _, err = os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return manifest, err
		}
		projectService, err := service.projectService(project.ID)
		if err != nil {
			return manifest, err
		}
		databases[filepath.ToSlash(relative)] = projectService.Store
	}
	files, err := serverInventory(dir)
	if err != nil {
		return manifest, err
	}
	for _, file := range files {
		destination, err := store.SafePath(stage, filepath.FromSlash(file.Path))
		if err != nil {
			return manifest, err
		}
		if database := databases[file.Path]; database != nil {
			if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
				return manifest, err
			}
			if err = database.Backup(destination); err != nil {
				return manifest, err
			}
			if err = os.Chmod(destination, 0600); err != nil {
				return manifest, err
			}
			continue
		}
		if filepath.Base(file.Path) == "runtime.db" {
			return manifest, errors.New("SERVER_BACKUP_LAYOUT_UNSUPPORTED")
		}
		if err = store.CopyAtomic(filepath.Join(dir, filepath.FromSlash(file.Path)), destination, 0600); err != nil {
			return manifest, err
		}
	}
	copied, err := serverInventory(stage)
	if err != nil {
		return manifest, err
	}
	manifest = ServerBackupManifest{Schema: serverBackupSchema, DataFormat: format, SourceRoot: service.store.Dir, Version: bootstrap.Version, Build: bootstrap.Build, Created: time.Now().UTC(), Maintenance: state, Files: copied}
	body, _ := json.MarshalIndent(manifest, "", "  ")
	if err = store.Atomic(filepath.Join(stage, "manifest.json"), append(body, '\n'), 0600); err != nil {
		return manifest, err
	}
	if err = os.Rename(stage, target); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func VerifyServerBackup(root string) (ServerBackupManifest, string, error) {
	var manifest ServerBackupManifest
	raw, err := readServerJSON(root, "manifest.json", &manifest)
	if err != nil {
		return manifest, "", err
	}
	if manifest.Schema != serverBackupSchema || manifest.DataFormat < 1 || manifest.DataFormat > serverDataFormatVersion || !filepath.IsAbs(manifest.SourceRoot) || len(manifest.Files) == 0 {
		return manifest, "", errors.New("SERVER_BACKUP_FORMAT_UNSUPPORTED")
	}
	if err = verifyServerFiles(root, manifest.Files); err != nil {
		return manifest, "", err
	}
	return manifest, store.Hash(raw), nil
}

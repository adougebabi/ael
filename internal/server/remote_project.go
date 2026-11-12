package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/store"
	"errors"
	"os"
	"path/filepath"
)

// projectService uses only server-owned paths derived from the registered ID.
// It deliberately does not call bootstrap.Open or import checkout records.
func (s *Standalone) projectService(id string) (*core.Service, error) {
	s.projectMu.Lock()
	defer s.projectMu.Unlock()
	if service := s.projectServices[id]; service != nil {
		return service, nil
	}
	if !standaloneID.MatchString(id) {
		return nil, errors.New("PROJECT_NOT_FOUND")
	}
	rel := filepath.Join("projects", store.Hash([]byte(id))[:32])
	projectDir, err := store.SafePath(s.store.Dir, rel)
	if err != nil {
		return nil, err
	}
	workspace := filepath.Join(projectDir, "workspace")
	runtime := filepath.Join(projectDir, "runtime")
	for _, dir := range []string{projectDir, workspace, runtime, filepath.Join(workspace, ".ael")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("SERVER_PROJECT_DIR_INVALID")
		}
		if err = standaloneNoACL(dir); err != nil {
			return nil, err
		}
		if err = standaloneLocalFilesystem(dir); err != nil {
			return nil, err
		}
	}
	policy, err := store.SafePath(workspace, ".ael/policy.yaml")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(policy, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, writeErr := f.WriteString(bootstrap.DefaultPolicy)
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if !os.IsExist(err) {
		return nil, err
	} else if data, readErr := os.ReadFile(policy); readErr != nil || string(data) != bootstrap.DefaultPolicy {
		return nil, errors.New("SERVER_POLICY_CHANGED")
	}
	database := filepath.Join(runtime, "runtime.db")
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err = standaloneSafeDatabaseFile(database + suffix); err != nil {
			return nil, err
		}
	}
	f, err = os.OpenFile(database, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = standaloneSafeDatabaseFile(database); err != nil {
		return nil, err
	}
	db, err := store.Open(runtime)
	if err != nil {
		return nil, err
	}
	// Pending projections are reconciled by task/operation endpoints. Opening
	// must remain possible when a conflict needs inspection and recovery.
	service := &core.Service{Store: db, Root: workspace, Active: true, RemoteAuthority: true, MatchRemoteCandidate: matchRemoteCandidateTx}
	s.projectServices[id] = service
	return service, nil
}

package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/securityfs"
	"ael/internal/store"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if err = initializeStandalonePolicy(policy); err != nil {
		return nil, err
	}
	database := filepath.Join(runtime, "runtime.db")
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err = standaloneSafeDatabaseFile(database + suffix); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(database, os.O_CREATE|os.O_RDWR, 0600)
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

// Publish the complete default policy with exclusive creation. A stopped
// initializer leaves a private staging file or a complete policy, never an
// empty final file. Existing policy changes remain a fail-closed condition.
func initializeStandalonePolicy(path string) error {
	body, err := os.ReadFile(path)
	if err == nil {
		if string(body) != bootstrap.DefaultPolicy {
			return errors.New("SERVER_POLICY_CHANGED")
		}
		return cleanupStandalonePolicyAliases(path)
	}
	if !os.IsNotExist(err) {
		return err
	}
	stage := filepath.Join(filepath.Dir(path), ".policy-bootstrap-"+core.ID(""))
	if err = store.Atomic(stage, []byte(bootstrap.DefaultPolicy), 0600); err != nil {
		return err
	}
	defer os.Remove(stage)
	if err = os.Link(stage, path); err != nil && !os.IsExist(err) {
		return err
	}
	body, err = os.ReadFile(path)
	if err != nil || string(body) != bootstrap.DefaultPolicy {
		return errors.New("SERVER_POLICY_CHANGED")
	}
	if err = os.Remove(stage); err != nil && !os.IsNotExist(err) {
		return err
	}
	return artifactSyncDirs(filepath.Dir(path))
}

func cleanupStandalonePolicyAliases(path string) error {
	policy, err := os.Lstat(path)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	changed := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".policy-bootstrap-") {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !os.SameFile(policy, info) {
			continue
		}
		if err = os.Remove(filepath.Join(filepath.Dir(path), entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
		changed = true
	}
	if err = securityfs.SingleLinkRegular(path); err != nil {
		return err
	}
	if changed {
		return artifactSyncDirs(filepath.Dir(path))
	}
	return nil
}

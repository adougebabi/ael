package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func runnerLockRuntime(cfg remoteRunnerConfig) (func(), error) {
	path, err := filepath.EvalSymlinks(cfg.RuntimeDir)
	if err != nil || !filepath.IsAbs(cfg.RuntimeDir) {
		return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "runner runtime is unavailable")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "runner lock is unavailable")
	}
	file := os.NewFile(uintptr(fd), path)
	info, statErr := file.Stat()
	link, linkErr := os.Lstat(path)
	if statErr != nil || linkErr != nil || !info.IsDir() || !os.SameFile(info, link) || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(path) != nil {
		_ = file.Close()
		return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "runner runtime is not a private directory")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, core.Error("RUNNER_WIP", "another local Runner owns this runtime")
	}
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
	}, nil
}

type runnerWorkspacePending struct {
	TaskID, AttemptID, OperationID string
	SnapshotRelative               string
	Input, Candidate, Fingerprint  string
}

type runnerWorkspaceReference struct {
	ScopeKind, ScopeID string
	Journal            string
	Pending            runnerWorkspacePending
}

// The journal is ordinary private Runner state, not a second task authority.
// A snapshot becomes current only after the matching Server receipt is read.
type runnerWorkspaceChain struct {
	Schema                                   int
	ProjectID, Profile, RunnerID, Repository string
	CheckoutID, WorkspaceID, Source          string
	ScopeKind, ScopeID                       string
	BaselineCandidate, BaselineFingerprint   string
	CurrentCandidate, CurrentFingerprint     string
	CurrentRelative                          string
	LastTaskID, LastAttemptID, LastOperation string
	Pending                                  *runnerWorkspacePending `json:",omitempty"`
	path, runtime                            string
}

func runnerWorkspaceHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func runnerWorkspaceSnapshotPath(runtime, relative, attempt string) (string, error) {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 4 || parts[0] != "io" || parts[1] != "attempts" || parts[2] != attempt || !remoteRunnerID.MatchString(attempt) || parts[3] != "snapshot" {
		return "", core.Error("RUNNER_RECOVERY_INVALID", "workspace snapshot is outside the retained attempt")
	}
	path, err := store.SafePath(runtime, filepath.FromSlash(relative))
	if err != nil {
		return "", core.Error("RUNNER_RECOVERY_INVALID", "workspace snapshot path is unsafe")
	}
	return path, nil
}

func runnerOpenWorkspaceChain(cfg remoteRunnerConfig, project string, binding remote.Binding, taskID, parentID string) (*runnerWorkspaceChain, error) {
	return runnerWorkspaceChainForScope(cfg, project, binding, taskID, parentID, true)
}

func runnerWorkspaceChainForScope(cfg remoteRunnerConfig, project string, binding remote.Binding, taskID, parentID string, create bool) (*runnerWorkspaceChain, error) {
	if (taskID == "") == (parentID == "") || binding.Schema != 1 || binding.ProjectID == "" || binding.Profile == "" {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "workspace scope or remote binding is invalid")
	}
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, core.Error("RUNNER_WORKSPACE_UNAUTHORIZED", "workspace does not match this runner")
	}
	workspace, err := filepath.EvalSymlinks(cfg.Workspace)
	if err != nil || !filepath.IsAbs(cfg.Workspace) || project != workspace {
		return nil, core.Error("RUNNER_WORKSPACE_UNAUTHORIZED", "workspace does not match this runner")
	}
	runtime, err := filepath.EvalSymlinks(cfg.RuntimeDir)
	if err != nil || !filepath.IsAbs(cfg.RuntimeDir) {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "runner runtime is unavailable")
	}
	kind, id := "task", taskID
	if parentID != "" {
		kind, id = "parent", parentID
	}
	if !remoteRunnerID.MatchString(id) {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "workspace scope is invalid")
	}
	identity := struct{ Project, Profile, Runner, Repository, Checkout, Workspace, Source, Kind, ID string }{
		binding.ProjectID, binding.Profile, cfg.RunnerID, cfg.Repository, cfg.CheckoutID, cfg.WorkspaceID, project, kind, id,
	}
	rawIdentity, err := json.Marshal(identity)
	if err != nil {
		return nil, err
	}
	dir, err := runnerPrivateDir(runtime, "control", "runs")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "run-"+store.Hash(rawIdentity)[:32]+".json")
	marker := strings.TrimSuffix(path, ".json") + ".created"
	chain := &runnerWorkspaceChain{path: path, runtime: runtime}
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		if !create {
			return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "workspace journal is missing; retain the attempt for inspection")
		}
		if _, markerErr := os.Lstat(marker); markerErr == nil {
			return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "workspace journal is missing but its creation marker remains")
		} else if !os.IsNotExist(markerErr) {
			return nil, markerErr
		}
		candidate, fingerprint, err := runnerWorkspaceFingerprint(project, cfg.Repository)
		if err != nil {
			return nil, err
		}
		mark, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "workspace journal creation marker is unavailable")
		}
		_, err = mark.Write([]byte(store.Hash(rawIdentity)))
		if err == nil {
			err = mark.Sync()
		}
		if closeErr := mark.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		chain.Schema, chain.ProjectID, chain.Profile, chain.RunnerID, chain.Repository = 1, binding.ProjectID, binding.Profile, cfg.RunnerID, cfg.Repository
		chain.CheckoutID, chain.WorkspaceID, chain.Source, chain.ScopeKind, chain.ScopeID = cfg.CheckoutID, cfg.WorkspaceID, project, kind, id
		chain.BaselineCandidate, chain.CurrentCandidate = candidate.ID, candidate.ID
		chain.BaselineFingerprint, chain.CurrentFingerprint = fingerprint, fingerprint
		if err = chain.save(); err != nil {
			return nil, err
		}
		return chain, nil
	} else if err != nil {
		return nil, err
	}
	markerBytes, err := readRunnerPrivateFile(marker)
	if err != nil || string(markerBytes) != store.Hash(rawIdentity) {
		return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "workspace journal creation marker is missing or invalid")
	}
	data, err := readRunnerPrivateFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(chain) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "workspace journal is invalid")
	}
	if chain.Schema != 1 || chain.ProjectID != binding.ProjectID || chain.Profile != binding.Profile || chain.RunnerID != cfg.RunnerID || chain.Repository != cfg.Repository || chain.CheckoutID != cfg.CheckoutID || chain.WorkspaceID != cfg.WorkspaceID || chain.Source != project || chain.ScopeKind != kind || chain.ScopeID != id || !runnerWorkspaceHash(chain.BaselineCandidate) || !runnerWorkspaceHash(chain.BaselineFingerprint) || !runnerWorkspaceHash(chain.CurrentCandidate) || !runnerWorkspaceHash(chain.CurrentFingerprint) {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "workspace journal identity is inconsistent")
	}
	if chain.CurrentRelative == "" {
		if chain.CurrentCandidate != chain.BaselineCandidate || chain.CurrentFingerprint != chain.BaselineFingerprint || chain.LastTaskID != "" || chain.LastAttemptID != "" || chain.LastOperation != "" {
			return nil, core.Error("RUNNER_RECOVERY_INVALID", "workspace baseline is inconsistent")
		}
	} else {
		parts := strings.Split(filepath.ToSlash(chain.CurrentRelative), "/")
		if len(parts) != 4 || !remoteRunnerID.MatchString(chain.LastTaskID) || !remoteRunnerID.MatchString(chain.LastAttemptID) || !remoteRunnerID.MatchString(chain.LastOperation) || parts[2] != chain.LastAttemptID {
			return nil, core.Error("RUNNER_RECOVERY_INVALID", "workspace journal path is invalid")
		}
		if _, err = runnerWorkspaceSnapshotPath(runtime, chain.CurrentRelative, parts[2]); err != nil {
			return nil, err
		}
	}
	if chain.Pending != nil {
		p := chain.Pending
		if !remoteRunnerID.MatchString(p.TaskID) || !remoteRunnerID.MatchString(p.AttemptID) || !remoteRunnerID.MatchString(p.OperationID) || p.Input != chain.CurrentCandidate || !runnerWorkspaceHash(p.Candidate) || !runnerWorkspaceHash(p.Fingerprint) || (kind == "task" && p.TaskID != id) {
			return nil, core.Error("RUNNER_RECOVERY_INVALID", "pending workspace identity is invalid")
		}
		if _, err = runnerWorkspaceSnapshotPath(runtime, p.SnapshotRelative, p.AttemptID); err != nil {
			return nil, err
		}
	}
	return chain, nil
}

func (chain *runnerWorkspaceChain) currentWorkspace() (string, error) {
	if chain.Pending != nil {
		return "", core.Error("RUNNER_RECOVERY_REQUIRED", "a workspace result is pending receipt confirmation")
	}
	path := chain.Source
	if chain.CurrentRelative != "" {
		parts := strings.Split(filepath.ToSlash(chain.CurrentRelative), "/")
		if len(parts) != 4 {
			return "", core.Error("RUNNER_RECOVERY_INVALID", "workspace journal path is invalid")
		}
		var err error
		path, err = runnerWorkspaceSnapshotPath(chain.runtime, chain.CurrentRelative, parts[2])
		if err != nil {
			return "", err
		}
	}
	candidate, fingerprint, err := runnerWorkspaceFingerprint(path, chain.Repository)
	if err != nil {
		return "", err
	}
	if candidate.ID != chain.CurrentCandidate || fingerprint != chain.CurrentFingerprint {
		return "", core.Error("RUNNER_WORKSPACE_CHANGED", "current workspace differs from its accepted checkpoint")
	}
	return path, nil
}

func (chain *runnerWorkspaceChain) save() error {
	raw, err := json.Marshal(chain)
	if err != nil {
		return err
	}
	return store.Atomic(chain.path, raw, 0600)
}

func (chain *runnerWorkspaceChain) prepare(taskID, attemptID, operationID, snapshot, candidateID string) error {
	return chain.prepareGuarded(taskID, attemptID, operationID, snapshot, candidateID, nil)
}

func (chain *runnerWorkspaceChain) prepareGuarded(taskID, attemptID, operationID, snapshot, candidateID string, guard *runnerLeaseGuard) error {
	if guard != nil {
		if err := guard.check(); err != nil {
			return err
		}
	}
	if chain.Pending != nil || !remoteRunnerID.MatchString(taskID) || !remoteRunnerID.MatchString(attemptID) || !remoteRunnerID.MatchString(operationID) || (chain.ScopeKind == "task" && chain.ScopeID != taskID) {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "another workspace result is pending or the scope is invalid")
	}
	snapshot, err := filepath.EvalSymlinks(snapshot)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(chain.runtime, snapshot)
	if err != nil {
		return err
	}
	path, err := runnerWorkspaceSnapshotPath(chain.runtime, relative, attemptID)
	if err != nil {
		return err
	}
	candidate, fingerprint, err := runnerWorkspaceFingerprint(path, chain.Repository)
	if err != nil {
		return err
	}
	if candidate.ID != candidateID {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "output snapshot differs from its candidate")
	}
	if guard != nil {
		if err = guard.check(); err != nil {
			return err
		}
	}
	next := *chain
	next.Pending = &runnerWorkspacePending{TaskID: taskID, AttemptID: attemptID, OperationID: operationID, SnapshotRelative: filepath.ToSlash(relative), Input: chain.CurrentCandidate, Candidate: candidateID, Fingerprint: fingerprint}
	if err = next.save(); err != nil {
		return err
	}
	*chain = next
	return chain.ensurePendingReference(guard)
}

func (chain *runnerWorkspaceChain) ensurePendingReference(guard *runnerLeaseGuard) error {
	if chain.Pending == nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "no workspace result is pending")
	}
	path, err := runnerWorkspaceSnapshotPath(chain.runtime, chain.Pending.SnapshotRelative, chain.Pending.AttemptID)
	if err != nil {
		return err
	}
	candidate, fingerprint, err := runnerWorkspaceFingerprint(path, chain.Repository)
	if err != nil || candidate.ID != chain.Pending.Candidate || fingerprint != chain.Pending.Fingerprint {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "pending snapshot differs from its journal")
	}
	reference := runnerWorkspaceReference{ScopeKind: chain.ScopeKind, ScopeID: chain.ScopeID, Journal: filepath.Base(chain.path), Pending: *chain.Pending}
	refBytes, err := json.Marshal(reference)
	if err != nil {
		return err
	}
	refPath, err := store.SafePath(chain.runtime, filepath.Join("control", "attempts", chain.Pending.AttemptID, "workspace.json"))
	if err != nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "attempt workspace reference is unsafe")
	}
	info, err := os.Lstat(filepath.Dir(refPath))
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(filepath.Dir(refPath)) != nil {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "attempt control directory is missing")
	}
	if _, err = os.Lstat(refPath); err == nil {
		existing, readErr := readRunnerPrivateFile(refPath)
		if readErr != nil || !bytes.Equal(existing, refBytes) {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "attempt already has a different workspace reference")
		}
	} else if os.IsNotExist(err) {
		if guard != nil {
			if err = guard.check(); err != nil {
				return err
			}
		}
		if err = store.Atomic(refPath, refBytes, 0600); err != nil {
			return err
		}
	} else {
		return err
	}
	return nil
}

func (chain *runnerWorkspaceChain) validateReceipt(receipt map[string]any) error {
	if chain.Pending == nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "no workspace result is pending")
	}
	if found, ok := receipt["found"].(bool); ok && !found {
		return core.Error("RUNNER_RECOVERY_INVALID", "Server operation was not found")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	var value struct {
		OperationID string    `json:"operation_id"`
		Next        core.Next `json:"next"`
	}
	if json.Unmarshal(raw, &value) != nil || value.OperationID != chain.Pending.OperationID || value.Next.Task == nil || value.Next.Task.Definition.ID != chain.Pending.TaskID {
		return core.Error("RUNNER_RECOVERY_INVALID", "Server receipt does not match the pending workspace")
	}
	matched := false
	for _, attempt := range value.Next.Task.Attempts {
		if attempt.ID == chain.Pending.AttemptID && attempt.Executor == chain.RunnerID && attempt.Input == chain.Pending.Input && attempt.Candidate == chain.Pending.Candidate && (attempt.Status == "pass" || attempt.Status == "fail" || attempt.Status == "blocked") {
			matched = true
			break
		}
	}
	if !matched {
		return core.Error("RUNNER_RECOVERY_INVALID", "Server receipt does not contain the accepted attempt and candidate")
	}
	return nil
}

func (chain *runnerWorkspaceChain) accept(receipt map[string]any) error {
	if err := chain.validateReceipt(receipt); err != nil {
		return err
	}
	path, err := runnerWorkspaceSnapshotPath(chain.runtime, chain.Pending.SnapshotRelative, chain.Pending.AttemptID)
	if err != nil {
		return err
	}
	candidate, fingerprint, err := runnerWorkspaceFingerprint(path, chain.Repository)
	if err != nil {
		return err
	}
	if candidate.ID != chain.Pending.Candidate || fingerprint != chain.Pending.Fingerprint {
		return core.Error("RUNNER_WORKSPACE_CHANGED", "pending snapshot changed before receipt confirmation")
	}
	next := *chain
	next.CurrentRelative, next.CurrentCandidate, next.CurrentFingerprint = chain.Pending.SnapshotRelative, chain.Pending.Candidate, chain.Pending.Fingerprint
	next.LastTaskID, next.LastAttemptID, next.LastOperation = chain.Pending.TaskID, chain.Pending.AttemptID, chain.Pending.OperationID
	next.Pending = nil
	if err = next.save(); err != nil {
		return err
	}
	*chain = next
	return nil
}

func runnerAcceptWorkspaceReceipt(cfg remoteRunnerConfig, project string, binding remote.Binding, attemptID string, receipt map[string]any) error {
	if !remoteRunnerID.MatchString(attemptID) {
		return core.Error("RUNNER_RECOVERY_INVALID", "invalid attempt ID")
	}
	runtime, err := filepath.EvalSymlinks(cfg.RuntimeDir)
	if err != nil {
		return err
	}
	path, err := store.SafePath(runtime, filepath.Join("control", "attempts", attemptID, "workspace.json"))
	if err != nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "workspace reference path is unsafe")
	}
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		found, scanErr := runnerWorkspaceJournalMentionsAttempt(cfg, project, binding, attemptID)
		if scanErr != nil {
			return scanErr
		}
		if found {
			return core.Error("RUNNER_RECOVERY_REQUIRED", "workspace reference is missing for a journaled attempt")
		}
		// Results retained before workspace journals existed remain recoverable
		// only after checking that no current journal references the attempt.
		return nil
	} else if err != nil {
		return err
	}
	raw, err := readRunnerPrivateFile(path)
	if err != nil {
		return err
	}
	var reference runnerWorkspaceReference
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reference) != nil || decoder.Decode(new(any)) != io.EOF || reference.Pending.AttemptID != attemptID {
		return core.Error("RUNNER_RECOVERY_INVALID", "attempt workspace reference is invalid")
	}
	taskID, parentID := "", ""
	switch reference.ScopeKind {
	case "task":
		taskID = reference.ScopeID
	case "parent":
		parentID = reference.ScopeID
	default:
		return core.Error("RUNNER_RECOVERY_INVALID", "attempt workspace scope is invalid")
	}
	chain, err := runnerWorkspaceChainForScope(cfg, project, binding, taskID, parentID, false)
	if err != nil {
		return err
	}
	if reference.Journal != filepath.Base(chain.path) {
		return core.Error("RUNNER_RECOVERY_INVALID", "workspace reference points to a different journal")
	}
	if chain.Pending != nil {
		if *chain.Pending != reference.Pending {
			return core.Error("RUNNER_RECOVERY_INVALID", "workspace journal and attempt reference disagree")
		}
		return chain.accept(receipt)
	}
	if chain.LastTaskID != reference.Pending.TaskID || chain.LastAttemptID != reference.Pending.AttemptID || chain.LastOperation != reference.Pending.OperationID || chain.CurrentRelative != reference.Pending.SnapshotRelative || chain.CurrentCandidate != reference.Pending.Candidate || chain.CurrentFingerprint != reference.Pending.Fingerprint {
		return core.Error("RUNNER_RECOVERY_INVALID", "workspace receipt was not applied to this journal")
	}
	// Revalidate the receipt and snapshot even when recovering after journal
	// advancement but before the pending file was removed.
	check := *chain
	check.Pending = &reference.Pending
	if err = check.validateReceipt(receipt); err != nil {
		return err
	}
	_, err = chain.currentWorkspace()
	return err
}

func runnerWorkspaceJournalMentionsAttempt(cfg remoteRunnerConfig, project string, binding remote.Binding, attemptID string) (bool, error) {
	runtime, err := filepath.EvalSymlinks(cfg.RuntimeDir)
	if err != nil {
		return false, err
	}
	dir, err := store.SafePath(runtime, filepath.Join("control", "runs"))
	if err != nil {
		return false, core.Error("RUNNER_RECOVERY_INVALID", "workspace journal directory is unsafe")
	}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(dir) != nil {
		return false, core.Error("RUNNER_RECOVERY_REQUIRED", "workspace journal directory is unavailable")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return false, err
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "run-") || !strings.HasSuffix(name, ".created") || len(name) != len("run-")+32+len(".created") {
			continue
		}
		if _, err = hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(name, "run-"), ".created")); err != nil {
			continue
		}
		journal := filepath.Join(dir, strings.TrimSuffix(name, ".created")+".json")
		marker, markErr := readRunnerPrivateFile(filepath.Join(dir, name))
		if markErr != nil || !runnerWorkspaceHash(string(marker)) {
			return false, core.Error("RUNNER_RECOVERY_REQUIRED", "a workspace journal marker is invalid")
		}
		raw, readErr := readRunnerPrivateFile(journal)
		if readErr != nil {
			return false, core.Error("RUNNER_RECOVERY_REQUIRED", "a workspace journal is missing or unreadable")
		}
		var state runnerWorkspaceChain
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.Schema != 1 {
			return false, core.Error("RUNNER_RECOVERY_REQUIRED", "a workspace journal is invalid")
		}
		identity := struct{ Project, Profile, Runner, Repository, Checkout, Workspace, Source, Kind, ID string }{
			state.ProjectID, state.Profile, state.RunnerID, state.Repository, state.CheckoutID, state.WorkspaceID, state.Source, state.ScopeKind, state.ScopeID,
		}
		identityBytes, marshalErr := json.Marshal(identity)
		if marshalErr != nil || string(marker) != store.Hash(identityBytes) || filepath.Base(journal) != "run-"+store.Hash(identityBytes)[:32]+".json" {
			return false, core.Error("RUNNER_RECOVERY_REQUIRED", "a workspace journal identity is inconsistent")
		}
		seen[filepath.Base(journal)] = true
		if state.ProjectID == binding.ProjectID && state.Profile == binding.Profile && state.RunnerID == cfg.RunnerID && state.Repository == cfg.Repository && state.CheckoutID == cfg.CheckoutID && state.WorkspaceID == cfg.WorkspaceID && state.Source == project && (state.LastAttemptID == attemptID || (state.Pending != nil && state.Pending.AttemptID == attemptID)) {
			return true, nil
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "run-") && strings.HasSuffix(name, ".json") && len(name) == len("run-")+32+len(".json") && !seen[name] {
			return false, core.Error("RUNNER_RECOVERY_REQUIRED", "a workspace journal marker is missing")
		}
	}
	return false, nil
}

func runnerRecoverWorkspaceChain(cfg remoteRunnerConfig, config, project string, binding remote.Binding, taskID, parentID string, chain *runnerWorkspaceChain) (*runnerWorkspaceChain, error) {
	if chain.Pending == nil {
		plan, err := runnerFindWorkspacePlan(chain)
		if err != nil {
			return nil, err
		}
		if plan != "" {
			if _, err = resumeRunnerSubmissionPlan(cfg, config, project, binding, plan); err != nil {
				return nil, err
			}
			return runnerWorkspaceChainForScope(cfg, project, binding, taskID, parentID, false)
		}
	}
	attemptID, expectedTask := chain.LastAttemptID, chain.LastTaskID
	if chain.Pending != nil {
		attemptID, expectedTask = chain.Pending.AttemptID, chain.Pending.TaskID
		if err := chain.ensurePendingReference(nil); err != nil {
			return nil, err
		}
	}
	if attemptID == "" {
		return chain, nil
	}
	dir, err := store.SafePath(chain.runtime, filepath.Join("control", "attempts", attemptID))
	if err != nil {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "attempt control path is unsafe")
	}
	pending := filepath.Join(dir, "pending-submission.json")
	if _, err = os.Lstat(pending); err == nil {
		if _, err = resumeRunnerResult(cfg, config, project, binding, pending); err != nil {
			return nil, err
		}
		return runnerWorkspaceChainForScope(cfg, project, binding, taskID, parentID, false)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	plan := filepath.Join(dir, "submission-plan.json")
	if _, err = os.Lstat(plan); err == nil {
		if _, err = resumeRunnerSubmissionPlan(cfg, config, project, binding, plan); err != nil {
			return nil, err
		}
		return runnerWorkspaceChainForScope(cfg, project, binding, taskID, parentID, false)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	leasePath := filepath.Join(dir, "lease.json")
	_, leaseErr := os.Lstat(leasePath)
	if chain.Pending == nil && os.IsNotExist(leaseErr) {
		return chain, nil
	}
	if leaseErr != nil && !os.IsNotExist(leaseErr) {
		return nil, leaseErr
	}
	receiptPath := filepath.Join(dir, "receipt.json")
	raw, err := readRunnerPrivateFile(receiptPath)
	if err != nil {
		return nil, core.Error("RUNNER_RECOVERY_REQUIRED", "workspace result has no confirmed receipt; retain the attempt for inspection")
	}
	var receipt map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&receipt) != nil || receipt == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "saved workspace receipt is invalid")
	}
	if err = runnerAcceptWorkspaceReceipt(cfg, project, binding, attemptID, receipt); err != nil {
		return nil, err
	}
	if leaseErr == nil {
		leaseBytes, err := readRunnerPrivateFile(leasePath)
		if err != nil {
			return nil, err
		}
		var lease remoteRunnerLease
		if json.Unmarshal(leaseBytes, &lease) != nil || lease.AttemptID != attemptID || lease.TaskID != expectedTask || lease.RunnerID != cfg.RunnerID || lease.Token == "" {
			return nil, core.Error("RUNNER_RECOVERY_INVALID", "retained lease does not match the accepted workspace")
		}
		if err = os.Remove(leasePath); err != nil {
			return nil, err
		}
	}
	return runnerWorkspaceChainForScope(cfg, project, binding, taskID, parentID, false)
}

func runnerFindWorkspacePlan(chain *runnerWorkspaceChain) (string, error) {
	dir, err := store.SafePath(chain.runtime, filepath.Join("control", "attempts"))
	if err != nil {
		return "", core.Error("RUNNER_RECOVERY_INVALID", "attempt directory is unsafe")
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var found string
	for _, entry := range entries {
		if !remoteRunnerID.MatchString(entry.Name()) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return "", core.Error("RUNNER_RECOVERY_INVALID", "attempt directory is a link")
		}
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(filepath.Join(dir, entry.Name())) != nil {
			return "", core.Error("RUNNER_RECOVERY_INVALID", "attempt directory is not private")
		}
		path, err := store.SafePath(chain.runtime, filepath.Join("control", "attempts", entry.Name(), "submission-plan.json"))
		if err != nil {
			return "", core.Error("RUNNER_RECOVERY_INVALID", "submission plan path is unsafe")
		}
		if _, err = os.Lstat(path); os.IsNotExist(err) {
			markerPath := filepath.Join(filepath.Dir(path), "submission-plan.sha256")
			if _, markerErr := os.Lstat(markerPath); os.IsNotExist(markerErr) {
				continue
			} else if markerErr != nil {
				return "", markerErr
			}
			markerBytes, markerErr := readRunnerPrivateFile(markerPath)
			var marker runnerSubmissionMarker
			if markerErr != nil || json.Unmarshal(markerBytes, &marker) != nil || marker.Schema != 1 || marker.AttemptID != entry.Name() || !runnerWorkspaceHash(marker.PlanHash) {
				return "", core.Error("RUNNER_RECOVERY_REQUIRED", "a submission plan marker is invalid")
			}
			if marker.Journal == filepath.Base(chain.path) {
				if chain.LastAttemptID == entry.Name() {
					if err = chain.finishAcceptedMarker(entry.Name(), markerPath); err != nil {
						return "", err
					}
					continue
				}
				return "", core.Error("RUNNER_RECOVERY_REQUIRED", "a marked submission plan is missing; retain the attempt for inspection")
			}
			continue
		} else if err != nil {
			return "", err
		}
		plan, _, err := runnerReadSubmissionPlan(path)
		if err != nil || plan.Schema != 1 || plan.AttemptID != entry.Name() {
			return "", core.Error("RUNNER_RECOVERY_REQUIRED", "a retained submission plan is invalid")
		}
		if plan.Journal != filepath.Base(chain.path) {
			continue
		}
		if plan.RunnerID != chain.RunnerID || plan.ScopeKind != chain.ScopeKind || plan.ScopeID != chain.ScopeID || (plan.InputCandidate != chain.CurrentCandidate || plan.InputFingerprint != chain.CurrentFingerprint) && (plan.AttemptID != chain.LastAttemptID || plan.OperationID != chain.LastOperation) {
			return "", core.Error("RUNNER_RECOVERY_REQUIRED", "a retained plan does not follow the current workspace")
		}
		if found != "" {
			return "", core.Error("RUNNER_RECOVERY_REQUIRED", "multiple submission plans need inspection")
		}
		found = path
	}
	return found, nil
}

func (chain *runnerWorkspaceChain) finishAcceptedMarker(attemptID, markerPath string) error {
	dir, err := store.SafePath(chain.runtime, filepath.Join("control", "attempts", attemptID))
	if err != nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "attempt control path is unsafe")
	}
	if _, err = os.Lstat(filepath.Join(dir, "pending-submission.json")); err == nil {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "pending result remains after its plan disappeared")
	} else if !os.IsNotExist(err) {
		return err
	}
	refBytes, err := readRunnerPrivateFile(filepath.Join(dir, "workspace.json"))
	if err != nil {
		return err
	}
	var reference runnerWorkspaceReference
	if json.Unmarshal(refBytes, &reference) != nil || reference.Journal != filepath.Base(chain.path) || reference.ScopeKind != chain.ScopeKind || reference.ScopeID != chain.ScopeID || reference.Pending.TaskID != chain.LastTaskID || reference.Pending.AttemptID != chain.LastAttemptID || reference.Pending.OperationID != chain.LastOperation || reference.Pending.SnapshotRelative != chain.CurrentRelative || reference.Pending.Candidate != chain.CurrentCandidate || reference.Pending.Fingerprint != chain.CurrentFingerprint {
		return core.Error("RUNNER_RECOVERY_INVALID", "accepted workspace reference is inconsistent")
	}
	receiptBytes, err := readRunnerPrivateFile(filepath.Join(dir, "receipt.json"))
	if err != nil {
		return err
	}
	var receipt map[string]any
	if json.Unmarshal(receiptBytes, &receipt) != nil || receipt == nil {
		return core.Error("RUNNER_RECOVERY_INVALID", "accepted workspace receipt is invalid")
	}
	check := *chain
	check.Pending = &reference.Pending
	if err = check.validateReceipt(receipt); err != nil {
		return err
	}
	if _, err = chain.currentWorkspace(); err != nil {
		return err
	}
	leasePath := filepath.Join(dir, "lease.json")
	if _, err = os.Lstat(leasePath); err == nil {
		leaseBytes, err := readRunnerPrivateFile(leasePath)
		if err != nil {
			return err
		}
		var lease remoteRunnerLease
		if json.Unmarshal(leaseBytes, &lease) != nil || lease.AttemptID != attemptID || lease.TaskID != chain.LastTaskID || lease.RunnerID != chain.RunnerID || lease.Token == "" {
			return core.Error("RUNNER_RECOVERY_INVALID", "retained lease does not match the accepted workspace")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Remove(markerPath); err != nil {
		return err
	}
	if err = os.Remove(leasePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

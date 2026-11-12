package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	stdcontext "context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

type remoteRunnerInput struct {
	ID               string   `json:"id"`
	WorkspaceID      string   `json:"workspace_id"`
	CheckoutID       string   `json:"checkout_id"`
	Repository       string   `json:"repository"`
	Version          string   `json:"version"`
	Capabilities     []string `json:"capabilities"`
	ExpectedRevision string   `json:"expected_revision"`
	OperationID      string   `json:"operation_id"`
	IdempotencyKey   string   `json:"idempotency_key"`
}

type remoteRunnerClaimInput struct {
	TaskID           string `json:"task_id"`
	ParentID         string `json:"parent_id,omitempty"`
	ContextID        string `json:"context_id"`
	ExpectedRevision string `json:"expected_revision"`
	OperationID      string `json:"operation_id"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type remoteRunnerHeartbeatInput struct {
	TaskID         string `json:"task_id"`
	AttemptID      string `json:"attempt_id"`
	Token          string `json:"token"`
	Fence          int    `json:"fence"`
	OperationID    string `json:"operation_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type remoteRunnerProgressInput struct {
	TaskID         string     `json:"task_id"`
	AttemptID      string     `json:"attempt_id"`
	Token          string     `json:"token"`
	Fence          int        `json:"fence"`
	Summary        string     `json:"summary"`
	Next           string     `json:"next"`
	Stage          string     `json:"stage"`
	ExpectedUntil  *time.Time `json:"expected_until,omitempty"`
	OperationID    string     `json:"operation_id"`
	IdempotencyKey string     `json:"idempotency_key"`
}

type remoteRunnerObservationInput struct {
	TaskID           string                   `json:"task_id"`
	AttemptID        string                   `json:"attempt_id"`
	Token            string                   `json:"token"`
	Fence            int                      `json:"fence"`
	ExpectedRevision string                   `json:"expected_revision"`
	Candidate        remote.PortableCandidate `json:"candidate"`
	OperationID      string                   `json:"operation_id"`
	IdempotencyKey   string                   `json:"idempotency_key"`
}

type remoteRunnerInterruptInput struct {
	TaskID         string `json:"task_id"`
	AttemptID      string `json:"attempt_id"`
	Token          string `json:"token"`
	Fence          int    `json:"fence"`
	Reason         string `json:"reason"`
	OperationID    string `json:"operation_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (p *portableStore) registerRunner(principal standalonePrincipal, input remoteRunnerInput) (map[string]any, error) {
	if !standaloneID.MatchString(input.ID) || !standaloneID.MatchString(input.WorkspaceID) || !standaloneID.MatchString(input.CheckoutID) || !standaloneID.MatchString(input.Repository) || input.ExpectedRevision != "absent" || strings.TrimSpace(input.Version) == "" || len(input.Version) > 100 || strings.ContainsAny(input.Version, "\r\n\x00") || len(input.Capabilities) > 32 {
		return nil, errors.New("RUNNER_INVALID")
	}
	seen := map[string]bool{}
	for _, capability := range input.Capabilities {
		if !standaloneID.MatchString(capability) || seen[capability] {
			return nil, errors.New("RUNNER_INVALID")
		}
		seen[capability] = true
	}
	return p.mutate(principal, "register-runner", input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var repository string
		if err := tx.Row("SELECT id FROM portable_repositories WHERE id=?", input.Repository).Scan(&repository); err != nil {
			return nil, errors.New("REPOSITORY_NOT_FOUND")
		}
		var existing string
		err := tx.Row("SELECT id FROM remote_runners WHERE id=?", input.ID).Scan(&existing)
		if err == nil {
			return nil, errors.New("REVISION_CONFLICT")
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		capabilities, _ := json.Marshal(input.Capabilities)
		created := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err = tx.Exec("INSERT INTO remote_runners(id,actor,workspace_id,checkout_id,repository,version,capabilities,created) VALUES(?,?,?,?,?,?,?,?)", input.ID, principal.UserID, input.WorkspaceID, input.CheckoutID, input.Repository, input.Version, string(capabilities), created); err != nil {
			return nil, err
		}
		return map[string]any{"id": input.ID, "workspace_id": input.WorkspaceID, "checkout_id": input.CheckoutID, "repository": input.Repository, "version": input.Version, "capabilities": input.Capabilities, "actor": principal.Username, "created_at": created, "operation_id": input.OperationID}, nil
	})
}

func (p *portableStore) getRunner(principal standalonePrincipal, runnerID string) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) {
		return nil, errors.New("RUNNER_NOT_FOUND")
	}
	var owner, workspace, checkout, repository, version, rawCapabilities, created string
	if err := p.service.Store.DB.QueryRow("SELECT actor,workspace_id,checkout_id,repository,version,capabilities,created FROM remote_runners WHERE id=?", runnerID).Scan(&owner, &workspace, &checkout, &repository, &version, &rawCapabilities, &created); err != nil || owner != principal.UserID {
		return nil, errors.New("RUNNER_NOT_FOUND")
	}
	var capabilities []string
	if json.Unmarshal([]byte(rawCapabilities), &capabilities) != nil {
		return nil, errors.New("RUNNER_STORAGE_ERROR")
	}
	connection := "no_responsibility"
	rows, err := p.service.Store.DB.Query("SELECT a.attempt_id,t.body FROM remote_runner_attempts a JOIN tasks t ON t.id=a.task_id WHERE a.runner_id=? AND a.state IN ('running','interrupted')", runnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		var task core.Task
		if json.Unmarshal([]byte(body), &task) != nil {
			return nil, errors.New("RUNNER_STORAGE_ERROR")
		}
		for _, attempt := range task.Attempts {
			if attempt.ID == id {
				connection = "last_reported"
				if attempt.Status != "running" || !attempt.Lease.After(time.Now()) || task.Status != "open" || task.Paused {
					connection = "lost"
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"id": runnerID, "workspace_id": workspace, "checkout_id": checkout, "repository": repository, "version": version, "capabilities": capabilities, "created_at": created, "connection_state": connection, "host_execution_state": "unknown"}, nil
}

func (p *portableStore) claimRunner(principal standalonePrincipal, role, runnerID string, input remoteRunnerClaimInput, artifacts *artifactStore) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.ContextID) || input.ExpectedRevision == "" {
		return nil, errors.New("RUNNER_INVALID")
	}
	return p.mutate(principal, "runner-claim:"+runnerID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var owner, workspace, checkout, repository, rawCapabilities string
		if err := tx.Row("SELECT actor,workspace_id,checkout_id,repository,capabilities FROM remote_runners WHERE id=?", runnerID).Scan(&owner, &workspace, &checkout, &repository, &rawCapabilities); err != nil || owner != principal.UserID {
			return nil, errors.New("RUNNER_NOT_FOUND")
		}
		if err := remoteWorkspaceAvailable(tx, repository, workspace); err != nil {
			return nil, err
		}
		if err := reconcileRunnerAttempts(tx, runnerID); err != nil {
			return nil, err
		}
		var active int
		if err := tx.Row("SELECT count(*) FROM remote_runner_attempts WHERE runner_id=? AND state IN ('running','interrupted')", runnerID).Scan(&active); err != nil {
			return nil, err
		}
		if active > 0 {
			return nil, errors.New("RUNNER_WIP")
		}
		var capabilities []string
		if json.Unmarshal([]byte(rawCapabilities), &capabilities) != nil {
			return nil, errors.New("RUNNER_STORAGE_ERROR")
		}
		chosenUnit := ""
		if input.ParentID != "" {
			if !standaloneID.MatchString(input.ParentID) {
				return nil, errors.New("RUNNER_INVALID")
			}
			choice, err := p.service.RemoteWorkChoiceTx(tx, core.WorkRequest{Parent: input.ParentID, Runner: runnerID, Workspace: workspace, Capabilities: capabilities})
			if err != nil {
				return nil, err
			}
			if choice.Action != "ready" || choice.Checkpoint.Task != input.TaskID {
				return nil, errors.New("NEXT_WORK_CHANGED")
			}
			chosenUnit = choice.Checkpoint.Unit
		}
		var body, taskID, candidateID string
		if err := tx.Row("SELECT body,task_id,candidate_id FROM portable_contexts WHERE id=?", input.ContextID).Scan(&body, &taskID, &candidateID); err != nil {
			return nil, errors.New("CONTEXT_NOT_FOUND")
		}
		var context map[string]any
		if json.Unmarshal([]byte(body), &context) != nil {
			return nil, errors.New("RUNNER_STORAGE_ERROR")
		}
		if taskID != input.TaskID || context["task_revision"] != input.ExpectedRevision || context["runner_id"] != runnerID || context["workspace_id"] != workspace || context["checkout_id"] != checkout || context["repository"] != repository {
			return nil, errors.New("REVISION_CONFLICT")
		}
		if context["inventory_unverified"] == true {
			return nil, errors.New("KNOWLEDGE_UNVERIFIED")
		}
		var latest string
		if err := tx.Row("SELECT candidate_id FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", taskID).Scan(&latest); err != nil || latest != candidateID {
			return nil, errors.New("CANDIDATE_CHANGED")
		}
		rows, err := tx.Conn.QueryContext(stdcontext.Background(), "SELECT id,path,artifact_id,sha256,status,reason FROM portable_context_sources WHERE context_id=? ORDER BY id", input.ContextID)
		if err != nil {
			return nil, err
		}
		type source struct{ id, path, artifact, hash, status, reason string }
		var sources []source
		for rows.Next() {
			var item source
			if err = rows.Scan(&item.id, &item.path, &item.artifact, &item.hash, &item.status, &item.reason); err != nil {
				break
			}
			sources = append(sources, item)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		basis := make([]core.BasisReference, 0, len(sources))
		for _, source := range sources {
			item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", source.artifact))
			if err != nil || item.Status != "finalized" || item.SHA256 != source.hash {
				return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
			}
			if _, err = artifacts.read(item); err != nil {
				return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
			}
			basis = append(basis, core.BasisReference{ID: source.id, Source: source.path, Version: source.hash, Status: source.status, Reason: source.reason})
		}
		token, err := standaloneRandom("lease_")
		if err != nil {
			return nil, err
		}
		task, attempt, err := p.service.RemoteClaimTx(tx, core.RemoteClaimInput{TaskID: taskID, ParentID: input.ParentID, ExpectedRevision: input.ExpectedRevision, Runner: runnerID, Workspace: workspace, Candidate: candidateID, Token: token, Basis: basis, ReviewerOnly: role == "reviewer"})
		if err != nil {
			return nil, err
		}
		if chosenUnit != "" && attempt.Unit != chosenUnit && attempt.Unit != "check:"+chosenUnit {
			return nil, errors.New("NEXT_WORK_CHANGED")
		}
		if strings.HasPrefix(attempt.Unit, "check:") {
			id := strings.TrimPrefix(attempt.Unit, "check:")
			for _, check := range task.Definition.Checks {
				if check.ID == id && len(check.Command) == 0 && !slices.Contains(capabilities, check.Method) {
					return nil, errors.New("RUNNER_CAPABILITY_MISSING")
				}
			}
		} else if !slices.Contains(capabilities, "development") {
			return nil, errors.New("RUNNER_CAPABILITY_MISSING")
		}
		if _, err = tx.Exec("INSERT INTO remote_runner_attempts(attempt_id,task_id,runner_id,actor,context_id,candidate_id,claim_revision,fence,token_hash) VALUES(?,?,?,?,?,?,?,?,?)", attempt.ID, taskID, runnerID, principal.UserID, input.ContextID, candidateID, input.ExpectedRevision, attempt.Fence, store.Hash([]byte(token))); err != nil {
			return nil, err
		}
		return map[string]any{"task": core.RemoteTaskView(task), "attempt": attempt, "context_id": input.ContextID, "revision": core.RemoteRevision(task), "operation_id": input.OperationID}, nil
	})
}

func (p *portableStore) verifyRunnerLease(tx *store.Tx, principal standalonePrincipal, runnerID, taskID, attemptID, token string, fence int) error {
	var owner, storedRunner, storedTask, hash, state string
	var storedFence int
	if err := tx.Row("SELECT actor,runner_id,task_id,token_hash,fence,state FROM remote_runner_attempts WHERE attempt_id=?", attemptID).Scan(&owner, &storedRunner, &storedTask, &hash, &storedFence, &state); err != nil {
		return errors.New("STALE_TOKEN")
	}
	want := store.Hash([]byte(token))
	if owner != principal.UserID || storedRunner != runnerID || storedTask != taskID || storedFence != fence || state != "running" || subtle.ConstantTimeCompare([]byte(hash), []byte(want)) != 1 {
		return errors.New("STALE_TOKEN")
	}
	return nil
}

func (p *portableStore) heartbeatRunner(principal standalonePrincipal, runnerID string, input remoteRunnerHeartbeatInput) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.AttemptID) || input.Token == "" || input.Fence < 1 {
		return nil, errors.New("RUNNER_INVALID")
	}
	return p.mutate(principal, "runner-heartbeat:"+runnerID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		if err := p.verifyRunnerLease(tx, principal, runnerID, input.TaskID, input.AttemptID, input.Token, input.Fence); err != nil {
			return nil, err
		}
		task, attempt, err := p.service.RemoteHeartbeatTx(tx, input.TaskID, runnerID, input.AttemptID, input.Token, input.Fence)
		if err != nil {
			return nil, err
		}
		return map[string]any{"attempt_id": attempt.ID, "fence": attempt.Fence, "lease_issued_at": attempt.LastContact, "lease_until": attempt.Lease, "revision": core.RemoteRevision(task), "operation_id": input.OperationID}, nil
	})
}

func (p *portableStore) progressRunner(principal standalonePrincipal, runnerID string, input remoteRunnerProgressInput) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.AttemptID) || input.Token == "" || input.Fence < 1 || len(input.Summary) > 1000 || len(input.Next) > 1000 || len(input.Stage) > 100 {
		return nil, errors.New("RUNNER_INVALID")
	}
	return p.mutate(principal, "runner-progress:"+runnerID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		if err := p.verifyRunnerLease(tx, principal, runnerID, input.TaskID, input.AttemptID, input.Token, input.Fence); err != nil {
			return nil, err
		}
		progress := core.Progress{Summary: input.Summary, Next: input.Next, Stage: input.Stage, ExpectedUntil: input.ExpectedUntil, EvidenceKind: "diagnostic"}
		task, err := p.service.RemoteProgressTx(tx, input.TaskID, runnerID, input.AttemptID, input.Token, input.Fence, progress)
		if err != nil {
			return nil, err
		}
		return map[string]any{"attempt_id": input.AttemptID, "fence": input.Fence, "revision": core.RemoteRevision(task), "evidence_changed": false, "operation_id": input.OperationID}, nil
	})
}

func (p *portableStore) observeRunner(principal standalonePrincipal, runnerID string, input remoteRunnerObservationInput) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.AttemptID) || input.Token == "" || input.Fence < 1 || input.ExpectedRevision == "" || remote.ValidatePortableCandidate(input.Candidate) != nil {
		return nil, errors.New("RUNNER_INVALID")
	}
	return p.mutate(principal, "runner-observe:"+runnerID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		if err := p.verifyRunnerLease(tx, principal, runnerID, input.TaskID, input.AttemptID, input.Token, input.Fence); err != nil {
			return nil, err
		}
		task, attempt, err := p.service.RemoteAttemptTx(tx, input.TaskID, runnerID, input.AttemptID, input.Token, input.Fence)
		if err != nil {
			return nil, err
		}
		var revision, repository string
		if err = tx.Row("SELECT a.claim_revision,r.repository FROM remote_runner_attempts a JOIN remote_runners r ON r.id=a.runner_id WHERE a.attempt_id=?", input.AttemptID).Scan(&revision, &repository); err != nil {
			return nil, err
		}
		if revision != input.ExpectedRevision || input.Candidate.Repository != repository {
			return nil, errors.New("REVISION_CONFLICT")
		}
		if strings.HasPrefix(attempt.Unit, "check:") && input.Candidate.ID != attempt.Input {
			return nil, errors.New("CANDIDATE_CHANGED")
		}
		body, err := json.Marshal(input.Candidate)
		if err != nil {
			return nil, err
		}
		var existing string
		err = tx.Row("SELECT body FROM portable_candidates WHERE id=?", input.Candidate.ID).Scan(&existing)
		if err == nil && existing != string(body) {
			return nil, errors.New("CANDIDATE_CONFLICT")
		}
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == sql.ErrNoRows {
			if _, err = tx.Exec("INSERT INTO portable_candidates(id,repository,commit_id,body,created) VALUES(?,?,?,?,?)", input.Candidate.ID, input.Candidate.Repository, input.Candidate.Commit, string(body), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec("UPDATE remote_runner_attempts SET output_candidate=? WHERE attempt_id=?", input.Candidate.ID, attempt.ID); err != nil {
			return nil, err
		}
		return map[string]any{"attempt_id": attempt.ID, "candidate_id": input.Candidate.ID, "revision": core.RemoteRevision(task), "operation_id": input.OperationID}, nil
	})
}

func (p *portableStore) nextRunner(principal standalonePrincipal, runnerID, parentID string) (core.WorkResult, error) {
	var result core.WorkResult
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(parentID) {
		return result, errors.New("RUNNER_INVALID")
	}
	err := p.service.Store.Write(func(tx *store.Tx) error {
		var owner, workspace, rawCapabilities string
		if err := tx.Row("SELECT actor,workspace_id,capabilities FROM remote_runners WHERE id=?", runnerID).Scan(&owner, &workspace, &rawCapabilities); err != nil || owner != principal.UserID {
			return errors.New("RUNNER_NOT_FOUND")
		}
		var capabilities []string
		if json.Unmarshal([]byte(rawCapabilities), &capabilities) != nil {
			return errors.New("RUNNER_STORAGE_ERROR")
		}
		active, err := runnerHasActiveAttempts(tx, runnerID)
		if err != nil {
			return err
		}
		if active {
			return errors.New("RUNNER_WIP")
		}
		result, err = p.service.RemoteWorkChoiceTx(tx, core.WorkRequest{Parent: parentID, Runner: runnerID, Workspace: workspace, Capabilities: capabilities})
		return err
	})
	return result, err
}

func runnerHasActiveAttempts(tx *store.Tx, runnerID string) (bool, error) {
	rows, err := tx.Conn.QueryContext(stdcontext.Background(), "SELECT attempt_id,task_id FROM remote_runner_attempts WHERE runner_id=? AND state IN ('running','interrupted')", runnerID)
	if err != nil {
		return false, err
	}
	type entry struct{ attempt, task string }
	var entries []entry
	for rows.Next() {
		var item entry
		if err = rows.Scan(&item.attempt, &item.task); err != nil {
			break
		}
		entries = append(entries, item)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return false, err
	}
	for _, item := range entries {
		task, err := artifactTask(tx, item.task)
		if err != nil {
			return true, nil
		}
		found := false
		for _, attempt := range task.Attempts {
			if attempt.ID == item.attempt {
				found = true
				if attempt.Status == "running" || attempt.Status == "interrupted" {
					return true, nil
				}
				break
			}
		}
		if !found {
			return true, nil
		}
	}
	return false, nil
}

// Only a task-state transition that has already marked an attempt abandoned
// or completed releases the runner. Expiry alone never proves a writer died.
func reconcileRunnerAttempts(tx *store.Tx, runnerID string) error {
	rows, err := tx.Conn.QueryContext(stdcontext.Background(), "SELECT attempt_id,task_id FROM remote_runner_attempts WHERE runner_id=? AND state IN ('running','interrupted')", runnerID)
	if err != nil {
		return err
	}
	type entry struct{ attempt, task string }
	var entries []entry
	for rows.Next() {
		var item entry
		if err = rows.Scan(&item.attempt, &item.task); err != nil {
			break
		}
		entries = append(entries, item)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, item := range entries {
		task, err := artifactTask(tx, item.task)
		if err != nil {
			continue
		}
		for _, attempt := range task.Attempts {
			if attempt.ID == item.attempt && attempt.Status != "running" && attempt.Status != "interrupted" {
				if _, err = tx.Exec("UPDATE remote_runner_attempts SET state='finished' WHERE attempt_id=?", item.attempt); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func (p *portableStore) interruptRunner(principal standalonePrincipal, runnerID string, input remoteRunnerInterruptInput) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.AttemptID) || input.Token == "" || input.Fence < 1 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 1000 || strings.Contains(input.Reason, input.Token) {
		return nil, errors.New("RUNNER_INVALID")
	}
	return p.mutate(principal, "runner-interrupt:"+runnerID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		if err := p.verifyRunnerLease(tx, principal, runnerID, input.TaskID, input.AttemptID, input.Token, input.Fence); err != nil {
			return nil, err
		}
		task, err := p.service.RemoteInterruptTx(tx, input.TaskID, runnerID, input.AttemptID, input.Token, input.Fence, input.Reason)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE remote_runner_attempts SET state='interrupted' WHERE attempt_id=?", input.AttemptID); err != nil {
			return nil, err
		}
		return map[string]any{"task": core.RemoteTaskView(task), "revision": core.RemoteRevision(task), "operation_id": input.OperationID}, nil
	})
}

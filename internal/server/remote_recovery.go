package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type remoteRunnerRecoveryInput struct {
	TaskID           string `json:"task_id"`
	AttemptID        string `json:"attempt_id"`
	ContextID        string `json:"context_id"`
	ExpectedRevision string `json:"expected_revision"`
	Mode             string `json:"mode"`
	Reason           string `json:"reason"`
	OperationID      string `json:"operation_id"`
	IdempotencyKey   string `json:"idempotency_key"`
}

func (p *portableStore) recoverRunner(principal standalonePrincipal, runnerID string, input remoteRunnerRecoveryInput) (map[string]any, error) {
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.AttemptID) || input.ExpectedRevision == "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 1000 {
		return nil, errors.New("REMOTE_RECOVERY_INVALID")
	}
	return p.mutate(principal, "runner-recover:"+runnerID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var owner, workspace, repository string
		if err := tx.Row("SELECT actor,workspace_id,repository FROM remote_runners WHERE id=?", runnerID).Scan(&owner, &workspace, &repository); err != nil || owner != principal.UserID {
			return nil, errors.New("RUNNER_NOT_FOUND")
		}
		if err := reconcileRunnerAttempts(tx, runnerID); err != nil {
			return nil, err
		}
		var occupied int
		if err := tx.Row("SELECT count(*) FROM remote_runner_attempts WHERE runner_id=? AND task_id!=? AND state IN ('running','interrupted')", runnerID, input.TaskID).Scan(&occupied); err != nil {
			return nil, err
		}
		if occupied > 0 {
			return nil, errors.New("RUNNER_WIP")
		}
		if input.Mode == "independent-workspace" {
			if err := remoteWorkspaceAvailable(tx, repository, workspace); err != nil {
				return nil, err
			}
		}

		var oldWorkspace, oldRepository, state string
		if err := tx.Row("SELECT r.workspace_id,r.repository,a.state FROM remote_runner_attempts a JOIN remote_runners r ON r.id=a.runner_id WHERE a.attempt_id=? AND a.task_id=?", input.AttemptID, input.TaskID).Scan(&oldWorkspace, &oldRepository, &state); err != nil {
			return nil, errors.New("REMOTE_RECOVERY_INVALID")
		}
		if oldRepository != repository {
			return nil, errors.New("REMOTE_RECOVERY_INVALID")
		}
		if input.Mode == "independent-workspace" && oldWorkspace == workspace {
			return nil, errors.New("WORKSPACE_CONFIRMATION_REQUIRED")
		}
		// A stopped declaration can also release an already retained old workspace.
		snapshot, err := artifactTask(tx, input.TaskID)
		if err != nil {
			return nil, err
		}
		acknowledgementOnly := input.Mode == "confirmed-stopped" && (snapshot.Status != "open" || snapshot.Confirmation != nil)
		var candidateID string
		if !acknowledgementOnly && (state == "running" || state == "interrupted" || input.Mode == "independent-workspace") {
			var body, taskID string
			if err := tx.Row("SELECT body,task_id,candidate_id FROM portable_contexts WHERE id=?", input.ContextID).Scan(&body, &taskID, &candidateID); err != nil {
				return nil, errors.New("CONTEXT_NOT_FOUND")
			}
			var context map[string]any
			if json.Unmarshal([]byte(body), &context) != nil || taskID != input.TaskID || context["task_revision"] != input.ExpectedRevision || context["runner_id"] != runnerID || context["workspace_id"] != workspace || context["repository"] != repository {
				return nil, errors.New("REVISION_CONFLICT")
			}
			if context["inventory_unverified"] == true {
				return nil, errors.New("KNOWLEDGE_UNVERIFIED")
			}
		}
		task, err := p.service.RemoteRecoverTx(tx, core.RemoteRecovery{TaskID: input.TaskID, AttemptID: input.AttemptID, ExpectedRevision: input.ExpectedRevision, Runner: runnerID, Workspace: workspace, Candidate: candidateID, Mode: input.Mode, Reason: input.Reason})
		if err != nil {
			return nil, err
		}
		holdState := "unknown"
		if input.Mode == "confirmed-stopped" {
			holdState = "stopped"
		}
		if state == "running" || state == "interrupted" || input.Mode == "confirmed-stopped" {
			if _, err = tx.Exec("INSERT INTO remote_workspace_holds(repository,workspace_id,attempt_id,task_id,state) VALUES(?,?,?,?,?) ON CONFLICT(repository,workspace_id,attempt_id) DO UPDATE SET state=excluded.state", repository, oldWorkspace, input.AttemptID, input.TaskID, holdState); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec("UPDATE remote_runner_attempts SET state='finished' WHERE attempt_id=?", input.AttemptID); err != nil {
			return nil, err
		}
		return map[string]any{"task": core.RemoteTaskView(task), "revision": core.RemoteRevision(task), "operation_id": input.OperationID, "workspace_decision": input.Mode, "host_execution_state": "unknown"}, nil
	})
}

func remoteWorkspaceAvailable(tx *store.Tx, repository, workspace string) error {
	var unknown int
	if err := tx.Row("SELECT count(*) FROM remote_workspace_holds WHERE repository=? AND workspace_id=? AND state='unknown'", repository, workspace).Scan(&unknown); err != nil {
		return err
	}
	if unknown > 0 {
		return errors.New("WORKSPACE_CONFIRMATION_REQUIRED")
	}
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT a.attempt_id,t.body FROM remote_runner_attempts a JOIN remote_runners r ON r.id=a.runner_id JOIN tasks t ON t.id=a.task_id WHERE r.repository=? AND r.workspace_id=? AND a.state IN ('running','interrupted')", repository, workspace)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			return err
		}
		var task core.Task
		if json.Unmarshal([]byte(body), &task) != nil {
			return errors.New("RUNNER_STORAGE_ERROR")
		}
		for _, attempt := range task.Attempts {
			if attempt.ID == id && (attempt.Status == "interrupted" || (attempt.Status == "running" && (!attempt.Lease.After(time.Now()) || task.Status != "open" || task.Paused || attempt.DefinitionRevision != task.Definition.Revision || attempt.PlanRevision != task.Plan.Revision))) {
				return errors.New("WORKSPACE_CONFIRMATION_REQUIRED")
			}
		}
	}
	return rows.Err()
}

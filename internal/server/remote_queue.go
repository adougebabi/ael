package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"database/sql"
	"errors"
	"strings"
)

// remoteQueueInput is shared by queue, queue-release and queue-transfer.
// The path runner is the current (or, for queue, the requesting) holder.
type remoteQueueInput struct {
	TaskID          string `json:"task_id"`
	ExpectedVersion string `json:"expected_version"`
	Scope           string `json:"scope,omitempty"`
	ToRunner        string `json:"to_runner,omitempty"`
	ToScope         string `json:"to_scope,omitempty"`
	Reason          string `json:"reason,omitempty"`
	OperationID     string `json:"operation_id"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type queueRunner struct{ ID, Actor, Label string }

func queueRunnerTx(tx *store.Tx, id string) (queueRunner, error) {
	r := queueRunner{ID: id}
	if !standaloneID.MatchString(id) {
		return r, errors.New("RUNNER_NOT_FOUND")
	}
	err := tx.Row("SELECT r.actor,coalesce(nullif(l.alias,''),r.id) FROM remote_runners r LEFT JOIN remote_runner_labels l ON l.runner_id=r.id WHERE r.id=?", id).Scan(&r.Actor, &r.Label)
	if err == sql.ErrNoRows {
		return r, errors.New("RUNNER_NOT_FOUND")
	}
	return r, err
}

func validQueueText(values ...string) bool {
	for _, v := range values {
		if len(v) > 500 || strings.ContainsAny(v, "\r\n\x00") {
			return false
		}
	}
	return true
}

func queueReceipt(task core.Task, record *core.QueueRecord, state, op string) map[string]any {
	result := map[string]any{"task_id": task.Definition.ID, "state": state, "revision": core.RemoteRevision(task), "operation_id": op, "queue": nil}
	if record != nil {
		result["queue"] = record
	}
	return result
}

// enqueueRunner records that runnerID (owned by the caller) queued the task.
func (p *portableStore) enqueueRunner(principal standalonePrincipal, role, runnerID string, input remoteQueueInput) (map[string]any, error) {
	if input.Scope == "" {
		input.Scope = "runner"
	}
	if !standaloneID.MatchString(runnerID) || !standaloneID.MatchString(input.TaskID) || input.ExpectedVersion == "" || len(input.ExpectedVersion) > 100 || (input.Scope != "runner" && input.Scope != "executor") || !validQueueText(input.Reason) {
		return nil, errors.New("QUEUE_INVALID")
	}
	if role != "admin" && role != "executor" {
		return nil, errors.New("FORBIDDEN")
	}
	return p.mutate(principal, "runner-queue:"+runnerID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		runner, err := queueRunnerTx(tx, runnerID)
		if err != nil || runner.Actor != principal.UserID {
			return nil, errors.New("RUNNER_NOT_FOUND")
		}
		record, err := p.service.QueueEnqueueTx(tx, core.QueueInput{TaskID: input.TaskID, ExpectedVersion: input.ExpectedVersion, Runner: runner.ID, RunnerLabel: runner.Label, Actor: principal.UserID, ActorName: principal.Username, Scope: input.Scope, Reason: input.Reason})
		if err != nil {
			return nil, err
		}
		task, _, err := p.service.QueueLoadTx(tx, input.TaskID)
		if err != nil {
			return nil, err
		}
		return queueReceipt(task, &record, "queued", input.OperationID), nil
	})
}

// queueHolderAuthorized: the holder's own executor account or a project admin.
func queueHolderAuthorized(principal standalonePrincipal, role string, record *core.QueueRecord) bool {
	return record == nil || role == "admin" || record.Actor == principal.UserID
}

func (p *portableStore) releaseQueue(principal standalonePrincipal, role, holder string, input remoteQueueInput) (map[string]any, error) {
	if !standaloneID.MatchString(holder) || !standaloneID.MatchString(input.TaskID) || input.ExpectedVersion == "" || len(input.ExpectedVersion) > 100 || !validQueueText(input.Reason) {
		return nil, errors.New("QUEUE_INVALID")
	}
	if role != "admin" && role != "executor" {
		return nil, errors.New("FORBIDDEN")
	}
	return p.mutate(principal, "runner-queue-release:"+holder, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		_, current, err := p.service.QueueLoadTx(tx, input.TaskID)
		if err != nil {
			return nil, err
		}
		if !queueHolderAuthorized(principal, role, current) {
			return nil, errors.New("FORBIDDEN")
		}
		if _, err = p.service.QueueReleaseTx(tx, input.TaskID, holder, input.ExpectedVersion, input.Reason, principal.Username); err != nil {
			return nil, err
		}
		task, _, err := p.service.QueueLoadTx(tx, input.TaskID)
		if err != nil {
			return nil, err
		}
		return queueReceipt(task, nil, "released", input.OperationID), nil
	})
}

// transferQueue moves a not-yet-started record to another project executor.
// member resolves the target account's current project role and name.
func (p *portableStore) transferQueue(principal standalonePrincipal, role, holder string, input remoteQueueInput, member func(string) (string, string)) (map[string]any, error) {
	if input.ToScope == "" {
		input.ToScope = "runner"
	}
	if !standaloneID.MatchString(holder) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.ToRunner) || input.ExpectedVersion == "" || len(input.ExpectedVersion) > 100 || strings.TrimSpace(input.Reason) == "" || !validQueueText(input.Reason) || (input.ToScope != "runner" && input.ToScope != "executor") {
		return nil, errors.New("QUEUE_INVALID")
	}
	if role != "admin" && role != "executor" {
		return nil, errors.New("FORBIDDEN")
	}
	return p.mutate(principal, "runner-queue-transfer:"+holder, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		task, current, err := p.service.QueueLoadTx(tx, input.TaskID)
		if err != nil {
			return nil, err
		}
		if !queueHolderAuthorized(principal, role, current) {
			return nil, errors.New("FORBIDDEN")
		}
		target, err := queueRunnerTx(tx, input.ToRunner)
		if err != nil {
			return nil, err
		}
		targetRole, targetName := member(target.Actor)
		if targetRole != "admin" && targetRole != "executor" && !(targetRole == "reviewer" && task.Step == "verify") {
			return nil, core.Error("QUEUE_TARGET_FORBIDDEN", "the receiving executor is not authorized to claim this step")
		}
		record, err := p.service.QueueTransferTx(tx, input.TaskID, holder, input.ExpectedVersion, core.QueueInput{Runner: target.ID, RunnerLabel: target.Label, Actor: target.Actor, ActorName: targetName, Scope: input.ToScope, Reason: input.Reason}, principal.Username)
		if err != nil {
			return nil, err
		}
		return queueReceipt(task, &record, "transferred", input.OperationID), nil
	})
}

// listQueue answers: unassigned, queued (by whom) and claimable for a runner.
func (p *portableStore) listQueue(principal standalonePrincipal, runnerID, state, taskID string) (map[string]any, error) {
	if state == "" {
		state = "all"
	}
	if state != "all" && state != "unassigned" && state != "queued" && state != "started" && state != "claimable" {
		return nil, errors.New("QUEUE_INVALID")
	}
	var runner queueRunner
	if runnerID != "" {
		if err := p.service.Store.DB.QueryRow("SELECT r.actor,coalesce(nullif(l.alias,''),r.id) FROM remote_runners r LEFT JOIN remote_runner_labels l ON l.runner_id=r.id WHERE r.id=?", runnerID).Scan(&runner.Actor, &runner.Label); err != nil || !standaloneID.MatchString(runnerID) {
			return nil, errors.New("RUNNER_NOT_FOUND")
		}
		runner.ID = runnerID
	}
	query, args := "SELECT id FROM tasks WHERE status='open' ORDER BY updated DESC,id", []any{}
	if taskID != "" {
		if !standaloneID.MatchString(taskID) {
			return nil, errors.New("QUEUE_INVALID")
		}
		query, args = "SELECT id FROM tasks WHERE id=?", []any{taskID}
	}
	rows, err := p.service.Store.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if taskID != "" && len(ids) == 0 {
		return nil, errors.New("TASK_NOT_FOUND")
	}
	items := []map[string]any{}
	counts := map[string]int{}
	for _, id := range ids {
		next, err := p.service.Next(id)
		if err != nil {
			return nil, err
		}
		summary, err := p.service.Summarize(next)
		if err != nil {
			return nil, err
		}
		item := map[string]any{"task_id": summary.ID, "title": summary.Title, "step": summary.Step, "board": summary.Board, "next_action": summary.Action, "blocking_reasons": summary.Reasons, "assignment": summary.Assignment, "queue": summary.Queue, "runnable": summary.Runnable, "claimable": summary.Claimable, "executors": summary.Executors}
		mine := summary.Claimable
		if runner.ID != "" {
			mine = summary.Runnable && (summary.Assignment == "unassigned" || (summary.Queue != nil && summary.Queue.Permits(runner.ID, runner.Actor)))
			item["claimable_for_runner"] = mine
		}
		if summary.Assignment != "" {
			counts[summary.Assignment]++
		}
		if summary.Claimable {
			counts["claimable"]++
		}
		switch state {
		case "unassigned", "queued", "started":
			if summary.Assignment != state {
				continue
			}
		case "claimable":
			if !mine {
				continue
			}
		}
		items = append(items, item)
	}
	result := map[string]any{"items": items, "counts": counts, "state": state, "authoritative": true}
	if runner.ID != "" {
		result["runner"] = map[string]string{"id": runner.ID, "label": runner.Label}
	}
	return result, nil
}

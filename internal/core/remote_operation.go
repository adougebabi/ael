package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"slices"
)

// RemoteOperation binds an authorized request to a full task-state revision.
// Its receipt is written in the same transaction as the task mutation.
type RemoteOperation struct {
	ID          string
	Key         string
	RequestHash string
	TaskID      string
	Expected    string
	Actor       string
	ReleasePlan string
}

type RemoteOperationReceipt struct {
	OperationID string          `json:"operation_id"`
	Revision    string          `json:"revision"`
	Task        Task            `json:"task"`
	Result      json.RawMessage `json:"result,omitempty"`
	RequestHash string          `json:"-"`
}

type RemotePendingReceipt struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
}

type remotePendingOperation struct {
	OperationID string `json:"operation_id"`
	RequestHash string `json:"request_hash"`
	Actor       string `json:"actor"`
	TaskID      string `json:"task_id"`
	Expected    string `json:"expected"`
	PlanID      string `json:"plan_id"`
}

func RemoteRevision(t Task) string {
	// Artifact availability is a read-time projection, not stored task state.
	t.Evidence = slices.Clone(t.Evidence)
	for i := range t.Evidence {
		t.Evidence[i].ArtifactStatus = nil
	}
	return taskStateHash(t)
}

func (s *Service) WithRemoteOperation(op RemoteOperation) *Service {
	copy := *s
	copy.RemoteAuthority = true
	copy.remoteOperation = &op
	return &copy
}

func remoteOperationKey(key string) string { return "remote-operation:" + store.Hash([]byte(key)) }
func remotePendingKey(key string) string   { return "remote-pending:" + store.Hash([]byte(key)) }
func remotePlanParentKey(id string) string { return "remote-plan-parent:" + store.Hash([]byte(id)) }
func remotePlanTaskKey(id string) string   { return "remote-plan-task:" + store.Hash([]byte(id)) }

func remoteVisibleTask(t Task) Task {
	raw, _ := json.Marshal(t)
	var result Task
	_ = json.Unmarshal(raw, &result)
	result.CandidateWorkspace = ""
	for i := range result.Attempts {
		a := &result.Attempts[i]
		a.Workspace, a.Token, a.Session, a.OwnerIdentity = "", "", "", ""
		a.PID, a.OwnerPID = 0, 0
	}
	return result
}

func RemoteTaskView(t Task) Task { return remoteVisibleTask(t) }

func (s *Service) RemoteReceipt(key, requestHash string) (RemoteOperationReceipt, bool, error) {
	var result RemoteOperationReceipt
	var raw string
	err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", remoteOperationKey(key)).Scan(&raw)
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	var saved struct {
		RequestHash string                 `json:"request_hash"`
		Receipt     RemoteOperationReceipt `json:"receipt"`
	}
	if err = json.Unmarshal([]byte(raw), &saved); err != nil {
		return result, false, err
	}
	if saved.RequestHash != requestHash {
		return result, true, Error("IDEMPOTENCY_CONFLICT", "the key belongs to a different request")
	}
	return saved.Receipt, true, nil
}

func (s *Service) RemoteReceiptByKey(key, actor string, admin bool) (RemoteOperationReceipt, bool, error) {
	var result RemoteOperationReceipt
	var raw string
	err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", remoteOperationKey(key)).Scan(&raw)
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	var saved struct {
		Actor   string                 `json:"actor"`
		Receipt RemoteOperationReceipt `json:"receipt"`
	}
	if err = json.Unmarshal([]byte(raw), &saved); err != nil {
		return result, false, err
	}
	if !admin && saved.Actor != actor {
		return result, false, nil
	}
	return saved.Receipt, true, nil
}

func (s *Service) RemotePendingByKey(key, actor string, admin bool) (RemotePendingReceipt, bool, error) {
	var result RemotePendingReceipt
	var raw string
	err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", remotePendingKey(key)).Scan(&raw)
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	var pending remotePendingOperation
	if err = json.Unmarshal([]byte(raw), &pending); err != nil {
		return result, false, err
	}
	if !admin && pending.Actor != actor {
		return result, false, nil
	}
	return RemotePendingReceipt{OperationID: pending.OperationID, Status: "pending"}, true, nil
}

func (s *Service) remoteBeforeSave(tx *store.Tx, t *Task) error {
	op := s.remoteOperation
	if op == nil {
		return nil
	}
	if op.ID == "" || op.Key == "" || op.RequestHash == "" || op.Expected == "" {
		return Error("REMOTE_OPERATION_REQUIRED", "operation ID, idempotency key and expected revision are required")
	}
	var taskLock string
	lockErr := tx.Row("SELECT body FROM metadata WHERE key=?", remotePlanTaskKey(t.Definition.ID)).Scan(&taskLock)
	if lockErr != nil && lockErr != sql.ErrNoRows {
		return lockErr
	}
	if taskLock != "" && taskLock != op.Key && taskLock != op.ReleasePlan {
		return Error("PLAN_APPLY_IN_PROGRESS", "finish the active plan before modifying its tasks")
	}
	parents := []string{t.Definition.ID, t.Definition.Parent}
	if t.EpicMembership != nil {
		parents = append(parents, t.EpicMembership.Parent)
	}
	for _, parent := range parents {
		if parent == "" {
			continue
		}
		var active string
		err := tx.Row("SELECT body FROM metadata WHERE key=?", remotePlanParentKey(parent)).Scan(&active)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if active != "" && active != op.Key && active != op.ReleasePlan {
			return Error("PLAN_APPLY_IN_PROGRESS", "finish the active plan before modifying its scope")
		}
	}
	if op.TaskID != t.Definition.ID {
		return nil
	}
	var existing string
	err := tx.Row("SELECT body FROM metadata WHERE key=?", remoteOperationKey(op.Key)).Scan(&existing)
	if err == nil {
		var saved struct {
			RequestHash string `json:"request_hash"`
		}
		if json.Unmarshal([]byte(existing), &saved) != nil || saved.RequestHash != op.RequestHash {
			return Error("IDEMPOTENCY_CONFLICT", "the key belongs to a different request")
		}
		return Error("REMOTE_OPERATION_REPLAY", "return the existing receipt")
	}
	if err != sql.ErrNoRows {
		return err
	}
	err = tx.Row("SELECT body FROM metadata WHERE key=?", remotePendingKey(op.Key)).Scan(&existing)
	if err == nil {
		var pending remotePendingOperation
		if json.Unmarshal([]byte(existing), &pending) != nil || pending.RequestHash != op.RequestHash || pending.OperationID != op.ID || pending.Actor != op.Actor || pending.TaskID != op.TaskID || pending.Expected != op.Expected {
			return Error("IDEMPOTENCY_CONFLICT", "the key belongs to an in-progress request")
		}
	} else if err != sql.ErrNoRows {
		return err
	}
	current, err := load(tx, op.TaskID)
	if op.Expected == "absent" {
		if err == nil {
			return Error("REVISION_CONFLICT", "task already exists")
		}
		if err != sql.ErrNoRows {
			return err
		}
		return nil
	}
	if err == sql.ErrNoRows || err == nil && RemoteRevision(current) != op.Expected {
		return Error("REVISION_CONFLICT", "task changed; reload before editing")
	}
	return err
}

func (s *Service) remoteAbandonPending(tx *store.Tx, parent *Task, planID string, result any) error {
	op := s.remoteOperation
	if op == nil {
		if s.RemoteAuthority {
			return Error("REMOTE_OPERATION_REQUIRED", "abandonment requires an authorized remote operation")
		}
		return nil
	}
	if op.ReleasePlan != planID || op.Key == planID {
		return Error("REMOTE_OPERATION_REQUIRED", "use a new operation key to abandon the pending plan")
	}
	var raw string
	if err := tx.Row("SELECT body FROM metadata WHERE key=?", remotePendingKey(planID)).Scan(&raw); err != nil {
		return Error("REMOTE_OPERATION_MISSING", "pending plan operation was not found")
	}
	var pending remotePendingOperation
	if json.Unmarshal([]byte(raw), &pending) != nil || pending.PlanID != planID || pending.TaskID != parent.Definition.ID {
		return Error("REMOTE_OPERATION_MISSING", "pending plan operation does not match")
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	saved := struct {
		RequestHash string                 `json:"request_hash"`
		Actor       string                 `json:"actor"`
		Receipt     RemoteOperationReceipt `json:"receipt"`
	}{RequestHash: pending.RequestHash, Actor: pending.Actor, Receipt: RemoteOperationReceipt{OperationID: pending.OperationID, Revision: RemoteRevision(*parent), Task: remoteVisibleTask(*parent), Result: resultJSON}}
	body, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", remoteOperationKey(planID), string(body)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM metadata WHERE key=?", remotePendingKey(planID)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM metadata WHERE key=? AND body=?", remotePlanParentKey(parent.Definition.ID), planID); err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM metadata WHERE key LIKE 'remote-plan-task:%' AND body=?", planID)
	return err
}

func (s *Service) remoteAfterSave(tx *store.Tx, t *Task) error {
	return s.remoteAfterSaveResult(tx, t, nil)
}

func (s *Service) remoteAfterSaveResult(tx *store.Tx, t *Task, result any) error {
	op := s.remoteOperation
	if op == nil || op.TaskID != t.Definition.ID {
		return nil
	}
	var resultJSON json.RawMessage
	if result != nil {
		var err error
		resultJSON, err = json.Marshal(result)
		if err != nil {
			return err
		}
	}
	item := struct {
		RequestHash string                 `json:"request_hash"`
		Actor       string                 `json:"actor"`
		Receipt     RemoteOperationReceipt `json:"receipt"`
	}{RequestHash: op.RequestHash, Actor: op.Actor, Receipt: RemoteOperationReceipt{OperationID: op.ID, Revision: RemoteRevision(*t), Task: remoteVisibleTask(*t), Result: resultJSON}}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", remoteOperationKey(op.Key), string(raw)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM metadata WHERE key=?", remotePendingKey(op.Key)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM metadata WHERE key=? AND body=?", remotePlanParentKey(op.TaskID), op.Key); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM metadata WHERE key LIKE 'remote-plan-task:%' AND body=?", op.Key); err != nil {
		return err
	}
	return tx.Event(t.Definition.ID, "remote_operation", map[string]string{"operation_id": op.ID, "actor": op.Actor})
}

func (s *Service) remoteReservePlan(tx *store.Tx, parent *Task, planID string, operations []planOperation) error {
	op := s.remoteOperation
	if op == nil {
		return nil
	}
	if op.TaskID != parent.Definition.ID || op.ID != planID || op.Key != planID {
		return Error("REMOTE_OPERATION_REQUIRED", "plan ID must equal operation ID and idempotency key")
	}
	if err := s.remoteBeforeSave(tx, parent); err != nil {
		return err
	}
	pending := remotePendingOperation{OperationID: op.ID, RequestHash: op.RequestHash, Actor: op.Actor, TaskID: op.TaskID, Expected: op.Expected, PlanID: planID}
	raw, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO NOTHING", remotePendingKey(op.Key), string(raw)); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", remotePlanParentKey(op.TaskID), op.Key); err != nil {
		return err
	}
	targets := []string{}
	for _, operation := range operations {
		targets = append(targets, operation.TaskID)
		if operation.Create && operation.Definition.FeedbackOf != "" {
			targets = append(targets, operation.Definition.FeedbackOf)
		}
	}
	for _, target := range targets {
		var active string
		err := tx.Row("SELECT body FROM metadata WHERE key=?", remotePlanTaskKey(target)).Scan(&active)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if active != "" && active != op.Key {
			return Error("PLAN_APPLY_IN_PROGRESS", "a planned task is reserved by another plan")
		}
		active = ""
		err = tx.Row("SELECT body FROM metadata WHERE key=?", remotePlanParentKey(target)).Scan(&active)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if active != "" && active != op.Key {
			return Error("PLAN_APPLY_IN_PROGRESS", "a feedback target is reserved by another plan")
		}
		if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", remotePlanTaskKey(target), op.Key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) remoteCheckPlanReplay(planID string) error {
	op := s.remoteOperation
	if op == nil {
		return nil
	}
	if op.ID != planID || op.Key != planID {
		return Error("REMOTE_OPERATION_REQUIRED", "plan ID must equal operation ID and idempotency key")
	}
	if _, found, err := s.RemoteReceipt(op.Key, op.RequestHash); found || err != nil {
		return err
	}
	var raw string
	if err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", remotePendingKey(op.Key)).Scan(&raw); err != nil {
		return Error("REMOTE_OPERATION_MISSING", "the plan operation is not registered")
	}
	var pending remotePendingOperation
	if json.Unmarshal([]byte(raw), &pending) != nil || pending.RequestHash != op.RequestHash || pending.OperationID != op.ID || pending.Actor != op.Actor || pending.TaskID != op.TaskID || pending.Expected != op.Expected || pending.PlanID != planID {
		return Error("IDEMPOTENCY_CONFLICT", "plan operation does not match the recorded request")
	}
	return nil
}

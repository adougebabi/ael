package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// QueueRecord is a collaboration marker: an external executor has placed the
// task's current pending responsibility in its own queue but has not claimed
// it. It is kept outside the task body, so it never changes the task
// revision, definition, plan, candidate or recorded checks, and it never
// creates an Attempt, a lease or progress.
type QueueRecord struct {
	Task   string `json:"task_id"`
	Runner string `json:"runner"`
	// RunnerLabel and ActorName are display snapshots taken when the record
	// was written; authority always comes from Runner/Actor.
	RunnerLabel string `json:"runner_label"`
	Actor       string `json:"actor"`
	ActorName   string `json:"actor_name"`
	// Scope "runner" lets only Runner claim; "executor" lets any runner owned
	// by the same executor account claim (a Bot with several worker slots).
	Scope              string    `json:"scope"`
	Step               string    `json:"step"`
	DefinitionRevision int       `json:"definition_revision"`
	PlanRevision       int       `json:"plan_revision"`
	Candidate          string    `json:"candidate,omitempty"`
	QueuedAt           time.Time `json:"queued_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Version            string    `json:"version"`
	TransferredFrom    string    `json:"transferred_from,omitempty"`
	Reason             string    `json:"reason,omitempty"`
}

// QueueInput names the queue holder. The transport resolves Actor, ActorName
// and RunnerLabel from the authenticated runner registration.
type QueueInput struct {
	TaskID, ExpectedVersion, Runner, RunnerLabel, Actor, ActorName, Scope, Reason string
}

func remoteQueueKey(id string) string { return "remote-queue:" + id }

// Permits reports whether the claiming runner/executor holds this record.
func (r QueueRecord) Permits(runner, actor string) bool {
	return r.Runner == runner || (r.Scope == "executor" && actor != "" && r.Actor == actor)
}

func (r QueueRecord) label() string {
	if r.RunnerLabel != "" {
		return r.RunnerLabel
	}
	return r.Runner
}

// QueueStale returns "" while the record still describes the task's current
// pending responsibility, otherwise the reason it no longer applies.
func QueueStale(t Task, r QueueRecord) string {
	switch {
	case t.Status == "completed":
		return "completed"
	case t.Status == "cancelled":
		return "cancelled"
	case t.Status != "open":
		return "not_open"
	case t.Paused:
		return "paused"
	}
	for _, a := range t.Attempts {
		if a.Status == "running" || a.Status == "interrupted" {
			return "started"
		}
	}
	switch {
	case t.Step != r.Step:
		return "step_changed"
	case t.Definition.Revision != r.DefinitionRevision:
		return "definition_changed"
	case t.Plan.Revision != r.PlanRevision:
		return "plan_changed"
	case r.Step == "verify" && t.Candidate != r.Candidate:
		return "candidate_changed"
	}
	return ""
}

func queueRecordTx(tx *store.Tx, id string) (*QueueRecord, error) {
	var raw string
	err := tx.Row("SELECT body FROM metadata WHERE key=?", remoteQueueKey(id)).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record QueueRecord
	if err = json.Unmarshal([]byte(raw), &record); err != nil {
		return nil, Error("QUEUE_STORAGE_ERROR", "invalid queue record")
	}
	return &record, nil
}

func putQueueTx(tx *store.Tx, r QueueRecord) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", remoteQueueKey(r.Task), string(raw))
	return err
}

func deleteQueueTx(tx *store.Tx, id string) error {
	_, err := tx.Exec("DELETE FROM metadata WHERE key=?", remoteQueueKey(id))
	return err
}

// reconcileQueueTx runs after every task save: a record that no longer
// describes the current pending responsibility is removed so it can never be
// used to start a different responsibility later.
func reconcileQueueTx(tx *store.Tx, t *Task) error {
	record, err := queueRecordTx(tx, t.Definition.ID)
	if err != nil || record == nil {
		return err
	}
	reason := QueueStale(*t, *record)
	if reason == "" {
		return nil
	}
	if err = deleteQueueTx(tx, t.Definition.ID); err != nil {
		return err
	}
	body := map[string]string{"runner": record.Runner, "runner_label": record.label(), "reason": reason, "version": record.Version}
	if reason == "started" {
		for _, a := range t.Attempts {
			if a.Status == "running" || a.Status == "interrupted" {
				body["started_by"] = a.Executor
			}
		}
	}
	return tx.Event(t.Definition.ID, "remote_queue_cleared", body)
}

// CurrentQueueTx returns the record only while it still applies.
func CurrentQueueTx(tx *store.Tx, t Task) (*QueueRecord, error) {
	record, err := queueRecordTx(tx, t.Definition.ID)
	if err != nil || record == nil || QueueStale(t, *record) != "" {
		return nil, err
	}
	return record, nil
}

// CurrentQueue is the read-only form used by projections.
func (s *Service) CurrentQueue(t Task) (*QueueRecord, error) {
	var raw string
	err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", remoteQueueKey(t.Definition.ID)).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record QueueRecord
	if json.Unmarshal([]byte(raw), &record) != nil || QueueStale(t, record) != "" {
		return nil, nil
	}
	return &record, nil
}

func queueConflict(r *QueueRecord) error {
	if r == nil {
		return Error("QUEUE_VERSION_CONFLICT", "no queue record; read the current queue state")
	}
	return Error("QUEUE_VERSION_CONFLICT", "current version "+r.Version+" held by "+r.Runner)
}

// QueueReservedError explains why a claim was refused.
func QueueReservedError(r QueueRecord) error {
	return Error("QUEUE_RESERVED", "queued by "+r.Runner+" ("+r.label()+") since "+r.QueuedAt.Format(time.RFC3339)+"; not started")
}

func queueStartedOrMissing(t Task, current *QueueRecord) error {
	for _, a := range t.Attempts {
		if a.Status == "running" || a.Status == "interrupted" {
			return Error("QUEUE_STARTED", "the task already has an execution responsibility "+a.ID+"; use the existing handoff/recovery rules")
		}
	}
	return queueConflict(current)
}

// QueueEnqueueTx registers or refreshes the caller's own queue record.
func (s *Service) QueueEnqueueTx(tx *store.Tx, in QueueInput) (QueueRecord, error) {
	var record QueueRecord
	if !s.RemoteAuthority || in.TaskID == "" || in.Runner == "" || in.Actor == "" || in.ExpectedVersion == "" || (in.Scope != "runner" && in.Scope != "executor") {
		return record, Error("QUEUE_INVALID", "task, runner, scope and expected_version are required")
	}
	t, err := load(tx, in.TaskID)
	if err == sql.ErrNoRows {
		return record, Error("TASK_NOT_FOUND", in.TaskID)
	}
	if err != nil {
		return record, err
	}
	stored, err := queueRecordTx(tx, in.TaskID)
	if err != nil {
		return record, err
	}
	var current *QueueRecord
	if stored != nil && QueueStale(t, *stored) == "" {
		current = stored
	}
	if t.Status != "open" || t.Paused {
		return record, Error("NOT_RUNNABLE", "only open, unpaused tasks can be queued")
	}
	if t.Step == "delivery" {
		return record, Error("DELIVERY_MANUAL_REQUIRED", "delivery is not queued for an executor")
	}
	for _, a := range t.Attempts {
		if a.Status == "running" || a.Status == "interrupted" {
			return record, Error("QUEUE_STARTED", "the task already has an execution responsibility "+a.ID)
		}
	}
	var handoff string
	if err = tx.Row("SELECT body FROM metadata WHERE key=?", "remote-recovery-target:"+in.TaskID).Scan(&handoff); err != nil && err != sql.ErrNoRows {
		return record, err
	}
	if handoff != "" {
		var target map[string]string
		if json.Unmarshal([]byte(handoff), &target) != nil || target["runner"] != in.Runner {
			return record, Error("REMOTE_HANDOFF_TARGET", "the task is reserved for an explicit handoff receiver")
		}
	}
	now := time.Now().UTC()
	switch {
	case current == nil && in.ExpectedVersion == "absent":
		record = QueueRecord{QueuedAt: now}
	case current != nil && current.Version == in.ExpectedVersion && current.Runner == in.Runner:
		record = *current // refresh by the same holder (e.g. Bot restart sync)
	case current != nil && current.Runner != in.Runner:
		return record, QueueReservedError(*current)
	default:
		return record, queueConflict(current)
	}
	record.Task, record.Runner, record.RunnerLabel, record.Actor, record.ActorName, record.Scope = in.TaskID, in.Runner, in.RunnerLabel, in.Actor, in.ActorName, in.Scope
	record.Step, record.DefinitionRevision, record.PlanRevision, record.Candidate = t.Step, t.Definition.Revision, t.Plan.Revision, ""
	if t.Step == "verify" {
		record.Candidate = t.Candidate
	}
	record.UpdatedAt, record.Version, record.Reason = now, ID("QV-"), strings.TrimSpace(in.Reason)
	if err = putQueueTx(tx, record); err != nil {
		return record, err
	}
	kind := "remote_queue_added"
	if current != nil {
		kind = "remote_queue_refreshed"
	}
	return record, tx.Event(in.TaskID, kind, map[string]string{"runner": record.Runner, "runner_label": record.label(), "actor": record.ActorName, "step": record.Step, "version": record.Version})
}

// QueueLoadTx returns the task and its applicable record for authorization.
func (s *Service) QueueLoadTx(tx *store.Tx, taskID string) (Task, *QueueRecord, error) {
	t, err := load(tx, taskID)
	if err == sql.ErrNoRows {
		return t, nil, Error("TASK_NOT_FOUND", taskID)
	}
	if err != nil {
		return t, nil, err
	}
	record, err := CurrentQueueTx(tx, t)
	return t, record, err
}

// QueueReleaseTx withdraws a not-yet-started record; the task is untouched.
func (s *Service) QueueReleaseTx(tx *store.Tx, taskID, holder, expectedVersion, reason, by string) (QueueRecord, error) {
	t, current, err := s.QueueLoadTx(tx, taskID)
	if err != nil {
		return QueueRecord{}, err
	}
	if current == nil || current.Version != expectedVersion || current.Runner != holder {
		return QueueRecord{}, queueStartedOrMissing(t, current)
	}
	if err = deleteQueueTx(tx, taskID); err != nil {
		return QueueRecord{}, err
	}
	return *current, tx.Event(taskID, "remote_queue_released", map[string]string{"runner": current.Runner, "runner_label": current.label(), "by": by, "reason": strings.TrimSpace(reason), "version": current.Version})
}

// QueueTransferTx atomically moves a not-yet-started record to another
// executor. The previous holder needs not be online; its stale local queue
// entry is rejected at claim time.
func (s *Service) QueueTransferTx(tx *store.Tx, taskID, holder, expectedVersion string, to QueueInput, by string) (QueueRecord, error) {
	t, current, err := s.QueueLoadTx(tx, taskID)
	if err != nil {
		return QueueRecord{}, err
	}
	if current == nil || current.Version != expectedVersion || current.Runner != holder {
		return QueueRecord{}, queueStartedOrMissing(t, current)
	}
	if to.Runner == "" || to.Actor == "" || (to.Scope != "runner" && to.Scope != "executor") || strings.TrimSpace(to.Reason) == "" {
		return QueueRecord{}, Error("QUEUE_INVALID", "target runner, scope and reason are required")
	}
	if to.Runner == current.Runner {
		return QueueRecord{}, Error("QUEUE_INVALID", "target already holds the queue record")
	}
	var handoff string
	if err = tx.Row("SELECT body FROM metadata WHERE key=?", "remote-recovery-target:"+taskID).Scan(&handoff); err != nil && err != sql.ErrNoRows {
		return QueueRecord{}, err
	}
	if handoff != "" {
		var target map[string]string
		if json.Unmarshal([]byte(handoff), &target) != nil || target["runner"] != to.Runner {
			return QueueRecord{}, Error("REMOTE_HANDOFF_TARGET", "the task is reserved for an explicit handoff receiver")
		}
	}
	now := time.Now().UTC()
	next := *current
	next.Runner, next.RunnerLabel, next.Actor, next.ActorName, next.Scope = to.Runner, to.RunnerLabel, to.Actor, to.ActorName, to.Scope
	next.QueuedAt, next.UpdatedAt, next.Version = now, now, ID("QV-")
	next.TransferredFrom, next.Reason = current.Runner, strings.TrimSpace(to.Reason)
	if err = putQueueTx(tx, next); err != nil {
		return QueueRecord{}, err
	}
	return next, tx.Event(taskID, "remote_queue_transferred", map[string]string{"from": current.Runner, "from_label": current.label(), "to": next.Runner, "to_label": next.label(), "by": by, "reason": next.Reason, "version": next.Version})
}

// queueSummary projects the marker next to, never instead of, the task facts.
func (s *Service) queueSummary(t *Task, r *TaskSummary) error {
	if t.Status != "open" {
		return nil
	}
	record, err := s.CurrentQueue(*t)
	if err != nil {
		return err
	}
	var handoff string
	if err = s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", "remote-recovery-target:"+t.Definition.ID).Scan(&handoff); err != nil && err != sql.ErrNoRows {
		return err
	}
	r.Queue, r.Assignment = record, "unassigned"
	if record != nil {
		r.Assignment = "queued"
	}
	for _, a := range t.Attempts {
		if a.Status == "running" || a.Status == "interrupted" {
			r.Assignment = "started"
		}
	}
	r.Runnable = r.Assignment != "started" && handoff == "" && QueueRunnable(*r)
	r.Claimable = r.Runnable && r.Assignment == "unassigned"
	return nil
}

// QueueRunnable reads the existing next-action facts: dependencies, pauses,
// decisions, plan approval, budget and human-only checks keep their reasons.
func QueueRunnable(r TaskSummary) bool {
	return r.Board == "pending" && (r.Action == "continue" || r.Action == "repair") && r.RecoveryAction == "" && r.Step != "delivery" && len(r.ManualReviews) == 0 && !r.Paused && !r.RequiresTaskDecision
}

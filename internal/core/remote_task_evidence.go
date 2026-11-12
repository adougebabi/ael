package core

import (
	"ael/internal/store"
	"database/sql"
)

// RemoteEvidenceTaskTx reads the exact central task whose already accepted
// evidence will support a finding revalidation. It never claims execution.
func (s *Service) RemoteEvidenceTaskTx(tx *store.Tx, id, expected string) (Task, error) {
	if !s.RemoteAuthority || expected == "" {
		return Task{}, Error("REMOTE_OPERATION_REQUIRED", "an exact central task version is required")
	}
	if err := s.active(); err != nil {
		return Task{}, err
	}
	task, err := load(tx, id)
	if err != nil {
		return task, err
	}
	if RemoteRevision(task) != expected {
		return task, Error("REVISION_CONFLICT", "repair task changed")
	}
	if task.Historical || (task.Status != "open" && task.Status != "completed") || task.Candidate == "" || task.Confirmation != nil {
		return task, Error("FINDING_RETEST_REQUIRED", "repair requires a current accepted candidate")
	}
	if err = s.checkTaskFile(task); err != nil {
		return task, err
	}
	policy, err := s.policyHash()
	if err != nil || policy != task.PolicyHash {
		return task, Error("POLICY_CHANGED", "reconcile the repair task policy")
	}
	keys := []string{remotePlanTaskKey(id), remotePlanParentKey(id), remotePlanParentKey(task.Definition.Parent)}
	if task.EpicMembership != nil {
		keys = append(keys, remotePlanParentKey(task.EpicMembership.Parent))
	}
	for _, key := range keys {
		var active string
		err = tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&active)
		if err != nil && err != sql.ErrNoRows {
			return task, err
		}
		if active != "" {
			return task, Error("PLAN_APPLY_IN_PROGRESS", "finish the active plan before revalidation")
		}
	}
	if s.MatchRemoteCandidate == nil {
		return task, Error("REMOTE_OPERATION_REQUIRED", "central candidate validation is unavailable")
	}
	matches, err := s.MatchRemoteCandidate(tx, &task)
	if err != nil {
		return task, err
	}
	if !matches {
		return task, Error("CANDIDATE_CHANGED", "reconcile the latest reported candidate")
	}
	return task, nil
}

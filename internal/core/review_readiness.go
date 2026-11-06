package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"strings"
)

const lastReviewBlockerEvent = `SELECT kind,body FROM events WHERE task_id=? AND (kind IN ('paused','resumed','review_resumed','definition_revised') OR (kind='submitted' AND json_extract(body,'$.result.status')='blocked')) ORDER BY id DESC LIMIT 1`

// blockedReviewCheck identifies the blocked check from the event which last
// established the pause. Later successful parallel checks do not own it.
func blockedReviewCheck(t *Task, kind, raw string) string {
	if !t.Paused || t.Blocker == nil || t.Blocker.ResumeAt != "verify" || t.Failure != t.Blocker.Reason || kind != "submitted" {
		return ""
	}
	var event struct {
		Attempt string `json:"attempt"`
		Result  struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(raw), &event) != nil || event.Result.Status != "blocked" {
		return ""
	}
	for _, a := range t.Attempts {
		if a.ID != event.Attempt || !strings.HasPrefix(a.Unit, "check:") || a.Status != "blocked" || a.Candidate != t.Candidate || a.DefinitionRevision != t.Definition.Revision || a.PlanRevision != t.Plan.Revision || strings.TrimSpace(a.Reason) != strings.TrimSpace(t.Blocker.Reason) {
			continue
		}
		id := strings.TrimPrefix(a.Unit, "check:")
		for _, c := range t.Definition.Checks {
			if c.ID == id && EffectiveCheckMethod(c) == "human" && len(c.Command) == 0 {
				if evidence := CurrentEvidence(t, c); evidence != nil && evidence.Result == "blocked" && evidence.Reason == t.Blocker.Reason {
					return id
				}
			}
		}
	}
	return ""
}

// ReviewCandidateStatus compares the recorded candidate to the workspace. It
// does not authorize a review; every write still repeats the version checks.
func (s *Service) ReviewCandidateStatus(id string) (map[string]any, error) {
	t, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if t.Candidate == "" {
		return map[string]any{"candidate": "", "matches": false}, nil
	}
	workspace := t.CandidateWorkspace
	if workspace == "" {
		workspace = s.Root
	}
	fingerprint, err := Fingerprint(workspace)
	if err != nil {
		return nil, err
	}
	var kind, raw string
	err = s.Store.DB.QueryRow(lastReviewBlockerEvent, id).Scan(&kind, &raw)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return map[string]any{"candidate": t.Candidate, "matches": fingerprint == t.Candidate, "blocked_check": blockedReviewCheck(&t, kind, raw)}, nil
}

// ResumeBlockedHumanReview resumes only a pause attributable to this exact
// human check. It never records an outcome or passes a gate. The caller must
// explicitly start the review; old writers and unrelated pauses remain blocked.
func (s *Service) ResumeBlockedHumanReview(id, check string, expected ReviewExpectation) (Next, error) {
	if err := s.active(); err != nil {
		return Next{}, err
	}
	n, err := s.Next(id)
	if err != nil {
		return n, err
	}
	if n.Action != "wait_environment" || n.Task.Blocker == nil || len(n.Reasons) != 1 || n.Reasons[0] != n.Task.Blocker.Reason {
		return n, Error("REVIEW_RESUME_NOT_ALLOWED", "resolve the other task blockers first")
	}
	updated := n.Task.Updated
	return s.mutate(id, "review_resumed", func(tx *store.Tx, t *Task) error {
		if !t.Updated.Equal(updated) || t.Status != "open" || t.Step != "verify" || !t.Paused || t.Confirmation != nil || t.Blocker == nil || t.Blocker.ResumeAt != "verify" || t.Failure != t.Blocker.Reason {
			return Error("REVIEW_RESUME_NOT_ALLOWED", "the task or its blocker changed")
		}
		if expected.Candidate == "" || !reviewSnapshotMatches(t, expected) {
			return Error("REVIEW_VERSION_MISMATCH", "review the current candidate and revisions")
		}
		if timeBudgetExhausted(t) {
			return Error("BUDGET_EXHAUSTED", "review the task budget first")
		}
		var selected Check
		for _, c := range t.Definition.Checks {
			if c.ID == check {
				selected = c
			}
		}
		if selected.ID == "" || EffectiveCheckMethod(selected) != "human" || len(selected.Command) != 0 {
			return Error("CHECK_METHOD_MISMATCH", check)
		}
		ev := CurrentEvidence(t, selected)
		if ev == nil || ev.Result != "blocked" || ev.Reason != t.Blocker.Reason {
			return Error("REVIEW_RESUME_NOT_ALLOWED", "this check is not the current blocker")
		}
		var kind, raw string
		if err := tx.Row(lastReviewBlockerEvent, id).Scan(&kind, &raw); err != nil {
			return err
		}
		if blockedReviewCheck(t, kind, raw) != check {
			return Error("REVIEW_RESUME_NOT_ALLOWED", "the blocker is not owned by this check")
		}
		for _, a := range t.Attempts {
			if a.Status == "running" || a.Status == "interrupted" {
				return Error("REVIEW_RESUME_NOT_ALLOWED", "an old execution still needs recovery")
			}
		}
		var locks int
		if err := tx.Row("SELECT count(*) FROM workspace_locks WHERE task=?", id).Scan(&locks); err != nil {
			return err
		}
		if locks != 0 {
			return Error("REVIEW_RESUME_NOT_ALLOWED", "a previous workspace lock still needs recovery")
		}
		workspace := t.CandidateWorkspace
		if workspace == "" {
			workspace = s.Root
		}
		fingerprint, err := Fingerprint(workspace)
		if err != nil {
			return err
		}
		if fingerprint != t.Candidate {
			return Error("CANDIDATE_CHANGED", "the workspace no longer matches the candidate")
		}
		t.Paused = false
		t.Blocker = nil
		return nil
	})
}

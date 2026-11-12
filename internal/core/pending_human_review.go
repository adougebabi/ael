package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// replayHumanNote is evaluated under the same write transaction as the note.
// Replaying a committed request does not reconsider the current task state.
func replayHumanNote(tx *store.Tx, key, hash string) (HumanReviewNote, bool, error) {
	var oldHash, raw string
	err := tx.Row("SELECT hash,response FROM submissions WHERE key=?", key).Scan(&oldHash, &raw)
	if err == sql.ErrNoRows {
		return HumanReviewNote{}, false, nil
	}
	if err != nil {
		return HumanReviewNote{}, false, err
	}
	if oldHash != hash {
		return HumanReviewNote{}, true, Error("IDEMPOTENCY_CONFLICT", key)
	}
	var note HumanReviewNote
	err = json.Unmarshal([]byte(raw), &note)
	return note, true, err
}

func validReviewKey(key string) bool { return strings.TrimSpace(key) != "" && len(key) <= 128 }

func reviewSnapshotMatches(t *Task, expected ReviewExpectation) bool {
	return expected.DefinitionRevision > 0 && expected.PlanRevision > 0 &&
		t.Definition.Revision == expected.DefinitionRevision && t.Plan.Revision == expected.PlanRevision && t.Candidate == expected.Candidate
}

// RecordPendingReview saves the user's observation without claiming a check,
// finishing an attempt, creating evidence, or satisfying any task gate.
func (s *Service) RecordPendingReview(id, check, actor, reason, recovery, result string, refs []string, requestKey string, expected ReviewExpectation) (HumanReviewNote, error) {
	if !validReviewKey(requestKey) {
		return HumanReviewNote{}, Error("IDEMPOTENCY_KEY_REQUIRED", "a unique review request key is required")
	}
	if result != "pass" && result != "fail" && result != "blocked" {
		return HumanReviewNote{}, Error("INVALID_RESULT", "select pass, fail or blocked")
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return HumanReviewNote{}, Error("REVIEW_INPUT_REQUIRED", "reviewer and observation are required")
	}
	request, _ := json.Marshal(struct {
		Task, Check, Actor, Reason, Recovery, Result string
		Artifacts                                    []string
		Expected                                     ReviewExpectation
	}{id, check, actor, reason, recovery, result, refs, expected})
	key, hash := "human-review-note:"+requestKey, store.Hash(request)
	if _, err := s.Get(id); err != nil {
		return HumanReviewNote{}, err
	}
	var note HumanReviewNote
	err := s.Store.Write(func(tx *store.Tx) error {
		var err error
		note, err = s.recordPendingReviewTx(tx, id, check, actor, reason, recovery, result, refs, key, hash, expected, nil)
		return err
	})
	if err == nil {
		err = s.Store.Flush()
	}
	return note, err
}

// ConfirmPendingReview binds a task-level observation to an exact current
// candidate. It remains a human observation and never becomes check evidence.
func (s *Service) ConfirmPendingReview(id, noteID, actor, requestKey string, expected ReviewExpectation) (HumanReviewNote, error) {
	if !validReviewKey(requestKey) {
		return HumanReviewNote{}, Error("IDEMPOTENCY_KEY_REQUIRED", "a unique review request key is required")
	}
	if strings.TrimSpace(actor) == "" {
		return HumanReviewNote{}, Error("REVIEW_INPUT_REQUIRED", "reviewer is required")
	}
	request, _ := json.Marshal(struct {
		Task, Note, Actor string
		Expected          ReviewExpectation
	}{id, noteID, actor, expected})
	key, hash := "human-review-confirm:"+requestKey, store.Hash(request)
	if _, err := s.Get(id); err != nil {
		return HumanReviewNote{}, err
	}
	var note HumanReviewNote
	err := s.Store.Write(func(tx *store.Tx) error {
		var err error
		note, err = s.confirmPendingReviewTx(tx, id, noteID, actor, key, hash, expected, nil)
		return err
	})
	if err == nil {
		err = s.Store.Flush()
	}
	return note, err
}

func (s *Service) recordPendingReviewTx(tx *store.Tx, id, check, actor, reason, recovery, result string, refs []string, key, hash string, expected ReviewExpectation, env *RemoteReviewEnvironment) (HumanReviewNote, error) {
	var note HumanReviewNote
	err := func() error {
		previous, found, err := replayHumanNote(tx, key, hash)
		if found || err != nil {
			note = previous
			return err
		}
		if err = s.active(); err != nil {
			return err
		}
		t, err := load(tx, id)
		if err != nil {
			return err
		}
		if t.Status != "open" {
			return Error("TERMINAL_TASK", id)
		}
		if !reviewSnapshotMatches(&t, expected) {
			return Error("REVIEW_VERSION_MISMATCH", "task candidate or revisions changed; review the current task")
		}
		if check != "" {
			var selected Check
			for _, c := range t.Definition.Checks {
				if c.ID == check {
					selected = c
					break
				}
			}
			if selected.ID == "" {
				return Error("CHECK_MISSING", check)
			}
			if EffectiveCheckMethod(selected) != "human" || len(selected.Command) != 0 {
				return Error("CHECK_METHOD_MISMATCH", "declared check capability cannot be replaced with human review")
			}
		}
		if env != nil {
			if err = env.Validate(tx, &t); err != nil {
				return err
			}
		}

		workspace := s.Root
		if t.Candidate != "" && t.CandidateWorkspace != "" {
			workspace = t.CandidateWorkspace
		}
		hashes := map[string]string{}
		for _, ref := range refs {
			data, err := readReviewArtifact(s, tx, env, workspace, ref)
			if err != nil {
				return err
			}
			hashes[ref] = store.Hash(data)
		}
		note = HumanReviewNote{ID: ID("HR-"), Check: check, Actor: strings.TrimSpace(actor), Result: result, Reason: strings.TrimSpace(reason), Recovery: strings.TrimSpace(recovery), Artifacts: refs, ArtifactHashes: hashes, Candidate: t.Candidate, DefinitionRevision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Status: "pending", RecordedAt: time.Now().UTC()}
		if env != nil {
			note.Comparison = env.Comparison
		}
		kind := "human_review_recorded"
		if check == "" && t.Candidate != "" && env == nil {
			fingerprint, err := Fingerprint(workspace)
			if err != nil {
				return err
			}
			if fingerprint != t.Candidate {
				return Error("CANDIDATE_CHANGED", "the workspace no longer matches the candidate")
			}
			now := note.RecordedAt
			note.Status, note.ConfirmedCandidate, note.ConfirmedAt, note.ConfirmedBy = "confirmed", t.Candidate, &now, note.Actor
			kind = "human_review_confirmed"
		}
		t.HumanReviewNotes = append(t.HumanReviewNotes, note)
		if err = s.save(tx, &t); err != nil {
			return err
		}
		if err = tx.Event(id, kind, note); err != nil {
			return err
		}
		body, err := json.Marshal(note)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO submissions(key,hash,response) VALUES(?,?,?)", key, hash, string(body))
		return err
	}()
	return note, err
}

func (s *Service) confirmPendingReviewTx(tx *store.Tx, id, noteID, actor, key, hash string, expected ReviewExpectation, env *RemoteReviewEnvironment) (HumanReviewNote, error) {
	var note HumanReviewNote
	err := func() error {
		previous, found, err := replayHumanNote(tx, key, hash)
		if found || err != nil {
			note = previous
			return err
		}
		if err = s.active(); err != nil {
			return err
		}
		t, err := load(tx, id)
		if err != nil {
			return err
		}
		if t.Status != "open" {
			return Error("TERMINAL_TASK", id)
		}
		if expected.Candidate == "" || !reviewSnapshotMatches(&t, expected) {
			return Error("REVIEW_VERSION_MISMATCH", "review the current candidate and revisions")
		}
		var selected *HumanReviewNote
		for i := range t.HumanReviewNotes {
			if t.HumanReviewNotes[i].ID == noteID {
				selected = &t.HumanReviewNotes[i]
				break
			}
		}
		if selected == nil {
			return Error("REVIEW_NOTE_MISSING", noteID)
		}
		if selected.Status != "pending" {
			return Error("REVIEW_NOTE_ALREADY_CONFIRMED", noteID)
		}
		if selected.Check != "" {
			return Error("REVIEW_CHECK_CONFIRM_REQUIRED", "confirm this opinion through its human check")
		}
		if selected.Candidate != "" && selected.Candidate != t.Candidate {
			return Error("REVIEW_NOTE_VERSION_MISMATCH", "record a new opinion for the current candidate")
		}
		if selected.DefinitionRevision != t.Definition.Revision || selected.PlanRevision != t.Plan.Revision {
			return Error("REVIEW_NOTE_VERSION_MISMATCH", "record a new opinion for the current definition and plan")
		}
		if env != nil {
			if err = env.Validate(tx, &t); err != nil {
				return err
			}
		}

		workspace := t.CandidateWorkspace
		if workspace == "" {
			workspace = s.Root
		}
		if env == nil {
			fingerprint, err := Fingerprint(workspace)
			if err != nil {
				return err
			}
			if fingerprint != t.Candidate {
				return Error("CANDIDATE_CHANGED", "the workspace no longer matches the candidate")
			}
		}
		for ref, expectedHash := range selected.ArtifactHashes {
			data, err := readReviewArtifact(s, tx, env, workspace, ref)
			if err != nil || store.Hash(data) != expectedHash {
				return Error("REVIEW_EVIDENCE_CHANGED", ref)
			}
		}
		now := time.Now().UTC()
		selected.Status, selected.ConfirmedCandidate, selected.ConfirmedAt, selected.ConfirmedBy = "confirmed", t.Candidate, &now, strings.TrimSpace(actor)
		note = *selected
		if err = s.save(tx, &t); err != nil {
			return err
		}
		if err = tx.Event(id, "human_review_confirmed", note); err != nil {
			return err
		}
		body, err := json.Marshal(note)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO submissions(key,hash,response) VALUES(?,?,?)", key, hash, string(body))
		return err
	}()
	return note, err
}

package core

import (
	"database/sql"
	"encoding/json"
)

// TaskActivity projects submitted outcomes and business state changes.
// Coordination and raw protocol records remain available in Events.
type TaskActivity struct {
	EvidenceChanged *bool    `json:"evidence_changed,omitempty"`
	ID              int      `json:"id"`
	Kind            string   `json:"kind"`
	Summary         string   `json:"summary,omitempty"`
	At              string   `json:"at"`
	Historical      bool     `json:"historical,omitempty"`
	Attempt         string   `json:"attempt,omitempty"`
	Check           string   `json:"check,omitempty"`
	Actor           string   `json:"actor,omitempty"`
	Status          string   `json:"status,omitempty"`
	Next            string   `json:"next,omitempty"`
	Artifacts       []string `json:"artifacts,omitempty"`
}

func (s *Service) TaskActivity(id string) ([]TaskActivity, bool, error) {
	var restoredAt int
	err := s.Store.DB.QueryRow(`SELECT id FROM events WHERE task_id='' AND kind='runtime_restored' ORDER BY id DESC LIMIT 1`).Scan(&restoredAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, false, err
	}
	rows, err := s.Store.DB.Query(`SELECT id,kind,body,created FROM events WHERE (task_id=? AND kind IN ('paused','resumed','review_resumed','cancelled','definition_revised','confirmation','submitted','interrupted','remote_interrupted','remote_responsibility_recovered','human_review_recorded','human_review_confirmed'))
		OR (task_id=? AND kind='work_progress' AND json_extract(body,'$.evidence_changed')=1)
		ORDER BY id DESC LIMIT 101`, id, id)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := []TaskActivity{}
	for rows.Next() {
		var item TaskActivity
		var body string
		if err = rows.Scan(&item.ID, &item.Kind, &body, &item.At); err != nil {
			return nil, false, err
		}
		var event struct {
			EvidenceChanged *bool           `json:"evidence_changed"`
			Attempt         string          `json:"attempt"`
			Failure         string          `json:"failure"`
			Reason          string          `json:"reason"`
			Summary         string          `json:"summary"`
			Actor           string          `json:"actor"`
			Next            string          `json:"next"`
			Artifacts       []string        `json:"artifacts"`
			Result          json.RawMessage `json:"result"`
			Evidence        *Evidence       `json:"evidence"`
		}
		if err = json.Unmarshal([]byte(body), &event); err != nil {
			return nil, false, Error("INVALID_EVENT", "cannot read task activity")
		}
		item.EvidenceChanged = event.EvidenceChanged
		item.Summary, item.Attempt, item.Actor = event.Summary, event.Attempt, event.Actor
		switch item.Kind {
		case "paused":
			item.Summary = event.Failure
		case "human_review_recorded", "human_review_confirmed":
			var note HumanReviewNote
			if err = json.Unmarshal([]byte(body), &note); err != nil {
				return nil, false, Error("INVALID_EVENT", "cannot read human opinion")
			}
			item.Summary, item.Check, item.Artifacts = note.Reason, note.Check, note.Artifacts
			if note.ConfirmedBy != "" {
				item.Actor = note.ConfirmedBy
			}
		case "work_progress":
			item.Next, item.Artifacts = event.Next, event.Artifacts
		case "submitted":
			var result StepResult
			if err = json.Unmarshal(event.Result, &result); err != nil {
				return nil, false, Error("INVALID_EVENT", "cannot read task submission")
			}
			item.Summary, item.Status, item.Artifacts = result.Summary, result.Status, result.Artifacts
			if event.Evidence != nil {
				item.Check, item.Actor = event.Evidence.Check, event.Evidence.Executor
				item.Artifacts = event.Evidence.Artifacts
				if item.Summary == "" {
					item.Summary = event.Evidence.Reason
				}
			}
		default:
			if item.Summary == "" {
				item.Summary = event.Reason
			}
		}
		item.Historical = restoredAt > item.ID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	return items[:min(len(items), 100)], len(items) > 100, nil
}

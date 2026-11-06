package core

import (
	"database/sql"
	"encoding/json"
)

// TaskActivity returns a bounded set of business state changes. Attempt and
// decision details already live on the task; low-level events remain in Events.
type TaskActivity struct {
	ID         int    `json:"id"`
	Kind       string `json:"kind"`
	Summary    string `json:"summary,omitempty"`
	At         string `json:"at"`
	Historical bool   `json:"historical,omitempty"`
}

func (s *Service) TaskActivity(id string) ([]TaskActivity, bool, error) {
	var restoredAt int
	err := s.Store.DB.QueryRow(`SELECT id FROM events WHERE task_id='' AND kind='runtime_restored' ORDER BY id DESC LIMIT 1`).Scan(&restoredAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, false, err
	}
	rows, err := s.Store.DB.Query(`SELECT id,kind,body,created FROM events WHERE task_id=? AND kind IN ('paused','resumed','review_resumed','cancelled','definition_revised','confirmation') ORDER BY id DESC LIMIT 101`, id)
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
			Failure string `json:"failure"`
		}
		if json.Unmarshal([]byte(body), &event) != nil {
			return nil, false, Error("INVALID_EVENT", "cannot read task activity")
		}
		if item.Kind == "paused" {
			item.Summary = event.Failure
		}
		item.Historical = restoredAt > item.ID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	return items[:min(len(items), 100)], len(items) > 100, nil
}

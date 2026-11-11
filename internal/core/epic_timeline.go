package core

import (
	"database/sql"
	"encoding/json"
)

// Scope changes are reconstructed from existing task events. Cancelling a
// required leaf deliberately has a zero delta: it cannot erase required scope.
type EpicScopeEvent struct {
	Task          string `json:"task"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	At            string `json:"at"`
	Required      bool   `json:"required"`
	RequiredDelta int    `json:"required_delta"`
	Reason        string `json:"reason,omitempty"`
	Historical    bool   `json:"historical,omitempty"`
}

type EpicProgress struct {
	Task       string   `json:"task"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	At         string   `json:"at"`
	Status     string   `json:"status,omitempty"`
	Actor      string   `json:"actor,omitempty"`
	Summary    string   `json:"summary"`
	Artifacts  []string `json:"artifacts,omitempty"`
	Next       string   `json:"next,omitempty"`
	Historical bool     `json:"historical,omitempty"`
}

type epicRawEvent struct {
	id                   int
	task, kind, body, at string
}

func (s *Service) epicEvents(id, kinds string, limit int) ([]epicRawEvent, error) {
	query := `SELECT e.id,e.task_id,e.kind,e.body,e.created FROM events e JOIN tasks t ON t.id=e.task_id
		WHERE json_extract(t.body,'$.epic_membership.parent')=? AND e.kind IN (` + kinds + `) ORDER BY e.id`
	if limit > 0 {
		query += ` DESC LIMIT ?`
	}
	var args []any
	args = append(args, id)
	if limit > 0 {
		args = append(args, limit)
	}
	rows, err := s.Store.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []epicRawEvent{}
	for rows.Next() {
		var event epicRawEvent
		if err = rows.Scan(&event.id, &event.task, &event.kind, &event.body, &event.at); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Service) epicTimeline(id string, children []EpicTreeMember) ([]EpicScopeEvent, []EpicProgress, bool, error) {
	var restoredAt int
	err := s.Store.DB.QueryRow(`SELECT id FROM events WHERE task_id='' AND kind='runtime_restored' ORDER BY id DESC LIMIT 1`).Scan(&restoredAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, nil, false, err
	}
	members := make(map[string]EpicTreeMember, len(children))
	for _, member := range children {
		members[member.Task.Task.Definition.ID] = member
	}
	raw, err := s.epicEvents(id, `'created','epic_linked','cancelled'`, 0)
	if err != nil {
		return nil, nil, false, err
	}
	linked := map[string]int{}
	for _, event := range raw {
		if event.kind == "epic_linked" {
			linked[event.task] = event.id
		}
	}
	scope := []EpicScopeEvent{}
	for _, event := range raw {
		member := members[event.task]
		task := member.Task.Task
		if event.kind == "created" && (linked[event.task] != 0 || task.Definition.Parent != id) {
			continue
		}
		if event.kind == "cancelled" && linked[event.task] != 0 && event.id < linked[event.task] {
			continue
		}
		item := EpicScopeEvent{Task: event.task, Title: task.Definition.Title, Kind: event.kind, At: event.at, Required: member.Required, Historical: task.Historical || restoredAt > event.id}
		if event.kind == "created" || event.kind == "epic_linked" {
			item.Kind = "added"
			if member.Required {
				item.RequiredDelta = 1
			}
		} else if task.Status == "cancelled" {
			item.Reason = task.Result
		}
		scope = append(scope, item)
	}
	raw, err = s.epicEvents(id, `'work_progress','submitted'`, 101)
	if err != nil {
		return nil, nil, false, err
	}
	truncated := len(raw) > 100
	if truncated {
		raw = raw[:100]
	}
	progress := []EpicProgress{}
	for _, event := range raw {
		member := members[event.task]
		task := member.Task.Task
		item := EpicProgress{Task: event.task, Title: task.Definition.Title, Kind: event.kind, At: event.at, Historical: task.Historical || restoredAt > event.id}
		if event.kind == "work_progress" {
			var fact Progress
			if err = json.Unmarshal([]byte(event.body), &fact); err != nil {
				return nil, nil, false, Error("INVALID_EVENT", "cannot read epic progress")
			}
			item.Summary, item.Artifacts, item.Next, item.Actor = fact.Summary, fact.Artifacts, fact.Next, fact.Actor
		} else {
			var submission struct {
				Attempt string     `json:"attempt"`
				Result  StepResult `json:"result"`
			}
			if err = json.Unmarshal([]byte(event.body), &submission); err != nil {
				return nil, nil, false, Error("INVALID_EVENT", "cannot read epic submission")
			}
			item.Summary, item.Artifacts, item.Status = submission.Result.Summary, submission.Result.Artifacts, submission.Result.Status
			for _, attempt := range task.Attempts {
				if attempt.ID == submission.Attempt {
					item.Actor = attempt.Executor
					break
				}
			}
		}
		progress = append(progress, item)
	}
	return scope, progress, truncated, nil
}

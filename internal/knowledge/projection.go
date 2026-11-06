package knowledge

import (
	"ael/internal/core"
	"encoding/json"
	"slices"
)

func WithUsage(s *core.Service, q string) ([]Record, error) {
	all, e := Search(s.Root, q)
	if e != nil {
		return nil, e
	}
	byID := map[string]int{}
	for i, r := range all {
		seen := map[string]bool{}
		distinct := make([]string, 0, len(r.Tasks))
		for _, id := range r.Tasks {
			if !seen[id] {
				distinct = append(distinct, id)
				seen[id] = true
			}
		}
		all[i].Tasks = distinct
		byID[r.ID] = i
	}
	rows, e := s.Store.DB.Query("SELECT body FROM tasks")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var t core.Task
		if e = json.Unmarshal([]byte(raw), &t); e != nil {
			return nil, e
		}
		ids := append([]string{}, t.Definition.Knowledge...)
		for _, b := range t.Basis {
			ids = append(ids, b.ID)
		}
		for _, id := range ids {
			i, ok := byID[id]
			if ok && !slices.Contains(all[i].Tasks, t.Definition.ID) {
				all[i].Tasks = append(all[i].Tasks, t.Definition.ID)
			}
		}
	}
	return all, rows.Err()
}
func TaskView(s *core.Service, id string) (any, error) {
	t, e := s.Get(id)
	if e != nil {
		return nil, e
	}
	all, loadErr := ReadAll(s.Root)
	out := []Record{}
	seen := map[string]bool{}
	for _, basis := range t.Basis {
		r, err := ReadVersion(s, basis.ID, basis.Version)
		if err != nil {
			r = Record{ID: basis.ID, Path: basis.Source, Hash: basis.Version, Status: basis.Status, Title: basis.ID, Reason: err.Error()}
		}
		out = append(out, r)
		seen[basis.ID] = true
	}
	for _, r := range all {
		if !seen[r.ID] && inScope(r, t.Definition) {
			out = append(out, r)
		}
	}
	health := Status(s.Root)
	if loadErr != nil {
		health.State = "load_failed"
		health.Reason = loadErr.Error()
	}
	return map[string]any{"records": out, "supplied_versions": t.Basis, "health": health}, nil
}

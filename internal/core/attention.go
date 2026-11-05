package core

import "time"

// Attention is a read-only, bounded scan of open tasks. It never infers a
// result or changes the authoritative task board.
type AttentionItem struct {
	Kind string      `json:"kind"`
	Task TaskSummary `json:"task"`
}

type AttentionPage struct {
	Items     []AttentionItem `json:"items"`
	Truncated bool            `json:"truncated"`
}

func (s *Service) Attention() (AttentionPage, error) {
	page := AttentionPage{Items: []AttentionItem{}}
	queries := []struct {
		kind, clause string
		args         []any
	}{
		{"human", `step='verify' AND COALESCE(json_extract(body,'$.paused'),0)=0 AND COALESCE(json_type(body,'$.confirmation'),'null')!='object' AND EXISTS (SELECT 1 FROM json_each(body,'$.definition.checks') c WHERE COALESCE(json_extract(c.value,'$.method'),'') IN ('','human') AND COALESCE(json_array_length(json_extract(c.value,'$.command')),0)=0)`, nil},
		{"decision", `json_type(body,'$.confirmation')='object'`, nil},
		{"blocked", `(COALESCE(json_extract(body,'$.paused'),0)=1 OR json_type(body,'$.blocker')='object' OR EXISTS (SELECT 1 FROM json_each(body,'$.attempts') a WHERE json_extract(a.value,'$.status')='running' AND json_extract(a.value,'$.lease_until')<=?))`, []any{time.Now().UTC().Format(time.RFC3339Nano)}},
	}
	for _, query := range queries {
		rows, err := s.Store.DB.Query(`SELECT id FROM tasks WHERE status='open' AND `+query.clause+` ORDER BY updated DESC LIMIT 201`, query.args...)
		if err != nil {
			return page, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return page, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, err
		}
		found := false
		for _, id := range ids[:min(len(ids), 200)] {
			n, err := s.Next(id)
			if err != nil {
				return page, err
			}
			summary, err := s.Summarize(n)
			if err != nil {
				return page, err
			}
			if query.kind == "human" && len(summary.ManualReviews) == 0 || query.kind == "decision" && !summary.RequiresTaskDecision || query.kind == "blocked" && summary.Blocker == "" && summary.Action != "wait_environment" {
				continue
			}
			page.Items = append(page.Items, AttentionItem{Kind: query.kind, Task: summary})
			found = true
			break
		}
		if !found && len(ids) > 200 {
			page.Truncated = true
		}
	}
	return page, nil
}

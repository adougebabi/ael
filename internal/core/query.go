package core

import (
	"strings"
	"time"
)

type Query struct {
	Limit   int
	Offset  int
	Offsets map[string]int
	Board   string
	Text    string
	Type    string
	Parent  string
	Days    int
}
type Page struct {
	Items   []Next         `json:"items"`
	Counts  map[string]int `json:"counts"`
	Total   int            `json:"total"`
	Limit   int            `json:"limit_per_board"`
	Offset  int            `json:"offset_per_board"`
	Offsets map[string]int `json:"offsets,omitempty"`
}

// QueryPage bounds detail hydration; status remains derived from runtime facts.
func (s *Service) QueryPage(q Query) (Page, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	p := Page{Items: []Next{}, Counts: map[string]int{}, Limit: q.Limit, Offset: q.Offset, Offsets: q.Offsets}
	now := time.Now().UTC()
	expr := `CASE WHEN status='completed' THEN 'completed' WHEN status='cancelled' THEN 'cancelled' WHEN json_type(body,'$.confirmation')='object' THEN 'confirmation' WHEN EXISTS(SELECT 1 FROM json_each(tasks.body,'$.attempts') a WHERE json_extract(a.value,'$.status')='running' AND json_extract(a.value,'$.lease_until')>?) THEN 'active' ELSE 'pending' END`
	filter := ` WHERE (?='' OR instr(lower(json_extract(body,'$.definition.title')||' '||id||' '||json_extract(body,'$.definition.type')),?)>0) AND (?='' OR json_extract(body,'$.definition.type')=?)`
	args := []any{now.Format(time.RFC3339Nano), strings.ToLower(q.Text), strings.ToLower(q.Text), q.Type, q.Type}
	if q.Parent != "" {
		filter += ` AND json_extract(body,'$.epic_membership.parent')=?`
		args = append(args, q.Parent)
	}
	if q.Days > 0 {
		filter += ` AND (status!='completed' OR (completed!='' AND completed>=?))`
		args = append(args, now.Add(-time.Duration(q.Days)*24*time.Hour).Format(time.RFC3339Nano))
	}
	base := `SELECT id,body,completed,updated,` + expr + ` AS board FROM tasks` + filter
	rows, e := s.Store.DB.Query(`SELECT board,count(*) FROM (`+base+`) GROUP BY board`, args...)
	if e != nil {
		return p, e
	}
	for rows.Next() {
		var board string
		var count int
		if e = rows.Scan(&board, &count); e != nil {
			rows.Close()
			return p, e
		}
		p.Counts[board] = count
		p.Total += count
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, e
	}
	// Collect a single cohort before Next hydrates definitions and constraints.
	// Concurrent transitions may move an ID between selections, but must never
	// duplicate it in the returned authoritative task projection.
	selected := []string{}
	seen := map[string]bool{}
	for _, board := range []string{"active", "pending", "confirmation", "completed", "cancelled"} {
		if q.Board != "" && q.Board != board {
			continue
		}
		pageOffset := q.Offset
		if offset, ok := q.Offsets[board]; ok {
			pageOffset = max(0, offset)
		}
		pageArgs := append(append([]any{}, args...), board, q.Limit, pageOffset)
		rows, e = s.Store.DB.Query(`SELECT id FROM (`+base+`) WHERE board=? ORDER BY completed DESC,updated DESC LIMIT ? OFFSET ?`, pageArgs...)
		if e != nil {
			return p, e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return p, e
			}
			if !seen[id] {
				seen[id] = true
				selected = append(selected, id)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return p, e
		}
	}
	for _, id := range selected {
		n, e := s.Next(id)
		if e != nil {
			return p, e
		}
		p.Items = append(p.Items, n)
	}
	return p, nil
}

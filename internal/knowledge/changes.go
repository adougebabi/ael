package knowledge

import (
	"ael/internal/core"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

type ChangeReference struct {
	TaskID string `json:"task_id,omitempty"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
}
type Change struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Kind         string            `json:"kind"`
	Reason       string            `json:"reason"`
	Content      string            `json:"content"`
	Result       string            `json:"result"`
	At           *time.Time        `json:"occurred_at"`
	Scopes       []string          `json:"scopes"`
	References   []ChangeReference `json:"references"`
	Artifacts    []string          `json:"artifacts"`
	SupersededBy string            `json:"superseded_by,omitempty"`
	Historical   bool              `json:"historical"`
	SourceAt     *time.Time        `json:"source_occurred_at"`
	TimeBasis    string            `json:"time_basis"`
	Facts        []ChangeFact      `json:"delivery_facts"`
}
type ChangeFact struct {
	Candidate string     `json:"candidate_id,omitempty"`
	Finding   string     `json:"finding_id,omitempty"`
	Task      string     `json:"task"`
	Title     string     `json:"title"`
	Result    string     `json:"result"`
	At        *time.Time `json:"occurred_at"`
	Content   string     `json:"content"`
	Artifacts []string   `json:"artifacts"`
}
type ChangePage struct {
	Items  []Change `json:"items"`
	Total  int      `json:"total"`
	Scopes []string `json:"scopes"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

func inDateRange(at *time.Time, from, to string) bool {
	if at == nil {
		return false
	}
	if bound, e := time.Parse(time.RFC3339Nano, from); e == nil {
		if at.Before(bound) {
			return false
		}
	} else if from != "" && at.Format("2006-01-02") < from {
		return false
	}
	if bound, e := time.Parse(time.RFC3339Nano, to); e == nil {
		if at.After(bound) {
			return false
		}
	} else if to != "" && at.Format("2006-01-02") > to {
		return false
	}
	return true
}

// Changes is computed from source records and task results, without storing a
// competing evolution state. Only explicit references group records together.
func Changes(s *core.Service, q BrowseQuery, kind, from, to string) (ChangePage, error) {
	records, err := ReadAll(s.Root)
	if err != nil {
		return ChangePage{}, err
	}
	return ChangesWithRecords(s, records, q, kind, from, to)
}

// ChangesWithRecords projects the same task facts against central snapshots.
func ChangesWithRecords(s *core.Service, all []Record, q BrowseQuery, kind, from, to string) (ChangePage, error) {
	p := ChangePage{Items: []Change{}, Scopes: []string{}, Limit: q.Limit, Offset: q.Offset}
	if p.Limit <= 0 || p.Limit > 100 {
		p.Limit = 25
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	rows, e := s.Store.DB.Query("SELECT body FROM tasks ORDER BY id")
	if e != nil {
		return p, e
	}
	tasks := map[string]core.Task{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return p, e
		}
		var t core.Task
		if e = json.Unmarshal([]byte(raw), &t); e != nil {
			rows.Close()
			return p, e
		}
		tasks[t.Definition.ID] = t
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, e
	}
	changes := map[string]*Change{}
	for id, t := range tasks {
		if t.Historical && (t.Result == "历史完成 · 导入未复验" || strings.HasPrefix(t.Result, "historical")) {
			continue
		}
		c := &Change{ID: "task:" + id, Title: t.Definition.Title, Kind: "delivery", Reason: t.Definition.OriginalRequest, Scopes: t.Definition.Scope, References: []ChangeReference{{ID: id, Title: t.Definition.Title, Kind: "task"}}, Artifacts: []string{}, Historical: t.Historical}
		var execution *core.Attempt
		for i := len(t.Attempts) - 1; i >= 0; i-- {
			a := &t.Attempts[i]
			if a.Unit == "execute" && a.Status == "pass" && a.Candidate == t.Candidate && a.DefinitionRevision == t.Definition.Revision && a.PlanRevision == t.Plan.Revision {
				execution = a
				break
			}
		}
		if c.Reason == "" {
			c.Reason = "未记录原因"
		}
		if t.Status == "completed" && t.Result != "" {
			c.Result = t.Result
			c.At = t.Completed
		} else if !t.Historical && t.Status == "open" && t.Definition.Intent != "inspect" && t.Candidate != "" {
			if execution != nil && len(execution.Outputs) > 0 {
				c.Result = "implemented"
				c.At = execution.Ended
			}
		}
		if c.Result == "" {
			continue
		}
		// Released is a claim supported by the exact successful receipt, not a goal.
		if (c.Result == "released" || c.Result == "merged") && (t.Delivery == nil || t.Delivery.Status != "success" || t.Delivery.Candidate != t.Candidate) {
			c.Result = "delivery_unconfirmed"
		}
		if execution != nil {
			c.Content = execution.Reason
			c.Artifacts = append(c.Artifacts, execution.Outputs...)
		}
		c.TimeBasis = "task_result"
		if c.Content == "" {
			c.Content = t.Result
		}
		changes[c.ID] = c
	}
	if err := findingChangeFacts(s, tasks, changes); err != nil {
		return p, err
	}
	consumed := map[string]string{}
	// An explicit, described record that lists several tasks defines a single
	// change. Merely sharing a common spec, path or date never merges tasks.
	for _, r := range all {
		eligible := (r.Kind == "decision" && slices.Contains([]string{"current", "accepted", "proposed", "rejected", "superseded"}, r.Status)) || (r.Kind == "spec" && r.Summary != "" && (r.Status == "current" || r.Status == "accepted" || r.Status == "superseded"))
		if !eligible {
			continue
		}
		result := map[string]string{"current": "effective", "accepted": "decided", "proposed": "proposed", "rejected": "rejected", "superseded": "superseded", "historical": "historical", "conflict": "conflict"}[r.Status]
		c := &Change{ID: "knowledge:" + r.ID, Title: Title(r), Kind: r.Kind, Reason: r.Rationale, Content: r.Summary, Result: result, Scopes: r.Paths, References: []ChangeReference{{ID: r.ID, Title: Title(r), Kind: r.Kind}}, Artifacts: []string{}, SupersededBy: r.SupersededBy, Historical: r.Status == "historical"}
		if c.Reason == "" {
			c.Reason = "未记录原因"
		}
		if c.Content == "" {
			c.Content = Excerpt(r.Body)
		}
		if at, err := time.Parse(time.RFC3339, r.OccurredAt); err == nil {
			c.At = &at
			c.SourceAt = &at
		}
		c.TimeBasis = "source_record"
		if len(r.Tasks) > 1 && r.Summary != "" {
			for _, id := range r.Tasks {
				if t, ok := tasks[id]; ok {
					c.References = append(c.References, ChangeReference{ID: id, Title: t.Definition.Title, Kind: "task"})
					if changes["task:"+id] != nil && consumed[id] == "" {
						consumed[id] = c.ID
					}
				}
			}
		}
		changes[c.ID] = c
	}
	for id, owner := range consumed {
		child := changes["task:"+id]
		if child != nil {
			parent := changes[owner]
			parent.Artifacts = append(parent.Artifacts, child.Artifacts...)
			parent.Facts = append(parent.Facts, ChangeFact{Task: id, Title: child.Title, Result: child.Result, At: child.At, Content: child.Content, Artifacts: child.Artifacts})
			parent.Facts = append(parent.Facts, child.Facts...)
			for _, reference := range child.References {
				if !slices.ContainsFunc(parent.References, func(item ChangeReference) bool { return item.ID == reference.ID && item.Kind == reference.Kind }) {
					parent.References = append(parent.References, reference)
				}
			}
			for _, scope := range child.Scopes {
				if !slices.Contains(parent.Scopes, scope) {
					parent.Scopes = append(parent.Scopes, scope)
				}
			}
			if child.At != nil && (parent.At == nil || child.At.After(*parent.At)) {
				parent.At = child.At
				parent.TimeBasis = "latest_delivery"
			}
			delete(changes, "task:"+id)
		}
	}
	for _, r := range all {
		for id, t := range tasks {
			linked := slices.Contains(r.Tasks, id) || slices.Contains(t.Definition.Knowledge, r.ID)
			for _, b := range t.Basis {
				if b.ID == r.ID {
					linked = true
				}
			}
			if !linked {
				continue
			}
			if c := changes["knowledge:"+r.ID]; c != nil {
				found := false
				for _, ref := range c.References {
					if ref.ID == id && ref.Kind == "task" {
						found = true
					}
				}
				if !found {
					c.References = append(c.References, ChangeReference{ID: id, Title: t.Definition.Title, Kind: "task"})
				}
			}
			key := "task:" + id
			if consumed[id] != "" {
				key = consumed[id]
			}
			if c := changes[key]; c != nil {
				found := false
				for _, ref := range c.References {
					if ref.ID == r.ID && ref.Kind != "task" {
						found = true
					}
				}
				if !found {
					c.References = append(c.References, ChangeReference{ID: r.ID, Title: Title(r), Kind: r.Kind})
				}
			}
		}
	}
	list := []Change{}
	scopes := map[string]bool{}
	for _, c := range changes {
		for _, x := range c.Scopes {
			scopes[x] = true
		}
		if q.Text != "" && !strings.Contains(strings.ToLower(c.Title+" "+c.Content+" "+c.Reason), strings.ToLower(q.Text)) {
			continue
		}
		if kind != "" && c.Kind != kind {
			continue
		}
		if q.Scope != "" && !slices.Contains(c.Scopes, q.Scope) {
			continue
		}
		if from != "" || to != "" {
			dates := []*time.Time{c.At, c.SourceAt}
			for _, f := range c.Facts {
				dates = append(dates, f.At)
			}
			match := false
			for _, at := range dates {
				if inDateRange(at, from, to) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		slices.SortFunc(c.Facts, func(a, b ChangeFact) int { return strings.Compare(a.Task, b.Task) })
		list = append(list, *c)
	}
	slices.SortFunc(list, func(a, b Change) int {
		if a.At == nil && b.At == nil {
			return strings.Compare(a.ID, b.ID)
		}
		if a.At == nil {
			return 1
		}
		if b.At == nil {
			return -1
		}
		if compare := b.At.Compare(*a.At); compare != 0 {
			return compare
		}
		return strings.Compare(a.ID, b.ID)
	})
	p.Total = len(list)
	end := min(p.Offset+p.Limit, p.Total)
	if p.Offset < end {
		p.Items = list[p.Offset:end]
	}
	for x := range scopes {
		p.Scopes = append(p.Scopes, x)
	}
	slices.Sort(p.Scopes)
	return p, nil
}

package core

import (
	"slices"
	"strings"
	"time"
)

// Workbench is a read-only reading view, never a replacement for Board or Next.
// Relationships are resolved over the full project before filtering or paging.
type WorkbenchRow struct {
	Required          int         `json:"required"`
	Satisfied         int         `json:"satisfied"`
	Optional          int         `json:"optional"`
	Related           int         `json:"related"`
	RequiredCancelled int         `json:"required_cancelled"`
	Task              TaskSummary `json:"task"`
	Purpose           string      `json:"purpose"`
	Path              []string    `json:"path"`
	Relation          string      `json:"relation,omitempty"`
	Issues            []string    `json:"issues"`
	Children          int         `json:"children"`
	Remaining         int         `json:"remaining"`
	Completed         int         `json:"completed"`
	Cancelled         int         `json:"cancelled"`
	Matches           int         `json:"matches"`
	// Unfinished descendants by reading state; the parent's own state and
	// gates stay in Task and are never inferred from these counts.
	ActiveChildren  int `json:"active_children"`
	ReviewChildren  int `json:"review_children"`
	BlockedChildren int `json:"blocked_children"`
}
type WorkbenchFamily struct {
	Direct      int  `json:"direct"`
	Descendants int  `json:"descendants"`
	Remaining   int  `json:"remaining"`
	Completed   int  `json:"completed"`
	Cancelled   int  `json:"cancelled"`
	Required    int  `json:"required"`
	Satisfied   int  `json:"satisfied"`
	Formal      bool `json:"formal"`
	// Same reading counts as WorkbenchRow, over unfinished descendants.
	ActiveChildren  int `json:"active_children"`
	ReviewChildren  int `json:"review_children"`
	BlockedChildren int `json:"blocked_children"`
}
type WorkbenchPage struct {
	Family          *WorkbenchFamily `json:"family,omitempty"`
	Items           []WorkbenchRow   `json:"items"`
	Total           int              `json:"total"`
	ProjectTotal    int              `json:"project_total"`
	ProjectChildren int              `json:"project_children"`
	Unfinished      int              `json:"unfinished"`
	MatchedChildren int              `json:"matched_children"`
	Anomalies       int              `json:"anomalies"`
	Limit           int              `json:"limit"`
	Offset          int              `json:"offset"`
	Counts          map[string]int   `json:"counts"`
	// Assignees: runners holding a queue record or a running responsibility
	// on unfinished work, for the assignment filter.
	Assignees []string `json:"assignees,omitempty"`
}

func workbenchMatch(n TaskSummary, view string) bool {
	switch view {
	case "", "unfinished":
		return n.Board != "completed" && n.Board != "cancelled"
	case "all", "issues":
		return true
	case "human":
		return len(n.ManualReviews) > 0
	case "review":
		// Waiting for a person: a current human check or a recorded decision.
		return len(n.ManualReviews) > 0 || n.Board == "confirmation"
	case "blocked":
		return n.Action == "wait_environment" || n.Action == "wait_dependency" || n.Blocker != "" || n.Checks.Blocked > 0
	case "repair":
		return n.Action == "repair" || n.Checks.Failed > 0
	default:
		return n.Board == view
	}
}

func (s *Service) Workbench(q Query) (WorkbenchPage, error) {
	p := WorkbenchPage{Items: []WorkbenchRow{}, Counts: map[string]int{}, Limit: min(100, max(1, q.Limit)), Offset: max(0, q.Offset)}
	if q.Limit <= 0 {
		p.Limit = 20
	}
	rows, err := s.Store.DB.Query("SELECT id FROM tasks ORDER BY CASE WHEN completed='' THEN 0 ELSE 1 END,completed DESC,updated DESC,id")
	if err != nil {
		return p, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return p, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	facts := map[string]WorkbenchRow{}
	tasks := map[string]Task{}
	resolveBasis, constraintCheck := s.ResolveBasis, s.ConstraintCheck
	if s.SnapshotConstraints != nil {
		resolvers := s.SnapshotConstraints()
		resolveBasis, constraintCheck = resolvers.ResolveBasis, resolvers.Check
	}
	parents := map[string]string{}
	for _, id := range ids {
		n, e := s.nextWithConstraints(id, false, resolveBasis, constraintCheck)
		flowIssue := ""
		if e != nil {
			task, readErr := s.Get(id)
			if readErr != nil {
				return p, readErr
			}
			n = task.BaseNext()
			flowIssue = "流程依据读取失败，状态待核对：" + e.Error()
		}
		tasks[id] = *n.Task
		// The tree is aggregated once below; avoid recursively hydrating each epic.
		summary, e := s.summarize(n, false)
		if e != nil {
			return p, e
		}
		purpose := n.Task.Definition.OriginalRequest
		if purpose == "" {
			purpose = strings.Join(n.Task.Definition.Acceptance, "；")
		}
		r := WorkbenchRow{Task: summary, Purpose: purpose, Path: []string{}, Issues: []string{}}
		if flowIssue != "" {
			r.Issues = append(r.Issues, flowIssue)
		}
		if purpose == "" {
			r.Issues = append(r.Issues, "原目标或需求未记录")
		}
		if summary.Parent != "" {
			parents[id] = summary.Parent
			r.Relation = "原定义关联，非必需集成成员"
			if n.Task.EpicMembership != nil {
				if n.Task.EpicMembership.Required {
					r.Relation = "必需子任务"
				} else {
					r.Relation = "可选子任务"
				}
			}
		}
		if n.Task.Status != "open" && n.Task.Status != "completed" && n.Task.Status != "cancelled" {
			r.Issues = append(r.Issues, "状态待核对："+n.Task.Status)
		}
		facts[id] = r
	}
	roots := map[string]string{}
	for _, id := range ids {
		cur := id
		seen := map[string]bool{id: true}
		path := []string{}
		r := facts[id]
		for parents[cur] != "" {
			parent := parents[cur]
			ancestor, ok := facts[parent]
			if !ok {
				r.Issues = append(r.Issues, "父事项缺失："+parent)
				break
			}
			if seen[parent] {
				r.Issues = append(r.Issues, "父子关系循环，保留独立入口")
				cur = id
				path = []string{}
				break
			}
			seen[parent] = true
			path = append([]string{parent}, path...)
			if (ancestor.Task.Board == "completed" || ancestor.Task.Board == "cancelled") && workbenchMatch(r.Task, "") {
				r.Issues = append(r.Issues, "父事项已结束，本项仍未完成")
			}
			cur = parent
		}
		r.Path = path
		facts[id] = r
		roots[id] = cur
	}
	if parent, ok := tasks[q.Parent]; ok {
		family := &WorkbenchFamily{Formal: parent.Definition.Type == "epic"}
		for _, id := range ids {
			if parents[id] == q.Parent {
				family.Direct++
			}
			if !slices.Contains(facts[id].Path, q.Parent) {
				continue
			}
			family.Descendants++
			switch facts[id].Task.Board {
			case "completed":
				family.Completed++
			case "cancelled":
				family.Cancelled++
			default:
				family.Remaining++
				if workbenchMatch(facts[id].Task, "active") {
					family.ActiveChildren++
				}
				if workbenchMatch(facts[id].Task, "review") {
					family.ReviewChildren++
				}
				if workbenchMatch(facts[id].Task, "blocked") {
					family.BlockedChildren++
				}
			}
			member := tasks[id]
			if family.Formal && member.EpicMembership != nil && member.EpicMembership.Parent == q.Parent && member.EpicMembership.Required {
				family.Required++
				if acceptedEpicLeaf(parent, member) {
					family.Satisfied++
				}
			}
		}
		p.Family = family
	}
	groups := map[string][]string{}
	order := []string{}
	for _, id := range ids {
		root := roots[id]
		if _, ok := groups[root]; !ok {
			order = append(order, root)
		}
		groups[root] = append(groups[root], id)
		if len(facts[id].Issues) > 0 {
			p.Anomalies++
		}
	}
	p.ProjectTotal = len(groups)
	p.ProjectChildren = len(ids) - len(groups)
	selected := []WorkbenchRow{}
	views := []string{"unfinished", "active", "pending", "confirmation", "review", "completed", "cancelled", "human", "blocked", "repair", "all", "issues"}
	for _, root := range order {
		row := facts[root]
		members := groups[root]
		matchIDs := []string{}
		hasOpen := false
		hits := map[string]bool{}
		for _, id := range members {
			f := facts[id]
			if workbenchMatch(f.Task, "") {
				hasOpen = true
			}
			for _, view := range views {
				if workbenchMatch(f.Task, view) && (view != "issues" || len(f.Issues) > 0) {
					hits[view] = true
				}
			}
			if id != root {
				row.Children++
				member := tasks[id]
				if member.EpicMembership != nil && member.EpicMembership.Parent == root {
					if member.EpicMembership.Required {
						row.Required++
						if acceptedEpicLeaf(tasks[root], member) {
							row.Satisfied++
						}
						if member.Status == "cancelled" {
							row.RequiredCancelled++
						}
					} else {
						row.Optional++
					}
				} else {
					row.Related++
				}
				if f.Task.Board == "completed" {
					row.Completed++
				} else if f.Task.Board == "cancelled" {
					row.Cancelled++
				} else {
					row.Remaining++
					if workbenchMatch(f.Task, "active") {
						row.ActiveChildren++
					}
					if workbenchMatch(f.Task, "review") {
						row.ReviewChildren++
					}
					if workbenchMatch(f.Task, "blocked") {
						row.BlockedChildren++
					}
				}
			}
			text := strings.ToLower(f.Task.Title + " " + f.Task.ID + " " + f.Purpose)
			for _, runner := range append(slices.Clone(f.Task.Executors), queueHolder(f.Task)) {
				if runner != "" && !slices.Contains(p.Assignees, runner) && workbenchMatch(f.Task, "") {
					p.Assignees = append(p.Assignees, runner)
				}
			}
			if !assignMatch(f.Task, q.Assign) {
				continue
			}
			if !workbenchMatch(f.Task, q.Board) || (q.Board == "issues" && len(f.Issues) == 0) || (q.Type != "" && f.Task.Type != q.Type) || (q.Text != "" && !strings.Contains(text, strings.ToLower(q.Text))) {
				continue
			}
			if q.Days > 0 && f.Task.Board == "completed" && (f.Task.Completed == nil || f.Task.Completed.Before(time.Now().Add(-time.Duration(q.Days)*24*time.Hour))) {
				continue
			}
			matchIDs = append(matchIDs, id)
		}
		if hasOpen {
			p.Unfinished++
		}
		for view := range hits {
			p.Counts[view]++
		}
		if q.Parent != "" {
			if root != roots[q.Parent] {
				continue
			}
			for _, id := range matchIDs {
				if id == q.Parent || !slices.Contains(facts[id].Path, q.Parent) {
					continue
				}
				selected = append(selected, facts[id])
				p.MatchedChildren++
			}
			continue
		}
		if len(matchIDs) == 0 {
			continue
		}
		for _, id := range matchIDs {
			if id != root {
				row.Matches++
				p.MatchedChildren++
			}
		}
		selected = append(selected, row)
	}
	p.Total = len(selected)
	start := min(p.Offset, p.Total)
	end := min(start+p.Limit, p.Total)
	p.Items = selected[start:end]
	return p, nil
}

func queueHolder(t TaskSummary) string {
	if t.Queue == nil {
		return ""
	}
	return t.Queue.Runner
}

func assignMatch(t TaskSummary, assign string) bool {
	switch {
	case assign == "":
		return true
	case assign == "unassigned" || assign == "queued":
		return t.Assignment == assign
	case assign == "claimable":
		return t.Claimable
	case strings.HasPrefix(assign, "runner:"):
		runner := strings.TrimPrefix(assign, "runner:")
		return queueHolder(t) == runner || slices.Contains(t.Executors, runner)
	}
	return true
}

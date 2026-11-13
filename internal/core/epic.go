package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"strings"
)

// EpicTree is a read-only projection of actual leaf states. Completion of the
// children does not itself complete the parent or claim integration acceptance.
type EpicTree struct {
	Root              Next             `json:"root"`
	Children          []EpicTreeMember `json:"children"`
	Required          int              `json:"required"`
	RequiredCompleted int              `json:"required_completed"`
	Summary           EpicSummary      `json:"summary"`
	ScopeChanges      []EpicScopeEvent `json:"scope_changes"`
	Progress          []EpicProgress   `json:"progress"`
	ProgressTruncated bool             `json:"progress_truncated"`
	ReportedProgress  []EpicProgress   `json:"reported_progress"`
	ReportsTruncated  bool             `json:"reports_truncated"`
}

type EpicTreeMember struct {
	Required  bool        `json:"required"`
	Milestone string      `json:"milestone,omitempty"`
	Task      Next        `json:"task"`
	Summary   TaskSummary `json:"summary"`
}

// These counts describe distinct leaves. The categories intentionally overlap:
// for example an implemented leaf may still be waiting for acceptance.
type EpicSummary struct {
	Total          int         `json:"total"`
	Required       int         `json:"required"`
	Implemented    int         `json:"implemented"`
	Accepted       int         `json:"accepted"`
	Integrated     int         `json:"integrated"`
	Delivered      int         `json:"delivered"`
	NotStarted     int         `json:"not_started"`
	PartialChecks  int         `json:"partial_checks"`
	Failed         int         `json:"failed"`
	Blocked        int         `json:"blocked"`
	WaitingHandoff int         `json:"waiting_handoff"`
	Cancelled      int         `json:"cancelled"`
	Issues         []EpicIssue `json:"issues"`
}

type EpicIssue struct {
	Task   string `json:"task"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Check  string `json:"check,omitempty"`
	Reason string `json:"reason"`
}

func (summary *EpicSummary) add(n Next, view TaskSummary, required bool) {
	t := n.Task
	summary.Total++
	if required {
		summary.Required++
	}
	if t.Status == "cancelled" {
		summary.Cancelled++
	} else if t.Status == "open" && !t.Historical && len(t.Attempts) == 0 && (t.History == nil || len(t.History.Attempts) == 0) {
		summary.NotStarted++
	}
	if !t.Historical && t.Status != "cancelled" && t.Candidate != "" {
		summary.Implemented++
		if gates(t) {
			summary.Accepted++
		}
		if t.Delivery != nil && t.Delivery.Status == "success" && t.Delivery.Candidate == t.Candidate {
			summary.Delivered++
		}
	}
	if !t.Historical && t.Status != "cancelled" && view.Checks.RequiredPassed > 0 && view.Checks.RequiredPassed < view.Checks.Required {
		summary.PartialChecks++
	}
	failed := !t.Historical && t.Status != "cancelled" && view.Checks.Failed > 0
	if t.Status == "open" && t.Route == "repair" {
		failed = true
	}
	if t.Status == "open" && len(t.Attempts) > 0 {
		last := t.Attempts[len(t.Attempts)-1]
		if last.Status == "fail" && !strings.HasPrefix(last.Unit, "check:") {
			failed = true
		}
	}
	if failed {
		summary.Failed++
	}
	if t.Status == "open" && (t.Paused || n.Action == "wait_dependency" || n.Action == "wait_environment") {
		summary.Blocked++
	}
	if t.Status == "open" && (n.Action == "continue" || n.Action == "repair") && view.Running == 0 && !view.RequiresTaskDecision && len(view.ManualReviews) == 0 {
		summary.WaitingHandoff++
	}
	issue := EpicIssue{Task: t.Definition.ID, Title: t.Definition.Title}
	switch {
	case t.Status == "cancelled" && required:
		issue.Kind, issue.Reason = "cancelled", "必要子任务已取消，范围仍未满足"
		if t.Result != "" {
			issue.Reason += "：" + t.Result
		}
	case t.Historical && required:
		issue.Kind, issue.Reason = "historical", "历史结果尚未在当前工作区复验"
	case failed:
		issue.Kind, issue.Reason = "failed", t.Failure
		var latest *Evidence
		for _, check := range t.Definition.Checks {
			if evidence := CurrentEvidence(t, check); evidence != nil && evidence.Result == "fail" && (latest == nil || evidence.At.After(latest.At)) {
				latest = evidence
			}
		}
		if latest == nil && t.Route == "repair" {
			for _, evidence := range append(append([]Evidence{}, t.EvidenceHistory...), t.Evidence...) {
				if evidence.Result != "fail" || (latest != nil && !evidence.At.After(latest.At)) {
					continue
				}
				for _, check := range t.Definition.Checks {
					if evidence.Check == check.ID {
						copy := evidence
						latest = &copy
						break
					}
				}
			}
		}
		if latest != nil {
			issue.Check = latest.Check
			if latest.Reason != "" {
				issue.Reason = latest.Reason
			}
		}
	case t.Status == "open" && t.Blocker != nil:
		issue.Kind, issue.Reason = "blocked", t.Blocker.Reason
	case t.Status == "open" && len(view.ManualReviews) > 0 && view.Running == 0:
		issue.Kind, issue.Check, issue.Reason = "human_review", view.ManualReviews[0].Check, "等待人工验收"
	case t.Status == "open" && len(n.Reasons) > 0:
		issue.Kind, issue.Reason = "waiting", n.Reasons[0]
	case t.Status == "open" && n.Health != nil && n.Health.NoProgress && !n.Health.HealthyLong:
		issue.Kind, issue.Reason = "no_progress", "超过设定时间未记录新的证据进展"
	}
	if issue.Reason == "" {
		return
	}
	summary.Issues = append(summary.Issues, issue)
}

func validateEpicParent(tx *store.Tx, parent, child string) error {
	if parent == child {
		return Error("PARENT_CYCLE", child)
	}
	t, err := load(tx, parent)
	if err != nil {
		return Error("EPIC_MISSING", parent)
	}
	if t.Definition.Type != "epic" || t.Status != "open" {
		return Error("EPIC_NOT_OPEN", parent)
	}
	return nil
}

// LinkEpic adds one existing leaf without rewriting its historical definition,
// result, candidate or completion time. The record is saved with the task.
func (s *Service) LinkEpic(parent, child string, required bool, milestone string) (Next, error) {
	return s.linkEpic(parent, child, required, milestone, "", "", "", "")
}

func (s *Service) linkEpic(parent, child string, required bool, milestone, expectedPolicy, expectedHash, planKey, expectedState string) (Next, error) {
	if err := s.active(); err != nil {
		return Next{}, err
	}
	read := s.Get
	if expectedPolicy != "" {
		read = s.planningTask
	}
	if _, err := read(parent); err != nil {
		return Next{}, Error("EPIC_MISSING", parent)
	}
	if _, err := read(child); err != nil {
		return Next{}, Error("TASK_MISSING", child)
	}
	err := s.Store.Write(func(tx *store.Tx) error {
		_, err := s.linkEpicTx(tx, parent, child, required, milestone, expectedPolicy, expectedHash, planKey, expectedState)
		return err
	})
	if err != nil {
		return Next{}, err
	}
	if err = s.Store.Flush(); err != nil {
		return Next{}, err
	}
	return s.Next(child)
}

// LinkRemoteEpicTx retains the existing membership, writer and scope guards
// while letting a finding association share the same central transaction.
func (s *Service) LinkRemoteEpicTx(tx *store.Tx, parent, child string, required bool, milestone string) (Task, error) {
	if !s.RemoteAuthority || s.remoteOperation == nil || s.remoteOperation.TaskID != child || s.remoteOperation.Expected == "" || s.remoteOperation.Expected == "absent" {
		return Task{}, Error("REMOTE_OPERATION_REQUIRED", "authorized association operation must name the existing leaf")
	}
	if err := s.active(); err != nil {
		return Task{}, err
	}
	return s.linkEpicTx(tx, parent, child, required, milestone, "", "", "", "")
}

func (s *Service) linkEpicTx(tx *store.Tx, parent, child string, required bool, milestone, expectedPolicy, expectedHash, planKey, expectedState string) (Task, error) {
	var t Task
	err := func() error {
		if err := checkPlanActive(tx, parent, planKey); err != nil {
			return err
		}
		if expectedPolicy != "" {
			policy, err := s.policyHash()
			if err != nil || policy != expectedPolicy {
				return Error("PLAN_STALE_POLICY", "project policy changed during application")
			}
		}
		if err := validateEpicParent(tx, parent, child); err != nil {
			return err
		}
		var err error
		t, err = load(tx, child)
		if err != nil {
			return Error("TASK_MISSING", child)
		}
		if t.Definition.Type == "epic" {
			return Error("EPIC_CHILD_INVALID", "only leaf tasks may be associated")
		}
		if expectedHash != "" && t.DefinitionHash != expectedHash {
			return Error("PLAN_TASK_CHANGED", child)
		}
		if expectedState != "" && taskStateHash(t) != expectedState {
			return Error("PLAN_TASK_CHANGED", child)
		}
		if expectedState != "" {
			if err := s.checkTaskFile(t); err != nil {
				return err
			}
		}
		if t.EpicMembership != nil {
			return Error("TASK_ALREADY_LINKED", t.EpicMembership.Parent)
		}
		if hasWriter(t) {
			return Error("RUNNING_ATTEMPT", "stop or finish the leaf before associating it")
		}
		t.EpicMembership = &EpicMembership{Parent: parent, Required: required, Milestone: strings.TrimSpace(milestone)}
		if planKey != "" {
			err = s.savePlan(tx, &t)
		} else {
			err = s.save(tx, &t)
		}
		if err != nil {
			return err
		}
		return tx.Event(child, "epic_linked", t.EpicMembership)
	}()
	return t, err
}

func (s *Service) epicMembers(parent string) ([]Task, error) {
	rows, err := s.Store.DB.Query("SELECT body FROM tasks WHERE json_extract(body,'$.epic_membership.parent')=? ORDER BY id", parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Task{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		var t Task
		if err = json.Unmarshal([]byte(body), &t); err != nil {
			return nil, err
		}
		members = append(members, t)
	}
	return members, rows.Err()
}

func (s *Service) summarizeEpicNext(n *Next, parent *Task) error {
	var activePlan string
	planErr := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", planActiveKey(parent.Definition.ID)).Scan(&activePlan)
	if planErr != nil && planErr != sql.ErrNoRows {
		return planErr
	}
	if planErr == nil {
		if n.Action == "continue" || n.Action == "repair" {
			n.Action = "wait_integration"
		}
		n.Reasons = append(n.Reasons, "active decomposition plan must be finished or recovered")
		return nil
	}
	members, err := s.epicMembers(parent.Definition.ID)
	if err != nil {
		return err
	}
	required, completed := 0, 0
	for _, member := range members {
		if !member.EpicMembership.Required {
			continue
		}
		required++
		if acceptedEpicLeaf(*parent, member) {
			completed++
		} else if member.Historical {
			n.Reasons = append(n.Reasons, "required child "+member.Definition.ID+" is historical and has not been revalidated here")
		} else if member.Status == "completed" {
			n.Reasons = append(n.Reasons, "required child "+member.Definition.ID+" has no passing acceptance for this goal")
		} else {
			n.Reasons = append(n.Reasons, "required child "+member.Definition.ID+" is "+member.Status)
		}
	}
	if n.Action == "continue" || n.Action == "repair" {
		if required == 0 || completed != required {
			n.Action = "wait_children"
			if required == 0 {
				n.Reasons = append(n.Reasons, "add required leaf tasks before integration")
			}
		} else if parent.Integration == nil || (parent.Step != "verify" && parent.Step != "delivery") {
			n.Action = "wait_integration"
			n.Reasons = append(n.Reasons, "required leaves completed; parent integration remains to be accepted")
		} else if parent.Integration.StaleReason != "" {
			n.Action = "wait_integration"
			n.Reasons = append(n.Reasons, parent.Integration.StaleReason)
		} else if parent.Integration.WaitReason != "" {
			n.Action = "wait_integration"
			n.Reasons = append(n.Reasons, parent.Integration.WaitReason)
		} else {
			digest := ""
			if parent.EpicPlan != nil {
				digest = parent.EpicPlan.Digest
			}
			candidateMatches := false
			if s.RemoteAuthority {
				err = s.Store.Write(func(tx *store.Tx) error {
					current, err := load(tx, parent.Definition.ID)
					if err != nil {
						return err
					}
					if RemoteRevision(current) != RemoteRevision(*parent) {
						return Error("EPIC_INTEGRATION_STALE", "parent changed during read")
					}
					return s.validateIntegration(tx, &current)
				})
				candidateMatches = err == nil
			} else {
				current, readErr := Fingerprint(parent.CandidateWorkspace)
				err = readErr
				candidateMatches = readErr == nil && current == parent.Candidate
			}
			if !candidateMatches || parent.Integration.Candidate != parent.Candidate || parent.Integration.Definition != parent.Definition.Revision || parent.Integration.Plan != parent.Plan.Revision || parent.Integration.PlanDigest != digest || !validSupportingLeaves(*parent, members) {
				n.Action = "wait_integration"
				n.Reasons = append(n.Reasons, "parent candidate changed; prepare integration again")
			} else {
				index := 0
				for _, member := range members {
					if !member.EpicMembership.Required {
						continue
					}
					if index >= len(parent.Integration.Leaves) || parent.Integration.Leaves[index].ID != member.Definition.ID || parent.Integration.Leaves[index].Candidate != member.Candidate {
						n.Action = "wait_integration"
						n.Reasons = append(n.Reasons, "required scope changed; prepare integration again")
						break
					}
					index++
				}
				if index != len(parent.Integration.Leaves) && n.Action != "wait_integration" {
					n.Action = "wait_integration"
					n.Reasons = append(n.Reasons, "required scope changed; prepare integration again")
				}
			}
		}
	}
	return nil
}

func (s *Service) Tree(id string) (EpicTree, error) {
	return s.epicTree(id, true)
}

func (s *Service) epicTree(id string, timeline bool) (EpicTree, error) {
	result := EpicTree{Children: []EpicTreeMember{}, Summary: EpicSummary{Issues: []EpicIssue{}}, ScopeChanges: []EpicScopeEvent{}, Progress: []EpicProgress{}, ReportedProgress: []EpicProgress{}}
	root, err := s.Next(id)
	if err != nil {
		return result, err
	}
	if root.Task == nil || root.Task.Definition.Type != "epic" {
		return result, Error("EPIC_REQUIRED", id)
	}
	result.Root = root
	members, err := s.epicMembers(id)
	if err != nil {
		return result, err
	}
	for _, member := range members {
		next, err := s.Next(member.Definition.ID)
		if err != nil {
			return result, err
		}
		view, err := s.Summarize(next)
		if err != nil {
			return result, err
		}
		m := member.EpicMembership
		result.Children = append(result.Children, EpicTreeMember{Required: m.Required, Milestone: m.Milestone, Task: next, Summary: view})
		result.Summary.add(next, view, m.Required)
		if root.Task.Integration != nil && root.Action != "wait_integration" && root.Action != "wait_children" && gates(root.Task) && root.Task.Integration.Candidate == root.Task.Candidate && root.Task.Integration.Definition == root.Task.Definition.Revision && root.Task.Integration.Plan == root.Task.Plan.Revision {
			for _, included := range root.Task.Integration.Leaves {
				if included.ID == next.Task.Definition.ID && included.Candidate == next.Task.Candidate {
					result.Summary.Integrated++
					break
				}
			}
		}
		if m.Required {
			result.Required++
			if next.Task.Status == "completed" && !next.Task.Historical {
				result.RequiredCompleted++
			}
		}
	}
	for _, check := range root.Task.Definition.Checks {
		if evidence := CurrentEvidence(root.Task, check); evidence != nil && evidence.Result == "fail" {
			issue := EpicIssue{Task: id, Title: root.Task.Definition.Title, Kind: "integration_failed", Check: check.ID, Reason: evidence.Reason}
			result.Summary.Issues = append([]EpicIssue{issue}, result.Summary.Issues...)
		}
	}
	if timeline {
		result.ScopeChanges, result.Progress, result.ProgressTruncated, err = s.epicTimeline(id, result.Children)
		if err != nil {
			return result, err
		}
		result.ReportedProgress, result.ReportsTruncated, err = s.epicReports(id, result.Children)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

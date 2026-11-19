package core

// Read-only UI projections. Scheduling and evidence remain owned by Next and
// CurrentEvidence; these types cannot be submitted as task state.
import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type CheckSummary struct {
	Required         int `json:"required"`
	RequiredPassed   int `json:"required_passed"`
	Passed           int `json:"passed"`
	Failed           int `json:"failed"`
	NotRun           int `json:"not_run"`
	Blocked          int `json:"blocked"`
	HistoricalFailed int `json:"historical_failed"`
	Running          int `json:"running"`
	Claimed          int `json:"claimed"`
	Waived           int `json:"waived"`
	NotApplicable    int `json:"not_applicable"`
}
type CheckExecution struct {
	Executors []string `json:"executors"`
	Started   bool     `json:"started"`
	Reported  bool     `json:"reported"`
}
type CheckAccess struct {
	EvidenceRequired   bool   `json:"evidence_required"`
	ScreenshotRequired bool   `json:"screenshot_required"`
	EffectiveMethod    string `json:"effective_method"`
	CanReview          bool   `json:"can_review"`
	Reason             string `json:"reason,omitempty"`
}
type PendingHumanReview struct {
	Check  string   `json:"check"`
	Covers []string `json:"covers"`
}

// EffectiveCheckMethod preserves the original explicit method. Only legacy
// checks without either a method or a command default to human review.
func EffectiveCheckMethod(c Check) string {
	if c.Method != "" {
		return c.Method
	}
	if len(c.Command) > 0 {
		return "command"
	}
	return "human"
}

// ReviewAccess is a presentation of the same eligibility checked by the
// review service and its claim transaction. It does not authorize a write.
func ReviewAccess(n Next, c Check) CheckAccess {
	a := CheckAccess{EffectiveMethod: EffectiveCheckMethod(c)}
	t := n.Task
	if t != nil {
		a.EvidenceRequired, a.ScreenshotRequired = HumanEvidenceRequirements(*t, c)
	}
	if a.EffectiveMethod != "human" || len(c.Command) != 0 {
		a.Reason = "check requires its declared method"
	} else if t == nil || t.Status != "open" || t.Step != "verify" || t.Paused || t.Confirmation != nil || t.Candidate == "" || (n.Action != "continue" && n.Action != "repair" && n.Action != "wait_executor") {
		a.Reason = "task is not ready for this check"
	} else if e := CurrentEvidence(t, c); e != nil && e.Result == "pass" {
		a.Reason = "check already passed for this candidate"
	} else {
		for _, attempt := range t.Attempts {
			if attempt.Unit == "check:"+c.ID && attempt.Status == "running" {
				a.Reason = "check already has an active or unrecovered attempt"
				return a
			}
		}
		a.CanReview = true
	}
	return a
}

type TaskSummary struct {
	RecoveryAction       string                    `json:"recovery_action,omitempty"`
	Health               *WorkHealth               `json:"health,omitempty"`
	Epic                 *EpicSummary              `json:"epic,omitempty"`
	Parent               string                    `json:"parent,omitempty"`
	Children             int                       `json:"children"`
	ID                   string                    `json:"id"`
	Title                string                    `json:"title"`
	Type                 string                    `json:"type"`
	Board                string                    `json:"board"`
	Step                 string                    `json:"step"`
	Action               string                    `json:"next_action"`
	Reasons              []string                  `json:"blocking_reasons"`
	Executors            []string                  `json:"executors"`
	Running              int                       `json:"running"`
	WaitingChecks        int                       `json:"waiting_checks"`
	LastResult           string                    `json:"last_result"`
	Next                 string                    `json:"next"`
	Activity             *time.Time                `json:"activity_at"`
	Completed            *time.Time                `json:"completed_at"`
	Historical           bool                      `json:"historical"`
	Recovery             string                    `json:"recovery"`
	Blocker              string                    `json:"blocker_reason"`
	NeedsHistoryReview   bool                      `json:"needs_history_review"`
	RequiresTaskDecision bool                      `json:"requires_task_decision"`
	Checks               CheckSummary              `json:"checks"`
	ManualReviews        []PendingHumanReview      `json:"manual_reviews"`
	PendingReviewNotes   int                       `json:"pending_review_notes"`
	HumanReviewCount     int                       `json:"human_review_count"`
	ReviewNoteState      string                    `json:"review_note_state,omitempty"`
	CurrentRuns          map[string]CheckExecution `json:"-"`
}

func (s *Service) Summarize(n Next) (TaskSummary, error) {
	t := n.Task
	r := TaskSummary{RecoveryAction: n.RecoveryAction, Health: n.Health, ID: t.Definition.ID, Title: t.Definition.Title, Type: t.Definition.Type, Board: n.Board, Step: n.Step, Action: n.Action, Reasons: n.Reasons, Executors: []string{}, ManualReviews: []PendingHumanReview{}, Completed: t.Completed, Historical: t.Historical}
	if r.Health == nil {
		health, err := s.workHealth(*t, time.Now().UTC())
		if err != nil {
			return r, err
		}
		r.Health = &health
	}
	r.Parent = t.Definition.Parent
	if t.EpicMembership != nil {
		r.Parent = t.EpicMembership.Parent
	}
	if t.Definition.Type == "epic" {
		tree, err := s.epicTree(t.Definition.ID, false)
		if err != nil {
			return r, err
		}
		r.Children = len(tree.Children)
		r.Epic = &tree.Summary
	}
	r.NeedsHistoryReview = t.Status == "open" && t.Confirmation != nil && t.Confirmation.Kind == "restore"
	r.RequiresTaskDecision = t.Status == "open" && t.Confirmation != nil && !r.NeedsHistoryReview
	r.HumanReviewCount = len(t.HumanReviewNotes)
	currentNotes := 0
	currentConfirmed := false
	for _, note := range t.HumanReviewNotes {
		if note.Status == "pending" {
			r.PendingReviewNotes++
			if note.DefinitionRevision == t.Definition.Revision && note.PlanRevision == t.Plan.Revision && (note.Candidate == "" || note.Candidate == t.Candidate) {
				currentNotes++
			}
		}
		if note.Status == "confirmed" && t.Candidate != "" && note.ConfirmedCandidate == t.Candidate && note.DefinitionRevision == t.Definition.Revision && note.PlanRevision == t.Plan.Revision {
			currentConfirmed = true
		}
	}
	switch {
	case currentNotes > 0 && t.Status != "open":
		r.ReviewNoteState = "closed"
	case currentNotes > 0 && t.Candidate == "":
		r.ReviewNoteState = "await_candidate"
	case currentNotes > 0:
		r.ReviewNoteState = "await_confirmation"
	case currentConfirmed:
		r.ReviewNoteState = "confirmed"
	case r.PendingReviewNotes > 0 && t.Status != "open":
		r.ReviewNoteState = "closed"
	case r.PendingReviewNotes > 0:
		r.ReviewNoteState = "outdated"
	case r.HumanReviewCount > 0:
		r.ReviewNoteState = "historical"
	}
	r.CurrentRuns = map[string]CheckExecution{}
	checkIDs := map[string]bool{}
	for _, c := range t.Definition.Checks {
		checkIDs["check:"+c.ID] = true
	}
	now := time.Now()
	for _, a := range t.Attempts {
		if a.Status == "running" && a.Lease.After(now) {
			r.Running++
			if checkIDs[a.Unit] && t.Status == "open" && t.Step == "verify" && !t.Paused && t.Candidate != "" &&
				a.Input == t.Candidate && a.DefinitionRevision == t.Definition.Revision && a.PlanRevision == t.Plan.Revision {
				id := strings.TrimPrefix(a.Unit, "check:")
				entry := r.CurrentRuns[id]
				entry.Executors = append(entry.Executors, a.Executor)
				entry.Started = entry.Started || (!s.RemoteAuthority && a.PID > 0)
				entry.Reported = entry.Reported || (s.RemoteAuthority && a.Stage == a.Unit)
				r.CurrentRuns[id] = entry
			}
			found := false
			for _, x := range r.Executors {
				if x == a.Executor {
					found = true
				}
			}
			if !found {
				r.Executors = append(r.Executors, a.Executor)
			}
		}
	}
	for _, c := range t.Definition.Checks {
		if ReviewAccess(n, c).CanReview {
			r.ManualReviews = append(r.ManualReviews, PendingHumanReview{Check: c.ID, Covers: c.Covers})
		}
		if c.Required {
			r.Checks.Required++
		}
		e := CurrentEvidence(t, c)
		if c.Required && e != nil && e.Result == "pass" {
			r.Checks.RequiredPassed++
		}
		if run, claimed := r.CurrentRuns[c.ID]; claimed {
			if run.Started || run.Reported {
				r.Checks.Running++
			} else {
				r.Checks.Claimed++
			}
			continue
		}
		result := ""
		if e != nil {
			result = e.Result
		}
		switch result {
		case "pass":
			r.Checks.Passed++
		case "fail":
			r.Checks.Failed++
		case "blocked":
			r.Checks.Blocked++
		case "waived":
			r.Checks.Waived++
		case "not_applicable":
			r.Checks.NotApplicable++
		default:
			r.Checks.NotRun++
		}
		if t.Status == "open" && t.Step == "verify" && result != "pass" && result != "waived" && result != "not_applicable" {
			r.WaitingChecks++
		}
	}

	for _, ev := range append(append([]Evidence{}, t.EvidenceHistory...), t.Evidence...) {
		if ev.Result != "fail" {
			continue
		}
		current := false
		for _, c := range t.Definition.Checks {
			if e := CurrentEvidence(t, c); e != nil && e.Check == ev.Check && e.At.Equal(ev.At) {
				current = true
			}
		}
		if !current {
			r.Checks.HistoricalFailed++
		}
	}
	// Only actual work/results count. Heartbeats, claims, process starts and
	// source observations never advance the displayed progress timestamp.
	var raw, stamp, kind string
	err := s.Store.DB.QueryRow(`SELECT body,created,kind FROM events WHERE task_id=? AND kind IN ('work_progress','submitted','verified','assessed','reviewed','human_review_recorded','human_review_confirmed','confirmation','cancelled','paused','resumed','definition_revised') AND (kind NOT IN ('work_progress','submitted') OR COALESCE(json_extract(body,'$.evidence_changed'),1)=1) ORDER BY id DESC LIMIT 1`, r.ID).Scan(&raw, &stamp, &kind)
	if err != nil && err != sql.ErrNoRows {
		return r, err
	}
	if err == nil {
		var body struct {
			Summary string          `json:"summary"`
			Reason  string          `json:"reason"`
			Failure string          `json:"failure"`
			Next    string          `json:"next"`
			Result  json.RawMessage `json:"result"`
		}
		if err = json.Unmarshal([]byte(raw), &body); err != nil {
			return r, err
		}
		r.LastResult = body.Summary
		if r.LastResult == "" && len(body.Result) > 0 && body.Result[0] == '{' {
			var result StepResult
			if err = json.Unmarshal(body.Result, &result); err != nil {
				return r, err
			}
			r.LastResult = result.Summary
		}
		if r.LastResult == "" {
			r.LastResult = body.Reason
		}
		if r.LastResult == "" {
			r.LastResult = body.Failure
		}
		r.Next = body.Next
		if at, e := time.Parse(time.RFC3339Nano, stamp); e == nil {
			r.Activity = &at
		}
		if kind == "confirmation" && len(t.Approvals) > 0 {
			a := t.Approvals[len(t.Approvals)-1]
			r.LastResult = map[string]string{"approve": "已批准：", "adjust": "要求调整：", "reject": "已否决："}[a.Decision] + a.Reason
			if a.Kind == "restore" && a.Decision == "approve" {
				r.LastResult = "已核对历史资料：" + a.Reason
			}
			r.Activity = &a.At
		}
		if kind == "resumed" {
			r.LastResult = "已记录恢复处理，请依据当前条件继续"
		}
	}
	if t.Status != "open" {
		r.LastResult = t.Result
		r.Activity = t.Completed
	}
	if t.Status == "open" && t.Blocker != nil {
		r.Recovery = t.Blocker.Recovery
		r.Blocker = t.Blocker.Reason
	}
	if r.LastResult == "" {
		r.LastResult = "尚未提交工作结果"
	}
	return r, nil
}

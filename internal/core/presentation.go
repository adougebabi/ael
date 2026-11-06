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
}
type CheckExecution struct {
	Executors []string `json:"executors"`
}
type CheckAccess struct {
	EffectiveMethod string `json:"effective_method"`
	CanReview       bool   `json:"can_review"`
	Reason          string `json:"reason,omitempty"`
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
	RequiresTaskDecision bool                      `json:"requires_task_decision"`
	Checks               CheckSummary              `json:"checks"`
	ManualReviews        []PendingHumanReview      `json:"manual_reviews"`
	PendingReviewNotes   int                       `json:"pending_review_notes"`
	CurrentRuns          map[string]CheckExecution `json:"-"`
}

func (s *Service) Summarize(n Next) (TaskSummary, error) {
	t := n.Task
	r := TaskSummary{ID: t.Definition.ID, Title: t.Definition.Title, Type: t.Definition.Type, Board: n.Board, Step: n.Step, Action: n.Action, Reasons: n.Reasons, Executors: []string{}, ManualReviews: []PendingHumanReview{}, Completed: t.Completed, Historical: t.Historical}
	r.RequiresTaskDecision = t.Status == "open" && t.Confirmation != nil
	for _, note := range t.HumanReviewNotes {
		if note.Status == "pending" {
			r.PendingReviewNotes++
		}
	}
	r.CurrentRuns = map[string]CheckExecution{}
	running := map[string]bool{}
	for _, a := range t.Attempts {
		if a.Status == "running" && a.Lease.After(time.Now()) {
			r.Running++
			if strings.HasPrefix(a.Unit, "check:") {
				id := strings.TrimPrefix(a.Unit, "check:")
				running[id] = true
				entry := r.CurrentRuns[id]
				entry.Executors = append(entry.Executors, a.Executor)
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
		if running[c.ID] {
			r.Checks.Running++
		}
		e := CurrentEvidence(t, c)
		if e == nil {
			if !running[c.ID] {
				r.Checks.NotRun++
			}
		} else {
			switch e.Result {
			case "pass":
				r.Checks.Passed++
				if c.Required {
					r.Checks.RequiredPassed++
				}
			case "fail":
				r.Checks.Failed++
			case "blocked":
				r.Checks.Blocked++
			default:
				if !running[c.ID] {
					r.Checks.NotRun++
				}
			}
		}
		if t.Status == "open" && t.Step == "verify" && !running[c.ID] && (e == nil || e.Result != "pass") {
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
	err := s.Store.DB.QueryRow(`SELECT body,created,kind FROM events WHERE task_id=? AND kind IN ('work_progress','submitted','verified','assessed','reviewed','human_review_recorded','human_review_confirmed','confirmation','cancelled','paused','resumed','definition_revised') ORDER BY id DESC LIMIT 1`, r.ID).Scan(&raw, &stamp, &kind)
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
			r.Activity = &a.At
		}
		if kind == "resumed" {
			r.LastResult = "已记录恢复处理，请依据当前条件继续"
		}
	}
	if r.LastResult == "" {
		for i := len(t.Attempts) - 1; i >= 0; i-- {
			if t.Attempts[i].Ended != nil && t.Attempts[i].Reason != "" {
				r.LastResult = t.Attempts[i].Reason
				break
			}
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

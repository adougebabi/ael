package core

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

type Check struct {
	ID        string   `json:"id" yaml:"id"`
	Kind      string   `json:"kind" yaml:"kind"`
	Command   []string `json:"command" yaml:"command"`
	Covers    []string `json:"covers" yaml:"covers"`
	Required  bool     `json:"required" yaml:"required"`
	Timeout   int      `json:"timeout_seconds" yaml:"timeout_seconds"`
	Artifacts []string `json:"artifacts" yaml:"artifacts"`
}
type Definition struct {
	Legacy       map[string]any `json:"legacy,omitempty" yaml:"legacy,omitempty"`
	Target       string         `json:"target" yaml:"target"`
	ID           string         `json:"id" yaml:"id"`
	Title        string         `json:"title" yaml:"title"`
	Type         string         `json:"type" yaml:"type"`
	Source       string         `json:"source" yaml:"source"`
	Scope        []string       `json:"scope" yaml:"scope"`
	NonGoals     []string       `json:"non_goals" yaml:"non_goals"`
	Acceptance   []string       `json:"acceptance" yaml:"acceptance"`
	Priority     int            `json:"priority" yaml:"priority"`
	Dependencies []string       `json:"dependencies" yaml:"dependencies"`
	Parent       string         `json:"parent" yaml:"parent"`
	Goal         string         `json:"goal" yaml:"goal"`
	Environment  string         `json:"environment" yaml:"environment"`
	Preset       string         `json:"preset" yaml:"preset"`
	Risk         []string       `json:"risk" yaml:"risk"`
	UI           bool           `json:"ui" yaml:"ui"`
	UX           bool           `json:"ux" yaml:"ux"`
	Checks       []Check        `json:"checks" yaml:"checks"`
	Knowledge    []string       `json:"knowledge" yaml:"knowledge"`
	FeedbackOf   string         `json:"feedback_of" yaml:"feedback_of"`
	Revision     int            `json:"definition_revision" yaml:"definition_revision"`
	MaxRetries   int            `json:"max_retries" yaml:"max_retries"`
	MaxSeconds   int            `json:"max_seconds" yaml:"max_seconds"`
}
type Plan struct {
	Revision int      `json:"revision" yaml:"revision"`
	Preset   string   `json:"preset" yaml:"preset"`
	Steps    []string `json:"steps" yaml:"steps"`
	Reason   string   `json:"reason" yaml:"reason"`
	Approved bool     `json:"approved" yaml:"approved"`
}
type Attempt struct {
	ID        string     `json:"id" yaml:"id"`
	Unit      string     `json:"unit" yaml:"unit"`
	Executor  string     `json:"executor" yaml:"executor"`
	Session   string     `json:"session" yaml:"session"`
	Workspace string     `json:"workspace" yaml:"workspace"`
	Input     string     `json:"input_version" yaml:"input_version"`
	Candidate string     `json:"candidate_version" yaml:"candidate_version"`
	Started   time.Time  `json:"started" yaml:"started"`
	Ended     *time.Time `json:"ended" yaml:"ended"`
	Lease     time.Time  `json:"lease_until" yaml:"lease_until"`
	Token     string     `json:"token,omitempty" yaml:"token"`
	Fence     int        `json:"fence" yaml:"fence"`
	Status    string     `json:"status" yaml:"status"`
	Reason    string     `json:"reason" yaml:"reason"`
	Outputs   []string   `json:"outputs" yaml:"outputs"`
	PID       int        `json:"pid" yaml:"pid"`
}
type Evidence struct {
	ArtifactStatus map[string]string `json:"artifact_status,omitempty" yaml:"artifact_status"`
	Check          string            `json:"check" yaml:"check"`
	Candidate      string            `json:"candidate" yaml:"candidate"`
	Revision       int               `json:"acceptance_revision" yaml:"acceptance_revision"`
	PlanRevision   int               `json:"plan_revision" yaml:"plan_revision"`
	Config         string            `json:"config_hash" yaml:"config_hash"`
	Result         string            `json:"result" yaml:"result"`
	Source         string            `json:"source" yaml:"source"`
	Environment    string            `json:"environment" yaml:"environment"`
	Artifacts      []string          `json:"artifacts" yaml:"artifacts"`
	ArtifactHashes map[string]string `json:"artifact_hashes" yaml:"artifact_hashes"`
	Reason         string            `json:"reason" yaml:"reason"`
	At             time.Time         `json:"at" yaml:"at"`
}
type Confirmation struct {
	Kind         string `json:"kind" yaml:"kind"`
	Reason       string `json:"reason" yaml:"reason"`
	Revision     int    `json:"definition_revision" yaml:"definition_revision"`
	PlanRevision int    `json:"plan_revision" yaml:"plan_revision"`
	Candidate    string `json:"candidate" yaml:"candidate"`
}
type Approval struct {
	Kind         string    `json:"kind" yaml:"kind"`
	Actor        string    `json:"actor" yaml:"actor"`
	Decision     string    `json:"decision" yaml:"decision"`
	Reason       string    `json:"reason" yaml:"reason"`
	Revision     int       `json:"definition_revision" yaml:"definition_revision"`
	PlanRevision int       `json:"plan_revision" yaml:"plan_revision"`
	Candidate    string    `json:"candidate" yaml:"candidate"`
	At           time.Time `json:"at" yaml:"at"`
}
type Delivery struct {
	RunID       string `json:"run_id" yaml:"run_id"`
	Key         string `json:"idempotency_key" yaml:"idempotency_key"`
	Candidate   string `json:"candidate" yaml:"candidate"`
	Artifact    string `json:"artifact" yaml:"artifact"`
	Environment string `json:"environment" yaml:"environment"`
	Target      string `json:"target" yaml:"target"`
	Status      string `json:"status" yaml:"status"`
}
type Task struct {
	PolicyHash         string         `json:"policy_hash" yaml:"policy_hash"`
	CandidateWorkspace string         `json:"candidate_workspace" yaml:"candidate_workspace"`
	Definition         Definition     `json:"definition" yaml:"definition"`
	DefinitionHash     string         `json:"definition_hash" yaml:"definition_hash"`
	Plan               Plan           `json:"plan" yaml:"plan"`
	Step               string         `json:"current_step" yaml:"current_step"`
	Status             string         `json:"status" yaml:"status"`
	Attempts           []Attempt      `json:"attempts" yaml:"attempts"`
	Evidence           []Evidence     `json:"evidence" yaml:"evidence"`
	Confirmation       *Confirmation  `json:"confirmation" yaml:"confirmation"`
	Approvals          []Approval     `json:"approvals" yaml:"approvals"`
	Candidate          string         `json:"candidate" yaml:"candidate"`
	Failure            string         `json:"failure" yaml:"failure"`
	Paused             bool           `json:"paused" yaml:"paused"`
	Created            time.Time      `json:"created" yaml:"created"`
	Updated            time.Time      `json:"updated" yaml:"updated"`
	Completed          *time.Time     `json:"completed" yaml:"completed"`
	Historical         bool           `json:"historical" yaml:"historical"`
	Result             string         `json:"result" yaml:"result"`
	Delivery           *Delivery      `json:"delivery" yaml:"delivery"`
	Legacy             map[string]any `json:"legacy,omitempty" yaml:"legacy"`
	Feedback           []string       `json:"feedback" yaml:"feedback"`
}
type Next struct {
	Action       string   `json:"next_action" yaml:"next_action"`
	Reasons      []string `json:"blocking_reasons" yaml:"blocking_reasons"`
	Step         string   `json:"current_step" yaml:"current_step"`
	Revision     int      `json:"definition_revision" yaml:"definition_revision"`
	PlanRevision int      `json:"plan_revision" yaml:"plan_revision"`
	Board        string   `json:"board" yaml:"board"`
	Task         *Task    `json:"task,omitempty" yaml:"task"`
	Attempt      *Attempt `json:"attempt,omitempty" yaml:"attempt"`
}
type StepResult struct {
	Status    string    `json:"status" yaml:"status"`
	Summary   string    `json:"summary" yaml:"summary"`
	Artifacts []string  `json:"artifacts" yaml:"artifacts"`
	ReturnTo  string    `json:"return_to" yaml:"return_to"`
	Delivery  *Delivery `json:"delivery" yaml:"delivery"`
}
type Submission struct {
	Task    string     `json:"task" yaml:"task"`
	Attempt string     `json:"attempt" yaml:"attempt"`
	Token   string     `json:"token" yaml:"token"`
	Key     string     `json:"key" yaml:"key"`
	Result  StepResult `json:"result" yaml:"result"`
}

func Error(code, detail string) error { return fmt.Errorf("%s: %s", code, detail) }
func Validate(d Definition) error {
	if d.Title == "" || len(d.Acceptance) == 0 {
		return Error("INVALID_DEFINITION", "title and acceptance required")
	}
	if !slices.Contains([]string{"feature", "bug", "improvement", "refactor", "research", "docs", "chore"}, d.Type) {
		return Error("INVALID_TYPE", d.Type)
	}
	if !slices.Contains([]string{"analysis", "verified", "merged", "released"}, d.Goal) {
		return Error("INVALID_GOAL", d.Goal)
	}
	if d.Goal == "merged" && d.Target == "" {
		return Error("TARGET_REQUIRED", "declare target branch when creating a merge goal")
	}
	if d.Goal == "released" && d.Environment == "" {
		return Error("ENVIRONMENT_REQUIRED", "release target")
	}
	ids := map[string]bool{}
	for _, c := range d.Checks {
		if c.ID == "" || ids[c.ID] {
			return errors.New("INVALID_CHECK_ID")
		}
		ids[c.ID] = true
		if len(c.Covers) == 0 {
			return Error("CHECK_COVERAGE_REQUIRED", c.ID)
		}
		for _, a := range c.Covers {
			if !slices.Contains(d.Acceptance, a) {
				return Error("UNKNOWN_ACCEPTANCE", a)
			}
		}
	}
	return nil
}
func MakePlan(d *Definition) Plan {
	preset := d.Preset
	reason := "explicit preset"
	if preset == "" {
		preset = "standard"
		reason = "ordinary scoped change"
		if d.Type == "bug" || d.Type == "chore" || d.Type == "docs" {
			preset = "fast"
			reason = "small explicit change"
		}
		if d.Type == "research" || d.Goal == "analysis" {
			preset = "research"
			reason = "conclusion and evidence goal"
		}
		if len(d.Risk) > 0 {
			preset = "strict"
			reason = "risk signals require analysis and approval"
		}
	}
	if len(d.Risk) > 0 && preset != "strict" {
		preset = "strict"
		reason = "risk signals require analysis and approval; lower preset overridden"
	}
	steps := []string{"define"}
	if preset == "standard" || preset == "strict" {
		steps = append(steps, "analysis")
	}
	steps = append(steps, "execute", "verify")
	if d.Goal == "released" || d.Goal == "merged" {
		steps = append(steps, "delivery")
	}
	if len(d.Checks) == 0 {
		d.Checks = []Check{{ID: "review", Kind: "manual", Covers: d.Acceptance, Required: true}}
	}
	for _, x := range []struct {
		required bool
		id       string
	}{{d.UI, "ui"}, {d.UX, "ux"}} {
		if x.required {
			found := false
			for i := range d.Checks {
				if d.Checks[i].Kind == x.id {
					d.Checks[i].Required = true
					found = true
				}
			}
			if !found {
				d.Checks = append(d.Checks, Check{ID: x.id, Kind: x.id, Covers: d.Acceptance, Required: true})
			}
		}
	}
	if d.MaxRetries <= 0 {
		d.MaxRetries = 3
	}
	if d.MaxSeconds <= 0 {
		d.MaxSeconds = 3600
	}
	return Plan{1, preset, steps, reason, preset != "strict"}
}
func (t *Task) Board(now time.Time) string {
	if t.Status == "completed" {
		return "completed"
	}
	if t.Status == "cancelled" {
		return "cancelled"
	}
	if t.Confirmation != nil {
		return "confirmation"
	}
	for _, a := range t.Attempts {
		if a.Status == "running" && a.Lease.After(now) {
			return "active"
		}
	}
	return "pending"
}
func (t *Task) BaseNext() Next {
	n := Next{Step: t.Step, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Board: t.Board(time.Now()), Reasons: []string{}}
	switch n.Board {
	case "completed":
		n.Action = "completed"
	case "cancelled":
		n.Action = "cancelled"
	case "confirmation":
		n.Action = "wait_confirmation"
		n.Reasons = append(n.Reasons, t.Confirmation.Reason)
	case "active":
		n.Action = "wait_executor"
	default:
		n.Action = "continue"
	}
	if n.Board == "completed" || n.Board == "cancelled" || n.Board == "confirmation" {
		return n
	}
	if t.Paused {
		n.Action = "wait_environment"
		n.Reasons = append(n.Reasons, t.Failure)
	}
	for _, a := range t.Attempts {
		if a.Status == "running" && !a.Lease.After(time.Now()) {
			n.Action = "wait_environment"
			n.Reasons = append(n.Reasons, "expired attempt requires resume with stopped-process acknowledgement")
		}
	}
	return n
}

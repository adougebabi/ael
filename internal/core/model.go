package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Check struct {
	Method    string   `json:"method,omitempty" yaml:"method,omitempty"`
	ID        string   `json:"id" yaml:"id"`
	Kind      string   `json:"kind" yaml:"kind"`
	Command   []string `json:"command" yaml:"command"`
	Covers    []string `json:"covers" yaml:"covers"`
	Required  bool     `json:"required" yaml:"required"`
	Timeout   int      `json:"timeout_seconds" yaml:"timeout_seconds"`
	Artifacts []string `json:"artifacts" yaml:"artifacts"`
}

// BasisReference identifies the exact authoritative input supplied to an executor.
type BasisReference struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
}
type KnowledgeOutcome struct {
	Status    string   `json:"status"`
	Reason    string   `json:"reason"`
	Artifacts []string `json:"artifacts"`
}
type Blocker struct {
	Category string   `json:"category"`
	Reason   string   `json:"reason"`
	Scope    []string `json:"scope"`
	Tried    string   `json:"tried"`
	Recovery string   `json:"recovery"`
	ResumeAt string   `json:"resume_at"`
}

// EpicMembership is an explicit, project-local association. Legacy Parent
// references remain readable without being promoted to managed membership.
type EpicMembership struct {
	Parent    string `json:"parent"`
	Required  bool   `json:"required"`
	Milestone string `json:"milestone,omitempty"`
}
type Definition struct {
	Intent          string         `json:"intent,omitempty" yaml:"intent,omitempty"`
	OriginalRequest string         `json:"original_request,omitempty" yaml:"original_request,omitempty"`
	Legacy          map[string]any `json:"legacy,omitempty" yaml:"legacy,omitempty"`
	Target          string         `json:"target" yaml:"target"`
	ID              string         `json:"id" yaml:"id"`
	Title           string         `json:"title" yaml:"title"`
	Type            string         `json:"type" yaml:"type"`
	Source          string         `json:"source" yaml:"source"`
	Scope           []string       `json:"scope" yaml:"scope"`
	NonGoals        []string       `json:"non_goals" yaml:"non_goals"`
	Acceptance      []string       `json:"acceptance" yaml:"acceptance"`
	Priority        int            `json:"priority" yaml:"priority"`
	Dependencies    []string       `json:"dependencies" yaml:"dependencies"`
	Parent          string         `json:"parent" yaml:"parent"`
	Goal            string         `json:"goal" yaml:"goal"`
	Environment     string         `json:"environment" yaml:"environment"`
	Preset          string         `json:"preset" yaml:"preset"`
	Risk            []string       `json:"risk" yaml:"risk"`
	UI              bool           `json:"ui" yaml:"ui"`
	UX              bool           `json:"ux" yaml:"ux"`
	Checks          []Check        `json:"checks" yaml:"checks"`
	Knowledge       []string       `json:"knowledge" yaml:"knowledge"`
	FeedbackOf      string         `json:"feedback_of" yaml:"feedback_of"`
	Revision        int            `json:"definition_revision" yaml:"definition_revision"`
	MaxRetries      int            `json:"max_retries" yaml:"max_retries"`
	MaxSeconds      int            `json:"max_seconds" yaml:"max_seconds"`
}
type Plan struct {
	Revision int      `json:"revision" yaml:"revision"`
	Preset   string   `json:"preset" yaml:"preset"`
	Steps    []string `json:"steps" yaml:"steps"`
	Reason   string   `json:"reason" yaml:"reason"`
	Approved bool     `json:"approved" yaml:"approved"`
}
type Attempt struct {
	DefinitionRevision int        `json:"definition_revision,omitempty"`
	PlanRevision       int        `json:"plan_revision,omitempty"`
	ID                 string     `json:"id" yaml:"id"`
	Unit               string     `json:"unit" yaml:"unit"`
	Executor           string     `json:"executor" yaml:"executor"`
	Session            string     `json:"session" yaml:"session"`
	Workspace          string     `json:"workspace" yaml:"workspace"`
	Input              string     `json:"input_version" yaml:"input_version"`
	Candidate          string     `json:"candidate_version" yaml:"candidate_version"`
	Started            time.Time  `json:"started" yaml:"started"`
	Ended              *time.Time `json:"ended" yaml:"ended"`
	Lease              time.Time  `json:"lease_until" yaml:"lease_until"`
	Token              string     `json:"token,omitempty" yaml:"token"`
	Fence              int        `json:"fence" yaml:"fence"`
	Status             string     `json:"status" yaml:"status"`
	Reason             string     `json:"reason" yaml:"reason"`
	Outputs            []string   `json:"outputs" yaml:"outputs"`
	PID                int        `json:"pid" yaml:"pid"`
	OwnerPID           int        `json:"owner_pid,omitempty" yaml:"owner_pid,omitempty"`
	OwnerIdentity      string     `json:"owner_identity,omitempty" yaml:"owner_identity,omitempty"`
}
type Evidence struct {
	ReviewNoteID   string            `json:"review_note_id,omitempty" yaml:"review_note_id,omitempty"`
	ReusedFrom     string            `json:"reused_from,omitempty"`
	Executor       string            `json:"executor,omitempty"`
	Recovery       string            `json:"recovery,omitempty"`
	Inputs         []string          `json:"inputs,omitempty"`
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

// HumanReviewNote is an explicitly recorded observation, not check evidence.
// It remains non-gating until a person separately reviews a specific candidate.
type HumanReviewNote struct {
	ID                 string            `json:"id"`
	Check              string            `json:"check,omitempty"`
	Actor              string            `json:"actor"`
	Result             string            `json:"result"`
	Reason             string            `json:"reason"`
	Recovery           string            `json:"recovery,omitempty"`
	Artifacts          []string          `json:"artifacts,omitempty"`
	ArtifactHashes     map[string]string `json:"artifact_hashes,omitempty"`
	Candidate          string            `json:"candidate,omitempty"`
	DefinitionRevision int               `json:"definition_revision"`
	PlanRevision       int               `json:"plan_revision"`
	Status             string            `json:"status"`
	RecordedAt         time.Time         `json:"recorded_at"`
	ConfirmedCandidate string            `json:"confirmed_candidate,omitempty"`
	ConfirmedAt        *time.Time        `json:"confirmed_at,omitempty"`
	ConfirmedBy        string            `json:"confirmed_by,omitempty"`
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
	EpicPlan            *EpicPlanSnapshot  `json:"epic_plan,omitempty"`
	EpicPlanHistory     []EpicPlanSnapshot `json:"epic_plan_history,omitempty"`
	EpicMembership      *EpicMembership    `json:"epic_membership,omitempty"`
	InspectionCandidate string             `json:"inspection_candidate,omitempty"`
	Basis               []BasisReference   `json:"applicable_knowledge"`
	ContextAttempt      string             `json:"context_attempt,omitempty"`
	KnowledgeOutcome    *KnowledgeOutcome  `json:"knowledge_outcome,omitempty"`
	Blocker             *Blocker           `json:"blocker,omitempty"`
	Route               string             `json:"route,omitempty"`
	PolicyHash          string             `json:"policy_hash" yaml:"policy_hash"`
	CandidateWorkspace  string             `json:"candidate_workspace" yaml:"candidate_workspace"`
	Definition          Definition         `json:"definition" yaml:"definition"`
	DefinitionHash      string             `json:"definition_hash" yaml:"definition_hash"`
	Plan                Plan               `json:"plan" yaml:"plan"`
	Step                string             `json:"current_step" yaml:"current_step"`
	Status              string             `json:"status" yaml:"status"`
	Attempts            []Attempt          `json:"attempts" yaml:"attempts"`
	EvidenceHistory     []Evidence         `json:"evidence_history,omitempty"`
	Evidence            []Evidence         `json:"evidence" yaml:"evidence"`
	HumanReviewNotes    []HumanReviewNote  `json:"human_review_notes,omitempty"`
	Confirmation        *Confirmation      `json:"confirmation" yaml:"confirmation"`
	Approvals           []Approval         `json:"approvals" yaml:"approvals"`
	Candidate           string             `json:"candidate" yaml:"candidate"`
	Failure             string             `json:"failure" yaml:"failure"`
	Paused              bool               `json:"paused" yaml:"paused"`
	Created             time.Time          `json:"created" yaml:"created"`
	Updated             time.Time          `json:"updated" yaml:"updated"`
	Completed           *time.Time         `json:"completed" yaml:"completed"`
	Historical          bool               `json:"historical" yaml:"historical"`
	Result              string             `json:"result" yaml:"result"`
	Delivery            *Delivery          `json:"delivery" yaml:"delivery"`
	Legacy              map[string]any     `json:"legacy,omitempty" yaml:"legacy"`
	Feedback            []string           `json:"feedback" yaml:"feedback"`
	History             *RecordHistory     `json:"history,omitempty" yaml:"history,omitempty"`
	TimeLimitSeconds    *int               `json:"time_limit_seconds,omitempty" yaml:"time_limit_seconds,omitempty"`
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
	KeeperPID    int      `json:"keeper_pid,omitempty" yaml:"keeper_pid,omitempty"`
}
type StepResult struct {
	Knowledge *KnowledgeOutcome `json:"knowledge,omitempty"`
	Blocker   *Blocker          `json:"blocker,omitempty"`
	Status    string            `json:"status" yaml:"status"`
	Summary   string            `json:"summary" yaml:"summary"`
	Artifacts []string          `json:"artifacts" yaml:"artifacts"`
	ReturnTo  string            `json:"return_to" yaml:"return_to"`
	Delivery  *Delivery         `json:"delivery" yaml:"delivery"`
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
	if d.ID == "." || d.ID == ".." || strings.ContainsAny(d.ID, "/\\") {
		return Error("INVALID_TASK_ID", d.ID)
	}
	if d.MaxSeconds < -1 {
		return Error("INVALID_BUDGET", "max_seconds must be -1 (unlimited), 0 (project default), or positive")
	}
	if !slices.Contains([]string{"epic", "feature", "bug", "improvement", "refactor", "research", "docs", "chore", "test", "qa", "verify"}, d.Type) {
		return Error("INVALID_TYPE", d.Type)
	}
	if !slices.Contains([]string{"analysis", "report", "verified", "merged", "released"}, d.Goal) {
		return Error("INVALID_GOAL", d.Goal)
	}
	if d.Goal == "merged" && d.Target == "" {
		return Error("TARGET_REQUIRED", "declare target branch when creating a merge goal")
	}
	if d.Goal == "released" && d.Environment == "" {
		return Error("ENVIRONMENT_REQUIRED", "release target")
	}
	if d.Intent != "" && d.Intent != "inspect" && d.Intent != "change" {
		return Error("INVALID_INTENT", d.Intent)
	}
	if d.Intent == "inspect" && d.Goal != "report" && d.Goal != "analysis" {
		return Error("INSPECTION_GOAL_REQUIRED", "inspection delivers a report or analysis; it cannot authorize source modification")
	}
	ids := map[string]bool{}
	for _, c := range d.Checks {
		if c.ID == "" || ids[c.ID] {
			return errors.New("INVALID_CHECK_ID")
		}
		ids[c.ID] = true
		if c.Method != "" && !slices.Contains([]string{"command", "scenario", "model", "human"}, c.Method) {
			return Error("INVALID_CHECK_METHOD", c.Method)
		}
		if len(c.Command) > 0 && (c.Method == "model" || c.Method == "human") {
			return Error("CHECK_METHOD_CONFLICT", c.ID)
		}
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
	if d.Type == "epic" {
		return Plan{1, "epic", []string{"define", "integrate"}, "aggregate scope and verify integration", false}
	}
	preset := d.Preset
	reason := "explicit preset"
	if preset == "" {
		preset = "standard"
		reason = "ordinary scoped change"
		if slices.Contains([]string{"bug", "chore", "docs", "test", "qa", "verify"}, d.Type) {
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
		d.Checks = []Check{{ID: "review", Kind: "review", Method: "model", Covers: d.Acceptance, Required: true}}
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
					if d.Checks[i].Method == "" {
						d.Checks[i].Method = "scenario"
					}
					found = true
				}
			}
			if !found {
				// Scope cannot be inferred from a UI flag. Reject an incomplete plan
				// instead of assigning every functional criterion to every checker.
				d.Checks = append(d.Checks, Check{ID: x.id, Kind: x.id, Method: "scenario", Required: true})
			}
		}
	}
	if d.MaxRetries <= 0 {
		d.MaxRetries = 3
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
		if t.Route == "repair" {
			n.Action = "repair"
		}
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

package core

import (
	"ael/internal/store"
	"database/sql"
	"slices"
	"strings"
	"time"
)

// RemoteClaimInput carries only opaque client identities and a candidate ID.
// The authenticated transport validates the runner, workspace and context in
// the same transaction before invoking the shared task-state rules below.
type RemoteClaimInput struct {
	TaskID, ParentID, ExpectedRevision, Runner, Workspace, Candidate, Token string
	Basis                                                                   []BasisReference
	ReviewerOnly                                                            bool
}

// RemoteSubmitEnvironment supplies server-validated, finalized artifacts and
// the client's candidate without opening a client path on the server.
type RemoteSubmitEnvironment struct {
	CandidateID         string
	Head                string
	Files               map[string]CandidateFile
	Fence               int
	Basis               []BasisReference
	DiagnosticArtifacts map[string]bool
	Prepare             func(*store.Tx, *Task, *Attempt) (*Evidence, error)
	ReadArtifact        func(*store.Tx, string) ([]byte, error)
}

func (s *Service) RemoteWorkChoiceTx(tx *store.Tx, request WorkRequest) (WorkResult, error) {
	if !s.RemoteAuthority || request.Parallel {
		return WorkResult{}, Error("REMOTE_OPERATION_REQUIRED", "remote parent work is sequential")
	}
	return s.chooseWorkTx(tx, request)
}

func (s *Service) SubmitRemote(sub Submission, requestHash string, env *RemoteSubmitEnvironment) (Next, error) {
	if !s.RemoteAuthority || env == nil || env.CandidateID == "" || env.Fence < 1 || env.Prepare == nil || env.ReadArtifact == nil {
		return Next{}, Error("REMOTE_OPERATION_REQUIRED", "validated candidate, lease and artifacts are required")
	}
	return s.submitWithEnvironment(sub, nil, requestHash, env)
}

func (s *Service) RemoteClaimTx(tx *store.Tx, input RemoteClaimInput) (Task, Attempt, error) {
	var attempt Attempt
	if !s.RemoteAuthority || !s.Active || input.TaskID == "" || input.ExpectedRevision == "" || input.Runner == "" || input.Workspace == "" || input.Candidate == "" || input.Token == "" {
		return Task{}, attempt, Error("REMOTE_OPERATION_REQUIRED", "runner, workspace, candidate and revision are required")
	}
	task, err := load(tx, input.TaskID)
	if err != nil {
		return task, attempt, err
	}
	if RemoteRevision(task) != input.ExpectedRevision {
		return task, attempt, Error("REVISION_CONFLICT", "task changed before claim")
	}
	if task.Definition.Type == "epic" {
		return task, attempt, Error("PARENT_WRITE_FORBIDDEN", "claim an executable leaf")
	}
	if task.EpicMembership != nil && task.EpicMembership.Parent != input.ParentID {
		return task, attempt, Error("PARENT_SCOPE_REQUIRED", "claim this managed leaf through its parent")
	}
	if task.EpicMembership == nil && input.ParentID != "" {
		return task, attempt, Error("PARENT_SCOPE_REQUIRED", "task is not a member of the requested parent")
	}
	if task.Status != "open" || task.Paused || task.Confirmation != nil {
		return task, attempt, Error("NOT_RUNNABLE", "task is paused, terminal or awaiting a decision")
	}
	if err = s.checkTaskFile(task); err != nil {
		return task, attempt, err
	}
	policy, err := s.policyHash()
	if err != nil || policy != task.PolicyHash {
		return task, attempt, Error("POLICY_CHANGED", "task policy requires reconciliation")
	}
	for _, key := range []string{remotePlanTaskKey(task.Definition.ID), remotePlanParentKey(task.Definition.ID), remotePlanParentKey(task.Definition.Parent)} {
		var active string
		err = tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&active)
		if err != nil && err != sql.ErrNoRows {
			return task, attempt, err
		}
		if active != "" {
			return task, attempt, Error("PLAN_APPLY_IN_PROGRESS", "finish the active plan before execution")
		}
	}
	for _, dep := range task.Definition.Dependencies {
		other, err := load(tx, dep)
		if err != nil || other.Status != "completed" || other.Historical {
			return task, attempt, Error("DEPENDENCY_BLOCKED", dep)
		}
	}
	if task.Step == "delivery" {
		return task, attempt, Error("DELIVERY_MANUAL_REQUIRED", "remote runner cannot publish or deliver automatically")
	}
	if task.Step == "execute" && task.Plan.Preset == "strict" && !task.Plan.Approved {
		return task, attempt, Error("PLAN_APPROVAL_REQUIRED", "strict plan requires approval")
	}
	if input.ReviewerOnly && task.Step != "verify" {
		return task, attempt, Error("FORBIDDEN", "reviewer may only claim checks")
	}
	unit := task.Step
	if task.Step == "verify" {
		if task.Candidate == "" || task.Candidate != input.Candidate {
			return task, attempt, Error("CANDIDATE_CHANGED", "verify the exact submitted candidate")
		}
		if task.CandidateWorkspace != "remote:"+input.Runner+"/"+input.Workspace {
			return task, attempt, Error("REMOTE_WORKSPACE_MISMATCH", "verify in the producing runner workspace")
		}
		unit = ""
		for _, check := range task.Definition.Checks {
			if !check.Required {
				continue
			}
			if evidence := CurrentEvidence(&task, check); evidence != nil && (evidence.Result == "pass" || (task.Definition.Goal == "report" && evidence.Result == "fail")) {
				continue
			}
			if len(check.Command) > 0 && check.Method != "" && check.Method != "command" {
				return task, attempt, Error("CHECK_METHOD_MISMATCH", "a command cannot substitute for the declared review method")
			}
			if len(check.Command) == 0 && check.Method != "model" && check.Method != "scenario" {
				return task, attempt, Error("HUMAN_REVIEW_REQUIRED", check.ID)
			}
			unit = "check:" + check.ID
			break
		}
		if unit == "" {
			return task, attempt, Error("CHECK_REQUIRED", "no executable required check")
		}
	}
	if task.Definition.Intent == "inspect" {
		if task.InspectionCandidate == "" {
			task.InspectionCandidate = input.Candidate
		}
		if task.InspectionCandidate != input.Candidate {
			return task, attempt, Error("INSPECTION_WRITE_DETECTED", "inspection candidate changed")
		}
	}
	for _, cycle := range task.FailureCycles {
		if cycle.Unit == unit && (cycle.State == "review_required" || cycle.State == "blocked") {
			return task, attempt, Error("ROOT_CAUSE_REVIEW_REQUIRED", "review repeated failure before retry")
		}
	}
	for _, old := range task.Attempts {
		if old.Status == "running" || old.Status == "interrupted" {
			return task, attempt, Error("CLAIM_CONFLICT", "active or interrupted attempt requires recovery")
		}
	}
	if timeBudgetExhausted(&task) {
		return task, attempt, Error("BUDGET_EXHAUSTED", "task execution budget exhausted")
	}
	duration, err := s.leaseDuration()
	if err != nil {
		return task, attempt, err
	}
	now := time.Now().UTC()
	attempt = Attempt{DefinitionRevision: task.Definition.Revision, PlanRevision: task.Plan.Revision, ID: ID("AT-"), Unit: unit, Executor: input.Runner, Workspace: "remote:" + input.Runner + "/" + input.Workspace, Input: input.Candidate, Started: now, LastContact: &now, Lease: now.Add(duration), Token: input.Token, Fence: len(task.Attempts) + 1, Status: "running"}
	task.Basis = slices.Clone(input.Basis)
	task.ContextAttempt = attempt.ID
	task.Attempts = append(task.Attempts, attempt)
	if err = s.save(tx, &task); err != nil {
		return task, Attempt{}, err
	}
	if err = tx.Event(task.Definition.ID, "remote_claimed", map[string]any{"attempt": attempt.ID, "runner": input.Runner, "unit": unit, "candidate": input.Candidate}); err != nil {
		return task, Attempt{}, err
	}
	return task, attempt, nil
}

func (s *Service) RemoteAttemptTx(tx *store.Tx, taskID, runner, attemptID, token string, fence int) (Task, Attempt, error) {
	var result Attempt
	task, err := load(tx, taskID)
	if err != nil {
		return task, result, err
	}
	attempt, err := findAttempt(&task, attemptID, token)
	if err != nil || attempt.Fence != fence || attempt.Executor != runner || !strings.HasPrefix(attempt.Workspace, "remote:") {
		return task, result, Error("STALE_TOKEN", "lease or fencing token is no longer valid")
	}
	if task.Status != "open" || task.Paused || attempt.DefinitionRevision != task.Definition.Revision || attempt.PlanRevision != task.Plan.Revision {
		return task, result, Error("STALE_EXECUTION_UNIT", "task responsibility changed")
	}
	return task, *attempt, nil
}

func (s *Service) RemoteHeartbeatTx(tx *store.Tx, taskID, runner, attemptID, token string, fence int) (Task, Attempt, error) {
	task, attemptCopy, err := s.RemoteAttemptTx(tx, taskID, runner, attemptID, token, fence)
	if err != nil {
		return task, Attempt{}, err
	}
	attempt, err := findAttempt(&task, attemptCopy.ID, token)
	if err != nil {
		return task, Attempt{}, err
	}
	duration, err := s.leaseDuration()
	if err != nil {
		return task, Attempt{}, err
	}
	now := time.Now().UTC()
	attempt.LastContact, attempt.Lease = &now, now.Add(duration)
	result := *attempt
	if err = save(tx, &task); err != nil {
		return task, Attempt{}, err
	}
	return task, result, nil
}

// RemoteProgressTx records diagnostic progress without treating heartbeats or
// executor prose as delivered evidence.
func (s *Service) RemoteProgressTx(tx *store.Tx, taskID, runner, attemptID, token string, fence int, progress Progress) (Task, error) {
	if strings.TrimSpace(progress.Summary) == "" || strings.TrimSpace(progress.Next) == "" || len(progress.Artifacts) != 0 || (progress.EvidenceKind != "" && progress.EvidenceKind != "diagnostic") {
		return Task{}, Error("PROGRESS_REQUIRED", "diagnostic summary and next action are required")
	}
	task, attemptCopy, err := s.RemoteAttemptTx(tx, taskID, runner, attemptID, token, fence)
	if err != nil {
		return task, err
	}
	attempt, err := findAttempt(&task, attemptCopy.ID, token)
	if err != nil {
		return task, err
	}
	progress.Actor, progress.At = runner, time.Now().UTC()
	if progress.ExpectedUntil != nil && (strings.TrimSpace(progress.Stage) == "" || !progress.ExpectedUntil.After(progress.At)) {
		return task, Error("INVALID_WORK_STAGE", "a future expected_until requires a named stage")
	}
	attempt.LastContact = &progress.At
	if progress.Stage != "" || progress.ExpectedUntil != nil {
		attempt.Stage, attempt.ExpectedUntil = progress.Stage, progress.ExpectedUntil
	}
	changed := false
	progress.EvidenceChanged = &changed
	if err = s.save(tx, &task); err != nil {
		return task, err
	}
	if err = tx.Event(taskID, "work_progress", progress); err != nil {
		return task, err
	}
	return task, nil
}

func (s *Service) RemoteInterruptTx(tx *store.Tx, taskID, runner, attemptID, token string, fence int, reason string) (Task, error) {
	if strings.TrimSpace(reason) == "" {
		return Task{}, Error("RESULT_REQUIRED", "interruption reason required")
	}
	task, copy, err := s.RemoteAttemptTx(tx, taskID, runner, attemptID, token, fence)
	if err != nil {
		return task, err
	}
	attempt, err := findAttempt(&task, copy.ID, token)
	if err != nil {
		return task, err
	}
	markInterrupted(&task, attempt, reason)
	if err = s.save(tx, &task); err != nil {
		return task, err
	}
	if err = tx.Event(taskID, "remote_interrupted", map[string]string{"attempt": attemptID, "reason": reason}); err != nil {
		return task, err
	}
	return task, nil
}

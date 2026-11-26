package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// RemoteClaimInput carries only opaque client identities and a candidate ID.
// The authenticated transport validates the runner, workspace and context in
// the same transaction before invoking the shared task-state rules below.
type RemoteClaimInput struct {
	TaskID, ParentID, ExpectedRevision, Runner, Workspace, Candidate, Token string
	// Actor is the authenticated executor account owning Runner.
	Actor        string
	Basis        []BasisReference
	ReviewerOnly bool
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
		return Next{}, Error("REMOTE_OPERATION_REQUIRED", "validated candidate and lease are required")
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
	var reservation string
	err = tx.Row("SELECT body FROM metadata WHERE key=?", "remote-recovery-target:"+input.TaskID).Scan(&reservation)
	if err != nil && err != sql.ErrNoRows {
		return task, attempt, err
	}
	if reservation != "" {
		var target map[string]string
		if json.Unmarshal([]byte(reservation), &target) != nil || target["runner"] != input.Runner || target["workspace"] != input.Workspace {
			return task, attempt, Error("REMOTE_HANDOFF_TARGET", "claim from the explicitly selected receiving workspace")
		}
	}
	// Another executor's not-yet-started queue record blocks an ordinary
	// claim; the holder's claim converts it into this Attempt below.
	queued, err := CurrentQueueTx(tx, task)
	if err != nil {
		return task, attempt, err
	}
	if queued != nil && !queued.Permits(input.Runner, input.Actor) {
		return task, attempt, QueueReservedError(*queued)
	}
	parentCheck := task.Definition.Type == "epic"
	if parentCheck {
		if task.Step != "verify" || task.Integration == nil {
			return task, attempt, Error("PARENT_WRITE_FORBIDDEN", "prepare the parent integration before claiming its checks")
		}
		if err = s.validateIntegration(tx, &task); err != nil {
			return task, attempt, err
		}
	}
	if task.EpicMembership != nil && task.EpicMembership.Parent != input.ParentID {
		return task, attempt, Error("PARENT_SCOPE_REQUIRED", "claim this managed leaf through its parent")
	}
	if task.EpicMembership == nil && input.ParentID != "" && !(parentCheck && input.ParentID == task.Definition.ID) {
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
			return task, attempt, Error("CANDIDATE_CHANGED", verifyCandidateChangedDetail(task, input.Candidate))
		}
		if task.CandidateWorkspace != "remote:"+input.Runner+"/"+input.Workspace {
			return task, attempt, Error("REMOTE_WORKSPACE_MISMATCH", "verify in the producing runner workspace")
		}
		unit = ""
		for _, check := range task.Definition.Checks {
			if !check.Required {
				continue
			}
			if remoteCheckFinished(&task, check) {
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
	if _, err = tx.Exec("DELETE FROM metadata WHERE key=?", "remote-recovery-target:"+input.TaskID); err != nil {
		return task, Attempt{}, err
	}
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
	attempt.ReportedSummary, attempt.ReportedNext, attempt.ProgressAt = progress.Summary, progress.Next, &progress.At
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

// RemoteRecovery records the executor's explicit workspace decision. The host
// owns workspace isolation; this transition only revokes API responsibility.
type RemoteRecovery struct {
	TaskID, AttemptID, ExpectedRevision, Runner, Workspace, Candidate, Mode, Reason string
}

func (s *Service) RemoteRecoverTx(tx *store.Tx, input RemoteRecovery) (Task, error) {
	task, err := load(tx, input.TaskID)
	if err != nil {
		return task, err
	}
	if !s.RemoteAuthority || RemoteRevision(task) != input.ExpectedRevision {
		return task, Error("REVISION_CONFLICT", "read the current task before recovery")
	}
	var previous *Attempt
	for i := range task.Attempts {
		if task.Attempts[i].ID == input.AttemptID {
			previous = &task.Attempts[i]
			break
		}
	}
	if previous == nil || !strings.HasPrefix(previous.Workspace, "remote:") || strings.TrimSpace(input.Reason) == "" || (input.Mode != "confirmed-stopped" && input.Mode != "independent-workspace") {
		return task, Error("REMOTE_RECOVERY_INVALID", "name the previous responsibility and an explicit workspace decision")
	}
	// Completed implementation may hand its exact candidate to another verifier.
	active := previous.Status == "running" || previous.Status == "interrupted"
	if !active && input.Mode == "confirmed-stopped" {
		return task, tx.Event(input.TaskID, "remote_workspace_confirmed_stopped", map[string]string{"attempt": input.AttemptID, "reason": input.Reason})
	}
	if !active && input.Mode == "independent-workspace" {
		for _, attempt := range task.Attempts {
			if attempt.Status == "running" || attempt.Status == "interrupted" {
				return task, Error("CLAIM_CONFLICT", "explicitly recover the current responsibility before handing off its candidate")
			}
		}
	}
	pendingHandoff := false
	if !active && previous.Status == "abandoned" {
		var raw string
		err := tx.Row("SELECT body FROM metadata WHERE key=?", "remote-recovery-target:"+input.TaskID).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return task, err
		}
		var reserved map[string]string
		if raw != "" && json.Unmarshal([]byte(raw), &reserved) != nil {
			return task, Error("REMOTE_RECOVERY_INVALID", "invalid pending handoff")
		}
		pendingHandoff = reserved["attempt"] == input.AttemptID
	}
	if !active && !pendingHandoff && (task.Step != "verify" || previous.Status != "pass" || previous.Candidate != task.Candidate) {
		return task, Error("REMOTE_RECOVERY_INVALID", "only the completed implementation candidate can be handed to verification")
	}
	if input.Mode == "confirmed-stopped" && (task.Status != "open" || task.Confirmation != nil || pendingFailureKey(&task) != "") {
		now := time.Now().UTC()
		previous.Status, previous.Ended, previous.Reason = "abandoned", &now, "executor explicitly confirmed stopped"
		if err := s.save(tx, &task); err != nil {
			return task, err
		}
		return task, tx.Event(input.TaskID, "remote_workspace_confirmed_stopped", map[string]string{"attempt": input.AttemptID, "reason": input.Reason})
	}
	if task.Status != "open" || task.Confirmation != nil || pendingFailureKey(&task) != "" {
		return task, Error("NOT_RUNNABLE", "resolve the task decision or failure review before handoff")
	}
	target := "remote:" + input.Runner + "/" + input.Workspace
	if input.Mode == "independent-workspace" && target == previous.Workspace {
		return task, Error("WORKSPACE_CONFIRMATION_REQUIRED", "choose a distinct declared workspace or confirm the old executor stopped")
	}
	if task.Step == "delivery" {
		return task, Error("DELIVERY_MANUAL_REQUIRED", "reconcile the external delivery before resuming")
	}
	if task.Step == "verify" && task.Candidate != input.Candidate {
		return task, Error("CANDIDATE_CHANGED", "the handoff must retain the exact candidate being checked")
	}
	oldWorkspace := previous.Workspace
	now := time.Now().UTC()
	if active {
		previous.Status, previous.Ended = "abandoned", &now
		previous.Reason = "API responsibility revoked; host execution state unknown"
		if input.Mode == "confirmed-stopped" {
			previous.Reason = "executor explicitly confirmed stopped"
		}
	}
	task.Paused, task.Blocker, task.ContextAttempt = false, nil, ""
	// Every check in the receiving workspace must be reported again.
	if task.Step == "verify" {
		task.EvidenceHistory = append(task.EvidenceHistory, task.Evidence...)
		task.Evidence = nil
		task.CandidateWorkspace = target
	}
	reservation, _ := json.Marshal(map[string]string{"runner": input.Runner, "workspace": input.Workspace, "attempt": input.AttemptID})
	if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", "remote-recovery-target:"+input.TaskID, string(reservation)); err != nil {
		return task, err
	}
	if err = s.save(tx, &task); err != nil {
		return task, err
	}
	err = tx.Event(input.TaskID, "remote_responsibility_recovered", map[string]string{"attempt": input.AttemptID, "from_workspace": oldWorkspace, "to_workspace": target, "mode": input.Mode, "reason": input.Reason, "host_execution_state": "unknown"})
	return task, err
}

func remoteCheckFinished(task *Task, check Check) bool {
	evidence := CurrentEvidence(task, check)
	return evidence != nil && (evidence.Result == "pass" || (task.Definition.Goal == "report" && evidence.Result == "fail"))
}

// verifyCandidateChangedDetail tells the claiming Agent which candidate the
// verify step is bound to and how to leave the deadlock when that exact
// working tree can no longer be restored (for example after the execute
// changes were committed, which changes the candidate's HEAD). Returning to
// execute is an ordinary executor action; it needs no reviewer.
func verifyCandidateChangedDetail(task Task, observed string) string {
	short := func(id string) string {
		if id == "" {
			return "none"
		}
		if len(id) > 12 {
			return id[:12]
		}
		return id
	}
	detail := fmt.Sprintf("verify needs submitted candidate %s, claim observed %s; restore that exact tree, or if it cannot be reproduced run: ael resume --task %s --return-to execute --expected-revision %s --operation-id OP --idempotency-key KEY, then re-execute and resubmit",
		short(task.Candidate), short(observed), task.Definition.ID, RemoteRevision(task))
	if len(detail) > 500 {
		detail = "verify the exact submitted candidate; if it cannot be reproduced run ael resume --task ID --return-to execute and resubmit"
	}
	return detail
}

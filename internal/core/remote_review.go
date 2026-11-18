package core

import (
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// RemoteReviewEnvironment is supplied by the authenticated server transport.
// Validate and ReadArtifact run under the same write transaction as the result.
// Neither callback opens client paths.
type RemoteReviewEnvironment struct {
	ArtifactNames map[string]string
	Comparison    *ReviewComparison
	Validate      func(*store.Tx, *Task) error
	ReadArtifact  func(*store.Tx, string) ([]byte, error)
}

type RemoteReviewInput struct {
	StateRevision                                        string
	Task, Check, Actor, Reason, Recovery, Result, NoteID string
	Artifacts                                            []string
	Expected                                             ReviewExpectation
}

func readReviewArtifact(s *Service, tx *store.Tx, env *RemoteReviewEnvironment, workspace, ref string) ([]byte, error) {
	if env != nil {
		return env.ReadArtifact(tx, ref)
	}
	return s.artifactAt(workspace, ref)
}

// RemoteReviewTx shares validation and the submission state machine with local
// human review. Its claim exists only inside this transaction, with no process
// or external runner responsibility to recover after a failed request.
func (s *Service) RemoteReviewTx(tx *store.Tx, input RemoteReviewInput, key, hash string, env *RemoteReviewEnvironment) (Next, error) {
	if !s.RemoteAuthority || env == nil || env.Validate == nil || env.ReadArtifact == nil || key == "" || hash == "" {
		return Next{}, Error("REMOTE_OPERATION_REQUIRED", "authorized human review environment is required")
	}
	if err := s.active(); err != nil {
		return Next{}, err
	}
	task, err := load(tx, input.Task)
	if err != nil {
		return Next{}, err
	}
	if input.StateRevision == "" || RemoteRevision(task) != input.StateRevision {
		return Next{}, Error("REVISION_CONFLICT", "task changed while preparing human review")
	}
	check, err := validateHumanReview(task, input.Check, input.Actor, input.Reason, input.Recovery, input.Result, input.Artifacts, &input.Expected, input.NoteID)
	if err != nil {
		return Next{}, err
	}
	if access := ReviewAccess(Next{Task: &task, Action: "continue"}, check); !access.CanReview {
		return Next{}, Error("REVIEW_NOT_ALLOWED", access.Reason)
	}
	if err = s.checkTaskFile(task); err != nil {
		return Next{}, err
	}
	policy, err := s.policyHash()
	if err != nil || policy != task.PolicyHash {
		return Next{}, Error("POLICY_CHANGED", "reconcile task policy before review")
	}
	if err = env.Validate(tx, &task); err != nil {
		return Next{}, err
	}
	for _, dep := range task.Definition.Dependencies {
		other, err := load(tx, dep)
		if err != nil || other.Status != "completed" || other.Historical {
			return Next{}, Error("DEPENDENCY_BLOCKED", dep)
		}
	}
	for _, attempt := range task.Attempts {
		if attempt.Status == "interrupted" || (attempt.Status == "running" && attempt.Unit == "check:"+check.ID) {
			return Next{}, Error("CLAIM_CONFLICT", "recover the existing responsibility before review")
		}
	}
	for _, cycle := range task.FailureCycles {
		if cycle.Unit == "check:"+check.ID && (cycle.State == "review_required" || cycle.State == "blocked") {
			return Next{}, Error("ROOT_CAUSE_REVIEW_REQUIRED", check.ID)
		}
	}
	if timeBudgetExhausted(&task) {
		return Next{}, Error("BUDGET_EXHAUSTED", "task execution budget exhausted")
	}
	hashes := map[string]string{}
	image := false
	for _, ref := range input.Artifacts {
		data, err := env.ReadArtifact(tx, ref)
		if err != nil {
			return Next{}, err
		}
		hashes[ref] = store.Hash(data)
		mime := http.DetectContentType(data)
		image = image || mime == "image/png" || mime == "image/jpeg"
	}
	if err := validateHumanArtifacts(check, input.Artifacts, env.ArtifactNames, input.Result); err != nil {
		return Next{}, err
	}
	_, screenshotRequired := HumanEvidenceRequirements(task, check)
	if screenshotRequired && input.Result != "blocked" && !image {
		return Next{}, Error("SCREENSHOT_REQUIRED", check.ID)
	}
	var mode string
	input.Reason, input.Recovery, mode = humanObservation(input.Reason, input.Recovery, input.Result, input.Artifacts)
	duration, err := s.leaseDuration()
	if err != nil {
		return Next{}, err
	}
	now := time.Now().UTC()
	attempt := Attempt{ID: ID("AT-"), Unit: "check:" + check.ID, Executor: input.Actor, Workspace: "remote:human/" + input.Actor, Input: task.Candidate, DefinitionRevision: task.Definition.Revision, PlanRevision: task.Plan.Revision, Started: now, LastContact: &now, Lease: now.Add(duration), Token: ID("lease-"), Fence: len(task.Attempts) + 1, Status: "running"}
	task.Attempts = append(task.Attempts, attempt)
	if err = s.save(tx, &task); err != nil {
		return Next{}, err
	}
	if err = tx.Event(input.Task, "human_review_claimed", map[string]string{"attempt": attempt.ID, "actor": input.Actor, "check": check.ID}); err != nil {
		return Next{}, err
	}
	config, _ := json.Marshal(check)
	evidence := Evidence{EvidenceMode: mode, Comparison: env.Comparison, ReviewNoteID: input.NoteID, Check: check.ID, Candidate: task.Candidate, Revision: task.Definition.Revision, PlanRevision: task.Plan.Revision, Config: store.Hash(config), Result: input.Result, Source: "human:" + input.Actor, Executor: input.Actor, Recovery: input.Recovery, Environment: "human remote review", Artifacts: input.Artifacts, ArtifactHashes: hashes, Reason: input.Reason, At: now}
	step := StepResult{Status: input.Result, Summary: input.Reason, Artifacts: input.Artifacts}
	if input.Result == "blocked" {
		step.Blocker = &Blocker{Category: "environment", Reason: input.Reason, Scope: task.Definition.Scope, Tried: input.Reason, Recovery: input.Recovery, ResumeAt: "verify"}
	}
	submitEnv := &RemoteSubmitEnvironment{CandidateID: task.Candidate, Fence: attempt.Fence, ReadArtifact: env.ReadArtifact, Prepare: func(tx *store.Tx, current *Task, _ *Attempt) (*Evidence, error) {
		if !reviewSnapshotMatches(current, input.Expected) {
			return nil, Error("REVIEW_VERSION_MISMATCH", "review the current candidate")
		}
		if err := env.Validate(tx, current); err != nil {
			return nil, err
		}
		return &evidence, nil
	}}
	return s.submitTx(tx, Submission{input.Task, attempt.ID, attempt.Token, key, step}, &evidence, hash, submitEnv)
}

// RemoteReviewNoteTx uses the original immutable observation and confirmation
// rules. These notes never supply gate evidence.
func (s *Service) RemoteReviewNoteTx(tx *store.Tx, input RemoteReviewInput, action, key, hash string, env *RemoteReviewEnvironment) (HumanReviewNote, error) {
	if !s.RemoteAuthority || env == nil || env.Validate == nil || env.ReadArtifact == nil || !validReviewKey(key) || hash == "" || strings.TrimSpace(input.Actor) == "" {
		return HumanReviewNote{}, Error("REMOTE_OPERATION_REQUIRED", "authorized human review environment is required")
	}
	if action == "review_confirm" {
		return s.confirmPendingReviewTx(tx, input.Task, input.NoteID, input.Actor, key, hash, input.Expected, env)
	}
	if action != "review_pending" || (input.Result != "pass" && input.Result != "fail" && input.Result != "blocked") || strings.TrimSpace(input.Reason) == "" {
		return HumanReviewNote{}, Error("REVIEW_INPUT_REQUIRED", "result and observation are required")
	}
	return s.recordPendingReviewTx(tx, input.Task, input.Check, input.Actor, input.Reason, input.Recovery, input.Result, input.Artifacts, key, hash, input.Expected, env)
}

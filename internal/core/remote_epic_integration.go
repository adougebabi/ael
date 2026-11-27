package core

import (
	"ael/internal/store"
	"slices"
	"strings"
	"time"
)

type RemoteIntegrationSnapshot struct {
	InputsKnown      bool
	Candidate        string
	Repository       string
	Project          string
	Head             string
	Workspace        string
	Files            map[string]CandidateFile
	LeafRepositories map[string]string
	LeafOutputs      map[string]map[string]RemoteIntegrationOutput
	LeafFiles        map[string]RemoteIntegrationFiles
	// LeafLineage records every required leaf's execution input and accepted
	// result, including leaves proven by outputs, so a later leaf that started
	// from an earlier result can explain why the final file differs.
	LeafLineage map[string]RemoteIntegrationFiles
	// LeafContinuity starts from another required leaf's accepted result that
	// the same workspace held before this leaf began, so edits made before the
	// execution claim still prove which result the leaf built on.
	LeafContinuity map[string]RemoteIntegrationFiles
	Basis            []BasisReference
	ImpactEvidence   string
	ImpactHash       string
}

type RemoteIntegrationOutput struct {
	Files map[string]CandidateFile
}

type RemoteIntegrationFiles struct {
	Before, After map[string]CandidateFile
}

func (s *Service) validateIntegration(tx *store.Tx, parent *Task) error {
	if !s.RemoteAuthority {
		return validateEpicIntegration(tx, parent)
	}
	if s.ValidateRemoteIntegration == nil || parent.Integration == nil || parent.Integration.RemoteRepository == "" {
		return Error("REMOTE_OPERATION_REQUIRED", "validated portable integration is required")
	}
	if err := validateEpicIntegrationState(tx, parent); err != nil {
		return err
	}
	return s.ValidateRemoteIntegration(tx, parent)
}

func (s *Service) remoteParentWorkTx(tx *store.Tx, request WorkRequest, parent *Task) (WorkResult, error) {
	if err := s.validateIntegration(tx, parent); err != nil {
		return workStopped(request, "wait_integration", err.Error(), parent, "integrate"), nil
	}
	if hasWriter(*parent) {
		return workStopped(request, "wait_executor", "parent already has an active or interrupted check", parent, "verify"), nil
	}
	if timeBudgetExhausted(parent) {
		return workStopped(request, "budget_stopped", "parent execution budget is exhausted", parent, "verify"), nil
	}
	for _, check := range parent.Definition.Checks {
		if !check.Required {
			continue
		}
		if remoteCheckFinished(parent, check) {
			continue
		}
		unit := "check:" + check.ID
		for _, cycle := range parent.FailureCycles {
			if cycle.Unit == unit && (cycle.State == "blocked" || cycle.State == "review_required") {
				return workStopped(request, "wait_human", "review the retained repeated failure", parent, unit), nil
			}
		}
		if EffectiveCheckMethod(check) == "human" {
			return workStopped(request, "wait_human", "a current-candidate human conclusion is required", parent, unit), nil
		}
		return WorkResult{Action: "ready", Checkpoint: WorkCheckpoint{Parent: request.Parent, Runner: request.Runner, Task: parent.Definition.ID, Step: "verify", Unit: unit, Revision: parent.Definition.Revision, PlanRevision: parent.Plan.Revision, At: time.Now().UTC()}, stateHash: taskStateHash(*parent)}, nil
	}
	return workStopped(request, "wait_human", "read the current parent gate outcome", parent, "verify"), nil
}

// PrepareRemoteEpicIntegrationTx binds the final externally produced candidate
// to the current required scope. Every required leaf check is rerun on that
// candidate through its explicitly mapped parent check.
func (s *Service) PrepareRemoteEpicIntegrationTx(tx *store.Tx, id, revision string, snapshot RemoteIntegrationSnapshot) (Task, error) {
	var parent Task
	if !s.RemoteAuthority || s.ValidateRemoteIntegration == nil || snapshot.Candidate == "" || snapshot.Repository == "" || snapshot.Project == "" || !strings.HasPrefix(snapshot.Workspace, "remote:") || snapshot.Files == nil {
		return parent, Error("REMOTE_OPERATION_REQUIRED", "portable candidate and workspace are required")
	}
	parent, err := load(tx, id)
	if err != nil {
		return parent, err
	}
	if RemoteRevision(parent) != revision {
		return parent, Error("REVISION_CONFLICT", "parent changed before integration")
	}
	if parent.Definition.Type != "epic" || parent.Status != "open" || parent.Paused || parent.Confirmation != nil || hasWriter(parent) {
		return parent, Error("EPIC_INTEGRATION_NOT_READY", "parent has a decision, pause or active responsibility")
	}
	if err = noActiveEpicPlan(tx, id); err != nil {
		return parent, err
	}
	policy, err := s.policyHash()
	if err != nil || policy != parent.PolicyHash {
		return parent, Error("EPIC_POLICY_CHANGED", "review current policy")
	}
	if err = epicCoverage(parent); err != nil {
		return parent, err
	}
	members, err := epicMembersTx(tx, id)
	if err != nil {
		return parent, err
	}
	known := map[string]bool{}
	for _, member := range members {
		known[member.Definition.ID] = true
	}
	for _, check := range parent.Definition.Checks {
		for _, affected := range check.Affected {
			if !known[affected] {
				return parent, Error("EPIC_AFFECTED_TASK_UNKNOWN", affected)
			}
		}
	}
	history := map[string][]leafPathChange{}
	for _, leaf := range members {
		if !leaf.EpicMembership.Required || !acceptedEpicLeaf(parent, leaf) || snapshot.LeafRepositories[leaf.Definition.ID] != snapshot.Repository {
			continue
		}
		for _, edges := range []map[string]RemoteIntegrationFiles{snapshot.LeafLineage, snapshot.LeafContinuity} {
			files, ok := edges[leaf.Definition.ID]
			if !ok {
				continue
			}
			for _, change := range candidateChanges(files.Before, files.After) {
				history[change.Path] = append(history[change.Path], leafPathChange{leaf: leaf.Definition.ID, change: change})
			}
		}
	}
	finalFile := func(path string) *CandidateFile {
		if file, ok := snapshot.Files[path]; ok {
			return &file
		}
		return nil
	}
	leaves := []IntegratedLeaf{}
	for _, leaf := range members {
		if !leaf.EpicMembership.Required {
			continue
		}
		if !acceptedEpicLeaf(parent, leaf) {
			return parent, Error("EPIC_REQUIRED_LEAF_NOT_ACCEPTED", leaf.Definition.ID)
		}
		superseded := func(path string, after *CandidateFile) bool {
			return supersedingLeaves(after, finalFile(path), history[path], leaf.Definition.ID) != nil
		}
		if snapshot.LeafRepositories[leaf.Definition.ID] != snapshot.Repository {
			return parent, Error("EPIC_REPOSITORY_UNVERIFIED", leaf.Definition.ID)
		}
		if len(leaf.UnboundArtifacts) > 0 {
			return parent, Error("EPIC_ARTIFACT_NOT_BOUND", leaf.Definition.ID)
		}
		for artifact, output := range snapshot.LeafOutputs[leaf.Definition.ID] {
			included := false
			for path, expected := range output.Files {
				if actual, exists := snapshot.Files[path]; (exists && actual == expected) || superseded(path, &expected) {
					included = true
					break
				}
			}
			if !included {
				return parent, Error("EPIC_LEAF_NOT_INCLUDED", leaf.Definition.ID+" "+artifact+"; the final candidate lost this accepted output and no later accepted leaf records it as its input")
			}
		}
		if files, ok := snapshot.LeafFiles[leaf.Definition.ID]; ok {
			for _, change := range candidateChanges(files.Before, files.After) {
				actual, exists := snapshot.Files[change.Path]
				if ((change.After == nil && exists) || (change.After != nil && (!exists || actual != *change.After))) && !superseded(change.Path, change.After) {
					return parent, Error("EPIC_LEAF_NOT_INCLUDED", leaf.Definition.ID+" "+change.Path+"; the final candidate lost this accepted change and no later accepted leaf records it as its input")
				}
			}
		}
		rechecks, err := epicRechecks(parent, leaf)
		if err != nil {
			return parent, err
		}
		leaves = append(leaves, IntegratedLeaf{ID: leaf.Definition.ID, Candidate: leaf.Candidate, Definition: leaf.Definition.Revision, Plan: leaf.Plan.Revision, Method: "parent_rechecked", Proof: leaf.CandidateProof, Rechecks: rechecks})
	}
	if len(leaves) == 0 {
		return parent, Error("EPIC_REQUIRED_LEAF_NOT_ACCEPTED", "required scope is empty")
	}
	if impact := parent.Definition.IntegrationImpact; impact != nil && impact.Complete {
		if snapshot.ImpactEvidence != impact.Evidence || snapshot.ImpactHash == "" {
			return parent, Error("EPIC_IMPACT_EVIDENCE_REQUIRED", "bind the declared impact evidence")
		}
	}
	digest := ""
	if parent.EpicPlan != nil {
		digest = parent.EpicPlan.Digest
	}
	parent.Plan.Revision++
	parent.Plan.Steps = []string{"define", "integrate", "verify"}
	if parent.Definition.Goal == "merged" || parent.Definition.Goal == "released" {
		parent.Plan.Steps = append(parent.Plan.Steps, "delivery")
	}
	parent.EvidenceHistory = append(parent.EvidenceHistory, parent.Evidence...)
	parent.Evidence = nil
	parent.Candidate, parent.CandidateWorkspace = snapshot.Candidate, snapshot.Workspace
	parent.Basis = slices.Clone(snapshot.Basis)
	parent.Integration = &EpicIntegration{ImpactUnknown: !snapshot.InputsKnown, RemoteRepository: snapshot.Repository, Candidate: snapshot.Candidate, ProjectID: snapshot.Project, GitHead: snapshot.Head, ToolBuild: s.ToolBuild, Files: snapshot.Files, Definition: parent.Definition.Revision, Plan: parent.Plan.Revision, PlanDigest: digest, ImpactEvidence: snapshot.ImpactEvidence, ImpactHash: snapshot.ImpactHash, Leaves: leaves, Prepared: time.Now().UTC()}
	parent.Step, parent.Route, parent.Failure = "verify", "", ""
	if err = s.ValidateRemoteIntegration(tx, &parent); err != nil {
		return parent, err
	}
	if err = s.save(tx, &parent); err != nil {
		return parent, err
	}
	return parent, tx.Event(id, "epic_integration_prepared", parent.Integration)
}

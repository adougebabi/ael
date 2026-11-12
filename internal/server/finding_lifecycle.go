package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
)

type findingMutationInput struct {
	FindingID              string `json:"finding_id"`
	ExpectedRevision       string `json:"expected_revision"`
	ExpectedTaskRevision   string `json:"expected_task_revision"`
	Action                 string `json:"action,omitempty"`
	Reason                 string `json:"reason"`
	OccurrenceID           string `json:"occurrence_id,omitempty"`
	RepairTaskID           string `json:"repair_task_id,omitempty"`
	RepairCheckID          string `json:"repair_check_id,omitempty"`
	ExpectedRepairRevision string `json:"expected_repair_revision,omitempty"`
	CandidateID            string `json:"candidate_id,omitempty"`
	EvidenceHash           string `json:"evidence_sha256,omitempty"`
	OperationID            string `json:"operation_id"`
	IdempotencyKey         string `json:"idempotency_key"`
}

func findingTx(tx *store.Tx, id string) (core.Finding, error) {
	var finding core.Finding
	var body string
	if !standaloneID.MatchString(id) {
		return finding, errors.New("FINDING_NOT_FOUND")
	}
	if err := tx.Row("SELECT body FROM findings WHERE id=?", id).Scan(&body); err != nil {
		return finding, errors.New("FINDING_NOT_FOUND")
	}
	if err := json.Unmarshal([]byte(body), &finding); err != nil {
		return finding, errors.New("FINDING_INVALID")
	}
	return finding, nil
}

func saveFindingChange(tx *store.Tx, finding core.Finding, kind string, value any, at string) error {
	body, err := json.Marshal(finding)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE findings SET body=? WHERE id=?", string(body), finding.ID); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO finding_history(id,finding_id,kind,body,created) VALUES(?,?,?,?,?)", core.ID("FH-"), finding.ID, kind, string(raw), at); err != nil {
		return err
	}
	taskID := finding.TaskID
	if kind == "finding_revalidated" && finding.Repair != nil {
		taskID = finding.Repair.TaskID
	}
	return tx.Event(taskID, kind, value)
}

func findingDecision(principal standalonePrincipal, role string, input findingMutationInput, finding core.Finding, at string) core.FindingDecision {
	authority := "client_reported"
	if len(principal.CSRFHash) > 0 && (role == "admin" || role == "reviewer") {
		authority = "human_session"
	}
	return core.FindingDecision{ID: core.ID("FD-"), Action: input.Action, Reason: strings.TrimSpace(input.Reason), Actor: principal.Username, Role: role, Authority: authority, FindingRevision: core.FindingRevision(finding), TaskRevision: input.ExpectedTaskRevision, At: at}
}

func findingReply(finding core.Finding, operationID string) map[string]any {
	next := "review_finding"
	switch finding.Status {
	case "needs_evidence":
		next = "provide_evidence"
	case "dismissed", "accepted_tradeoff":
		next = "review_task_gates"
	case "resolved":
		next = "resolved"
	default:
		if finding.Repair == nil {
			next = "create_repair"
		} else {
			next = "inspect_repair_and_revalidate"
		}
	}
	return map[string]any{"operation_id": operationID, "finding_id": finding.ID, "finding_revision": core.FindingRevision(finding), "status": finding.Status, "decision": finding.Decision, "repair": finding.Repair, "resolution": finding.Resolution, "next_action": next, "gate_effect": "none"}
}

func (p *portableStore) changeFinding(principal standalonePrincipal, role, id, action string, input findingMutationInput, artifacts *artifactStore) (map[string]any, error) {
	if input.FindingID != id || input.ExpectedRevision == "" || input.ExpectedTaskRevision == "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 8000 {
		return nil, errors.New("FINDING_MUTATION_INVALID")
	}
	if action == "decisions" {
		if _, err := core.FindingDecisionStatus(input.Action); err != nil {
			return nil, err
		}
		if (input.Action == "non_issue" || input.Action == "accepted_tradeoff") && (len(principal.CSRFHash) == 0 || (role != "admin" && role != "reviewer")) {
			return nil, errors.New("FORBIDDEN")
		}
	}
	return p.mutate(principal, "finding-"+action+":"+id, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		finding, err := findingTx(tx, id)
		if err != nil {
			return nil, err
		}
		// A new request key still cannot create another repair for the same finding.
		if action == "repair" && finding.Repair != nil {
			if (input.RepairTaskID != "" && input.RepairTaskID != finding.Repair.TaskID) || (input.RepairCheckID != "" && input.RepairCheckID != finding.Repair.CheckID) {
				return nil, errors.New("FINDING_REPAIR_EXISTS")
			}
			result := findingReply(finding, input.OperationID)
			result["existing"] = true
			return result, nil
		}
		if core.FindingRevision(finding) != input.ExpectedRevision {
			return nil, errors.New("FINDING_REVISION_CONFLICT")
		}
		source, err := artifactTask(tx, finding.TaskID)
		if err != nil {
			return nil, errors.New("TASK_NOT_FOUND")
		}
		if core.RemoteRevision(source) != input.ExpectedTaskRevision {
			return nil, errors.New("REVISION_CONFLICT")
		}
		at := time.Now().UTC().Format(time.RFC3339Nano)
		switch action {
		case "decisions":
			status, _ := core.FindingDecisionStatus(input.Action)
			decision := findingDecision(principal, role, input, finding, at)
			finding.Decision = &decision
			finding.Status = status
			if err = saveFindingChange(tx, finding, "finding_decided", decision, at); err != nil {
				return nil, err
			}
		case "repair":
			return p.linkFindingRepair(tx, principal, role, input, finding, source, at)
		case "revalidations":
			return p.revalidateFinding(tx, principal, input, finding, artifacts, at)
		default:
			return nil, errors.New("FINDING_MUTATION_INVALID")
		}
		return findingReply(finding, input.OperationID), nil
	})
}

func originalFindingDefinition(tx *store.Tx, finding core.Finding, occurrence core.FindingOccurrence, current core.Task) (core.Definition, error) {
	var snapshot string
	err := tx.Row("SELECT body FROM portable_contexts WHERE id=?", occurrence.ContextID).Scan(&snapshot)
	if err != nil && err != sql.ErrNoRows {
		return core.Definition{}, err
	}
	if err == nil {
		var context struct {
			Definition *core.Definition `json:"task_definition"`
		}
		if json.Unmarshal([]byte(snapshot), &context) != nil {
			return core.Definition{}, errors.New("FINDING_SOURCE_UNAVAILABLE")
		}
		if context.Definition != nil && context.Definition.ID == finding.TaskID && context.Definition.Revision == finding.DefinitionRevision {
			return *context.Definition, nil
		}
	}
	if current.Definition.Revision == finding.DefinitionRevision {
		return current.Definition, nil
	}
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT kind,body FROM events WHERE task_id=? AND kind IN ('created','definition_revised') ORDER BY id DESC", finding.TaskID)
	if err != nil {
		return core.Definition{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, body string
		if err = rows.Scan(&kind, &body); err != nil {
			return core.Definition{}, err
		}
		definitions := []core.Definition{}
		if kind == "created" {
			var definition core.Definition
			if json.Unmarshal([]byte(body), &definition) == nil {
				definitions = append(definitions, definition)
			}
		} else {
			var change struct {
				Previous core.Definition `json:"previous"`
				Proposed core.Definition `json:"proposed"`
			}
			if json.Unmarshal([]byte(body), &change) == nil {
				definitions = append(definitions, change.Previous, change.Proposed)
			}
		}
		for _, definition := range definitions {
			if definition.ID == finding.TaskID && definition.Revision == finding.DefinitionRevision {
				return definition, nil
			}
		}
	}
	if err = rows.Err(); err != nil {
		return core.Definition{}, err
	}
	return core.Definition{}, errors.New("FINDING_SOURCE_UNAVAILABLE")
}

func findingOccurrenceTx(tx *store.Tx, findingID, occurrenceID string) (core.FindingOccurrence, error) {
	var body string
	var occurrence core.FindingOccurrence
	query := "SELECT body FROM finding_occurrences WHERE finding_id=? ORDER BY rowid DESC LIMIT 1"
	args := []any{findingID}
	if occurrenceID != "" {
		query = "SELECT body FROM finding_occurrences WHERE finding_id=? AND id=?"
		args = append(args, occurrenceID)
	}
	if err := tx.Row(query, args...).Scan(&body); err != nil {
		return occurrence, errors.New("FINDING_SOURCE_UNAVAILABLE")
	}
	if err := json.Unmarshal([]byte(body), &occurrence); err != nil {
		return occurrence, errors.New("FINDING_INVALID")
	}
	return occurrence, nil
}

func selectedFindingCheck(definition core.Definition, finding core.Finding) (core.Check, error) {
	for _, check := range definition.Checks {
		raw, _ := json.Marshal(check)
		if check.ID == finding.CheckID && store.Hash(raw) == finding.Config {
			return check, nil
		}
	}
	return core.Check{}, errors.New("FINDING_SOURCE_UNAVAILABLE")
}

func repairOriginalRequest(finding core.Finding, occurrence core.FindingOccurrence, reason string) string {
	steps := strings.Join(occurrence.Input.Reproduction, "\n- ")
	return fmt.Sprintf("Finding %s\nSource task %s\nCandidate %s\nRule %s\nLocation %s %s\nExpected\n%s\nObserved\n%s\nReproduction\n- %s\nImpact\n%s\nRepair rationale\n%s\nReport %s\nEvidence %s", finding.ID, finding.TaskID, finding.Candidate, finding.Rule, finding.Location.Path, finding.Location.Selector, occurrence.Input.Expected, occurrence.Input.Actual, steps, occurrence.Input.Impact, reason, occurrence.ReportArtifactID, strings.Join(occurrence.Input.Artifacts, ", "))
}

func (p *portableStore) linkFindingRepair(tx *store.Tx, principal standalonePrincipal, role string, input findingMutationInput, finding core.Finding, source core.Task, at string) (map[string]any, error) {
	occurrence, err := findingOccurrenceTx(tx, finding.ID, input.OccurrenceID)
	if err != nil {
		return nil, err
	}
	definition, err := originalFindingDefinition(tx, finding, occurrence, source)
	if err != nil {
		return nil, err
	}
	sourceCheck, err := selectedFindingCheck(definition, finding)
	if err != nil {
		return nil, err
	}
	parentID := source.Definition.Parent
	if source.EpicMembership != nil {
		parentID = source.EpicMembership.Parent
	}
	originalParent := parentID
	var parent core.Task
	if parentID != "" {
		parent, err = artifactTask(tx, parentID)
		if err != nil {
			return nil, err
		}
		if parent.Status != "open" {
			parentID = ""
		}
	}
	var repair core.Task
	checkID := input.RepairCheckID
	if checkID == "" {
		checkID = finding.CheckID
	}
	if input.RepairTaskID == "" {
		checks := slices.Clone(definition.Checks)
		if !sourceCheck.Required {
			// Policy-owned optional checks retain their original configuration.
			// A distinct required check binds the actual repair acceptance.
			required := sourceCheck
			base := "repair-" + store.Hash([]byte(sourceCheck.ID))[:20]
			required.ID = base
			for suffix := 1; slices.ContainsFunc(checks, func(check core.Check) bool { return check.ID == required.ID }); suffix++ {
				required.ID = fmt.Sprintf("%s-%d", base, suffix)
			}
			required.Required = true
			checks = append(checks, required)
			if input.RepairCheckID == "" {
				checkID = required.ID
			}
		}
		scope := slices.Clone(definition.Scope)
		if len(scope) == 0 {
			scope = slices.Clone(parent.Definition.Scope)
		}
		if len(scope) == 0 && finding.Location.Path != "" {
			scope = []string{finding.Location.Path}
		}
		nonGoals := slices.Clone(definition.NonGoals)
		knowledge := slices.Clone(definition.Knowledge)
		risk := slices.Clone(definition.Risk)
		for _, item := range parent.Definition.NonGoals {
			if !slices.Contains(nonGoals, item) {
				nonGoals = append(nonGoals, item)
			}
		}
		for _, item := range parent.Definition.Knowledge {
			if !slices.Contains(knowledge, item) {
				knowledge = append(knowledge, item)
			}
		}
		for _, item := range parent.Definition.Risk {
			if !slices.Contains(risk, item) {
				risk = append(risk, item)
			}
		}
		target := core.Definition{ID: core.ID("WI-"), Title: "修复 · " + finding.Title, Type: "bug", Intent: "change", Goal: "verified", Source: "finding:" + finding.ID, OriginalRequest: repairOriginalRequest(finding, occurrence, input.Reason), Scope: scope, NonGoals: nonGoals, Acceptance: slices.Clone(definition.Acceptance), Checks: checks, Knowledge: knowledge, Risk: risk, Parent: parentID, FeedbackOf: finding.TaskID, Dependencies: slices.Clone(definition.Dependencies), Priority: definition.Priority, UI: definition.UI, UX: definition.UX, MaxRetries: definition.MaxRetries, MaxSeconds: definition.MaxSeconds}
		op := findingTaskOperation(principal, input, target.ID, "absent")
		repair, err = p.service.WithRemoteOperation(op).CreateRemoteTaskTx(tx, target)
		if err != nil {
			return nil, err
		}
	} else {
		repair, err = artifactTask(tx, input.RepairTaskID)
		if err != nil {
			return nil, errors.New("TASK_NOT_FOUND")
		}
		if input.ExpectedRepairRevision == "" || core.RemoteRevision(repair) != input.ExpectedRepairRevision {
			return nil, errors.New("REVISION_CONFLICT")
		}
		if repair.Status != "open" || repair.Historical || repair.Definition.Type == "epic" || repair.Definition.Intent == "inspect" || repair.Definition.Goal == "report" || repair.Definition.Goal == "analysis" {
			return nil, errors.New("FINDING_REPAIR_INVALID")
		}
		if !repairCoversFinding(definition, finding, repair.Definition) {
			return nil, errors.New("FINDING_REPAIR_SCOPE_MISMATCH")
		}
		if parentID != "" {
			if repair.EpicMembership == nil {
				op := findingTaskOperation(principal, input, repair.Definition.ID, input.ExpectedRepairRevision)
				repair, err = p.service.WithRemoteOperation(op).LinkRemoteEpicTx(tx, parentID, repair.Definition.ID, true, "finding repair")
				if err != nil {
					return nil, err
				}
			} else if repair.EpicMembership.Parent != parentID || !repair.EpicMembership.Required {
				return nil, errors.New("FINDING_REPAIR_SCOPE_MISMATCH")
			}
		}
	}
	var repairCheck core.Check
	for _, check := range repair.Definition.Checks {
		if check.ID == checkID {
			repairCheck = check
		}
	}
	if repairCheck.ID == "" || !repairCheck.Required || core.CheckMeaning(repairCheck) != core.CheckMeaning(sourceCheck) {
		return nil, errors.New("FINDING_REPAIR_CHECK_MISMATCH")
	}
	raw, _ := json.Marshal(repairCheck)
	link := core.FindingRepair{TaskID: repair.Definition.ID, SourceTaskID: finding.TaskID, SourceParent: originalParent, OccurrenceID: occurrence.ID, CheckID: checkID, CheckConfig: store.Hash(raw), Actor: principal.Username, Reason: strings.TrimSpace(input.Reason), Created: at}
	decisionInput := input
	decisionInput.Action = "fix"
	decision := findingDecision(principal, role, decisionInput, finding, at)
	finding.Repair = &link
	finding.Decision = &decision
	finding.Status = "open"
	if err = saveFindingChange(tx, finding, "finding_repair_linked", map[string]any{"finding_id": finding.ID, "source_task_id": finding.TaskID, "repair": link, "decision": decision}, at); err != nil {
		return nil, err
	}
	// Keep the actual repair relation available to the task evolution projection.
	if err = tx.Event(repair.Definition.ID, "finding_repair_origin", map[string]any{"finding_id": finding.ID, "title": finding.Title, "source_task_id": finding.TaskID, "occurrence_id": occurrence.ID}); err != nil {
		return nil, err
	}
	result := findingReply(finding, input.OperationID)
	result["task"], result["task_revision"], result["existing"] = core.RemoteTaskView(repair), core.RemoteRevision(repair), false
	result["next_action"] = "claim_repair"
	return result, nil
}

func findingTaskOperation(principal standalonePrincipal, input findingMutationInput, taskID, expected string) core.RemoteOperation {
	raw, _ := json.Marshal(input)
	key := "finding-repair:" + store.Hash([]byte(principal.UserID+"\n"+input.IdempotencyKey))
	return core.RemoteOperation{ID: input.OperationID, Key: key, RequestHash: store.Hash(raw), TaskID: taskID, Expected: expected, Actor: principal.Username}
}

func candidateRepository(tx *store.Tx, id string) (string, error) {
	var body string
	if err := tx.Row("SELECT body FROM portable_candidates WHERE id=?", id).Scan(&body); err != nil {
		return "", errors.New("CANDIDATE_NOT_FOUND")
	}
	var candidate remote.PortableCandidate
	if json.Unmarshal([]byte(body), &candidate) != nil || candidate.ID != id || remote.ValidatePortableCandidate(candidate) != nil {
		return "", errors.New("CANDIDATE_STORAGE_ERROR")
	}
	return candidate.Repository, nil
}

func (p *portableStore) revalidateFinding(tx *store.Tx, principal standalonePrincipal, input findingMutationInput, finding core.Finding, artifacts *artifactStore, at string) (map[string]any, error) {
	if finding.Repair == nil || input.RepairTaskID != finding.Repair.TaskID {
		return nil, errors.New("FINDING_REPAIR_REQUIRED")
	}
	task, err := p.service.RemoteEvidenceTaskTx(tx, finding.Repair.TaskID, input.ExpectedRepairRevision)
	if err != nil {
		return nil, err
	}
	if input.CandidateID != task.Candidate || task.Candidate == finding.Candidate {
		return nil, errors.New("FINDING_NEW_CANDIDATE_REQUIRED")
	}
	sourceRepository, err := candidateRepository(tx, finding.Candidate)
	if err != nil {
		return nil, err
	}
	repository, err := candidateRepository(tx, task.Candidate)
	if err != nil {
		return nil, err
	}
	if repository != sourceRepository {
		return nil, errors.New("FINDING_REPAIR_SCOPE_MISMATCH")
	}
	var check core.Check
	for _, item := range task.Definition.Checks {
		if item.ID == finding.Repair.CheckID {
			check = item
		}
	}
	raw, _ := json.Marshal(check)
	evidence := core.CurrentEvidence(&task, check)
	if check.ID == "" || store.Hash(raw) != finding.Repair.CheckConfig || evidence == nil || evidence.Result != "pass" || input.EvidenceHash == "" || core.EvidenceRevision(*evidence) != input.EvidenceHash {
		return nil, errors.New("FINDING_RETEST_REQUIRED")
	}
	var total int64
	for _, id := range evidence.Artifacts {
		item, err := findingArtifact(tx, artifacts, id, task.Definition.ID, task.Candidate)
		if err != nil || evidence.ArtifactHashes[id] != item.SHA256 {
			return nil, errors.New("FINDING_EVIDENCE_INVALID")
		}
		total += item.Size
		if total > 64<<20 {
			return nil, errors.New("FINDING_MUTATION_INVALID")
		}
		if _, err = artifacts.read(item); err != nil {
			return nil, errors.New("FINDING_EVIDENCE_INVALID")
		}
	}
	if len(evidence.Artifacts) == 0 {
		return nil, errors.New("FINDING_EVIDENCE_INVALID")
	}
	if finding.Status == "resolved" && finding.Resolution != nil && finding.Resolution.Candidate == task.Candidate && finding.Resolution.EvidenceHash == core.EvidenceRevision(*evidence) && finding.Resolution.DefinitionRevision == task.Definition.Revision && finding.Resolution.PlanRevision == task.Plan.Revision {
		result := findingReply(finding, input.OperationID)
		result["existing"] = true
		return result, nil
	}
	resolution := core.FindingResolution{ID: core.ID("FR-"), FindingID: finding.ID, RepairTaskID: task.Definition.ID, Candidate: task.Candidate, DefinitionRevision: task.Definition.Revision, PlanRevision: task.Plan.Revision, EvidenceHash: core.EvidenceRevision(*evidence), Evidence: *evidence, Actor: principal.Username, Reason: strings.TrimSpace(input.Reason), At: at}
	finding.Resolution = &resolution
	finding.Status = "resolved"
	if err = saveFindingChange(tx, finding, "finding_revalidated", resolution, at); err != nil {
		return nil, err
	}
	result := findingReply(finding, input.OperationID)
	next := task.BaseNext()
	safe := core.RemoteTaskView(task)
	next.Task = &safe
	next.Attempt = nil
	result["task_next"] = next
	if task.Status == "open" {
		result["next_action"] = "continue_required_checks"
	}
	return result, nil
}

func (h *standaloneHandler) mutateFinding(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, role, id, action string, p *portableStore, artifacts *artifactStore) {
	if role != "admin" && role != "executor" && role != "reviewer" {
		standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	if action == "repair" && role != "admin" && role != "executor" {
		standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	if err := p.service.Store.Flush(); err != nil {
		standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
		return
	}
	var input findingMutationInput
	if decodePortable(r, &input, p) != nil {
		standaloneFail(w, http.StatusBadRequest, "FINDING_MUTATION_INVALID")
		return
	}
	result, err := p.changeFinding(principal, role, id, action, input, artifacts)
	if err != nil {
		findingLifecycleError(w, err)
		return
	}
	if err = p.service.Store.Flush(); err != nil {
		standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
		return
	}
	standaloneReply(w, http.StatusOK, result)
}

func findingLifecycleError(w http.ResponseWriter, err error) {
	code, _, _ := strings.Cut(err.Error(), ":")
	switch code {
	case "FINDING_MUTATION_INVALID", "FINDING_DECISION_INVALID":
		standaloneFail(w, http.StatusBadRequest, code)
	case "FINDING_REVISION_CONFLICT", "FINDING_REPAIR_EXISTS", "FINDING_REPAIR_INVALID", "FINDING_REPAIR_SCOPE_MISMATCH", "FINDING_REPAIR_CHECK_MISMATCH", "FINDING_REPAIR_REQUIRED", "FINDING_NEW_CANDIDATE_REQUIRED", "FINDING_RETEST_REQUIRED":
		standaloneFail(w, http.StatusConflict, code)
	case "POLICY_CHECK_CONFLICT", "POLICY_SCHEMA_UNSUPPORTED", "TASK_EXISTS", "TASK_ID_DELETED", "DEPENDENCY_MISSING", "DEPENDENCY_CYCLE", "EPIC_NOT_OPEN", "EPIC_MISSING", "RUNNING_ATTEMPT", "TASK_ALREADY_LINKED", "EPIC_CHILD_INVALID", "INVALID_DEFINITION", "INVALID_PRESET", "UNKNOWN_ACCEPTANCE":
		standaloneFail(w, http.StatusConflict, code)
	case "FINDING_SOURCE_UNAVAILABLE":
		standaloneFail(w, http.StatusGone, code)
	default:
		portableError(w, err)
	}
}

func repairCoversFinding(source core.Definition, finding core.Finding, repair core.Definition) bool {
	required := source.Scope
	if len(required) == 0 && finding.Location.Path != "" {
		required = []string{finding.Location.Path}
	}
	for _, item := range required {
		covered := false
		for _, scope := range repair.Scope {
			prefix := path.Clean(strings.ReplaceAll(scope, "\\", "/"))
			itemPath := path.Clean(strings.ReplaceAll(item, "\\", "/"))
			if prefix == "." || itemPath == prefix || strings.HasPrefix(itemPath, prefix+"/") {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

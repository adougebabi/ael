package core

import (
	"ael/internal/store"
	"encoding/json"
)

// Finding decisions describe investigation outcomes. Task acceptance continues
// to use CurrentEvidence and the original task gates.
type FindingDecision struct {
	ID              string `json:"id"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	Actor           string `json:"actor"`
	Role            string `json:"role"`
	Authority       string `json:"authority"`
	FindingRevision string `json:"finding_revision"`
	TaskRevision    string `json:"task_revision"`
	At              string `json:"created_at"`
}

type FindingRepair struct {
	TaskID       string `json:"task_id"`
	SourceTaskID string `json:"source_task_id"`
	SourceParent string `json:"source_parent,omitempty"`
	OccurrenceID string `json:"occurrence_id"`
	CheckID      string `json:"check_id"`
	CheckConfig  string `json:"config_sha256"`
	Actor        string `json:"actor"`
	Reason       string `json:"reason"`
	Created      string `json:"created_at"`
}

type FindingResolution struct {
	ID                 string   `json:"id"`
	FindingID          string   `json:"finding_id"`
	RepairTaskID       string   `json:"repair_task_id"`
	Candidate          string   `json:"candidate_id"`
	DefinitionRevision int      `json:"definition_revision"`
	PlanRevision       int      `json:"plan_revision"`
	EvidenceHash       string   `json:"evidence_sha256"`
	Evidence           Evidence `json:"evidence"`
	Actor              string   `json:"actor"`
	Reason             string   `json:"reason"`
	At                 string   `json:"created_at"`
}

func FindingRevision(finding Finding) string {
	raw, _ := json.Marshal(finding)
	return store.Hash(raw)
}

func FindingDecisionStatus(action string) (string, error) {
	switch action {
	case "fix":
		return "open", nil
	case "non_issue":
		return "dismissed", nil
	case "accepted_tradeoff":
		return "accepted_tradeoff", nil
	case "more_evidence":
		return "needs_evidence", nil
	default:
		return "", Error("FINDING_DECISION_INVALID", "select fix, non_issue, accepted_tradeoff or more_evidence")
	}
}

// CheckMeaning preserves every declared requirement while permitting an
// explicitly mapped check ID and a required repair check.
func CheckMeaning(check Check) string {
	check.ID = ""
	check.Required = true
	check.Method = EffectiveCheckMethod(check)
	raw, _ := json.Marshal(check)
	return store.Hash(raw)
}

func EvidenceRevision(evidence Evidence) string {
	evidence.ArtifactStatus = nil
	raw, _ := json.Marshal(evidence)
	return store.Hash(raw)
}

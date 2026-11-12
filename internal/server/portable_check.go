package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"errors"
	"time"
)

// A client-reported check links an existing report to the candidate and
// configuration observed by that client. It is not an acceptance gate pass.
type portableCheckInput struct {
	TaskID           string                    `json:"task_id"`
	ExpectedRevision string                    `json:"expected_revision"`
	CandidateID      string                    `json:"candidate_id"`
	CheckID          string                    `json:"check_id"`
	ArtifactID       string                    `json:"artifact_id"`
	InputsKnown      bool                      `json:"inputs_known"`
	OperationID      string                    `json:"operation_id"`
	IdempotencyKey   string                    `json:"idempotency_key"`
	ContextID        string                    `json:"context_id,omitempty"`
	Source           *core.FindingReportSource `json:"source,omitempty"`
	Findings         []core.FindingInput       `json:"findings,omitempty"`
}

func (p *portableStore) reportCheck(principal standalonePrincipal, contextID string, input portableCheckInput, artifacts *artifactStore, trustedHuman bool) (map[string]any, error) {
	if !standaloneID.MatchString(contextID) || !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.CheckID) || (input.ArtifactID != "" && !standaloneID.MatchString(input.ArtifactID)) || (input.ArtifactID == "" && len(input.Findings) == 0) || (input.ContextID != "" && input.ContextID != contextID) || len(input.Findings) > 100 || !portableDigestID(input.CandidateID) || input.ExpectedRevision == "" {
		return nil, errors.New("CANDIDATE_INVALID")
	}
	return p.mutate(principal, "report-check:"+contextID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var body, taskID, candidateID string
		if err := tx.Row("SELECT body,task_id,candidate_id FROM portable_contexts WHERE id=?", contextID).Scan(&body, &taskID, &candidateID); err != nil {
			return nil, errors.New("CONTEXT_NOT_FOUND")
		}
		var context map[string]any
		if json.Unmarshal([]byte(body), &context) != nil {
			return nil, errors.New("CANDIDATE_STORAGE_ERROR")
		}
		if taskID != input.TaskID || candidateID != input.CandidateID || context["task_revision"] != input.ExpectedRevision || (input.InputsKnown && context["inputs_known"] != true) {
			return nil, errors.New("REVISION_CONFLICT")
		}
		task, err := artifactTask(tx, taskID)
		if err != nil {
			return nil, errors.New("TASK_NOT_FOUND")
		}
		if core.RemoteRevision(task) != input.ExpectedRevision {
			return nil, errors.New("REVISION_CONFLICT")
		}
		var latest string
		if err = tx.Row("SELECT candidate_id FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", taskID).Scan(&latest); err != nil || latest != candidateID {
			return nil, errors.New("REVISION_CONFLICT")
		}
		var check *core.Check
		for i := range task.Definition.Checks {
			if task.Definition.Checks[i].ID == input.CheckID {
				check = &task.Definition.Checks[i]
				break
			}
		}
		if check == nil {
			return nil, errors.New("CHECK_NOT_FOUND")
		}
		config, err := json.Marshal(check)
		if err != nil {
			return nil, err
		}
		var artifact artifactObject
		if input.ArtifactID != "" {
			var err error
			artifact, err = scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", input.ArtifactID))
			if err != nil || artifact.Status != "finalized" || artifact.TaskID != taskID || artifact.Uploader != principal.Username || artifact.Source != "context-check:"+contextID+":"+input.CheckID {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			contextCreated, ok := context["created_at"].(string)
			contextTime, contextErr := time.Parse(time.RFC3339Nano, contextCreated)
			artifactTime, artifactErr := time.Parse(time.RFC3339Nano, artifact.Created)
			if !ok || contextErr != nil || artifactErr != nil || artifactTime.Before(contextTime) {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			if _, err = artifacts.read(artifact); err != nil {
				return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
			}
		}
		id, err := standaloneRandom("check_")
		if err != nil {
			return nil, err
		}
		created := time.Now().UTC().Format(time.RFC3339Nano)
		result := map[string]any{"id": id, "context_id": contextID, "task_id": taskID, "task_revision": input.ExpectedRevision, "candidate_id": candidateID, "check_id": input.CheckID, "config_sha256": store.Hash(config), "artifact_id": artifact.ID, "artifact_sha256": artifact.SHA256, "inputs_known": input.InputsKnown, "actor": principal.Username, "created_at": created, "attestation": "client_reported", "gate_effect": "none", "reusable": false, "operation_id": input.OperationID}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO portable_checks(id,context_id,task_id,candidate_id,check_id,artifact_id,body,created) VALUES(?,?,?,?,?,?,?,?)", id, contextID, taskID, candidateID, input.CheckID, artifact.ID, string(raw), created); err != nil {
			return nil, err
		}
		if len(input.Findings) > 0 {
			findings, err := p.recordFindings(tx, principal, task, *check, contextID, id, input, artifact, artifacts, trustedHuman, created)
			if err != nil {
				return nil, err
			}
			result["findings"] = findings
			result["source"] = findingsSource(input, *check, principal)
			enriched, err := json.Marshal(result)
			if err != nil {
				return nil, err
			}
			if _, err = tx.Exec("UPDATE portable_checks SET body=? WHERE id=?", string(enriched), id); err != nil {
				return nil, err
			}
			if err = tx.Event(taskID, "findings_reported", map[string]any{"check_report": id, "candidate": candidateID, "count": len(findings), "actor": principal.Username, "gate_effect": "none"}); err != nil {
				return nil, err
			}
		}
		return result, nil
	})
}

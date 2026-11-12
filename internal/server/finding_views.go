package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
)

// Finding identity remains version-bound. Lineage adds only exact-location
// relationships within a task or an explicitly associated repair task.
func findingPredecessor(tx *store.Tx, task core.Task, check core.Check, config, candidate string, input core.FindingInput) (string, string, error) {
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM findings WHERE task_id=? OR json_extract(body,'$.repair.task_id')=? ORDER BY first_seen DESC,id", task.Definition.ID, task.Definition.ID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return "", "", err
		}
		var previous core.Finding
		if err = json.Unmarshal([]byte(body), &previous); err != nil {
			return "", "", err
		}
		if previous.TaskID == task.Definition.ID && previous.Candidate == candidate && previous.DefinitionRevision == task.Definition.Revision && previous.PlanRevision == task.Plan.Revision {
			continue
		}
		matches := previous.CheckID == check.ID && previous.Config == config
		if previous.Repair != nil && previous.Repair.TaskID == task.Definition.ID {
			matches = matches || (previous.Repair.CheckID == check.ID && previous.Repair.CheckConfig == config)
		}
		if !matches || previous.Rule != input.Rule || previous.Route != input.Route || previous.Kind != input.Kind || previous.Location != input.Location {
			continue
		}
		root := previous.LineageID
		if root == "" {
			root = previous.ID
		}
		return previous.ID, root, nil
	}
	return "", "", rows.Err()
}

func (p *portableStore) findingLifecycleView(finding core.Finding, view map[string]any, artifacts *artifactStore, detail bool, available map[string]string) error {
	if finding.Repair != nil {
		task, err := p.service.Get(finding.Repair.TaskID)
		if err != nil {
			view["repair_state"] = "unavailable"
		} else {
			var observed, observedAt string
			observationErr := p.service.Store.DB.QueryRow("SELECT candidate_id,created FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", task.Definition.ID).Scan(&observed, &observedAt)
			if observationErr != nil && observationErr != sql.ErrNoRows {
				return observationErr
			}
			candidateCurrent := reviewObservationMatches(task, observed, observedAt)
			view["repair_candidate_current"] = candidateCurrent
			check := core.Check{}
			for _, item := range task.Definition.Checks {
				if item.ID == finding.Repair.CheckID {
					check = item
				}
			}
			current := core.CurrentEvidence(&task, check)
			state := "repair_pending"
			if task.Candidate != "" && task.Candidate != finding.Candidate {
				state = "awaiting_retest"
			}
			if !candidateCurrent {
				state = "candidate_changed"
			}
			view["repair_state"] = state
			view["repair_task"] = map[string]any{"id": task.Definition.ID, "title": task.Definition.Title, "status": task.Status, "paused": task.Paused, "candidate_id": task.Candidate, "definition_revision": task.Definition.Revision, "plan_revision": task.Plan.Revision, "revision": core.RemoteRevision(task), "check": check.ID, "current_evidence": current}
			if current != nil {
				hash := core.EvidenceRevision(*current)
				readable := true
				for _, id := range current.Artifacts {
					key := id + ":" + current.ArtifactHashes[id]
					status, known := available[key]
					if !known {
						object, err := artifacts.get(id)
						status = "unavailable"
						if err == nil && object.Status == "finalized" && object.SHA256 == current.ArtifactHashes[id] {
							if _, err = artifacts.read(object); err == nil {
								status = "finalized"
							}
						}
						available[key] = status
					}
					readable = readable && status == "finalized"
				}
				raw, _ := json.Marshal(check)
				canRevalidate := candidateCurrent && task.Candidate != "" && task.Candidate != finding.Candidate && !task.Historical && task.Status != "cancelled" && current.Result == "pass" && store.Hash(raw) == finding.Repair.CheckConfig && len(current.Artifacts) > 0 && readable
				view["repair_evidence_sha256"], view["repair_can_revalidate"] = hash, canRevalidate
				if finding.Resolution != nil {
					validity := "historical"
					if candidateCurrent && finding.Resolution.Candidate == task.Candidate && finding.Resolution.DefinitionRevision == task.Definition.Revision && finding.Resolution.PlanRevision == task.Plan.Revision && finding.Resolution.EvidenceHash == hash {
						validity = "current"
						if !readable {
							validity = "unavailable"
						}
					}
					view["resolution_validity"] = validity
					if finding.Status == "resolved" && validity == "current" {
						view["repair_state"] = "verified"
					}
				}
			} else if finding.Resolution != nil {
				view["resolution_validity"] = "historical"
			}
		}
	}
	if !detail {
		return nil
	}
	rows, err := p.service.Store.DB.Query("SELECT id,kind,body,created FROM finding_history WHERE finding_id=? ORDER BY rowid", finding.ID)
	if err != nil {
		return err
	}
	history := []map[string]any{}
	for rows.Next() {
		var id, kind, body, created string
		if err = rows.Scan(&id, &kind, &body, &created); err != nil {
			break
		}
		var data any
		if err = json.Unmarshal([]byte(body), &data); err != nil {
			break
		}
		history = append(history, map[string]any{"id": id, "kind": kind, "data": data, "created_at": created})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	view["history"] = history
	root := finding.LineageID
	if root == "" {
		root = finding.ID
	}
	rows, err = p.service.Store.DB.Query("SELECT body FROM findings WHERE id=? OR json_extract(body,'$.lineage_id')=? ORDER BY first_seen,id", root, root)
	if err != nil {
		return err
	}
	related := []map[string]any{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			break
		}
		var entity core.Finding
		if err = json.Unmarshal([]byte(body), &entity); err != nil {
			break
		}
		if entity.ID != finding.ID {
			related = append(related, map[string]any{"id": entity.ID, "task_id": entity.TaskID, "title": entity.Title, "candidate_id": entity.Candidate, "status": entity.Status, "recurrence_of": entity.RecurrenceOf})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	view["related_findings"] = related
	return nil
}

// Context feedback is diagnostic material from an immutable appearance. The
// task definition and its own accepted input remain the execution authority.
func (p *portableStore) findingContextSources(task string) ([]map[string]any, error) {
	rows, err := p.service.Store.DB.Query("SELECT f.body,o.body FROM findings f JOIN finding_occurrences o ON o.id=json_extract(f.body,'$.repair.occurrence_id') WHERE json_extract(f.body,'$.repair.task_id')=? ORDER BY f.id", task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := []map[string]any{}
	for rows.Next() {
		var findingBody, occurrenceBody string
		if err = rows.Scan(&findingBody, &occurrenceBody); err != nil {
			return nil, err
		}
		var finding core.Finding
		var occurrence core.FindingOccurrence
		if err = json.Unmarshal([]byte(findingBody), &finding); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(occurrenceBody), &occurrence); err != nil {
			return nil, err
		}
		sources = append(sources, map[string]any{"finding_id": finding.ID, "source_task_id": finding.TaskID, "original_candidate": finding.Candidate, "occurrence_id": occurrence.ID, "input": occurrence.Input, "source": occurrence.Source, "report_artifact_id": occurrence.ReportArtifactID, "evidence": occurrence.Evidence, "diagnostic_only": true, "evidence_status": "report_time_snapshot"})
	}
	return sources, rows.Err()
}

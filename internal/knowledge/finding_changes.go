package knowledge

import (
	"ael/internal/core"
	"encoding/json"
	"slices"
)

// Finding relations enrich actual task deliveries. Intake, scan repetition and
// adjudication do not manufacture engineering progress entries.
func findingChangeFacts(service *core.Service, tasks map[string]core.Task, changes map[string]*Change) error {
	rows, err := service.Store.DB.Query("SELECT task_id,kind,body FROM events WHERE kind IN ('finding_repair_origin','finding_revalidated') ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	type origin struct {
		Finding string `json:"finding_id"`
		Title   string `json:"title"`
		Task    string `json:"source_task_id"`
	}
	origins := map[string]origin{}
	seenEvidence := map[string]bool{}
	for rows.Next() {
		var taskID, kind, body string
		if err = rows.Scan(&taskID, &kind, &body); err != nil {
			return err
		}
		change := changes["task:"+taskID]
		if kind == "finding_repair_origin" {
			var source origin
			if err = json.Unmarshal([]byte(body), &source); err != nil {
				return err
			}
			origins[source.Finding] = source
			if change == nil {
				continue
			}
			reference := ChangeReference{ID: source.Finding, Title: source.Title, Kind: "finding", TaskID: source.Task}
			if !slices.ContainsFunc(change.References, func(item ChangeReference) bool { return item.Kind == reference.Kind && item.ID == reference.ID }) {
				change.References = append(change.References, reference)
			}
			if sourceTask, ok := tasks[source.Task]; ok {
				if !slices.ContainsFunc(change.References, func(item ChangeReference) bool { return item.Kind == "task" && item.ID == source.Task }) {
					change.References = append(change.References, ChangeReference{ID: source.Task, Title: sourceTask.Definition.Title, Kind: "task"})
				}
			}
			continue
		}
		if change == nil {
			continue
		}
		var result core.FindingResolution
		if err = json.Unmarshal([]byte(body), &result); err != nil {
			return err
		}
		if result.RepairTaskID != taskID || result.Evidence.Result != "pass" || result.Candidate != result.Evidence.Candidate || result.DefinitionRevision != result.Evidence.Revision || result.PlanRevision != result.Evidence.PlanRevision || result.EvidenceHash != core.EvidenceRevision(result.Evidence) {
			continue
		}
		key := taskID + ":" + result.EvidenceHash
		if seenEvidence[key] {
			continue
		}
		seenEvidence[key] = true
		source := origins[result.FindingID]
		title := source.Title
		if title == "" {
			title = result.FindingID
		}
		fact := ChangeFact{Task: taskID, Title: "复验 · " + title, Result: "pass", Content: result.Reason, Artifacts: slices.Clone(result.Evidence.Artifacts), Candidate: result.Candidate, Finding: result.FindingID}
		if !result.Evidence.At.IsZero() {
			at := result.Evidence.At
			fact.At = &at
		}
		change.Facts = append(change.Facts, fact)
	}
	return rows.Err()
}

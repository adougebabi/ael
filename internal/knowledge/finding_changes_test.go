package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"testing"
	"time"
)

func TestGroupedFindingChangesRetainOriginAndDistinctRetestEvidence(t *testing.T) {
	database, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := &core.Service{Store: database, Root: t.TempDir(), Active: true}
	completed := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for _, task := range []core.Task{
		{Definition: core.Definition{ID: "scan", Title: "Original inspection", Scope: []string{"screen.html"}}, Status: "completed", Result: "report_delivered_with_findings", Completed: &completed},
		{Definition: core.Definition{ID: "repair", Title: "Actual repair", Scope: []string{"screen.html"}}, Status: "completed", Result: "verified", Candidate: "candidate-c", Completed: &completed},
	} {
		raw, _ := json.Marshal(task)
		if _, err = database.DB.Exec("INSERT INTO tasks(id,body,status,step,updated,completed) VALUES(?,?,?,?,?,?)", task.Definition.ID, string(raw), task.Status, "done", completed.Format(time.RFC3339Nano), completed.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	atB := completed.Add(-time.Hour)
	atC := completed.Add(-time.Minute)
	evidenceB := core.Evidence{Check: "visual", Candidate: "candidate-b", Revision: 1, PlanRevision: 1, Result: "pass", Artifacts: []string{"image-b"}, At: atB}
	evidenceC := core.Evidence{Check: "visual", Candidate: "candidate-c", Revision: 1, PlanRevision: 1, Result: "pass", Artifacts: []string{"image-c"}, At: atC}
	err = database.Write(func(tx *store.Tx) error {
		if err := tx.Event("repair", "finding_repair_origin", map[string]string{"finding_id": "finding", "title": "Original defect", "source_task_id": "scan"}); err != nil {
			return err
		}
		for index, evidence := range []core.Evidence{evidenceB, evidenceC, evidenceC} {
			result := core.FindingResolution{ID: core.ID("FR-"), FindingID: "finding", RepairTaskID: "repair", Candidate: evidence.Candidate, DefinitionRevision: 1, PlanRevision: 1, EvidenceHash: core.EvidenceRevision(evidence), Evidence: evidence, Reason: "Actual retest basis", At: completed.Add(time.Duration(index) * time.Minute).Format(time.RFC3339Nano)}
			if err := tx.Event("repair", "finding_revalidated", result); err != nil {
				return err
			}
		}
		// Repeated diagnostic observations cannot add delivery facts.
		return tx.Event("repair", "findings_reported", map[string]any{"count": 100})
	})
	if err != nil {
		t.Fatal(err)
	}
	records := []Record{{ID: "decision", Title: "Grouped repair", Kind: "decision", Status: "accepted", Summary: "Explicitly grouped work", Tasks: []string{"scan", "repair"}, Paths: []string{"screen.html"}}}
	page, err := ChangesWithRecords(service, records, BrowseQuery{}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "knowledge:decision" {
		t.Fatal(page)
	}
	findingReferences, tests := 0, []ChangeFact{}
	for _, reference := range page.Items[0].References {
		if reference.Kind == "finding" && reference.ID == "finding" && reference.TaskID == "scan" {
			findingReferences++
		}
	}
	for _, fact := range page.Items[0].Facts {
		if fact.Finding == "finding" {
			tests = append(tests, fact)
		}
	}
	if findingReferences != 1 || len(tests) != 2 {
		t.Fatal("aggregation lost or duplicated finding facts", page.Items[0])
	}
	if tests[0].At == nil || !tests[0].At.Equal(atB) || tests[1].At == nil || !tests[1].At.Equal(atC) {
		t.Fatal("acknowledgement time replaced actual test time", tests)
	}
}

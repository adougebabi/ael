package server

import (
	"ael/internal/core"
	"net/http"
	"testing"
)

func TestApprovedLegacyImportCanResumeAsExecutorWithoutAnotherHumanAction(t *testing.T) {
	f := newArtifactFixture(t)
	archive := authorityArchiveFixture("legacy-approved-resume")
	original := archive.Tasks[0].Snapshot
	original.Status, original.Step = "open", "define"
	archive.Tasks[0] = core.ArchiveTask(original, archive.Tasks[0].MappedID)
	archive.Tasks[0].Recovery = nil // Actual pre-UC-01 exports lack this field.
	activateReconciliationFixture(t, f, archive)
	id := archive.Tasks[0].MappedID
	service, _ := f.s.projectService("shared")
	task, err := service.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/projects/shared/tasks/" + id
	approval := map[string]any{"operation_id": "restore-reviewed", "idempotency_key": "restore-reviewed", "expected_revision": core.RemoteRevision(task), "kind": "restore", "decision": "approve", "reason": "owner reviewed this exact legacy scope", "definition_revision": task.Definition.Revision, "plan_revision": task.Plan.Revision, "candidate": ""}
	reviewed := standaloneJSON(t, standaloneRequest(t, f.h, "POST", path+"/confirm", approval, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	next := standaloneJSON(t, standaloneRequest(t, f.h, "GET", path+"/next", nil, nil, "", f.uploader, "", ""), http.StatusOK)
	if next["recovery_action"] != "resume" || next["next_action"] != "wait_environment" {
		t.Fatal("approved import lacks executor recovery guidance", next)
	}
	dashboard := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/dashboard/task?id="+id, nil, nil, "", f.uploader, "", ""), http.StatusOK)
	if dashboard["summary"].(map[string]any)["recovery_action"] != "resume" {
		t.Fatal("dashboard and CLI disagree", dashboard)
	}
	input := map[string]any{"operation_id": "executor-resume", "idempotency_key": "executor-resume", "expected_revision": reviewed["revision"], "ack_stopped": false}
	first := standaloneRequest(t, f.h, "POST", path+"/resume", input, nil, "", f.uploader, "", "1")
	standaloneJSON(t, first, http.StatusOK)
	replay := standaloneRequest(t, f.h, "POST", path+"/resume", input, nil, "", f.uploader, "", "1")
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatal("resume retry changed original receipt")
	}
	next = standaloneJSON(t, standaloneRequest(t, f.h, "GET", path+"/next", nil, nil, "", f.uploader, "", ""), http.StatusOK)
	if next["next_action"] != "continue" || next["recovery_action"] != nil {
		t.Fatal("resume did not produce runnable next", next)
	}
	current, err := service.Get(id)
	if err != nil || current.Paused || current.Confirmation != nil || len(current.Approvals) != 1 || current.Approvals[0].Actor != "owner" || len(current.Attempts) != 0 || len(current.Evidence) != 0 {
		t.Fatal("technical resume changed authority, claimed work or revived evidence", current, err)
	}
}

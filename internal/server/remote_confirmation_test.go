package server

import (
	"ael/internal/core"
	"net/http"
	"strings"
	"testing"
)

func TestRemoteTaskConfirmationRequiresHumanAndExactVersion(t *testing.T) {
	f, task, _ := reviewFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	// An accepted strict-plan candidate can exist before implementation has
	// supplied the task candidate for verification.
	task.Confirmation = &core.Confirmation{Kind: "plan", Reason: "review the isolated plan", Revision: task.Definition.Revision, PlanRevision: task.Plan.Revision, Candidate: task.Candidate}
	task.Candidate, task.Step = "", "execute"
	if err := service.ImportTask(task); err != nil {
		t.Fatal(err)
	}
	task, err = service.Get(task.Definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/projects/shared/tasks/" + task.Definition.ID + "/confirm"
	body := map[string]any{
		"operation_id": "confirm-plan", "idempotency_key": "confirm-plan", "expected_revision": core.RemoteRevision(task),
		"kind": "plan", "decision": "approve", "reason": "Reviewed the exact isolated plan",
		"candidate": task.Confirmation.Candidate, "definition_revision": task.Confirmation.Revision, "plan_revision": task.Confirmation.PlanRevision,
	}
	request := func() (int, string) {
		response := standaloneRequest(t, f.h, "POST", path, body, f.admin, f.csrf, "", "http://server.test", "1")
		return response.Code, response.Body.String()
	}
	if w := standaloneRequest(t, f.h, "POST", path, body, nil, "", f.uploader, "", "1"); w.Code != http.StatusForbidden {
		t.Fatalf("executor token confirmed: %d %s", w.Code, w.Body.String())
	}
	reader, csrf := standaloneLogin(t, f.h, "reader", "fixture-user-password")
	if w := standaloneRequest(t, f.h, "POST", path, body, reader, csrf, "", "http://server.test", "1"); w.Code != http.StatusForbidden {
		t.Fatalf("reader confirmed: %d", w.Code)
	}
	body["plan_revision"] = task.Confirmation.PlanRevision + 1
	if status, response := request(); status != http.StatusConflict || !strings.Contains(response, "CONFIRMATION_VERSION_MISMATCH") {
		t.Fatalf("stale plan: %d %s", status, response)
	}
	body["plan_revision"] = task.Confirmation.PlanRevision
	body["expected_revision"] = "stale"
	if status, response := request(); status != http.StatusConflict || !strings.Contains(response, "REVISION_CONFLICT") {
		t.Fatalf("stale task: %d %s", status, response)
	}
	body["expected_revision"] = core.RemoteRevision(task)
	status, first := request()
	if status != http.StatusOK {
		t.Fatalf("approval: %d %s", status, first)
	}
	if status, replay := request(); status != http.StatusOK || replay != first {
		t.Fatalf("replay: %d %s", status, replay)
	}
	approved, err := service.Get(task.Definition.ID)
	if err != nil || approved.Confirmation != nil || len(approved.Approvals) != 1 || !approved.Plan.Approved || approved.Approvals[0].Actor != "owner" {
		t.Fatal("confirmation did not commit once for the authenticated actor", approved, err)
	}
}

package server

import (
	"ael/internal/core"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Synthetic failure state isolates the adapter. Evidence is uploaded through
// the authenticated artifact API and every review uses the public task route.
func TestRemoteFailureReviewGuardsAndReplay(t *testing.T) {
	f, task, artifact := reviewFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	task.Paused, task.ActiveFailure = true, "execute"
	task.FailureCycles = map[string]core.FailureCycle{"execute": {Unit: "execute", Count: 2, State: "review_required"}}
	if err = service.ImportTask(task); err != nil {
		t.Fatal(err)
	}
	task, err = service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	review := core.FailureReview{Actor: "forged", Tried: "reproduced failure", Excluded: "missing input", Hypothesis: "configuration", Next: "check configuration", Recovery: "retain original input", Evidence: []string{artifact}}
	body := remoteFailureReviewInput{Review: review, ExpectedRevision: core.RemoteRevision(task), OperationID: "review-failure", IdempotencyKey: "review-failure"}
	endpoint := "/api/v1/projects/shared/tasks/task/review-failure"
	for _, test := range []struct {
		name, token, path, revision, code string
		status                            int
	}{
		{"reader", f.reader, endpoint, body.ExpectedRevision, "FORBIDDEN", http.StatusForbidden},
		{"other project", f.other, endpoint, body.ExpectedRevision, "PROJECT_NOT_FOUND", http.StatusNotFound},
		{"stale", f.uploader, endpoint, "stale", "REVISION_CONFLICT", http.StatusConflict},
		{"missing task", f.uploader, "/api/v1/projects/shared/tasks/missing/review-failure", body.ExpectedRevision, "TASK_NOT_FOUND", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := body
			input.ExpectedRevision = test.revision
			w := standaloneRequest(t, f.h, "POST", test.path, input, nil, "", test.token, "", "1")
			if w.Code != test.status || !strings.Contains(w.Body.String(), test.code) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, update := range []struct{ sql, code string }{
		{"UPDATE artifact_objects SET task_id='another' WHERE id=?", "ARTIFACT_REQUIRED"},
		{"UPDATE artifact_objects SET task_id='task',status='uploading' WHERE id=?", "ARTIFACT_REQUIRED"},
		{"UPDATE artifact_objects SET status='finalized',size=67108865 WHERE id=?", "ARTIFACT_INVALID"},
	} {
		if _, err = service.Store.DB.Exec(update.sql, artifact); err != nil {
			t.Fatal(err)
		}
		w := standaloneRequest(t, f.h, "POST", endpoint, body, nil, "", f.uploader, "", "1")
		if w.Code < 400 || w.Code >= 500 || !strings.Contains(w.Body.String(), update.code) {
			t.Fatalf("invalid evidence: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err = service.Store.DB.Exec("UPDATE artifact_objects SET size=? WHERE id=?", len(fixturePNG(t)), artifact); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Get("task")
	if core.RemoteRevision(before) != body.ExpectedRevision || before.FailureCycles["execute"].State != "review_required" {
		t.Fatal("rejected review changed task")
	}
	first := standaloneRequest(t, f.h, "POST", endpoint, body, nil, "", f.uploader, "", "1")
	standaloneJSON(t, first, http.StatusOK)
	again := standaloneRequest(t, f.h, "POST", endpoint, body, nil, "", f.uploader, "", "1")
	if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
		t.Fatalf("replay changed receipt: %d %s", again.Code, again.Body.String())
	}
	current, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	cycle := current.FailureCycles["execute"]
	if cycle.Count != 2 || cycle.State != "reviewed" || !current.Paused || cycle.Review.Actor != "upload" {
		t.Fatalf("incorrect review: %+v", cycle)
	}
	cycle.State, cycle.Count = "blocked", 3
	current.FailureCycles["execute"] = cycle
	raw, _ := json.Marshal(current)
	if _, err = service.Store.DB.Exec("UPDATE tasks SET body=? WHERE id='task'", string(raw)); err != nil {
		t.Fatal(err)
	}
	body.ExpectedRevision, body.OperationID, body.IdempotencyKey = core.RemoteRevision(current), "repeat-review", "repeat-review"
	w := standaloneRequest(t, f.h, "POST", endpoint, body, nil, "", f.uploader, "", "1")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ROOT_CAUSE_NEW_EVIDENCE_REQUIRED") {
		t.Fatalf("old evidence reused: %d %s", w.Code, w.Body.String())
	}
}

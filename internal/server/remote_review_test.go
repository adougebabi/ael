package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The task-state setup is synthetic. All review writes go through the actual
// authenticated HTTP handler, SQLite transaction and shared submission kernel.
func reviewFixture(t *testing.T) (artifactFixture, core.Task, string) {
	t.Helper()
	f := newArtifactFixture(t)
	candidate := portableFixtureCandidate(t)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", map[string]any{"task_id": "task", "expected_revision": f.revision, "checkout_id": "checkout", "workspace_id": "workspace", "runner_id": "runner", "candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	data := fixturePNG(t)
	started := artifactStart(t, f, "actual.png", "image/png", data, "image")
	upload := started["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "image", "idempotency_key": "image"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	task.Definition.Checks = []core.Check{{ID: "visual", Kind: "ui", Method: "human", Required: true, Covers: []string{"result"}}, {ID: "independent", Kind: "review", Method: "human", Required: true, Covers: []string{"result"}}}
	task.Step, task.Candidate, task.CandidateWorkspace = "verify", candidate.ID, "remote:runner/workspace"
	if err = service.ImportTask(task); err != nil {
		t.Fatal(err)
	}
	// Bind the finalized fixture object to the synthetic accepted candidate.
	if _, err = service.Store.DB.Exec("UPDATE artifact_objects SET candidate=? WHERE id=?", candidate.ID, started["artifact_id"]); err != nil {
		t.Fatal(err)
	}
	task, err = service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	return f, task, started["artifact_id"].(string)
}

func reviewBody(task core.Task, artifact, result string) map[string]any {
	return map[string]any{"action": "review_result", "id": task.Definition.ID, "unit": "visual", "executor": "forged-name", "reason": "Actual visual observation", "recovery": "Correct layout and recheck", "result": result, "artifacts": []string{artifact}, "candidate": task.Candidate, "revision": task.Definition.Revision, "plan_revision": task.Plan.Revision, "request_key": "review"}
}

func TestRemoteHumanReviewOutcomesAndAtomicReplay(t *testing.T) {
	for _, outcome := range []string{"pass", "fail", "blocked"} {
		t.Run(outcome, func(t *testing.T) {
			f, task, image := reviewFixture(t)
			body := reviewBody(task, image, outcome)
			request := func() string {
				w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
				standaloneJSON(t, w, http.StatusOK)
				return w.Body.String()
			}
			first := request()
			if again := request(); again != first {
				t.Fatal("replay changed receipt")
			}
			service, _ := f.s.projectService("shared")
			current, err := service.Get("task")
			if err != nil {
				t.Fatal(err)
			}
			if len(current.Attempts) != 1 || len(current.Evidence) != 1 || current.Evidence[0].Result != outcome || current.Evidence[0].Source != "human:owner" || current.Status != "open" {
				t.Fatalf("incorrect result or bypassed other check: %+v", current)
			}
			if outcome == "fail" && current.Step != "execute" {
				t.Fatal("failed review did not route repair")
			}
			if outcome == "blocked" && (!current.Paused || current.Blocker == nil) {
				t.Fatal("blocked review lost recovery")
			}
			if strings.Contains(first, "lease-") || strings.Contains(first, "remote:runner/workspace") {
				t.Fatal("receipt leaked private responsibility")
			}
			body["reason"] = "different input"
			w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
			if w.Code < 400 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestRemoteHumanReviewRejectsStaleUnauthorizedAndMissingEvidence(t *testing.T) {
	f, task, image := reviewFixture(t)
	body := reviewBody(task, image, "pass")
	for _, token := range []string{f.uploader, f.reader, f.other} {
		w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, nil, "", token, "", "1")
		if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Fatal("token judged as human", w.Code, w.Body.String())
		}
	}
	reader, csrf := standaloneLogin(t, f.h, "reader", "fixture-user-password")
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, reader, csrf, "", "http://server.test", "1")
	if w.Code != http.StatusForbidden {
		t.Fatal("read-only session judged", w.Code)
	}
	body["candidate"] = strings.Repeat("b", 64)
	w = standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
	if w.Code < 400 || !strings.Contains(w.Body.String(), "REVIEW_VERSION_MISMATCH") {
		t.Fatal(w.Code, w.Body.String())
	}
	body["candidate"] = task.Candidate
	service, _ := f.s.projectService("shared")
	_, err := service.Store.DB.Exec("UPDATE artifact_objects SET status='purged' WHERE id=?", image)
	if err != nil {
		t.Fatal(err)
	}
	w = standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
	if w.Code < 400 || !strings.Contains(w.Body.String(), "REVIEW_EVIDENCE_CHANGED") {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ := service.Get("task")
	if len(current.Attempts) != 0 || len(current.Evidence) != 0 || core.RemoteRevision(current) != core.RemoteRevision(task) {
		t.Fatal("failed request left partial state")
	}
}

func TestRemoteHumanOpinionRemainsNonGatingAndCanBeAppliedExplicitly(t *testing.T) {
	f, task, image := reviewFixture(t)
	body := reviewBody(task, image, "pass")
	body["action"] = "review_pending"
	note := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	service, _ := f.s.projectService("shared")
	current, _ := service.Get("task")
	if len(current.Evidence) != 0 || len(current.Attempts) != 0 || len(current.HumanReviewNotes) != 1 || note["status"] != "pending" {
		t.Fatal("opinion advanced task")
	}
	body["action"], body["request_key"], body["review_note_id"] = "review_result", "formal", note["id"]
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	current, _ = service.Get("task")
	if current.HumanReviewNotes[0].Status != "confirmed" || len(current.Evidence) != 1 {
		t.Fatal("formal review failed to bind opinion")
	}
	// Verify both the submission and transport receipt live in the central DB.
	var count int
	if err := service.Store.DB.QueryRow("SELECT count(*) FROM portable_operations WHERE key='formal'").Scan(&count); err != nil || count != 1 {
		t.Fatal(err, count)
	}
	raw, _ := json.Marshal(current.Evidence[0])
	if !strings.Contains(string(raw), store.Hash(fixturePNG(t))) {
		t.Fatal("evidence hash missing")
	}
}

func TestReviewObservationDoesNotInvalidateAcceptedExecutionOutput(t *testing.T) {
	now := time.Now().UTC()
	task := core.Task{Candidate: "output", Attempts: []core.Attempt{{Unit: "execute", Status: "pass", Candidate: "output", Ended: &now}}}
	if !reviewObservationMatches(task, "input", now.Add(-time.Minute).Format(time.RFC3339Nano)) {
		t.Fatal("execution input superseded its accepted output")
	}
	if reviewObservationMatches(task, "new", now.Add(time.Minute).Format(time.RFC3339Nano)) {
		t.Fatal("newer different observation was ignored")
	}
}

func TestRemoteBlockedReviewResumeIsExactAndReplayable(t *testing.T) {
	f, task, image := reviewFixture(t)
	body := reviewBody(task, image, "blocked")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	body["action"], body["request_key"] = "review_resume", "resume-review"
	first := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
	standaloneJSON(t, first, http.StatusOK)
	again := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
	if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
		t.Fatal(again.Code, again.Body.String())
	}
	service, _ := f.s.projectService("shared")
	current, _ := service.Get("task")
	if current.Paused || current.Blocker != nil || len(current.Evidence) != 1 || current.Evidence[0].Result != "blocked" {
		t.Fatal("resume altered gate evidence")
	}
}

func TestRemoteReviewRejectsDefinitionAndPlanFormsAndCompletesOnlyAllChecks(t *testing.T) {
	f, task, image := reviewFixture(t)
	for _, field := range []string{"revision", "plan_revision"} {
		body := reviewBody(task, image, "pass")
		body[field] = 999
		w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "REVIEW_VERSION_MISMATCH") {
			t.Fatal(field, w.Code, w.Body.String())
		}
	}
	body := reviewBody(task, image, "pass")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	body["unit"], body["request_key"] = "independent", "other-check"
	response := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	if response["next_action"] != "completed" {
		t.Fatal("all required checks failed to close", response)
	}
}

func TestRemoteCheckRevisionRetainsHistoryWithoutOpeningClientWorkspace(t *testing.T) {
	f, task, image := reviewFixture(t)
	body := reviewBody(task, image, "pass")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	service, _ := f.s.projectService("shared")
	current, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	definition := current.Definition
	definition.Checks = append(append([]core.Check{}, definition.Checks...), core.Check{ID: "new-human", Kind: "review", Method: "human", Required: true, Covers: []string{"result"}})
	request := map[string]any{"definition": definition, "reason": "add a required check", "expected_revision": core.RemoteRevision(current), "operation_id": "check-revision", "idempotency_key": "check-revision"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/edit", request, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	updated, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Definition.Revision != current.Definition.Revision+1 || updated.Candidate != current.Candidate || len(updated.Evidence) != 1 || updated.Evidence[0].ReusedFrom == "" || len(updated.EvidenceHistory) != 1 || updated.EvidenceHistory[0].Result != "pass" {
		t.Fatal("remote revision lost immutable candidate, unchanged check evidence or history")
	}
}

func TestRemoteHumanReviewRespectsRuntimeParentPlanFence(t *testing.T) {
	f, task, image := reviewFixture(t)
	service, _ := f.s.projectService("shared")
	task.EpicMembership = &core.EpicMembership{Parent: "runtime-parent", Required: true}
	if err := service.ImportTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.DB.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", "remote-plan-parent:"+store.Hash([]byte("runtime-parent")), "active-plan"); err != nil {
		t.Fatal(err)
	}
	body := reviewBody(task, image, "pass")
	response := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", body, f.admin, f.csrf, "", "http://server.test", "1")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "PLAN_APPLY_IN_PROGRESS") {
		t.Fatal(response.Code, response.Body.String())
	}
	current, _ := service.Get("task")
	if len(current.Attempts) != 0 || len(current.Evidence) != 0 {
		t.Fatal("reserved parent accepted human write")
	}
}

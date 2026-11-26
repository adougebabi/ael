package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRemoteRunnerClaimIsOwnedFencedAndIdempotent(t *testing.T) {
	f := newArtifactFixture(t)
	candidate := portableFixtureCandidate(t)
	registerRepo := map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", registerRepo, nil, "", f.uploader, "", "1"), http.StatusCreated)
	observation := map[string]any{"task_id": "task", "expected_revision": f.revision, "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}
	seen := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", observation, nil, "", f.uploader, "", "1"), http.StatusCreated)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	currentTask, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	contextID := "context-fixture"
	contextBody, _ := json.Marshal(map[string]any{"id": contextID, "task_id": "task", "task_revision": f.revision, "definition_revision": currentTask.Definition.Revision, "plan_revision": currentTask.Plan.Revision, "policy_hash": currentTask.PolicyHash, "candidate_id": candidate.ID, "observation_id": seen["id"], "repository": "repo-one", "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "sources": []any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	if _, err = service.Store.DB.Exec("INSERT INTO portable_contexts(id,task_id,candidate_id,body,created) VALUES(?,?,?,?,?)", contextID, "task", candidate.ID, string(contextBody), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	runner := map[string]any{"id": "runner-a", "workspace_id": "workspace-a", "checkout_id": "checkout-a", "repository": "repo-one", "version": "fixture", "capabilities": []string{"development"}, "expected_revision": "absent", "operation_id": "register", "idempotency_key": "register"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners", runner, nil, "", f.uploader, "", "1"), http.StatusCreated)
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners", runner, nil, "", f.reader, "", "1"); w.Code != http.StatusForbidden {
		t.Fatalf("reader registered runner: %d", w.Code)
	}
	claim := map[string]any{"task_id": "task", "context_id": contextID, "expected_revision": f.revision, "operation_id": "claim", "idempotency_key": "claim"}
	endpoint := "/api/v1/projects/shared/runners/runner-a/claim"
	first := standaloneRequest(t, f.h, "POST", endpoint, claim, nil, "", f.uploader, "", "1")
	receipt := standaloneJSON(t, first, http.StatusCreated)
	if receipt["claim_revision"] != f.revision {
		t.Fatalf("claim did not expose frozen write revision: %+v", receipt)
	}
	attempt, ok := receipt["attempt"].(map[string]any)
	if !ok || attempt["token"] == "" || attempt["fence"].(float64) < 1 {
		t.Fatalf("missing lease: %+v", receipt)
	}
	replay := standaloneRequest(t, f.h, "POST", endpoint, claim, nil, "", f.uploader, "", "1")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("claim replay differed: %d %s", replay.Code, replay.Body.String())
	}
	if w := standaloneRequest(t, f.h, "POST", endpoint, claim, nil, "", f.other, "", "1"); w.Code != http.StatusNotFound {
		t.Fatalf("cross-project claim: %d", w.Code)
	}
	second := map[string]any{"task_id": "task", "context_id": contextID, "expected_revision": f.revision, "operation_id": "claim-again", "idempotency_key": "claim-again"}
	if w := standaloneRequest(t, f.h, "POST", endpoint, second, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "RUNNER_WIP") || !strings.Contains(w.Body.String(), attempt["id"].(string)) || !strings.Contains(w.Body.String(), "unit "+attempt["unit"].(string)) {
		t.Fatalf("RUNNER_WIP did not name the occupying unit: %d %s", w.Code, w.Body.String())
	}
	heartbeat := map[string]any{"task_id": "task", "attempt_id": attempt["id"], "token": attempt["token"], "fence": attempt["fence"], "operation_id": "beat", "idempotency_key": "beat"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/heartbeat", heartbeat, nil, "", f.uploader, "", "1"), http.StatusOK)
	runnerView := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/runners/runner-a", nil, nil, "", f.uploader, "", ""), http.StatusOK)
	assignment := runnerView["assignments"].([]any)[0].(map[string]any)
	if assignment["claim_revision"] != f.revision {
		t.Fatalf("runner recovery omitted claim revision: %+v", assignment)
	}
	contextView := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/contexts/"+contextID, nil, nil, "", f.reader, "", ""), http.StatusOK)
	if contextView["validity"] != "current" {
		t.Fatalf("heartbeat invalidated unchanged context: %+v", contextView)
	}
	progress := map[string]any{"task_id": "task", "attempt_id": attempt["id"], "token": attempt["token"], "fence": attempt["fence"], "summary": "fixture process started", "next": "wait for result", "stage": "execute", "operation_id": "progress", "idempotency_key": "progress"}
	progressFirst := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/progress", progress, nil, "", f.uploader, "", "1")
	standaloneJSON(t, progressFirst, http.StatusOK)
	healthNext, healthErr := service.Next("task")
	if healthErr != nil || healthNext.Health.CurrentWork != "fixture process started" || healthNext.Health.ReportedNext != "wait for result" || healthNext.Health.LastProgress != nil {
		t.Fatal("diagnostic work and actual outcomes were mixed", healthNext.Health, healthErr)
	}
	progressReplay := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/progress", progress, nil, "", f.uploader, "", "1")
	if progressReplay.Code != http.StatusOK || progressReplay.Body.String() != progressFirst.Body.String() {
		t.Fatalf("progress replay differed: %d %s", progressReplay.Code, progressReplay.Body.String())
	}
	output := map[string]any{"task_id": "task", "attempt_id": attempt["id"], "token": attempt["token"], "fence": attempt["fence"], "expected_revision": f.revision, "candidate": candidate, "operation_id": "output", "idempotency_key": "output"}
	// Heartbeat/progress changes the live task revision. Attempt writes retain
	// the revision of their original claim, independent of read-time revision.
	liveTask, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	liveRevision := core.RemoteRevision(liveTask)
	if liveRevision == f.revision {
		t.Fatal("fixture did not advance the live task revision")
	}
	output["expected_revision"], output["operation_id"], output["idempotency_key"] = liveRevision, "output-live-revision", "output-live-revision"
	wrongRevision := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/observe", output, nil, "", f.uploader, "", "1")
	if wrongRevision.Code != http.StatusConflict || !strings.Contains(wrongRevision.Body.String(), "REVISION_CONFLICT") {
		t.Fatalf("live revision did not reproduce claim-bound conflict: %d %s", wrongRevision.Code, wrongRevision.Body.String())
	}
	output["expected_revision"], output["operation_id"], output["idempotency_key"] = f.revision, "output", "output"
	outputReceipt := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/observe", output, nil, "", f.uploader, "", "1"), http.StatusOK)
	if outputReceipt["candidate_id"] != candidate.ID {
		t.Fatalf("wrong output candidate: %+v", outputReceipt)
	}
	data := []byte("runner evidence\n")
	// Upload names and candidate binding paths are independent. Binary uploads
	// preserve source bytes even when their original extensions are unsupported.
	start := map[string]any{"task_id": "task", "attempt_id": attempt["id"], "candidate": candidate.ID, "runner_id": "runner-a", "lease_token": attempt["token"], "fence": attempt["fence"], "expected_revision": f.revision, "filename": "result.bin", "mime": "application/octet-stream", "size": len(data), "sha256": store.Hash(data), "source": "runner", "operation_id": "artifact", "idempotency_key": "artifact"}
	created := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", start, nil, "", f.uploader, "", "1"), http.StatusCreated)
	uploadID := created["upload_id"].(string)
	chunk := map[string]any{"offset": 0, "data": data, "runner_id": "runner-a", "lease_token": attempt["token"], "fence": attempt["fence"]}
	withoutLease := map[string]any{"offset": 0, "data": data}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", withoutLease, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("unfenced artifact chunk accepted: %d %s", w.Code, w.Body.String())
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", chunk, nil, "", f.uploader, "", "1"), http.StatusOK)
	submit := map[string]any{"task_id": "task", "attempt_id": attempt["id"], "token": attempt["token"], "fence": attempt["fence"], "expected_revision": f.revision, "candidate_id": candidate.ID, "result": map[string]any{"status": "pass", "summary": "claimed success", "artifacts": []string{created["artifact_id"].(string)}}, "method": "", "inputs": []string{}, "bindings": []any{map[string]any{"path": "runtime:result.txt", "artifact_id": created["artifact_id"]}}, "operation_id": "unfinished-result", "idempotency_key": "unfinished-result"}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/submit", submit, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("unfinished artifact submitted: %d %s", w.Code, w.Body.String())
	}
	delete(submit, "result")
	submit["bindings"], submit["operation_id"], submit["idempotency_key"] = []any{}, "missing-result", "missing-result"
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/submit", submit, nil, "", f.uploader, "", "1"); w.Code != http.StatusBadRequest {
		t.Fatalf("missing final result accepted: %d %s", w.Code, w.Body.String())
	}
	heartbeat["fence"], heartbeat["operation_id"], heartbeat["idempotency_key"] = 999, "bad-beat", "bad-beat"
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/heartbeat", heartbeat, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("stale fence accepted: %d %s", w.Code, w.Body.String())
	}
	var task core.Task
	if err = service.Store.DB.QueryRow("SELECT body FROM tasks WHERE id='task'").Scan(&contextBody); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(contextBody, &task); err != nil {
		t.Fatal(err)
	}
	task.Attempts[len(task.Attempts)-1].Lease = time.Now().Add(-time.Second)
	expired, _ := json.Marshal(task)
	if _, err = service.Store.DB.Exec("UPDATE tasks SET body=? WHERE id='task'", string(expired)); err != nil {
		t.Fatal(err)
	}
	finish := map[string]any{"operation_id": "artifact", "idempotency_key": "artifact", "runner_id": "runner-a", "lease_token": attempt["token"], "fence": attempt["fence"]}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", finish, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("expired lease finalized evidence: %d %s", w.Code, w.Body.String())
	}
	heartbeat["fence"], heartbeat["operation_id"], heartbeat["idempotency_key"] = attempt["fence"], "expired", "expired"
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/heartbeat", heartbeat, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("expired lease accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestRemoteAgentInvalidClaimStillRejected(t *testing.T) {
	f := newArtifactFixture(t)
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/claim", map[string]any{}, nil, "", f.uploader, "", "1")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "RUNNER_INVALID") {
		t.Fatalf("unverified runner was not refused: %d %s", w.Code, w.Body.String())
	}
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Get("task")
	if err != nil || len(task.Attempts) != 0 {
		t.Fatalf("refused claim changed task: %+v %v", task.Attempts, err)
	}
}

func TestRemoteRunnerSubmitsFinalizedAttemptThroughTaskStateMachine(t *testing.T) {
	for _, imported := range []bool{false, true} {
		t.Run(fmt.Sprint("imported-", imported), func(t *testing.T) {
			taskID := "task"
			f := newArtifactFixture(t)
			service, err := f.s.projectService("shared")
			if err != nil {
				t.Fatal(err)
			}
			task, err := service.Get("task")
			if err != nil {
				t.Fatal(err)
			}
			task.Step = "execute"
			task.Definition.Checks = []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"fixture-check"}, Covers: []string{"result"}, Required: true, Artifacts: []string{"app.txt"}}}
			raw, _ := json.Marshal(task)
			if _, err = service.Store.DB.Exec("UPDATE tasks SET body=? WHERE id='task'", string(raw)); err != nil {
				t.Fatal(err)
			}
			candidate := portableFixtureCandidate(t)
			if imported {
				archive := authorityArchiveFixture("agent-recovery")
				task.Definition.ID = "same-name"
				task.Status, task.Step, task.Paused, task.Confirmation = "open", "execute", false, nil
				archive.Tasks[0] = core.ArchiveTask(task, archive.Tasks[0].MappedID)
				taskID = archive.Tasks[0].MappedID
				activateReconciliationFixture(t, f, archive)
				task, err = service.Get(taskID)
				if err != nil {
					t.Fatal(err)
				}
				revision := core.RemoteRevision(task)
				standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
				observed := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", map[string]any{"task_id": taskID, "expected_revision": revision, "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "candidate": candidate, "inputs_known": true, "operation_id": "reconcile-observe", "idempotency_key": "reconcile-observe"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
				content := []byte("# Project constraints\nUse the shared fixture.\n")
				uploaded := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", map[string]any{"task_id": taskID, "expected_revision": revision, "filename": "project.md", "mime": "text/plain", "size": len(content), "sha256": store.Hash(content), "source": "reconcile", "operation_id": "spec-reconcile", "idempotency_key": "spec-reconcile"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
				uploadID := uploaded["upload_id"].(string)
				standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", map[string]any{"offset": 0, "data": content}, nil, "", f.uploader, "", "1"), http.StatusOK)
				standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", map[string]any{"operation_id": "spec-reconcile", "idempotency_key": "spec-reconcile"}, nil, "", f.uploader, "", "1"), http.StatusOK)

				published := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/contexts", map[string]any{"task_id": taskID, "expected_revision": revision, "observation_id": observed["id"], "sources": []any{map[string]any{"id": "source:spec/project.md", "path": "spec/project.md", "artifact_id": uploaded["artifact_id"], "reason": "current constraints"}}, "inventory": []any{map[string]any{"id": "source:spec/project.md", "path": "spec/project.md", "sha256": store.Hash(content), "status": "current", "kind": "spec", "paths": []string{}, "tasks": []string{}, "action": "connected"}}, "operation_id": "reconcile-context", "idempotency_key": "reconcile-context"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
				reconcileContext := published["id"].(string)
				input := map[string]any{"context_id": reconcileContext, "reason": "Verified original history and current engineering basis", "start": "Implement current app.txt candidate", "operation_id": "reconcile", "idempotency_key": "reconcile", "expected_revision": revision}
				path := "/api/v1/projects/shared/tasks/" + taskID + "/reconcile-import"
				first := standaloneRequest(t, f.h, "POST", path, input, nil, "", f.uploader, "", "1")
				standaloneJSON(t, first, http.StatusOK)
				replay := standaloneRequest(t, f.h, "POST", path, input, nil, "", f.uploader, "", "1")
				if replay.Body.String() != first.Body.String() {
					t.Fatal("reconciliation replay differed")
				}
				task, err = service.Get(taskID)
				if err != nil || task.Paused || task.Confirmation != nil || len(task.Approvals) != 0 || len(task.Evidence) != 0 {
					t.Fatal("recovery revived authority", task, err)
				}
			}
			f.revision = core.RemoteRevision(task)
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
			observation := map[string]any{"task_id": taskID, "expected_revision": f.revision, "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "candidate": candidate, "operation_id": "observe", "idempotency_key": "observe"}
			seen := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", observation, nil, "", f.uploader, "", "1"), http.StatusCreated)
			contextID := "context-submit"
			contextBody, _ := json.Marshal(map[string]any{"id": contextID, "task_id": taskID, "task_revision": f.revision, "candidate_id": candidate.ID, "observation_id": seen["id"], "repository": "repo-one", "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "sources": []any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
			if _, err = service.Store.DB.Exec("INSERT INTO portable_contexts(id,task_id,candidate_id,body,created) VALUES(?,?,?,?,?)", contextID, taskID, candidate.ID, string(contextBody), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			runner := map[string]any{"id": "runner-a", "workspace_id": "workspace-a", "checkout_id": "checkout-a", "repository": "repo-one", "version": "fixture", "capabilities": []string{"development"}, "expected_revision": "absent", "operation_id": "register", "idempotency_key": "register"}
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners", runner, nil, "", f.uploader, "", "1"), http.StatusCreated)
			claim := map[string]any{"task_id": taskID, "context_id": contextID, "expected_revision": f.revision, "operation_id": "claim", "idempotency_key": "claim"}
			claimed := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/claim", claim, nil, "", f.uploader, "", "1"), http.StatusCreated)
			attempt := claimed["attempt"].(map[string]any)
			output := map[string]any{"task_id": taskID, "attempt_id": attempt["id"], "token": attempt["token"], "fence": attempt["fence"], "expected_revision": f.revision, "candidate": candidate, "operation_id": "output", "idempotency_key": "output"}
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/observe", output, nil, "", f.uploader, "", "1"), http.StatusOK)
			data := []byte("fixture\n")
			start := map[string]any{"task_id": taskID, "attempt_id": attempt["id"], "candidate": candidate.ID, "runner_id": "runner-a", "lease_token": attempt["token"], "fence": attempt["fence"], "expected_revision": f.revision, "filename": "result.txt", "mime": "text/plain", "size": len(data), "sha256": store.Hash(data), "source": "runner", "operation_id": "artifact", "idempotency_key": "artifact"}
			created := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", start, nil, "", f.uploader, "", "1"), http.StatusCreated)
			uploadID, artifactID := created["upload_id"].(string), created["artifact_id"].(string)
			chunk := map[string]any{"offset": 0, "data": data, "runner_id": "runner-a", "lease_token": attempt["token"], "fence": attempt["fence"]}
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", chunk, nil, "", f.uploader, "", "1"), http.StatusOK)
			finish := map[string]any{"operation_id": "artifact", "idempotency_key": "artifact", "runner_id": "runner-a", "lease_token": attempt["token"], "fence": attempt["fence"]}
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", finish, nil, "", f.uploader, "", "1"), http.StatusOK)
			submit := map[string]any{"task_id": taskID, "attempt_id": attempt["id"], "token": attempt["token"], "fence": attempt["fence"], "expected_revision": f.revision, "candidate_id": candidate.ID, "result": map[string]any{"status": "pass", "summary": "real fixture deliverable", "artifacts": []string{artifactID}}, "method": "", "inputs": []string{}, "bindings": []any{map[string]any{"path": "app.txt", "artifact_id": artifactID}}, "operation_id": "submit", "idempotency_key": "submit"}
			endpoint := "/api/v1/projects/shared/runners/runner-a/submit"
			first := standaloneRequest(t, f.h, "POST", endpoint, submit, nil, "", f.uploader, "", "1")
			response := standaloneJSON(t, first, http.StatusOK)
			next := response["next"].(map[string]any)
			resultTask := next["task"].(map[string]any)
			if resultTask["candidate"] != candidate.ID || resultTask["current_step"] != "verify" {
				t.Fatalf("shared task state did not advance: %+v", response)
			}
			replay := standaloneRequest(t, f.h, "POST", endpoint, submit, nil, "", f.uploader, "", "1")
			if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
				t.Fatalf("submission replay differed: %d %s", replay.Code, replay.Body.String())
			}
			lookup := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/runners/runner-a/operations/submit", nil, nil, "", f.uploader, "", "")
			stored := standaloneJSON(t, lookup, http.StatusOK)
			if stored["found"] != true || strings.Contains(lookup.Body.String(), attempt["token"].(string)) {
				t.Fatalf("receipt missing or exposed lease token: %s", lookup.Body.String())
			}
			verifyTask, err := service.Get(taskID)
			if err != nil {
				t.Fatal(err)
			}
			verifyRevision := core.RemoteRevision(verifyTask)
			// A verify claim on a different (for example later committed)
			// candidate must tell the Agent how to return to execute itself.
			mismatch := service.Store.Write(func(tx *store.Tx) error {
				_, _, err := service.RemoteClaimTx(tx, core.RemoteClaimInput{TaskID: taskID, ExpectedRevision: verifyRevision, Runner: "runner-a", Workspace: "workspace-a", Candidate: strings.Repeat("b", 64), Token: "fixture-mismatch"})
				return err
			})
			if mismatch == nil || !strings.HasPrefix(mismatch.Error(), "CANDIDATE_CHANGED: ") || !strings.Contains(mismatch.Error(), "--return-to execute --expected-revision "+verifyRevision) || !strings.Contains(mismatch.Error(), candidate.ID[:12]) || len(strings.TrimPrefix(mismatch.Error(), "CANDIDATE_CHANGED: ")) > 500 {
				t.Fatalf("verify candidate mismatch lacked recovery detail: %v", mismatch)
			}
			verifyObservation := map[string]any{"task_id": taskID, "expected_revision": verifyRevision, "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "candidate": candidate, "operation_id": "verify-observe", "idempotency_key": "verify-observe"}
			verifySeen := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", verifyObservation, nil, "", f.uploader, "", "1"), http.StatusCreated)
			verifyContext := "context-verify"
			contextBody, _ = json.Marshal(map[string]any{"id": verifyContext, "task_id": taskID, "task_revision": verifyRevision, "candidate_id": candidate.ID, "observation_id": verifySeen["id"], "repository": "repo-one", "checkout_id": "checkout-a", "workspace_id": "workspace-a", "runner_id": "runner-a", "sources": []any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
			if _, err = service.Store.DB.Exec("INSERT INTO portable_contexts(id,task_id,candidate_id,body,created) VALUES(?,?,?,?,?)", verifyContext, taskID, candidate.ID, string(contextBody), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			verifyClaim := map[string]any{"task_id": taskID, "context_id": verifyContext, "expected_revision": verifyRevision, "operation_id": "verify-claim", "idempotency_key": "verify-claim"}
			claimed = standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/claim", verifyClaim, nil, "", f.uploader, "", "1"), http.StatusCreated)
			verifyAttempt := claimed["attempt"].(map[string]any)
			verifyOutput := map[string]any{"task_id": taskID, "attempt_id": verifyAttempt["id"], "token": verifyAttempt["token"], "fence": verifyAttempt["fence"], "expected_revision": verifyRevision, "candidate": candidate, "operation_id": "verify-output", "idempotency_key": "verify-output"}
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/observe", verifyOutput, nil, "", f.uploader, "", "1"), http.StatusOK)
			wrongData := []byte("not the candidate file\n")
			wrongStart := map[string]any{"task_id": taskID, "attempt_id": verifyAttempt["id"], "candidate": candidate.ID, "runner_id": "runner-a", "lease_token": verifyAttempt["token"], "fence": verifyAttempt["fence"], "expected_revision": verifyRevision, "filename": "wrong.txt", "mime": "text/plain", "size": len(wrongData), "sha256": store.Hash(wrongData), "source": "runner", "operation_id": "wrong-artifact", "idempotency_key": "wrong-artifact"}
			wrong := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", wrongStart, nil, "", f.uploader, "", "1"), http.StatusCreated)
			wrongUpload, wrongID := wrong["upload_id"].(string), wrong["artifact_id"].(string)
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+wrongUpload+"/chunks", map[string]any{"offset": 0, "data": wrongData, "runner_id": "runner-a", "lease_token": verifyAttempt["token"], "fence": verifyAttempt["fence"]}, nil, "", f.uploader, "", "1"), http.StatusOK)
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+wrongUpload+"/finalize", map[string]any{"operation_id": "wrong-artifact", "idempotency_key": "wrong-artifact", "runner_id": "runner-a", "lease_token": verifyAttempt["token"], "fence": verifyAttempt["fence"]}, nil, "", f.uploader, "", "1"), http.StatusOK)
			badCheck := map[string]any{"task_id": taskID, "attempt_id": verifyAttempt["id"], "token": verifyAttempt["token"], "fence": verifyAttempt["fence"], "expected_revision": verifyRevision, "candidate_id": candidate.ID, "result": map[string]any{"status": "pass", "summary": "fixture check", "artifacts": []string{wrongID}}, "method": "command", "exit_code": 0, "inputs": []string{}, "bindings": []any{map[string]any{"path": "app.txt", "artifact_id": wrongID}}, "operation_id": "bad-check", "idempotency_key": "bad-check"}
			if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/submit", badCheck, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
				t.Fatalf("check artifact inconsistent with candidate accepted: %d %s", w.Code, w.Body.String())
			}
			interrupt := map[string]any{"task_id": taskID, "attempt_id": verifyAttempt["id"], "token": verifyAttempt["token"], "fence": verifyAttempt["fence"], "reason": "fixture writer stopped", "operation_id": "interrupt", "idempotency_key": "interrupt"}
			interrupted := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/runner-a/interrupt", interrupt, nil, "", f.uploader, "", "1"), http.StatusOK)
			resume := map[string]any{"operation_id": "resume-verify", "idempotency_key": "resume-verify", "expected_revision": interrupted["revision"], "ack_stopped": true}
			if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/"+taskID+"/resume", resume, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "REMOTE_CANDIDATE_REQUIRED") {
				t.Fatalf("server attempted to inspect remote workspace during recovery: %d %s", w.Code, w.Body.String())
			}
			resume["operation_id"], resume["idempotency_key"], resume["return_to"] = "resume-execute", "resume-execute", "execute"
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/"+taskID+"/resume", resume, nil, "", f.uploader, "", "1"), http.StatusOK)
			if err = service.Store.Write(func(tx *store.Tx) error { return reconcileRunnerAttempts(tx, "runner-a") }); err != nil {
				t.Fatal(err)
			}
			var state string
			if err = service.Store.DB.QueryRow("SELECT state FROM remote_runner_attempts WHERE attempt_id=?", verifyAttempt["id"]).Scan(&state); err != nil || state != "finished" {
				t.Fatalf("acknowledged abandoned attempt still held runner: %q %v", state, err)
			}
		})
	}
}

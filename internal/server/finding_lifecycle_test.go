package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The accepted source task and context are synthetic fixture setup. Finding
// reports and lifecycle changes use the authenticated production HTTP handler.
func findingLifecycleFixture(t *testing.T, optionalPolicy ...bool) (artifactFixture, string, string) {
	t.Helper()
	f, task, image := reviewFixture(t)
	service, _ := f.s.projectService("shared")
	task.Definition.Scope = []string{"app.txt"}
	if len(optionalPolicy) > 0 && optionalPolicy[0] {
		policy, err := service.Policy()
		if err != nil {
			t.Fatal(err)
		}
		task.Definition.Checks[0].Required = false
		policy.Checks = append(policy.Checks, task.Definition.Checks[0])
		raw, _ := json.Marshal(policy)
		if err = os.WriteFile(filepath.Join(service.Root, ".ael/policy.yaml"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.ImportTask(task); err != nil {
		t.Fatal(err)
	}
	task, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	context := map[string]any{"id": "lifecycle-context", "task_id": "task", "task_revision": core.RemoteRevision(task), "candidate_id": task.Candidate, "task_definition": task.Definition, "created_at": created}
	raw, _ := json.Marshal(context)
	if _, err = service.Store.DB.Exec("INSERT INTO portable_contexts VALUES(?,?,?,?,?)", "lifecycle-context", "task", task.Candidate, string(raw), created); err != nil {
		t.Fatal(err)
	}
	report := lifecycleUpload(t, f, "task", task.Candidate, "origin-report.json", "text/plain", []byte("{\"observation\":\"synthetic old failure\"}"), "context-check:lifecycle-context:visual", "origin-report")
	input := map[string]any{"context_id": "lifecycle-context", "task_id": "task", "candidate_id": task.Candidate, "check_id": "visual", "expected_revision": core.RemoteRevision(task), "artifact_id": report, "source": map[string]string{"method": "model", "tool": "synthetic-test-classification"}, "findings": []any{map[string]any{"rule": "ui.clipped-label", "title": "Clipped action label", "kind": "ui", "severity": "high", "route": "/fixture", "location": map[string]string{"path": "app.txt", "selector": "#save"}, "expected": "Label is readable", "actual": "Original candidate clips the label", "reproduction": []string{"Open the fixture", "Inspect the action label"}, "impact": "The action cannot be read", "artifacts": []string{image}}}, "operation_id": "origin-finding", "idempotency_key": "origin-finding"}
	result := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings", input, nil, "", f.uploader, "", "1"), http.StatusCreated)
	finding := result["findings"].([]any)[0].(map[string]any)
	return f, finding["id"].(string), image
}

func lifecycleUpload(t *testing.T, f artifactFixture, task, candidate, name, mime string, data []byte, source, key string) string {
	t.Helper()
	service, _ := f.s.projectService("shared")
	current, err := service.Get(task)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"operation_id": key, "idempotency_key": key, "expected_revision": core.RemoteRevision(current), "task_id": task, "candidate": candidate, "filename": name, "mime": mime, "size": len(data), "sha256": store.Hash(data), "source": source}
	start := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", body, nil, "", f.uploader, "", "1"), http.StatusCreated)
	upload := start["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]any{"operation_id": key, "idempotency_key": key}, nil, "", f.uploader, "", "1"), http.StatusOK)
	return start["artifact_id"].(string)
}

func lifecycleBody(t *testing.T, f artifactFixture, id, key string) map[string]any {
	t.Helper()
	view := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/findings/"+id, nil, nil, "", f.uploader, "", ""), http.StatusOK)
	return map[string]any{"finding_id": id, "expected_revision": view["revision"], "expected_task_revision": view["source_task_revision"], "reason": "Explicit authorized finding action", "operation_id": key, "idempotency_key": key}
}

func TestFindingDecisionsPreserveGatesAndUseAuthenticatedAuthority(t *testing.T) {
	f, id, _ := findingLifecycleFixture(t)
	service, _ := f.s.projectService("shared")
	before, _ := service.Get("task")
	for _, action := range []string{"fix", "more_evidence", "non_issue", "accepted_tradeoff"} {
		input := lifecycleBody(t, f, id, "decide-"+action)
		input["action"] = action
		if action == "non_issue" || action == "accepted_tradeoff" {
			rejected := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/decisions", input, nil, "", f.uploader, "", "1")
			if rejected.Code != http.StatusForbidden {
				t.Fatal("token claimed human decision", rejected.Code, rejected.Body.String())
			}
		}
		first := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/decisions", input, f.admin, f.csrf, "", "http://server.test", "1")
		value := standaloneJSON(t, first, http.StatusOK)
		decision := value["decision"].(map[string]any)
		if decision["actor"] != "owner" || decision["authority"] != "human_session" || value["gate_effect"] != "none" {
			t.Fatal(value)
		}
		again := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/decisions", input, f.admin, f.csrf, "", "http://server.test", "1")
		if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
			t.Fatal("decision replay changed")
		}
	}
	after, _ := service.Get("task")
	if core.RemoteRevision(before) != core.RemoteRevision(after) {
		t.Fatal("finding disposition changed task gates")
	}
	view := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/findings/"+id, nil, nil, "", f.reader, "", ""), http.StatusOK)
	if len(view["history"].([]any)) != 4 {
		t.Fatal("decision history was overwritten", view)
	}
}

func TestFindingRepairIsUniqueAcrossKeysAndScannerCannotCreateIt(t *testing.T) {
	f, id, _ := findingLifecycleFixture(t)
	if err := f.s.createUser("scanner", "fixture-scanner-password"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.addMember("shared", "scanner", "reviewer"); err != nil {
		t.Fatal(err)
	}
	_, scanner, err := f.s.issueToken("scanner", "shared", "scan-only")
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycleBody(t, f, id, "scan-repair")
	rejected := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/repair", request, nil, "", scanner, "", "1")
	if rejected.Code != http.StatusForbidden {
		t.Fatal("scan role created repair", rejected.Code)
	}
	var wait sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, key := range []string{"repair-one", "repair-two"} {
		input := lifecycleBody(t, f, id, key)
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/repair", input, nil, "", f.uploader, "", "1")
		}()
	}
	wait.Wait()
	close(results)
	repairID := ""
	for response := range results {
		value := standaloneJSON(t, response, http.StatusOK)
		task := value["repair"].(map[string]any)["task_id"].(string)
		if repairID != "" && task != repairID {
			t.Fatal("different keys created distinct repairs")
		}
		repairID = task
	}
	service, _ := f.s.projectService("shared")
	var count int
	if err = service.Store.DB.QueryRow("SELECT count(*) FROM tasks WHERE json_extract(body,'$.definition.feedback_of')='task'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	repair, err := service.Get(repairID)
	if err != nil {
		t.Fatal(err)
	}
	if repair.Definition.Intent != "change" || repair.Definition.Goal != "verified" || repair.Definition.Source != "finding:"+id || !strings.Contains(repair.Definition.OriginalRequest, "Original candidate clips the label") || !strings.Contains(repair.Definition.OriginalRequest, "Open the fixture") {
		t.Fatal("repair lost authorization or reproduction")
	}
	sources, err := (&portableStore{service: service, project: "shared"}).findingContextSources(repairID)
	if err != nil || len(sources) != 1 || sources[0]["finding_id"] != id {
		t.Fatal("repair context lost its source", sources, err)
	}
}

func TestFindingRepairFailureRollsBackTaskFeedbackAndReceipt(t *testing.T) {
	f, id, _ := findingLifecycleFixture(t)
	service, _ := f.s.projectService("shared")
	before, _ := service.Get("task")
	input := lifecycleBody(t, f, id, "invalid-mapping")
	input["repair_check_id"] = "unrelated"
	response := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/repair", input, nil, "", f.uploader, "", "1")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "FINDING_REPAIR_CHECK_MISMATCH") {
		t.Fatal(response.Code, response.Body.String())
	}
	after, _ := service.Get("task")
	if core.RemoteRevision(before) != core.RemoteRevision(after) {
		t.Fatal("failed mapping left feedback")
	}
	var count int
	if err := service.Store.DB.QueryRow("SELECT count(*) FROM tasks WHERE json_extract(body,'$.definition.feedback_of')='task'").Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan task", count, err)
	}
	input = lifecycleBody(t, f, id, "invalid-mapping")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/repair", input, nil, "", f.uploader, "", "1"), http.StatusOK)
}

func lifecycleCandidate(t *testing.T, content, repository string) remote.PortableCandidate {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "app.txt"}, {"commit", "-m", "fixed fixture"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	candidate, err := remote.CapturePortableCandidate(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestFindingClosesOnlyAfterEffectiveNewCandidateRetest(t *testing.T) {
	f, id, oldImage := findingLifecycleFixture(t)
	created := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/repair", lifecycleBody(t, f, id, "repair"), nil, "", f.uploader, "", "1"), http.StatusOK)
	repairID := created["repair"].(map[string]any)["task_id"].(string)
	service, _ := f.s.projectService("shared")
	repair, _ := service.Get(repairID)
	candidate := lifecycleCandidate(t, "fixed\n", "repo-one")
	repair.Candidate, repair.CandidateWorkspace, repair.Step = candidate.ID, "remote:repair/workspace", "verify"
	if err := service.ImportTask(repair); err != nil {
		t.Fatal(err)
	}
	repair, _ = service.Get(repairID)
	observed := map[string]any{"task_id": repairID, "expected_revision": core.RemoteRevision(repair), "checkout_id": "repair", "workspace_id": "workspace", "runner_id": "repair", "candidate": candidate, "operation_id": "repair-observation", "idempotency_key": "repair-observation"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", observed, nil, "", f.uploader, "", "1"), http.StatusCreated)
	image := lifecycleUpload(t, f, repairID, candidate.ID, "fixed.png", "image/png", fixturePNG(t), "new-candidate-screenshot", "fixed-image")
	body := lifecycleBody(t, f, id, "before-retest")
	body["repair_task_id"], body["candidate_id"], body["expected_repair_revision"], body["evidence_sha256"] = repairID, candidate.ID, core.RemoteRevision(repair), "fake-pass"
	rejected := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/revalidations", body, nil, "", f.uploader, "", "1")
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "FINDING_RETEST_REQUIRED") {
		t.Fatal(rejected.Code, rejected.Body.String())
	}
	view := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/findings/"+id, nil, nil, "", f.reader, "", ""), http.StatusOK)
	if view["status"] != "open" || view["repair_state"] != "awaiting_retest" {
		t.Fatal("implementation closed finding", view)
	}
	review := map[string]any{"action": "review_result", "id": repairID, "unit": "visual", "reason": "Synthetic exact new-candidate human check", "result": "pass", "artifacts": []string{image}, "candidate": candidate.ID, "revision": repair.Definition.Revision, "plan_revision": repair.Plan.Revision, "request_key": "repair-review"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", review, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	repair, _ = service.Get(repairID)
	evidence := core.CurrentEvidence(&repair, repair.Definition.Checks[0])
	if evidence == nil {
		t.Fatal("check did not produce evidence")
	}
	body = lifecycleBody(t, f, id, "valid-retest")
	body["repair_task_id"], body["candidate_id"], body["expected_repair_revision"], body["evidence_sha256"] = repairID, candidate.ID, core.RemoteRevision(repair), core.EvidenceRevision(*evidence)
	first := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/revalidations", body, nil, "", f.uploader, "", "1")
	resolved := standaloneJSON(t, first, http.StatusOK)
	if resolved["status"] != "resolved" || resolved["next_action"] != "continue_required_checks" {
		t.Fatal(resolved)
	}
	again := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/revalidations", body, nil, "", f.uploader, "", "1")
	if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
		t.Fatal("revalidation replay changed")
	}
	view = standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/findings/"+id, nil, nil, "", f.reader, "", ""), http.StatusOK)
	if view["resolution_validity"] != "current" {
		t.Fatal(view)
	}
	occurrences := view["occurrences"].([]any)
	original := occurrences[0].(map[string]any)["input"].(map[string]any)
	if original["actual"] != "Original candidate clips the label" || original["artifacts"].([]any)[0] != oldImage {
		t.Fatal("old failure was overwritten", original)
	}
	current, _ := service.Get(repairID)
	if current.Status != "open" {
		t.Fatal("finding closure bypassed remaining task check")
	}
}

func TestFindingRepairPreservesOptionalPolicyCheckAndAddsRequiredMapping(t *testing.T) {
	f, id, _ := findingLifecycleFixture(t, true)
	result := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/findings/"+id+"/repair", lifecycleBody(t, f, id, "optional-policy-repair"), nil, "", f.uploader, "", "1"), http.StatusOK)
	link := result["repair"].(map[string]any)
	if link["check_id"] == "visual" {
		t.Fatal("optional policy check was promoted in place")
	}
	service, _ := f.s.projectService("shared")
	task, err := service.Get(link["task_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	original, required := core.Check{}, core.Check{}
	for _, check := range task.Definition.Checks {
		if check.ID == "visual" {
			original = check
		}
		if check.ID == link["check_id"] {
			required = check
		}
	}
	if original.ID == "" || original.Required || required.ID == "" || !required.Required || core.CheckMeaning(original) != core.CheckMeaning(required) {
		t.Fatal("required mapping lost policy or acceptance meaning")
	}
}

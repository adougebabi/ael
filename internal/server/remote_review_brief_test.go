package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteReviewBriefIsBoundToCurrentCandidateAndShownToReviewers(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	var revision string
	err = service.Store.Write(func(tx *store.Tx) error {
		var raw string
		if err := tx.Row("SELECT body FROM tasks WHERE id='task'").Scan(&raw); err != nil {
			return err
		}
		var task core.Task
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			return err
		}
		task.Definition.Checks = []core.Check{{ID: "ui", Kind: "ui", Method: "human", Covers: []string{"result"}, Required: true}}
		task.Candidate = "candidate-one"
		body, err := json.Marshal(task)
		if err != nil {
			return err
		}
		revision = core.RemoteRevision(task)
		_, err = tx.Exec("UPDATE tasks SET body=? WHERE id='task'", string(body))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	brief := map[string]any{"check": "ui", "summary": "确认手机屏宽截图变化符合设计稿", "changes": "合并手机端工作后改为单列", "steps": []string{"在附件中按页面名对照"}, "focus": []string{"底部输入栏"}}
	request := func(token string, body map[string]any) *httptest.ResponseRecorder {
		return standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/review-brief", body, nil, "", token, "", "1")
	}
	with := func(key string, b map[string]any, rev string) map[string]any {
		return map[string]any{"brief": b, "expected_revision": rev, "operation_id": key, "idempotency_key": key}
	}
	if response := request(f.reader, with("reader", brief, revision)); response.Code != http.StatusForbidden {
		t.Fatalf("reader recorded a brief: %d", response.Code)
	}
	if body := standaloneJSON(t, request(f.uploader, with("stale", brief, "old-revision")), http.StatusConflict); body["code"] != "REVISION_CONFLICT" {
		t.Fatalf("stale revision accepted: %v", body)
	}
	unknown := map[string]any{}
	for k, v := range brief {
		unknown[k] = v
	}
	unknown["evidence"] = []string{"missing-artifact"}
	if body := standaloneJSON(t, request(f.uploader, with("unknown-evidence", unknown, revision)), http.StatusConflict); body["code"] != "ARTIFACT_REQUIRED" || body["detail"] == "" {
		t.Fatalf("unbound evidence accepted: %v", body)
	}
	if body := standaloneJSON(t, request(f.uploader, with("empty", map[string]any{"check": "ui", "summary": ""}, revision)), http.StatusBadRequest); body["code"] != "REVIEW_BRIEF_INVALID" {
		t.Fatalf("empty brief accepted: %v", body)
	}
	body := standaloneJSON(t, request(f.uploader, with("brief", brief, revision)), http.StatusOK)
	task, _ := body["task"].(map[string]any)
	briefs, _ := task["review_briefs"].([]any)
	if body["review_brief_recorded"] != true || len(briefs) != 1 {
		t.Fatalf("brief not recorded: %v", body)
	}
	recorded := briefs[0].(map[string]any)
	if recorded["candidate"] != "candidate-one" || recorded["actor"] != "upload" || recorded["summary"] != brief["summary"] {
		t.Fatalf("brief not bound to candidate and actor: %v", recorded)
	}
}

package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailureReviewAndNoProgressReachLocalAPI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	for _, d := range []core.Definition{
		{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}},
		{ID: "leaf", Parent: "epic", Title: "子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}},
	} {
		if _, err = s.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Store.DB.Exec(`UPDATE tasks SET body=json_set(body,'$.created','2000-01-01T00:00:00Z') WHERE id='leaf'`); err != nil {
		t.Fatal(err)
	}
	h := New(s, "127.0.0.1:7331")
	request := func(method, path string, payload any, auth bool) *httptest.ResponseRecorder {
		var data []byte
		if payload != nil {
			data, _ = json.Marshal(payload)
		}
		r := httptest.NewRequest(method, "http://"+h.Host+path, bytes.NewReader(data))
		if auth {
			r.Header.Set("Authorization", "Bearer "+h.Token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	tree := request("GET", "/api/task-tree?id=epic", nil, true)
	var projected core.EpicTree
	if tree.Code != 200 || json.Unmarshal(tree.Body.Bytes(), &projected) != nil || len(projected.Summary.Issues) != 1 || projected.Summary.Issues[0].Kind != "no_progress" {
		t.Fatalf("parent omitted no-progress warning: %d %s", tree.Code, tree.Body.String())
	}
	for i := 0; i < 3; i++ {
		claim, e := s.Claim("leaf", "", "fixture", "", "")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Submit(core.Submission{Task: "leaf", Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("api-failure-"), Result: core.StepResult{Status: "fail", Summary: fmt.Sprintf("failure %d", i)}}); e != nil {
			t.Fatal(e)
		}
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "review.txt"), []byte("isolated actual review"), 0600); err != nil {
		t.Fatal(err)
	}
	review := core.FailureReview{Actor: "fixture", Tried: "检查失败输入", Excluded: "排除缺失路径", Hypothesis: "配置不符", Next: "复核配置", Recovery: "配置已核对", Evidence: []string{"runtime:review.txt"}}
	payload := map[string]any{"action": "review_failure", "id": "leaf", "failure_review": review}
	if got := request("POST", "/api/action", payload, false); got.Code != 401 {
		t.Fatalf("unauthorized review accepted: %d", got.Code)
	}
	if got := request("POST", "/api/action", map[string]any{"action": "resume", "id": "leaf"}, true); got.Code != 409 || !strings.Contains(got.Body.String(), "ROOT_CAUSE_REVIEW_REQUIRED") {
		t.Fatalf("review bypassed: %d %s", got.Code, got.Body.String())
	}
	if got := request("POST", "/api/action", payload, true); got.Code != 200 {
		t.Fatalf("review rejected: %d %s", got.Code, got.Body.String())
	}
	if got := request("POST", "/api/action", map[string]any{"action": "resume", "id": "leaf"}, true); got.Code != 200 {
		t.Fatalf("reviewed recovery rejected: %d %s", got.Code, got.Body.String())
	}
	if task, e := s.Get("leaf"); e != nil || task.FailureCycles["define"].Count != 3 || task.FailureCycles["define"].State != "reviewed" {
		t.Fatalf("API reset budget: %+v %v", task.FailureCycles, e)
	}
}

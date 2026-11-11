package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestEpicTreeAPIUsesDomainTreeAndLocalAssociation(t *testing.T) {
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
	for _, definition := range []core.Definition{
		{ID: "epic", Title: "父任务", Type: "epic", Goal: "verified", Acceptance: []string{"整体结果"}},
		{ID: "leaf", Title: "叶子", Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}},
	} {
		if _, err = s.Create(definition); err != nil {
			t.Fatal(err)
		}
	}
	h := New(s, "127.0.0.1:7331")
	request := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+h.Host+path, strings.NewReader(body))
		if auth {
			r.Header.Set("Authorization", "Bearer "+h.Token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := `{"parent":"epic","task":"leaf","required":false,"milestone":"第一阶段"}`
	if w := request("POST", "/api/task-link", body, false); w.Code != 401 {
		t.Fatalf("unauthorized association: %d", w.Code)
	}
	if w := request("POST", "/api/task-link", body, true); w.Code != 200 {
		t.Fatalf("association: %d %s", w.Code, w.Body.String())
	}
	w := request("GET", "/api/task-tree?id=epic", "", true)
	if w.Code != 200 {
		t.Fatalf("tree: %d %s", w.Code, w.Body.String())
	}
	var got, want any
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	actual, err := s.Tree("epic")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &want); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP and service trees differ: %s %s %v", w.Body.String(), encoded, err)
	}
	w = request("GET", "/api/tasks?compact=1&parent=epic", "", true)
	var page struct {
		Items []core.TaskSummary `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != "leaf" || page.Items[0].Parent != "epic" {
		t.Fatalf("parent list: %d %s", w.Code, w.Body.String())
	}
}

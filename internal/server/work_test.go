package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNextWorkAPIUsesSameSelectionAndAuthenticatedClaim(t *testing.T) {
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
		{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}},
		{ID: "leaf", Parent: "epic", Title: "叶子", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}},
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
	body := `{"parent":"epic","runner":"runner","capabilities":[],"resources":[],"parallel":false,"workspace":"","session":""}`
	if w := request("POST", "/api/work/claim", body, false); w.Code != 401 {
		t.Fatalf("unauthenticated claim: %d", w.Code)
	}
	w := request("GET", "/api/work/next?parent=epic&runner=runner", "", true)
	var api core.WorkResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &api) != nil {
		t.Fatalf("next: %d %s", w.Code, w.Body.String())
	}
	domain, err := s.NextWork(core.WorkRequest{Parent: "epic", Runner: "runner"})
	if err != nil || api.Action != domain.Action || api.Checkpoint.Task != domain.Checkpoint.Task || api.Checkpoint.Unit != domain.Checkpoint.Unit {
		t.Fatalf("API differs: %+v %+v %v", api, domain, err)
	}
	w = request("POST", "/api/work/claim", body, true)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &api) != nil || api.Action != "claimed" || api.Next == nil || api.Next.Attempt == nil {
		t.Fatalf("claim: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/work/checkpoint?parent=epic&runner=runner", "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), api.Next.Attempt.Token) {
		t.Fatalf("checkpoint leaked token or unavailable: %d %s", w.Code, w.Body.String())
	}
}

func TestWorkAPICompletesAlreadyClaimedChecks(t *testing.T) {
	for _, method := range []string{"command", "model"} {
		t.Run(method, func(t *testing.T) {
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
			if _, err = s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}}); err != nil {
				t.Fatal(err)
			}
			check := core.Check{ID: "proof", Kind: "functional", Method: method, Covers: []string{"结果"}, Required: true}
			if method == "command" {
				check.Command = []string{"sh", "-c", "test -f result.txt"}
			}
			if _, err = s.Create(core.Definition{ID: "leaf", Parent: "epic", Title: "叶", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}, Checks: []core.Check{check}}); err != nil {
				t.Fatal(err)
			}
			for _, stage := range []string{"define", "execute"} {
				n, err := s.Claim("leaf", "", "setup", "", "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = knowledge.Context(s, "leaf", "", 12000); err != nil {
					t.Fatal(err)
				}
				artifacts := []string{}
				if stage == "execute" {
					if err = os.WriteFile(filepath.Join(root, "result.txt"), []byte("actual result"), 0644); err != nil {
						t.Fatal(err)
					}
					artifacts = []string{"result.txt"}
				}
				a := n.Attempt
				if _, err = s.Submit(core.Submission{Task: "leaf", Attempt: a.ID, Token: a.Token, Key: a.ID, Result: core.StepResult{Status: "pass", Summary: stage, Artifacts: artifacts, Knowledge: &core.KnowledgeOutcome{Status: "unchanged", Reason: "fixture", Artifacts: []string{}}}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.ReleaseWork("epic", "setup", "handoff to API runner"); err != nil {
				t.Fatal(err)
			}
			h := New(s, "127.0.0.1:7331")
			post := func(path string, value any) *httptest.ResponseRecorder {
				body, _ := json.Marshal(value)
				r := httptest.NewRequest("POST", "http://"+h.Host+path, strings.NewReader(string(body)))
				r.Header.Set("Authorization", "Bearer "+h.Token)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			w := post("/api/work/claim", core.WorkRequest{Parent: "epic", Runner: "runner", Capabilities: []string{"model"}})
			var claim core.WorkResult
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &claim) != nil || claim.Action != "claimed" {
				t.Fatalf("claim: %d %s", w.Code, w.Body.String())
			}
			a := claim.Next.Attempt
			action := map[string]any{"action": "verify", "id": "leaf", "unit": "proof", "executor": "runner", "attempt": a.ID, "token": a.Token}
			if method == "model" {
				if err = os.WriteFile(filepath.Join(s.Store.Dir, "report.md"), []byte("assessment"), 0644); err != nil {
					t.Fatal(err)
				}
				action = map[string]any{"action": "assess", "attempt": a.ID, "token": a.Token, "assessment": core.Assessment{Task: "leaf", Check: "proof", Candidate: claim.Next.Task.Candidate, Method: "model", Executor: "runner", Result: "pass", Reason: "inspected", Inputs: []string{"result.txt"}, Artifacts: []string{"runtime:report.md"}}}
			}
			w = post("/api/action", action)
			var result core.Next
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Action != "completed" {
				t.Fatalf("claimed check: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

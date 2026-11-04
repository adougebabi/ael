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

func TestSessionHostOriginAndPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	bootstrap.Init(root, false, false)
	s, _, e := bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Store.Close()
	h := New(s, "127.0.0.1:7331")
	cases := []struct {
		host, origin, token, path, body, method string
		want                                    int
	}{{"evil.example", "", "", "/api/session", "", "GET", 403}, {h.Host, "http://evil.example", h.Token, "/api/tasks", "{}", "POST", 403}, {h.Host, "", "", "/api/tasks", "{}", "POST", 401}, {h.Host, "", h.Token, "/api/artifact?ref=../../etc/passwd", "", "GET", 409}, {h.Host, "", h.Token, "/api/tasks", "{\"title\":\"x\",\"evil\":true}", "POST", 409}, {h.Host, "", h.Token, "/api/session", "", "GET", 200}}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://"+c.host+c.path, strings.NewReader(c.body))
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.token != "" {
			r.Header.Set("Authorization", "Bearer "+c.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Fatalf("%s %d %s", c.path, w.Code, w.Body.String())
		}
	}
	os.Symlink("/etc/passwd", filepath.Join(root, "unsafe"))
	r := httptest.NewRequest("GET", "http://"+h.Host+"/api/artifact?ref=unsafe", nil)
	r.Header.Set("Authorization", "Bearer "+h.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatal("symlink artifact exposed")
	}
}

func TestKnowledgeLinksUseExistingTaskTitlesAndKeepMissingReferencesUnlinked(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, e := bootstrap.Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	s, _, e := bootstrap.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Store.Close()
	n, e := s.Create(core.Definition{ID: "real-task", Title: "可读任务标题", Type: "bug", Goal: "verified", Acceptance: []string{"真实关联"}})
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, ".ael/knowledge")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "rule.md"), knowledge.Encode(knowledge.Record{ID: "rule", Title: "规则", Kind: "spec", Status: "current", Tasks: []string{n.Task.Definition.ID, "missing"}, Body: "# 规则正文"}), 0644)
	h := New(s, "127.0.0.1:7331")
	r := httptest.NewRequest("GET", "http://"+h.Host+"/api/knowledge-record?id=rule", nil)
	r.Header.Set("Authorization", "Bearer "+h.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result struct {
		Links []knowledgeTaskLink `json:"related_tasks"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || len(result.Links) != 2 || result.Links[0].Title != "可读任务标题" || !result.Links[0].Available || result.Links[1].Available {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestWildcardListenerHostAndOrigin(t *testing.T) {
	h := New(nil, "0.0.0.0:7331")
	for _, c := range []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:7331", "http://127.0.0.1:7331", 200},
		{"192.168.10.20:7331", "http://192.168.10.20:7331", 200},
		{"localhost:7331", "http://localhost:7331", 200},
		{"evil.example:7331", "", 403},
		{"192.168.10.20:9999", "", 403},
		{"192.168.10.20:7331", "http://127.0.0.1:7331", 403},
	} {
		r := httptest.NewRequest("GET", "http://"+c.host+"/api/session", nil)
		r.Header.Set("Origin", c.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("host %s origin %s: got %d want %d", c.host, c.origin, w.Code, c.want)
		}
	}
}

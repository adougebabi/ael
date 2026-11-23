package server

import (
	"ael/internal/core"
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type remoteBlockingBody struct {
	started chan struct{}
	release chan struct{}
	data    *bytes.Reader
	once    bool
}

func (b *remoteBlockingBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.started)
		<-b.release
	}
	return b.data.Read(p)
}
func (b *remoteBlockingBody) Close() error { return nil }

func TestRemoteTasksAreScopedRevisionedAndIdempotent(t *testing.T) {
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	admin, csrf := standaloneLogin(t, h, "owner", "fixture-admin-password")
	postAdmin := func(path string, body any) map[string]any {
		return standaloneJSON(t, standaloneRequest(t, h, "POST", path, body, admin, csrf, "", "http://server.test", "1"), http.StatusCreated)
	}
	postAdmin("/api/v1/projects", map[string]string{"id": "one", "name": "One"})
	postAdmin("/api/v1/projects", map[string]string{"id": "two", "name": "Two"})
	postAdmin("/api/v1/users", map[string]string{"username": "client-a", "password": "fixture-client-password"})
	postAdmin("/api/v1/users", map[string]string{"username": "client-b", "password": "fixture-client-password"})
	postAdmin("/api/v1/projects/one/members", map[string]string{"username": "client-a", "role": "executor"})
	postAdmin("/api/v1/projects/one/members", map[string]string{"username": "client-b", "role": "executor"})
	postAdmin("/api/v1/projects/two/members", map[string]string{"username": "client-b", "role": "executor"})
	members := standaloneJSON(t, standaloneRequest(t, h, "GET", "/api/v1/projects/one/members", nil, admin, csrf, "", "", ""), http.StatusOK)["members"].([]any)
	if len(members) != 2 || members[0].(map[string]any)["username"] != "client-a" || members[0].(map[string]any)["role"] != "executor" {
		t.Fatal("project members", members)
	}
	a := postAdmin("/api/v1/tokens", map[string]string{"username": "client-a", "project_id": "one", "label": "A"})
	b := postAdmin("/api/v1/tokens", map[string]string{"username": "client-b", "project_id": "one", "label": "B"})
	other := postAdmin("/api/v1/tokens", map[string]string{"username": "client-b", "project_id": "two", "label": "other"})
	tokenA, tokenB, tokenOther := a["token"].(string), b["token"].(string), other["token"].(string)
	if w := standaloneRequest(t, h, "GET", "/api/v1/projects/one/members", nil, nil, "", tokenA, "", ""); w.Code != http.StatusForbidden {
		t.Fatal("member list must require an admin session", w.Code)
	}
	parent := core.Definition{ID: "parent", Title: "Parent", Type: "epic", Goal: "verified", Acceptance: []string{"integrated"}}
	create := map[string]any{"operation_id": "op-parent", "idempotency_key": "key-parent", "expected_revision": "absent", "definition": parent}
	w := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", create, nil, "", tokenA, "", "1")
	created := standaloneJSON(t, w, http.StatusCreated)
	if created["operation_id"] != "op-parent" || created["revision"] == "" {
		t.Fatalf("missing operation receipt: %+v", created)
	}
	lookup := standaloneRequest(t, h, "GET", "/api/v1/projects/one/operations/key-parent", nil, nil, "", tokenA, "", "")
	if lookup.Code != http.StatusOK || lookup.Body.String() != w.Body.String() {
		t.Fatalf("operation could not be queried after a lost response: %d %s", lookup.Code, lookup.Body.String())
	}
	if denied := standaloneRequest(t, h, "GET", "/api/v1/projects/one/operations/key-parent", nil, nil, "", tokenB, "", ""); denied.Code != http.StatusNotFound {
		t.Fatalf("another actor read an operation receipt: %d", denied.Code)
	}
	replay := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", create, nil, "", tokenA, "", "1")
	if replay.Code != http.StatusCreated || replay.Body.String() != w.Body.String() {
		t.Fatalf("idempotent create did not replay exact result: %d %s", replay.Code, replay.Body.String())
	}
	altered := map[string]any{"operation_id": "op-parent", "idempotency_key": "key-parent", "expected_revision": "absent", "definition": core.Definition{ID: "different", Title: "Different", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}}
	if conflict := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", altered, nil, "", tokenA, "", "1"); conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency key reused for different request: %d %s", conflict.Code, conflict.Body.String())
	}
	otherParent := parent
	otherParent.Title = "Other project parent"
	otherCreate := map[string]any{"operation_id": "op-parent", "idempotency_key": "key-parent", "expected_revision": "absent", "definition": otherParent}
	otherReceipt := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/two/tasks", otherCreate, nil, "", tokenOther, "", "1"), http.StatusCreated)
	otherTask, _ := otherReceipt["task"].(map[string]any)
	otherDefinition, _ := otherTask["definition"].(map[string]any)
	if otherDefinition["title"] != "Other project parent" {
		t.Fatalf("same task ID was not isolated by project: %+v", otherReceipt)
	}
	child := core.Definition{ID: "child", Parent: "parent", Title: "Child", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}
	childCreate := map[string]any{"operation_id": "op-child", "idempotency_key": "key-child", "expected_revision": "absent", "definition": child}
	childReceipt := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", childCreate, nil, "", tokenA, "", "1"), http.StatusCreated)
	read := standaloneRequest(t, h, "GET", "/api/v1/projects/one/tasks/child", nil, nil, "", tokenB, "", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"parent":"parent"`) {
		t.Fatalf("second client did not see child: %d %s", read.Code, read.Body.String())
	}
	if denied := standaloneRequest(t, h, "GET", "/api/v1/projects/one/tasks/child", nil, nil, "", tokenOther, "", ""); denied.Code != http.StatusNotFound {
		t.Fatalf("cross-project task read accepted: %d %s", denied.Code, denied.Body.String())
	}
	tree := standaloneRequest(t, h, "GET", "/api/v1/projects/one/tasks/parent/tree", nil, nil, "", tokenB, "", "")
	if tree.Code != http.StatusOK || !strings.Contains(tree.Body.String(), `"required":1`) || !strings.Contains(tree.Body.String(), `"id":"child"`) {
		t.Fatalf("second client did not see the task tree: %d %s", tree.Code, tree.Body.String())
	}
	pause := map[string]any{"operation_id": "op-pause", "idempotency_key": "key-pause", "expected_revision": childReceipt["revision"], "reason": "fixture pause"}
	paused := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/child/pause", pause, nil, "", tokenA, "", "1"), http.StatusOK)
	if task, ok := paused["task"].(map[string]any); !ok || task["paused"] != true {
		t.Fatalf("remote pause did not affect authoritative state: %+v", paused)
	}
	resume := map[string]any{"operation_id": "op-resume", "idempotency_key": "key-resume", "expected_revision": paused["revision"]}
	resumed := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/child/resume", resume, nil, "", tokenB, "", "1"), http.StatusOK)
	if task, ok := resumed["task"].(map[string]any); !ok || task["paused"] != false {
		t.Fatalf("remote resume did not affect authoritative state: %+v", resumed)
	}
	revision := created["revision"].(string)
	parent.Title = "first edit"
	edit := map[string]any{"operation_id": "op-edit-a", "idempotency_key": "key-edit-a", "expected_revision": revision, "definition": parent, "reason": "fixture edit"}
	standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/parent/edit", edit, nil, "", tokenA, "", "1"), http.StatusOK)
	parent.Title = "stale edit"
	stale := map[string]any{"operation_id": "op-edit-b", "idempotency_key": "key-edit-b", "expected_revision": revision, "definition": parent, "reason": "fixture edit"}
	if conflict := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/parent/edit", stale, nil, "", tokenB, "", "1"); conflict.Code != http.StatusConflict {
		t.Fatalf("stale edit did not conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	planParent := core.Definition{ID: "plan-parent", Title: "Plan Parent", Type: "epic", Goal: "verified", Acceptance: []string{"overall"}}
	planCreate := map[string]any{"operation_id": "op-plan-parent", "idempotency_key": "key-plan-parent", "expected_revision": "absent", "definition": planParent}
	planParentReceipt := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", planCreate, nil, "", tokenA, "", "1"), http.StatusCreated)
	prepare := map[string]any{"operation_id": "op-prepare", "idempotency_key": "key-prepare", "expected_revision": planParentReceipt["revision"], "input": core.PlanInput{Request: "two isolated results"}}
	prepared := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/plan-parent/plan/prepare", prepare, nil, "", tokenA, "", "1"), http.StatusOK)
	preparedResult, ok := prepared["result"].(map[string]any)
	if !ok || preparedResult["input_hash"] == "" {
		t.Fatalf("plan preparation lost its result: %+v", prepared)
	}
	leaf := func(key string, order int) core.PlanLeaf {
		return core.PlanLeaf{Key: key, Definition: core.Definition{Title: key, Type: "bug", Goal: "verified", Scope: []string{key + ".txt"}, NonGoals: []string{"no release"}, Acceptance: []string{"result"}, Checks: []core.Check{{ID: "unit", Kind: "functional", Method: "command", Command: []string{"sh", "-c", "true"}, Covers: []string{"result"}, Required: true}}}, Deliverables: []string{key + ".txt"}, Basis: []string{"fixture"}, Capabilities: []string{"development"}, Order: order, Required: true}
	}
	proposal := core.PlanProposal{Version: 1, ID: "plan-op", Parent: "plan-parent", ParentRevision: 1, InputHash: preparedResult["input_hash"].(string), Request: "two isolated results", Leaves: []core.PlanLeaf{leaf("one", 1), leaf("two", 2)}, Coverage: []core.PlanCoverage{{Acceptance: "overall", Leaves: []string{"one", "two"}, Integration: "run integration check"}}}
	preview := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/plan-parent/plan/preview", map[string]any{"proposal": proposal}, nil, "", tokenB, "", "1"), http.StatusOK)
	apply := map[string]any{"operation_id": "plan-op", "idempotency_key": "plan-op", "expected_revision": planParentReceipt["revision"], "proposal": proposal, "digest": preview["digest"]}
	plan := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/plan-parent/plan/apply", apply, nil, "", tokenA, "", "1")
	planResult := standaloneJSON(t, plan, http.StatusOK)
	if result, ok := planResult["result"].(map[string]any); !ok || result["status"] != "applied" {
		t.Fatalf("plan was not applied: %+v", planResult)
	}
	planReplay := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/plan-parent/plan/apply", apply, nil, "", tokenA, "", "1")
	if planReplay.Code != http.StatusOK || planReplay.Body.String() != plan.Body.String() {
		t.Fatalf("plan replay changed result: %d %s", planReplay.Code, planReplay.Body.String())
	}
	plannedTree := standaloneRequest(t, h, "GET", "/api/v1/projects/one/tasks/plan-parent/tree", nil, nil, "", tokenB, "", "")
	if plannedTree.Code != http.StatusOK || !strings.Contains(plannedTree.Body.String(), `"required":2`) {
		t.Fatalf("second client did not see applied plan: %d %s", plannedTree.Code, plannedTree.Body.String())
	}
	pendingParent := core.Definition{ID: "pending-parent", Title: "Pending Parent", Type: "epic", Goal: "verified", Acceptance: []string{"overall"}}
	pendingCreate := map[string]any{"operation_id": "op-pending-parent", "idempotency_key": "key-pending-parent", "expected_revision": "absent", "definition": pendingParent}
	pendingParentReceipt := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", pendingCreate, nil, "", tokenA, "", "1"), http.StatusCreated)
	pendingPrepare := map[string]any{"operation_id": "op-pending-prepare", "idempotency_key": "key-pending-prepare", "expected_revision": pendingParentReceipt["revision"], "input": core.PlanInput{Request: "two isolated results"}}
	pendingPrepared := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/pending-parent/plan/prepare", pendingPrepare, nil, "", tokenA, "", "1"), http.StatusOK)
	pendingProposal := proposal
	pendingProposal.ID, pendingProposal.Parent = "pending-op", "pending-parent"
	pendingProposal.InputHash = pendingPrepared["result"].(map[string]any)["input_hash"].(string)
	pendingProposal.Leaves = append([]core.PlanLeaf(nil), proposal.Leaves...)
	pendingProposal.Leaves[0].Definition.ID, pendingProposal.Leaves[1].Definition.ID = "pending-one", "pending-two"
	pendingPreview := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/pending-parent/plan/preview", map[string]any{"proposal": pendingProposal}, nil, "", tokenA, "", "1"), http.StatusOK)
	pendingApply := map[string]any{"operation_id": "pending-op", "idempotency_key": "pending-op", "expected_revision": pendingParentReceipt["revision"], "proposal": pendingProposal, "digest": pendingPreview["digest"]}
	service, err := s.projectService("one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec(`CREATE TRIGGER test_remote_http_plan BEFORE INSERT ON tasks WHEN NEW.id='pending-two' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if failed := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/pending-parent/plan/apply", pendingApply, nil, "", tokenA, "", "1"); failed.Code != http.StatusConflict {
		t.Fatalf("injected plan failure was not retained: %d %s", failed.Code, failed.Body.String())
	}
	if _, err = service.Store.DB.Exec(`DROP TRIGGER test_remote_http_plan`); err != nil {
		t.Fatal(err)
	}
	pending := standaloneJSON(t, standaloneRequest(t, h, "GET", "/api/v1/projects/one/operations/pending-op", nil, nil, "", tokenA, "", ""), http.StatusOK)
	if pending["status"] != "pending" {
		t.Fatalf("incomplete operation was not queryable: %+v", pending)
	}
	abandon := map[string]any{"operation_id": "abandon-op", "idempotency_key": "abandon-key", "expected_revision": pendingParentReceipt["revision"], "plan_id": "pending-op", "reason": "review partial work"}
	abandoned := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks/pending-parent/plan/abandon", abandon, nil, "", tokenA, "", "1"), http.StatusOK)
	if result, ok := abandoned["result"].(map[string]any); !ok || result["status"] != "abandoned" {
		t.Fatalf("abandonment did not return its receipt: %+v", abandoned)
	}
	original := standaloneJSON(t, standaloneRequest(t, h, "GET", "/api/v1/projects/one/operations/pending-op", nil, nil, "", tokenA, "", ""), http.StatusOK)
	if result, ok := original["result"].(map[string]any); !ok || result["status"] != "abandoned" {
		t.Fatalf("original operation lost its abandoned outcome: %+v", original)
	}
	concurrent := map[string]any{"operation_id": "concurrent-op", "idempotency_key": "concurrent-key", "expected_revision": "absent", "definition": core.Definition{ID: "concurrent", Title: "Concurrent", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}}
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 4)
	for i := 0; i < 4; i++ {
		go func() {
			<-start
			results <- standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", concurrent, nil, "", tokenA, "", "1")
		}()
	}
	close(start)
	var firstResponse string
	for i := 0; i < 4; i++ {
		response := <-results
		if response.Code != http.StatusCreated || (firstResponse != "" && response.Body.String() != firstResponse) {
			t.Fatalf("concurrent replay differed: %d %s", response.Code, response.Body.String())
		}
		firstResponse = response.Body.String()
	}
	var taskCount int
	if err = service.Store.DB.QueryRow("SELECT count(*) FROM tasks WHERE id='concurrent'").Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatalf("concurrent replay created duplicate tasks: %d %v", taskCount, err)
	}
	standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/tokens/revoke", map[string]any{"id": a["id"]}, admin, csrf, "", "http://server.test", "1"), http.StatusOK)
	if denied := standaloneRequest(t, h, "POST", "/api/v1/projects/one/tasks", childCreate, nil, "", tokenA, "", "1"); denied.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still wrote tasks: %d %s", denied.Code, denied.Body.String())
	}
}

func TestRemoteRevocationFencesInFlightWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	if err = s.createUser("worker", "fixture-worker-password"); err != nil {
		t.Fatal(err)
	}
	if err = s.createProject("project", "Project"); err != nil {
		t.Fatal(err)
	}
	if err = s.addMember("project", "worker", "executor"); err != nil {
		t.Fatal(err)
	}
	tokenID, token, err := s.issueToken("worker", "project", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	other, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	otherHandler, err := other.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	admin, csrf := standaloneLogin(t, otherHandler, "owner", "fixture-admin-password")
	body := &remoteBlockingBody{started: make(chan struct{}), release: make(chan struct{}), data: bytes.NewReader([]byte(`{"operation_id":"op-write","idempotency_key":"key-write","expected_revision":"absent","definition":{"id":"task","title":"Task","type":"bug","goal":"verified","acceptance":["result"]}}`))}
	r := httptest.NewRequest("POST", "http://server.test/api/v1/projects/project/tasks", body)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-AEL-Protocol", "1")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	<-body.started
	revoked := standaloneRequest(t, otherHandler, "POST", "/api/v1/tokens/revoke", map[string]string{"id": tokenID}, admin, csrf, "", "http://server.test", "1")
	if revoked.Code != http.StatusOK {
		close(body.release)
		<-done
		t.Fatalf("revoke failed: %d %s", revoked.Code, revoked.Body.String())
	}
	close(body.release)
	<-done
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked in-flight write was accepted: %d %s", w.Code, w.Body.String())
	}
	service, err := s.projectService("project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Get("task"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked task was persisted: %v", err)
	}
}

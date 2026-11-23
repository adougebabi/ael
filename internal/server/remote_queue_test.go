package server

import (
	"ael/internal/core"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type queueFixture struct {
	artifactFixture
	service    *core.Service
	bot, codex string
	contexts   int
}

// newQueueFixture: a Bot and a local Codex use separate executor accounts and
// runners against the same project, as two devices would.
func newQueueFixture(t *testing.T, tasks ...core.Definition) *queueFixture {
	t.Helper()
	f := &queueFixture{artifactFixture: newArtifactFixture(t)}
	tokens := map[string]string{}
	for _, user := range []string{"bot", "codex"} {
		if err := f.s.createUser(user, "fixture-user-password"); err != nil {
			t.Fatal(err)
		}
		if err := f.s.addMember("shared", user, "executor"); err != nil {
			t.Fatal(err)
		}
		_, token, err := f.s.issueToken(user, "shared", "fixture")
		if err != nil {
			t.Fatal(err)
		}
		tokens[user] = token
	}
	f.bot, f.codex = tokens["bot"], tokens["codex"]
	var err error
	if f.service, err = f.s.projectService("shared"); err != nil {
		t.Fatal(err)
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/repositories", map[string]any{"id": "repo-one", "expected_revision": "absent", "operation_id": "repo", "idempotency_key": "repo"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	for _, r := range []struct{ id, token, alias string }{{"bot-1", f.bot, "Bot A"}, {"bot-2", f.bot, "Bot A"}, {"codex-1", f.codex, "Codex"}, {"codex-2", f.codex, "Codex"}} {
		body := map[string]any{"id": r.id, "workspace_id": "ws-" + r.id, "checkout_id": "co-" + r.id, "repository": "repo-one", "version": "fixture", "capabilities": []string{"development"}, "alias": r.alias, "expected_revision": "absent", "operation_id": "reg-" + r.id, "idempotency_key": "reg-" + r.id}
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners", body, nil, "", r.token, "", "1"), http.StatusCreated)
	}
	for _, d := range tasks {
		create := map[string]any{"operation_id": "create-" + d.ID, "idempotency_key": "create-" + d.ID, "expected_revision": "absent", "definition": d}
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks", create, nil, "", f.uploader, "", "1"), http.StatusCreated)
	}
	return f
}

func queueDef(id string, deps ...string) core.Definition {
	return core.Definition{ID: id, Title: "Task " + id, Type: "bug", Goal: "verified", Acceptance: []string{"result"}, Dependencies: deps}
}

func (f *queueFixture) task(t *testing.T, id string) core.Task {
	t.Helper()
	task, err := f.service.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// claimContext prepares the observation and context an ordinary claim needs.
func (f *queueFixture) claimContext(t *testing.T, token, runner, task string) (string, string) {
	t.Helper()
	revision := core.RemoteRevision(f.task(t, task))
	candidate := portableFixtureCandidate(t)
	f.contexts++
	key := fmt.Sprintf("obs-%s-%d", runner, f.contexts)
	observation := map[string]any{"task_id": task, "expected_revision": revision, "checkout_id": "co-" + runner, "workspace_id": "ws-" + runner, "runner_id": runner, "candidate": candidate, "operation_id": key, "idempotency_key": key}
	seen := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/candidates", observation, nil, "", token, "", "1"), http.StatusCreated)
	current := f.task(t, task)
	contextID := fmt.Sprintf("ctx-%s-%d", runner, f.contexts)
	body, _ := json.Marshal(map[string]any{"id": contextID, "task_id": task, "task_revision": revision, "definition_revision": current.Definition.Revision, "plan_revision": current.Plan.Revision, "policy_hash": current.PolicyHash, "candidate_id": candidate.ID, "observation_id": seen["id"], "repository": "repo-one", "checkout_id": "co-" + runner, "workspace_id": "ws-" + runner, "runner_id": runner, "sources": []any{}, "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	if _, err := f.service.Store.DB.Exec("INSERT INTO portable_contexts(id,task_id,candidate_id,body,created) VALUES(?,?,?,?,?)", contextID, task, candidate.ID, string(body), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return contextID, revision
}

func (f *queueFixture) claim(t *testing.T, token, runner, task, key string) *httpResult {
	t.Helper()
	contextID, revision := f.claimContext(t, token, runner, task)
	body := map[string]any{"task_id": task, "context_id": contextID, "expected_revision": revision, "operation_id": key, "idempotency_key": key}
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/"+runner+"/claim", body, nil, "", token, "", "1")
	return &httpResult{w.Code, w.Body.String()}
}

type httpResult struct {
	code int
	body string
}

func (f *queueFixture) post(t *testing.T, token, runner, action string, body map[string]any) *httpResult {
	t.Helper()
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/"+runner+"/"+action, body, nil, "", token, "", "1")
	return &httpResult{w.Code, w.Body.String()}
}

func (r *httpResult) json(t *testing.T, want int) map[string]any {
	t.Helper()
	if r.code != want {
		t.Fatalf("status %d, want %d: %s", r.code, want, r.body)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *queueFixture) enqueue(t *testing.T, runner, task string) map[string]any {
	t.Helper()
	receipt := f.post(t, f.bot, runner, "queue", map[string]any{"task_id": task, "expected_version": "absent", "operation_id": "q-" + task, "idempotency_key": "q-" + task}).json(t, http.StatusOK)
	return receipt["queue"].(map[string]any)
}

func (f *queueFixture) list(t *testing.T, token, query string) map[string]map[string]any {
	t.Helper()
	v := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/queue"+query, nil, nil, "", token, "", ""), http.StatusOK)
	items := map[string]map[string]any{}
	for _, raw := range v["items"].([]any) {
		item := raw.(map[string]any)
		items[item["task_id"].(string)] = item
	}
	return items
}

func TestQueueMarkersAreSharedAndDoNotStartWork(t *testing.T) {
	f := newQueueFixture(t, queueDef("t1"), queueDef("t2"), queueDef("t3"), queueDef("t4"), queueDef("t5"), queueDef("free"), queueDef("dep", "t1"), queueDef("held"))
	before, steps := map[string]string{}, map[string]string{}
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5"} {
		before[id], steps[id] = core.RemoteRevision(f.task(t, id)), f.task(t, id).Step
		f.enqueue(t, "bot-1", id)
	}
	// The other client reads the same server record.
	items := f.list(t, f.codex, "?runner=codex-1")
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5"} {
		item := items[id]
		queue := item["queue"].(map[string]any)
		if item["assignment"] != "queued" || queue["runner"] != "bot-1" || queue["runner_label"] != "Bot A" || item["claimable_for_runner"] != false || item["claimable"] != false {
			t.Fatalf("queued task %s not visible as Bot A's queue: %+v", id, item)
		}
		task := f.task(t, id)
		if len(task.Attempts) != 0 || task.Step != steps[id] || core.RemoteRevision(task) != before[id] {
			t.Fatalf("queueing changed task state for %s: attempts=%d step=%s", id, len(task.Attempts), task.Step)
		}
		summary, err := f.service.Summarize(core.Next{Task: &task, Board: "pending", Action: "continue", Step: task.Step})
		if err != nil || summary.Running != 0 || summary.Activity != nil {
			t.Fatalf("queue counted as running work or progress: %+v %v", summary, err)
		}
	}
	if items["free"]["assignment"] != "unassigned" || items["free"]["claimable"] != true || items["free"]["claimable_for_runner"] != true {
		t.Fatalf("unassigned runnable task not claimable: %+v", items["free"])
	}
	if items["dep"]["claimable"] != false || items["dep"]["next_action"] != "wait_dependency" {
		t.Fatalf("dependency blocked task reported claimable: %+v", items["dep"])
	}
	// Only unassigned and runnable tasks are offered to Codex.
	claimable := f.list(t, f.codex, "?runner=codex-1&state=claimable")
	// "task" is the base fixture's own unassigned task.
	if len(claimable) != 3 || claimable["free"] == nil || claimable["held"] == nil || claimable["task"] == nil {
		t.Fatalf("claimable listing wrong: %v", claimable)
	}
	// Paused tasks cannot be queued and stay non-claimable.
	held := f.task(t, "held")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/held/pause", map[string]any{"operation_id": "pause-held", "idempotency_key": "pause-held", "expected_revision": core.RemoteRevision(held), "reason": "wait for env"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	if r := f.post(t, f.bot, "bot-1", "queue", map[string]any{"task_id": "held", "expected_version": "absent", "operation_id": "q-held", "idempotency_key": "q-held"}); r.code != http.StatusConflict || !strings.Contains(r.body, "NOT_RUNNABLE") {
		t.Fatalf("paused task queued: %d %s", r.code, r.body)
	}
	if f.list(t, f.codex, "?task=held")["held"]["claimable"] != false {
		t.Fatal("paused task claimable")
	}
	// Codex can still claim the unassigned task; the dependency stays blocked.
	f.claim(t, f.codex, "codex-1", "free", "claim-free").json(t, http.StatusCreated)
	if r := f.claim(t, f.bot, "bot-2", "dep", "claim-dep"); r.code != http.StatusConflict || !strings.Contains(r.body, "DEPENDENCY_BLOCKED") {
		t.Fatalf("dependency claim: %d %s", r.code, r.body)
	}
	// Codex cannot bypass Bot A's queue with an ordinary claim.
	if r := f.claim(t, f.codex, "codex-2", "t2", "claim-t2-codex"); r.code != http.StatusConflict || !strings.Contains(r.body, "QUEUE_RESERVED") || !strings.Contains(r.body, "Bot A") {
		t.Fatalf("queue bypassed: %d %s", r.code, r.body)
	}
	// The workbench projection carries the marker for the UI.
	page, err := f.service.Workbench(core.Query{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range page.Items {
		if row.Task.ID == "t3" {
			found = row.Task.Assignment == "queued" && row.Task.Queue != nil && row.Task.Queue.RunnerLabel == "Bot A"
		}
	}
	if !found {
		t.Fatal("workbench row lacks queue marker")
	}
}

func TestQueueClaimTransferReleaseAndLifecycle(t *testing.T) {
	f := newQueueFixture(t, queueDef("own"), queueDef("move"), queueDef("drop"), queueDef("scope"), queueDef("edit"), queueDef("stop"))
	for _, id := range []string{"own", "move", "drop", "edit", "stop"} {
		f.enqueue(t, "bot-1", id)
	}
	// The holder's claim converts the marker into the existing Attempt.
	f.claim(t, f.bot, "bot-1", "own", "claim-own").json(t, http.StatusCreated)
	own := f.list(t, f.codex, "?task=own")["own"]
	if own["assignment"] != "started" || own["queue"] != nil {
		t.Fatalf("claimed task still queued: %+v", own)
	}
	// Transfer without the original Bot being online; Codex is not the holder
	// and not admin, so it cannot take the record itself.
	move := f.list(t, f.codex, "?task=move")["move"]["queue"].(map[string]any)
	steal := map[string]any{"task_id": "move", "expected_version": move["version"], "to_runner": "codex-1", "reason": "local capacity", "operation_id": "steal", "idempotency_key": "steal"}
	if r := f.post(t, f.codex, "bot-1", "queue-transfer", steal); r.code != http.StatusForbidden {
		t.Fatalf("non-holder transferred: %d %s", r.code, r.body)
	}
	before := core.RemoteRevision(f.task(t, "move"))
	transfer := map[string]any{"task_id": "move", "expected_version": move["version"], "to_runner": "codex-1", "reason": "local capacity", "operation_id": "move-1", "idempotency_key": "move-1"}
	first := f.post(t, f.bot, "bot-1", "queue-transfer", transfer)
	moved := first.json(t, http.StatusOK)
	if replay := f.post(t, f.bot, "bot-1", "queue-transfer", transfer); replay.code != http.StatusOK || replay.body != first.body {
		t.Fatalf("transfer replay differed: %s", replay.body)
	}
	changed := map[string]any{}
	for k, v := range transfer {
		changed[k] = v
	}
	changed["reason"] = "different"
	if r := f.post(t, f.bot, "bot-1", "queue-transfer", changed); r.code != http.StatusConflict || !strings.Contains(r.body, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("changed replay accepted: %d %s", r.code, r.body)
	}
	if core.RemoteRevision(f.task(t, "move")) != before || moved["revision"] != before {
		t.Fatal("transfer changed the task revision")
	}
	// The Bot's stale local queue item is refused (bot-1 is busy with "own",
	// so its idle slot bot-2 tries); Codex claims legally.
	if r := f.claim(t, f.bot, "bot-2", "move", "claim-move-bot"); r.code != http.StatusConflict || !strings.Contains(r.body, "QUEUE_RESERVED") {
		t.Fatalf("old Bot queue claimed: %d %s", r.code, r.body)
	}
	f.claim(t, f.codex, "codex-1", "move", "claim-move-codex").json(t, http.StatusCreated)
	// After start, release/transfer cannot bypass the existing handoff rules.
	stale := map[string]any{"task_id": "move", "expected_version": moved["queue"].(map[string]any)["version"], "to_runner": "bot-1", "reason": "back", "operation_id": "move-back", "idempotency_key": "move-back"}
	if r := f.post(t, f.codex, "codex-1", "queue-transfer", stale); r.code != http.StatusConflict || !strings.Contains(r.body, "QUEUE_STARTED") {
		t.Fatalf("started task transferred: %d %s", r.code, r.body)
	}
	// Release is versioned and idempotent; the task itself is retained.
	drop := f.list(t, f.bot, "?task=drop")["drop"]["queue"].(map[string]any)
	if r := f.post(t, f.bot, "bot-1", "queue-release", map[string]any{"task_id": "drop", "expected_version": "QV-old", "operation_id": "drop-old", "idempotency_key": "drop-old"}); r.code != http.StatusConflict || !strings.Contains(r.body, "QUEUE_VERSION_CONFLICT") {
		t.Fatalf("stale release accepted: %d %s", r.code, r.body)
	}
	release := map[string]any{"task_id": "drop", "expected_version": drop["version"], "reason": "removed from Bot queue", "operation_id": "drop-1", "idempotency_key": "drop-1"}
	firstRelease := f.post(t, f.bot, "bot-1", "queue-release", release)
	firstRelease.json(t, http.StatusOK)
	if replay := f.post(t, f.bot, "bot-1", "queue-release", release); replay.body != firstRelease.body {
		t.Fatalf("release replay differed: %s", replay.body)
	}
	var events int
	if err := f.service.Store.DB.QueryRow("SELECT count(*) FROM events WHERE task_id='drop' AND kind='remote_queue_released'").Scan(&events); err != nil || events != 1 {
		t.Fatalf("release side effect repeated: %d %v", events, err)
	}
	if item := f.list(t, f.codex, "?task=drop")["drop"]; item["assignment"] != "unassigned" || item["claimable"] != true {
		t.Fatalf("released task not unassigned: %+v", item)
	}
	// Executor scope: any runner of the Bot account may start it; Codex not.
	f.post(t, f.bot, "bot-1", "queue", map[string]any{"task_id": "scope", "expected_version": "absent", "scope": "executor", "operation_id": "q-scope", "idempotency_key": "q-scope"}).json(t, http.StatusOK)
	if r := f.claim(t, f.codex, "codex-1", "scope", "claim-scope-codex"); r.code != http.StatusConflict {
		t.Fatalf("executor-scope record bypassed: %d %s", r.code, r.body)
	}
	f.claim(t, f.bot, "bot-2", "scope", "claim-scope-bot2").json(t, http.StatusCreated)
	// Definition change and cancellation clear the old marker.
	edit := f.task(t, "edit")
	revised := edit.Definition
	revised.Acceptance = []string{"result", "new scope"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/edit/edit", map[string]any{"operation_id": "edit-1", "idempotency_key": "edit-1", "expected_revision": core.RemoteRevision(edit), "reason": "scope changed", "definition": revised}, nil, "", f.uploader, "", "1"), http.StatusOK)
	stop := f.task(t, "stop")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/stop/cancel", map[string]any{"operation_id": "stop-1", "idempotency_key": "stop-1", "expected_revision": core.RemoteRevision(stop), "reason": "not needed"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	for id, reason := range map[string]string{"edit": "definition_changed", "stop": "cancelled"} {
		var body string
		if err := f.service.Store.DB.QueryRow("SELECT body FROM events WHERE task_id=? AND kind='remote_queue_cleared' ORDER BY id DESC LIMIT 1", id).Scan(&body); err != nil || !strings.Contains(body, reason) {
			t.Fatalf("%s marker not cleared with %s: %s %v", id, reason, body, err)
		}
		var left int
		_ = f.service.Store.DB.QueryRow("SELECT count(*) FROM metadata WHERE key=?", "remote-queue:"+id).Scan(&left)
		if left != 0 {
			t.Fatalf("%s retains a startable marker", id)
		}
	}
	// The revised task follows its own conditions again, not the old record.
	if item := f.list(t, f.codex, "?task=edit")["edit"]; item["assignment"] != "unassigned" || item["queue"] != nil {
		t.Fatalf("revised task stayed reserved by the stale record: %+v", item)
	}
	if r := f.claim(t, f.codex, "codex-2", "edit", "claim-edit"); strings.Contains(r.body, "QUEUE_RESERVED") {
		t.Fatalf("stale record still gates the claim: %s", r.body)
	}
}

func TestQueueAdminSessionTransferAndAuthorization(t *testing.T) {
	f := newQueueFixture(t, queueDef("a"))
	queue := f.enqueue(t, "bot-1", "a")
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/bot-1/queue", map[string]any{"task_id": "a", "expected_version": "absent", "operation_id": "r", "idempotency_key": "r"}, nil, "", f.reader, "", "1"); w.Code != http.StatusForbidden {
		t.Fatalf("reader queued: %d", w.Code)
	}
	// Codex cannot enqueue on another account's runner.
	if r := f.post(t, f.codex, "bot-1", "queue", map[string]any{"task_id": "a", "expected_version": queue["version"], "operation_id": "x", "idempotency_key": "x"}); r.code != http.StatusNotFound {
		t.Fatalf("foreign runner queued: %d %s", r.code, r.body)
	}
	if r := f.post(t, f.codex, "codex-1", "queue", map[string]any{"task_id": "a", "expected_version": "absent", "operation_id": "y", "idempotency_key": "y"}); r.code != http.StatusConflict || !strings.Contains(r.body, "QUEUE_RESERVED") {
		t.Fatalf("second queue owner: %d %s", r.code, r.body)
	}
	// The user transfers from the dashboard session without the Bot online.
	body := map[string]any{"task_id": "a", "expected_version": queue["version"], "to_runner": "codex-1", "reason": "move to local Codex", "operation_id": "ui-1", "idempotency_key": "ui-1"}
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/bot-1/queue-transfer", body, f.admin, f.csrf, "", "http://server.test", "1")
	moved := standaloneJSON(t, w, http.StatusOK)["queue"].(map[string]any)
	if moved["runner"] != "codex-1" || moved["transferred_from"] != "bot-1" {
		t.Fatalf("transfer receipt: %+v", moved)
	}
	if r := f.post(t, f.bot, "bot-1", "queue", map[string]any{"task_id": "a", "expected_version": queue["version"], "operation_id": "refresh", "idempotency_key": "refresh"}); r.code != http.StatusConflict {
		t.Fatalf("old holder refreshed a transferred record: %d %s", r.code, r.body)
	}
}

// Transfer and the holder's claim race; exactly one wins every time.
func TestQueueTransferAndClaimRaceHasOneOutcome(t *testing.T) {
	outcomes := map[string]int{}
	defer func() { t.Logf("race outcomes: %v", outcomes) }()
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("race%d", i)
		f := newQueueFixture(t, queueDef(id))
		queue := f.enqueue(t, "bot-1", id)
		contextID, revision := f.claimContext(t, f.bot, "bot-1", id)
		var wg sync.WaitGroup
		var claim, transfer *httpResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/bot-1/claim", map[string]any{"task_id": id, "context_id": contextID, "expected_revision": revision, "operation_id": "c", "idempotency_key": "c"}, nil, "", f.bot, "", "1")
			claim = &httpResult{w.Code, w.Body.String()}
		}()
		go func() {
			defer wg.Done()
			w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/runners/bot-1/queue-transfer", map[string]any{"task_id": id, "expected_version": queue["version"], "to_runner": "codex-1", "reason": "rebalance", "operation_id": "t", "idempotency_key": "t"}, f.admin, f.csrf, "", "http://server.test", "1")
			transfer = &httpResult{w.Code, w.Body.String()}
		}()
		wg.Wait()
		task := f.task(t, id)
		switch {
		case claim.code == http.StatusCreated && transfer.code == http.StatusConflict && strings.Contains(transfer.body, "QUEUE_STARTED"):
			outcomes["claim"]++
			if len(task.Attempts) != 1 || task.Attempts[0].Executor != "bot-1" {
				t.Fatalf("claim won but attempts=%+v", task.Attempts)
			}
		case transfer.code == http.StatusOK && claim.code == http.StatusConflict && strings.Contains(claim.body, "QUEUE_RESERVED"):
			outcomes["transfer"]++
			if len(task.Attempts) != 0 || f.list(t, f.codex, "?task="+id)[id]["queue"].(map[string]any)["runner"] != "codex-1" {
				t.Fatal("transfer won but state differs")
			}
		default:
			t.Fatalf("ambiguous race: claim %d %s / transfer %d %s", claim.code, claim.body, transfer.code, transfer.body)
		}
	}
}

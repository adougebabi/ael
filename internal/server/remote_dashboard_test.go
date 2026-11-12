package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRemoteDashboardScopeCursorAndResponsibility(t *testing.T) {
	f := newArtifactFixture(t)
	get := func(path string) map[string]any {
		return standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/dashboard/"+path, nil, nil, "", f.reader, "", ""), http.StatusOK)
	}
	first := get("sync")
	if !strings.Contains(first["cursor"].(string), ":shared:") {
		t.Fatal(first)
	}
	unchanged := get("sync?cursor=" + first["cursor"].(string))
	if unchanged["changed"] != false {
		t.Fatal(unchanged)
	}
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	err = service.Store.Write(func(tx *store.Tx) error {
		_, _, err := service.RemoteClaimTx(tx, core.RemoteClaimInput{TaskID: "task", ExpectedRevision: f.revision, Runner: "external-client", Workspace: "declared-workspace", Candidate: "candidate", Token: "private-dashboard-lease"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	view := get("task?id=task")
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "private-dashboard-lease") || strings.Contains(string(raw), service.Root) {
		t.Fatalf("sensitive state leaked: %s", raw)
	}
	collaboration := view["collaboration"].([]any)
	item := collaboration[0].(map[string]any)
	if item["host_execution_state"] != "unknown" || item["workspace"] != "declared-workspace" {
		t.Fatal(item)
	}
	actual, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	if view["revision"] != core.RemoteRevision(actual) {
		t.Fatal("sanitizing the view changed the responsibility revision")
	}
	if _, err = service.Pause("task", "collaboration paused"); err != nil {
		t.Fatal(err)
	}
	paused := get("task?id=task")["collaboration"].([]any)[0].(map[string]any)
	if paused["responsibility_state"] != "paused" || paused["connection_state"] != "last_reported" || paused["host_execution_state"] != "unknown" {
		t.Fatalf("pause confused authority with host state: %+v", paused)
	}
	if get("sync")["cursor"] == first["cursor"] {
		t.Fatal("claim did not advance the synchronization cursor")
	}
	w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/other/dashboard/tasks", nil, nil, "", f.reader, "", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-project view accepted: %d %s", w.Code, w.Body.String())
	}
	w = standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/dashboard/tasks", map[string]any{}, nil, "", f.uploader, "", "1")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal("read projection accepted mutation")
	}
}

func TestRemoteDashboardReadsFinalizedCentralSource(t *testing.T) {
	f := newArtifactFixture(t)
	content := []byte("---\nid: rules\nkind: spec\nstatus: current\nsource: design/origin.md\n---\n# Central rules\nExact immutable bytes.\n")
	started := artifactStart(t, f, "rules.md", "text/plain", content, "source-upload")
	upload := started["upload_id"].(string)
	artifact := started["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": content}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "source-upload", "idempotency_key": "source-upload"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.portable("shared", service); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec("INSERT INTO portable_contexts VALUES(?,?,?,?,?)", "context-source", "task", "candidate", "{}", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec("INSERT INTO portable_context_sources VALUES(?,?,?,?,?,?,?,?)", "context-source", "rules", ".ael/knowledge/rules.md", artifact, store.Hash(content), "current", "spec", "task basis"); err != nil {
		t.Fatal(err)
	}
	w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/dashboard/knowledge-record?id=rules&version="+store.Hash(content), nil, nil, "", f.reader, "", "")
	record := standaloneJSON(t, w, http.StatusOK)
	if record["source"] != "design/origin.md" {
		t.Fatalf("original source metadata was lost: %+v", record)
	}
	if record["hash"] != store.Hash(content) || !strings.Contains(record["body"].(string), "Exact immutable bytes.") {
		t.Fatal(record)
	}
	w = standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/dashboard/knowledge-page?view=current", nil, nil, "", f.reader, "", "")
	page := standaloneJSON(t, w, http.StatusOK)
	if page["total"] != float64(1) {
		t.Fatal(page)
	}
	if strings.Contains(w.Body.String(), "Exact immutable bytes.") && strings.Contains(w.Body.String(), `"body"`) {
		t.Fatal("catalog returned full source bodies")
	}
}

func TestRemoteDashboardRelativeLinksKeepHistoricalContext(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.portable("shared", service); err != nil {
		t.Fatal(err)
	}
	upload := func(key string, content []byte) string {
		started := artifactStart(t, f, key+".md", "text/plain", content, key)
		id := started["upload_id"].(string)
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+id+"/chunks", map[string]any{"offset": 0, "data": content}, nil, "", f.uploader, "", "1"), http.StatusOK)
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+id+"/finalize", map[string]string{"operation_id": key, "idempotency_key": key}, nil, "", f.uploader, "", "1"), http.StatusOK)
		return started["artifact_id"].(string)
	}
	source := []byte("# Root\n[Historical target](target.md)\n")
	oldTarget := []byte("# Target\nOLD_TARGET_ONLY\n")
	newTarget := []byte("# Target\nNEW_TARGET_ONLY\n")
	sourceID := upload("root-source", source)
	oldID := upload("old-target", oldTarget)
	newID := upload("new-target", newTarget)
	for _, context := range []struct {
		id, target string
		bytes      []byte
	}{{"old-context", oldID, oldTarget}, {"new-context", newID, newTarget}} {
		if _, err = service.Store.DB.Exec("INSERT INTO portable_contexts VALUES(?,?,?,?,?)", context.id, "task", "candidate", "{}", "fixture"); err != nil {
			t.Fatal(err)
		}
		for _, item := range []struct {
			id, path, artifact string
			bytes              []byte
		}{{"root", "spec/root.md", sourceID, source}, {"target", "spec/target.md", context.target, context.bytes}} {
			if _, err = service.Store.DB.Exec("INSERT INTO portable_context_sources VALUES(?,?,?,?,?,?,?,?)", context.id, item.id, item.path, item.artifact, store.Hash(item.bytes), "current", "spec", "linked source"); err != nil {
				t.Fatal(err)
			}
		}
	}
	prefix := "/api/v1/projects/shared/dashboard/"
	w := standaloneRequest(t, f.h, "GET", prefix+"knowledge-link?id=root&version="+store.Hash(source)+"&context=old-context&href=target.md", nil, nil, "", f.reader, "", "")
	link := standaloneJSON(t, w, http.StatusOK)
	if link["version"] != store.Hash(oldTarget) || link["context"] != "old-context" {
		t.Fatalf("historical link drifted: %+v", link)
	}
	w = standaloneRequest(t, f.h, "GET", prefix+"knowledge-record?id=target&version="+link["version"].(string)+"&context=old-context", nil, nil, "", f.reader, "", "")
	record := standaloneJSON(t, w, http.StatusOK)
	if !strings.Contains(record["body"].(string), "OLD_TARGET_ONLY") || strings.Contains(record["body"].(string), "NEW_TARGET_ONLY") {
		t.Fatal(record)
	}
}

func TestRemoteDashboardDoesNotRepublishLegacyWorkspacePaths(t *testing.T) {
	task := core.Task{Attempts: []core.Attempt{{ID: "legacy", Executor: "legacy-client", Workspace: "/private/client/source", Token: "private-legacy-token", Status: "pass"}}}
	projected := dashboardResponsibilities(task, map[string]map[string]string{})
	raw, _ := json.Marshal(projected)
	if strings.Contains(string(raw), "/private/client/source") || strings.Contains(string(raw), "private-legacy-token") {
		t.Fatalf("legacy private execution data leaked: %s", raw)
	}
	if projected[0]["workspace"] != "" {
		t.Fatal(projected)
	}
}

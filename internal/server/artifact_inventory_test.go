package server

import (
	"ael/internal/store"
	"net/http"
	"strings"
	"testing"
)

func TestArtifactInventoryExplainsStoredAndReservedBytes(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("stored report\n")
	start := artifactStart(t, f, "report.txt", "text/plain", data, "stored")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+start["upload_id"].(string)+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+start["upload_id"].(string)+"/finalize", map[string]string{"operation_id": "stored", "idempotency_key": "stored"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	pending := artifactStart(t, f, "pending.txt", "text/plain", []byte("pending bytes"), "pending")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+pending["upload_id"].(string)+"/chunks", map[string]any{"offset": 0, "data": []byte("pen")}, nil, "", f.uploader, "", "1"), http.StatusOK)
	path := "/api/v1/projects/shared/artifacts/inventory"
	page := standaloneJSON(t, standaloneRequest(t, f.h, "GET", path+"?limit=1", nil, f.admin, "", "", "", ""), http.StatusOK)
	items := page["items"].([]any)
	if page["total"].(float64) != 1 || len(items) != 1 || items[0].(map[string]any)["filename"] != "report.txt" {
		t.Fatalf("stored file missing: %+v", page)
	}
	stats := page["stats"].(map[string]any)
	if stats["artifact_bytes"].(float64) != float64(len(data)) || stats["temporary_bytes"].(float64) != 3 || stats["reserved_bytes"].(float64) != float64(len("pending bytes")-3) {
		t.Fatalf("physical bytes differ: %+v", stats)
	}
	owned := standaloneJSON(t, standaloneRequest(t, f.h, "GET", path+"?kind=uploads", nil, nil, "", f.uploader, "", ""), http.StatusOK)
	upload := owned["items"].([]any)[0].(map[string]any)
	if upload["can_cancel"] != true || upload["operation_id"] != "pending" || upload["reserved_bytes"].(float64) == 0 {
		t.Fatalf("owner cannot manage upload: %+v", upload)
	}
	reader := standaloneJSON(t, standaloneRequest(t, f.h, "GET", path+"?kind=uploads", nil, nil, "", f.reader, "", ""), http.StatusOK)
	if reader["total"].(float64) != 0 {
		t.Fatalf("another owner upload disclosed: %+v", reader)
	}
	for _, suffix := range []string{"?limit=101", "?limit=bad", "?offset=-1", "?kind=unknown"} {
		if w := standaloneRequest(t, f.h, "GET", path+suffix, nil, nil, "", f.reader, "", ""); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid inventory query %s: %d", suffix, w.Code)
		}
	}
	filtered := standaloneJSON(t, standaloneRequest(t, f.h, "GET", path+"?q=no-match", nil, nil, "", f.reader, "", ""), http.StatusOK)
	if filtered["total"].(float64) != 0 {
		t.Fatalf("filename filter ignored: %+v", filtered)
	}
	end := standaloneJSON(t, standaloneRequest(t, f.h, "GET", path+"?offset=1", nil, nil, "", f.reader, "", ""), http.StatusOK)
	if end["total"].(float64) != 1 || len(end["items"].([]any)) != 0 {
		t.Fatalf("pagination count changed: %+v", end)
	}
	if w := standaloneRequest(t, f.h, "GET", path, nil, nil, "", f.other, "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("inventory crossed project scope: %d", w.Code)
	}
}

func TestArtifactQuotaCanBeDisabledAndEnabledWithCAS(t *testing.T) {
	f := newArtifactFixture(t)
	quota := map[string]any{"limit_bytes": 0, "expected_quota_bytes": artifactDefaultQuota, "operation_id": "unlimited", "idempotency_key": "unlimited"}
	url := "/api/v1/projects/shared/artifacts/quota"
	if w := standaloneRequest(t, f.h, "POST", url, quota, nil, "", f.uploader, "", "1"); w.Code != http.StatusForbidden {
		t.Fatalf("agent changed quota: %d", w.Code)
	}
	response := standaloneJSON(t, standaloneRequest(t, f.h, "POST", url, quota, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	if response["quota_bytes"].(float64) != 0 {
		t.Fatalf("unlimited rejected: %+v", response)
	}
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	// A tiny configured limit blocks new uploads, while zero permits them.
	if _, err := service.Store.DB.Exec("INSERT INTO artifact_settings(key,value) VALUES('quota',1) ON CONFLICT(key) DO UPDATE SET value=1"); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"operation_id": "too-big", "idempotency_key": "too-big", "expected_revision": f.revision, "task_id": "task", "filename": "report.txt", "mime": "text/plain", "size": 2, "sha256": store.Hash([]byte("ok")), "source": "fixture"}
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", input, nil, "", f.uploader, "", "1")
	body := standaloneJSON(t, w, http.StatusRequestEntityTooLarge)
	if body["stats"] == nil || !strings.Contains(body["detail"].(string), "Project settings") {
		t.Fatalf("quota error lacks settings direction: %+v", body)
	}
	if _, err := a.service.Store.DB.Exec("UPDATE artifact_settings SET value=0 WHERE key='quota'"); err != nil {
		t.Fatal(err)
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", input, nil, "", f.uploader, "", "1"), http.StatusCreated)
	enable := map[string]any{"limit_bytes": 8, "expected_quota_bytes": 0, "operation_id": "enable", "idempotency_key": "enable"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", url, enable, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	if w := standaloneRequest(t, f.h, "POST", url, map[string]any{"limit_bytes": 16, "expected_quota_bytes": 0, "operation_id": "stale", "idempotency_key": "stale"}, f.admin, f.csrf, "", "http://server.test", "1"); w.Code != http.StatusConflict {
		t.Fatalf("unlimited stale update accepted: %d", w.Code)
	}
}

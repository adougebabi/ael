package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMaterialFiltersBeforePagingAndLogicalSize(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("a readable report")
	contentFinish(t, f, artifactStart(t, f, "first.txt", "text/plain", data, "filter-one"), "filter-one", data)
	contentFinish(t, f, artifactStart(t, f, "second.txt", "text/plain", data, "filter-two"), "filter-two", data)
	get := func(query string) map[string]any {
		return standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/inventory?limit=1&"+query, nil, nil, "", f.reader, "", ""), http.StatusOK)
	}
	page := get("task=task&mime=text/&order=recent")
	if page["total"] != float64(2) || len(page["items"].([]any)) != 1 {
		t.Fatal(page)
	}
	item := page["items"].([]any)[0].(map[string]any)
	if item["size"] != float64(len(data)) {
		t.Fatal(item)
	}
	if get("since=2099-01-01")["total"] != float64(0) || get("mime=image/")["total"] != float64(0) || get("task=missing")["total"] != float64(0) {
		t.Fatal("filters do not apply to full range")
	}
	p := get("q=second.txt")
	if p["total"] != float64(1) {
		t.Fatal(p)
	}
	w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/other/artifacts/inventory?task=task", nil, nil, "", f.reader, "", "")
	if w.Code != http.StatusNotFound {
		t.Fatal("cross project inventory", w.Code)
	}
}

func TestReviewMaterialMetadataPreservesInputAndReferenceRelations(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("Fixture input specification")
	id := contentFinish(t, f, artifactStart(t, f, "AGENTS.md", "text/plain", data, "material-input"), "material-input", data)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.portable("shared", service); err != nil {
		t.Fatal(err)
	}
	_, err = service.Store.DB.Exec(`INSERT INTO portable_contexts(id,task_id,candidate_id,body,created) VALUES('material-context','task','candidate','{}','2026-11-20T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Store.DB.Exec(`INSERT INTO portable_context_sources(context_id,id,path,artifact_id,sha256,status,kind,reason) VALUES('material-context','spec','AGENTS.md',?,'fixture-hash','current','spec','input specification')`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Store.DB.Exec(`INSERT INTO artifact_references(artifact_id,task_id,purpose,candidate,actor,actor_name,operation_id,idempotency_key,request_hash,created) VALUES(?,'task','reference','candidate','fixture','fixture','material-ref','material-ref','fixture','2026-11-20T00:00:00Z')`, id)
	if err != nil {
		t.Fatal(err)
	}
	w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/dashboard/review-evidence?id=task", nil, nil, "", f.reader, "", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var values []map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || len(values[0]["references"].([]any)) != 1 || len(values[0]["context_sources"].([]any)) != 1 {
		t.Fatal(values)
	}
}

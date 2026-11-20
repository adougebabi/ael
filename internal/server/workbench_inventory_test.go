package server

import (
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

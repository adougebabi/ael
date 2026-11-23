package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type artifactFixture struct {
	s                       *Standalone
	h                       http.Handler
	admin                   []*http.Cookie
	csrf                    string
	uploader, reader, other string
	revision                string
}

func newArtifactFixture(t *testing.T) artifactFixture {
	t.Helper()
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"shared", "other"} {
		if err = s.createProject(project, project); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ user, project, role string }{{"upload", "shared", "executor"}, {"reader", "shared", "reader"}, {"other", "other", "executor"}} {
		if err = s.createUser(item.user, "fixture-user-password"); err != nil {
			t.Fatal(err)
		}
		if err = s.addMember(item.project, item.user, item.role); err != nil {
			t.Fatal(err)
		}
	}
	_, uploader, err := s.issueToken("upload", "shared", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := s.issueToken("reader", "shared", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := s.issueToken("other", "other", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	admin, csrf := standaloneLogin(t, h, "owner", "fixture-admin-password")
	definition := core.Definition{ID: "task", Title: "Task", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}
	create := map[string]any{"operation_id": "task-op", "idempotency_key": "task-key", "expected_revision": "absent", "definition": definition}
	receipt := standaloneJSON(t, standaloneRequest(t, h, "POST", "/api/v1/projects/shared/tasks", create, nil, "", uploader, "", "1"), http.StatusCreated)
	return artifactFixture{s: s, h: h, admin: admin, csrf: csrf, uploader: uploader, reader: reader, other: other, revision: receipt["revision"].(string)}
}

func fixturePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 200, G: 80, B: 50, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func artifactStart(t *testing.T, f artifactFixture, name, mime string, content []byte, key string) map[string]any {
	t.Helper()
	body := map[string]any{"operation_id": key, "idempotency_key": key, "expected_revision": f.revision, "task_id": "task", "filename": name, "mime": mime, "size": len(content), "sha256": store.Hash(content), "source": "fixture"}
	return standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", body, nil, "", f.uploader, "", "1"), http.StatusCreated)
}

func TestArtifactUploadFinalizeDownloadAndAuthorization(t *testing.T) {
	f := newArtifactFixture(t)
	imageData, reportData := fixturePNG(t), []byte("Synthetic report\nAll fixture checks recorded.\n")
	for _, item := range []struct {
		name, mime, key string
		data            []byte
	}{{"screen.png", "image/png", "image-op", imageData}, {"report.txt", "text/plain", "report-op", reportData}} {
		start := artifactStart(t, f, item.name, item.mime, item.data, item.key)
		uploadID, artifactID := start["upload_id"].(string), start["artifact_id"].(string)
		if w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/"+artifactID, nil, nil, "", f.reader, "", ""); w.Code != http.StatusNotFound {
			t.Fatalf("unfinished upload became an artifact: %d %s", w.Code, w.Body.String())
		}
		finalize := map[string]string{"operation_id": item.key, "idempotency_key": item.key}
		if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", finalize, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
			t.Fatalf("incomplete upload finalized: %d %s", w.Code, w.Body.String())
		}
		chunk := map[string]any{"offset": 0, "data": item.data}
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", chunk, nil, "", f.uploader, "", "1"), http.StatusOK)
		// Replaying a chunk is harmless and does not append it twice.
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", chunk, nil, "", f.uploader, "", "1"), http.StatusOK)
		first := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", finalize, nil, "", f.uploader, "", "1")
		metadata := standaloneJSON(t, first, http.StatusOK)
		if metadata["id"] != artifactID || metadata["sha256"] != store.Hash(item.data) || metadata["status"] != "finalized" {
			t.Fatalf("incorrect artifact metadata: %+v", metadata)
		}
		again := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", finalize, nil, "", f.uploader, "", "1")
		if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
			t.Fatalf("finalize not idempotent: %d %s", again.Code, again.Body.String())
		}
		got := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/"+artifactID+"/content", nil, nil, "", f.reader, "", "")
		if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), item.data) {
			t.Fatalf("authorized download differed: %d", got.Code)
		}
		denied := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/"+artifactID+"/content", nil, nil, "", f.other, "", "")
		if denied.Code != http.StatusNotFound {
			t.Fatalf("cross-project download accepted: %d", denied.Code)
		}
		anonymous := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/"+artifactID, nil, nil, "", "", "", "")
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous metadata access: %d", anonymous.Code)
		}
	}
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := service.Store.DB.Query("PRAGMA table_info(artifact_objects)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if strings.EqualFold(typ, "BLOB") {
			t.Fatal("artifact data stored in database BLOB")
		}
	}
}

func TestDeliveryCorrectionArtifactPreservesCompletedTask(t *testing.T) {
	f, task, evidence := reviewFixture(t)
	review := reviewBody(task, evidence, "pass")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", review, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	review["unit"], review["request_key"] = "independent", "other-check"
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/reviews", review, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)

	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.Get("task")
	if err != nil || completed.Status != "completed" || completed.Result != "verified" || len(completed.Evidence) != 2 {
		t.Fatalf("fixture did not complete: %v, status %q, result %q", err, completed.Status, completed.Result)
	}

	const route = "/api/v1/projects/shared/artifacts"
	report := []byte("Task: task\nFailed run: fixture-ci-1 (gofmt)\nCorrected commit: 1234567890123456789012345678901234567890\nLocal format: pass\nRemote CI: pending\n")
	start := map[string]any{
		"operation_id": "correction", "idempotency_key": "correction", "expected_revision": core.RemoteRevision(completed),
		"task_id": "task", "candidate": completed.Candidate, "filename": "correction.md", "mime": "text/plain",
		"size": len(report), "sha256": store.Hash(report), "source": "delivery-correction",
	}
	denied := standaloneRequest(t, f.h, "POST", route+"/uploads", start, nil, "", f.reader, "", "1")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only user could start a correction upload: %d", denied.Code)
	}
	created := standaloneJSON(t, standaloneRequest(t, f.h, "POST", route+"/uploads", start, nil, "", f.uploader, "", "1"), http.StatusCreated)
	uploadPath := route + "/uploads/" + created["upload_id"].(string)
	chunk := map[string]any{"offset": 0, "data": report}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", uploadPath+"/chunks", chunk, nil, "", f.uploader, "", "1"), http.StatusOK)
	keys := map[string]string{"operation_id": "correction", "idempotency_key": "correction"}
	finalized := standaloneRequest(t, f.h, "POST", uploadPath+"/finalize", keys, nil, "", f.uploader, "", "1")
	metadata := standaloneJSON(t, finalized, http.StatusOK)
	replay := standaloneRequest(t, f.h, "POST", uploadPath+"/finalize", keys, nil, "", f.uploader, "", "1")
	if replay.Code != http.StatusOK || replay.Body.String() != finalized.Body.String() {
		t.Fatalf("correction replay did not return the same receipt: %d %s", replay.Code, replay.Body.String())
	}
	content := standaloneRequest(t, f.h, "GET", route+"/"+metadata["id"].(string)+"/content", nil, nil, "", f.reader, "", "")
	if content.Code != http.StatusOK || !bytes.Equal(content.Body.Bytes(), report) {
		t.Fatalf("correction report not readable: %d", content.Code)
	}
	after, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(completed)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("delivery correction changed the original task or check history")
	}
}

func TestArtifactRejectsUnsafeInputAndRespectsQuota(t *testing.T) {
	f := newArtifactFixture(t)
	for _, name := range []string{"../escape.png", "/tmp/absolute.png", `folder\\file.png`, ".hidden.png"} {
		body := map[string]any{"operation_id": "bad-name", "idempotency_key": "bad-name", "expected_revision": f.revision, "task_id": "task", "filename": name, "mime": "image/png", "size": 1, "sha256": store.Hash([]byte("x")), "source": "fixture"}
		if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", body, nil, "", f.uploader, "", "1"); w.Code != http.StatusBadRequest {
			t.Fatalf("unsafe filename accepted %q: %d", name, w.Code)
		}
	}
	bad := []byte("this is not a PNG")
	start := artifactStart(t, f, "bad.png", "image/png", bad, "bad-mime")
	uploadID := start["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", map[string]any{"offset": 0, "data": bad}, nil, "", f.uploader, "", "1"), http.StatusOK)
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", map[string]string{"operation_id": "bad-mime", "idempotency_key": "bad-mime"}, nil, "", f.uploader, "", "1"); w.Code != http.StatusBadRequest {
		t.Fatalf("forged MIME finalized: %d %s", w.Code, w.Body.String())
	}
	quota := map[string]any{"limit_bytes": 32, "expected_quota_bytes": artifactDefaultQuota, "operation_id": "quota-op", "idempotency_key": "quota-key"}
	firstQuota := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/quota", quota, f.admin, f.csrf, "", "http://server.test", "1")
	standaloneJSON(t, firstQuota, http.StatusOK)
	againQuota := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/quota", quota, f.admin, f.csrf, "", "http://server.test", "1")
	if againQuota.Code != http.StatusOK || againQuota.Body.String() != firstQuota.Body.String() {
		t.Fatalf("quota replay differed: %d %s", againQuota.Code, againQuota.Body.String())
	}
	staleQuota := map[string]any{"limit_bytes": 40, "expected_quota_bytes": artifactDefaultQuota, "operation_id": "quota-stale", "idempotency_key": "quota-stale"}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/quota", staleQuota, f.admin, f.csrf, "", "http://server.test", "1"); w.Code != http.StatusConflict {
		t.Fatalf("stale quota update accepted: %d %s", w.Code, w.Body.String())
	}
	tooLarge := map[string]any{"operation_id": "too-large", "idempotency_key": "too-large", "expected_revision": f.revision, "task_id": "task", "filename": "large.txt", "mime": "text/plain", "size": 33, "sha256": store.Hash([]byte("x")), "source": "fixture"}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", tooLarge, nil, "", f.uploader, "", "1"); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("quota bypassed: %d %s", w.Code, w.Body.String())
	}
	stats := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/stats", nil, nil, "", f.reader, "", ""), http.StatusOK)
	if stats["temporary_bytes"].(float64) < float64(len(bad)) {
		t.Fatalf("temporary upload omitted from stats: %+v", stats)
	}
}

func TestArtifactCleanupProtectsActiveTaskAndRetainsMetadata(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("retained synthetic report\n")
	start := artifactStart(t, f, "report.txt", "text/plain", data, "cleanup-op")
	uploadID, artifactID := start["upload_id"].(string), start["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+uploadID+"/finalize", map[string]string{"operation_id": "cleanup-op", "idempotency_key": "cleanup-op"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	ref := map[string]any{"task_id": "task", "purpose": "reference", "expected_revision": f.revision, "operation_id": "ref-op", "idempotency_key": "ref-op"}
	firstRef := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/"+artifactID+"/references", ref, nil, "", f.uploader, "", "1")
	standaloneJSON(t, firstRef, http.StatusCreated)
	replayedRef := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/"+artifactID+"/references", ref, nil, "", f.uploader, "", "1")
	if replayedRef.Code != http.StatusCreated || replayedRef.Body.String() != firstRef.Body.String() {
		t.Fatalf("reference replay changed its receipt: %d %s", replayedRef.Code, replayedRef.Body.String())
	}
	before := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	prune := func(apply bool, digest string) map[string]any {
		body := map[string]any{"before": before, "apply": apply}
		if apply {
			body["digest"], body["operation_id"], body["idempotency_key"] = digest, "prune-op", "prune-key"
		}
		return standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/prune", body, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	}
	if got := prune(false, ""); got["count"].(float64) != 0 {
		t.Fatalf("active task artifact was selected for deletion: %+v", got)
	}
	cancel := map[string]any{"operation_id": "cancel-op", "idempotency_key": "cancel-op", "expected_revision": f.revision, "reason": "fixture completed"}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/cancel", cancel, nil, "", f.uploader, "", "1"), http.StatusOK)
	preview := prune(false, "")
	if preview["count"].(float64) != 1 {
		t.Fatalf("terminal artifact not shown in dry-run: %+v", preview)
	}
	if got := prune(true, preview["digest"].(string)); got["count"].(float64) != 1 {
		t.Fatalf("artifact was not purged: %+v", got)
	}
	metadata := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/"+artifactID, nil, nil, "", f.reader, "", ""), http.StatusOK)
	if metadata["status"] != "purged" || metadata["sha256"] != store.Hash(data) {
		t.Fatalf("purge lost metadata: %+v", metadata)
	}
	if w := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/"+artifactID+"/content", nil, nil, "", f.reader, "", ""); w.Code != http.StatusGone {
		t.Fatalf("purged content still previewable: %d", w.Code)
	}
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	var refs int
	if err = service.Store.DB.QueryRow("SELECT count(*) FROM artifact_references WHERE artifact_id=?", artifactID).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("purge lost reference: %d %v", refs, err)
	}
	if _, err = os.Stat(filepath.Join(service.Root, ".ael", "work-items")); err != nil {
		t.Fatal("fixture task projection missing", err)
	}
}

func TestArtifactCleanupDoesNotPartiallyDeleteOnCorruption(t *testing.T) {
	f := newArtifactFixture(t)
	var ids []string
	for _, key := range []string{"purge-one", "purge-two"} {
		data := []byte("report " + key + "\n")
		start := artifactStart(t, f, key+".txt", "text/plain", data, key)
		upload, id := start["upload_id"].(string), start["artifact_id"].(string)
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": key, "idempotency_key": key}, nil, "", f.uploader, "", "1"), http.StatusOK)
		ids = append(ids, id)
	}
	sort.Strings(ids)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.objectPath(ids[0])
	second, _ := a.objectPath(ids[1])
	if err := os.WriteFile(second, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/cancel", map[string]any{"operation_id": "cancel", "idempotency_key": "cancel", "expected_revision": f.revision, "reason": "fixture"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	before := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	preview := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/prune", map[string]any{"before": before}, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/prune", map[string]any{"before": before, "apply": true, "digest": preview["digest"], "operation_id": "prune", "idempotency_key": "prune"}, f.admin, f.csrf, "", "http://server.test", "1")
	if w.Code == http.StatusOK {
		t.Fatal("corrupted object was accepted")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("earlier object was deleted before validation completed: %v", err)
	}
}

func TestArtifactPruneCleansInterruptedFinalize(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("completed upload interrupted before transaction commit\n")
	start := artifactStart(t, f, "interrupted.txt", "text/plain", data, "interrupt")
	upload, id := start["upload_id"].(string), start["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	temp, _ := a.uploadPath(upload)
	dest, _ := a.objectPath(id)
	if err := os.Link(temp, dest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/cancel", map[string]any{"operation_id": "cancel", "idempotency_key": "cancel", "expected_revision": f.revision, "reason": "fixture"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	before := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	preview := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/prune", map[string]any{"before": before}, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/prune", map[string]any{"before": before, "apply": true, "digest": preview["digest"], "operation_id": "prune", "idempotency_key": "prune"}, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("interrupted finalize left object on disk: %v", err)
	}
}

func TestArtifactReferenceRejectsMissingOriginal(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("report\n")
	start := artifactStart(t, f, "missing.txt", "text/plain", data, "missing")
	upload, id := start["upload_id"].(string), start["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "missing", "idempotency_key": "missing"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := a.objectPath(id)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ref := map[string]any{"task_id": "task", "purpose": "reference", "expected_revision": f.revision, "operation_id": "ref", "idempotency_key": "ref"}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/"+id+"/references", ref, nil, "", f.uploader, "", "1"); w.Code == http.StatusCreated {
		t.Fatal("missing object accepted as a new reference")
	}
}

func TestArtifactEvidenceRequiresFinalizedMatchingCandidate(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	task.Candidate = "fixture-candidate"
	body, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec("UPDATE tasks SET body=? WHERE id='task'", string(body)); err != nil {
		t.Fatal(err)
	}
	revision := core.RemoteRevision(task)
	data := []byte("candidate evidence\n")
	startBody := map[string]any{"operation_id": "evidence-file", "idempotency_key": "evidence-file", "expected_revision": revision, "task_id": "task", "candidate": task.Candidate, "filename": "evidence.txt", "mime": "text/plain", "size": len(data), "sha256": store.Hash(data), "source": "fixture"}
	start := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", startBody, nil, "", f.uploader, "", "1"), http.StatusCreated)
	upload, id := start["upload_id"].(string), start["artifact_id"].(string)
	ref := map[string]any{"task_id": "task", "purpose": "evidence", "candidate": task.Candidate, "expected_revision": revision, "operation_id": "evidence-ref", "idempotency_key": "evidence-ref"}
	endpoint := "/api/v1/projects/shared/artifacts/" + id + "/references"
	if w := standaloneRequest(t, f.h, "POST", endpoint, ref, nil, "", f.uploader, "", "1"); w.Code != http.StatusNotFound {
		t.Fatalf("unfinished artifact accepted as evidence: %d", w.Code)
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "evidence-file", "idempotency_key": "evidence-file"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	wrong := map[string]any{"task_id": "task", "purpose": "evidence", "candidate": "wrong", "expected_revision": revision, "operation_id": "wrong", "idempotency_key": "wrong"}
	if w := standaloneRequest(t, f.h, "POST", endpoint, wrong, nil, "", f.uploader, "", "1"); w.Code != http.StatusConflict {
		t.Fatalf("mismatched candidate accepted: %d", w.Code)
	}
	first := standaloneRequest(t, f.h, "POST", endpoint, ref, nil, "", f.uploader, "", "1")
	standaloneJSON(t, first, http.StatusCreated)
	replay := standaloneRequest(t, f.h, "POST", endpoint, ref, nil, "", f.uploader, "", "1")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("evidence reference not idempotent: %d %s", replay.Code, replay.Body.String())
	}
}

func TestArtifactOwnerCancelsInterruptedUploadOnActiveTask(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("interrupted report content\n")
	start := artifactStart(t, f, "partial.txt", "text/plain", data, "partial")
	upload := start["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data[:5]}, nil, "", f.uploader, "", "1"), http.StatusOK)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec("UPDATE artifact_uploads SET status='purging' WHERE id=?", upload); err != nil {
		t.Fatal(err)
	}
	pending := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/stats", nil, nil, "", f.reader, "", ""), http.StatusOK)
	if pending["reserved_bytes"].(float64) != float64(len(data)-5) {
		t.Fatalf("pending cleanup lost its quota reservation: %+v", pending)
	}
	endpoint := "/api/v1/projects/shared/artifacts/uploads/" + upload + "/cancel"
	body := map[string]string{"operation_id": "partial", "idempotency_key": "partial"}
	if w := standaloneRequest(t, f.h, "POST", endpoint, body, nil, "", f.reader, "", "1"); w.Code != http.StatusForbidden {
		t.Fatalf("reader cancelled another upload: %d", w.Code)
	}
	first := standaloneJSON(t, standaloneRequest(t, f.h, "POST", endpoint, body, nil, "", f.uploader, "", "1"), http.StatusOK)
	second := standaloneJSON(t, standaloneRequest(t, f.h, "POST", endpoint, body, nil, "", f.uploader, "", "1"), http.StatusOK)
	if first["status"] != "purged" || second["status"] != "purged" {
		t.Fatalf("cancel did not replay: %+v %+v", first, second)
	}
	stats := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/artifacts/stats", nil, nil, "", f.reader, "", ""), http.StatusOK)
	if stats["used_bytes"].(float64) != 0 || stats["reserved_bytes"].(float64) != 0 {
		t.Fatalf("cancel did not release quota: %+v", stats)
	}
}

func TestArtifactInterruptedPurgeReplaysPersistedIntent(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("recoverable purge\n")
	start := artifactStart(t, f, "purge.txt", "text/plain", data, "purge-file")
	upload, id := start["upload_id"].(string), start["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "purge-file", "idempotency_key": "purge-file"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/cancel", map[string]any{"operation_id": "cancel", "idempotency_key": "cancel", "expected_revision": f.revision, "reason": "fixture"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	var ownerID string
	if err = f.s.store.DB.QueryRow("SELECT id FROM server_users WHERE username='owner'").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: ownerID, Username: "owner", Admin: true}
	input := artifactPruneInput{Before: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	preview, err := a.prune(principal, input)
	if err != nil || preview.Count != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	input.Apply, input.Digest, input.OperationID, input.IdempotencyKey = true, preview.Digest, "recover", "recover"
	raw, _ := json.Marshal(struct {
		Actor, Project string
		Input          artifactPruneInput
	}{ownerID, "shared", input})
	preview.Apply = true
	receipt, _ := json.Marshal(artifactPruneReceipt{Hash: store.Hash(raw), Result: preview, State: "pending"})
	key := "artifact-prune:" + store.Hash([]byte(ownerID+"\nrecover"))
	if err = service.Store.Write(func(tx *store.Tx) error {
		if _, err := tx.Exec("UPDATE artifact_objects SET status='purging' WHERE id=?", id); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", key, string(receipt))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	path, _ := a.objectPath(id)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = a.prune(principal, input); err != nil {
		t.Fatalf("interrupted purge failed to resume: %v", err)
	}
	if _, err = a.prune(principal, input); err != nil {
		t.Fatalf("purge replay failed: %v", err)
	}
	item, err := a.get(id)
	if err != nil || item.Status != "purged" {
		t.Fatalf("purge not recorded: %+v %v", item, err)
	}
}

func TestArtifactInterruptedPurgeRespectsNewActiveDependent(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("dependency evidence\n")
	start := artifactStart(t, f, "dependency.txt", "text/plain", data, "dependency-file")
	upload, id := start["upload_id"].(string), start["artifact_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "dependency-file", "idempotency_key": "dependency-file"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/cancel", map[string]any{"operation_id": "cancel", "idempotency_key": "cancel", "expected_revision": f.revision, "reason": "fixture"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	var ownerID string
	if err = f.s.store.DB.QueryRow("SELECT id FROM server_users WHERE username='owner'").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: ownerID, Username: "owner", Admin: true}
	input := artifactPruneInput{Before: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	preview, err := a.prune(principal, input)
	if err != nil || preview.Count != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	input.Apply, input.Digest, input.OperationID, input.IdempotencyKey = true, preview.Digest, "recover", "recover"
	raw, _ := json.Marshal(struct {
		Actor, Project string
		Input          artifactPruneInput
	}{ownerID, "shared", input})
	preview.Apply = true
	receipt, _ := json.Marshal(artifactPruneReceipt{Hash: store.Hash(raw), Result: preview, State: "pending"})
	key := "artifact-prune:" + store.Hash([]byte(ownerID+"\nrecover"))
	if err = service.Store.Write(func(tx *store.Tx) error {
		if _, err := tx.Exec("UPDATE artifact_objects SET status='purging' WHERE id=?", id); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", key, string(receipt))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dependent := core.Definition{ID: "dependent", Title: "Dependent", Type: "bug", Goal: "verified", Acceptance: []string{"result"}, Dependencies: []string{"task"}}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks", map[string]any{"operation_id": "dependent", "idempotency_key": "dependent", "expected_revision": "absent", "definition": dependent}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	if _, err = a.prune(principal, input); err == nil || err.Error() != "PRUNE_STALE" {
		t.Fatalf("new active dependent did not stop purge: %v", err)
	}
	item, err := a.get(id)
	if err != nil || item.Status != "finalized" {
		t.Fatalf("protected original not restored: %+v %v", item, err)
	}
	if got, err := a.read(item); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("protected original unavailable: %v", err)
	}
}

func TestArtifactProtectsCompletedChildOfActiveParent(t *testing.T) {
	f := newArtifactFixture(t)
	parent := core.Definition{ID: "parent", Title: "Parent", Type: "epic", Goal: "verified", Acceptance: []string{"integrated"}}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks", map[string]any{"operation_id": "parent", "idempotency_key": "parent", "expected_revision": "absent", "definition": parent}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	linked := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/link", map[string]any{"operation_id": "link", "idempotency_key": "link", "expected_revision": f.revision, "parent": "parent", "required": true}, nil, "", f.uploader, "", "1"), http.StatusOK)
	f.revision = linked["revision"].(string)
	data := []byte("child report\n")
	start := artifactStart(t, f, "child.txt", "text/plain", data, "child-report")
	upload := start["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "child-report", "idempotency_key": "child-report"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	partial := artifactStart(t, f, "child-partial.txt", "text/plain", []byte("unfinished child evidence\n"), "child-partial")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+partial["upload_id"].(string)+"/chunks", map[string]any{"offset": 0, "data": []byte("unfinished")}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/task/cancel", map[string]any{"operation_id": "cancel-child", "idempotency_key": "cancel-child", "expected_revision": f.revision, "reason": "fixture"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	before := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	preview := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/prune", map[string]any{"before": before}, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	if preview["count"].(float64) != 0 {
		t.Fatalf("active parent did not protect child artifact: %+v", preview)
	}
}

func TestArtifactBeginRecoversMissingStageAndLinkedDestination(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("restartable upload\n")
	start := artifactStart(t, f, "restart.txt", "text/plain", data, "restart")
	upload, id := start["upload_id"].(string), start["artifact_id"].(string)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	temp, _ := a.uploadPath(upload)
	dest, _ := a.objectPath(id)
	if err = os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	again := artifactStart(t, f, "restart.txt", "text/plain", data, "restart")
	if again["upload_id"] != upload {
		t.Fatal("begin retry changed upload")
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	if err = os.Link(temp, dest); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	again = artifactStart(t, f, "restart.txt", "text/plain", data, "restart")
	if again["upload_id"] != upload {
		t.Fatal("begin retry changed upload")
	}
	if _, err = os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("begin recreated a stage after object link: %v", err)
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/finalize", map[string]string{"operation_id": "restart", "idempotency_key": "restart"}, nil, "", f.uploader, "", "1"), http.StatusOK)
}

func TestArtifactRejectsSymlinkedUpload(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("safe content\n")
	start := artifactStart(t, f, "safe.txt", "text/plain", data, "symlink")
	upload := start["upload_id"].(string)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	temp, _ := a.uploadPath(upload)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err = os.WriteFile(outside, []byte("do not modify"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, temp); err != nil {
		t.Fatal(err)
	}
	w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+upload+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1")
	if w.Code == http.StatusOK {
		t.Fatal("symlinked stage accepted")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "do not modify" {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
}

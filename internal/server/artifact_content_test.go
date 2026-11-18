package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func contentFinish(t *testing.T, f artifactFixture, start map[string]any, key string, data []byte) string {
	t.Helper()
	id := start["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+id+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+id+"/finalize", map[string]any{"operation_id": key, "idempotency_key": key}, nil, "", f.uploader, "", "1"), http.StatusOK)
	return start["artifact_id"].(string)
}
func contentCancelTask(t *testing.T, f artifactFixture, id string) {
	t.Helper()
	service, _ := f.s.projectService("shared")
	task, err := service.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/"+id+"/cancel", map[string]any{"operation_id": "cancel-" + id, "idempotency_key": "cancel-" + id, "expected_revision": core.RemoteRevision(task), "reason": "isolated retention test"}, nil, "", f.uploader, "", "1"), http.StatusOK)
}
func TestContentReuseKeepsLogicalIdentityQuotaAndLastReference(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("unchanged current specification\n")
	first := contentFinish(t, f, artifactStart(t, f, "spec.md", "text/plain", data, "first"), "first", data)
	service, _ := f.s.projectService("shared")
	a, _ := f.s.artifacts("shared", service)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/quota", map[string]any{"limit_bytes": len(data), "expected_quota_bytes": artifactDefaultQuota, "operation_id": "quota", "idempotency_key": "quota"}, f.admin, f.csrf, "", "http://server.test", "1"), http.StatusOK)
	created := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks", map[string]any{"operation_id": "second-task", "idempotency_key": "second-task", "expected_revision": "absent", "definition": core.Definition{ID: "second", Title: "Second task", Type: "bug", Goal: "verified", Acceptance: []string{"result"}}}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	secondStart := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", map[string]any{"task_id": "second", "expected_revision": created["revision"], "filename": "other-spec.md", "mime": "text/plain", "size": len(data), "sha256": store.Hash(data), "source": "context snapshot", "operation_id": "second", "idempotency_key": "second"}, nil, "", f.uploader, "", "1"), http.StatusCreated)
	if int(secondStart["received"].(float64)) != len(data) {
		t.Fatal("duplicate did not reuse bytes", secondStart)
	}
	second := contentFinish(t, f, secondStart, "second", data)
	if first == second {
		t.Fatal("logical evidence identities merged")
	}
	stats, _ := a.stats()
	if stats.Used != int64(len(data)) || stats.Reserved != 0 || stats.Temporary != 0 {
		t.Fatal("duplicate occupied full content", stats)
	}
	inventory, err := a.inventory(standalonePrincipal{Admin: true}, true, "files", "", 100, 0)
	if err != nil || inventory.Total != 2 || inventory.SharedSavings != int64(len(data)) || inventory.DuplicatePhysical != 0 || inventory.UntrackedBytes != 0 {
		t.Fatal(inventory, err)
	}
	var sum int64
	for _, task := range inventory.Tasks {
		sum += task.Bytes
	}
	if sum != stats.Used {
		t.Fatal("cross-task double counting", sum, stats)
	}
	pending := artifactStart(t, f, "pending.md", "text/plain", data, "cancel-shared")
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+pending["upload_id"].(string)+"/cancel", map[string]any{"operation_id": "cancel-shared", "idempotency_key": "cancel-shared"}, nil, "", f.uploader, "", "1"), http.StatusOK)
	contentCancelTask(t, f, "task")
	if _, err = service.Store.DB.Exec("UPDATE artifact_objects SET created='2000-01-01T00:00:00Z' WHERE id=?", first); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: "owner", Username: "owner", Admin: true}
	preview, err := a.prune(principal, artifactPruneInput{Before: "2001-01-01T00:00:00Z"})
	if err != nil || preview.Count != 1 || preview.Bytes != 0 {
		t.Fatal("shared holder not protected", preview, err)
	}
	if _, err = a.prune(principal, artifactPruneInput{Before: "2001-01-01T00:00:00Z", Apply: true, Digest: preview.Digest, OperationID: "prune-first", IdempotencyKey: "prune-first"}); err != nil {
		t.Fatal(err)
	}
	object, _ := a.get(second)
	if bytes, err := a.read(object); err != nil || string(bytes) != string(data) || object.TaskID != "second" {
		t.Fatal("purging one broke another identity", object, err)
	}
	contentCancelTask(t, f, "second")
	if _, err = service.Store.DB.Exec("UPDATE artifact_objects SET created='2000-01-01T00:00:00Z' WHERE id=?", second); err != nil {
		t.Fatal(err)
	}
	preview, err = a.prune(principal, artifactPruneInput{Before: "2001-01-01T00:00:00Z"})
	if err != nil || preview.Bytes != int64(len(data)) {
		t.Fatal(preview, err)
	}
	if _, err = a.prune(principal, artifactPruneInput{Before: "2001-01-01T00:00:00Z", Apply: true, Digest: preview.Digest, OperationID: "prune-last", IdempotencyKey: "prune-last"}); err != nil {
		t.Fatal(err)
	}
	stats, _ = a.stats()
	if stats.Used != 0 || stats.Reserved != 0 {
		t.Fatal("last holder not reclaimed", stats)
	}
	t.Logf("two independent logical identities: %d B once; first purge 0 B, final purge %d B", len(data), len(data))
}
func TestOverlappingUploadsConvergeToOnePhysicalContent(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("same report bytes\n")
	left := artifactStart(t, f, "left.txt", "text/plain", data, "left")
	right := artifactStart(t, f, "right.txt", "text/plain", data, "right")
	first := contentFinish(t, f, left, "left", data)
	second := contentFinish(t, f, right, "right", data)
	service, _ := f.s.projectService("shared")
	a, _ := f.s.artifacts("shared", service)
	stats, err := a.stats()
	if err != nil || stats.Used != int64(len(data)) || stats.Reserved != 0 || stats.Temporary != 0 {
		t.Fatal(stats, err)
	}
	for _, id := range []string{first, second} {
		object, _ := a.get(id)
		if _, err = a.read(object); err != nil {
			t.Fatal(err)
		}
	}
	contentCancelTask(t, f, "task")
	preview, err := a.prune(standalonePrincipal{Admin: true}, artifactPruneInput{Before: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	if err != nil || preview.Bytes != int64(len(data)) || preview.Count != 2 {
		t.Fatal("batch physical count", preview, err)
	}
}
func TestContentReuseRecoversOwnLinkedFileBeforeDatabaseCommit(t *testing.T) {
	for _, paired := range []bool{false, true} {
		t.Run(fmt.Sprint("paired-", paired), func(t *testing.T) {
			f := newArtifactFixture(t)
			data := []byte("same bytes after interrupted finalize\n")
			left := artifactStart(t, f, "left.txt", "text/plain", data, "left-crash")
			right := artifactStart(t, f, "right.txt", "text/plain", data, "right-crash")
			id := left["upload_id"].(string)
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+id+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), http.StatusOK)
			service, _ := f.s.projectService("shared")
			a, _ := f.s.artifacts("shared", service)
			temp, _ := a.uploadPath(id)
			own, _ := a.objectPath(left["artifact_id"].(string))
			if err := os.Link(temp, own); err != nil {
				t.Fatal(err)
			}
			if !paired {
				if err := os.Remove(temp); err != nil {
					t.Fatal(err)
				}
			}
			contentFinish(t, f, right, "right-crash", data)
			standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads/"+id+"/finalize", map[string]any{"operation_id": "left-crash", "idempotency_key": "left-crash"}, nil, "", f.uploader, "", "1"), http.StatusOK)
			if _, err := os.Lstat(own); !os.IsNotExist(err) {
				t.Fatal("interrupted target leaked", err)
			}
			stats, err := a.stats()
			if err != nil || stats.Used != int64(len(data)) || stats.Reserved != 0 || stats.Temporary != 0 {
				t.Fatal(stats, err)
			}
			object, _ := a.get(left["artifact_id"].(string))
			if _, err = a.read(object); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestReviewEvidenceIncludesPendingUploadWithContentMetadata(t *testing.T) {
	f := newArtifactFixture(t)
	data := []byte("a pending observation\n")
	artifactStart(t, f, "pending.txt", "text/plain", data, "pending-review")
	value := standaloneRequest(t, f.h, "GET", "/api/v1/projects/shared/dashboard/review-evidence?id=task", nil, nil, "", f.reader, "", "")
	if value.Code != http.StatusOK || !strings.Contains(value.Body.String(), "uploading") {
		t.Fatal(value.Code, value.Body.String())
	}
}

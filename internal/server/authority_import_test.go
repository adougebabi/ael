package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func authorityArchiveFixture(source string) core.AuthorityArchive {
	task := core.Task{Definition: core.Definition{ID: "same-name", Title: "Historical completed task", Type: "feature", Goal: "verified", Revision: 2, Acceptance: []string{"result"}}, Plan: core.Plan{Revision: 2, Approved: true}, Status: "completed", Step: "done", Candidate: strings.Repeat("a", 64), Evidence: []core.Evidence{{Check: "old-check", Result: "pass", Revision: 2, Candidate: strings.Repeat("a", 64)}}}
	identity, _ := json.Marshal([]string{"old-project", source, "same-name"})
	return core.AuthorityArchive{Schema: 1, Project: "old-project", Source: source, Version: "0.1.19", Tasks: []core.ArchivedTask{core.ArchiveTask(task, "import-"+store.Hash(identity)[:32])}, Files: []core.ArchivedFile{{TaskID: "same-name", Ref: "runtime:missing.png", Status: "missing", Kind: "evidence"}}}
}

func TestAuthorityImportStagesTwoSourcesWithoutRevivingAcceptance(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"mac-old", "cloud-old"} {
		archive := authorityArchiveFixture(source)
		input := authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "import-" + source, IdempotencyKey: "import-" + source}
		path := "/api/v1/projects/shared/imports"
		denied := standaloneRequest(t, f.h, http.MethodPost, path, input, nil, "", f.reader, "", "1")
		if denied.Code != http.StatusForbidden {
			t.Fatalf("reader imported authority %d", denied.Code)
		}
		result := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
		replay := standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
		if result["id"] != replay["id"] || result["state"] != "importing" {
			t.Fatal("import replay changed")
		}
		mapped := archive.Tasks[0].MappedID
		task, err := service.Get(mapped)
		if err != nil {
			t.Fatal(err)
		}
		if !task.Historical || task.Status != "completed" || task.Plan.Approved || len(task.Attempts) != 0 || len(task.Evidence) != 0 || task.Candidate != "" || task.History == nil || len(task.History.Checks) != 1 || !task.Created.IsZero() {
			t.Fatalf("old acceptance or timestamp revived %+v", task)
		}
		if _, err = service.Store.DB.Exec("UPDATE tasks SET updated='unauthorized' WHERE id=?", mapped); err == nil || !strings.Contains(err.Error(), "IMPORT_NOT_ACTIVE") {
			t.Fatalf("staged task writable %v", err)
		}
		var missing int
		if err = service.Store.DB.QueryRow("SELECT count(*) FROM authority_import_files WHERE task_id=? AND status='missing'", mapped).Scan(&missing); err != nil || missing != 1 {
			t.Fatal("missing picture lost")
		}
	}
	var imported int
	if err = service.Store.DB.QueryRow("SELECT count(*) FROM authority_import_tasks").Scan(&imported); err != nil || imported != 2 {
		t.Fatalf("source identities merged %d %v", imported, err)
	}
}

func TestAuthorityImportRejectsChangedSourceAndLiveCredentials(t *testing.T) {
	f := newArtifactFixture(t)
	archive := authorityArchiveFixture("mac-old")
	input := authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "first", IdempotencyKey: "first"}
	path := "/api/v1/projects/shared/imports"
	standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	archive.Tasks[0].Snapshot.Result = "conflicting later result"
	archive.Tasks[0] = core.ArchiveTask(archive.Tasks[0].Snapshot, archive.Tasks[0].MappedID)
	input = authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "changed", IdempotencyKey: "changed"}
	if response := standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"); response.Code == 200 {
		t.Fatal("source overwrite accepted")
	}
	archive = authorityArchiveFixture("cloud-old")
	archive.Tasks[0].Snapshot.Attempts = []core.Attempt{{ID: "attempt", Token: "private-old-lease"}}
	input = authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "credential", IdempotencyKey: "credential"}
	if response := standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"); response.Code == 200 {
		t.Fatal("live credentials accepted into history")
	}
	service, _ := f.s.projectService("shared")
	var count int
	if err := service.Store.DB.QueryRow("SELECT count(*) FROM authority_imports").Scan(&count); err != nil || count != 1 {
		t.Fatal("failed import left partial state")
	}
}

func TestAuthorityImportVerifiesBytesBeforeActivatingAndReplaysUploads(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.s.portable("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	if err = authorityImportSchema(service); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: "import-admin", Username: "owner", Admin: true}
	archive := authorityArchiveFixture("mac-old")
	data := fixturePNG(t)
	archive.Files = append(archive.Files, core.ArchivedFile{TaskID: "same-name", Ref: "runtime:actual.png", SHA256: store.Hash(data), Size: int64(len(data)), Status: "available", Kind: "evidence"})
	digest := importArchiveDigest(archive)
	input := authorityImportInput{Archive: archive, Digest: digest, OperationID: "stage", IdempotencyKey: "stage"}
	staged, err := p.stageAuthorityImport(principal, input)
	if err != nil {
		t.Fatal(err)
	}
	id := staged["id"].(string)
	artifacts, err := f.s.artifacts("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	control := authorityImportControl{Digest: digest, OperationID: "verify", IdempotencyKey: "verify"}
	if _, err = p.controlAuthorityImport(principal, id, "verify", control, artifacts); err == nil {
		t.Fatal("missing bytes verified")
	}
	mapped := archive.Tasks[0].MappedID
	task, err := service.Get(mapped)
	if err != nil {
		t.Fatal(err)
	}
	start := artifactStartInput{ImportID: id, OperationID: "upload", IdempotencyKey: "upload", ExpectedRevision: core.RemoteRevision(task), TaskID: mapped, Filename: "actual.png", MIME: "image/png", Size: int64(len(data)), SHA256: store.Hash(data), Source: "authority-import:" + id}
	wrong := start
	wrong.SHA256 = strings.Repeat("b", 64)
	if _, err = artifacts.begin(principal, wrong); err == nil {
		t.Fatal("undeclared bytes accepted")
	}
	upload, err := artifacts.begin(principal, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = artifacts.chunk(principal, upload.UploadID, 0, data, artifactLeaseInput{}); err != nil {
		t.Fatal(err)
	}
	object, err := artifacts.finalize(principal, upload.UploadID, "upload", "upload", artifactLeaseInput{})
	if err != nil {
		t.Fatal(err)
	}
	control.Bindings = []authorityImportBinding{{TaskID: mapped, Ref: "runtime:actual.png", ArtifactID: object.ID}}
	verified, err := p.controlAuthorityImport(principal, id, "verify", control, artifacts)
	if err != nil || verified["state"] != "verified" {
		t.Fatalf("verification failed %+v %v", verified, err)
	}
	preview, err := artifacts.prune(principal, artifactPruneInput{Before: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range preview.Items {
		if item.ID == object.ID {
			t.Fatal("verified import evidence was offered for pruning")
		}
	}
	commit := authorityImportControl{Digest: digest, OperationID: "commit", IdempotencyKey: "commit"}
	active, err := p.controlAuthorityImport(principal, id, "commit", commit, artifacts)
	if err != nil || active["state"] != "active" {
		t.Fatalf("commit failed %+v %v", active, err)
	}
	if _, err = artifacts.begin(principal, start); err != nil {
		t.Fatalf("finalized original upload no longer replayed %v", err)
	}
	start.OperationID, start.IdempotencyKey = "late", "late"
	if _, err = artifacts.begin(principal, start); err == nil {
		t.Fatal("new import upload accepted after activation")
	}
	var count int
	if err = service.Store.DB.QueryRow("SELECT count(*) FROM artifact_objects WHERE task_id=?", mapped).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated original")
	}
}

func TestAuthorityImportPreservesCompletedEpicAndOptionalLeaf(t *testing.T) {
	f := newArtifactFixture(t)
	service, _ := f.s.projectService("shared")
	p, err := f.s.portable("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	if err = authorityImportSchema(service); err != nil {
		t.Fatal(err)
	}
	archive := authorityArchiveFixture("tree")
	parent := core.Task{Definition: core.Definition{ID: "parent", Title: "Archived epic", Type: "epic", Goal: "verified", Revision: 1, Acceptance: []string{"integrated"}}, Status: "completed", Step: "done", Plan: core.Plan{Revision: 1}}
	identity, _ := json.Marshal([]string{archive.Project, archive.Source, "parent"})
	parentID := "import-" + store.Hash(identity)[:32]
	archive.Tasks[0].Snapshot.Definition.Parent = "parent"
	archive.Tasks[0].Snapshot.EpicMembership = &core.EpicMembership{Parent: "parent", Required: false, Milestone: "optional milestone"}
	archive.Tasks[0] = core.ArchiveTask(archive.Tasks[0].Snapshot, archive.Tasks[0].MappedID)
	archive.Tasks = append(archive.Tasks, core.ArchiveTask(parent, parentID))
	_, err = p.stageAuthorityImport(standalonePrincipal{UserID: "admin", Username: "owner", Admin: true}, authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "tree", IdempotencyKey: "tree"})
	if err != nil {
		t.Fatalf("archived tree failed to import %v", err)
	}
	leaf, err := service.Get(archive.Tasks[0].MappedID)
	if err != nil || leaf.EpicMembership == nil || leaf.EpicMembership.Required || leaf.EpicMembership.Parent != parentID {
		t.Fatalf("membership changed %+v %v", leaf.EpicMembership, err)
	}
}

func TestAuthorityImportAcceptsEmptyLocalProject(t *testing.T) {
	f := newArtifactFixture(t)
	service, _ := f.s.projectService("shared")
	p, err := f.s.portable("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	if err = authorityImportSchema(service); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: "operator", Username: "operator"}
	archive := core.AuthorityArchive{Schema: 1, Project: "empty-project", Source: "empty-instance", Version: "0.1.20", Tasks: []core.ArchivedTask{}, Files: []core.ArchivedFile{}}
	digest := importArchiveDigest(archive)
	result, err := p.stageAuthorityImport(principal, authorityImportInput{Archive: archive, Digest: digest, OperationID: "empty", IdempotencyKey: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, _ := f.s.artifacts("shared", service)
	id := result["id"].(string)
	for _, action := range []string{"verify", "commit"} {
		if _, err = p.controlAuthorityImport(principal, id, action, authorityImportControl{Digest: digest, OperationID: action, IdempotencyKey: action}, artifacts); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuthorityImportOwnerAndNewFactsProtectRollback(t *testing.T) {
	f := newArtifactFixture(t)
	service, _ := f.s.projectService("shared")
	p, err := f.s.portable("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	if err = authorityImportSchema(service); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: "operator", Username: "operator"}
	archive := authorityArchiveFixture("rollback")
	digest := importArchiveDigest(archive)
	staged, err := p.stageAuthorityImport(principal, authorityImportInput{Archive: archive, Digest: digest, OperationID: "stage", IdempotencyKey: "stage"})
	if err != nil {
		t.Fatal(err)
	}
	id := staged["id"].(string)
	artifacts, _ := f.s.artifacts("shared", service)
	verify := authorityImportControl{Digest: digest, OperationID: "verify", IdempotencyKey: "verify"}
	if _, err = p.controlAuthorityImport(standalonePrincipal{UserID: "other-operator"}, id, "verify", verify, artifacts); err == nil {
		t.Fatal("another operator controlled the import")
	}
	if _, err = p.controlAuthorityImport(principal, id, "verify", verify, artifacts); err != nil {
		t.Fatal(err)
	}
	if _, err = p.controlAuthorityImport(principal, id, "commit", authorityImportControl{Digest: digest, OperationID: "commit", IdempotencyKey: "commit"}, artifacts); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Store.DB.Exec("INSERT INTO events(task_id,kind,body,created) VALUES(?,'new-fact','{}','')", archive.Tasks[0].MappedID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.controlAuthorityImport(principal, id, "rollback-freeze", authorityImportControl{Digest: digest, OperationID: "rollback", IdempotencyKey: "rollback", AckDrained: true}, artifacts); err == nil || !strings.Contains(err.Error(), "IMPORT_NEW_FACTS") {
		t.Fatalf("new central facts were ignored %v", err)
	}
}

func TestAuthorityImportAcceptsLocalSourceIdentifierRange(t *testing.T) {
	for _, source := range []string{"mac.old", strings.Repeat("m", 96)} {
		f := newArtifactFixture(t)
		service, _ := f.s.projectService("shared")
		p, _ := f.s.portable("shared", service)
		if err := authorityImportSchema(service); err != nil {
			t.Fatal(err)
		}
		archive := authorityArchiveFixture(source)
		if _, err := p.stageAuthorityImport(standalonePrincipal{UserID: "operator"}, authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "stage", IdempotencyKey: "stage"}); err != nil {
			t.Fatalf("locally accepted source rejected %v", err)
		}
	}
}

func TestAuthorityImportMetadataResponsibilityIsFenced(t *testing.T) {
	for _, phase := range []string{"before-hashed-task", "before-hashed-parent", "after-plain", "after-hashed"} {
		t.Run(phase, func(t *testing.T) {
			f := newArtifactFixture(t)
			service, _ := f.s.projectService("shared")
			p, _ := f.s.portable("shared", service)
			if err := authorityImportSchema(service); err != nil {
				t.Fatal(err)
			}
			principal := standalonePrincipal{UserID: "operator"}
			archive := authorityArchiveFixture("metadata")
			digest := importArchiveDigest(archive)
			staged, err := p.stageAuthorityImport(principal, authorityImportInput{Archive: archive, Digest: digest, OperationID: "stage", IdempotencyKey: "stage"})
			if err != nil {
				t.Fatal(err)
			}
			id := staged["id"].(string)
			artifacts, _ := f.s.artifacts("shared", service)
			for _, action := range []string{"verify", "commit"} {
				if _, err = p.controlAuthorityImport(principal, id, action, authorityImportControl{Digest: digest, OperationID: action, IdempotencyKey: action}, artifacts); err != nil {
					t.Fatal(err)
				}
			}
			taskID := archive.Tasks[0].MappedID
			key := "remote-plan-task:" + store.Hash([]byte(taskID))
			if phase == "before-hashed-parent" {
				key = "remote-plan-parent:" + store.Hash([]byte(taskID))
			}
			freeze := authorityImportControl{Digest: digest, OperationID: "freeze", IdempotencyKey: "freeze", AckDrained: true}
			if strings.HasPrefix(phase, "before-") {
				if _, err = service.Store.DB.Exec("INSERT INTO metadata(key,body) VALUES(?,'new-plan-op')", key); err != nil {
					t.Fatal(err)
				}
				if _, err = p.controlAuthorityImport(principal, id, "rollback-freeze", freeze, artifacts); err == nil || !strings.Contains(err.Error(), "IMPORT_NEW_FACTS") {
					t.Fatalf("hashed task responsibility was ignored %v", err)
				}
				return
			}
			if _, err = p.controlAuthorityImport(principal, id, "rollback-freeze", freeze, artifacts); err != nil {
				t.Fatal(err)
			}
			if phase == "after-plain" {
				key = "remote-recovery-target:" + taskID
			}
			if _, err = service.Store.DB.Exec("INSERT INTO metadata(key,body) VALUES(?,'new-plan-op')", key); err == nil {
				t.Fatal("new metadata responsibility escaped frozen authority")
			}
		})
	}
}

func TestAuthorityImportAbortsAbsentAndUnactivatedBatches(t *testing.T) {
	for _, phase := range []string{"absent", "importing", "verified"} {
		t.Run(phase, func(t *testing.T) {
			f := newArtifactFixture(t)
			service, _ := f.s.projectService("shared")
			p, _ := f.s.portable("shared", service)
			if err := authorityImportSchema(service); err != nil {
				t.Fatal(err)
			}
			principal := standalonePrincipal{UserID: "operator"}
			archive := authorityArchiveFixture("abort")
			digest := importArchiveDigest(archive)
			id := "import-" + digest[:32]
			stage := authorityImportInput{Archive: archive, Digest: digest, OperationID: "stage", IdempotencyKey: "stage"}
			artifacts, _ := f.s.artifacts("shared", service)
			if phase != "absent" {
				if _, err := p.stageAuthorityImport(principal, stage); err != nil {
					t.Fatal(err)
				}
				if phase == "verified" {
					if _, err := p.controlAuthorityImport(principal, id, "verify", authorityImportControl{Digest: digest, OperationID: "verify", IdempotencyKey: "verify"}, artifacts); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := p.controlAuthorityImport(principal, id, "rollback-freeze", authorityImportControl{Digest: digest, OperationID: "freeze", IdempotencyKey: "freeze", AckDrained: true}, artifacts); err != nil {
				t.Fatal(err)
			}
			if _, err := p.controlAuthorityImport(principal, id, "rollback-complete", authorityImportControl{Digest: digest, OperationID: "complete", IdempotencyKey: "complete", AckDrained: true}, artifacts); err != nil {
				t.Fatal(err)
			}
			if phase == "absent" {
				if _, err := p.stageAuthorityImport(principal, stage); err == nil || !strings.Contains(err.Error(), "IMPORT_ABORTED") {
					t.Fatalf("late original stage revived an aborted authority %v", err)
				}
			} else {
				if _, err := service.Store.DB.Exec("UPDATE tasks SET updated='new' WHERE id=?", archive.Tasks[0].MappedID); err == nil {
					t.Fatal("retired central projection became writable")
				}
			}
		})
	}
}

func TestAuthorityImportFrozenUploadDoesNotWriteAndRetiredUploadCanCancel(t *testing.T) {
	f := newArtifactFixture(t)
	service, _ := f.s.projectService("shared")
	p, _ := f.s.portable("shared", service)
	if err := authorityImportSchema(service); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: "operator", Username: "operator"}
	archive := authorityArchiveFixture("partial")
	data := fixturePNG(t)
	archive.Files = append(archive.Files, core.ArchivedFile{TaskID: "same-name", Ref: "runtime:partial.png", Size: int64(len(data)), SHA256: store.Hash(data), Status: "available", Kind: "evidence"})
	digest := importArchiveDigest(archive)
	id := "import-" + digest[:32]
	if _, err := p.stageAuthorityImport(principal, authorityImportInput{Archive: archive, Digest: digest, OperationID: "stage", IdempotencyKey: "stage"}); err != nil {
		t.Fatal(err)
	}
	artifacts, _ := f.s.artifacts("shared", service)
	task, _ := service.Get(archive.Tasks[0].MappedID)
	upload, err := artifacts.begin(principal, artifactStartInput{ImportID: id, TaskID: task.Definition.ID, ExpectedRevision: core.RemoteRevision(task), OperationID: "upload", IdempotencyKey: "upload", Filename: "partial.png", MIME: "image/png", Size: int64(len(data)), SHA256: store.Hash(data), Source: "authority-import:" + id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = artifacts.chunk(principal, upload.UploadID, 0, data[:len(data)/2], artifactLeaseInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.controlAuthorityImport(principal, id, "rollback-freeze", authorityImportControl{Digest: digest, OperationID: "freeze", IdempotencyKey: "freeze", AckDrained: true}, artifacts); err != nil {
		t.Fatal(err)
	}
	path, _ := artifacts.uploadPath(upload.UploadID)
	before, _ := store.HashFile(path)
	if _, err = artifacts.chunk(principal, upload.UploadID, int64(len(data)/2), data[len(data)/2:], artifactLeaseInput{}); err == nil {
		t.Fatal("frozen upload accepted a chunk")
	}
	after, _ := store.HashFile(path)
	if before != after {
		t.Fatal("rejected frozen request changed staging bytes")
	}
	if _, err = p.controlAuthorityImport(principal, id, "rollback-complete", authorityImportControl{Digest: digest, OperationID: "complete", IdempotencyKey: "complete", AckDrained: true}, artifacts); err != nil {
		t.Fatal(err)
	}
	if _, err = artifacts.cancelUpload(principal, upload.UploadID, "upload", "upload"); err != nil {
		t.Fatalf("retired original upload could not be cancelled %v", err)
	}
	stats, err := artifacts.stats()
	if err != nil || stats.Reserved != 0 || stats.Temporary != 0 {
		t.Fatalf("unfinished upload retained reservation %+v %v", stats, err)
	}
}

func TestAuthorityImportArtifactFenceHasConflictResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	artifactError(recorder, errors.New("IMPORT_NOT_ACTIVE"))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "IMPORT_NOT_ACTIVE") {
		t.Fatalf("import authority rejection was hidden as storage failure %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthorityImportAllowsCompleteHistoryBeyondOrdinaryRequestLimit(t *testing.T) {
	f := newArtifactFixture(t)
	archive := authorityArchiveFixture("large-history")
	archive.Tasks[0].Snapshot.Definition.OriginalRequest = strings.Repeat("history ", (core.RemoteJSONRequestLimit/8)+1024)
	if err := core.ValidateArchiveTransferLimits(archive); err != nil {
		t.Fatal(err)
	}
	input := authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "large-history", IdempotencyKey: "large-history"}
	raw, _ := json.Marshal(input)
	if len(raw) <= core.RemoteJSONRequestLimit {
		t.Fatal("fixture did not exercise import limit")
	}
	path := "/api/v1/projects/shared/imports"
	standaloneJSON(t, standaloneRequest(t, f.h, http.MethodPost, path, input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	if jsonRequestLimit(httptest.NewRequest(http.MethodPost, "/api/v1/projects/shared/tasks", nil)) != core.RemoteJSONRequestLimit {
		t.Fatal("ordinary request limit changed")
	}
	archive.Tasks[0].Snapshot.Definition.OriginalRequest = strings.Repeat("x", core.AuthorityArchiveRequestLimit)
	if err := core.ValidateArchiveTransferLimits(archive); err == nil {
		t.Fatal("oversized archive accepted")
	}
}

func TestAuthorityImportPreservesLegacyParentAttribution(t *testing.T) {
	f := newArtifactFixture(t)
	service, _ := f.s.projectService("shared")
	p, err := f.s.portable("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	if err = authorityImportSchema(service); err != nil {
		t.Fatal(err)
	}
	archive := authorityArchiveFixture("legacy-tree")
	parent := core.Task{Definition: core.Definition{ID: "parent", Title: "Legacy feature parent", Type: "feature", Goal: "verified", Revision: 1, Acceptance: []string{"result"}}, Status: "completed", Step: "done", Plan: core.Plan{Revision: 1}}
	identity, _ := json.Marshal([]string{archive.Project, archive.Source, "parent"})
	parentID := "import-" + store.Hash(identity)[:32]
	archive.Tasks[0].Snapshot.Definition.Parent = "parent"
	archive.Tasks[0] = core.ArchiveTask(archive.Tasks[0].Snapshot, archive.Tasks[0].MappedID)
	archive.Tasks = append(archive.Tasks, core.ArchiveTask(parent, parentID))
	_, err = p.stageAuthorityImport(standalonePrincipal{UserID: "admin", Username: "owner", Admin: true}, authorityImportInput{Archive: archive, Digest: importArchiveDigest(archive), OperationID: "legacy-tree", IdempotencyKey: "legacy-tree"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.Get(archive.Tasks[0].MappedID)
	if err != nil || child.Definition.Parent != parentID || child.EpicMembership != nil {
		t.Fatalf("legacy attribution changed: %+v %v", child, err)
	}
	if child.Status != "completed" || !child.Historical {
		t.Fatal("historical result changed")
	}
}

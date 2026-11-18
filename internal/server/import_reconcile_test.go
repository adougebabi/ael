package server

import (
	"ael/internal/core"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func activateReconciliationFixture(t *testing.T, f artifactFixture, archive core.AuthorityArchive) {
	t.Helper()
	service, _ := f.s.projectService("shared")
	p, _ := f.s.portable("shared", service)
	if err := authorityImportSchema(service); err != nil {
		t.Fatal(err)
	}
	principal := standalonePrincipal{UserID: "operator", Username: "operator"}
	digest := importArchiveDigest(archive)
	staged, err := p.stageAuthorityImport(principal, authorityImportInput{Archive: archive, Digest: digest, OperationID: "stage", IdempotencyKey: "stage"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, _ := f.s.artifacts("shared", service)
	for _, action := range []string{"verify", "commit"} {
		if _, err = p.controlAuthorityImport(principal, staged["id"].(string), action, authorityImportControl{Digest: digest, OperationID: action, IdempotencyKey: action}, artifacts); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImportReconciliationProtectsOtherHolds(t *testing.T) {
	for _, mode := range []string{"unknown", "source-pause", "decision", "manual", "running", "context", "reader", "stale", "resolved"} {
		t.Run(mode, func(t *testing.T) {
			f := newArtifactFixture(t)
			service, _ := f.s.projectService("shared")
			archive := authorityArchiveFixture("protected")
			original := archive.Tasks[0].Snapshot
			original.Status, original.Step = "open", "execute"
			original.Paused = mode == "source-pause"
			if mode == "decision" {
				original.Confirmation = &core.Confirmation{Kind: "plan"}
			}
			archive.Tasks[0] = core.ArchiveTask(original, archive.Tasks[0].MappedID)
			if mode == "unknown" {
				archive.Tasks[0].Recovery = nil
			}
			activateReconciliationFixture(t, f, archive)
			id := archive.Tasks[0].MappedID
			task, _ := service.Get(id)
			if mode == "manual" {
				if _, err := service.Pause(id, "user requested pause"); err != nil {
					t.Fatal(err)
				}
				task, _ = service.Get(id)
			}
			if mode == "running" {
				task.Attempts = append(task.Attempts, core.Attempt{ID: "other", Status: "running"})
				if err := service.ImportTask(task); err != nil {
					t.Fatal(err)
				}
				task, _ = service.Get(id)
			}
			if mode == "resolved" {
				task.Paused = false
				task.Confirmation = nil
				task.Plan.Approved = true
				task.Step = "execute"
				if err := service.ImportTask(task); err != nil {
					t.Fatal(err)
				}
				task, _ = service.Get(id)
			}
			revision := core.RemoteRevision(task)
			ctx, _ := json.Marshal(map[string]any{"task_revision": revision, "actor": "upload", "inputs_known": mode != "context"})
			if _, err := service.Store.DB.Exec("INSERT INTO portable_contexts(id,task_id,candidate_id,body,created) VALUES('reconcile',?,'',?,'')", id, string(ctx)); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"context_id": "reconcile", "reason": "read history", "start": "continue implementation", "operation_id": "reconcile", "idempotency_key": "reconcile", "expected_revision": revision}
			token := f.uploader
			if mode == "reader" {
				token = f.reader
			}
			if mode == "stale" {
				input["expected_revision"] = "stale"
			}
			response := standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/tasks/"+id+"/reconcile-import", input, nil, "", token, "", "1")
			if response.Code == http.StatusOK {
				t.Fatal("protected hold released", response.Body.String())
			}
			if mode == "unknown" && !strings.Contains(response.Body.String(), "IMPORT_SOURCE_UNKNOWN") {
				t.Fatal(response.Body.String())
			}
			after, _ := service.Get(id)
			if core.RemoteRevision(after) != revision {
				t.Fatal("rejected reconciliation changed task")
			}
		})
	}
}

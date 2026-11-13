package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"database/sql"
	"errors"
	"net/http"
)

type remoteFailureReviewInput struct {
	Review           core.FailureReview `json:"review"`
	ExpectedRevision string             `json:"expected_revision"`
	OperationID      string             `json:"operation_id"`
	IdempotencyKey   string             `json:"idempotency_key"`
}

func (h *standaloneHandler) remoteFailureReview(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project, taskID string, service *core.Service) {
	p, err := h.server.portable(project, service)
	if err != nil {
		standaloneFail(w, 500, "SERVER_ERROR")
		return
	}
	var input remoteFailureReviewInput
	if decodePortable(r, &input, p) != nil || input.ExpectedRevision == "" || len(input.Review.Evidence) > 32 {
		standaloneFail(w, 400, "ROOT_CAUSE_REVIEW_REQUIRED")
		return
	}
	input.Review.Actor = principal.Username
	artifacts, err := h.server.artifacts(project, service)
	if err != nil {
		artifactError(w, err)
		return
	}
	result, err := p.mutate(principal, "failure-review-"+taskID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var total int64
		read := func(tx *store.Tx, task *core.Task, _ string, id string) ([]byte, error) {
			item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", id))
			if err != nil || item.TaskID != task.Definition.ID || item.Status != "finalized" {
				return nil, errors.New("ARTIFACT_REQUIRED")
			}
			total += item.Size
			if total > 64<<20 {
				return nil, errors.New("ARTIFACT_INVALID")
			}
			return artifacts.read(item)
		}
		task, err := service.ReviewRemoteFailureTx(tx, taskID, input.ExpectedRevision, input.Review, read)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": core.RemoteTaskView(task), "revision": core.RemoteRevision(task), "review_recorded": true}, nil
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = errors.New("TASK_NOT_FOUND")
		}
		portableError(w, err)
		return
	}
	if service.Store.Flush() != nil {
		standaloneFail(w, 503, "PROJECTION_BLOCKED")
		return
	}
	standaloneReply(w, 200, result)
}

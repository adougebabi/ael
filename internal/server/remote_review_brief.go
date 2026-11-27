package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

type remoteReviewBriefInput struct {
	Brief            core.ReviewBrief `json:"brief"`
	ExpectedRevision string           `json:"expected_revision"`
	OperationID      string           `json:"operation_id"`
	IdempotencyKey   string           `json:"idempotency_key"`
}

// remoteReviewBrief records the executor's explanation for a pending human
// check. Evidence IDs must be finalized attachments of this task bound to the
// current candidate, so the reviewer opens exactly what was described.
func (h *standaloneHandler) remoteReviewBrief(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project, taskID string, service *core.Service) {
	p, err := h.server.portable(project, service)
	if err != nil {
		standaloneFail(w, 500, "SERVER_ERROR")
		return
	}
	var input remoteReviewBriefInput
	if decodePortable(r, &input, p) != nil || input.ExpectedRevision == "" {
		standaloneFail(w, 400, "REVIEW_BRIEF_INVALID")
		return
	}
	result, err := p.mutate(principal, "review-brief-"+taskID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		evidenceOK := func(task core.Task, id string) bool {
			if !standaloneID.MatchString(id) {
				return false
			}
			item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", id))
			return err == nil && item.TaskID == task.Definition.ID && item.Status == "finalized" && item.Candidate == task.Candidate
		}
		task, err := service.RecordRemoteReviewBriefTx(tx, taskID, input.ExpectedRevision, input.Brief, principal.Username, evidenceOK)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": core.RemoteTaskView(task), "revision": core.RemoteRevision(task), "review_brief_recorded": true}, nil
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = errors.New("TASK_NOT_FOUND")
		}
		if code := errorCode(err); code == "REVIEW_BRIEF_INVALID" || code == "CHECK_METHOD_MISMATCH" || code == "CHECK_NOT_FOUND" || code == "TERMINAL_TASK" || code == "ARTIFACT_REQUIRED" || code == "REVISION_CONFLICT" {
			standaloneReply(w, reviewBriefStatus(code), map[string]string{"code": code, "error": code, "detail": errorDetail(err)})
			return
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

func errorCode(err error) string {
	code, _, _ := strings.Cut(err.Error(), ": ")
	return code
}

func errorDetail(err error) string {
	_, detail, _ := strings.Cut(err.Error(), ": ")
	detail = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return ' '
		}
		return r
	}, detail)
	if len(detail) > 500 {
		cut := 497
		for cut > 0 && !utf8.RuneStart(detail[cut]) {
			cut--
		}
		detail = detail[:cut] + "..."
	}
	return detail
}

func reviewBriefStatus(code string) int {
	switch code {
	case "REVIEW_BRIEF_INVALID":
		return http.StatusBadRequest
	case "CHECK_NOT_FOUND":
		return http.StatusNotFound
	}
	return http.StatusConflict
}

package server

import (
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

func artifactError(w http.ResponseWriter, err error) {
	var quota *artifactQuotaError
	switch {
	case errors.As(err, &quota):
		standaloneReply(w, http.StatusRequestEntityTooLarge, map[string]any{
			"code": "QUOTA_EXCEEDED", "error": "QUOTA_EXCEEDED", "stats": quota.Stats, "requested_bytes": quota.Requested,
			"detail": fmt.Sprintf("project attachment quota exceeded: used=%d reserved=%d requested=%d limit=%d bytes; open Project settings > Storage and attachments to inspect usage or change the quota", quota.Stats.Used, quota.Stats.Reserved, quota.Requested, quota.Stats.Quota),
		})
	case strings.Contains(err.Error(), "IMPORT_NOT_ACTIVE"):
		standaloneFail(w, http.StatusConflict, "IMPORT_NOT_ACTIVE")
	case err.Error() == "RUNNER_ISOLATION_REQUIRED":
		standaloneFail(w, http.StatusServiceUnavailable, "RUNNER_ISOLATION_REQUIRED")
	case errors.Is(err, sql.ErrNoRows), err.Error() == "ARTIFACT_NOT_FOUND", err.Error() == "UPLOAD_NOT_FOUND", err.Error() == "TASK_NOT_FOUND":
		standaloneFail(w, http.StatusNotFound, "ARTIFACT_NOT_FOUND")
	case err.Error() == "ARTIFACT_PURGED", err.Error() == "ARTIFACT_UNAVAILABLE":
		standaloneFail(w, http.StatusGone, "ARTIFACT_UNAVAILABLE")
	case err.Error() == "QUOTA_EXCEEDED":
		standaloneFail(w, http.StatusRequestEntityTooLarge, "QUOTA_EXCEEDED")
	case err.Error() == "CONTEXT_SOURCE_MISMATCH", err.Error() == "UPLOAD_INCOMPLETE", err.Error() == "UPLOAD_CONFLICT", err.Error() == "IDEMPOTENCY_CONFLICT", err.Error() == "REVISION_CONFLICT", err.Error() == "PRUNE_STALE", err.Error() == "QUOTA_TOO_SMALL", err.Error() == "ARTIFACT_CANDIDATE_MISMATCH", err.Error() == "ARTIFACT_CONFLICT", strings.HasPrefix(err.Error(), "STALE_TOKEN"), strings.HasPrefix(err.Error(), "STALE_EXECUTION_UNIT"):
		standaloneFail(w, http.StatusConflict, err.Error())
	case err.Error() == "ARTIFACT_INVALID", err.Error() == "ARTIFACT_MIME_INVALID", err.Error() == "ARTIFACT_CONTENT_MISMATCH", err.Error() == "ATTEMPT_NOT_FOUND":
		standaloneFail(w, http.StatusBadRequest, err.Error())
	default:
		standaloneFail(w, http.StatusInternalServerError, "ARTIFACT_STORAGE_ERROR")
	}
}

func (a *artifactStore) list(taskID string) ([]artifactObject, error) {
	rows, err := a.service.Store.DB.Query("SELECT "+artifactColumns+" FROM artifact_objects WHERE task_id=? OR id IN (SELECT artifact_id FROM artifact_references WHERE task_id=?) ORDER BY created,id", taskID, taskID)
	if err != nil {
		return nil, err
	}
	items := []artifactObject{}
	for rows.Next() {
		item, err := scanArtifact(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range items {
		if items[i].Status == "finalized" {
			if _, err = a.read(items[i]); err != nil {
				items[i].Status = "missing"
			}
		}
	}
	return items, nil
}

func (h *standaloneHandler) remoteArtifacts(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, method, project string, parts []string) {
	role, err := h.server.role(principal, project)
	if err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	if r.Method != http.MethodGet && role != "admin" && role != "executor" && role != "reviewer" {
		standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	service, err := h.server.projectService(project)
	if err != nil {
		standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
		return
	}
	a, err := h.server.artifacts(project, service)
	if err != nil {
		artifactError(w, err)
		return
	}
	if len(parts) == 1 && parts[0] == "inventory" && r.Method == http.MethodGet {
		limit, limitErr := strconv.Atoi(r.URL.Query().Get("limit"))
		if r.URL.Query().Get("limit") == "" {
			limit, limitErr = 25, nil
		}
		offset, offsetErr := strconv.Atoi(r.URL.Query().Get("offset"))
		if r.URL.Query().Get("offset") == "" {
			offset, offsetErr = 0, nil
		}
		if limitErr != nil || offsetErr != nil {
			artifactError(w, errors.New("ARTIFACT_INVALID"))
			return
		}
		kind := r.URL.Query().Get("kind")
		if kind == "" {
			kind = "files"
		}
		result, err := a.inventory(principal, role == "admin" || role == "executor" || role == "reviewer", kind, r.URL.Query().Get("q"), limit, offset)
		if err != nil {
			artifactError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if len(parts) == 1 && parts[0] == "stats" && r.Method == http.MethodGet {
		stats, err := a.stats()
		if err != nil {
			artifactError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, stats)
		return
	}
	if len(parts) == 1 && parts[0] == "quota" && r.Method == http.MethodPost {
		if !principal.Admin || method != "session" {
			standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		var input artifactQuotaInput
		if decode(r, &input) != nil {
			standaloneFail(w, http.StatusBadRequest, "ARTIFACT_INVALID")
			return
		}
		stats, err := a.setQuota(principal, input)
		if err != nil {
			artifactError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, stats)
		return
	}
	if len(parts) == 1 && parts[0] == "prune" && r.Method == http.MethodPost {
		if !principal.Admin || method != "session" {
			standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		var input artifactPruneInput
		if decode(r, &input) != nil {
			standaloneFail(w, http.StatusBadRequest, "ARTIFACT_INVALID")
			return
		}
		result, err := a.prune(principal, input)
		if err != nil {
			artifactError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if len(parts) == 1 && parts[0] == "uploads" && r.Method == http.MethodPost {
		var input artifactStartInput
		if decode(r, &input) != nil {
			standaloneFail(w, http.StatusBadRequest, "ARTIFACT_INVALID")
			return
		}
		result, err := a.begin(principal, input)
		if err != nil {
			artifactError(w, err)
			return
		}
		standaloneReply(w, http.StatusCreated, result)
		return
	}
	if len(parts) == 3 && parts[0] == "uploads" && parts[1] == "by-key" && r.Method == http.MethodGet {
		if !standaloneID.MatchString(parts[2]) {
			artifactError(w, errors.New("UPLOAD_NOT_FOUND"))
			return
		}
		upload, err := scanUpload(service.Store.DB.QueryRow("SELECT "+uploadColumns+" FROM artifact_uploads WHERE owner_id=? AND idempotency_key=?", principal.UserID, parts[2]))
		if err != nil {
			artifactError(w, errors.New("UPLOAD_NOT_FOUND"))
			return
		}
		standaloneReply(w, http.StatusOK, artifactStartResult{UploadID: upload.ID, ArtifactID: upload.ArtifactID, Received: upload.Received, Status: upload.Status})
		return
	}
	if len(parts) == 3 && parts[0] == "uploads" && r.Method == http.MethodPost {
		switch parts[2] {
		case "chunks":
			var input struct {
				Offset     int64  `json:"offset"`
				Data       []byte `json:"data"`
				RunnerID   string `json:"runner_id,omitempty"`
				LeaseToken string `json:"lease_token,omitempty"`
				Fence      int    `json:"fence,omitempty"`
			}
			if decode(r, &input) != nil {
				standaloneFail(w, http.StatusBadRequest, "ARTIFACT_INVALID")
				return
			}
			result, err := a.chunk(principal, parts[1], input.Offset, input.Data, artifactLeaseInput{RunnerID: input.RunnerID, LeaseToken: input.LeaseToken, Fence: input.Fence})
			if err != nil {
				artifactError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		case "finalize":
			var input struct {
				OperationID    string `json:"operation_id"`
				IdempotencyKey string `json:"idempotency_key"`
				RunnerID       string `json:"runner_id,omitempty"`
				LeaseToken     string `json:"lease_token,omitempty"`
				Fence          int    `json:"fence,omitempty"`
			}
			if decode(r, &input) != nil {
				standaloneFail(w, http.StatusBadRequest, "ARTIFACT_INVALID")
				return
			}
			item, err := a.finalize(principal, parts[1], input.OperationID, input.IdempotencyKey, artifactLeaseInput{RunnerID: input.RunnerID, LeaseToken: input.LeaseToken, Fence: input.Fence})
			if err != nil {
				artifactError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, item)
			return
		case "cancel":
			var input struct {
				OperationID    string `json:"operation_id"`
				IdempotencyKey string `json:"idempotency_key"`
			}
			if decode(r, &input) != nil {
				standaloneFail(w, http.StatusBadRequest, "ARTIFACT_INVALID")
				return
			}
			result, err := a.cancelUpload(principal, parts[1], input.OperationID, input.IdempotencyKey)
			if err != nil {
				artifactError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
	}
	if len(parts) == 2 && parts[1] == "references" && r.Method == http.MethodPost {
		var input artifactReferenceInput
		if decode(r, &input) != nil {
			standaloneFail(w, http.StatusBadRequest, "ARTIFACT_INVALID")
			return
		}
		result, err := a.reference(principal, parts[0], input)
		if err != nil {
			artifactError(w, err)
			return
		}
		standaloneReply(w, http.StatusCreated, result)
		return
	}
	if len(parts) >= 1 && len(parts) <= 2 && r.Method == http.MethodGet {
		item, err := a.get(parts[0])
		if err != nil {
			artifactError(w, err)
			return
		}
		if len(parts) == 1 {
			if item.Status == "finalized" {
				if _, err = a.read(item); err != nil {
					item.Status = "missing"
				}
			}
			standaloneReply(w, http.StatusOK, item)
			return
		}
		if parts[1] == "content" {
			data, err := a.read(item)
			if err != nil {
				artifactError(w, err)
				return
			}
			contentType, disposition := item.MIME, "inline"
			if item.MIME == "text/plain" {
				contentType = "text/plain; charset=utf-8"
			}
			if item.MIME == "video/mp4" || item.MIME == "application/octet-stream" {
				contentType, disposition = "application/octet-stream", "attachment"
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Filename}))
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
	}
	standaloneFail(w, http.StatusNotFound, "NOT_FOUND")
}

func (h *standaloneHandler) remoteTaskArtifacts(w http.ResponseWriter, principal standalonePrincipal, project, taskID string) {
	if _, err := h.server.role(principal, project); err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	service, err := h.server.projectService(project)
	if err != nil {
		standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
		return
	}
	if _, err = service.Get(taskID); err != nil {
		standaloneFail(w, http.StatusNotFound, "TASK_NOT_FOUND")
		return
	}
	a, err := h.server.artifacts(project, service)
	if err != nil {
		artifactError(w, err)
		return
	}
	items, err := a.list(taskID)
	if err != nil {
		artifactError(w, err)
		return
	}
	standaloneReply(w, http.StatusOK, map[string]any{"items": items})
}

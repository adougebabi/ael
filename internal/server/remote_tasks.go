package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type remoteTaskMutation struct {
	OperationID      string          `json:"operation_id"`
	IdempotencyKey   string          `json:"idempotency_key"`
	ExpectedRevision string          `json:"expected_revision"`
	Definition       core.Definition `json:"definition"`
	Reason           string          `json:"reason"`
	Parent           string          `json:"parent"`
	Required         *bool           `json:"required"`
	Milestone        string          `json:"milestone"`
	ReturnTo         string          `json:"return_to"`
	AckStopped       bool            `json:"ack_stopped"`
}

type remoteNextView struct {
	core.Next
	StateRevision    string `json:"revision"`
	ProjectionStatus string `json:"projection_status"`
}

func remoteReceiptReady(w http.ResponseWriter, service *core.Service, status int, receipt core.RemoteOperationReceipt) {
	if err := service.Store.Flush(); err != nil {
		standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
		return
	}
	standaloneReply(w, status, receipt)
}

func (h *standaloneHandler) remoteOperation(w http.ResponseWriter, principal standalonePrincipal, project, key string) {
	if !standaloneID.MatchString(key) {
		standaloneFail(w, http.StatusNotFound, "OPERATION_NOT_FOUND")
		return
	}
	if _, err := h.server.role(principal, project); err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	service, err := h.server.projectService(project)
	if err != nil {
		standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
		return
	}
	receipt, found, err := service.RemoteReceiptByKey(key, principal.Username, principal.Admin)
	if err == nil && !found {
		pending, pendingFound, pendingErr := service.RemotePendingByKey(key, principal.Username, principal.Admin)
		if pendingErr == nil && pendingFound {
			if service.Store.Flush() != nil {
				standaloneReply(w, http.StatusOK, map[string]string{"operation_id": pending.OperationID, "status": "projection_blocked", "projection_status": "blocked"})
				return
			}
			standaloneReply(w, http.StatusOK, pending)
			return
		}
	}
	if err != nil || !found {
		standaloneFail(w, http.StatusNotFound, "OPERATION_NOT_FOUND")
		return
	}
	if service.Store.Flush() != nil {
		standaloneReply(w, http.StatusOK, map[string]string{"operation_id": receipt.OperationID, "status": "projection_blocked", "projection_status": "blocked"})
		return
	}
	standaloneReply(w, http.StatusOK, receipt)
}

func standaloneTaskError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "IMPORT_NOT_ACTIVE") {
		standaloneFail(w, http.StatusConflict, "IMPORT_NOT_ACTIVE")
		return
	}
	if err == sql.ErrNoRows || strings.HasPrefix(err.Error(), "TASK_MISSING:") {
		standaloneFail(w, http.StatusNotFound, "TASK_NOT_FOUND")
		return
	}
	for _, code := range []string{"REVISION_CONFLICT", "IDEMPOTENCY_CONFLICT", "TASK_EXISTS", "REMOTE_OPERATION_REPLAY", "TERMINAL_TASK", "PLAN_DIGEST_MISMATCH", "PLAN_PARENT_CHANGED", "PLAN_TASK_CHANGED", "PLAN_STALE_TASK", "PLAN_APPLY_IN_PROGRESS", "PLAN_APPLY_INCOMPLETE", "REMOTE_CANDIDATE_REQUIRED"} {
		if strings.HasPrefix(err.Error(), code+":") {
			standaloneFail(w, http.StatusConflict, code)
			return
		}
	}
	standaloneFail(w, http.StatusBadRequest, "TASK_REJECTED")
}

func (h *standaloneHandler) remoteTasks(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project string, parts []string) {
	if len(parts) == 2 && parts[1] == "artifacts" && r.Method == http.MethodGet {
		h.remoteTaskArtifacts(w, principal, project, parts[0])
		return
	}
	role, err := h.server.role(principal, project)
	if err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	if r.Method != http.MethodGet && role != "admin" && role != "executor" {
		standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	service, err := h.server.projectService(project)
	if err != nil {
		standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
		return
	}
	projection := "ready"
	if service.Store.Flush() != nil {
		projection = "blocked"
	}
	if r.Method != http.MethodGet && projection != "ready" {
		standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
		return
	}
	if len(parts) == 0 && r.Method == http.MethodGet {
		query := r.URL.Query()
		parseNumber := func(name string) (int, error) {
			if query.Get(name) == "" {
				return 0, nil
			}
			return strconv.Atoi(query.Get(name))
		}
		limit, limitErr := parseNumber("limit")
		offset, offsetErr := parseNumber("offset")
		days, daysErr := parseNumber("days")
		if limitErr != nil || offsetErr != nil || daysErr != nil || limit < 0 || offset < 0 || days < 0 || days > 36500 {
			standaloneFail(w, http.StatusBadRequest, "INVALID_QUERY")
			return
		}
		page, err := service.QueryPage(core.Query{Limit: limit, Offset: offset, Days: days, Text: query.Get("query"), Board: query.Get("board"), Parent: query.Get("parent")})
		if err != nil {
			standaloneTaskError(w, err)
			return
		}
		result := make([]remoteNextView, 0, len(page.Items))
		for _, item := range page.Items {
			if item.Task != nil {
				revision := core.RemoteRevision(*item.Task)
				safe := core.RemoteTaskView(*item.Task)
				item.Task = &safe
				result = append(result, remoteNextView{Next: item, StateRevision: revision, ProjectionStatus: projection})
			}
		}
		standaloneReply(w, http.StatusOK, map[string]any{"mode": "remote", "items": result, "counts": page.Counts, "total": page.Total, "limit_per_board": page.Limit, "offset_per_board": page.Offset, "projection_status": projection})
		return
	}
	if len(parts) == 2 && parts[1] == "next" && r.Method == http.MethodGet {
		next, err := service.Next(parts[0])
		if err != nil {
			standaloneTaskError(w, err)
			return
		}
		if next.Task == nil {
			standaloneFail(w, http.StatusNotFound, "TASK_NOT_FOUND")
			return
		}
		revision := core.RemoteRevision(*next.Task)
		safe := core.RemoteTaskView(*next.Task)
		next.Task = &safe
		standaloneReply(w, http.StatusOK, remoteNextView{Next: next, StateRevision: revision, ProjectionStatus: projection})
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		next, err := service.Next(parts[0])
		if err != nil {
			standaloneTaskError(w, err)
			return
		}
		if next.Task == nil {
			standaloneFail(w, http.StatusNotFound, "TASK_NOT_FOUND")
			return
		}
		revision := core.RemoteRevision(*next.Task)
		safe := core.RemoteTaskView(*next.Task)
		next.Task = &safe
		standaloneReply(w, http.StatusOK, remoteNextView{Next: next, StateRevision: revision, ProjectionStatus: projection})
		return
	}
	if len(parts) == 2 && parts[1] == "tree" && r.Method == http.MethodGet {
		tree, err := service.Tree(parts[0])
		if err != nil {
			standaloneTaskError(w, err)
			return
		}
		if tree.Root.Task != nil {
			safe := core.RemoteTaskView(*tree.Root.Task)
			tree.Root.Task = &safe
		}
		for i := range tree.Children {
			if tree.Children[i].Task.Task != nil {
				safe := core.RemoteTaskView(*tree.Children[i].Task.Task)
				tree.Children[i].Task.Task = &safe
			}
		}
		raw, err := json.Marshal(tree)
		if err != nil {
			standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
			return
		}
		var result map[string]any
		if err = json.Unmarshal(raw, &result); err != nil {
			standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
			return
		}
		result["projection_status"] = projection
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if len(parts) == 3 && parts[1] == "plan" {
		h.remotePlan(w, r, principal, project, parts[0], parts[2], service)
		return
	}
	allowed := map[string]bool{"edit": true, "pause": true, "cancel": true, "resume": true, "link": true}
	if r.Method != http.MethodPost || (len(parts) != 0 && !(len(parts) == 2 && allowed[parts[1]])) {
		standaloneFail(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	var input remoteTaskMutation
	if decode(r, &input) != nil || !standaloneID.MatchString(input.OperationID) || !standaloneID.MatchString(input.IdempotencyKey) || input.ExpectedRevision == "" {
		standaloneFail(w, http.StatusBadRequest, "REMOTE_OPERATION_REQUIRED")
		return
	}
	action := "create"
	if len(parts) == 2 {
		action = parts[1] + ":" + parts[0]
	}
	requestBytes, err := json.Marshal(struct {
		Actor, Project, Action string
		Input                  remoteTaskMutation
	}{principal.UserID, project, action, input})
	if err != nil {
		standaloneFail(w, http.StatusBadRequest, "TASK_REJECTED")
		return
	}
	hash := store.Hash(requestBytes)
	if receipt, found, err := service.RemoteReceipt(input.IdempotencyKey, hash); found || err != nil {
		if err != nil {
			standaloneTaskError(w, err)
			return
		}
		status := http.StatusOK
		if action == "create" {
			status = http.StatusCreated
		}
		remoteReceiptReady(w, service, status, receipt)
		return
	}
	taskID := input.Definition.ID
	if action == "create" {
		if input.ExpectedRevision != "absent" {
			standaloneFail(w, http.StatusConflict, "REVISION_CONFLICT")
			return
		}
		if taskID == "" {
			taskID, err = standaloneRandom("WI-")
			if err != nil {
				standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
				return
			}
		}
		input.Definition.ID = taskID
		input.Definition.Revision = 0
	} else {
		taskID = parts[0]
	}
	op := core.RemoteOperation{ID: input.OperationID, Key: input.IdempotencyKey, RequestHash: hash, TaskID: taskID, Expected: input.ExpectedRevision, Actor: principal.Username}
	guarded := service.WithRemoteOperation(op)
	switch {
	case action == "create":
		_, err = guarded.Create(input.Definition)
	case strings.HasPrefix(action, "edit:"):
		_, err = guarded.Edit(taskID, input.Definition, input.Reason)
	case strings.HasPrefix(action, "pause:"):
		_, err = guarded.Pause(taskID, input.Reason)
	case strings.HasPrefix(action, "cancel:"):
		_, err = guarded.Cancel(taskID, input.Reason)
	case strings.HasPrefix(action, "resume:"):
		_, err = guarded.Resume(taskID, input.ReturnTo, input.AckStopped)
	case strings.HasPrefix(action, "link:"):
		required := true
		if input.Required != nil {
			required = *input.Required
		}
		_, err = guarded.LinkEpic(input.Parent, taskID, required, input.Milestone)
	}
	if err != nil {
		if receipt, found, lookupErr := service.RemoteReceipt(input.IdempotencyKey, hash); lookupErr == nil && found {
			status := http.StatusOK
			if action == "create" {
				status = http.StatusCreated
			}
			remoteReceiptReady(w, service, status, receipt)
			return
		}
		standaloneTaskError(w, err)
		return
	}
	receipt, found, err := service.RemoteReceipt(input.IdempotencyKey, hash)
	if err != nil || !found {
		standaloneFail(w, http.StatusInternalServerError, "OPERATION_RECEIPT_UNAVAILABLE")
		return
	}
	status := http.StatusOK
	if action == "create" {
		status = http.StatusCreated
	}
	remoteReceiptReady(w, service, status, receipt)
}

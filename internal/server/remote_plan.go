package server

import (
	"ael/assets"
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"net/http"
)

type remotePlanRequest struct {
	OperationID      string            `json:"operation_id"`
	IdempotencyKey   string            `json:"idempotency_key"`
	ExpectedRevision string            `json:"expected_revision"`
	Input            core.PlanInput    `json:"input"`
	Proposal         core.PlanProposal `json:"proposal"`
	Digest           string            `json:"digest"`
	PlanID           string            `json:"plan_id"`
	Reason           string            `json:"reason"`
}

func (h *standaloneHandler) remotePlan(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project, parent, action string, service *core.Service) {
	if r.Method != http.MethodPost || (action != "prepare" && action != "preview" && action != "apply" && action != "abandon") {
		standaloneFail(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	var input remotePlanRequest
	if decode(r, &input) != nil {
		standaloneFail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if action == "preview" {
		if input.Proposal.Parent != parent {
			standaloneFail(w, http.StatusBadRequest, "PLAN_PARENT_MISMATCH")
			return
		}
		preview, err := service.PreviewPlan(input.Proposal)
		if err != nil {
			standaloneTaskError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, preview)
		return
	}
	if !standaloneID.MatchString(input.OperationID) || !standaloneID.MatchString(input.IdempotencyKey) || input.ExpectedRevision == "" {
		standaloneFail(w, http.StatusBadRequest, "REMOTE_OPERATION_REQUIRED")
		return
	}
	if action == "apply" && (input.Proposal.Parent != parent || input.Proposal.ID != input.OperationID || input.OperationID != input.IdempotencyKey) {
		standaloneFail(w, http.StatusBadRequest, "REMOTE_OPERATION_REQUIRED")
		return
	}
	if action == "abandon" && (!standaloneID.MatchString(input.PlanID) || input.IdempotencyKey == input.PlanID) {
		standaloneFail(w, http.StatusBadRequest, "REMOTE_OPERATION_REQUIRED")
		return
	}
	raw, err := json.Marshal(struct {
		Actor, Project, Parent, Action string
		Input                          remotePlanRequest
	}{principal.UserID, project, parent, action, input})
	if err != nil {
		standaloneFail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	hash := store.Hash(raw)
	reply := func(receipt core.RemoteOperationReceipt) {
		if service.Store.Flush() != nil {
			standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
			return
		}
		if action == "prepare" {
			schema, err := assets.Files.ReadFile("plan-proposal.json")
			if err != nil {
				standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
				return
			}
			standaloneReply(w, http.StatusOK, map[string]any{"operation_id": receipt.OperationID, "revision": receipt.Revision, "task": receipt.Task, "result": receipt.Result, "schema": json.RawMessage(schema)})
			return
		}
		standaloneReply(w, http.StatusOK, receipt)
	}
	if receipt, found, err := service.RemoteReceipt(input.IdempotencyKey, hash); found || err != nil {
		if err != nil {
			standaloneTaskError(w, err)
			return
		}
		reply(receipt)
		return
	}
	op := core.RemoteOperation{ID: input.OperationID, Key: input.IdempotencyKey, RequestHash: hash, TaskID: parent, Expected: input.ExpectedRevision, Actor: principal.Username}
	if action == "abandon" {
		op.ReleasePlan = input.PlanID
	}
	guarded := service.WithRemoteOperation(op)
	if action == "prepare" {
		_, err = guarded.PreparePlan(parent, input.Input)
	} else if action == "apply" {
		_, err = guarded.ApplyPlan(input.Proposal, input.Digest)
	} else {
		_, err = guarded.AbandonPlan(parent, input.PlanID, input.Reason)
	}
	if err != nil {
		if receipt, found, lookupErr := service.RemoteReceipt(input.IdempotencyKey, hash); lookupErr == nil && found {
			reply(receipt)
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
	reply(receipt)
}

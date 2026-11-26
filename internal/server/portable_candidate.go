package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type portableStore struct {
	service       *core.Service
	project       string
	requestDigest string
}

func (s *Standalone) portable(project string, service *core.Service) (*portableStore, error) {
	_, err := service.Store.DB.Exec(`
CREATE TABLE IF NOT EXISTS portable_repositories(id TEXT PRIMARY KEY,actor TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS portable_candidates(id TEXT PRIMARY KEY,repository TEXT NOT NULL,commit_id TEXT NOT NULL,body TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS portable_observations(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,task_revision TEXT NOT NULL,candidate_id TEXT NOT NULL,checkout_id TEXT NOT NULL,workspace_id TEXT NOT NULL,runner_id TEXT NOT NULL,inputs_known INTEGER NOT NULL,actor TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS portable_operations(actor TEXT NOT NULL,key TEXT NOT NULL,hash TEXT NOT NULL,result TEXT NOT NULL,PRIMARY KEY(actor,key));
CREATE TABLE IF NOT EXISTS portable_contexts(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,candidate_id TEXT NOT NULL,body TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS portable_context_sources(context_id TEXT NOT NULL,id TEXT NOT NULL,path TEXT NOT NULL,artifact_id TEXT NOT NULL,sha256 TEXT NOT NULL,status TEXT NOT NULL,kind TEXT NOT NULL,reason TEXT NOT NULL,PRIMARY KEY(context_id,id));
CREATE TABLE IF NOT EXISTS findings(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,candidate_id TEXT NOT NULL,check_id TEXT NOT NULL,fingerprint TEXT NOT NULL UNIQUE,body TEXT NOT NULL,first_seen TEXT NOT NULL,last_seen TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS findings_task ON findings(task_id,candidate_id);
CREATE TABLE IF NOT EXISTS finding_occurrences(id TEXT PRIMARY KEY,finding_id TEXT NOT NULL,check_report_id TEXT NOT NULL,body TEXT NOT NULL,created TEXT NOT NULL,UNIQUE(finding_id,check_report_id));
CREATE TABLE IF NOT EXISTS finding_history(id TEXT PRIMARY KEY,finding_id TEXT NOT NULL,kind TEXT NOT NULL,body TEXT NOT NULL,created TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS finding_history_entity ON finding_history(finding_id,created);
CREATE INDEX IF NOT EXISTS finding_repair_task ON findings(json_extract(body,'$.repair.task_id'));
CREATE INDEX IF NOT EXISTS finding_lineage ON findings(json_extract(body,'$.lineage_id'));
CREATE TABLE IF NOT EXISTS portable_checks(id TEXT PRIMARY KEY,context_id TEXT NOT NULL,task_id TEXT NOT NULL,candidate_id TEXT NOT NULL,check_id TEXT NOT NULL,artifact_id TEXT NOT NULL,body TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remote_runners(id TEXT PRIMARY KEY,actor TEXT NOT NULL,workspace_id TEXT NOT NULL,checkout_id TEXT NOT NULL,repository TEXT NOT NULL,version TEXT NOT NULL,capabilities TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remote_runner_labels(runner_id TEXT PRIMARY KEY,alias TEXT NOT NULL,machine TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remote_runner_attempts(attempt_id TEXT PRIMARY KEY,task_id TEXT NOT NULL,runner_id TEXT NOT NULL,actor TEXT NOT NULL,context_id TEXT NOT NULL,candidate_id TEXT NOT NULL,claim_revision TEXT NOT NULL,output_candidate TEXT NOT NULL DEFAULT '',state TEXT NOT NULL DEFAULT 'running',fence INTEGER NOT NULL,token_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remote_artifact_leases(upload_id TEXT PRIMARY KEY,runner_id TEXT NOT NULL,attempt_id TEXT NOT NULL,fence INTEGER NOT NULL,token_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remote_submission_digests(submission_key TEXT PRIMARY KEY,digest TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remote_workspace_holds(repository TEXT NOT NULL,workspace_id TEXT NOT NULL,attempt_id TEXT NOT NULL,task_id TEXT NOT NULL,state TEXT NOT NULL,PRIMARY KEY(repository,workspace_id,attempt_id));
CREATE TABLE IF NOT EXISTS remote_runner_result_ops(submission_key TEXT PRIMARY KEY,actor TEXT NOT NULL,runner_id TEXT NOT NULL,operation_id TEXT NOT NULL);`)
	if err != nil {
		return nil, err
	}
	return &portableStore{service: service, project: project}, nil
}

type portableRepositoryInput struct {
	ID               string `json:"id"`
	ExpectedRevision string `json:"expected_revision"`
	OperationID      string `json:"operation_id"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type portableObservationInput struct {
	TaskID           string                   `json:"task_id"`
	ExpectedRevision string                   `json:"expected_revision"`
	CheckoutID       string                   `json:"checkout_id"`
	WorkspaceID      string                   `json:"workspace_id"`
	RunnerID         string                   `json:"runner_id"`
	InputsKnown      bool                     `json:"inputs_known"`
	Candidate        remote.PortableCandidate `json:"candidate"`
	OperationID      string                   `json:"operation_id"`
	IdempotencyKey   string                   `json:"idempotency_key"`
}

func (p *portableStore) mutate(principal standalonePrincipal, action, op, key string, input any, apply func(*store.Tx) (map[string]any, error)) (map[string]any, error) {
	if !standaloneID.MatchString(op) || !standaloneID.MatchString(key) {
		return nil, errors.New("REMOTE_OPERATION_REQUIRED")
	}
	raw, err := json.Marshal(struct {
		Actor, Project, Action string
		Input                  any
	}{principal.UserID, p.project, action, input})
	if err != nil {
		return nil, errors.New("CANDIDATE_INVALID")
	}
	hash := store.Hash(raw)
	var result map[string]any
	err = p.service.Store.Write(func(tx *store.Tx) error {
		var priorHash, prior string
		err := tx.Row("SELECT hash,result FROM portable_operations WHERE actor=? AND key=?", principal.UserID, key).Scan(&priorHash, &prior)
		if err == nil {
			if priorHash != hash {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			return json.Unmarshal([]byte(prior), &result)
		}
		if err != sql.ErrNoRows {
			return err
		}
		result, err = apply(tx)
		if err != nil {
			return err
		}
		if p.requestDigest != "" {
			result["request_digest"] = p.requestDigest
		}
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO portable_operations(actor,key,hash,result) VALUES(?,?,?,?)", principal.UserID, key, hash, string(body)); err != nil {
			return err
		}
		// Normalize nested structs so a first response and its replay have
		// exactly the same JSON representation.
		return json.Unmarshal(body, &result)
	})
	return result, err
}

func (p *portableStore) register(principal standalonePrincipal, input portableRepositoryInput) (map[string]any, error) {
	if !standaloneID.MatchString(input.ID) || input.ExpectedRevision != "absent" {
		return nil, errors.New("CANDIDATE_INVALID")
	}
	return p.mutate(principal, "register-repository", input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var existing string
		err := tx.Row("SELECT id FROM portable_repositories WHERE id=?", input.ID).Scan(&existing)
		if err == nil {
			return nil, errors.New("REVISION_CONFLICT")
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		created := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err = tx.Exec("INSERT INTO portable_repositories(id,actor,created) VALUES(?,?,?)", input.ID, principal.UserID, created); err != nil {
			return nil, err
		}
		return map[string]any{"id": input.ID, "created_at": created, "operation_id": input.OperationID}, nil
	})
}

func (p *portableStore) observe(principal standalonePrincipal, input portableObservationInput) (map[string]any, error) {
	if !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.CheckoutID) || !standaloneID.MatchString(input.WorkspaceID) || !standaloneID.MatchString(input.RunnerID) || input.ExpectedRevision == "" || remote.ValidatePortableCandidate(input.Candidate) != nil {
		return nil, errors.New("CANDIDATE_INVALID")
	}
	return p.mutate(principal, "observe-candidate", input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var registered string
		if err := tx.Row("SELECT id FROM portable_repositories WHERE id=?", input.Candidate.Repository).Scan(&registered); err != nil {
			return nil, errors.New("REPOSITORY_NOT_FOUND")
		}
		task, err := artifactTask(tx, input.TaskID)
		if err != nil {
			return nil, errors.New("TASK_NOT_FOUND")
		}
		if core.RemoteRevision(task) != input.ExpectedRevision {
			return nil, errors.New("REVISION_CONFLICT")
		}
		body, err := json.Marshal(input.Candidate)
		if err != nil {
			return nil, err
		}
		var existing string
		err = tx.Row("SELECT body FROM portable_candidates WHERE id=?", input.Candidate.ID).Scan(&existing)
		if err == nil && existing != string(body) {
			return nil, errors.New("CANDIDATE_CONFLICT")
		}
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		created := time.Now().UTC().Format(time.RFC3339Nano)
		if err == sql.ErrNoRows {
			if _, err = tx.Exec("INSERT INTO portable_candidates(id,repository,commit_id,body,created) VALUES(?,?,?,?,?)", input.Candidate.ID, input.Candidate.Repository, input.Candidate.Commit, string(body), created); err != nil {
				return nil, err
			}
		}
		id, err := standaloneRandom("observation_")
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO portable_observations(id,task_id,task_revision,candidate_id,checkout_id,workspace_id,runner_id,inputs_known,actor,created) VALUES(?,?,?,?,?,?,?,?,?,?)", id, input.TaskID, input.ExpectedRevision, input.Candidate.ID, input.CheckoutID, input.WorkspaceID, input.RunnerID, input.InputsKnown, principal.UserID, created); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "task_id": input.TaskID, "task_revision": input.ExpectedRevision, "candidate_id": input.Candidate.ID, "repository": input.Candidate.Repository, "commit": input.Candidate.Commit, "checkout_id": input.CheckoutID, "workspace_id": input.WorkspaceID, "runner_id": input.RunnerID, "inputs_known": input.InputsKnown, "capture_stability_proven": false, "actor": principal.Username, "created_at": created, "operation_id": input.OperationID}, nil
	})
}

func portableError(w http.ResponseWriter, err error) {
	code, detail, _ := strings.Cut(err.Error(), ": ")
	status := http.StatusInternalServerError
	switch code {
	case "PROJECT_NOT_FOUND", "FINDING_NOT_FOUND", "REPOSITORY_NOT_FOUND", "TASK_NOT_FOUND", "CANDIDATE_NOT_FOUND", "CONTEXT_NOT_FOUND", "OBSERVATION_NOT_FOUND", "CHECK_NOT_FOUND", "RUNNER_NOT_FOUND":
		status = http.StatusNotFound
	case "FINDING_EVIDENCE_INVALID", "FINDING_SOURCE_MISMATCH", "REMOTE_HANDOFF_TARGET", "WORKSPACE_CONFIRMATION_REQUIRED", "REVISION_CONFLICT", "IDEMPOTENCY_CONFLICT", "CANDIDATE_CONFLICT", "CONTEXT_SOURCE_MISMATCH", "KNOWLEDGE_MISSING", "KNOWLEDGE_CONFLICT", "KNOWLEDGE_UNVERIFIED", "CANDIDATE_CHANGED", "REMOTE_WORKSPACE_MISMATCH", "STALE_TOKEN", "STALE_EXECUTION_UNIT", "CLAIM_CONFLICT", "RUNNER_WIP", "NEXT_WORK_CHANGED", "PARENT_SCOPE_REQUIRED", "NOT_RUNNABLE", "PLAN_APPLY_IN_PROGRESS", "DEPENDENCY_BLOCKED", "DELIVERY_MANUAL_REQUIRED", "PLAN_APPROVAL_REQUIRED", "HUMAN_REVIEW_REQUIRED", "RUNNER_CAPABILITY_MISSING", "CHECK_REQUIRED", "PARENT_WRITE_FORBIDDEN", "ROOT_CAUSE_REVIEW_REQUIRED", "ROOT_CAUSE_REVIEW_NOT_EXPECTED", "ROOT_CAUSE_NEW_EVIDENCE_REQUIRED", "BUDGET_EXHAUSTED", "POLICY_CHANGED", "INSPECTION_WRITE_DETECTED", "ARTIFACT_REQUIRED", "ARTIFACT_MISSING", "CHECK_METHOD_MISMATCH", "CHECK_RESULT_INVALID", "ASSESSMENT_EVIDENCE_REQUIRED", "SCREENSHOT_REQUIRED", "TASK_CONTEXT_REQUIRED", "KNOWLEDGE_OUTCOME_REQUIRED", "KNOWLEDGE_ARTIFACT_REQUIRED", "TRUSTED_CHECK_REQUIRED", "TERMINAL_TASK", "WRONG_UNIT", "QUEUE_RESERVED", "QUEUE_STARTED", "QUEUE_VERSION_CONFLICT":
		status = http.StatusConflict
	case "CONTEXT_SOURCE_UNAVAILABLE":
		status = http.StatusGone
	case "ARTIFACT_INVALID", "FINDING_INVALID", "FINDING_DUPLICATE", "REMOTE_RECOVERY_INVALID", "REMOTE_OPERATION_REQUIRED", "CANDIDATE_INVALID", "RUNNER_INVALID", "PROGRESS_REQUIRED", "INVALID_WORK_STAGE", "RESULT_REQUIRED", "INVALID_RESULT", "INVALID_FAILURE_CLASS", "QUEUE_INVALID":
		status = http.StatusBadRequest
	case "FORBIDDEN", "QUEUE_TARGET_FORBIDDEN":
		status = http.StatusForbidden
	case "ONBOARDING_GRANT_REVOKED":
		status = http.StatusForbidden
	case "ONBOARDING_INVALID":
		status = http.StatusBadRequest
	case "PROJECT_ID_CONFLICT":
		status = http.StatusConflict
	default:
		standaloneFail(w, http.StatusInternalServerError, "CANDIDATE_STORAGE_ERROR")
		return
	}
	if detail != "" && len(detail) <= 500 && !strings.ContainsAny(detail, "\r\n\x00") && (code == "KNOWLEDGE_MISSING" || code == "KNOWLEDGE_CONFLICT" || code == "CONTEXT_SOURCE_MISMATCH" || code == "REVISION_CONFLICT" || code == "CANDIDATE_CHANGED" || code == "ARTIFACT_REQUIRED" || code == "RUNNER_INVALID" || code == "RUNNER_WIP" || strings.HasPrefix(code, "QUEUE_")) {
		standaloneReply(w, status, map[string]string{"code": code, "error": code, "detail": detail})
		return
	}
	standaloneFail(w, status, code)
}

func (h *standaloneHandler) remotePortable(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project, kind string, parts []string) {
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
		portableError(w, err)
		return
	}
	p, err := h.server.portable(project, service)
	if err != nil {
		portableError(w, err)
		return
	}
	if kind == "portable-operations" && len(parts) == 1 && r.Method == http.MethodGet {
		if !standaloneID.MatchString(parts[0]) {
			portableError(w, errors.New("CANDIDATE_INVALID"))
			return
		}
		var saved string
		err := service.Store.DB.QueryRow("SELECT result FROM portable_operations WHERE actor=? AND key=?", principal.UserID, parts[0]).Scan(&saved)
		if err == sql.ErrNoRows {
			standaloneReply(w, http.StatusOK, map[string]any{"found": false})
			return
		}
		if err != nil {
			portableError(w, err)
			return
		}
		var result map[string]any
		if json.Unmarshal([]byte(saved), &result) != nil {
			portableError(w, errors.New("CANDIDATE_STORAGE_ERROR"))
			return
		}
		standaloneReply(w, http.StatusOK, map[string]any{"found": true, "result": result})
		return
	}
	if kind == "runners" && len(parts) == 3 && parts[1] == "operations" && r.Method == http.MethodGet {
		result, err := p.runnerResultReceipt(principal, parts[0], parts[2])
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if kind == "queue" && len(parts) == 0 && r.Method == http.MethodGet {
		q := r.URL.Query()
		result, err := p.listQueue(principal, q.Get("runner"), q.Get("state"), q.Get("task"))
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if kind == "runners" && len(parts) == 1 && r.Method == http.MethodGet {
		result, err := p.getRunner(principal, parts[0])
		if err != nil {
			portableError(w, err)
			return
		}
		result["project_role"] = role
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if kind == "runners" && len(parts) == 2 && parts[1] == "next" && r.Method == http.MethodGet {
		result, err := p.nextRunner(principal, parts[0], r.URL.Query().Get("parent"))
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if kind == "runners" && r.Method == http.MethodPost {
		// Communication only; ownership, role, lease and version checks remain below.
		if len(parts) == 0 {
			var input remoteRunnerInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			result, err := p.registerRunner(principal, input)
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusCreated, result)
			return
		}
		if len(parts) == 2 && parts[1] == "recover" {
			if role != "admin" && role != "executor" {
				standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
				return
			}
			var input remoteRunnerRecoveryInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("REMOTE_RECOVERY_INVALID"))
				return
			}
			result, err := p.recoverRunner(principal, parts[0], input)
			if err != nil {
				portableError(w, err)
				return
			}
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && (parts[1] == "queue" || parts[1] == "queue-release" || parts[1] == "queue-transfer") {
			var input remoteQueueInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("QUEUE_INVALID"))
				return
			}
			var result map[string]any
			switch parts[1] {
			case "queue":
				result, err = p.enqueueRunner(principal, role, parts[0], input)
			case "queue-release":
				result, err = p.releaseQueue(principal, role, parts[0], input)
			default:
				result, err = p.transferQueue(principal, role, parts[0], input, h.server.queueMember(project))
			}
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && parts[1] == "claim" {
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			artifacts, err := h.server.artifacts(project, service)
			if err != nil {
				portableError(w, err)
				return
			}
			var input remoteRunnerClaimInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			result, err := p.claimRunner(principal, role, parts[0], input, artifacts)
			if err != nil {
				portableError(w, err)
				return
			}
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			standaloneReply(w, http.StatusCreated, result)
			return
		}
		if len(parts) == 2 && parts[1] == "heartbeat" {
			var input remoteRunnerHeartbeatInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			result, err := p.heartbeatRunner(principal, parts[0], input)
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && parts[1] == "progress" {
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			var input remoteRunnerProgressInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			if strings.Contains(input.Summary, input.Token) || strings.Contains(input.Next, input.Token) {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			result, err := p.progressRunner(principal, parts[0], input)
			if err != nil {
				portableError(w, err)
				return
			}
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && parts[1] == "observe" {
			var input remoteRunnerObservationInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			result, err := p.observeRunner(principal, parts[0], input)
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && parts[1] == "interrupt" {
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			var input remoteRunnerInterruptInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			result, err := p.interruptRunner(principal, parts[0], input)
			if err != nil {
				portableError(w, err)
				return
			}
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
		if len(parts) == 2 && parts[1] == "submit" {
			if service.Store.Flush() != nil {
				standaloneFail(w, http.StatusServiceUnavailable, "PROJECTION_BLOCKED")
				return
			}
			artifacts, err := h.server.artifacts(project, service)
			if err != nil {
				portableError(w, err)
				return
			}
			var input remoteRunnerSubmitInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("RUNNER_INVALID"))
				return
			}
			result, err := p.submitRunner(principal, parts[0], input, artifacts)
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
	}
	if kind == "repositories" && len(parts) == 0 && r.Method == http.MethodPost {
		var input portableRepositoryInput
		if decodePortable(r, &input, p) != nil {
			portableError(w, errors.New("CANDIDATE_INVALID"))
			return
		}
		result, err := p.register(principal, input)
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusCreated, result)
		return
	}
	if kind == "repositories" && len(parts) == 0 && r.Method == http.MethodGet {
		rows, err := service.Store.DB.Query("SELECT id,created FROM portable_repositories ORDER BY id")
		if err != nil {
			portableError(w, err)
			return
		}
		items := []map[string]string{}
		for rows.Next() {
			var id, created string
			if err = rows.Scan(&id, &created); err != nil {
				break
			}
			items = append(items, map[string]string{"id": id, "created_at": created})
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	if kind == "candidates" && len(parts) == 0 && r.Method == http.MethodPost {
		var input portableObservationInput
		if decodePortable(r, &input, p) != nil {
			portableError(w, errors.New("CANDIDATE_INVALID"))
			return
		}
		result, err := p.observe(principal, input)
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusCreated, result)
		return
	}
	if kind == "candidates" && len(parts) == 1 && r.Method == http.MethodGet {
		var body string
		if !portableDigestID(parts[0]) || service.Store.DB.QueryRow("SELECT body FROM portable_candidates WHERE id=?", parts[0]).Scan(&body) != nil {
			portableError(w, errors.New("CANDIDATE_NOT_FOUND"))
			return
		}
		var candidate remote.PortableCandidate
		if json.Unmarshal([]byte(body), &candidate) != nil || remote.ValidatePortableCandidate(candidate) != nil {
			portableError(w, errors.New("CANDIDATE_STORAGE_ERROR"))
			return
		}
		standaloneReply(w, http.StatusOK, candidate)
		return
	}
	if kind == "observations" && len(parts) == 1 && r.Method == http.MethodGet {
		var taskID, revision, candidateID, checkout, workspace, runner, created string
		var inputsKnown bool
		if !standaloneID.MatchString(parts[0]) || service.Store.DB.QueryRow("SELECT task_id,task_revision,candidate_id,checkout_id,workspace_id,runner_id,inputs_known,created FROM portable_observations WHERE id=?", parts[0]).Scan(&taskID, &revision, &candidateID, &checkout, &workspace, &runner, &inputsKnown, &created) != nil {
			portableError(w, errors.New("OBSERVATION_NOT_FOUND"))
			return
		}
		var candidateBody string
		if service.Store.DB.QueryRow("SELECT body FROM portable_candidates WHERE id=?", candidateID).Scan(&candidateBody) != nil {
			portableError(w, errors.New("CANDIDATE_STORAGE_ERROR"))
			return
		}
		var candidate remote.PortableCandidate
		if json.Unmarshal([]byte(candidateBody), &candidate) != nil || remote.ValidatePortableCandidate(candidate) != nil {
			portableError(w, errors.New("CANDIDATE_STORAGE_ERROR"))
			return
		}
		standaloneReply(w, http.StatusOK, map[string]any{"id": parts[0], "task_id": taskID, "task_revision": revision, "candidate_id": candidateID, "repository": candidate.Repository, "commit": candidate.Commit, "checkout_id": checkout, "workspace_id": workspace, "runner_id": runner, "inputs_known": inputsKnown, "created_at": created})
		return
	}
	if kind == "contexts" {
		artifacts, err := h.server.artifacts(project, service)
		if err != nil {
			portableError(w, err)
			return
		}
		if len(parts) == 2 && parts[1] == "checks" && r.Method == http.MethodPost {
			var input portableCheckInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("CANDIDATE_INVALID"))
				return
			}
			result, err := p.reportCheck(principal, parts[0], input, artifacts, len(principal.CSRFHash) > 0 && (role == "admin" || role == "reviewer"))
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusCreated, result)
			return
		}
		if len(parts) == 0 && r.Method == http.MethodPost {
			var input portableContextInput
			if decodePortable(r, &input, p) != nil {
				portableError(w, errors.New("CANDIDATE_INVALID"))
				return
			}
			result, err := p.context(principal, input, artifacts)
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusCreated, result)
			return
		}
		if r.Method == http.MethodGet && len(parts) <= 1 {
			id := ""
			if len(parts) == 1 {
				id = parts[0]
			} else {
				taskID := r.URL.Query().Get("task")
				if !standaloneID.MatchString(taskID) {
					portableError(w, errors.New("TASK_NOT_FOUND"))
					return
				}
				var found string
				if service.Store.DB.QueryRow("SELECT id FROM tasks WHERE id=?", taskID).Scan(&found) != nil {
					portableError(w, errors.New("TASK_NOT_FOUND"))
					return
				}
				err = service.Store.DB.QueryRow("SELECT id FROM portable_contexts WHERE task_id=? ORDER BY rowid DESC LIMIT 1", taskID).Scan(&id)
				if err == sql.ErrNoRows {
					standaloneReply(w, http.StatusOK, map[string]any{"status": "empty", "task_id": taskID, "sources": []any{}})
					return
				}
				if err != nil {
					portableError(w, err)
					return
				}
			}
			result, err := p.getContext(id, artifacts)
			if err != nil {
				portableError(w, err)
				return
			}
			standaloneReply(w, http.StatusOK, result)
			return
		}
	}
	standaloneFail(w, http.StatusNotFound, "NOT_FOUND")
}

func portableDigestID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func decodePortable(r *http.Request, target any, p *portableStore) error {
	limit := jsonRequestLimit(r)
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return errors.New("REMOTE_REQUEST_INVALID")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err = decode(r, target); err != nil {
		return err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return err
	}
	digest, err := remote.RequestDigest(value)
	if err != nil {
		return err
	}
	p.requestDigest = digest
	return nil
}

package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type remoteReviewRequest struct {
	Comparison *struct {
		Baseline string `json:"baseline"`
		Actual   string `json:"actual"`
	} `json:"comparison,omitempty"`
	Action       string   `json:"action"`
	Task         string   `json:"id"`
	Check        string   `json:"unit"`
	Executor     string   `json:"executor"` // Accepted for existing UI compatibility; identity comes from the session.
	Reason       string   `json:"reason"`
	Recovery     string   `json:"recovery"`
	Result       string   `json:"result"`
	Artifacts    []string `json:"artifacts"`
	Candidate    string   `json:"candidate"`
	Revision     int      `json:"revision"`
	PlanRevision int      `json:"plan_revision"`
	NoteID       string   `json:"review_note_id"`
	Key          string   `json:"request_key"`
}

func (p *portableStore) reviewEnvironment(expected core.ReviewExpectation) *core.RemoteReviewEnvironment {
	return &core.RemoteReviewEnvironment{
		Validate: func(tx *store.Tx, task *core.Task) error {
			if task.Candidate != expected.Candidate || task.Definition.Revision != expected.DefinitionRevision || task.Plan.Revision != expected.PlanRevision {
				return core.Error("REVIEW_VERSION_MISMATCH", "review the current candidate and revisions")
			}
			keys := []string{"remote-plan-task:" + store.Hash([]byte(task.Definition.ID)), "remote-plan-parent:" + store.Hash([]byte(task.Definition.ID)), "remote-plan-parent:" + store.Hash([]byte(task.Definition.Parent))}
			if task.EpicMembership != nil {
				keys = append(keys, "remote-plan-parent:"+store.Hash([]byte(task.EpicMembership.Parent)))
			}
			for _, key := range keys {
				var active string
				err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&active)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				if active != "" {
					return core.Error("PLAN_APPLY_IN_PROGRESS", "finish the current plan before review")
				}
			}
			if expected.Candidate != "" {
				var candidate, created string
				err := tx.Row("SELECT candidate_id,created FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", task.Definition.ID).Scan(&candidate, &created)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				if !reviewObservationMatches(*task, candidate, created) {
					return core.Error("CANDIDATE_CHANGED", "a newer client observation requires reconciliation")
				}
			}
			return nil
		},
	}
}

func (p *portableStore) reviewRemote(principal standalonePrincipal, input remoteReviewRequest, artifacts *artifactStore) (map[string]any, error) {
	if !standaloneID.MatchString(input.Task) || !standaloneID.MatchString(input.Key) || len(input.Artifacts) > 32 || len(input.Reason) > 8000 || len(input.Recovery) > 8000 || input.Revision < 1 || input.PlanRevision < 1 {
		return nil, core.Error("REVIEW_INPUT_REQUIRED", "task, request key and exact versions are required")
	}
	if input.Action != "review_result" && input.Action != "review_pending" && input.Action != "review_confirm" && input.Action != "review_resume" {
		return nil, errors.New("REVIEW_ACTION_INVALID")
	}
	expected := core.ReviewExpectation{Candidate: input.Candidate, DefinitionRevision: input.Revision, PlanRevision: input.PlanRevision}
	env := p.reviewEnvironment(expected)
	var totalBytes int64
	cache := map[string][]byte{}
	env.ReadArtifact = func(tx *store.Tx, id string) ([]byte, error) {
		if data, ok := cache[id]; ok {
			return data, nil
		}
		item, err := findingArtifact(tx, artifacts, id, input.Task, input.Candidate)
		if err != nil {
			return nil, core.Error("REVIEW_EVIDENCE_CHANGED", "attachment must be finalized and bound to the reviewed task and candidate")
		}
		totalBytes += item.Size
		if totalBytes > 64<<20 {
			return nil, core.Error("REVIEW_INPUT_REQUIRED", "review evidence exceeds 64 MiB")
		}
		data, err := artifacts.read(item)
		if err != nil {
			return nil, core.Error("REVIEW_EVIDENCE_CHANGED", "attachment bytes are unavailable")
		}
		cache[id] = data
		return data, nil
	}
	raw, _ := json.Marshal(input)
	key := "remote-review:" + store.Hash([]byte(principal.UserID+"\n"+input.Key))
	hash := store.Hash(raw)
	var readyNext core.Next
	var readyError error
	if input.Action == "review_result" {
		readyNext, readyError = p.service.Next(input.Task)
	}
	var resumeNext core.Next
	var resumeError error
	if input.Action == "review_resume" {
		resumeNext, resumeError = p.service.Next(input.Task)
	}
	return p.mutate(principal, input.Action, input.Key, input.Key, input, func(tx *store.Tx) (map[string]any, error) {
		if input.Action == "review_result" {
			if readyError != nil {
				return nil, readyError
			}
			var selected core.Check
			for _, check := range readyNext.Task.Definition.Checks {
				if check.ID == input.Check {
					selected = check
				}
			}
			if access := core.ReviewAccess(readyNext, selected); !access.CanReview {
				return nil, core.Error("REVIEW_NOT_ALLOWED", access.Reason)
			}
		}
		if input.Comparison != nil && (input.Comparison.Baseline != "" || input.Comparison.Actual != "") {
			var err error
			env.Comparison, err = reviewComparison(tx, artifacts, input.Task, input.Candidate, input.Comparison.Baseline, input.Comparison.Actual)
			if err != nil {
				return nil, err
			}
		}
		if input.NoteID != "" && (input.Action == "review_result" || input.Action == "review_confirm") {
			var raw string
			if err := tx.Row("SELECT body FROM tasks WHERE id=?", input.Task).Scan(&raw); err != nil {
				return nil, err
			}
			var task core.Task
			if err := json.Unmarshal([]byte(raw), &task); err != nil {
				return nil, err
			}
			for _, note := range task.HumanReviewNotes {
				if note.ID != input.NoteID || note.Comparison == nil {
					continue
				}
				comparison, err := reviewComparison(tx, artifacts, input.Task, input.Candidate, note.Comparison.Baseline.ArtifactID, note.Comparison.Actual.ArtifactID)
				if err != nil {
					return nil, err
				}
				expected, _ := json.Marshal(note.Comparison)
				actual, _ := json.Marshal(comparison)
				if string(expected) != string(actual) {
					return nil, core.Error("REVIEW_EVIDENCE_CHANGED", "the saved opinion's comparison input changed")
				}
			}
		}

		review := core.RemoteReviewInput{Task: input.Task, Check: input.Check, Actor: principal.Username, Reason: strings.TrimSpace(input.Reason), Recovery: strings.TrimSpace(input.Recovery), Result: input.Result, Artifacts: input.Artifacts, Expected: expected, NoteID: input.NoteID}
		if input.Action == "review_result" {
			review.StateRevision = core.RemoteRevision(*readyNext.Task)
		}
		if input.Action == "review_resume" {
			if resumeError != nil {
				return nil, resumeError
			}
			if resumeNext.Action != "wait_environment" || resumeNext.Task.Blocker == nil || len(resumeNext.Reasons) != 1 || resumeNext.Reasons[0] != resumeNext.Task.Blocker.Reason {
				return nil, core.Error("REVIEW_RESUME_NOT_ALLOWED", "resolve other task blockers first")
			}
			next, err := p.service.RemoteResumeReviewTx(tx, input.Task, input.Check, expected, resumeNext.Task.Updated, env)
			if err != nil {
				return nil, err
			}
			safe := core.RemoteTaskView(*next.Task)
			next.Task, next.Attempt = &safe, nil
			raw, _ := json.Marshal(next)
			result := map[string]any{}
			err = json.Unmarshal(raw, &result)
			return result, err
		}
		if input.Action != "review_result" {
			note, err := p.service.RemoteReviewNoteTx(tx, review, input.Action, key, hash, env)
			if err != nil {
				return nil, err
			}
			body, _ := json.Marshal(note)
			result := map[string]any{}
			err = json.Unmarshal(body, &result)
			return result, err
		}
		next, err := p.service.RemoteReviewTx(tx, review, key, hash, env)
		if err != nil {
			return nil, err
		}
		safe := core.RemoteTaskView(*next.Task)
		next.Task, next.Attempt = &safe, nil
		body, _ := json.Marshal(next)
		result := map[string]any{}
		err = json.Unmarshal(body, &result)
		result["operation_id"] = input.Key
		return result, err
	})
}

func (h *standaloneHandler) remoteReview(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project string) {
	role, err := h.server.role(principal, project)
	if err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	if r.Method != http.MethodPost {
		standaloneFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	if len(principal.CSRFHash) == 0 || (role != "admin" && role != "reviewer") {
		standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	var input remoteReviewRequest
	if decode(r, &input) != nil {
		standaloneFail(w, http.StatusBadRequest, "REVIEW_INPUT_REQUIRED")
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
	artifacts, err := h.server.artifacts(project, service)
	if err != nil {
		artifactError(w, err)
		return
	}
	result, err := p.reviewRemote(principal, input, artifacts)
	if err != nil {
		reviewError(w, err)
		return
	}
	if err = service.Store.Flush(); err != nil {
		reviewError(w, err)
		return
	}
	standaloneReply(w, http.StatusOK, result)
}

func reviewError(w http.ResponseWriter, err error) {
	code, _, _ := strings.Cut(err.Error(), ":")
	switch code {
	case "REVIEW_RESUME_NOT_ALLOWED", "REVIEW_VERSION_MISMATCH", "REVIEW_VERSION_REQUIRED", "REVIEW_NOTE_VERSION_MISMATCH", "REVIEW_NOTE_ALREADY_CONFIRMED", "REVIEW_EVIDENCE_CHANGED", "REVIEW_NOT_ALLOWED", "REVIEW_CHECK_CONFIRM_REQUIRED":
		standaloneFail(w, http.StatusConflict, code)
	case "REVIEW_INPUT_REQUIRED", "HUMAN_EVIDENCE_REQUIRED", "RECOVERY_REQUIRED", "REVIEW_ACTION_INVALID", "REVIEW_NOTE_MISSING", "REVIEW_NOTE_CHECK_MISMATCH", "MANUAL_CHECK_REQUIRED", "CHECK_MISSING", "IDEMPOTENCY_KEY_REQUIRED":
		standaloneFail(w, http.StatusBadRequest, code)
	default:
		portableError(w, err)
	}
}

// reviewEvidence exposes central object provenance, including unfinished uploads.
// Client filesystem paths and upload credentials never enter this projection.
func (a *artifactStore) reviewEvidence(task string) ([]map[string]any, error) {
	items, err := a.list(task)
	if err != nil {
		return nil, err
	}
	rows, err := a.service.Store.DB.Query("SELECT artifact_id,project_id,task_id,attempt_id,candidate,filename,mime,size,sha256,source,uploader,created,status FROM artifact_uploads WHERE task_id=? AND artifact_id NOT IN (SELECT id FROM artifact_objects) ORDER BY created,artifact_id", task)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		item, e := scanArtifact(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if item.Status == "open" {
			item.Status = "uploading"
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	byID := map[string]map[string]any{}
	for _, item := range items {
		raw, _ := json.Marshal(item)
		value := map[string]any{}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value["observations"] = []map[string]any{}
		byID[item.ID] = value
		result = append(result, value)
	}
	rows, err = a.service.Store.DB.Query("SELECT o.body,f.body FROM finding_occurrences o JOIN findings f ON f.id=o.finding_id WHERE f.task_id=? ORDER BY o.rowid", task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var appearanceRaw, findingRaw string
		if err = rows.Scan(&appearanceRaw, &findingRaw); err != nil {
			return nil, err
		}
		var appearance core.FindingOccurrence
		var finding core.Finding
		if json.Unmarshal([]byte(appearanceRaw), &appearance) != nil || json.Unmarshal([]byte(findingRaw), &finding) != nil {
			return nil, errors.New("FINDING_INVALID")
		}
		observation := map[string]any{"id": appearance.ID, "finding_id": finding.ID, "route": appearance.Input.Route, "location": appearance.Input.Location, "viewport": appearance.Input.Viewport, "environment": appearance.Input.Environment, "report_artifact_id": appearance.ReportArtifactID, "trace_artifacts": appearance.Input.TraceArtifacts}
		for _, evidence := range appearance.Evidence {
			if value := byID[evidence.ID]; value != nil {
				value["observations"] = append(value["observations"].([]map[string]any), observation)
			}
		}
	}
	return result, rows.Err()
}

// An input observation from before successful execution does not supersede its
// accepted output. Later observations of another candidate require reconciliation.
func reviewObservationMatches(task core.Task, candidate, created string) bool {
	if candidate == "" || candidate == task.Candidate {
		return true
	}
	observed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return false
	}
	for _, attempt := range task.Attempts {
		if attempt.Unit == "execute" && attempt.Status == "pass" && attempt.Candidate == task.Candidate && attempt.Ended != nil && !observed.After(*attempt.Ended) {
			return true
		}
	}
	return false
}

func reviewComparison(tx *store.Tx, artifacts *artifactStore, task, candidate, baseline, actual string) (*core.ReviewComparison, error) {
	comparison := &core.ReviewComparison{}
	for index, id := range []string{baseline, actual} {
		if id == "" {
			continue
		}
		item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=? AND (task_id=? OR id IN (SELECT artifact_id FROM artifact_references WHERE task_id=?))", id, task, task))
		if err != nil || item.Status != "finalized" || (item.MIME != "image/png" && item.MIME != "image/jpeg") {
			return nil, core.Error("REVIEW_EVIDENCE_CHANGED", "selected comparison image is unavailable")
		}
		if _, err = artifacts.read(item); err != nil {
			return nil, core.Error("REVIEW_EVIDENCE_CHANGED", "selected comparison image is unavailable")
		}
		image := core.ReviewImage{ArtifactID: id, SHA256: item.SHA256, Candidate: item.Candidate, Source: item.Source, Uploader: item.Uploader}
		if index == 0 {
			comparison.Baseline = image
		} else {
			if item.Candidate != candidate {
				return nil, core.Error("REVIEW_VERSION_MISMATCH", "actual image belongs to another candidate")
			}
			comparison.Actual = image
		}
	}
	return comparison, nil
}

// matchRemoteCandidateTx checks the accepted immutable server candidate and its
// observation order. It makes no claim about unreported client filesystem state.
func matchRemoteCandidateTx(tx *store.Tx, task *core.Task) (bool, error) {
	var present int
	if err := tx.Row("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='portable_candidates'").Scan(&present); err != nil {
		return false, err
	}
	if present == 0 {
		return false, nil
	}
	var body string
	err := tx.Row("SELECT body FROM portable_candidates WHERE id=?", task.Candidate).Scan(&body)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var candidate remote.PortableCandidate
	if json.Unmarshal([]byte(body), &candidate) != nil || candidate.ID != task.Candidate || remote.ValidatePortableCandidate(candidate) != nil {
		return false, errors.New("CANDIDATE_STORAGE_ERROR")
	}
	var observed, created string
	err = tx.Row("SELECT candidate_id,created FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", task.Definition.ID).Scan(&observed, &created)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	return reviewObservationMatches(*task, observed, created), nil
}

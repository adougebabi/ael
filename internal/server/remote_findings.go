package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
)

func findingsSource(input portableCheckInput, check core.Check, principal standalonePrincipal) core.FindingSource {
	source := core.FindingReportSource{Method: core.EffectiveCheckMethod(check)}
	if input.Source != nil {
		source = *input.Source
	}
	return core.FindingSource{FindingReportSource: source, Actor: principal.Username, Attestation: "client_reported"}
}

func findingArtifact(tx *store.Tx, artifacts *artifactStore, id, task, candidate string) (artifactObject, error) {
	item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", id))
	if err != nil || item.Status != "finalized" || item.Candidate != candidate {
		return item, errors.New("FINDING_EVIDENCE_INVALID")
	}
	if item.TaskID != task {
		var references int
		if err = tx.Row("SELECT count(*) FROM artifact_references WHERE artifact_id=? AND task_id=? AND candidate=?", id, task, candidate).Scan(&references); err != nil {
			return item, err
		}
		if references == 0 {
			return item, errors.New("FINDING_EVIDENCE_INVALID")
		}
	}
	return item, nil
}

func findingEvidence(item artifactObject, role string) core.FindingEvidence {
	return core.FindingEvidence{ID: item.ID, Filename: item.Filename, MIME: item.MIME, SHA256: item.SHA256, Role: role, Status: item.Status, Uploader: item.Uploader}
}

func (p *portableStore) recordFindings(tx *store.Tx, principal standalonePrincipal, task core.Task, check core.Check, contextID, reportID string, input portableCheckInput, report artifactObject, artifacts *artifactStore, trustedHuman bool, created string) ([]map[string]any, error) {
	source := findingsSource(input, check, principal)
	if report.ID != "" && report.Candidate != "" && report.Candidate != input.CandidateID {
		return nil, errors.New("FINDING_EVIDENCE_INVALID")
	}
	if !slices.Contains([]string{"command", "scenario", "model", "human"}, source.Method) || (source.Method == "human" && !trustedHuman) {
		return nil, errors.New("FINDING_SOURCE_MISMATCH")
	}
	for _, value := range []string{source.Tool, source.Version, source.RunID} {
		if len(value) > 200 || strings.ContainsAny(value, "\r\n\x00") {
			return nil, errors.New("FINDING_INVALID")
		}
	}
	config, _ := json.Marshal(check)
	configHash := store.Hash(config)
	results := []map[string]any{}
	batch := map[string]bool{}
	cache := map[string]artifactObject{}
	var totalBytes int64
	if report.ID != "" {
		cache[report.ID] = report
		totalBytes = report.Size
	}
	for _, rawInput := range input.Findings {
		findingInput, err := core.NormalizeFinding(rawInput, check.Kind)
		if err != nil {
			return nil, err
		}
		fingerprint := core.FindingFingerprint(task.Definition.ID, check.ID, input.CandidateID, configHash, task.Definition.Revision, task.Plan.Revision, findingInput)
		if batch[fingerprint] {
			return nil, errors.New("FINDING_DUPLICATE")
		}
		batch[fingerprint] = true
		evidence := []core.FindingEvidence{}
		ids := append(slices.Clone(findingInput.Artifacts), findingInput.TraceArtifacts...)
		if report.ID != "" {
			ids = append(ids, report.ID)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		for _, id := range ids {
			item, ok := cache[id]
			if !ok {
				if !standaloneID.MatchString(id) {
					return nil, errors.New("FINDING_EVIDENCE_INVALID")
				}
				item, err = findingArtifact(tx, artifacts, id, task.Definition.ID, input.CandidateID)
				if err != nil {
					return nil, err
				}
				totalBytes += item.Size
				if totalBytes > 64<<20 {
					return nil, errors.New("FINDING_INVALID")
				}
				if _, err = artifacts.read(item); err != nil {
					return nil, errors.New("FINDING_EVIDENCE_INVALID")
				}
				cache[id] = item
			}
			role := "attachment"
			if item.MIME == "image/png" || item.MIME == "image/jpeg" {
				role = "screenshot"
			}
			if slices.Contains(findingInput.TraceArtifacts, id) {
				role = "interaction"
			}
			if id == report.ID {
				role = "report"
			}
			evidence = append(evidence, findingEvidence(item, role))
		}
		var entity core.Finding
		var body string
		err = tx.Row("SELECT body FROM findings WHERE fingerprint=?", fingerprint).Scan(&body)
		if err == nil {
			if json.Unmarshal([]byte(body), &entity) != nil {
				return nil, errors.New("FINDING_INVALID")
			}
		} else if err == sql.ErrNoRows {
			id, err := standaloneRandom("finding_")
			if err != nil {
				return nil, err
			}
			entity = core.Finding{ID: id, TaskID: task.Definition.ID, CheckID: check.ID, Candidate: input.CandidateID, DefinitionRevision: task.Definition.Revision, PlanRevision: task.Plan.Revision, Config: configHash, Fingerprint: fingerprint, Rule: findingInput.Rule, Title: findingInput.Title, Kind: findingInput.Kind, Severity: findingInput.Severity, Route: findingInput.Route, Location: findingInput.Location, Status: "open", FirstSeen: created}
			entity.RecurrenceOf, entity.LineageID, err = findingPredecessor(tx, task, check, configHash, input.CandidateID, findingInput)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
		severities := []string{"unknown", "info", "low", "medium", "high", "critical"}
		if slices.Index(severities, findingInput.Severity) > slices.Index(severities, entity.Severity) {
			entity.Severity = findingInput.Severity
		}
		entity.LastSeen = created
		encoded, err := json.Marshal(entity)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO findings(id,task_id,candidate_id,check_id,fingerprint,body,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,last_seen=excluded.last_seen", entity.ID, entity.TaskID, entity.Candidate, entity.CheckID, entity.Fingerprint, string(encoded), entity.FirstSeen, created); err != nil {
			return nil, err
		}
		occurrenceID, err := standaloneRandom("appearance_")
		if err != nil {
			return nil, err
		}
		missing := core.FindingMissing(findingInput, report.ID, evidence)
		completeness := "complete"
		if len(missing) > 0 {
			completeness = "incomplete"
		}
		occurrence := core.FindingOccurrence{ID: occurrenceID, FindingID: entity.ID, CheckReportID: reportID, ContextID: contextID, ReportArtifactID: report.ID, ReportSHA256: report.SHA256, TaskRevision: input.ExpectedRevision, Input: findingInput, Source: source, Evidence: evidence, Completeness: completeness, Missing: missing, Created: created}
		encoded, err = json.Marshal(occurrence)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO finding_occurrences VALUES(?,?,?,?,?)", occurrenceID, entity.ID, reportID, string(encoded), created); err != nil {
			return nil, err
		}
		var count int
		if err = tx.Row("SELECT count(*) FROM finding_occurrences WHERE finding_id=?", entity.ID).Scan(&count); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{"id": entity.ID, "occurrence_id": occurrenceID, "occurrence_count": count, "completeness": completeness, "missing_evidence": missing})
	}
	return results, nil
}

func (p *portableStore) findingView(id string, artifacts *artifactStore, detail bool, available map[string]string) (map[string]any, error) {
	if !standaloneID.MatchString(id) {
		return nil, errors.New("FINDING_NOT_FOUND")
	}
	var body string
	if err := p.service.Store.DB.QueryRow("SELECT body FROM findings WHERE id=?", id).Scan(&body); err != nil {
		return nil, errors.New("FINDING_NOT_FOUND")
	}
	var entity core.Finding
	if json.Unmarshal([]byte(body), &entity) != nil {
		return nil, errors.New("FINDING_INVALID")
	}
	var view map[string]any
	if json.Unmarshal([]byte(body), &view) != nil {
		return nil, errors.New("FINDING_INVALID")
	}
	rows, err := p.service.Store.DB.Query("SELECT body FROM finding_occurrences WHERE finding_id=? ORDER BY rowid DESC", id)
	if err != nil {
		return nil, err
	}
	occurrences := []core.FindingOccurrence{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var occurrence core.FindingOccurrence
		if err = json.Unmarshal([]byte(raw), &occurrence); err != nil {
			break
		}
		occurrences = append(occurrences, occurrence)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	current := "incomplete"
	rendered := []map[string]any{}
	for _, occurrence := range occurrences {
		evidence := slices.Clone(occurrence.Evidence)
		for i, item := range evidence {
			key := item.ID + ":" + item.SHA256
			state, ok := available[key]
			if !ok {
				object, readErr := artifacts.get(item.ID)
				state = "unavailable"
				if readErr == nil && object.Status == "finalized" && object.SHA256 == item.SHA256 {
					if _, readErr = artifacts.read(object); readErr == nil {
						state = "finalized"
					}
				}
				available[key] = state
			}
			evidence[i].Status = state
		}
		missing := core.FindingMissing(occurrence.Input, occurrence.ReportArtifactID, evidence)
		completeness := "complete"
		if len(missing) > 0 {
			completeness = "incomplete"
		}
		if completeness == "complete" {
			current = "complete"
		}
		if detail {
			raw, _ := json.Marshal(occurrence)
			var item map[string]any
			_ = json.Unmarshal(raw, &item)
			item["current_evidence"], item["current_missing_evidence"], item["current_completeness"] = evidence, missing, completeness
			rendered = append(rendered, item)
		}
	}
	task, err := p.service.Get(entity.TaskID)
	if err != nil {
		return nil, err
	}
	validity := "current"
	var latest, observedAt string
	observedErr := p.service.Store.DB.QueryRow("SELECT candidate_id,created FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", entity.TaskID).Scan(&latest, &observedAt)
	if observedErr != nil && observedErr != sql.ErrNoRows {
		return nil, observedErr
	}
	if task.Candidate != "" && reviewObservationMatches(task, latest, observedAt) {
		latest, observedErr = task.Candidate, nil
	}
	configMatches := false
	for _, check := range task.Definition.Checks {
		raw, _ := json.Marshal(check)
		if check.ID == entity.CheckID && store.Hash(raw) == entity.Config {
			configMatches = true
		}
	}
	if !configMatches || task.Definition.Revision != entity.DefinitionRevision || task.Plan.Revision != entity.PlanRevision || (observedErr == nil && latest != entity.Candidate) {
		validity = "historical"
	} else if observedErr == sql.ErrNoRows {
		validity = "unknown"
	}
	view["task_status"] = task.Status
	view["revision"], view["source_task_revision"] = core.FindingRevision(entity), core.RemoteRevision(task)
	view["occurrence_count"], view["current_completeness"], view["validity"], view["gate_effect"] = len(occurrences), current, validity, "none"
	if detail {
		view["occurrences"] = rendered
	}
	if err = p.findingLifecycleView(entity, view, artifacts, detail, available); err != nil {
		return nil, err
	}
	return view, nil
}

func (h *standaloneHandler) remoteFindings(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project string, parts []string) {
	role, err := h.server.role(principal, project)
	if err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
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
		portableError(w, err)
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && slices.Contains([]string{"decisions", "repair", "revalidations"}, parts[1]) {
		h.mutateFinding(w, r, principal, role, parts[0], parts[1], p, artifacts)
		return
	}
	if r.Method == http.MethodPost && len(parts) == 0 {
		if role != "admin" && role != "executor" && role != "reviewer" {
			standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		var input portableCheckInput
		if decodePortable(r, &input, p) != nil || len(input.Findings) == 0 {
			portableError(w, errors.New("FINDING_INVALID"))
			return
		}
		result, err := p.reportCheck(principal, input.ContextID, input, artifacts, len(principal.CSRFHash) > 0 && (role == "admin" || role == "reviewer"))
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusCreated, result)
		return
	}
	if r.Method != http.MethodGet {
		standaloneFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	if len(parts) == 1 {
		result, err := p.findingView(parts[0], artifacts, true, map[string]string{})
		if err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, result)
		return
	}
	if len(parts) != 0 {
		standaloneFail(w, http.StatusNotFound, "FINDING_NOT_FOUND")
		return
	}
	q := r.URL.Query()
	limit, offset := bounds(r)
	rows, err := service.Store.DB.Query("SELECT id,body FROM findings WHERE (?='' OR task_id=?) AND (?='' OR candidate_id=?) ORDER BY last_seen DESC,id", q.Get("task"), q.Get("task"), q.Get("candidate"), q.Get("candidate"))
	if err != nil {
		portableError(w, err)
		return
	}
	records := []core.Finding{}
	for rows.Next() {
		var id, body string
		if err = rows.Scan(&id, &body); err != nil {
			break
		}
		var entity core.Finding
		if err = json.Unmarshal([]byte(body), &entity); err != nil {
			break
		}
		records = append(records, entity)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		portableError(w, err)
		return
	}
	items := []map[string]any{}
	total := 0
	available := map[string]string{}
	for _, entity := range records {
		if q.Get("severity") != "" && entity.Severity != q.Get("severity") {
			continue
		}
		if q.Get("status") != "" && entity.Status != q.Get("status") {
			continue
		}
		if q.Get("query") != "" && !strings.Contains(strings.ToLower(entity.Title+" "+entity.Rule+" "+entity.Route), strings.ToLower(q.Get("query"))) {
			continue
		}
		selected := total >= offset && len(items) < limit
		if q.Get("completeness") == "" && !selected {
			total++
			continue
		}
		item, err := p.findingView(entity.ID, artifacts, false, available)
		if err != nil {
			portableError(w, err)
			return
		}
		if q.Get("completeness") != "" && item["current_completeness"] != q.Get("completeness") {
			continue
		}
		if total >= offset && len(items) < limit {
			items = append(items, item)
		}
		total++
	}

	standaloneReply(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
)

func dashboardNext(service *core.Service, id string) (map[string]any, error) {
	next, err := service.Next(id)
	if err != nil {
		return nil, err
	}
	summary, err := service.Summarize(next)
	if err != nil {
		return nil, err
	}
	activity, truncated, err := service.TaskActivity(id)
	if err != nil {
		return nil, err
	}
	original := *next.Task
	revision := core.RemoteRevision(original)
	safe := core.RemoteTaskView(*next.Task)
	next.Task = &safe
	next.Attempt = nil
	raw, _ := json.Marshal(next)
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	checks := map[string]*core.Evidence{}
	access := map[string]core.CheckAccess{}
	for _, check := range safe.Definition.Checks {
		checks[check.ID] = core.CurrentEvidence(&safe, check)
		access[check.ID] = core.CheckAccess{EffectiveMethod: core.EffectiveCheckMethod(check)}
	}
	result["summary"], result["current_checks"], result["current_runs"], result["check_access"] = summary, checks, summary.CurrentRuns, access
	result["activity"], result["activity_truncated"], result["revision"] = activity, truncated, revision
	labels, err := dashboardClientLabels(service)
	if err != nil {
		return nil, err
	}
	result["collaboration"] = dashboardResponsibilities(original, labels)
	return result, nil
}

func dashboardResponsibilities(task core.Task, labels map[string]map[string]string) []map[string]any {
	items := []map[string]any{}
	for _, attempt := range task.Attempts {
		state, authority := remoteResponsibilityState(task, attempt)
		workspace := ""
		prefix := "remote:" + attempt.Executor + "/"
		if strings.HasPrefix(attempt.Workspace, prefix) {
			workspace = strings.TrimPrefix(attempt.Workspace, prefix)
		}
		identity := labels[attempt.Executor]
		alias := identity["alias"]
		if alias == "" {
			alias = attempt.Executor
		}
		items = append(items, map[string]any{"alias": alias, "machine": identity["machine"], "checkout": identity["checkout"], "client_version": identity["version"], "workspace": workspace, "runner": attempt.Executor, "attempt": attempt.ID, "unit": attempt.Unit, "candidate": attempt.Input, "last_contact": attempt.LastContact, "lease_until": attempt.Lease, "stage": attempt.Stage, "result": attempt.Status, "connection_state": state, "responsibility_state": authority, "host_execution_state": "unknown"})
	}
	return items
}

func dashboardKnowledge(service *core.Service, artifacts *artifactStore, allVersions bool) ([]knowledge.Record, error) {
	query := "SELECT s.id,s.path,s.artifact_id,s.sha256,s.status,s.kind,c.task_id FROM portable_context_sources s JOIN portable_contexts c ON c.id=s.context_id"
	if !allVersions {
		query += " WHERE c.rowid IN (SELECT max(rowid) FROM portable_contexts GROUP BY task_id)"
	}
	query += " ORDER BY s.rowid DESC"
	rows, err := service.Store.DB.Query(query)
	if err != nil {
		return nil, err
	}
	type source struct{ id, path, artifact, hash, status, kind, task string }
	sources := []source{}
	for rows.Next() {
		var s source
		if err = rows.Scan(&s.id, &s.path, &s.artifact, &s.hash, &s.status, &s.kind, &s.task); err != nil {
			break
		}
		sources = append(sources, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	records := []knowledge.Record{}
	seen := map[string]int{}
	hashes := map[string]string{}
	for _, source := range sources {
		key := source.id + "\n" + source.hash
		if index, ok := seen[key]; ok {
			if !slices.Contains(records[index].Tasks, source.task) {
				records[index].Tasks = append(records[index].Tasks, source.task)
			}
			continue
		}
		item, err := artifacts.get(source.artifact)
		if err != nil || item.Status != "finalized" || item.SHA256 != source.hash {
			return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
		}
		content, err := artifacts.read(item)
		if err != nil {
			return nil, err
		}
		record, err := knowledge.ParseSnapshot(source.path, content)
		if err != nil {
			return nil, err
		}
		record.ID, record.Hash, record.Kind, record.Status = source.id, source.hash, source.kind, source.status
		record.Title = knowledge.Title(record)
		if record.Source == "" {
			record.Source = source.path
		}
		if !slices.Contains(record.Tasks, source.task) {
			record.Tasks = append(record.Tasks, source.task)
		}
		if prior, exists := hashes[source.id]; exists && prior != source.hash && !allVersions {
			record.Status = "conflict"
			record.Reason = "多个任务引用不同版本，请从任务依据查看准确快照"
			for i := range records {
				if records[i].ID == source.id {
					records[i].Status = "conflict"
					records[i].Reason = record.Reason
				}
			}
		}
		hashes[source.id] = source.hash
		seen[key] = len(records)
		records = append(records, record)
	}
	return records, nil
}

func (h *standaloneHandler) remoteDashboard(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project, view string) {
	role, err := h.server.role(principal, project)
	if err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	if r.Method != http.MethodGet {
		standaloneFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	service, err := h.server.projectService(project)
	if err != nil {
		portableError(w, err)
		return
	}
	if _, err = h.server.portable(project, service); err != nil {
		portableError(w, err)
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if id == "" && (view == "artifact" || view == "artifact-status") {
		id = q.Get("task")
	}
	limit, offset := bounds(r)
	var result any
	switch view {
	case "sync":
		var cursor int64
		if err = service.Store.DB.QueryRow("SELECT coalesce(max(id),0) FROM events").Scan(&cursor); err == nil {
			watermark := h.server.dashboardEpoch + ":" + project + ":" + strconv.FormatInt(cursor, 10)
			result = map[string]any{"project_id": project, "epoch": h.server.dashboardEpoch, "cursor": watermark, "changed": q.Get("cursor") != watermark, "role": role}
		}
	case "info":
		var name string
		err = h.server.store.DB.QueryRow("SELECT name FROM server_projects WHERE id=?", project).Scan(&name)
		result = map[string]any{"project": "remote:" + project + ":" + principal.UserID + ":" + hex.EncodeToString(principal.CSRFHash), "project_name": name, "project_id": project, "remote": true, "role": role, "config": map[string]string{"mode": "remote"}, "service_version": bootstrap.Version, "service_build": bootstrap.Build, "knowledge_health": map[string]any{"sources": []any{}, "state": "central_snapshots"}}
	case "tasks":
		days, _ := strconv.Atoi(q.Get("days"))
		offsets := map[string]int{}
		for _, board := range []string{"active", "pending", "confirmation", "completed", "cancelled"} {
			value, _ := strconv.Atoi(q.Get("offset_" + board))
			offsets[board] = max(0, value)
		}
		page, e := service.QueryPage(core.Query{Limit: limit, Offset: offset, Offsets: offsets, Days: days, Text: q.Get("q"), Type: q.Get("type"), Board: q.Get("board"), Parent: q.Get("parent")})
		err = e
		labels, e := dashboardClientLabels(service)
		if e != nil {
			err = e
		}
		items := []map[string]any{}
		if err == nil {
			for _, next := range page.Items {
				summary, e := service.Summarize(next)
				if e != nil {
					err = e
					break
				}
				raw, _ := json.Marshal(summary)
				var item map[string]any
				_ = json.Unmarshal(raw, &item)
				item["collaboration"] = dashboardResponsibilities(*next.Task, labels)
				items = append(items, item)
			}
		}
		result = map[string]any{"items": items, "counts": page.Counts, "total": page.Total, "limit": page.Limit, "offset": page.Offset, "offsets": page.Offsets}
	case "task":
		result, err = dashboardNext(service, id)
	case "task-tree":
		tree, e := service.Tree(id)
		err = e
		if err == nil {
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
		}
		result = tree
	case "attention":
		result, err = service.Attention()
	case "events":
		result, err = service.Events(id)
	case "artifact", "artifact-status":
		artifacts, e := h.server.artifacts(project, service)
		err = e
		if err == nil {
			item, e := artifacts.get(q.Get("ref"))
			err = e
			if err == nil && item.TaskID != id {
				var references int
				if queryErr := service.Store.DB.QueryRow("SELECT count(*) FROM artifact_references WHERE artifact_id=? AND task_id=?", item.ID, id).Scan(&references); queryErr != nil {
					err = queryErr
				} else if references == 0 {
					err = errors.New("ARTIFACT_NOT_FOUND")
				}
			}
			if err == nil && view == "artifact-status" {
				status := item.Status
				if status == "finalized" {
					if _, readErr := artifacts.read(item); readErr != nil {
						status = "unavailable"
					}
				}
				result = map[string]any{"task_id": id, "status": status, "sha256": item.SHA256}
				break
			}
			if err == nil {
				data, e := artifacts.read(item)
				err = e
				object := map[string]any{"ref": item.ID, "body": "", "image": "", "sha256": item.SHA256, "download": "/api/v1/projects/" + url.PathEscape(project) + "/artifacts/" + url.PathEscape(item.ID) + "/content"}
				if strings.HasPrefix(item.MIME, "image/") {
					object["image"] = "/api/v1/projects/" + url.PathEscape(project) + "/artifacts/" + url.PathEscape(item.ID) + "/content"
				} else if item.MIME == "text/plain" {
					object["body"] = string(data)
				} else {
					object["body"] = "此格式请下载原件查看。"
				}
				result = object
			}
		}
	case "knowledge-page", "knowledge-record", "task-knowledge", "knowledge-link", "context", "evolution":
		artifacts, e := h.server.artifacts(project, service)
		err = e
		if err != nil {
			break
		}
		all, e := dashboardKnowledge(service, artifacts, (view == "knowledge-record" && q.Get("version") != "") || view == "task-knowledge")
		err = e
		if err != nil {
			break
		}
		switch view {
		case "knowledge-page":
			unique := []knowledge.Record{}
			seen := map[string]bool{}
			for _, record := range all {
				if !seen[record.ID] {
					seen[record.ID] = true
					unique = append(unique, record)
				}
			}
			result = knowledge.BrowseRecords(unique, knowledge.BrowseQuery{Text: q.Get("q"), View: q.Get("view"), Scope: q.Get("scope"), Limit: limit, Offset: offset})
		case "knowledge-record":
			err = errors.New("KNOWLEDGE_NOT_FOUND")
			versions := []map[string]string{}
			for _, record := range all {
				if record.ID == id {
					versions = append(versions, map[string]string{"hash": record.Hash, "status": record.Status})
				}
			}
			for _, record := range all {
				if record.ID != id || (q.Get("version") != "" && q.Get("version") != record.Hash) {
					continue
				}
				raw, _ := json.Marshal(record)
				item := map[string]any{}
				_ = json.Unmarshal(raw, &item)
				links := []map[string]any{}
				for _, taskID := range record.Tasks {
					var title string
					available := service.Store.DB.QueryRow("SELECT json_extract(body,'$.definition.title') FROM tasks WHERE id=?", taskID).Scan(&title) == nil
					links = append(links, map[string]any{"id": taskID, "title": title, "available": available})
				}
				contextID, e := dashboardKnowledgeContext(service, record.ID, record.Hash, q.Get("context"), q.Get("task"))
				if e != nil {
					err = e
					break
				}
				item["context_id"] = contextID
				item["related_tasks"] = links
				if q.Get("version") == "" && len(versions) > 1 {
					item["body"] = ""
					item["status"] = "conflict"
					item["reason"] = "多个任务引用不同版本，请选择准确快照"
					item["versions"] = versions
				}
				result = item
				err = nil
				break
			}
		case "knowledge-link":
			contextID, e := dashboardKnowledgeContext(service, id, q.Get("version"), q.Get("context"), q.Get("task"))
			err = e
			if err != nil {
				break
			}
			var sourcePath string
			err = service.Store.DB.QueryRow("SELECT path FROM portable_context_sources WHERE context_id=? AND id=?", contextID, id).Scan(&sourcePath)
			link, e := url.Parse(q.Get("href"))
			if err != nil || e != nil || link.IsAbs() || link.Host != "" {
				err = errors.New("KNOWLEDGE_LINK_UNAVAILABLE")
				break
			}
			target := sourcePath
			if link.Path != "" {
				target = path.Clean(path.Join(path.Dir(sourcePath), link.Path))
			}
			var targetID, version string
			err = service.Store.DB.QueryRow("SELECT id,sha256 FROM portable_context_sources WHERE context_id=? AND path=?", contextID, target).Scan(&targetID, &version)
			if err != nil {
				err = errors.New("KNOWLEDGE_LINK_UNAVAILABLE")
				break
			}
			result = map[string]string{"id": targetID, "version": version, "context": contextID, "fragment": link.Fragment}
		case "task-knowledge":
			task, e := service.Get(id)
			err = e
			records := []knowledge.Record{}
			if err == nil {
				for _, ref := range task.Basis {
					found := false
					for _, record := range all {
						if record.ID == ref.ID && record.Hash == ref.Version {
							record.Body = ""
							records = append(records, record)
							found = true
							break
						}
					}
					if !found {
						records = append(records, knowledge.Record{ID: ref.ID, Hash: ref.Version, Path: ref.Source, Status: ref.Status, Reason: "原始依据不可用"})
					}
				}
			}
			result = map[string]any{"records": records, "health": map[string]any{"state": "central_snapshots", "sources": []any{}}, "supplied_versions": task.Basis}
		case "context":
			p, _ := h.server.portable(project, service)
			var contextID string
			err = service.Store.DB.QueryRow("SELECT id FROM portable_contexts WHERE task_id=? ORDER BY rowid DESC LIMIT 1", id).Scan(&contextID)
			if err == nil {
				result, err = p.getContext(contextID, artifacts)
			}
		case "evolution":
			query := knowledge.BrowseQuery{Text: q.Get("q"), Scope: q.Get("scope"), Limit: limit, Offset: offset}
			page, e := knowledge.ChangesWithRecords(service, all, query, q.Get("kind"), q.Get("from"), q.Get("to"))
			err = e
			result = page
			if id != "" && err == nil {
				err = errors.New("CHANGE_NOT_FOUND")
				for start := 0; start < page.Total; start += 100 {
					items, e := knowledge.ChangesWithRecords(service, all, knowledge.BrowseQuery{Limit: 100, Offset: start}, "", "", "")
					if e != nil {
						err = e
						break
					}
					for _, item := range items.Items {
						if item.ID == id {
							result = item
							err = nil
							break
						}
					}
					if err == nil {
						break
					}
				}
			}
		}
	default:
		standaloneFail(w, http.StatusNotFound, "DASHBOARD_VIEW_NOT_FOUND")
		return
	}
	if err != nil {
		standaloneTaskError(w, err)
		return
	}
	// Project authorization is checked on every read; no client path is opened.
	standaloneReply(w, http.StatusOK, result)
}

func dashboardClientLabels(service *core.Service) (map[string]map[string]string, error) {
	rows, err := service.Store.DB.Query("SELECT r.id,r.checkout_id,r.version,coalesce(l.alias,''),coalesce(l.machine,'') FROM remote_runners r LEFT JOIN remote_runner_labels l ON l.runner_id=r.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := map[string]map[string]string{}
	for rows.Next() {
		var id, checkout, version, alias, machine string
		if err := rows.Scan(&id, &checkout, &version, &alias, &machine); err != nil {
			return nil, err
		}
		labels[id] = map[string]string{"alias": alias, "machine": machine, "checkout": checkout, "version": version}
	}
	return labels, rows.Err()
}

func dashboardKnowledgeContext(service *core.Service, id, version, contextID, task string) (string, error) {
	query := "SELECT s.context_id FROM portable_context_sources s JOIN portable_contexts c ON c.id=s.context_id WHERE s.id=? AND (?='' OR s.sha256=?) AND (?='' OR s.context_id=?) AND (?='' OR c.task_id=?) ORDER BY CASE WHEN c.id=(SELECT context_id FROM remote_runner_attempts WHERE attempt_id=(SELECT json_extract(body,'$.context_attempt') FROM tasks WHERE id=?)) THEN 0 ELSE 1 END,c.rowid DESC LIMIT 1"
	var selected string
	err := service.Store.DB.QueryRow(query, id, version, version, contextID, contextID, task, task, task).Scan(&selected)
	if err != nil {
		return "", errors.New("KNOWLEDGE_LINK_UNAVAILABLE")
	}
	return selected, nil
}

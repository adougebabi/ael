package server

import (
	"ael/internal/core"
	"ael/internal/execution"
	"ael/internal/knowledge"
	"ael/internal/migration"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func (h *Handler) executorProbe(w http.ResponseWriter, r *http.Request) {
	v, e := execution.ProbeCodex()
	if e != nil {
		reply(w, map[string]any{"state": "unavailable", "detail": e.Error(), "reason": "受管执行器接口探测失败；尚未验证登录、模型或实际执行能力。"}, nil)
		return
	}
	reply(w, map[string]any{"state": "interface_available", "interface": v, "reason": "命令接口可调用；登录、模型权限及实际执行能力仍需真实任务验证。"}, nil)
}

type knowledgeTaskLink struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Available bool   `json:"available"`
}

func (h *Handler) knowledgeRecord(w http.ResponseWriter, r *http.Request) {
	record, e := knowledge.Read(h.Service, r.URL.Query().Get("id"), r.URL.Query().Get("version"))
	if e != nil {
		reply(w, nil, e)
		return
	}
	links := []knowledgeTaskLink{}
	titles := map[string]string{}
	if len(record.Tasks) > 0 {
		rows, e := h.Service.Store.DB.Query("SELECT id,json_extract(body,'$.definition.title') FROM tasks")
		if e != nil {
			reply(w, nil, e)
			return
		}
		for rows.Next() {
			var id, title string
			if e = rows.Scan(&id, &title); e != nil {
				rows.Close()
				reply(w, nil, e)
				return
			}
			titles[id] = title
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			reply(w, nil, e)
			return
		}
	}
	for _, id := range record.Tasks {
		title, ok := titles[id]
		links = append(links, knowledgeTaskLink{id, title, ok})
	}
	reply(w, struct {
		knowledge.Record
		Links []knowledgeTaskLink `json:"related_tasks"`
	}{record, links}, nil)
}

type warningItem struct {
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Source   string `json:"source"`
	Raw      string `json:"raw"`
	Action   string `json:"action"`
}

func warning(raw string, src migration.Source, status string) warningItem {
	w := warningItem{Category: "unknown", Kind: "other", Title: "影响尚未确认", Source: src.Path, Raw: raw, Action: "查看来源和原文，核实是否被当前任务引用。"}
	switch {
	case src.Action == "pending" || strings.Contains(raw, "conflict") || strings.Contains(raw, "ambiguous") || strings.Contains(raw, "duplicate"):
		w.Category = "conflicts"
		w.Kind = "conflict"
		w.Title = "资料或映射存在待核实冲突"
		w.Action = "审阅原资料和当前任务依据；保留冲突状态，不能直接当成现行规范。"
	case status == "historical" || status == "superseded" || strings.Contains(raw, "raw historical evidence") || strings.Contains(raw, "cache explicitly excluded"):
		w.Category = "history"
		w.Title = "历史资料需核对"
		w.Action = "按需检查原资料，不将导入记录当成当前验收证据。"
	}
	if strings.Contains(raw, "unresolved link:") {
		w.Kind = "links"
		w.Title = "文档引用尚未解析"
	}
	if strings.Contains(raw, "broken") || strings.Contains(raw, "missing context") {
		w.Kind = "integrity"
		w.Title = "原资料结构或上下文引用不完整"
	}
	if strings.Contains(raw, "never execute") || strings.Contains(raw, "legacy managed entry") {
		w.Kind = "integration"
		w.Title = "旧集成保留待审阅"
		w.Action = "旧入口不会自动执行；切换继续使用原 CLI 授权边界。"
	}
	if strings.Contains(raw, "inherited contract baseline") {
		w.Kind = "baseline"
		w.Title = "继承规范未证明实现合规"
		w.Action = "需要在实际任务中验收相关实现；继承记录本身不等于验证通过。"
	}
	return w
}
func classifiedWarning(raw string, src migration.Source, status string, blocking bool) warningItem {
	w := warning(raw, src, status)
	if blocking {
		w.Category = "current"
		w.Title = "当前任务被此规范冲突阻断"
		w.Action = "打开当前受影响任务，核实适用规范与解除条件。"
	}
	return w
}
func (h *Handler) currentIssues() ([]map[string]any, error) {
	issues := []map[string]any{}
	rows, e := h.Service.Store.DB.Query("SELECT id FROM tasks WHERE status='open' ORDER BY id")
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, id := range ids {
		n, err := h.Service.Next(id)
		if err != nil {
			return nil, err
		}
		if len(n.Reasons) > 0 {
			blocker, recovery := "", ""
			if n.Task.Blocker != nil {
				blocker = n.Task.Blocker.Reason
				recovery = n.Task.Blocker.Recovery
			}
			issues = append(issues, map[string]any{"id": id, "title": n.Task.Definition.Title, "reasons": n.Reasons, "action": n.Action, "blocker_reason": blocker, "recovery": recovery})
		}
	}
	return issues, nil
}
func (h *Handler) projectStatus(w http.ResponseWriter, r *http.Request) {
	health := knowledge.Status(h.Service.Root)
	health.Sources = nil
	issues, e := h.currentIssues()
	if e != nil {
		reply(w, nil, e)
		return
	}
	runs, e := migration.List(h.Service)
	if e != nil {
		reply(w, nil, e)
		return
	}
	applied := 0
	for _, run := range runs {
		if run.State == "applied" {
			applied++
		}
	}
	reply(w, map[string]any{"knowledge": health, "workflow_active": h.Service.Active, "applied_migrations": applied, "issues": issues, "executor": map[string]string{"state": "not_probed", "reason": "尚未探测受管执行器；Attached 协议可用不代表已存在在线执行者。"}}, nil)
}
func bounds(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
func (h *Handler) migrationView(w http.ResponseWriter, r *http.Request) {
	runs, e := migration.List(h.Service)
	if e != nil {
		reply(w, nil, e)
		return
	}
	var preview migration.Plan
	var raw string
	if h.Service.Store.DB.QueryRow("SELECT body FROM metadata WHERE key='migration-preview'").Scan(&raw) == nil {
		if e = json.Unmarshal([]byte(raw), &preview); e != nil {
			reply(w, nil, e)
			return
		}
	}
	if r.URL.Query().Get("id") == "preview" {
		runs = []migration.Run{{ID: "preview", State: "preview", Count: len(preview.Sources), Plan: preview}}
	}
	statuses := map[string]string{}
	blockedTargets := map[string]bool{}
	issues, issueErr := h.currentIssues()
	if issueErr != nil {
		reply(w, nil, issueErr)
		return
	}
	if all, err := knowledge.ReadAll(h.Service.Root); err == nil {
		for _, k := range all {
			statuses[k.Path] = k.Status
			for _, issue := range issues {
				for _, reason := range issue["reasons"].([]string) {
					if strings.TrimPrefix(reason, "unresolved knowledge conflict: ") == k.ID {
						blockedTargets[k.Path] = true
					}
				}
			}
		}
	}
	summaries := []map[string]any{}
	for _, run := range runs {
		counts := map[string]int{"sources": run.Count, "applied": len(run.Applied), "imported": 0, "pending": 0, "ignored": 0, "preserved": 0}
		items := []warningItem{}
		seen := map[string]bool{}
		for _, src := range run.Plan.Sources {
			if slices.Contains(run.Applied, src.ID) && src.Target != "" && src.Action != "ignore" && src.Action != "preserve" {
				counts["imported"]++
			}
			if src.Action == "pending" {
				counts["pending"]++
			}
			if src.Action == "ignore" {
				counts["ignored"]++
			}
			if src.Action == "preserve" {
				counts["preserved"]++
			}
			for _, risk := range src.Risks {
				key := src.Path + "|" + risk
				if !seen[key] {
					seen[key] = true
					items = append(items, classifiedWarning(risk, src, statuses[src.Target], blockedTargets[src.Target]))
				}
			}
		}
		for _, warn := range run.Warnings {
			src := migration.Source{}
			for _, x := range run.Plan.Sources {
				if strings.Contains(warn, x.Path+" ->") {
					src = x
					break
				}
			}
			key := src.Path + "|" + warn
			if !seen[key] {
				seen[key] = true
				items = append(items, classifiedWarning(warn, src, statuses[src.Target], blockedTargets[src.Target]))
			}
		}
		groups := []map[string]any{}
		for _, category := range []string{"current", "conflicts", "history", "unknown"} {
			kinds := map[string]int{}
			for _, item := range items {
				if item.Category == category {
					kinds[item.Kind]++
				}
			}
			count := 0
			for _, n := range kinds {
				count += n
			}
			groups = append(groups, map[string]any{"category": category, "count": count, "kinds": kinds})
		}
		if run.ID == r.URL.Query().Get("id") {
			if sourceID := r.URL.Query().Get("source"); sourceID != "" {
				for _, src := range run.Plan.Sources {
					if src.ID == sourceID {
						reply(w, src, nil)
						return
					}
				}
				reply(w, nil, core.Error("SOURCE_NOT_FOUND", sourceID))
				return
			}
			limit, offset := bounds(r)
			category := r.URL.Query().Get("category")
			filtered := []warningItem{}
			for _, item := range items {
				if category == "" || item.Category == category {
					filtered = append(filtered, item)
				}
			}
			page := []warningItem{}
			if offset < len(filtered) {
				page = filtered[offset:min(offset+limit, len(filtered))]
			}
			sources := []migration.Source{}
			for i := offset; i < min(offset+limit, len(run.Plan.Sources)); i++ {
				src := run.Plan.Sources[i]
				src.Body = ""
				sources = append(sources, src)
			}
			reply(w, map[string]any{"id": run.ID, "state": run.State, "counts": counts, "groups": groups, "warnings": page, "warning_total": len(filtered), "sources": sources, "source_total": len(run.Plan.Sources), "digest": run.Plan.Digest, "created": run.Plan.Created, "activated": run.Activated, "limit": limit, "offset": offset}, nil)
			return
		}
		summaries = append(summaries, map[string]any{"id": run.ID, "state": run.State, "counts": counts, "groups": groups, "created": run.Plan.Created, "activated": run.Activated})
	}
	if r.URL.Query().Get("id") != "" {
		reply(w, nil, core.Error("MIGRATION_NOT_FOUND", r.URL.Query().Get("id")))
		return
	}
	slices.Reverse(summaries)
	reply(w, map[string]any{"runs": summaries, "preview": map[string]any{"available": preview.ID != "", "count": len(preview.Sources), "digest": preview.Digest}}, nil)
}

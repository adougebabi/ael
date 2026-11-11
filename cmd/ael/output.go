package main

import (
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// textNext adds read-only event summaries without changing the JSON protocol.
type textNext struct {
	Next    core.Next
	Summary *core.TaskSummary
	Command string
}

func (n textNext) MarshalJSON() ([]byte, error) { return json.Marshal(n.Next) }

type textPage struct {
	Page      core.Page
	Summaries map[string]*core.TaskSummary
}

type planPreparationOutput struct {
	core.PlanPreparation
	Schema json.RawMessage `json:"schema"`
}

func (p textPage) MarshalJSON() ([]byte, error) { return json.Marshal(p.Page) }

func withTextSummaries(svc *core.Service, value any, command string) any {
	summary := func(n core.Next) *core.TaskSummary {
		if n.Task == nil {
			return nil
		}
		s, err := svc.Summarize(n)
		if err != nil {
			return nil
		}
		return &s
	}
	switch v := value.(type) {
	case core.Next:
		return textNext{Next: v, Summary: summary(v), Command: command}
	case core.Page:
		p := textPage{Page: v, Summaries: make(map[string]*core.TaskSummary)}
		for _, n := range v.Items {
			if n.Task != nil {
				p.Summaries[n.Task.Definition.ID] = summary(n)
			}
		}
		return p
	default:
		return value
	}
}

func writeOutput(w io.Writer, value any, machine, verbose bool) error {
	if machine {
		return json.NewEncoder(w).Encode(value)
	}
	switch v := value.(type) {
	case planPreparationOutput:
		fmt.Fprintf(w, "规划输入：%s\n父任务：%s (%s)\n已有叶子：%d\n", v.Input.Request, v.Parent.Title, v.Parent.ID, len(v.ExistingLeaves))
		fmt.Fprintln(w, "由 Agent 或用户按需求生成提议；使用 --json 读取完整输入和提议 schema。")
		return nil
	case core.PlanPreview:
		fmt.Fprintf(w, "计划预览（摘要）：%s\n", v.Digest)
		if v.AlreadyApplied {
			fmt.Fprintln(w, "该计划已应用；任务可能仍在进行。")
		}
		for _, change := range v.Changes {
			fmt.Fprintf(w, "  %s → %s：%s", change.Key, change.TaskID, change.Action)
			if change.ExistingStatus != "" {
				fmt.Fprintf(w, "；现有状态：%s", change.ExistingStatus)
			}
			if change.ExistingHistorical {
				fmt.Fprint(w, "；历史记录尚未在此工作区复验")
			}
			if len(change.Fields) > 0 {
				fmt.Fprintf(w, "；变更字段：%s", strings.Join(change.Fields, ", "))
			}
			if change.RequiresConfirmation {
				fmt.Fprint(w, "；范围变化需确认")
			}
			fmt.Fprintln(w)
			fmt.Fprintf(w, "    标题：%s；目标：%s；顺序：%d\n", change.Title, change.Goal, change.Order)
			fmt.Fprintf(w, "    范围：%s；产物：%s\n", strings.Join(change.Scope, ", "), strings.Join(change.Deliverables, ", "))
			fmt.Fprintf(w, "    验收：%s；依赖：%s\n", strings.Join(change.Acceptance, ", "), strings.Join(change.Dependencies, ", "))
			for _, diff := range change.Diff {
				fmt.Fprintf(w, "    %s：%v → %v\n", diff.Field, diff.Before, diff.After)
			}
		}
		for _, mapping := range v.Coverage {
			fmt.Fprintf(w, "  验收 %s：子任务 %s；集成检查 %s\n", mapping.Acceptance, strings.Join(mapping.Leaves, ", "), mapping.Integration)
		}
		for _, warning := range v.Warnings {
			fmt.Fprintf(w, "提示：%s\n", warning)
		}
		return nil
	case core.PlanApplyResult:
		fmt.Fprintf(w, "计划 %s：%s；父任务 %s\n", v.PlanID, v.Status, v.Parent)
		for _, change := range v.Changes {
			fmt.Fprintf(w, "  %s → %s：%s", change.Key, change.TaskID, change.Action)
			if change.RequiresConfirmation {
				fmt.Fprint(w, "；范围变化待确认")
			}
			fmt.Fprintln(w)
		}
		return nil
	case textNext:
		return writeNext(w, v.Next, v.Summary, verbose, v.Command == "task plan")
	case core.Next:
		return writeNext(w, v, nil, verbose, false)
	case core.EpicTree:
		fmt.Fprintf(w, "任务树：必需子任务 %d / %d 已完成\n", v.RequiredCompleted, v.Required)
		if err := writeNext(w, v.Root, nil, verbose, false); err != nil {
			return err
		}
		for _, child := range v.Children {
			kind := "可选"
			if child.Required {
				kind = "必需"
			}
			fmt.Fprintf(w, "\n子任务（%s，里程碑：%s）\n", kind, child.Milestone)
			if err := writeNext(w, child.Task, nil, verbose, false); err != nil {
				return err
			}
		}
		return nil
	case textPage:
		fmt.Fprintf(w, "任务总数：%d（本页 %d）\n", v.Page.Total, len(v.Page.Items))
		for _, n := range v.Page.Items {
			fmt.Fprintln(w)
			var summary *core.TaskSummary
			if n.Task != nil {
				summary = v.Summaries[n.Task.Definition.ID]
			}
			if err := writeNext(w, n, summary, verbose, false); err != nil {
				return err
			}
		}
		return nil
	}
	// Preserve arbitrary existing command results as readable field/value text.
	// No width padding or truncation is used, so CJK titles and IDs remain intact.
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&decoded); err != nil {
		return err
	}
	if object, ok := decoded.(map[string]any); ok {
		if commands, help := object["commands"]; help {
			fmt.Fprintln(w, "AEL 命令（详细机器结构：ael help --json）")
			writeTextValue(w, "", commands, 0)
			fmt.Fprintln(w, "选项：--project PATH  --json  --verbose  --no-color")
			fmt.Fprintln(w, "退出码 0 表示命令执行成功；任务或检查结果仍须查看状态。错误写入 stderr，退出码为 1。")
			return nil
		}
	}
	writeTextValue(w, "", decoded, 0)
	return nil
}

func writeNext(w io.Writer, n core.Next, summary *core.TaskSummary, verbose, showPlan bool) error {
	if n.Task == nil {
		_, err := fmt.Fprintf(w, "当前责任：%s\n下一动作：%s\n", n.Step, n.Action)
		return err
	}
	t := n.Task
	parent := t.Definition.Parent
	if t.EpicMembership != nil {
		parent = t.EpicMembership.Parent
	}
	if parent == "" {
		parent = "无"
	}
	fmt.Fprintf(w, "任务：%s\nID：%s\n父任务：%s\n状态：%s / %s\n当前责任：%s\n", t.Definition.Title, t.Definition.ID, parent, t.Status, n.Board, n.Step)
	if summary != nil && len(summary.Executors) > 0 {
		fmt.Fprintf(w, "执行者：%s\n", strings.Join(summary.Executors, ", "))
	} else {
		fmt.Fprintln(w, "执行者：当前无人执行")
	}
	if n.Attempt != nil {
		fmt.Fprintf(w, "执行记录：%s (%s)\n", n.Attempt.ID, n.Attempt.Executor)
		if n.Attempt.Token != "" {
			fmt.Fprintf(w, "执行令牌：%s\n", n.Attempt.Token)
		}
	}
	if n.KeeperPID != 0 {
		fmt.Fprintf(w, "续约器 PID：%d\n", n.KeeperPID)
	}
	if summary != nil {
		fmt.Fprintf(w, "实际成果：%s\n", summary.LastResult)
	} else if t.Result != "" {
		fmt.Fprintf(w, "实际成果：%s\n", t.Result)
	} else {
		fmt.Fprintln(w, "实际成果：暂无可用摘要")
	}
	fmt.Fprintln(w, "必要检查：")
	count := 0
	for _, check := range t.Definition.Checks {
		if !check.Required {
			continue
		}
		count++
		state := "not_run"
		if e := core.CurrentEvidence(t, check); e != nil {
			state = e.Result
		} else if summary != nil && len(summary.CurrentRuns[check.ID].Executors) > 0 {
			state = "running"
		}
		fmt.Fprintf(w, "  - %s: %s\n", check.ID, state)
	}
	if count == 0 {
		fmt.Fprintln(w, "  无")
	}
	fmt.Fprintf(w, "下一动作：%s\n", n.Action)
	if summary != nil && summary.Next != "" {
		fmt.Fprintf(w, "已记录的下一步：%s\n", summary.Next)
	}
	for _, reason := range n.Reasons {
		fmt.Fprintf(w, "原因：%s\n", reason)
	}
	if t.Status == "open" && t.Blocker != nil {
		fmt.Fprintf(w, "阻塞原因：%s\n恢复条件：%s\n", t.Blocker.Reason, t.Blocker.Recovery)
	}
	if showPlan || verbose {
		fmt.Fprintf(w, "计划预设：%s\n计划步骤：%s\n计划原因：%s\n计划已批准：%t\n", t.Plan.Preset, strings.Join(t.Plan.Steps, " → "), t.Plan.Reason, t.Plan.Approved)
	}
	if verbose {
		fmt.Fprintf(w, "定义修订：%d\n计划修订：%d\n候选：%s\n", n.Revision, n.PlanRevision, t.Candidate)
	}
	return nil
}

func writeTextValue(w io.Writer, label string, value any, depth int) {
	indent := strings.Repeat("  ", depth)
	switch v := value.(type) {
	case map[string]any:
		if label != "" {
			fmt.Fprintf(w, "%s%s：\n", indent, label)
			depth++
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			writeTextValue(w, key, v[key], depth)
		}
	case []any:
		if label != "" {
			fmt.Fprintf(w, "%s%s：\n", indent, label)
			depth++
		}
		for _, item := range v {
			switch item.(type) {
			case map[string]any, []any:
				fmt.Fprintf(w, "%s-\n", strings.Repeat("  ", depth))
				writeTextValue(w, "", item, depth+1)
			default:
				fmt.Fprintf(w, "%s- %v\n", strings.Repeat("  ", depth), item)
			}
		}
	case nil:
		fmt.Fprintf(w, "%s%s：无\n", indent, label)
	default:
		fmt.Fprintf(w, "%s%s：%v\n", indent, label, v)
	}
}

var errorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

func writeError(w io.Writer, err error, machine bool) error {
	message := err.Error()
	if machine {
		// Preserve the existing error object, including its legacy code rule.
		return json.NewEncoder(w).Encode(map[string]any{"error": message, "code": strings.SplitN(message, ":", 2)[0]})
	}
	code, detail, found := strings.Cut(message, ":")
	if !errorCode.MatchString(code) {
		code, detail, found = "ERROR", message, true
	}
	if !found || strings.TrimSpace(detail) == "" {
		detail = message
	}
	_, e := fmt.Fprintf(w, "错误 [%s]：%s\n建议：检查原因和命令参数，必要时运行 ael help 查看用法。\n", code, strings.TrimSpace(detail))
	return e
}

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
	"time"
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

type textEpicProgress struct{ Tree core.EpicTree }

func (p textEpicProgress) MarshalJSON() ([]byte, error) { return json.Marshal(p.Tree) }

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
	case core.EpicTree:
		if command == "task progress" {
			return textEpicProgress{Tree: v}
		}
		return v
	default:
		return value
	}
}

func writeOutput(w io.Writer, value any, machine, verbose bool) error {
	if machine {
		return json.NewEncoder(w).Encode(value)
	}
	switch v := value.(type) {
	case core.WorkResult:
		fmt.Fprintf(w, "父任务：%s；Runner：%s\n状态：%s\n", v.Checkpoint.Parent, v.Checkpoint.Runner, v.Action)
		if v.Checkpoint.Task != "" {
			fmt.Fprintf(w, "当前叶子：%s；步骤：%s；单元：%s\n", v.Checkpoint.Task, v.Checkpoint.Step, v.Checkpoint.Unit)
		}
		if v.Detail != "" {
			fmt.Fprintf(w, "原因：%s\n", v.Detail)
		}
		if v.Blocker != nil {
			fmt.Fprintf(w, "当前单元：%s\n失败输入：%s\n已尝试：%s\n恢复条件：%s\n下一步：%s\n", v.Blocker.Unit, v.Blocker.Input, v.Blocker.Tried, v.Blocker.Recovery, v.Blocker.Next)
		}
		if v.Next != nil {
			return writeNext(w, *v.Next, nil, verbose, false)
		}
		return nil
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
		writeEpicSummary(w, v.Summary)
		if err := writeNext(w, v.Root, nil, verbose, false); err != nil {
			return err
		}
		for _, child := range v.Children {
			kind := "可选"
			if child.Required {
				kind = "必需"
			}
			fmt.Fprintf(w, "\n子任务（%s，里程碑：%s）\n", kind, child.Milestone)
			if err := writeNext(w, child.Task, &child.Summary, verbose, false); err != nil {
				return err
			}
		}
		return nil
	case textEpicProgress:
		fmt.Fprintf(w, "父任务：%s (%s)\n", v.Tree.Root.Task.Definition.Title, v.Tree.Root.Task.Definition.ID)
		writeEpicSummary(w, v.Tree.Summary)
		for _, fact := range v.Tree.Progress {
			fmt.Fprintf(w, "进展 %s · %s：%s", fact.Task, fact.At, fact.Summary)
			if fact.Next != "" {
				fmt.Fprintf(w, "；下一步：%s", fact.Next)
			}
			if len(fact.Artifacts) > 0 {
				fmt.Fprintf(w, "；产物：%s", strings.Join(fact.Artifacts, ", "))
			}
			fmt.Fprintln(w)
		}
		if v.Tree.ProgressTruncated {
			fmt.Fprintln(w, "仅显示最近 100 条进展。")
		}
		for _, fact := range v.Tree.ReportedProgress {
			fmt.Fprintf(w, "未标记成果的记录 %s · %s：%s\n", fact.Task, fact.At, fact.Summary)
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

func writeEpicSummary(w io.Writer, s core.EpicSummary) {
	fmt.Fprintf(w, "叶子任务：%d（必需 %d）\n", s.Total, s.Required)
	fmt.Fprintf(w, "已实施：%d；已验收：%d；已集成：%d；叶子已交付：%d\n", s.Implemented, s.Accepted, s.Integrated, s.Delivered)
	fmt.Fprintf(w, "待开始：%d；部分检查通过：%d；失败：%d；阻塞：%d；等待接手：%d；取消：%d\n", s.NotStarted, s.PartialChecks, s.Failed, s.Blocked, s.WaitingHandoff, s.Cancelled)
	fmt.Fprintln(w, "各项是可重叠的叶子状态；父级集成验收另行确认。")
	for _, issue := range s.Issues {
		fmt.Fprintf(w, "问题 %s", issue.Task)
		if issue.Check != "" {
			fmt.Fprintf(w, " / 检查 %s", issue.Check)
		}
		fmt.Fprintf(w, "：%s\n", issue.Reason)
	}
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
		if summary.Epic != nil {
			writeEpicSummary(w, *summary.Epic)
		}
	} else if t.Result != "" {
		fmt.Fprintf(w, "实际成果：%s\n", t.Result)
	} else {
		fmt.Fprintln(w, "实际成果：暂无可用摘要")
	}
	if t.Integration != nil {
		integration := t.Integration
		fmt.Fprintf(w, "父级集成候选：%s；项目：%s\n", integration.Candidate, integration.ProjectID)
		if integration.ToolBuild != "" {
			fmt.Fprintf(w, "AEL 工具构建：%s\n", integration.ToolBuild)
		}
		if integration.GitHead != "" {
			fmt.Fprintf(w, "Git 提交：%s；本地仓库标识：%s\n", integration.GitHead, integration.LocalRepository)
		} else {
			fmt.Fprintln(w, "仓库身份：非 Git 或提交未知，仅保留本地内容证明。")
		}
		if integration.ImpactEvidence != "" {
			fmt.Fprintf(w, "影响范围依据：%s (%s)\n", integration.ImpactEvidence, integration.ImpactHash)
		} else {
			fmt.Fprintln(w, "影响范围：未知，按需保守复验。")
		}
		for _, leaf := range integration.Leaves {
			fmt.Fprintf(w, "  叶子 %s：%s\n", leaf.ID, leaf.Method)
			for _, check := range leaf.Rechecks {
				fmt.Fprintf(w, "    叶子检查 %s → 父检查 %s\n", check.LeafCheck, check.ParentCheck)
			}
		}
		for _, support := range integration.Supporting {
			fmt.Fprintf(w, "  可选任务 %s：作为范围差异的内容依据\n", support.ID)
		}
	}
	health := n.Health
	if summary != nil && summary.Health != nil {
		health = summary.Health
	}
	if health != nil {
		contact := map[string]string{"no_executor": "当前无人执行", "running": "执行中", "expired": "联系已过期", "mixed": "部分联系已过期"}[health.ContactState]
		if contact == "" {
			contact = health.ContactState
		}
		fmt.Fprintf(w, "联系状态：%s", contact)
		if health.LastContact != nil {
			fmt.Fprintf(w, "；最近联系：%s", health.LastContact.Format(time.RFC3339))
		}
		fmt.Fprintln(w)
		if health.LastProgress != nil {
			fmt.Fprintf(w, "最近有证据的进展：%s；%s\n", health.LastProgress.Format(time.RFC3339), health.LastSummary)
		} else {
			fmt.Fprintln(w, "最近有证据的进展：尚未记录")
		}
		if health.Stage != "" {
			fmt.Fprintf(w, "执行阶段：%s", health.Stage)
			if health.ExpectedUntil != nil {
				fmt.Fprintf(w, "；预计期限：%s", health.ExpectedUntil.Format(time.RFC3339))
			}
			fmt.Fprintln(w)
		}
		if health.NoProgress {
			if health.HealthyLong {
				fmt.Fprintln(w, "提示：超过设定时间没有新证据；已声明的长阶段仍在期限内，未自动停止。")
			} else {
				fmt.Fprintln(w, "提示：超过设定时间没有新证据，请核对执行者、阶段和下一动作。")
			}
		}
		if health.FailureSignature != "" {
			fmt.Fprintf(w, "失败签名：%s\n", health.FailureSignature)
		}
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
		if t.Definition.Type == "epic" {
			if len(check.Affected) > 0 {
				fmt.Fprintf(w, "    关联排查任务：%s\n", strings.Join(check.Affected, ", "))
			} else {
				fmt.Fprintln(w, "    影响范围未知；按实际证据保守复核。")
			}
		}
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
		fmt.Fprintf(w, "当前单元：%s\n失败输入：%s\n已尝试：%s\n下一步：%s\n", t.Blocker.Unit, t.Blocker.Input, t.Blocker.Tried, t.Blocker.Next)
	}
	if cycle, ok := t.FailureCycles[t.ActiveFailure]; ok && t.ActiveFailure != "" {
		state := map[string]string{"review_required": "需要限定复核", "blocked": "复核后仍失败，已停止", "reviewed": "复核已记录"}[cycle.State]
		if state == "" {
			state = cycle.State
		}
		fmt.Fprintf(w, "同单元失败：%d 次；复核状态：%s\n", cycle.Count, state)
		if cycle.FailureClass != "" {
			fmt.Fprintf(w, "失败类别：%s\n", cycle.FailureClass)
		}
		if cycle.State == "review_required" || cycle.State == "blocked" {
			fmt.Fprintln(w, "记录限定复核：ael task review-failure --task ID --file review.json")
		}
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

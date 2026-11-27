export const boards = [
  ["active", "正在推进"],
  ["pending", "待处理"],
  ["confirmation", "待确认"],
  ["completed", "最近完成"],
  ["cancelled", "已取消"],
] as const;
const labels: Record<string, string> = {
  central_snapshots: "已接收规范快照",
  epic: "大任务",
  bug: "缺陷",
  feature: "功能",
  improvement: "改进",
  refactor: "重构",
  docs: "文档",
  chore: "维护",
  test: "测试",
  qa: "质量验证",
  define: "明确目标",
  analysis: "分析与方案",
  execute: "实施工作",
  integrate: "整体验收",
  verify: "执行验收",
  delivery: "交付结果",
  completed: "已完成",
  cancelled: "已取消",
  deleted: "已删除",
  continue: "等待接手",
  repair: "等待修复",
  wait_executor: "执行中",
  wait_environment: "等待环境",
  wait_dependency: "等待前置交付",
  wait_confirmation: "等待决定",
  wait_children: "等待子任务",
  wait_integration: "等待整体验收",
  current: "现行有效",
  accepted: "已采纳",
  proposed: "待决定",
  rejected: "已否决",
  historical: "历史资料",
  conflict: "待核实冲突",
  superseded: "已被替代",
  spec: "规范",
  decision: "决策",
  research: "研究",
  reference: "参考资料",
  pass: "通过",
  fail: "失败",
  blocked: "受阻",
  not_run: "未执行",
  not_applicable: "不适用",
  waived: "已豁免",
  running: "执行中",
  claimed: "已领取 · 待上报",
  interrupted: "已中断",
  abandoned: "已释放",
  finding: "发现",
  verified: "已验证",
  implemented: "已实现 · 尚无发布依据",
  merged: "已合并 · 尚无发布依据",
  released: "已发布（有交付回执）",
  report_delivered: "已交付报告",
  report_delivered_with_findings: "已交付报告 · 存在不通过项",
  analysis_delivered: "已交付分析",
  effective: "规范已生效",
  decided: "已决定 · 未据此认定实现",
  delivery_unconfirmed: "交付结果待核实",
  ready: "资料可读取",
  empty: "未接入资料",
  not_indexed: "尚未建立来源索引",
  index_error: "来源索引异常",
  read_failed: "来源读取失败",
  load_failed: "资料加载失败",
  applied: "导入已应用",
  applying: "正在应用",
  rolled_back: "已回滚",
  rollback_conflict: "回滚存在冲突",
  rolling_back: "正在回滚",
  inherit: "继承原规范",
  pending: "待核实",
  preserve: "保留原件",
  ignore: "忽略缓存",
  import: "导入资料",
  command: "命令检查",
  scenario: "实际场景",
  model: "模型判断",
  human: "人工判断",
  review: "审阅",
  unit: "单元检查",
  functional: "功能检查",
  ui: "视觉检查",
  ux: "交互检查",
  unchanged: "沿用现有规范",
  updated: "已更新规范",
  preview: "等待审阅",
  integration: "旧集成",
  artifact: "证据原件",
  connected: "已接入",
  retired: "已切换权威来源",
  ignored: "已忽略",
  success: "成功",
  interface_available: "接口可调用",
  unavailable: "不可用",
  not_probed: "尚未探测",
};
export const label = (value: string) =>
  value?.startsWith("check:")
    ? "验收检查 · " + value.slice(6)
    : labels[value] || value || "未记录";
export const goalLabel = (value: string) =>
  (
    ({
      verified: "交付经过验证的改动",
      analysis: "交付分析结论",
      report: "交付验收报告",
      merged: "合并到指定分支",
      released: "发布到指定环境",
    }) as Record<string, string>
  )[value] || "查看原定交付目标";
export function blockerText(value: string, compact = true) {
  const exit = /^exit status (\d+)$/.exec(value);
  if (exit) return `检查命令失败，退出码 ${exit[1]}`;
  if (value === "command completed and acceptance coverage recorded") return "命令已完成，检查结果与覆盖条件已记录。";
  if (value === "restore the missing capability or dependency; verify any old writer stopped then resume") return "补齐缺失的能力或依赖，确认旧执行已经停止后恢复";
  if (value === "restore the missing capability and prepare a stable candidate again") return "补齐缺失的能力，重新准备稳定候选";
  if (["verify", "execute", "analysis", "define", "integrate", "delivery"].includes(value)) return labels[value];
  if (labels[value]) return labels[value];
  if (value.includes("approve strict plan before execution"))
    return "方案需要审阅当前定义与计划，批准后才可继续实施。";
  if (value === "runtime restored; reconcile checkpoints, candidate and any delivery run before resuming")
    return "运行库已从备份恢复。请核对任务检查点、候选和已有交付回执后恢复。";
  if (value === "checkpoint imported from project; review files and historical checks before resuming")
    return "已从项目检查点恢复定义。请核对工作文件和旧检查记录，再重新验收。";
  if (value === "historical import requires current context and fresh execution")
    return "请核对原始资料与当前规范，按现行条件重新提交成果及验收。";
  if (value === "required leaves completed; parent integration remains to be accepted")
    return "必需子项满足条件，父级仍需独立整体验收。";
  if (value === "add required leaf tasks before integration")
    return "父级集成前，需要关联至少一项必需子任务。";
  if (value === "parent candidate changed; prepare integration again" || value === "required scope changed; prepare integration again")
    return "父级候选或必需范围发生变化，需重新准备集成。";
  if (value.includes("unknown delivery outcome"))
    return "既有交付结果尚未核实，请先对照运行回执，再决定是否重试。";
  if (value.includes("project shadow mode"))
    return "工作流尚未激活，请在项目设置核实接入与授权。";
  if (value.includes("expired attempt"))
    return "旧执行租约已过期，核实进程停止后恢复任务。";
  if (value.includes("unresolved knowledge conflict"))
    return "适用规范存在待核实冲突，请审阅任务关联依据。";
  if (value.includes("unsatisfied dependency"))
    return "前置任务尚未完成，请查看依赖关系。";
  if (value.includes("budget exhausted"))
    return "已达到明确设置的时间预算，需要审阅后调整。";
  if (value.includes("applicable knowledge changed"))
    return "实施后规范已变化，需要补读并重新验证受影响的候选。";
  if (value.includes("KNOWLEDGE_NOT_INDEXED"))
    return "尚未建立资料来源索引，请在设置查看接入问题。";
  if (value.includes("KNOWLEDGE_SOURCE_CHANGED"))
    return "规范来源已变化，需审阅并更新索引。";
  if (value.includes("KNOWLEDGE_MISSING"))
    return "任务引用的规范缺失，需核实来源与引用。";
  const text = compact ? value.replace(/\s+/g, " ").trim() : value.trim();
  return compact && text.length > 180 ? text.slice(0, 177) + "…" : text;
}
export const stamp = (s: string | null | undefined) =>
  s && Number.isFinite(Date.parse(s))
    ? new Date(s).toLocaleString("zh-CN", { hour12: false })
    : "日期未确认";
export function relativeStamp(value: string | null | undefined) {
  if (!value || !Number.isFinite(Date.parse(value))) return "日期未确认";
  const seconds = Math.round((Date.parse(value) - Date.now()) / 1000);
  const formatter = new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" });
  if (Math.abs(seconds) < 60) return formatter.format(seconds, "second");
  if (Math.abs(seconds) < 3600) return formatter.format(Math.round(seconds / 60), "minute");
  if (Math.abs(seconds) < 86400) return formatter.format(Math.round(seconds / 3600), "hour");
  if (Math.abs(seconds) < 7 * 86400) return formatter.format(Math.round(seconds / 86400), "day");
  return stamp(value);
}
export function schedule(s: any) {
  if (s.recovery_action === "resume") return "恢复已批准，等待执行者继续";
  if (s.needs_history_review) return "历史工作待核对";
  if (s.next_action === "wait_children") return "等待必需子任务";
  if (s.requires_task_decision) return "等待版本决定";
  if (s.historical && s.next_action === "completed") return "历史完成 · 当前未复验";
  if (s.next_action === "wait_confirmation") return "等待规范核实";
  if (s.manual_reviews?.length && !s.running) return "等待人工验收";
  if (s.next_action !== "wait_executor") return label(s.next_action);
  if (s.checks?.running) return `${s.checks.running} 项检查执行中`;
  if (s.checks?.claimed) return `${s.checks.claimed} 项检查已领取`;
  if (s.running) return "当前责任已领取";
  return "等待责任状态刷新";
}

export function checkStatusSummary(checks: any) {
  if (!checks) return "";
  const states = [
    ["passed", "通过"], ["failed", "失败"], ["not_run", "未执行"],
    ["blocked", "受阻"], ["running", "执行中"], ["claimed", "已领取待上报"], ["waived", "已豁免"],
    ["not_applicable", "不适用"],
  ];
  const counts = states.filter(([key]) => checks[key] > 0);
  if (!counts.length) return "尚未配置验收检查";
  if (counts.length === 1 && counts[0][0] === "not_run")
    return checks.historical_failed
      ? `当前版本 ${checks.not_run} 项未执行 · 历史失败 ${checks.historical_failed} 项`
      : `验收尚未开始，${checks.not_run} 项未执行`;
  return counts.map(([key, title]) => `${checks[key]} 项${title}`).join(" · ");
}

export function reviewNoteStatus(s: any) {
  return ({
    await_candidate: "已记录人工意见 · 待候选确认",
    await_confirmation: "已记录人工意见 · 待确认当前候选",
    confirmed: "人工意见已确认当前候选",
    historical: "已有历史人工验收记录",
    outdated: "人工验收意见需重新记录",
    closed: "人工意见未完成候选确认",
  } as Record<string, string>)[s?.review_note_state] || "";
}
export function nextAction(s: any) {
  if (s.recovery_action === "resume") return "执行者调用 resume 恢复任务，刷新依据后领取当前工作；无需再次人工确认";
  if (s.needs_history_review && s.imported) return "执行者核对导入记录与当前实现，用 ael task reconcile-import 记录核对结果与继续起点，再按原门禁重新执行和验收；负责人也可直接确认核对";
  if (s.needs_history_review) return "负责人核对原始工作与当前要求并确认恢复，之后由执行者重新提交成果和验收";
  if (s.requires_task_decision) return "审阅确认对象及其准确版本后作出决定";
  if (s.manual_reviews?.length) return "对照当前候选记录人工验收";
  if (s.next_action === "wait_confirmation" && !s.requires_task_decision)
    return "核实并解决适用规范冲突，再更新来源索引";
  const map: Record<string, string> = {
    continue: "领取当前工作并开始处理",
    repair: "查看失败依据，领取修复工作",
    wait_executor: s.waiting_checks
      ? "可接手其余待运行检查"
      : "等待执行者提交实际结果",
    wait_environment: s.recovery ? blockerText(s.recovery, false) : "解决阻塞条件后恢复",
    wait_dependency: "完成前置任务后继续",
    wait_children: "完成或处理必需子任务后继续父任务",
    wait_integration: "核对全部必要结果后进行父级整体验收",
    wait_confirmation: "查看依据并作出准确版本决定",
    completed: "查看交付与验收结果",
    cancelled: "保留取消记录；新工作另建任务",
  };
  return map[s.next_action] || "查看详情核实下一步";
}
export function readableError(error: string) {
  const messages: Record<string, string> = {
    QUOTA_EXCEEDED: "项目附件容量已达上限，请打开项目设置中的存储与附件，查看占用并调整额度。",
    QUOTA_TOO_SMALL: "新容量上限小于已用与预留容量，请提高上限或先处理上传与可回收原件。",
    PRUNE_STALE: "可清理范围已变化，请重新预览后确认。",
    NOT_RUNNABLE: "当前条件不允许领取，请先处理阻塞。",
    REVIEW_VERSION_REQUIRED: "验收版本信息缺失，请刷新任务后重新审阅。",
    REVIEW_VERSION_MISMATCH: "验收候选或修订已变化，请刷新后重新审阅。",
    REVIEW_INPUT_REQUIRED: "请选择验收结论并填写必需的信息。",
    REVIEW_NOT_ALLOWED: "当前检查不能记录人工验收，请刷新后核对状态。",
    REVIEW_RESUME_NOT_ALLOWED: "当前阻塞无法从此处恢复，请查看任务的阻塞原因和恢复条件。",
    REVIEW_EVIDENCE_CHANGED: "证据已变化或无法读取，请核对后重新选择。",
    REVIEW_NOTE_VERSION_MISMATCH: "记录意见后的定义或计划已变化，请针对当前要求重新记录。",
    REVIEW_NOTE_ALREADY_CONFIRMED: "这条人工意见已经确认，请刷新任务查看结果。",
    REVIEW_NOTE_CHECK_MISMATCH: "这条意见属于另一项检查，请选择对应检查。",
    REVIEW_NOTE_MISSING: "这条人工意见不存在，请刷新任务后重试。",
    REVIEW_CHECK_CONFIRM_REQUIRED: "请在对应人工检查中重新确认这条意见。",
    HUMAN_EVIDENCE_REQUIRED: "请填写记录人和实际观察，并关联需要的真实证据。",
    RECOVERY_REQUIRED: "请填写修复方向或继续验证的条件。",
    SCREENSHOT_REQUIRED: "视觉检查需要关联真实的 PNG 或 JPEG 截图。",
    REVIEW_IMAGE_SIZE: "请选择不超过 10 MB 的 PNG 或 JPEG 截图。",
    REVIEW_IMAGE_INVALID: "图片无法读取，请选择真实的 PNG 或 JPEG 截图。",
    REVIEW_IMAGE_NOT_ALLOWED: "任务已结束或尚无候选，当前不能添加验收截图。",
    CHECK_METHOD_MISMATCH: "此检查需要原先声明的执行方法，不能替换为人工判断。",
    CANDIDATE_CHANGED: "工作区候选已变化，请重新生成并审阅当前候选。",
    IDEMPOTENCY_CONFLICT: "本次请求已经记录但内容不同，请刷新任务核对结果。",
    REVISION_MISMATCH: "对象版本已变化，请刷新后重新审阅。",
    REVISION_CONFLICT: "任务已变化。请保留输入，重新读取并核对确认对象。",
    CONFIRMATION_DETAIL_REQUIRED: "请填写核对意见，并确认所审阅的准确版本。",
    CONFIRMATION_MISMATCH: "确认版本已变化，请刷新后重新审阅。",
    CONFIRMATION_VERSION_MISMATCH: "确认版本已变化，请刷新后重新审阅。",
    KNOWLEDGE_LINK_UNAVAILABLE: "目标文档未接入或当前无法访问。",
    KNOWLEDGE_NOT_FOUND: "这份资料不存在或已从来源中移除。",
    KNOWLEDGE_SOURCE_CHANGED: "原资料已变化，需要审阅并更新来源索引。",
    TASK_CONTEXT_REQUIRED: "请先读取本次工作所需的准确版本依据。",
    STALE_TOKEN: "这次接手已失效；原样重试未产生提交，可在人工验收中查看已保留的旧填写。",
    ATTEMPT_MISSING: "原接手已不存在；请查看保留的旧填写并核对任务状态。",
    STALE_EXECUTION_UNIT: "责任节点或修订已变化；请核对当前任务并查看保留的旧填写。",
    MIGRATION_APPROVAL_REQUIRED: "请核对并填写当前计划的完整确认摘要。",
    LEASE_ACTIVE: "仍有有效执行，请先核实当前执行者。",
    STOP_ACK_REQUIRED: "恢复前须明确核实旧进程已经停止。",
    ROOT_CAUSE_REVIEW_REQUIRED: "请先记录限定根因复核及其真实依据，再恢复该单元。",
    ROOT_CAUSE_NEW_EVIDENCE_REQUIRED: "复核后仍失败；需要保留新的证据，不能只修改措辞后重试。",
    ROOT_CAUSE_REVIEW_NOT_EXPECTED: "当前没有等待复核的失败单元，请刷新状态。",
    INVALID_WORK_STAGE: "阶段期限必须是未来时间，并且需要填写阶段名称。",
    EPIC_INTEGRATION_COVERAGE_REQUIRED: "父任务缺少覆盖该验收条件的必需集成检查，请先补齐定义。",
    EPIC_REQUIRED_LEAF_NOT_ACCEPTED: "必要子任务尚未在当前候选通过要求，不能准备父级整体验收。",
    EPIC_LEAF_NOT_INCLUDED: "父候选缺少或改动了必要叶子的内容，请核对并集成后重试。",
    EPIC_LEAF_INCLUSION_UNKNOWN: "现有记录无法证明叶子内容已纳入；请针对该叶子补充可靠验证。",
    EPIC_LEAF_BASELINE_MISMATCH: "父候选与叶子受验输入存在无法解释的差异，请核对对应文件。",
    EPIC_EXTERNAL_INVALID: "父任务之外改动的声明无效：起点须为本父任务已验收的必需叶子，并写明来源与说明。",
    EPIC_EXTERNAL_UNUSED: "声明的起点叶子之后没有父任务之外的改动，无需声明。",
    EPIC_RECHECK_REQUIRED: "受影响或影响未知的叶子需要在父候选上运行等价必需检查。",
    EPIC_ARTIFACT_NOT_BOUND: "叶子产物未纳入候选指纹，不能据此证明集成。",
    EPIC_EXCLUDED_SCOPE_UNKNOWN: "候选排除范围与叶子范围重叠或无法判定，请核对后重试。",
    EPIC_CANDIDATE_CHANGED: "准备过程中父候选已变化，请核对后重试。",
    EPIC_INTEGRATION_NOT_READY: "父任务仍有执行、暂停或待处理决定，请先处理。",
    EPIC_PLAN_STALE: "父验收或拆分计划已变化，需要核对并更新后重新准备。",
    EPIC_AFFECTED_TASK_UNKNOWN: "集成检查引用了不属于该父任务的排查对象。",
    EPIC_IMPACT_EVIDENCE_REQUIRED: "完整影响范围需要引用候选内的非空影响分析依据。",
    EPIC_IMPACT_INCOMPLETE: "完整影响分析需要为每个必需父检查明确关联范围。",
    EPIC_IMPACT_OVERLAPS_SCOPE: "影响分析文件与叶子交付范围重叠，不能用作独立依据。",
    EPIC_REPOSITORY_UNVERIFIED: "父候选与叶子的本地仓库身份无法对应，不能自动认定已纳入。",
    EPIC_GIT_IDENTITY_UNAVAILABLE: "无法核实本地 Git 仓库或提交，请先检查工作区。",
    EPIC_INTEGRATION_STALE: "父候选或必要叶子已变化，请重新准备整体验收。",
    TASK_NOT_FOUND: "任务不存在或已经删除。",
    INVALID_DEFINITION: "任务定义不完整，请检查标题和验收条件。",
    CHECK_COVERAGE_REQUIRED: "检查需要明确关联验收条件。",
    SYMLINK_REJECTED: "该资源是符号链接，不能通过阅读器访问。",
  };
  for (const [code, text] of Object.entries(messages))
    if (error.includes(code)) return text;
  if (/fetch|network|abort|Failed to fetch|Load failed/i.test(error))
    return "无法取得最新数据，请检查本机服务连接后重试。";
  if (/JSON|SyntaxError/i.test(error))
    return "内容格式有误，请检查后再次提交。";
  return "操作或数据读取失败，输入已保留。请查看原因后重试。";
}

// A read-only presentation of authority-owned facts, shared by rows and detail.
export function workPresentation(s: any) {
  s=s || {};const h=s.health || {};
  const title=schedule(s);
  const reason=s.blocker_reason ? blockerText(s.blocker_reason,false) : (s.blocking_reasons || []).map((x:string)=>blockerText(x,false)).join('；');
  const role=s.recovery_action === 'resume' || (s.needs_history_review && s.imported) ? '执行者' : s.needs_history_review || s.requires_task_decision ? '负责人' : s.manual_reviews?.length ? '验收人' : s.next_action === 'wait_environment' ? '阻塞处理人' : s.running ? '当前执行者' : '执行者';
  const next=h.reported_next && s.running && !reason ? h.reported_next : nextAction(s);
  const latest=h.last_progress_summary ? ((h.last_progress_validity === 'prior' || (h.last_progress_candidate && h.last_progress_candidate!==h.candidate) ? '先前候选或修订结果，当前需复验：' : h.last_progress_validity === 'unknown' ? '适用版本待核对：' : '')+(['define','analysis'].includes(h.last_progress_unit) ? '分析产物：' : '')+blockerText(h.last_progress_summary, false)) : '';
  const current=reason || (s.running ? h.current_work || (h.stage ? '正在处理：'+label(h.stage) : '已领取当前工作，尚未回报具体工作内容') : s.next_action === 'completed' || s.next_action === 'cancelled' ? '' : next);
  return {title,reason,role,next,latest,current,at:h.last_progress_at,reportedAt:h.reported_at,action:s.recovery_action==='resume'?'恢复并继续':s.needs_history_review?'核对历史资料':s.requires_task_decision?'审阅方案':s.manual_reviews?.length?'进行人工验收':s.next_action==='completed'?'查看交付结果':s.next_action==='wait_environment'?'查看阻塞':s.running?'查看当前工作':'查看下一步'};
}

// A single status-aware reading fact; absence of an owner is not a blocker.
export function workFocus(s:any) {
 const p=workPresentation(s),h=s?.health || {};
 if(s?.recovery_action==='resume')return '恢复已批准，等待执行者按当前授权继续';
 if(s?.needs_history_review)return historyReviewText(p.reason);
 if(s?.requires_task_decision)return p.reason || '具体方案或范围问题尚未记录，请查看当前决定对象';
 if(s?.next_action==='wait_environment' || s?.next_action==='wait_dependency' || s?.blocker_reason)return p.reason || '阻塞条件尚未记录，需由处理人核实';
 if(s?.next_action==='wait_children')return '等待子任务处理，剩余工作见下方';
 if(s?.next_action==='wait_integration')return '子任务条件已满足，父级仍待集成与整体验收';
 if(s?.next_action==='repair')return '待修复：'+(p.reason || p.latest || '具体失败点请查看原检查记录');
 if(s?.manual_reviews?.length){const briefs=s.manual_reviews.map((c:any)=>c.brief?.summary).filter(Boolean);return briefs.length?'请你判断：'+briefs.join('；'):'需要判断（执行者未说明审核内容）：'+[...new Set<string>(s.manual_reviews.flatMap((c:any)=>c.covers?.length ? c.covers : [c.check]))].join('；');}
 if(s?.running && h.no_progress)return (h.current_work || '已有执行责任')+'；最近上报后运行情况待核实';
 if(s?.running)return h.current_work || p.latest || '已接手，尚未回报具体工作';
 if(p.latest)return p.latest;
 if(['completed','cancelled'].includes(s?.board))return s.last_result || (s.board==='completed'?'已按原规则完成':'已取消');
 // The step only advances after the previous one passed, so it is itself the fact.
 const reached:Record<string,string>={analysis:'目标已明确，等待接手分析与方案',execute:'定义与分析已完成，等待接手实施',verify:'实施结果已提交，等待验证',integrate:'子任务条件已满足，等待整体验收',delivery:'验收已通过，等待交付'};
 if(s?.next_action==='continue' && reached[s?.step])return reached[s.step];
 return '等待执行者接手';
}

export function historyReviewText(reason?:string) {
 if(reason?.includes('historical import requires current context and fresh execution'))return '导入记录尚无本次上下文与新执行证据；负责人核对原工作范围后，执行者按原门禁恢复并重新执行。';
 if(reason?.includes('runtime restored; reconcile checkpoints'))return '需核对恢复的检查点、候选与可能已发生的交付运行；具体异常项尚未记录。';
 return reason || '具体核对项尚未记录；相关历史资料仍可查，请由负责人核实后按原门禁继续。';
}

// Reading-layer status for list rows and detail headers. It only names facts
// the authority already returned (board, next action, reviews, history); the
// backend enums and gates are unchanged.
export type WorkState = { key: string; label: string; tone: string };
export function workState(s: any, canJudge = true): WorkState {
  s = s || {};
  if (s.board === 'completed') return s.historical ? { key: 'done', label: '历史完成', tone: '' } : { key: 'done', label: '已完成', tone: 'done' };
  if (s.board === 'cancelled') return { key: 'cancelled', label: '已取消', tone: '' };
  if (s.recovery_action === 'resume') return { key: 'resume', label: '可恢复', tone: 'active' };
  if (s.needs_history_review) return { key: 'history', label: '历史待核对', tone: '' };
  if (s.requires_task_decision || s.board === 'confirmation') return { key: 'decision', label: canJudge ? '待你决定' : '待决定', tone: 'review' };
  if (s.manual_reviews?.length && !s.running) return { key: 'review', label: canJudge ? '待你验收' : '待验收', tone: 'review' };
  if (s.next_action === 'wait_environment' || s.blocker_reason) return { key: 'blocked', label: (s.blocking_reasons || []).some((r: string) => r.includes('expired attempt')) ? '责任已过期' : '受阻', tone: 'blocked' };
  if (s.next_action === 'repair') return { key: 'repair', label: '待修复', tone: 'amber' };
  if (s.board === 'active') return { key: 'active', label: '推进中', tone: 'active' };
  if (s.next_action === 'wait_dependency') return { key: 'dependency', label: '等待前置', tone: '' };
  if (s.next_action === 'wait_children') return { key: 'children', label: '等待子任务', tone: '' };
  if (s.next_action === 'wait_integration') return { key: 'integration', label: '待整体验收', tone: 'review' };
  if (s.next_action === 'wait_executor') return { key: 'active', label: '推进中', tone: 'active' };
  // Waiting for a taker: name the step so "analysed, awaiting implementation"
  // is distinguishable from "never started".
  const stepLabel: Record<string, string> = { analysis: '待分析', execute: '待实施', verify: '待验证', integrate: '待整体验收', delivery: '待交付' };
  return { key: 'pending', label: stepLabel[s.step] || '待接手', tone: '' };
}
// A parent row also says what its unfinished children are doing, without
// changing the parent's own state or completion conditions.
export function rowState(row: any, canJudge = true): WorkState {
  const own = workState(row?.task, canJudge);
  if (!row?.children || !['pending', 'children'].includes(own.key)) return own;
  if (row.active_children) return { key: 'active', label: '推进中', tone: 'active' };
  if (row.review_children) return { key: 'review', label: canJudge ? '子项待你验收' : '子项待验收', tone: 'review' };
  if (row.blocked_children) return { key: 'blocked', label: '子项受阻', tone: 'blocked' };
  return own;
}
// Detail pages read the same family counts as the list row.
export function familyRow(task: any, family: any) {
  return family?.descendants ? { task, children: family.descendants, completed: family.completed, remaining: family.remaining, active_children: family.active_children, review_children: family.review_children, blocked_children: family.blocked_children } : { task, children: 0 };
}
export function rowSummary(row: any) {
  if (!row?.children) return workFocus(row?.task);
  const parts = [`已完成 ${row.completed} / ${row.children} 个子任务`];
  const states = [[row.active_children, '推进中'], [row.review_children, '待验收'], [row.blocked_children, '受阻']].filter(([n]) => n).map(([n, t]) => `${n} 项${t}`);
  const own = workState(row.task);
  const tail = own.key === 'pending' || own.key === 'children' ? '' : '；父事项：' + workFocus(row.task);
  return parts[0] + (states.length ? '，' + states.join('、') : row.remaining ? `，剩余 ${row.remaining} 项` : '') + tail;
}
export type Owner = { name: string; mark: string; gray: boolean; you: boolean };
export function workOwner(s: any, canJudge = true, username = '', row?: any): Owner {
  s = s || {};
  const you = (name = '你') => ({ name, mark: (username || 'W').slice(0, 1).toUpperCase(), gray: false, you: true });
  const agent = (name: string) => ({ name, mark: 'A', gray: true, you: false });
  const role = (name: string) => ({ name, mark: name.slice(0, 1), gray: true, you: false });
  if (s.board === 'completed') return { name: s.historical ? '历史记录' : '已交付', mark: '✓', gray: true, you: false };
  if (s.board === 'cancelled') return { name: '—', mark: '·', gray: true, you: false };
  if (s.requires_task_decision || s.board === 'confirmation') return canJudge ? you() : role('负责人');
  if (s.manual_reviews?.length && !s.running) return canJudge ? you() : role('验收人');
  if (s.running && s.executors?.length) return agent(s.executors.join('、'));
  // Queued is a collaboration marker, not running work: neutral, own label.
  if (s.assignment === 'queued' && s.queue) return { name: queueLabel(s.queue) + ' · 排队中', mark: queueLabel(s.queue).slice(0, 1).toUpperCase(), gray: true, you: false };
  if (s.recovery_action === 'resume' || (s.needs_history_review && s.imported)) return agent('执行者');
  if (s.needs_history_review) return canJudge ? you() : role('负责人');
  if (s.next_action === 'wait_environment' || s.blocker_reason) return role('阻塞处理人');
  if (row?.children && row.active_children && !s.running) return agent('子任务执行者');
  if (s.next_action === 'repair') return agent('执行者');
  // "未安排" only says AEL holds no queue record; another system may still have one.
  return { name: s.assignment === 'unassigned' ? '未安排' : '待接手', mark: '·', gray: true, you: false };
}
export function queueLabel(q: any) { return q?.runner_label || q?.runner || '执行者'; }
// Actual waiting time since the record was written; never a liveness verdict.
export function waitedFor(value: string | null | undefined) {
  if (!value || !Number.isFinite(Date.parse(value))) return '时间未确认';
  const minutes = Math.max(0, Math.floor((Date.now() - Date.parse(value)) / 60000));
  if (minutes < 1) return '不到 1 分钟';
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} 小时${minutes % 60 ? ' ' + (minutes % 60) + ' 分钟' : ''}`;
  return `${Math.floor(hours / 24)} 天`;
}
export const queueErrors: Record<string, string> = {
  QUEUE_VERSION_CONFLICT: '排队安排已被他人更新或已失效，已刷新为最新归属。',
  QUEUE_STARTED: '任务已被领取开始执行，不能再释放或转交排队；请使用现有交接入口。',
  QUEUE_RESERVED: '任务已由其他执行者安排，尚未开始。',
  QUEUE_TARGET_FORBIDDEN: '接收者在本项目没有领取这一步骤的权限。',
  QUEUE_INVALID: '请求不完整：请选择接收者并填写原因。',
  REMOTE_HANDOFF_TARGET: '任务已指定交接接收方，只能转交给该接收方。',
  RUNNER_NOT_FOUND: '接收者未在本项目注册。',
  FORBIDDEN: '只有安排所属的执行者账号或项目管理员可以操作。',
  IDEMPOTENCY_CONFLICT: '同一请求键已用于不同内容，请重新提交。',
};
export function relativeShort(value: string | null | undefined) {
  if (!value || !Number.isFinite(Date.parse(value))) return '';
  const days = Math.floor((Date.now() - Date.parse(value)) / 86400000);
  if (days < 1) return relativeStamp(value);
  if (days === 1) return '昨天';
  if (days === 2) return '前天';
  return new Date(value).toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' });
}
// Copyable hand-off text. Copying never claims, starts or reserves work.
export function handoffText(project: string, task?: { id: string; title: string; next?: string }) {
  return `请在 AEL 项目 ${project} 中读取${task ? `任务「${task.title}」（${task.id}）` : '当前未完成工作'}，核对当前状态、已有结果与授权范围。` +
    (task?.next ? `下一步：${task.next}。` : '按已有父级计划接续可执行的工作。') +
    '按 AEL 的领取、上下文与提交协议执行；保留已完成成果，只在真实阻塞或需要人作决定时等待。';
}

// Responsibility and reporting facts for an open task. A report is a past
// observation: it never proves an Agent is online, and silence never proves
// a failure.
export function progressFacts(n: any) {
  const s = n?.summary || {}, h = s.health || n?.health || {};
  const active = (n?.collaboration || []).filter((c: any) => c.responsibility_state === 'active' || c.responsibility_state === 'claimed');
  const owner = active.map((c: any) => c.alias || c.runner).filter(Boolean).join('、') || (s.executors || []).join('、');
  const kind: Record<string, string> = { artifact: '提交成果', check: '提交检查记录' };
  let record = '';
  if (h.reported_at && (!h.last_progress_at || h.reported_at >= h.last_progress_at)) record = relativeShort(h.reported_at) + '上报进展';
  else if (h.last_progress_at) record = relativeShort(h.last_progress_at) + (kind[h.last_progress_kind] || '记录进展');
  else if (s.running) record = '已领取责任，尚未上报进展';
  const contact = h.last_contact ? relativeShort(h.last_contact) + '客户端联系（不代表当前在线）' : '';
  const since = h.last_progress_at || h.reported_at;
  const minutes = since ? Math.max(0, Math.round((Date.now() - new Date(since).getTime()) / 60000)) : 0;
  const quiet = s.running && h.no_progress ? { minutes, threshold: Math.round((h.threshold_seconds || 0) / 60) } : null;
  const long = s.running && h.healthy_long && h.expected_until ? { until: h.expected_until } : null;
  return { owner, record, contact, quiet, long };
}

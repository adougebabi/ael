export const boards = [
  ["active", "正在推进"],
  ["pending", "待处理"],
  ["confirmation", "待确认"],
  ["completed", "最近完成"],
  ["cancelled", "已取消"],
] as const;
const labels: Record<string, string> = {
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
  interrupted: "已中断",
  abandoned: "已释放",
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
export function blockerText(value: string) {
  if (value.includes("approve strict plan before execution"))
    return "方案需要审阅当前定义与计划，批准后才可继续实施。";
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
  const text = value.replace(/\s+/g, " ").trim();
  return text.length > 180 ? text.slice(0, 177) + "…" : text;
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
  if (s.manual_reviews?.length && !s.running) return "等待人工验收";
  if (
    s.next_action === "wait_confirmation" &&
    !s.requires_task_decision &&
    !s.running
  )
    return "等待规范核实";
  if (s.running > 0 && s.waiting_checks > 0)
    return `${s.running} 项执行中 · ${s.waiting_checks} 项检查待接手`;
  if (s.running > 0)
    return `${s.running} 项执行中${s.next_action !== "wait_executor" ? " · " + label(s.next_action) : ""}`;
  if (s.next_action === "wait_executor") return "等待执行事实刷新";
  return label(s.next_action);
}
export function reviewNoteStatus(s: any) {
  return ({
    await_candidate: "已记录人工验收 · 待候选确认",
    await_confirmation: "已记录人工验收 · 待确认当前候选",
    confirmed: "人工意见已确认当前候选",
    historical: "已有历史人工验收记录",
    outdated: "人工验收意见需重新记录",
    closed: "人工意见未完成候选确认",
  } as Record<string, string>)[s?.review_note_state] || "";
}
export function nextAction(s: any) {
  if (s.manual_reviews?.length) return "对照当前候选记录人工验收";
  if (s.next_action === "wait_confirmation" && !s.requires_task_decision)
    return "核实并解决适用规范冲突，再更新来源索引";
  const map: Record<string, string> = {
    continue: "领取当前工作并开始处理",
    repair: "查看失败依据，领取修复工作",
    wait_executor: s.waiting_checks
      ? "可接手其余待运行检查"
      : "等待执行者提交实际结果",
    wait_environment: s.recovery || "解决阻塞条件后恢复",
    wait_dependency: "完成前置任务后继续",
    wait_confirmation: "查看依据并作出准确版本决定",
    completed: "查看交付与验收结果",
    cancelled: "保留取消记录；新工作另建任务",
  };
  return (
    (["continue", "repair", "wait_executor"].includes(s.next_action)
      ? s.next
      : "") ||
    map[s.next_action] ||
    "查看详情核实下一步"
  );
}
export function readableError(error: string) {
  const messages: Record<string, string> = {
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

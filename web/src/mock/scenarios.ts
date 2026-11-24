// Scenario catalogue for `npm run dev:mock`. Each scenario only chooses which
// captured authority state the real pages see and how the API boundary
// behaves (latency, failures, what an action changes). Task data itself comes
// from fixtures/captured.json, recorded from a real local Server by
// scripts/web_mock_capture.py.

export type Scenario = {
  id: string;
  title: string;
  /** What the scenario is for, shown in the mock bar. */
  note: string;
  /** Hash route opened when the scenario starts. */
  entry: string;
  /** Delay for every mocked response, in ms. */
  latency?: number;
  /** GET /api/task fails with 503 after this many successful reads (0 = always). */
  taskReadFailsAfter?: number;
  /** Action handling: which real follow-up state an action moves a task to. */
  actions?: Record<string, { task: string; state: number; delay?: number } | { error: string; code: string; status: number; delay?: number }>;
  /** A server-side change the mock bar can trigger (as an Agent would). */
  advance?: { task: string; state: number; label: string };
};

export const scenarios: Scenario[] = [
  { id: "queued", title: "排队等待执行", note: "任务已创建，还没有执行者接手，也没有任何执行记录。", entry: "/tasks/mock-queued" },
  {
    id: "running", title: "执行中", note: "执行者已接手实施步骤；可在模拟栏推进“执行者提交结果”，再观察轮询和手动刷新。", entry: "/tasks/mock-running",
    advance: { task: "mock-running", state: 1, label: "执行者提交实施结果" },
  },
  { id: "delivered", title: "成功且有结果", note: "所有步骤和必需检查通过，任务已交付。", entry: "/tasks/mock-delivered" },
  { id: "failed", title: "失败且有原因", note: "实施先失败一次后重做，必需检查失败，任务退回修复。", entry: "/tasks/mock-failed" },
  { id: "blocked", title: "环境受阻", note: "执行者报告外部环境阻塞，等待解除。", entry: "/tasks/mock-blocked" },
  { id: "no-calls", title: "没有执行记录", note: "直接打开执行诊断，任务还没有任何执行。", entry: "/tasks/mock-queued?section=execution" },
  { id: "mixed-calls", title: "多次执行部分失败", note: "执行诊断：5 次执行中 2 次失败，直接定位到失败的检查执行。", entry: "/tasks/mock-failed?section=execution" },
  {
    id: "step-passed-open", title: "执行成功但任务未完成", note: "实施步骤已通过，但必需检查尚未运行，任务仍未完成。点击“运行真实检查”会得到真实的后续状态。", entry: "/tasks/mock-awaiting-check",
    actions: { verify: { task: "mock-awaiting-check", state: 1, delay: 900 } },
  },
  {
    id: "action-error", title: "操作失败反馈", note: "同上，但运行检查返回错误，用于检查弹窗内的失败反馈。", entry: "/tasks/mock-awaiting-check",
    actions: { verify: { status: 409, code: "CANDIDATE_CHANGED", error: "CANDIDATE_CHANGED: return to execute with resume --return-to execute", delay: 900 } },
  },
  { id: "long", title: "超长输入与错误", note: "超长任务原文、超长执行失败说明和 8KB 检查日志。", entry: "/tasks/mock-long?section=execution" },
  { id: "slow", title: "请求加载中", note: "所有接口延迟 2.5 秒返回，用于检查加载状态。", entry: "/tasks/mock-failed", latency: 2500 },
  { id: "load-error", title: "请求失败", note: "任务详情接口始终返回 503。", entry: "/tasks/mock-failed", taskReadFailsAfter: 0 },
  { id: "refresh-error", title: "刷新失败", note: "首次读取成功，之后的刷新与轮询返回 503，页面应保留旧数据并提示过期。", entry: "/tasks/mock-failed", taskReadFailsAfter: 1 },
  { id: "check-detail", title: "检查详情弹窗", note: "直接打开失败检查的详情弹窗。", entry: "/tasks/mock-failed?section=checks&detail=report-file" },
];

export const defaultScenario = "failed";
export function findScenario(id: string | null | undefined) {
  return scenarios.find((s) => s.id === id) || scenarios.find((s) => s.id === defaultScenario)!;
}

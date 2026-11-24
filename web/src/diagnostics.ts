// Reading of a task's executions (attempts) for the diagnostics view and the
// overview's "where is it stuck" line. Every field comes from the snapshot the
// authority returned; an execution's own result is never read as the task's.
import { blockerText, label } from "./display";

export type Execution = {
  id: string; unit: string; step: string; status: string; tone: "pass" | "fail" | "warn" | "running" | "muted";
  executor: string; started: string; ended: string; duration: string; summary: string; outputs: string[];
  candidate: string; revisions: string; current: boolean; raw: any;
};

const tones: Record<string, Execution["tone"]> = { pass: "pass", fail: "fail", blocked: "warn", interrupted: "warn", abandoned: "muted", running: "running" };
const statusNames: Record<string, string> = { pass: "执行通过", fail: "执行失败", blocked: "执行受阻", interrupted: "执行中断", abandoned: "已释放", running: "执行中" };

export function duration(from?: string, to?: string | null) {
  const start = Date.parse(from || ""), end = to ? Date.parse(to) : NaN;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return "";
  const s = Math.round((end - start) / 1000);
  if (s < 1) return "不到 1 秒";
  if (s < 60) return s + " 秒";
  if (s < 3600) return Math.floor(s / 60) + " 分 " + (s % 60) + " 秒";
  return Math.floor(s / 3600) + " 小时 " + Math.floor((s % 3600) / 60) + " 分";
}

/** Executions newest first, each with the summary its own submission recorded. */
export function executions(n: any): Execution[] {
  const t = n?.task;
  const submitted = new Map<string, any>();
  for (const e of n?.activity || []) if (e.kind === "submitted" && e.attempt && !submitted.has(e.attempt)) submitted.set(e.attempt, e);
  return [...(t?.attempts || [])].reverse().map((a: any) => {
    const event = submitted.get(a.id);
    const running = a.status === "running" && Date.parse(a.lease_until) > Date.now();
    const status = a.status === "running" && !running ? "interrupted" : a.status;
    const text = event?.summary || a.reported_summary || a.reason || "";
    // The lease token is a credential for the executor; it is never shown.
    const { token: _token, ...raw } = a;
    return {
      id: a.id, unit: a.unit, step: label(a.unit), status, tone: tones[status] || "muted",
      executor: event?.actor || a.executor || "", started: a.started, ended: a.ended || "",
      duration: running ? "" : duration(a.started, a.ended), summary: text ? blockerText(text, false) : "",
      outputs: event?.artifacts || a.outputs || [], candidate: a.candidate_version || "",
      revisions: "定义 " + (a.definition_revision ?? "?") + " / 计划 " + (a.plan_revision ?? "?"),
      current: a.candidate_version === t?.candidate && a.definition_revision === n?.definition_revision && a.plan_revision === n?.plan_revision,
      raw,
    };
  });
}
export const executionStatus = (status: string) => statusNames[status] || label(status);

/** The latest execution, when it ended unsuccessfully and the task is still open. */
export function stuckAt(n: any): Execution | undefined {
  if (n?.task?.status !== "open") return undefined;
  const latest = executions(n).find((e) => e.status !== "running");
  return latest && ["fail", "blocked", "interrupted"].includes(latest.status) ? latest : undefined;
}

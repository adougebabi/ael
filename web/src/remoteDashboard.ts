import { ref } from "vue";
import { api, ApiError, infoError, refreshCurrentView, useRemoteTransport, remoteProject } from "./api";
import { setProjectScope } from "./router";

type Project = { id: string; name: string; role: string };
let generation = 0;
let current: Project | null = null;
let requestCursor = 0;
let appliedCursor = 0;
let lastWatermark = "";
export const remoteSyncError = ref("");
export const remoteSyncAt = ref("");

export function connectDashboard(project: Project | null, csrf = "", expired?: () => void, lostAccess?: () => void) {
  const epoch = ++generation;
  current = project;
  lastWatermark = "";
  remoteSyncError.value = "";
  remoteSyncAt.value = "";
  setProjectScope(project ? csrf + ":" + project.id : "", project?.id || "");
  // The URL carries the project ID; the private session context is only a
  // namespace for browser state and is never placed in a URL.
  if (!project) { useRemoteTransport(null); return; }
  useRemoteTransport(project, async (path, body, raw) => {
    const url = new URL(path, location.origin);
    let endpoint = `/api/v1/projects/${encodeURIComponent(project.id)}/dashboard/${url.pathname.slice(5)}` + url.search;
    if (url.pathname === "/api/findings") endpoint = `/api/v1/projects/${encodeURIComponent(project.id)}/findings` + url.search;
    if (url.pathname === "/api/finding") endpoint = `/api/v1/projects/${encodeURIComponent(project.id)}/findings/${encodeURIComponent(url.searchParams.get("id") || "")}`;
    let payload = body;
    if (body !== undefined) {
      const input = body as Record<string, any>;
      if (url.pathname !== "/api/action" || !["pause", "cancel"].includes(input.action)) throw new ApiError("REMOTE_ACTION_UNAVAILABLE", "此操作需要对应执行者权限与责任。");
      endpoint = `/api/v1/projects/${encodeURIComponent(project.id)}/tasks/${encodeURIComponent(input.id)}/${input.action}`;
      const key = crypto.randomUUID();
      payload = { reason: input.reason, expected_revision: input.remote_revision, operation_id: key, idempotency_key: key };
    }
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 20000);
    try {
      const response = await fetch(endpoint, {
        method: body === undefined ? "GET" : "POST", signal: controller.signal,
        headers: body === undefined ? {} : { "Content-Type": "application/json", "X-AEL-Protocol": "1", "X-AEL-CSRF": csrf },
        body: body === undefined ? undefined : JSON.stringify(payload), cache: "no-store",
      });
      if (generation !== epoch) throw new ApiError("PROJECT_CHANGED", "项目已切换，旧请求已失效。");
      if (response.status === 401 || response.headers.get("X-AEL-Session-Context") !== csrf) {
        expired?.();
        throw new ApiError("AUTH_REQUIRED", "登录状态已变化，请重新登录。");
      }
      if (!response.ok) {
        const error = await response.json().catch(() => ({}));
        if (error.code === "PROJECT_NOT_FOUND") lostAccess?.();
        throw new ApiError(error.code || String(response.status), error.error || "服务端读取失败");
      }
      const result = raw === "blob" ? await response.blob() : raw ? await response.text() : await response.json();
      if (generation !== epoch) throw new ApiError("PROJECT_CHANGED", "项目已切换，旧请求已失效。");
      return result;
    } finally { clearTimeout(timeout); }
  });
}

export async function pollDashboard() {
  if (!current) return;
  const epoch = generation;
  const sequence = ++requestCursor;
  try {
    const result = await api("/api/sync?cursor=" + encodeURIComponent(lastWatermark));
    if (epoch !== generation || sequence < appliedCursor || result.project_id !== current?.id) return;
    appliedCursor = sequence;
    const watermark = result.cursor;
    if (remoteProject.value && result.role) remoteProject.value.role = result.role;
    remoteSyncAt.value = new Date().toLocaleTimeString("zh-CN");
    const recovering = !!remoteSyncError.value;
    remoteSyncError.value = "";
    if (watermark !== lastWatermark || recovering) { lastWatermark = watermark; refreshCurrentView(); }
  } catch (error) {
    if (epoch === generation && sequence >= appliedCursor) {
      appliedCursor = sequence;
      remoteSyncError.value = String(error);
      infoError.value = String(error);
    }
  }
}

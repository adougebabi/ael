<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import WorkspaceApp from "./App.vue";
import { connectDashboard } from "./remoteDashboard";
import { navigate, route, href, taskHref } from "./router";
import AppIcon from "./components/AppIcon.vue";
import MenuPopover from "./components/MenuPopover.vue";
import RemoteServicePanel from "./components/RemoteServicePanel.vue";
import RemoteAccountPanel from "./components/RemoteAccountPanel.vue";
import ProjectStoragePanel from "./components/ProjectStoragePanel.vue";
import MaterialsPage from "./pages/MaterialsPage.vue";
import { refreshRevision, currentUser } from "./api";
import { stamp, relativeShort } from "./display";
import { theme, setTheme } from "./theme";
import { toast, copyText } from "./toast";
const workspaceMode = ref(false);
const locationOrigin = location.origin;
let projectGeneration = 0;
let panelRequest = 0;

type Session = { username: string; admin: boolean; csrf: string };
type Project = { id: string; name: string; role: string };
type Token = { id: string; username: string; project_id: string; label: string; revoked: boolean };
type TaskItem = { task: { definition: { id: string; title: string }; status: string } };
type Artifact = { id: string; filename: string; mime: string; size: number; sha256: string; status: string };
type ContextSource = { id: string; path: string; origin?: string; artifact_id: string; sha256: string; status: string; kind: string; reason: string; content?: string; error?: string; loading?: boolean };
type ContextCheck = { id: string; check_id: string; artifact_id: string; artifact_status: string; artifact_sha256: string; config_sha256: string; candidate_id: string; actor: string };
type TaskContext = { status?: string; validity?: string; validity_reasons?: string[]; changed_paths?: string[]; impact_unknown?: boolean; inputs_known?: boolean; inventory_unverified?: boolean; id?: string; candidate_id?: string; repository?: string; commit?: string; checkout_id?: string; workspace_id?: string; runner_id?: string; actor?: string; sources: ContextSource[]; local_sources?: { id: string; source: string; version: string }[]; checks?: ContextCheck[] };

type ClientReport = { client_id: string; actor: string; session_id?: string; runner?: string; cli_version: string; cli_build?: string; skill_version: string; update_policy: string; specification_state: string; hooks?: Record<string, string>; reported_at: string; compatible: boolean };
const pickerOpen = ref(false), projectSearch = ref("");
const accountMenu = ref<InstanceType<typeof MenuPopover> | null>(null);
function closeAccount() { accountMenu.value?.close(); }
const ownerRoles: Record<string, string> = { admin: "项目管理员", executor: "执行者", reviewer: "验收者", reader: "只读成员" };
function connectionText() {
  if (!selected.value) return "";
  return `AEL 项目接入\n项目：${selected.value.name}（${selected.value.id}）\n中心服务：${locationOrigin}\n在已安装 ael 的设备上，于工程目录运行：\nael init --server ${locationOrigin} --remote-project ${selected.value.id}`;
}
async function copyConnection() { toast(await copyText(connectionText()) ? "已复制项目连接信息" : "浏览器未允许复制，请在项目设置中手动复制"); }
async function copyConnectionInfo() { toast(await copyText(connectionText()) ? "已复制接入信息" : "浏览器未允许复制，请手动选择命令"); }
// Member and token dialogs; the server keeps the only authority over roles.
const memberDialog = ref<HTMLDialogElement | null>(null), tokenDialog = ref<HTMLDialogElement | null>(null), revokeDialog = ref<HTMLDialogElement | null>(null);
const memberEditing = ref(false), revokeTarget = ref<Token | null>(null);
async function openMemberDialog(username = "", role = "executor") { memberEditing.value = !!username; memberUsername.value = username; memberRole.value = role; memberProject.value = selected.value?.id || ""; error.value = ""; await nextTick(); memberDialog.value?.showModal(); }
async function saveMember() { await addMember(); if (!error.value) { memberDialog.value?.close(); await loadPanel(); } }
async function openTokenDialog() { tokenProject.value = selected.value?.id || ""; tokenUsername.value = members.value[0]?.username || ""; tokenLabel.value = ""; if (selected.value) delete tokenReplies.value[selected.value.id]; error.value = ""; await nextTick(); tokenDialog.value?.showModal(); }
async function saveToken() { await createToken(); await loadPanel(); }
function closeTokenDialog() { tokenDialog.value?.close(); if (selected.value) delete tokenReplies.value[selected.value.id]; }
async function copyToken() { toast(await copyText(tokenSecret.value) ? "令牌已复制，请保存到安全位置" : "浏览器未允许复制，请手动选择令牌"); }
async function confirmRevoke(item: Token) { revokeTarget.value = item; await nextTick(); revokeDialog.value?.showModal(); }
async function doRevoke() { if (!revokeTarget.value) return; await revoke(revokeTarget.value.id); revokeDialog.value?.close(); revokeTarget.value = null; await loadPanel(); }
function switchTheme() { setTheme(theme.value === "indigo" ? "graphite" : "indigo"); }
const pickerRoot = ref<HTMLElement | null>(null), pickerInput = ref<HTMLInputElement | null>(null);
const panelLoading = ref(false), panelError = ref("");
const projectConfiguration = ref<any>(null), clients = ref<ClientReport[]>([]), serviceInfo = ref<any>(null), serviceInfoAt = ref("");
const servicePlatform = ref("");
const settingsTab = computed(() => ({ storage: "storage", members: "members" } as Record<string, string>)[route.value.query.get("section") || ""] || "connection");
const members = ref<{ username: string; role: string }[]>([]);
const panel = computed(() => route.value.path.split("/")[1] || "tasks");
const filteredProjects = computed(() => projects.value.filter(project => (project.name + " " + project.id).toLowerCase().includes(projectSearch.value.trim().toLowerCase())));
const projectTokens = computed(() => tokens.value.filter(token => token.project_id === selected.value?.id));
const policyNames: Record<string, string> = { notify: "只提示", auto: "自动兼容更新", pinned: "固定版本" };
const specificationNames: Record<string, string> = { awaiting_sources: "资料待完善", readable_snapshots_require_checkout_verification: "快照可读，待核对 checkout", checkout_differs_from_server_snapshots: "本机与规范快照有差异", session_prepared: "工具会话已准备" };
function hookSummary(client: ClientReport) { const states = Object.values(client.hooks || {}); return states.includes("pending_host_trust") ? "Hook 待宿主信任" : states.includes("unsupported_or_unavailable") ? "显式恢复可用" : "未上报 Hook 状态"; }
async function openPicker() { closeAccount();pickerOpen.value = !pickerOpen.value; if (pickerOpen.value) { projectSearch.value = ""; await nextTick(); pickerInput.value?.focus(); } }
function closePicker(event: MouseEvent) { if (pickerRoot.value && !pickerRoot.value.contains(event.target as Node)) pickerOpen.value = false;}
async function chooseProject(id: string) { pickerOpen.value = false; await view(id); }
async function loadPanel() {
  if (!session.value || !["settings", "service"].includes(panel.value)) return;
  const generation = projectGeneration, currentPanel = panel.value, requestID = ++panelRequest;
  panelLoading.value = true; panelError.value = "";
  try {
    if (currentPanel === "settings" && route.value.query.get("section") === "storage") return;
    if (currentPanel === "settings" && selected.value && session.value.admin && route.value.query.get("section") === "members") {
      const [memberPage, tokenPage] = await Promise.all([request(`/api/v1/projects/${encodeURIComponent(selected.value.id)}/members`), request("/api/v1/tokens")]);
      if (generation !== projectGeneration || panel.value !== currentPanel || requestID !== panelRequest) return;
      members.value = memberPage.members; tokens.value = tokenPage.tokens;
      return;
    }
    if (currentPanel === "settings" && route.value.query.get("section") === "members") return;
    if (currentPanel === "settings" && selected.value) {
      const base = `/api/v1/projects/${encodeURIComponent(selected.value.id)}`;
      const [configuration, reports, page, tokenPage] = await Promise.all([request(base + "/onboarding"), request(base + "/client-onboarding"), request(base + "/tasks?limit=100"), session.value.admin ? request("/api/v1/tokens") : Promise.resolve({ tokens: [] })]);
      if (generation !== projectGeneration || panel.value !== currentPanel || requestID !== panelRequest) return;
      projectConfiguration.value = configuration; clients.value = reports.clients; tasks.value = page.items; tokens.value = tokenPage.tokens;
    }
    if (currentPanel === "service") {
      // The page shows its own no-permission state; nothing to fetch or retry.
      if (!session.value.admin) return;
      const value = await request("/api/v1/admin/client-release" + (servicePlatform.value ? "?platform=" + encodeURIComponent(servicePlatform.value) : ""));
      if (generation !== projectGeneration || panel.value !== currentPanel || requestID !== panelRequest) return;
      serviceInfo.value = value; serviceInfoAt.value = new Date().toLocaleTimeString("zh-CN");
    }
  } catch (e) { if (generation === projectGeneration && panel.value === currentPanel && requestID === panelRequest) panelError.value = e instanceof Error ? e.message : "页面信息读取失败。"; }
  finally { if (generation === projectGeneration && panel.value === currentPanel && requestID === panelRequest) panelLoading.value = false; }
}
const session = ref<Session | null>(null);
const loading = ref(true), busy = ref(false), error = ref(""), notice = ref("");
const username = ref(""), password = ref("");
const memberProject = ref(""), memberUsername = ref(""), memberRole = ref("reader");
const tokenProject = ref(""), tokenUsername = ref(""), tokenLabel = ref("");
const tokenReplies = ref<Record<string, { secret: string; id: string }>>({});
const tokenSecret = computed(() => tokenReplies.value[selected.value?.id || ""]?.secret || "");
const tokenID = computed(() => tokenReplies.value[selected.value?.id || ""]?.id || "");
const projects = ref<Project[]>([]), tokens = ref<Token[]>([]), selected = ref<Project | null>(null);
const tasks = ref<TaskItem[]>([]), selectedTask = ref(""), artifacts = ref<Artifact[]>([]);
const taskContext = ref<TaskContext | null>(null);
const contextError = ref("");
const previewID = ref(""), previewURL = ref(""), previewText = ref(""), previewError = ref("");
const taskLookup = ref("");
let previewEpoch = 0, previewChecking = false, contextChecking = false;
let previewTimer: ReturnType<typeof setInterval> | undefined;
const roles: Record<string, string> = { admin: "管理员", executor: "执行者", reviewer: "验收者", reader: "只读者" };
const sessionChannel = typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel("ael-standalone-session");

function clearPreview() {
  previewEpoch++;
  if (previewURL.value) URL.revokeObjectURL(previewURL.value);
  previewID.value = ""; previewURL.value = ""; previewText.value = ""; previewError.value = "";
}

function clearSession() {
  projectGeneration++; workspaceMode.value = false; connectDashboard(null);
  pickerOpen.value = false; projectSearch.value = ""; clients.value = []; projectConfiguration.value = null; serviceInfo.value = null; panelError.value = "";
  session.value = null; projects.value = []; tokens.value = []; selected.value = null;
  tasks.value = []; selectedTask.value = ""; taskLookup.value = ""; artifacts.value = []; taskContext.value = null; contextError.value = ""; clearPreview();
  tokenReplies.value = {}; tokenProject.value = ""; tokenUsername.value = ""; tokenLabel.value = "";
  memberProject.value = ""; memberUsername.value = ""; memberRole.value = "reader";
}

if (sessionChannel) sessionChannel.onmessage = (event) => {
  if (event.data === "changed" && session.value) {
    clearSession();
    error.value = "登录状态已在其他页面变化，请重新登录。";
  }
};

async function request(path: string, body?: unknown) {
  const expectedSession = session.value?.csrf || "";
  const expectedGeneration = projectGeneration;
  const expectedTask = selectedTask.value;
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? {} : {
      "Content-Type": "application/json", "X-AEL-Protocol": "1",
      ...(session.value?.csrf ? { "X-AEL-CSRF": session.value.csrf } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (expectedSession && (session.value?.csrf !== expectedSession || response.headers.get("X-AEL-Session-Context") !== expectedSession)) {
    if (session.value?.csrf === expectedSession) clearSession();
    throw new Error("登录状态已变化，请重新打开工作台。");
  }
  if (path.startsWith("/api/v1/projects/") && (expectedGeneration !== projectGeneration || ((/\/artifacts\/artifact_[^/?]+(?:\/content)?(?:\?|$)/.test(path) || path.includes("/contexts") || /\/tasks\/[^/]+\/artifacts/.test(path)) && expectedTask !== selectedTask.value))) throw new Error("项目或任务已切换，旧请求已失效。");
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    if (response.status === 401 && session.value) clearSession();
    const message: Record<string, string> = {
      QUOTA_EXCEEDED: "项目附件容量已达上限，请在项目设置的存储与附件中查看占用或调整额度。",
      QUOTA_TOO_SMALL: "新上限小于已占用和已预留容量，请提高上限或先处理上传与可回收原件。",
      REVISION_CONFLICT: "设置已被其他操作修改，请刷新后重新保存。",
      PRUNE_STALE: "可回收范围已变化，请重新预览。",
      UPLOAD_CONFLICT: "上传状态已变化，请刷新后核对。",
      INVALID_CREDENTIALS: "账号或密码不正确。", LOGIN_THROTTLED: "尝试次数过多，请稍后再试。",
      AUTH_REQUIRED: "登录已失效，请重新登录。", FORBIDDEN: "当前账号没有执行此操作的权限。",
      PROJECT_REJECTED: "项目未创建，请检查项目 ID 是否有效且未被占用。",
      USER_REJECTED: "账号未创建，请检查用户名和至少 12 位的密码。",
      MEMBERSHIP_REJECTED: "成员未添加，请检查项目和用户名。", TOKEN_REJECTED: "令牌操作未完成，请检查成员和项目。",
    };
    throw Object.assign(new Error(message[data.code] || `请求未完成（${data.code || response.status}）。`), { code: data.code });
  }
  return response.json();
}

async function refresh() {
  projects.value = (await request("/api/v1/projects")).projects;
  tokens.value = session.value?.admin ? (await request("/api/v1/tokens")).tokens : [];
  if (selected.value) {
    if (!projects.value.some((project) => project.id === selected.value?.id)) {
      selected.value = null; tasks.value = []; selectedTask.value = ""; artifacts.value = []; taskContext.value = null; contextError.value = ""; clearPreview();
      return;
    }
    const base = `/api/v1/projects/${encodeURIComponent(selected.value.id)}`;
    tasks.value = (await request(`${base}/tasks?limit=100`)).items;
    if (selectedTask.value) {
      artifacts.value = (await request(`${base}/tasks/${encodeURIComponent(selectedTask.value)}/artifacts`)).items;
      await loadTaskContext(base, selectedTask.value);
    }
    clearPreview();
  }
}

// The Server answers the same way for a missing project and one this account
// may not read, so both possibilities are stated; no empty task list is shown.
function unreachableProject(id: string) { return "无法打开链接中的项目「" + id + "」：项目不存在、已移除，或当前账号没有访问权限。可以选择下面仍可访问的项目。"; }
async function copyAccessCheck() {
  const text = "AEL 授权核对\n服务地址：" + location.origin + "\n账号：" + (session.value?.username || "") + "\n当前没有可访问的项目，请服务管理员核对项目成员授权。";
  toast(await copyText(text) ? "已复制授权核对信息" : "浏览器未允许复制，请手动记录服务地址与账号");
}
async function restore() {
  try {
    session.value = await request("/api/v1/auth/session");
    await refresh();
    const wanted = new URL(location.hash.slice(1) || "/tasks", location.origin).searchParams.get("project") || sessionStorage.getItem("ael-remote-project:" + session.value?.csrf);
    if (wanted && !projects.value.some(project => project.id === wanted)) error.value = unreachableProject(wanted);
    else if (wanted || projects.value[0]) await view(wanted || projects.value[0].id);
    else await loadPanel();
  } catch {
    clearSession();
  } finally {
    loading.value = false;
  }
}

async function login() {
  busy.value = true; error.value = ""; notice.value = "";
  try {
    await request("/api/v1/auth/login", { username: username.value, password: password.value });
    sessionChannel?.postMessage("changed");
    session.value = await request("/api/v1/auth/session");
    await refresh();
    const wanted = route.value.query.get("project");
    const first = projects.value.find(project => project.id === wanted) || projects.value[0];
    if (first) await view(first.id); else await loadPanel();
  } catch (e) { error.value = e instanceof Error ? e.message : "登录未完成。"; }
  finally { password.value = ""; busy.value = false; }
}

async function logout() {
  busy.value = true; error.value = "";
  try {
    await request("/api/v1/auth/logout", {});
    sessionChannel?.postMessage("changed");
    clearSession();
  } catch (e) { error.value = e instanceof Error ? e.message : "退出未完成。"; }
  finally { busy.value = false; }
}

async function action(operation: () => Promise<void>, message: string) {
  const generation = projectGeneration, identity = session.value?.csrf;
  busy.value = true; error.value = ""; notice.value = "";
  try {
    await operation();
    if (generation !== projectGeneration || identity !== session.value?.csrf) return;
    await refresh();
    if (generation === projectGeneration && identity === session.value?.csrf) notice.value = message;
  } catch (e) { if (generation === projectGeneration && identity === session.value?.csrf) error.value = e instanceof Error ? e.message : "操作未完成。"; }
  finally { if (generation === projectGeneration && identity === session.value?.csrf) busy.value = false; }
}

async function serviceProjectCreated(id: string) { await refresh(); await view(id); }
function changeServicePlatform(value: string) { servicePlatform.value = value; void loadPanel(); }
async function addMember() {
  await action(async () => { await request(`/api/v1/projects/${encodeURIComponent(memberProject.value)}/members`, { username: memberUsername.value, role: memberRole.value }); memberUsername.value = ""; }, "项目成员已更新。");
}
async function createToken() {
  const project = tokenProject.value;
  delete tokenReplies.value[project];
  await action(async () => {
    const result = await request("/api/v1/tokens", { username: tokenUsername.value, project_id: project, label: tokenLabel.value });
    tokenReplies.value[project] = { secret: result.token, id: result.id };
    if (selected.value?.id === project) tokenLabel.value = "";
  }, "令牌已创建，请立即妥善保存。");
}

async function revoke(id: string) {
  await action(async () => { await request("/api/v1/tokens/revoke", { id }); }, "令牌已撤销。");
}
async function view(id: string) {
  const epoch = ++projectGeneration;
  busy.value = true;
  error.value = "";
  workspaceMode.value = false;
  connectDashboard(null);
  clearPreview();
  selectedTask.value = ""; artifacts.value = []; taskContext.value = null;
  clients.value = []; projectConfiguration.value = null; panelError.value = "";
  try {
    const project = await request(`/api/v1/projects/${encodeURIComponent(id)}`);
    if (epoch !== projectGeneration || !session.value) return;
    selected.value = project;
    memberProject.value = project.id; tokenProject.value = project.id;
    connectDashboard(project, session.value.csrf, clearSession, () => {
      projectGeneration++; workspaceMode.value = false; selected.value = null; connectDashboard(null);
      clients.value = []; projectConfiguration.value = null; selectedTask.value = ""; artifacts.value = []; taskContext.value = null; clearPreview();
      tokenReplies.value = {}; notice.value = "";
      error.value = "当前项目访问权限已变化，请选择有权限的项目。";
      void refresh().catch(() => { error.value = "项目权限核对未完成，请刷新或重新登录。"; });
    });
    sessionStorage.setItem("ael-remote-project:" + session.value.csrf, id);
    const url = new URL(location.hash.slice(1) || "/tasks", location.origin);
    if (url.searchParams.get("project") !== id) navigate("/tasks?project=" + encodeURIComponent(id));
    workspaceMode.value = true;
    await loadPanel();
  } catch (e) { if (epoch === projectGeneration) error.value = String(e); }
  finally { if (epoch === projectGeneration) busy.value = false; }
}

async function loadTaskContext(base: string, id: string) {
  taskContext.value = null; contextError.value = "";
  try { taskContext.value = await request(`${base}/contexts?task=${encodeURIComponent(id)}`); }
  catch (e) { contextError.value = e instanceof Error ? e.message : "依据快照读取失败。"; throw e; }
}

async function viewArtifacts(id: string) {
  if (!selected.value) return;
  busy.value = true; error.value = ""; contextError.value = ""; clearPreview(); selectedTask.value = id; artifacts.value = []; taskContext.value = null;
  try {
    artifacts.value = (await request(`/api/v1/projects/${encodeURIComponent(selected.value.id)}/tasks/${encodeURIComponent(id)}/artifacts`)).items;
    await loadTaskContext(`/api/v1/projects/${encodeURIComponent(selected.value.id)}`, id);
  } catch (e) { error.value = e instanceof Error ? e.message : "证据读取失败。"; }
  finally { busy.value = false; }
}

async function findTask() {
  if (!selected.value || !taskLookup.value.trim()) return;
  busy.value = true; error.value = ""; taskContext.value = null; contextError.value = ""; clearPreview();
  try {
    const id = taskLookup.value.trim();
    const base = `/api/v1/projects/${encodeURIComponent(selected.value.id)}`;
    const result: TaskItem = await request(`${base}/tasks/${encodeURIComponent(id)}`);
    tasks.value = [result, ...tasks.value.filter((item) => item.task.definition.id !== id)];
    selectedTask.value = id;
    artifacts.value = (await request(`${base}/tasks/${encodeURIComponent(id)}/artifacts`)).items;
    await loadTaskContext(base, id);
  } catch (e) { error.value = e instanceof Error ? e.message : "任务读取失败。"; }
  finally { busy.value = false; }
}

async function loadContextSource(source: ContextSource, event: Event) {
  const details = event.target as HTMLDetailsElement;
  if (!details.open) { source.content = undefined; source.error = ""; return; }
  if (!selected.value || !session.value || !taskContext.value?.id) return;
  const project = selected.value.id, task = selectedTask.value, context = taskContext.value.id, expectedSession = session.value.csrf;
  source.loading = true; source.content = undefined; source.error = "";
  try {
    const response = await fetch(`/api/v1/projects/${encodeURIComponent(project)}/artifacts/${encodeURIComponent(source.artifact_id)}/content`, { cache: "no-store" });
    if (session.value?.csrf !== expectedSession || response.headers.get("X-AEL-Session-Context") !== expectedSession) {
      if (session.value?.csrf === expectedSession) clearSession();
      throw new Error("登录状态已变化，请重新打开工作台。");
    }
    if (!response.ok) throw new Error("规范快照原件不可用，请重新核对。");
    const text = await response.text();
    if (session.value?.csrf !== expectedSession || selected.value?.id !== project || selectedTask.value !== task || taskContext.value?.id !== context || !details.open) return;
    source.content = text;
  } catch (e) { source.error = e instanceof Error ? e.message : "规范快照读取失败。"; }
  finally { source.loading = false; }
}

async function checkPreview() {
  if (previewChecking || !previewID.value || !selected.value || !session.value) return;
  const epoch = previewEpoch, id = previewID.value, project = selected.value.id;
  previewChecking = true;
  try {
    const item: Artifact = await request(`/api/v1/projects/${encodeURIComponent(project)}/artifacts/${encodeURIComponent(id)}`);
    if (previewEpoch === epoch && item.status !== "finalized") {
      clearPreview(); previewError.value = "原件已清理或不可用，预览已关闭。";
    }
  } catch {
    if (previewEpoch === epoch) { clearPreview(); previewError.value = "无法确认原件状态，预览已关闭。"; }
  } finally { previewChecking = false; }
}

async function checkContext() {
  if (contextChecking || !selected.value || !session.value || !taskContext.value?.id) return;
  const project = selected.value.id, task = selectedTask.value, id = taskContext.value.id;
  contextChecking = true;
  try {
    const fresh: TaskContext = await request(`/api/v1/projects/${encodeURIComponent(project)}/contexts/${encodeURIComponent(id)}`);
    if (selected.value?.id !== project || selectedTask.value !== task || taskContext.value?.id !== id) return;
    taskContext.value.validity = fresh.validity; taskContext.value.validity_reasons = fresh.validity_reasons;
    taskContext.value.changed_paths = fresh.changed_paths; taskContext.value.impact_unknown = fresh.impact_unknown;
    taskContext.value.checks = fresh.checks;
    contextError.value = "";
  } catch (e) {
    if (selected.value?.id === project && selectedTask.value === task && taskContext.value?.id === id) {
      contextError.value = e instanceof Error ? e.message : "依据快照无法核对。";
    }
  } finally { contextChecking = false; }
}

async function preview(item: Artifact) {
  if (!selected.value || !session.value) return;
  clearPreview(); busy.value = true;
  const expectedSession = session.value.csrf, project = selected.value.id, task = selectedTask.value;
  try {
    const response = await fetch(`/api/v1/projects/${encodeURIComponent(project)}/artifacts/${encodeURIComponent(item.id)}/content`, { cache: "no-store" });
    if (session.value?.csrf !== expectedSession || response.headers.get("X-AEL-Session-Context") !== expectedSession) {
      if (session.value?.csrf === expectedSession) clearSession();
      throw new Error("登录状态已变化，请重新打开工作台。");
    }
    if (!response.ok) {
      if (response.status === 401) clearSession();
      throw new Error("原件不可用，可能已被清理或内容校验失败。请刷新列表。");
    }
    if (selected.value?.id !== project || selectedTask.value !== task) return;
    if (item.mime === "image/png" || item.mime === "image/jpeg") {
      const blob = await response.blob();
      if (session.value?.csrf !== expectedSession || selected.value?.id !== project || selectedTask.value !== task) return;
      previewURL.value = URL.createObjectURL(blob);
    } else if (item.mime === "text/plain") {
      const text = await response.text();
      if (session.value?.csrf !== expectedSession || selected.value?.id !== project || selectedTask.value !== task) return;
      previewText.value = text;
    } else {
      throw new Error("此类型不支持页面预览，请下载原件。");
    }
    previewID.value = item.id;
  } catch (e) { previewError.value = e instanceof Error ? e.message : "证据读取失败。"; }
  finally { busy.value = false; }
}

watch(() => session.value?.username, (name) => { currentUser.value = name || ""; }, { immediate: true });
watch(() => route.value.key, () => {
  pickerOpen.value = false;
  closeAccount();
  const id = route.value.query.get("project");
  if (id && workspaceMode.value && id !== selected.value?.id) {
    if (projects.value.some(project => project.id === id)) void view(id);
    else { workspaceMode.value = false; selected.value = null; connectDashboard(null); error.value = unreachableProject(id); }
    return;
  }
  void loadPanel();
});
watch(refreshRevision, () => { void loadPanel(); });
onMounted(() => { document.addEventListener("click", closePicker); void restore(); previewTimer = setInterval(() => { void checkPreview(); void checkContext(); }, 2000); });
onUnmounted(() => { document.removeEventListener("click", closePicker); if (previewTimer) clearInterval(previewTimer); clearPreview(); sessionChannel?.close(); });
</script>

<template>
  <WorkspaceApp v-if="session && selected && workspaceMode" :key="selected.id + session.csrf">
    <template #workspace>
      <div ref="pickerRoot" class="wb-project" @keydown.esc.stop.prevent="pickerOpen = false; pickerRoot?.querySelector('button')?.focus()">
        <button class="wb-project-switch" aria-label="切换项目" :aria-expanded="pickerOpen" aria-controls="remote-project-options" @click.stop="openPicker">
          <span class="wb-project-symbol"><AppIcon name="sparkles" /></span>
          <span class="wb-project-text"><strong>{{ selected.name }}</strong><small>{{ selected.id }}</small></span><AppIcon name="chevron" />
        </button>
        <div v-if="pickerOpen" id="remote-project-options" class="wb-popover project" role="region" aria-label="选择项目">
          <div class="wb-popover-header"><strong>切换项目</strong><p>每个项目独立展示任务与资料</p></div>
          <label v-if="projects.length > 6" class="wb-popover-search"><span class="visually-hidden">搜索项目</span><AppIcon name="search"/><input ref="pickerInput" v-model="projectSearch" type="search" placeholder="搜索项目名称或 ID" /></label>
          <p v-if="!filteredProjects.length" class="wb-empty-line">没有匹配的项目。</p>
          <div class="wb-popover-list"><button v-for="project in filteredProjects" :key="project.id" :class="{ selected: selected.id === project.id }" :aria-current="selected.id === project.id ? 'true' : undefined" @click="chooseProject(project.id)"><AppIcon name="folder" /><span>{{ project.name }}<small>{{ project.id }} · {{ roles[project.role] || project.role }}</small></span><span v-if="selected.id === project.id" class="spacer"></span><AppIcon v-if="selected.id === project.id" name="tick" /></button></div>
          <template v-if="session.admin"><hr /><a :href="href('/service')" @click="pickerOpen = false"><AppIcon name="plus" />注册或管理项目</a></template>
        </div>
      </div>
    </template>
    <template #sidebar-controls>
      <a class="wb-nav-item" :class="{ selected: panel === 'settings' }" :href="href('/settings')" :aria-current="panel === 'settings' ? 'page' : undefined"><AppIcon name="settings"/><span>项目设置</span></a>
      <a v-if="session.admin" class="wb-nav-item" :class="{ selected: panel === 'service' }" :href="href('/service')" :aria-current="panel === 'service' ? 'page' : undefined"><AppIcon name="server"/><span>服务管理</span></a>
    </template>
    <template #account>
      <div class="wb-owner">
        <MenuPopover ref="accountMenu" label="账户菜单" menu-id="account-options" menu-class="owner" trigger-class="wb-owner-button">
          <template #trigger><span class="avatar">{{ session.username.slice(0, 1).toUpperCase() }}</span><span><strong>{{ session.username }}</strong><small>{{ ownerRoles[selected.role] || selected.role }}</small></span><span class="spacer"></span><AppIcon name="more" /></template>
          <div class="wb-popover-header"><strong>{{ session.username }}</strong><p>{{ ownerRoles[selected.role] || selected.role }}{{ session.admin ? " · 服务管理员" : "" }}</p></div>
          <a role="menuitem" :href="href('/account')"><AppIcon name="user" />个人设置</a>
          <button role="menuitem" @click="copyConnection"><AppIcon name="copy" />复制项目连接信息</button>
          <button role="menuitem" @click="switchTheme"><AppIcon name="sun" />{{ theme === "indigo" ? "切换为石墨外观" : "切换为靛蓝外观" }}</button>
          <hr />
          <button role="menuitem" class="danger" data-keep-open :disabled="busy" @click="logout"><AppIcon name="logout" />{{ busy ? "正在退出…" : "退出登录" }}</button>
        </MenuPopover>
      </div>
    </template>
    <template #page-actions="{ area }"><a v-if="area === 'materials'" class="button ghost" :href="href('/settings', { section: 'storage' })"><AppIcon name="database" />存储管理</a><a v-else-if="area === 'settings'" class="wb-header-project" :href="href('/settings')"><AppIcon name="folder" />{{ selected.id }}</a><span v-else-if="area === 'service' && session.admin" class="tag">管理员可见</span></template>
    <template #workspace-feedback><p v-if="error" role="alert" class="wb-banner error">{{ error }}</p><p v-if="notice" role="status" class="wb-banner ok">{{ notice }}</p></template>
    <template #remote-page="{ area }">
      <p v-if="panelLoading" role="status" class="secondary">正在读取页面信息…</p>
      <p v-if="panelError" role="alert" class="wb-banner error">{{ panelError }} <button class="small" @click="loadPanel">重试</button></p>
      <MaterialsPage v-if="area === 'materials'" :key="'materials' + selected.id + session.csrf" />
      <template v-else-if="area === 'settings'">
        <nav class="wb-page-tabs" aria-label="项目设置分区">
          <a :href="href('/settings')" :class="{ selected: settingsTab === 'connection' }" :aria-current="settingsTab === 'connection' ? 'page' : undefined">项目接入</a>
          <a :href="href('/settings', { section: 'members' })" :class="{ selected: settingsTab === 'members' }" :aria-current="settingsTab === 'members' ? 'page' : undefined">成员与令牌</a>
          <a :href="href('/settings', { section: 'storage' })" :class="{ selected: settingsTab === 'storage' }" :aria-current="settingsTab === 'storage' ? 'page' : undefined">存储管理</a>
        </nav>
        <ProjectStoragePanel v-if="settingsTab === 'storage'" :key="selected.id + session.csrf" :project-id="selected.id" :admin="session.admin" :request="request" />
        <template v-else-if="settingsTab === 'members'">
          <section v-if="!session.admin" class="wb-empty"><AppIcon name="users" /><h3>成员与令牌由服务管理员管理</h3><p>你在本项目的角色是「{{ roles[selected.role] || selected.role }}」。需要调整权限或签发 Agent 令牌时，请联系服务管理员。</p></section>
          <template v-else>
            <section class="wb-card" aria-labelledby="members-title">
              <div class="wb-card-head"><div><h2 id="members-title">项目成员 <span class="muted">{{ members.length }}</span></h2><p>权限只在本项目生效；执行与验收责任分别授权。服务管理员可访问全部项目，不在此列出。</p></div><button class="primary" @click="openMemberDialog()"><AppIcon name="plus" />添加成员</button></div>
              <div v-if="!members.length && !panelLoading" class="wb-empty"><h3>还没有项目成员</h3><p>添加已有账户为执行者、验收者或只读者。</p></div>
              <div v-for="m in members" :key="m.username" class="wb-setting-row"><span class="avatar">{{ m.username.slice(0, 1).toUpperCase() }}</span><div><strong>{{ m.username }}</strong><small>{{ projectTokens.filter((t) => t.username === m.username && !t.revoked).length }} 个有效令牌</small></div><span class="tag" :class="m.role === 'reviewer' ? 'blue' : ''">{{ roles[m.role] || m.role }}</span><button class="small ghost" @click="openMemberDialog(m.username, m.role)">调整角色</button></div>
            </section>
            <section class="wb-card" aria-labelledby="tokens-title">
              <div class="wb-card-head"><div><h2 id="tokens-title">Agent 访问令牌</h2><p>令牌继承所属成员的项目角色，不额外授予权限；完整内容只在创建时显示一次。</p></div><button :disabled="!members.length" @click="openTokenDialog"><AppIcon name="key" />创建令牌</button></div>
              <div v-if="!projectTokens.length" class="wb-empty"><h3>尚无令牌</h3><p>为成员创建令牌后，Agent 可用共享 CLI 连接本项目。</p></div>
              <div v-for="item in projectTokens" :key="item.id" class="wb-setting-row"><span class="wb-setting-icon"><AppIcon name="key" /></span><div><strong>{{ item.label }}</strong><small>{{ item.username }} · 继承{{ roles[members.find((m) => m.username === item.username)?.role || ''] || '成员角色' }} · {{ item.revoked ? '已撤销' : '有效' }}</small></div><button v-if="!item.revoked" class="small danger" :disabled="busy" @click="confirmRevoke(item)">撤销</button><span v-else class="tag">已撤销</span></div>
            </section>
          </template>
        </template>
        <template v-else>
          <section class="wb-card" aria-labelledby="connect-title">
            <div class="wb-card-head"><div><h2 id="connect-title">在新设备接入项目</h2><p>使用共享的轻量 CLI，让已有 Agent 读取同一个项目的进度与规范。</p></div><button class="primary" @click="copyConnectionInfo"><AppIcon name="copy" />复制接入信息</button></div>
            <dl class="wb-facts"><div><dt>中心服务</dt><dd>{{ locationOrigin }}</dd></div><div><dt>项目标识</dt><dd><code>{{ selected.id }}</code></dd></div><div><dt>当前权限</dt><dd>{{ ownerRoles[selected.role] || selected.role }} <small class="muted">项目范围{{ session.admin ? ' · 服务管理员' : '' }}</small></dd></div><div><dt>规范资料</dt><dd>{{ specificationNames[projectConfiguration?.specification_state] || (panelLoading ? '正在读取' : '待核对') }}</dd></div></dl>
            <div class="wb-hint-row"><AppIcon name="terminal" /><span>在工程目录运行 <code>ael init --server {{ locationOrigin }} --remote-project {{ selected.id }}</code> 绑定已有项目。工程编译与验证仍由本机 Agent 执行。</span><a class="text-button" :href="href('/local-setup')">查看接入指引</a></div>
            <details v-if="projectConfiguration" class="wb-disclosure"><summary>协议与检查配置</summary><div class="wb-disclosure-body"><p>API {{ projectConfiguration.api_protocol }} · Skill {{ projectConfiguration.skill_protocol }} · 政策来源 {{ projectConfiguration.configuration_source }}</p><p v-if="projectConfiguration.critical_knowledge?.length">关键依据：{{ projectConfiguration.critical_knowledge.join('、') }}</p><p v-for="check in projectConfiguration.checks || []" :key="check.id">{{ check.id }} · {{ check.kind }} · {{ check.method || '未声明方法' }}</p></div></details>
          </section>
          <section class="wb-card" aria-labelledby="clients-title">
            <div class="wb-card-head"><div><h2 id="clients-title">最近接入的客户端</h2><p>展示最近一次接入或会话准备上报，不代表 Agent 或本机进程当前在线。</p></div><button class="ghost small" :disabled="panelLoading" @click="loadPanel"><AppIcon name="refresh" />刷新</button></div>
            <div v-if="!clients.length && !panelLoading" class="wb-empty"><h3>尚无接入上报</h3><p>在设备上运行 ael init 接入后，最近一次上报会出现在这里。</p></div>
            <div v-for="client in clients" :key="client.client_id" class="wb-setting-row wb-client-row">
              <span class="wb-setting-icon"><AppIcon :name="client.session_id ? 'terminal' : 'monitor'" /></span>
              <div><strong>{{ client.runner || client.actor }}</strong><small>CLI {{ client.cli_version }} · Skill {{ client.skill_version }} · {{ policyNames[client.update_policy] || client.update_policy }}</small></div>
              <div class="wb-client-state"><small>{{ relativeShort(client.reported_at) }}上报</small><small :class="client.compatible ? 'ok' : 'warn'">{{ client.compatible ? '兼容当前服务' : '客户端版本不兼容' }}</small></div>
              <details class="wb-client-more"><summary class="button small ghost">查看信息</summary><div class="wb-popover client"><p>{{ client.session_id ? '工具会话' : '初始化接入' }} · {{ stamp(client.reported_at) }}</p><p>{{ specificationNames[client.specification_state] || '资料状态待核对' }} · {{ hookSummary(client) }}</p><p>客户端 {{ client.client_id }}</p><p v-if="client.session_id">会话 {{ client.session_id }}</p><p v-if="client.cli_build">构建 {{ client.cli_build }}</p></div></details>
            </div>
          </section>
          <section class="wb-card"><div class="wb-card-head"><div><h2>项目约定</h2><p>Agent 按当前任务读取关联的现行规范与决策依据。</p></div><a class="button ghost" :href="href('/knowledge')">查看规范</a></div></section>
          <details class="wb-card wb-settings-disclosure"><summary>按任务检索执行依据快照与附件（排查用）</summary>        <section v-if="selected" class="remote-evidence-search" aria-label="项目证据">
          <h2>{{ selected.name }} · 任务与证据</h2>
          <form class="standalone-evidence-lookup" @submit.prevent="findTask"><label>按任务 ID 查找<input v-model="taskLookup" required /></label><button type="submit" :disabled="busy">查找任务</button></form>
          <p v-if="!tasks.length" class="secondary">此项目暂无任务。</p>
          <ul v-else class="standalone-list">
            <li v-for="item in tasks" :key="item.task.definition.id">
              <div><strong>{{ item.task.definition.title }}</strong><small>{{ item.task.definition.id }} · {{ item.task.status }}</small></div>
              <div class="standalone-evidence-actions"><a :href="taskHref(item.task.definition.id, 'checks')">任务详情与证据</a><button type="button" :disabled="busy" @click="viewArtifacts(item.task.definition.id)">读取快照</button></div>
            </li>
          </ul>
          <div v-if="selectedTask" class="standalone-evidence">
            <section class="standalone-context" aria-label="执行依据快照">
              <h3>执行依据快照</h3>
              <p v-if="contextError" class="standalone-error" role="alert">依据快照无法读取：{{ contextError }}</p>
              <p v-else-if="!taskContext" class="secondary">正在核对依据快照…</p>
              <p v-else-if="taskContext.status === 'empty'" class="secondary">此任务尚无已发布的执行依据快照。</p>
              <template v-else>
                <p class="secondary">仓库 {{ taskContext.repository }} · 提交 {{ taskContext.commit }}</p>
                <p class="secondary">候选 {{ taskContext.candidate_id }} · Runner 标识 {{ taskContext.runner_id }} · 工作区 {{ taskContext.workspace_id }} · 上报者 {{ taskContext.actor }}</p>
                <p class="secondary">来源清单由客户端报告；本记录本身不表示检查已执行或通过。</p>
                <p class="secondary">这是采集时的观测，不证明之后工作区持续未变；检查复用需要额外证明。</p>
                <p v-if="taskContext.inventory_unverified" class="secondary">部分已删除或未分类来源无法确认，依据完整性尚未证明。</p>
                <p v-if="taskContext.validity === 'stale'" class="standalone-error" role="alert">此快照已过期：{{ taskContext.validity_reasons?.join('；') }}。请重新核对并发布。</p>
                <p v-if="taskContext.changed_paths?.length" class="secondary">变化路径：{{ taskContext.changed_paths.join('、') }}</p>
                <p v-if="taskContext.impact_unknown" class="secondary">其他受影响范围尚不能确定，需要重新核对。</p>
                <p v-if="taskContext.validity !== 'stale'" class="secondary">服务端记录与当前任务版本一致；使用前仍需核对本机工作区。</p>
                <p v-if="!taskContext.inputs_known" class="secondary">存在未纳入清单的忽略输入，不能据此证明检查输入等价。</p>
                <p v-if="!taskContext.sources.length && !taskContext.local_sources?.length" class="secondary">该范围没有选中的规范依据。</p>
                <details v-if="taskContext.local_sources?.length">
                  <summary>工作区中的规范依据（{{ taskContext.local_sources.length }} 项）</summary>
                  <ul class="secondary">
                    <li v-for="source in taskContext.local_sources" :key="source.id">{{ source.source }} · 正文保存在执行工作区 · {{ source.version }}</li>
                  </ul>
                </details>
                <details v-for="source in taskContext.sources" :key="source.id" class="standalone-source" @toggle="loadContextSource(source, $event)">
                  <summary>{{ source.id }} · 记录状态：{{ source.status }}</summary>
                  <p class="secondary">来源 {{ source.path }} · SHA-256 {{ source.sha256 }}</p>
                  <p v-if="source.origin && source.origin !== source.path" class="secondary">原始依据：{{ source.origin }}</p>
                  <p class="secondary">适用理由：{{ source.reason }}</p>
                  <p v-if="source.loading" class="secondary">正在读取规范原文…</p>
                  <p v-else-if="source.error" class="standalone-error" role="alert">{{ source.error }}</p>
                  <pre v-else-if="source.content !== undefined">{{ source.content }}</pre>
                </details>
                <div v-if="taskContext.checks?.length" class="standalone-checks">
                  <h4>客户端报告的检查</h4>
                  <p class="secondary">报告保留候选和检查配置；不会自动通过门禁或证明环境等价。</p>
                  <ul class="standalone-list"><li v-for="check in taskContext.checks" :key="check.id">
                    <div><strong>{{ check.check_id }}</strong><small>{{ check.actor }} · 配置 {{ check.config_sha256 }} · 报告 {{ check.artifact_sha256 }}</small><small v-if="check.artifact_status !== 'finalized'">报告原件不可用或已清理</small></div>
                    <a v-if="check.artifact_status === 'finalized'" :href="`/api/v1/projects/${encodeURIComponent(selected.id)}/artifacts/${encodeURIComponent(check.artifact_id)}/content`" download>下载报告</a>
                  </li></ul>
                </div>
              </template>
            </section>
            <h3>证据 · {{ selectedTask }}</h3>
            <p v-if="!artifacts.length" class="secondary">此任务尚无已完成上传的证据。</p>
            <ul v-else class="standalone-list">
              <li v-for="item in artifacts" :key="item.id">
                <div><strong>{{ item.filename }}</strong><small>{{ item.id }} · {{ item.size }} 字节 · {{ item.sha256 }}</small><small v-if="item.status !== 'finalized'">{{ item.status === 'purged' ? '原件已清理' : '原件不可用' }}</small></div>
                <div v-if="item.status === 'finalized'" class="standalone-evidence-actions">
                  <button v-if="['image/png', 'image/jpeg', 'text/plain'].includes(item.mime)" type="button" :disabled="busy" @click="preview(item)">预览</button>
                  <a :href="`/api/v1/projects/${encodeURIComponent(selected.id)}/artifacts/${encodeURIComponent(item.id)}/content`" download>下载</a>
                </div>
              </li>
            </ul>
            <p v-if="previewError" class="standalone-error" role="alert">{{ previewError }}</p>
            <div v-if="previewID" class="standalone-preview" aria-label="证据预览">
              <img v-if="previewURL" :src="previewURL" alt="证据图片" />
              <pre v-else>{{ previewText }}</pre>
            </div>
          </div>
        </section>
</details>
        </template>
        <dialog ref="memberDialog" class="wb-dialog" aria-labelledby="member-dialog-title" @cancel.prevent="memberDialog?.close()">
          <form class="wb-dialog-frame" @submit.prevent="saveMember">
            <div class="wb-dialog-heading"><h2 id="member-dialog-title">{{ memberEditing ? '调整成员角色' : '添加项目成员' }}</h2><button type="button" class="icon-button" aria-label="关闭" @click="memberDialog?.close()"><AppIcon name="x" /></button></div>
            <div class="wb-dialog-body wb-form">
              <p class="wb-dialog-text">成员必须是服务中已有的账户；角色只在「{{ selected.name }}」生效。</p>
              <label>用户名<input v-model="memberUsername" required :readonly="memberEditing" autocomplete="off" /></label>
              <label>项目角色<select v-model="memberRole"><option value="executor">执行者 · 领取与提交工作</option><option value="reviewer">验收者 · 记录人工验收</option><option value="reader">只读者 · 查看进度与资料</option></select></label>
              <p v-if="error" class="wb-banner error" role="alert">{{ error }}</p>
            </div>
            <div class="wb-dialog-footer"><button type="button" @click="memberDialog?.close()">取消</button><button class="primary" :disabled="busy">{{ busy ? '正在保存…' : '保存成员' }}</button></div>
          </form>
        </dialog>
        <dialog ref="tokenDialog" class="wb-dialog" aria-labelledby="token-dialog-title" @cancel.prevent="closeTokenDialog">
          <form class="wb-dialog-frame" @submit.prevent="saveToken">
            <div class="wb-dialog-heading"><h2 id="token-dialog-title">创建 Agent 令牌</h2><button type="button" class="icon-button" aria-label="关闭" @click="closeTokenDialog"><AppIcon name="x" /></button></div>
            <div class="wb-dialog-body wb-form">
              <template v-if="!tokenSecret">
                <p class="wb-dialog-text">令牌只用于本项目，权限继承所选成员的当前角色。</p>
                <label>所属成员<select v-model="tokenUsername" required><option v-for="m in members" :key="m.username" :value="m.username">{{ m.username }} · {{ roles[m.role] || m.role }}</option></select></label>
                <label>用途<input v-model="tokenLabel" required maxlength="100" placeholder="例如：云电脑验收环境" /></label>
              </template>
              <template v-else>
                <p class="wb-banner ok" role="status">令牌已创建。完整内容只显示这一次，关闭后无法再查看。</p>
                <label>新令牌<textarea :value="tokenSecret" rows="3" readonly aria-label="新令牌" /></label>
                <p class="wb-dialog-note">令牌 ID：{{ tokenID }}。在设备上运行 <code>ael server profile add --url {{ locationOrigin }} --token-stdin</code> 时粘贴。</p>
              </template>
              <p v-if="error" class="wb-banner error" role="alert">{{ error }}</p>
            </div>
            <div class="wb-dialog-footer"><template v-if="!tokenSecret"><button type="button" @click="closeTokenDialog">取消</button><button class="primary" :disabled="busy || !tokenUsername">{{ busy ? '正在创建…' : '创建令牌' }}</button></template><template v-else><button type="button" @click="copyToken"><AppIcon name="copy" />复制令牌</button><button type="button" class="primary" @click="closeTokenDialog">我已保存</button></template></div>
          </form>
        </dialog>
        <dialog ref="revokeDialog" class="wb-dialog" aria-labelledby="revoke-title" @cancel.prevent="revokeDialog?.close()">
          <div class="wb-dialog-frame">
            <div class="wb-dialog-heading"><h2 id="revoke-title">撤销令牌？</h2></div>
            <div class="wb-dialog-body"><p class="wb-dialog-text">撤销「{{ revokeTarget?.label }}」（{{ revokeTarget?.username }}）后，使用它的设备将无法继续访问本项目。已提交的记录保留。</p></div>
            <div class="wb-dialog-footer"><button @click="revokeDialog?.close()">保留令牌</button><button class="danger" :disabled="busy" @click="doRevoke">确认撤销</button></div>
          </div>
        </dialog>
      </template>
      <RemoteAccountPanel v-else-if="area === 'account'" :username="session.username" :admin="session.admin" :role="ownerRoles[selected.role] || selected.role" :project="selected.name" :busy="busy" @logout="logout" />
      <template v-else-if="area === 'service'"><section v-if="!session.admin" class="wb-empty" role="alert"><AppIcon name="lock" /><h3>当前账号没有服务管理权限</h3><p>服务管理仅对服务管理员开放；项目角色不包含这项权限。</p><a class="button" :href="href('/tasks')">返回工作台</a></section><RemoteServicePanel :projects="projects" v-else :key="session.csrf" :info="serviceInfo" :loading="panelLoading" :platform="servicePlatform" :request="request" @refresh="loadPanel" @platform="changeServicePlatform" @created="serviceProjectCreated" /></template>
    </template>
  </WorkspaceApp>
  <div v-else class="standalone-shell">
    <main id="main-content" class="wb-login-shell" :class="{ wide: session && !loading }">
      <p v-if="loading" role="status" class="muted">正在连接服务…</p>
      <section v-else-if="!session" class="wb-login-card" aria-labelledby="login-title">
        <div class="wb-login-logo"><span>A</span>EL</div>
        <p class="wb-login-tagline">工作有进展，也有来处</p>
        <h1 id="login-title">登录 AEL 工作台</h1>
        <p class="wb-login-lead">使用管理员为你创建的账号访问项目。</p>
        <p v-if="error" class="wb-banner error" role="alert">{{ error }}</p>
        <form class="wb-login-form" @submit.prevent="login">
          <label>用户名<input v-model="username" autocomplete="username" required /></label>
          <label>密码<input v-model="password" type="password" autocomplete="current-password" required /></label>
          <button class="primary" type="submit" :disabled="busy">{{ busy ? "正在登录…" : "登录" }}</button>
        </form>
        <p class="wb-login-foot">登录状态只保存在当前浏览器会话</p>
      </section>
      <template v-else>
        <header class="wb-login-top"><div class="wb-login-logo small"><span>A</span>EL</div><nav aria-label="服务与账户"><a :href="href('/tasks')">项目</a><a v-if="session.admin" href="#/service">服务管理</a><a href="#/account">个人设置</a><span class="muted">{{ session.username }}</span><button class="small" :disabled="busy" @click="logout">退出登录</button></nav></header>
        <template v-if="panel === 'account'">
          <h1>个人设置</h1><RemoteAccountPanel :username="session.username" :admin="session.admin" :busy="busy" @logout="logout" />
        </template>
        <template v-else-if="panel === 'service'">
          <h1>服务管理</h1>
          <section v-if="!session.admin" class="wb-empty" role="alert"><AppIcon name="lock" /><h3>当前账号没有服务管理权限</h3><p>服务管理仅对服务管理员开放。</p></section>
          <p v-if="panelError" role="alert" class="wb-banner error">当前无法读取服务状态：{{ panelError }}<template v-if="serviceInfo">。下方是 {{ serviceInfoAt }} 成功读取的信息，可能已过期</template>；这不说明任何本机 Agent 是否在线。<button class="small" :disabled="panelLoading" @click="loadPanel">重试连接</button></p>
          <RemoteServicePanel :projects="projects" v-if="session.admin" :key="session.csrf" :info="serviceInfo" :loading="panelLoading" :platform="servicePlatform" :request="request" @refresh="loadPanel" @platform="changeServicePlatform" @created="serviceProjectCreated" />
        </template>
        <template v-else>
          <div class="wb-page-header"><div><h1>选择项目</h1><p class="wb-lead">查看你有权访问的项目和身份。</p></div><button type="button" :disabled="busy" @click="action(refresh, '已刷新。')"><AppIcon name="refresh" />刷新</button></div>
          <p v-if="error" class="wb-banner error" role="alert">{{ error }}</p>
          <p v-if="notice" class="wb-banner ok" role="status">{{ notice }}</p>
          <section class="wb-card" aria-label="项目列表">
            <div v-if="!projects.length" class="wb-empty"><AppIcon name="folder" /><h3>当前账号还没有可访问的项目</h3><p>账号「{{ session.username }}」已登录且有效；只是还没有被加入任何项目。请服务管理员核对项目授权，这里不会自动创建项目。</p><button class="button" @click="copyAccessCheck">复制授权核对信息</button><a v-if="session.admin" class="button" href="#/service">创建项目与管理服务</a></div>
            <ul v-else class="wb-project-list">
              <li v-for="project in projects" :key="project.id">
                <span class="wb-project-symbol"><AppIcon name="folder" /></span><div><strong>{{ project.name }}</strong><small>{{ project.id }} · {{ roles[project.role] || project.role }}</small></div>
                <button type="button" :disabled="busy" @click="view(project.id)">进入项目</button>
              </li>
            </ul>
          </section>
        </template>
      </template>
    </main>
  </div>
</template>

<style scoped>
.project-settings-tabs a { padding: 10px 3px; border-bottom: 2px solid transparent; text-decoration: none; }
.project-settings-tabs a.chosen { border-color: var(--brand); }
.standalone-shell { min-height: 100vh; }
.standalone-header { display: flex; justify-content: space-between; align-items: center; gap: 20px; padding: 18px max(24px, calc((100vw - 1120px) / 2)); background: var(--paper); border-bottom: 1px solid var(--line); }
.standalone-header > div { display: flex; align-items: center; gap: 14px; }
.standalone-brand { color: var(--brand); font-size: 20px; letter-spacing: .08em; }
.standalone-account { color: var(--muted); }
.standalone-main { max-width: 1120px; margin: auto; padding: 48px 24px 72px; display: grid; gap: 20px; }
.standalone-login { width: min(100%, 430px); margin: 8vh auto 0; padding: 32px; background: var(--paper); border: 1px solid var(--line); border-radius: 10px; }
.standalone-login h1 { margin-bottom: 6px; }
.standalone-eyebrow { color: var(--brand); font-size: 12px; letter-spacing: .12em; font-weight: 700; margin-bottom: 8px; }
.standalone-title { display: flex; align-items: end; justify-content: space-between; gap: 24px; }
.standalone-card { background: var(--paper); border: 1px solid var(--line); border-radius: 8px; padding: 24px; }
.standalone-card h2 { margin-bottom: 6px; }
.standalone-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
.standalone-form { display: grid; gap: 14px; margin-top: 20px; }
.standalone-form button { justify-self: start; }
.standalone-list { list-style: none; margin: 14px 0 0; padding: 0; }
.standalone-list li { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 14px 0; border-top: 1px solid var(--line); }
.standalone-selection, .standalone-notice { color: #326e56; }
.standalone-error { color: var(--danger); margin-top: 12px; }
.standalone-secret { margin-top: 18px; padding: 14px; background: var(--tint); border-radius: 5px; overflow-wrap: anywhere; }
.standalone-evidence { margin-top: 24px; }
.standalone-context { margin-bottom: 24px; overflow-wrap: anywhere; }
.standalone-source { margin: 12px 0; }
.standalone-source pre { white-space: pre-wrap; overflow-wrap: anywhere; }
.standalone-evidence-lookup { display: flex; align-items: end; gap: 12px; margin-top: 16px; flex-wrap: wrap; }
.standalone-evidence-actions { display: flex; align-items: center; gap: 14px; }
.standalone-preview { margin-top: 18px; padding: 16px; border: 1px solid var(--line); overflow: auto; }
.standalone-preview img { display: block; max-width: 100%; height: auto; }
.standalone-preview pre { white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 700px) { .standalone-grid { grid-template-columns: 1fr; } .standalone-header { padding: 14px 18px; } .standalone-main { padding: 28px 18px 48px; } }
</style>

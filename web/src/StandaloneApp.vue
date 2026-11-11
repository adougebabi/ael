<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";

type Session = { username: string; admin: boolean; csrf: string };
type Project = { id: string; name: string; role: string };
type Token = { id: string; username: string; project_id: string; label: string; revoked: boolean };
type TaskItem = { task: { definition: { id: string; title: string }; status: string } };
type Artifact = { id: string; filename: string; mime: string; size: number; sha256: string; status: string };
type ContextSource = { id: string; path: string; origin?: string; artifact_id: string; sha256: string; status: string; kind: string; reason: string; content?: string; error?: string; loading?: boolean };
type ContextCheck = { id: string; check_id: string; artifact_id: string; artifact_status: string; artifact_sha256: string; config_sha256: string; candidate_id: string; actor: string };
type TaskContext = { status?: string; validity?: string; validity_reasons?: string[]; changed_paths?: string[]; impact_unknown?: boolean; inputs_known?: boolean; inventory_unverified?: boolean; id?: string; candidate_id?: string; repository?: string; commit?: string; checkout_id?: string; workspace_id?: string; runner_id?: string; actor?: string; sources: ContextSource[]; checks?: ContextCheck[] };

const session = ref<Session | null>(null);
const loading = ref(true), busy = ref(false), error = ref(""), notice = ref("");
const username = ref(""), password = ref("");
const projectID = ref(""), projectName = ref("");
const newUsername = ref(""), newPassword = ref("");
const memberProject = ref(""), memberUsername = ref(""), memberRole = ref("reader");
const tokenProject = ref(""), tokenUsername = ref(""), tokenLabel = ref("");
const tokenSecret = ref(""), tokenID = ref("");
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
  session.value = null; projects.value = []; tokens.value = []; selected.value = null;
  tasks.value = []; selectedTask.value = ""; taskLookup.value = ""; artifacts.value = []; taskContext.value = null; contextError.value = ""; clearPreview();
  tokenSecret.value = ""; tokenID.value = ""; tokenProject.value = ""; tokenUsername.value = ""; tokenLabel.value = "";
  projectID.value = ""; projectName.value = ""; newUsername.value = ""; newPassword.value = "";
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
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    if (response.status === 401 && session.value) clearSession();
    const message: Record<string, string> = {
      INVALID_CREDENTIALS: "账号或密码不正确。", LOGIN_THROTTLED: "尝试次数过多，请稍后再试。",
      AUTH_REQUIRED: "登录已失效，请重新登录。", FORBIDDEN: "当前账号没有执行此操作的权限。",
      PROJECT_REJECTED: "项目未创建，请检查项目 ID 是否有效且未被占用。",
      USER_REJECTED: "账号未创建，请检查用户名和至少 12 位的密码。",
      MEMBERSHIP_REJECTED: "成员未添加，请检查项目和用户名。", TOKEN_REJECTED: "令牌操作未完成，请检查成员和项目。",
    };
    throw new Error(message[data.code] || `请求未完成（${data.code || response.status}）。`);
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

async function restore() {
  try {
    session.value = await request("/api/v1/auth/session");
    await refresh();
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
  busy.value = true; error.value = ""; notice.value = "";
  try { await operation(); await refresh(); notice.value = message; }
  catch (e) { error.value = e instanceof Error ? e.message : "操作未完成。"; }
  finally { busy.value = false; }
}

async function createProject() {
  await action(async () => { await request("/api/v1/projects", { id: projectID.value, name: projectName.value }); projectID.value = ""; projectName.value = ""; }, "项目已注册。");
}
async function createUser() {
  try { await action(async () => { await request("/api/v1/users", { username: newUsername.value, password: newPassword.value }); newUsername.value = ""; }, "账号已创建。"); }
  finally { newPassword.value = ""; }
}
async function addMember() {
  await action(async () => { await request(`/api/v1/projects/${encodeURIComponent(memberProject.value)}/members`, { username: memberUsername.value, role: memberRole.value }); memberUsername.value = ""; }, "项目成员已更新。");
}
async function createToken() {
  tokenSecret.value = ""; tokenID.value = "";
  await action(async () => {
    const result = await request("/api/v1/tokens", { username: tokenUsername.value, project_id: tokenProject.value, label: tokenLabel.value });
    tokenSecret.value = result.token; tokenID.value = result.id; tokenLabel.value = "";
  }, "令牌已创建，请立即妥善保存。");
}
async function revoke(id: string) {
  await action(async () => { await request("/api/v1/tokens/revoke", { id }); }, "令牌已撤销。");
}
async function view(id: string) {
  await action(async () => { selected.value = null; tasks.value = []; selectedTask.value = ""; artifacts.value = []; taskContext.value = null; contextError.value = ""; clearPreview(); selected.value = await request(`/api/v1/projects/${encodeURIComponent(id)}`); }, "");
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
  } catch (e) {
    if (selected.value?.id === project && selectedTask.value === task && taskContext.value?.id === id) {
      taskContext.value = null; contextError.value = e instanceof Error ? e.message : "依据快照无法核对。";
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

onMounted(() => { void restore(); previewTimer = setInterval(() => { void checkPreview(); void checkContext(); }, 2000); });
onUnmounted(() => { if (previewTimer) clearInterval(previewTimer); clearPreview(); sessionChannel?.close(); });
</script>

<template>
  <div class="standalone-shell">
    <header class="standalone-header">
      <div><strong class="standalone-brand">AEL</strong><span>工程工作台</span></div>
      <div v-if="session" class="standalone-account"><span>{{ session.username }}</span><button type="button" :disabled="busy" @click="logout">退出登录</button></div>
    </header>
    <main id="main-content" class="standalone-main">
      <p v-if="loading" role="status">正在连接服务…</p>
      <template v-else-if="!session">
        <section class="standalone-login">
          <p class="standalone-eyebrow">AEL SERVER</p>
          <h1>登录工作台</h1>
          <p class="secondary">使用管理员创建的账号访问项目。</p>
          <p v-if="error" class="standalone-error" role="alert">{{ error }}</p>
          <form class="standalone-form" @submit.prevent="login">
            <label>用户名<input v-model="username" autocomplete="username" required /></label>
            <label>密码<input v-model="password" type="password" autocomplete="current-password" required /></label>
            <button class="primary" type="submit" :disabled="busy">登录</button>
          </form>
        </section>
      </template>
      <template v-else>
        <div class="standalone-title"><div><p class="standalone-eyebrow">PROJECTS</p><h1>项目</h1><p class="secondary">查看你有权访问的项目和身份。</p></div><button type="button" :disabled="busy" @click="action(refresh, '已刷新。')">刷新</button></div>
        <p v-if="error" class="standalone-error" role="alert">{{ error }}</p>
        <p v-if="notice" class="standalone-notice" role="status">{{ notice }}</p>
        <section class="standalone-card" aria-label="项目列表">
          <p v-if="!projects.length" class="secondary">当前账号还没有可访问的项目。</p>
          <ul v-else class="standalone-list">
            <li v-for="project in projects" :key="project.id">
              <div><strong>{{ project.name }}</strong><small>{{ project.id }} · {{ roles[project.role] || project.role }}</small></div>
              <button type="button" :disabled="busy" @click="view(project.id)">查看项目</button>
            </li>
          </ul>
          <p v-if="selected" class="standalone-selection">{{ selected.name }} · {{ roles[selected.role] || selected.role }}</p>
        </section>
        <section v-if="selected" class="standalone-card" aria-label="项目证据">
          <h2>{{ selected.name }} · 任务与证据</h2>
          <form class="standalone-evidence-lookup" @submit.prevent="findTask"><label>按任务 ID 查找<input v-model="taskLookup" required /></label><button type="submit" :disabled="busy">查找任务</button></form>
          <p v-if="!tasks.length" class="secondary">此项目暂无任务。</p>
          <ul v-else class="standalone-list">
            <li v-for="item in tasks" :key="item.task.definition.id">
              <div><strong>{{ item.task.definition.title }}</strong><small>{{ item.task.definition.id }} · {{ item.task.status }}</small></div>
              <button type="button" :disabled="busy" @click="viewArtifacts(item.task.definition.id)">查看证据</button>
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
                <p v-if="!taskContext.sources.length" class="secondary">该范围没有选中的规范依据。</p>
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
                  <button v-if="item.mime !== 'video/mp4'" type="button" :disabled="busy" @click="preview(item)">预览</button>
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
        <template v-if="session.admin">
          <div class="standalone-grid">
            <section class="standalone-card"><h2>注册项目</h2><form class="standalone-form" @submit.prevent="createProject"><label>项目 ID<input v-model="projectID" required pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" /></label><label>项目名称<input v-model="projectName" required maxlength="200" /></label><button class="primary" :disabled="busy">注册项目</button></form></section>
            <section class="standalone-card"><h2>创建账号</h2><form class="standalone-form" @submit.prevent="createUser"><label>用户名<input v-model="newUsername" required pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" /></label><label>初始密码<input v-model="newPassword" type="password" autocomplete="new-password" minlength="12" required /></label><button :disabled="busy">创建账号</button></form></section>
            <section class="standalone-card"><h2>设置项目成员</h2><form class="standalone-form" @submit.prevent="addMember"><label>项目<select v-model="memberProject" required><option value="" disabled>选择项目</option><option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option></select></label><label>用户名<input v-model="memberUsername" required /></label><label>角色<select v-model="memberRole"><option value="executor">执行者</option><option value="reviewer">验收者</option><option value="reader">只读者</option></select></label><button :disabled="busy">保存成员</button></form></section>
            <section class="standalone-card"><h2>创建 Agent 令牌</h2><p class="secondary">令牌只用于所选项目，权限取自当前成员角色。</p><form class="standalone-form" @submit.prevent="createToken"><label>项目<select v-model="tokenProject" required><option value="" disabled>选择项目</option><option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option></select></label><label>成员用户名<input v-model="tokenUsername" required /></label><label>用途<input v-model="tokenLabel" required maxlength="100" /></label><button :disabled="busy">创建令牌</button></form><div v-if="tokenSecret" class="standalone-secret"><strong>令牌仅显示一次</strong><p class="secondary">请现在妥善保存。令牌 ID：{{ tokenID }}</p><textarea :value="tokenSecret" rows="3" readonly aria-label="新令牌" /></div></section>
          </div>
          <section class="standalone-card"><h2>Agent 令牌</h2><p v-if="!tokens.length" class="secondary">尚无令牌。</p><ul v-else class="standalone-list"><li v-for="item in tokens" :key="item.id"><div><strong>{{ item.label }}</strong><small>{{ item.username }} · {{ item.project_id }} · {{ item.revoked ? "已撤销" : "有效" }}</small></div><button type="button" :disabled="busy || item.revoked" @click="revoke(item.id)">撤销令牌</button></li></ul></section>
        </template>
      </template>
    </main>
  </div>
</template>

<style scoped>
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

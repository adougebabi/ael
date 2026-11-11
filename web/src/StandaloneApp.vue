<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";

type Session = { username: string; admin: boolean; csrf: string };
type Project = { id: string; name: string; role: string };
type Token = { id: string; username: string; project_id: string; label: string; revoked: boolean };

const session = ref<Session | null>(null);
const loading = ref(true), busy = ref(false), error = ref(""), notice = ref("");
const username = ref(""), password = ref("");
const projectID = ref(""), projectName = ref("");
const newUsername = ref(""), newPassword = ref("");
const memberProject = ref(""), memberUsername = ref(""), memberRole = ref("reader");
const tokenProject = ref(""), tokenUsername = ref(""), tokenLabel = ref("");
const tokenSecret = ref(""), tokenID = ref("");
const projects = ref<Project[]>([]), tokens = ref<Token[]>([]), selected = ref<Project | null>(null);
const roles: Record<string, string> = { admin: "管理员", executor: "执行者", reviewer: "验收者", reader: "只读者" };
const sessionChannel = typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel("ael-standalone-session");

function clearSession() {
  session.value = null; projects.value = []; tokens.value = []; selected.value = null;
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
  await action(async () => { selected.value = await request(`/api/v1/projects/${encodeURIComponent(id)}`); }, "");
}

onMounted(restore);
onUnmounted(() => sessionChannel?.close());
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
@media (max-width: 700px) { .standalone-grid { grid-template-columns: 1fr; } .standalone-header { padding: 14px 18px; } .standalone-main { padding: 28px 18px 48px; } }
</style>

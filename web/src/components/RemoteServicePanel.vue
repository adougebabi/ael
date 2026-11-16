<script setup lang="ts">
import { ref } from "vue";
import { stamp } from "../display";

const props = defineProps<{
  info: any;
  loading: boolean;
  platform: string;
  request: (path: string, body?: unknown) => Promise<any>;
}>();
const emit = defineEmits<{ refresh: []; platform: [value: string]; created: [id: string] }>();
const projectID = ref(""), projectName = ref("");
const username = ref(""), password = ref("");
const busy = ref(false), error = ref(""), notice = ref("");
async function createProject() {
  busy.value = true; error.value = ""; notice.value = "";
  const id = projectID.value;
  try {
    await props.request("/api/v1/projects", { id, name: projectName.value });
    projectID.value = ""; projectName.value = "";
    emit("created", id);
  } catch (e) { error.value = e instanceof Error ? e.message : "项目创建未完成。"; }
  finally { busy.value = false; }
}
async function createUser() {
  busy.value = true; error.value = ""; notice.value = "";
  try {
    await props.request("/api/v1/users", { username: username.value, password: password.value });
    username.value = ""; notice.value = "账号已创建。";
  } catch (e) { error.value = e instanceof Error ? e.message : "账号创建未完成。"; }
  finally { password.value = ""; busy.value = false; }
}
</script>
<template>
  <p v-if="error" role="alert" class="notice">{{ error }}</p>
  <p v-if="notice" role="status" class="notice">{{ notice }}</p>
  <section class="standalone-card" aria-label="服务版本与发行">
    <div class="section-heading"><h2>服务版本与发行</h2><button :disabled="loading" @click="emit('refresh')">检查发行信息</button></div>
    <dl v-if="info" class="remote-facts">
      <div><dt>Server / Dashboard</dt><dd>{{ info.server_version }} · {{ info.server_build || '构建未上报' }}</dd></div>
      <div><dt>最低兼容 CLI</dt><dd>{{ info.minimum_client_version }}</dd></div>
      <div><dt>协议范围</dt><dd>API {{ info.protocol_range.api_min }}—{{ info.protocol_range.api_max }} · Skill {{ info.protocol_range.skill_min }}—{{ info.protocol_range.skill_max }}</dd></div>
      <div><dt>推荐 CLI</dt><dd>{{ info.client_release.available ? info.client_release.version : '尚无可验证推荐版本' }}</dd></div>
      <div><dt>发行来源</dt><dd>{{ info.client_release.catalog_url || '未配置正式发行源' }}</dd></div>
    </dl>
    <label>客户端平台<select :value="platform" @change="emit('platform', ($event.target as HTMLSelectElement).value)"><option value="">服务默认平台</option><option v-for="target in ['darwin-arm64', 'darwin-amd64', 'linux-arm64', 'linux-amd64']" :key="target" :value="target">{{ target }}</option></select></label>
    <details v-if="info" class="remote-diagnostics"><summary>校验与构建诊断</summary><p>发行状态 {{ info.client_release.status }}</p><p v-if="info.client_release.error_code">校验原因 {{ info.client_release.error_code }}</p><p>发布身份 {{ info.client_release.publisher_identity }}</p><p v-if="info.client_release.publisher_identity_constraint">身份约束 {{ info.client_release.publisher_identity_constraint }}</p><p v-if="info.client_release.checked_at">核对时间 {{ stamp(info.client_release.checked_at) }}</p></details>
  </section>
  <div class="standalone-grid">
    <section class="standalone-card"><h2>注册项目</h2><form class="standalone-form" @submit.prevent="createProject"><label>项目 ID<input v-model="projectID" required pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" /></label><label>项目名称<input v-model="projectName" required maxlength="200" /></label><button class="primary" :disabled="busy">注册项目</button></form></section>
    <section class="standalone-card"><h2>创建账号</h2><form class="standalone-form" @submit.prevent="createUser"><label>用户名<input v-model="username" required pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" /></label><label>初始密码<input v-model="password" type="password" autocomplete="new-password" minlength="12" required /></label><button :disabled="busy">创建账号</button></form></section>
  </div>
</template>
<style scoped>
.standalone-card { background: var(--paper); border: 1px solid var(--line); border-radius: 8px; padding: 24px; margin-bottom: 20px; }
.standalone-card h2 { margin-bottom: 6px; }
.standalone-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
.standalone-form { display: grid; gap: 14px; margin-top: 20px; }
.standalone-form button { justify-self: start; }
@media (max-width: 700px) { .standalone-grid { grid-template-columns: 1fr; } }
</style>

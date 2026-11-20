<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { stamp } from "../display";
import { toast, copyText } from "../toast";
import AppIcon from "./AppIcon.vue";

const props = defineProps<{
  info: any;
  loading: boolean;
  platform: string;
  request: (path: string, body?: unknown) => Promise<any>;
  projects?: { id: string; name: string; role: string }[];
}>();
const emit = defineEmits<{ refresh: []; platform: [value: string]; created: [id: string] }>();
const projectID = ref(""), projectName = ref("");
const username = ref(""), password = ref("");
const busy = ref(false), error = ref(""), notice = ref("");
const origin = location.origin;
const projectDialog = ref<HTMLDialogElement | null>(null), userDialog = ref<HTMLDialogElement | null>(null);
// The server validates exactly these release targets (client_release.go).
const platformNames: Record<string, string> = { "darwin-arm64": "macOS · Apple Silicon", "darwin-amd64": "macOS · Intel", "linux-arm64": "Linux · ARM64", "linux-amd64": "Linux · x86_64" };
const release = computed(() => props.info?.client_release || {});
const currentPlatform = computed(() => props.platform || release.value.platform || "");
const updateText = computed(() => release.value.available ? `AEL 客户端更新说明\n平台：${platformNames[currentPlatform.value] || currentPlatform.value}\n推荐版本：${release.value.version}\n中心服务：${origin}（最低兼容 CLI ${props.info?.minimum_client_version}）\n在自己的设备上运行：ael upgrade --version ${release.value.version}` : "");
async function copyUpdate() { toast(await copyText(updateText.value) ? "已复制更新说明；复制不会下载或执行安装" : "浏览器未允许复制"); }
async function open(dialog: HTMLDialogElement | null) { error.value = ""; await nextTick(); dialog?.showModal(); }
async function createProject() {
  busy.value = true; error.value = ""; notice.value = "";
  const id = projectID.value;
  try {
    await props.request("/api/v1/projects", { id, name: projectName.value });
    projectID.value = ""; projectName.value = ""; projectDialog.value?.close();
    emit("created", id);
  } catch (e) { error.value = e instanceof Error ? e.message : "项目创建未完成。"; }
  finally { busy.value = false; }
}
async function createUser() {
  busy.value = true; error.value = ""; notice.value = "";
  try {
    await props.request("/api/v1/users", { username: username.value, password: password.value });
    notice.value = `账户 ${username.value} 已创建。到项目设置的「成员与令牌」中为它配置项目角色。`; username.value = ""; userDialog.value?.close();
  } catch (e) { error.value = e instanceof Error ? e.message : "账户创建未完成。"; }
  finally { password.value = ""; busy.value = false; }
}
const statusText: Record<string, string> = { no_configured_source: "未配置正式发行源", verified: "已校验", unavailable: "发行信息不可用" };
</script>
<template>
  <p v-if="notice" role="status" class="wb-banner ok">{{ notice }}</p>
  <section class="wb-card" aria-labelledby="service-title">
    <div class="wb-card-head"><div><h2 id="service-title">中心服务</h2><p>Server 与 Dashboard 随同一版本交付。</p></div><span class="tag blue">{{ info?.server_build || '构建未上报' }}</span></div>
    <p v-if="loading && !info" class="muted">正在读取服务信息…</p>
    <dl v-if="info" class="wb-facts">
      <div><dt>服务地址</dt><dd>{{ origin }}</dd></div>
      <div><dt>Server / Dashboard</dt><dd>{{ info.server_version }}</dd></div>
      <div><dt>最低兼容 CLI</dt><dd>{{ info.minimum_client_version }}</dd></div>
      <div><dt>协议范围</dt><dd>API {{ info.protocol_range.api_min }}—{{ info.protocol_range.api_max }} · Skill {{ info.protocol_range.skill_min }}—{{ info.protocol_range.skill_max }}</dd></div>
    </dl>
  </section>
  <section class="wb-card" aria-labelledby="release-title">
    <div class="wb-card-head"><div><h2 id="release-title">客户端发行信息</h2><p>来自服务配置的可信发行源；复制的是更新说明，不下载或执行安装程序。</p></div><button class="ghost small" :disabled="loading" @click="emit('refresh')"><AppIcon name="refresh" />刷新信息</button></div>
    <div class="wb-release-row">
      <label>目标平台<select :value="currentPlatform" @change="emit('platform', ($event.target as HTMLSelectElement).value)"><option v-for="(name, target) in platformNames" :key="target" :value="target">{{ name }}</option></select></label>
      <div class="wb-release-version"><strong>{{ release.available ? release.version : '尚无可验证的推荐版本' }}</strong><small>{{ release.available ? '推荐 · 与当前服务兼容' : statusText[release.status] || release.status || '状态未知' }}</small></div>
      <button class="primary" :disabled="!release.available" @click="copyUpdate"><AppIcon name="copy" />复制更新说明</button>
    </div>
    <p class="wb-dialog-note">安装位置在自己的设备。客户端上报版本不代表服务可以启动或控制本地 Agent。</p>
    <details v-if="info" class="wb-disclosure"><summary>校验与构建诊断</summary><div class="wb-disclosure-body"><p>发行状态 {{ release.status }}<template v-if="release.error_code"> · 校验原因 {{ release.error_code }}</template></p><p>发行来源 {{ release.catalog_url || '未配置' }}</p><p>发布身份 {{ release.publisher_identity }}<template v-if="release.publisher_identity_constraint"> · 约束 {{ release.publisher_identity_constraint }}</template></p><p v-if="release.checked_at">核对时间 {{ stamp(release.checked_at) }}</p></div></details>
  </section>
  <div class="wb-storage-grid">
    <section class="wb-card" aria-labelledby="projects-title">
      <div class="wb-card-head"><div><h2 id="projects-title">注册项目 <span class="muted">{{ projects?.length || 0 }}</span></h2><p>为已有工程建立服务端项目入口。</p></div><button class="ghost" @click="open(projectDialog)"><AppIcon name="plus" />注册项目</button></div>
      <div v-for="p in projects || []" :key="p.id" class="wb-setting-row"><span class="wb-setting-icon"><AppIcon name="folder" /></span><div><strong>{{ p.name }}</strong><small>{{ p.id }}</small></div></div>
    </section>
    <section class="wb-card" aria-labelledby="accounts-title">
      <div class="wb-card-head"><div><h2 id="accounts-title">服务账户</h2><p>创建后仍需在项目中配置对应角色；网页不列出全部账户。</p></div><button class="ghost" @click="open(userDialog)"><AppIcon name="plus" />创建账户</button></div>
    </section>
  </div>
  <dialog ref="projectDialog" class="wb-dialog" aria-labelledby="project-dialog-title" @cancel.prevent="projectDialog?.close()">
    <form class="wb-dialog-frame" @submit.prevent="createProject">
      <div class="wb-dialog-heading"><h2 id="project-dialog-title">注册项目</h2><button type="button" class="icon-button" aria-label="关闭" @click="projectDialog?.close()"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body wb-form"><label>项目 ID<input v-model="projectID" required pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" placeholder="稳定标识，接入时使用" /></label><label>项目名称<input v-model="projectName" required maxlength="200" /></label><p v-if="error" class="wb-banner error" role="alert">{{ error }}</p></div>
      <div class="wb-dialog-footer"><button type="button" @click="projectDialog?.close()">取消</button><button class="primary" :disabled="busy">注册项目</button></div>
    </form>
  </dialog>
  <dialog ref="userDialog" class="wb-dialog" aria-labelledby="user-dialog-title" @cancel.prevent="userDialog?.close()">
    <form class="wb-dialog-frame" @submit.prevent="createUser">
      <div class="wb-dialog-heading"><h2 id="user-dialog-title">创建账户</h2><button type="button" class="icon-button" aria-label="关闭" @click="userDialog?.close()"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body wb-form"><label>用户名<input v-model="username" required pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" autocomplete="off" /></label><label>初始密码<input v-model="password" type="password" autocomplete="new-password" minlength="12" required /></label><p class="wb-dialog-note">至少 12 位。创建后到项目设置中添加为成员。</p><p v-if="error" class="wb-banner error" role="alert">{{ error }}</p></div>
      <div class="wb-dialog-footer"><button type="button" @click="userDialog?.close()">取消</button><button class="primary" :disabled="busy">创建账户</button></div>
    </form>
  </dialog>
</template>

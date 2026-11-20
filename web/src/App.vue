<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { api, info, infoError, infoLast, notices, refreshInfo, refreshCurrentView, refreshRevision, remoteProject } from "./api";
import { pollDashboard, remoteSyncError, remoteSyncAt } from "./remoteDashboard";
import { route, href } from "./router";
import TaskList from "./pages/TaskList.vue";
import TaskPage from "./pages/TaskPage.vue";
import CreateTask from "./pages/CreateTask.vue";
import KnowledgePage from "./pages/KnowledgePage.vue";
import EvolutionPage from "./pages/EvolutionPage.vue";
import SettingsPage from "./pages/SettingsPage.vue";
import AppIcon from "./components/AppIcon.vue";
import GlobalSearchDialog from "./components/GlobalSearchDialog.vue";
import LocalMaterials from "./components/LocalMaterials.vue";
import SetupGuide from "./components/SetupGuide.vue";
import { theme, applyTheme, setTheme } from "./theme";
import { toastText } from "./toast";

const area = computed(() => route.value.path.split("/")[1] || "tasks");
const id = computed(() => decodeURIComponent(route.value.path.split("/").slice(2).join("/")));
const titles: Record<string, string> = {
  tasks: "工作台", materials: "资料", knowledge: "规范与决策", evolution: "项目演进",
  settings: "项目设置", account: "个人设置", service: "服务管理", "local-setup": "接入指引",
};
const pageTitle = computed(() => area.value === "tasks" && id.value ? (id.value === "new" ? "新建任务" : "任务详情") : titles[area.value] || "页面不存在");
const subtitles: Record<string, string> = {
  knowledge: "找到当前有效的约定，也知道它为什么这样设计。",
  evolution: "把交付、决定和规范变化串起来，保留每次调整的来由。",
  settings: "管理项目接入、成员权限与存储。",
  account: "管理当前身份与阅读外观。",
  service: "查看中心服务与客户端兼容信息，管理服务中的项目与账户。",
  materials: "从工作成果找到对应的任务与依据。",
  "local-setup": "一个已有工程，几步接入统一进度。命令都在你自己的设备上执行。",
};
const projectName = computed(() => remoteProject.value?.name || info.value.project?.split("/").filter(Boolean).pop() || "正在连接");
const projectSubtitle = computed(() => remoteProject.value ? remoteProject.value.id
  : info.value.config?.mode === "shadow" ? "预览模式 · 工作流未激活" : info.value.config ? "本地项目" : "等待读取项目身份");
document.title = "AEL · 项目工作台";
watch(pageTitle, (value) => { document.title = value + " · AEL"; }, { immediate: true });

const mobileMenu = ref(false), showSearch = ref(false);
const attentionCount = ref(0), attentionTruncated = ref(false), attentionHumanOnly = ref(true);
async function refreshAttention() {
  try {
    const result = await api("/api/attention?human=1");
    attentionCount.value = result.total || 0; attentionTruncated.value = !!result.truncated;
    attentionHumanOnly.value = (result.items || []).every((item: any) => item.kind === "human");
  } catch { attentionCount.value = 0; attentionTruncated.value = false; }
}
const attentionLabel = computed(() => (attentionTruncated.value ? "≥" : "") + attentionCount.value + (attentionHumanOnly.value ? " 待验收" : " 待处理"));

// Local mode has no account; its menu keeps appearance and settings in the
// same control so the shell looks and behaves the same as remote projects.
const localMenu = ref(false), localTrigger = ref<HTMLButtonElement | null>(null), localRoot = ref<HTMLElement | null>(null);
async function toggleLocalMenu() { localMenu.value = !localMenu.value; if (localMenu.value) { await nextTick(); localRoot.value?.querySelector<HTMLElement>(".wb-popover a, .wb-popover button")?.focus(); } }
function closeLocalMenu(restore = false) { localMenu.value = false; if (restore) localTrigger.value?.focus(); }
function outside(event: MouseEvent) { if (localRoot.value && !localRoot.value.contains(event.target as Node)) localMenu.value = false; }

function focusMain() { document.getElementById("main-content")?.focus(); }
function keyboard(e: KeyboardEvent) {
  const target = e.target as HTMLElement;
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k" || e.key === "/" && !["INPUT", "TEXTAREA", "SELECT"].includes(target?.tagName) && !target?.isContentEditable) {
    e.preventDefault(); showSearch.value = true;
  }
  if (e.key === "Escape" && mobileMenu.value) mobileMenu.value = false;
}
watch(refreshRevision, () => { if (remoteProject.value) void refreshAttention(); });
watch(() => route.value.key, () => { mobileMenu.value = false; localMenu.value = false; });
let timer: ReturnType<typeof setInterval>;
onMounted(() => {
  refreshInfo(); refreshAttention(); applyTheme();
  window.addEventListener("keydown", keyboard); document.addEventListener("click", outside);
  timer = setInterval(() => { if (remoteProject.value) void pollDashboard(); else { refreshInfo(); void refreshAttention(); } }, 2000);
});
onUnmounted(() => { clearInterval(timer); window.removeEventListener("keydown", keyboard); document.removeEventListener("click", outside); });
const navItems = [["tasks", "工作台", "workbench"], ["materials", "资料", "folder"], ["knowledge", "规范与决策", "book"], ["evolution", "项目演进", "evolution"]];
</script>
<template>
  <a href="#main-content" class="wb-skip" @click.prevent="focusMain">跳到主要内容</a>
  <button v-if="mobileMenu" class="wb-scrim open" aria-label="关闭导航" @click="mobileMenu = false"></button>
  <aside class="wb-sidebar" :class="{ 'mobile-open': mobileMenu }" aria-label="项目导航">
    <a class="wb-brand" :href="href('/tasks')" aria-label="AEL 工作台" @click="mobileMenu = false">
      <span class="wb-brand-mark"><svg width="23" height="23" viewBox="0 0 32 32" fill="none" aria-hidden="true"><path d="m6 24 10-18 10 18M10 18h12" stroke="currentColor" stroke-width="2.8" stroke-linejoin="round"/></svg></span>
      <span class="wb-brand-name">ael<span class="wb-brand-sub">WORKSPACE</span></span>
    </a>
    <slot name="workspace">
      <div class="wb-project">
        <div class="wb-project-switch" role="group" aria-label="当前项目">
          <span class="wb-project-symbol"><AppIcon name="terminal" /></span>
          <span class="wb-project-text"><strong>{{ projectName }}</strong><small>{{ projectSubtitle }}</small></span>
        </div>
      </div>
    </slot>
    <div class="wb-nav-title">项目空间</div>
    <nav class="wb-nav" aria-label="主导航" @click="mobileMenu = false">
      <a v-for="[key, name, icon] in navItems" :key="key" class="wb-nav-item" :class="{ selected: area === key }" :href="href('/' + key)" :aria-current="area === key ? 'page' : undefined">
        <AppIcon :name="icon" /><span>{{ name }}</span>
        <span v-if="key === 'tasks' && attentionCount" class="wb-nav-badge" :aria-label="(attentionTruncated ? '至少 ' : '') + attentionCount + ' 项需要确认'">{{ attentionLabel }}</span>
      </a>
    </nav>
    <div class="wb-sidebar-footer" @click="($event.target as HTMLElement).closest('a') && (mobileMenu = false)">
      <slot name="sidebar-controls">
        <a class="wb-nav-item" :class="{ selected: area === 'settings' }" :href="href('/settings')" :aria-current="area === 'settings' ? 'page' : undefined"><AppIcon name="settings" /><span>项目设置</span></a>
      </slot>
      <div class="wb-connection" :class="{ offline: infoError }" :title="infoLast ? '更新于 ' + infoLast : ''">
        <i></i>{{ infoError ? "连接或数据异常" : remoteProject ? "中心服务 · 已连接" : "本机服务 · v" + (info.service_version || "…") }}
      </div>
      <slot name="account">
        <div ref="localRoot" class="wb-owner" @keydown.esc.stop.prevent="closeLocalMenu(true)">
          <button ref="localTrigger" class="wb-owner-button" aria-label="本机菜单" :aria-expanded="localMenu" aria-controls="local-menu" @click.stop="toggleLocalMenu">
            <span class="avatar">本</span><span><strong>本机</strong><small>本地项目</small></span><span class="spacer"></span><AppIcon name="more" />
          </button>
          <div v-if="localMenu" id="local-menu" class="wb-popover owner" role="menu" aria-label="本机菜单">
            <div class="wb-popover-header"><strong>本机</strong><p>本地项目 · v{{ info.service_version || "…" }}</p></div>
            <a role="menuitem" :href="href('/settings')" @click="closeLocalMenu()"><AppIcon name="settings" />项目设置</a>
            <button role="menuitem" @click="setTheme(theme === 'indigo' ? 'graphite' : 'indigo')"><AppIcon name="sun" />{{ theme === "indigo" ? "切换为石墨外观" : "切换为靛蓝外观" }}</button>
          </div>
        </div>
      </slot>
    </div>
  </aside>
  <div class="wb-main-frame">
    <header class="wb-topbar">
      <button class="icon-button wb-mobile-button" aria-label="打开导航" :aria-expanded="mobileMenu" @click="mobileMenu = true"><AppIcon name="menu" /></button>
      <div class="wb-breadcrumb"><span>{{ projectName }}</span><AppIcon name="chevron-right" /><span>{{ pageTitle }}</span></div>
      <button class="wb-global-search" @click="showSearch = true"><AppIcon name="search" /><span>搜索任务、规范或资料</span><span class="wb-kbd">⌘ K</span></button>
      <span v-if="infoError" class="wb-connection-alert">连接异常</span>
      <button class="icon-button" aria-label="刷新项目状态" title="刷新项目状态" @click="refreshCurrentView"><AppIcon name="refresh" /></button>
    </header>
    <main id="main-content" class="wb-page" tabindex="-1">
      <header v-if="['knowledge', 'evolution', 'settings', 'account', 'service', 'materials', 'local-setup'].includes(area) && !id || area === 'tasks' && id === 'new'" class="wb-page-header">
        <div><h1>{{ pageTitle }}</h1><p class="wb-lead">{{ area === "tasks" ? "在本地项目中记录新的工作目标。" : area === "settings" && !remoteProject ? "管理本地项目接入、资料来源、迁移与运行诊断。" : subtitles[area] }}</p></div>
        <div class="wb-header-actions"><slot name="page-actions" :area="area"><template v-if="!remoteProject"><a v-if="area === 'knowledge'" class="button ghost" :href="href('/settings', { section: 'sources' })"><AppIcon name="folder" />资料来源</a><a v-else-if="area === 'evolution'" class="button ghost" :href="href('/settings', { section: 'diagnostics' })"><AppIcon name="terminal" />技术记录</a></template></slot></div>
      </header>
      <p v-if="remoteProject && remoteSyncError" role="alert" class="wb-banner error">连接异常，当前内容可能过期。最后成功同步 {{ remoteSyncAt || "尚未同步" }}。<button class="small" @click="pollDashboard">重试连接</button></p>
      <div v-if="notices" role="status" class="wb-banner"><span>{{ notices }}</span><button class="icon-button" aria-label="关闭操作反馈" @click="notices = ''"><AppIcon name="x" /></button></div>
      <slot name="workspace-feedback" />
      <template v-if="area === 'tasks'"><TaskList v-if="!id" /><CreateTask v-else-if="id === 'new' && !remoteProject" /><TaskPage v-else-if="id !== 'new'" :id="id" /><section v-else class="wb-empty"><h3>中心项目不在网页中新建任务</h3><p>由已接入的 Agent 通过 CLI 创建。</p><a class="button" :href="href('/tasks')">返回工作台</a></section></template>
      <KnowledgePage v-else-if="area === 'knowledge'" :id="id" />
      <EvolutionPage v-else-if="area === 'evolution'" :id="id" />
      <SetupGuide v-else-if="area === 'local-setup'" />
      <slot v-else-if="remoteProject && ['settings', 'account', 'service', 'materials'].includes(area)" name="remote-page" :area="area" />
      <SettingsPage v-else-if="area === 'settings' && !remoteProject" />
      <LocalMaterials v-else-if="area === 'materials' && !remoteProject" />
      <section v-else class="wb-empty"><h3>页面不存在</h3><p>这个地址没有对应的页面。</p><a class="button" :href="href('/tasks')">返回工作台</a></section>
    </main>
  </div>
  <GlobalSearchDialog v-if="showSearch" @close="showSearch = false" />
  <div class="wb-toast" :class="{ visible: toastText }" role="status" aria-live="polite">{{ toastText }}</div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { info, infoError, infoLast, notices, refreshInfo } from "./api";
import { route, href } from "./router";
import TaskList from "./pages/TaskList.vue";
import TaskPage from "./pages/TaskPage.vue";
import CreateTask from "./pages/CreateTask.vue";
import KnowledgePage from "./pages/KnowledgePage.vue";
import EvolutionPage from "./pages/EvolutionPage.vue";
import SettingsPage from "./pages/SettingsPage.vue";
import AppIcon from "./components/AppIcon.vue";
import GlobalSearchDialog from "./components/GlobalSearchDialog.vue";
const area = computed(() => route.value.path.split("/")[1] || "tasks");
const id = computed(() =>
  decodeURIComponent(route.value.path.split("/").slice(2).join("/")),
);
const titles: Record<string, string> = {
  tasks: "工作台",
  knowledge: "规范与决策",
  evolution: "项目演进",
  settings: "项目设置",
};
function focusMain() {
  document.getElementById("main-content")?.focus();
}
const mobileMenu = ref(false), showSearch = ref(false);
let savedTheme = "";
try { savedTheme = localStorage.getItem("ael-theme") || ""; } catch {}
const theme = ref(savedTheme === "graphite" ? "graphite" : "indigo");
function applyTheme() {
  document.documentElement.dataset.theme = theme.value;
  try { localStorage.setItem("ael-theme", theme.value); } catch {}
}
function toggleTheme() {
  theme.value = theme.value === "indigo" ? "graphite" : "indigo";
  applyTheme();
}
function keyboard(e: KeyboardEvent) {
  const target = e.target as HTMLElement;
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k' || e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes(target?.tagName) && !target?.isContentEditable) {
    e.preventDefault(); showSearch.value = true;
  }
}
let timer: ReturnType<typeof setInterval>;
onMounted(() => {
  refreshInfo();
  applyTheme();
  window.addEventListener('keydown', keyboard);
  timer = setInterval(refreshInfo, 10000);
});
onUnmounted(() => { clearInterval(timer); window.removeEventListener('keydown', keyboard); });
</script>
<template>
  <a href="#main-content" class="skip-link" @click.prevent="focusMain"
    >跳到主要内容</a
  >
  <div class="shell">
    <button v-if="mobileMenu" class="mobile-scrim" aria-label="关闭导航" @click="mobileMenu = false"></button>
    <aside class="sidebar" :class="{ 'mobile-open': mobileMenu }">
      <a class="brand" href="#/tasks"
        @click="mobileMenu = false"><AppIcon name="loop"/><span class="mark">ael<span>/</span></span></a
      >
      <div class="workspace">
        <span class="eyebrow">当前项目</span
        ><strong>{{ info.project?.split("/").pop() || "正在连接" }}</strong
        ><small>{{
          info.config?.mode === "shadow"
            ? "预览模式 · 工作流未激活"
            : info.config
              ? "活动工作区"
              : "等待读取项目身份"
        }}</small>
      </div>
      <p class="sidebar-label">WORKSPACE</p><nav aria-label="主导航" @click="mobileMenu = false">
        <a
          v-for="[key, name] in [
            ['tasks', '工作台'],
            ['knowledge', '规范与决策'],
            ['evolution', '项目演进'],
          ]"
          :key="key"
          :href="href('/' + key)"
          :aria-current="area === key ? 'page' : undefined"
          :class="{ chosen: area === key }"
          ><AppIcon :name="key"/>{{ name }}</a
        >
      </nav>
      <div class="sidebar-bottom">
        <a
          href="#/settings"
          :class="{ chosen: area === 'settings' }"
          :aria-current="area === 'settings' ? 'page' : undefined"
          @click="mobileMenu = false"><AppIcon name="settings"/>项目设置</a
        >
        <button class="theme-switch" :aria-label="theme === 'indigo' ? '切换到石墨外观' : '切换到靛蓝外观'" @click="toggleTheme"><AppIcon name="theme"/>外观：{{ theme === 'indigo' ? '靛蓝' : '石墨' }}</button>
        <footer>
          <span :class="['dot', { offline: infoError }]"></span
          >{{ infoError ? "连接或数据异常" : "本机服务已连接"
          }}<small
            >v{{ info.service_version || "…" }} · 内嵌离线资源<br />更新于
            {{ infoLast || "等待连接" }}</small
          >
        </footer>
      </div>
    </aside>
    <div class="app-frame"><header class="topbar"><button class="mobile-menu-button" aria-label="打开导航" :aria-expanded="mobileMenu" @click="mobileMenu = true"><AppIcon name="menu"/></button><div class="breadcrumbs">工作空间 <span>›</span> {{ titles[area] || '页面' }}<span v-if="id">› 详情</span></div><div class="topbar-actions"><span v-if="infoError" class="connection-alert">连接异常</span><button class="top-search" @click="showSearch = true"><AppIcon name="search"/>快速查找 <kbd>⌘ K</kbd></button><button class="icon-button" aria-label="刷新项目状态" @click="refreshInfo"><AppIcon name="loop"/></button></div></header>
    <main id="main-content" tabindex="-1">
      <header v-if="!id || (area === 'tasks' && id === 'new')" class="page-header">
        <div>
          <p class="eyebrow">{{ info.project?.split("/").pop() || "AEL" }}</p>
          <h1>{{ titles[area] || "页面不存在" }}</h1>
          <p v-if="area === 'tasks'" class="page-subtitle">让工作继续向前，让需要你处理的事项一目了然。</p>
        </div>
        <a v-if="area === 'tasks'" class="button primary" href="#/tasks/new"
          >＋ 创建任务</a
        ><button v-else @click="refreshInfo">刷新项目状态</button>
      </header>
      <div v-if="notices" role="status" class="notice">
        <span>{{ notices }}</span
        ><button @click="notices = ''" aria-label="关闭操作反馈">×</button>
      </div>
      <template v-if="area === 'tasks'"><TaskList v-if="!id || id === 'new'" /><CreateTask v-if="id === 'new'" /><TaskPage v-else-if="id" :id="id" /></template><KnowledgePage
        v-else-if="area === 'knowledge'"
        :id="id"
      /><EvolutionPage v-else-if="area === 'evolution'" :id="id" /><SettingsPage
        v-else-if="area === 'settings'"
      />
      <section v-else class="empty">
        <h2>页面不存在</h2>
        <a href="#/tasks">返回工作台</a>
      </section>
    </main></div>
    <GlobalSearchDialog v-if="showSearch" @close="showSearch = false" />
  </div>
</template>

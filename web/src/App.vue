<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";
import { info, infoError, infoLast, notices, refreshInfo } from "./api";
import { route, href } from "./router";
import TaskList from "./pages/TaskList.vue";
import TaskPage from "./pages/TaskPage.vue";
import CreateTask from "./pages/CreateTask.vue";
import KnowledgePage from "./pages/KnowledgePage.vue";
import EvolutionPage from "./pages/EvolutionPage.vue";
import SettingsPage from "./pages/SettingsPage.vue";
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
let timer: ReturnType<typeof setInterval>;
onMounted(() => {
  refreshInfo();
  timer = setInterval(refreshInfo, 10000);
});
onUnmounted(() => clearInterval(timer));
</script>
<template>
  <a href="#main-content" class="skip-link" @click.prevent="focusMain"
    >跳到主要内容</a
  >
  <div class="shell">
    <aside class="sidebar">
      <a class="brand" href="#/tasks"
        ><span class="mark">ael<span> / </span></span
        ><span>Agent Engineering Loop</span></a
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
      <nav aria-label="主导航">
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
          >{{ name }}</a
        >
      </nav>
      <div class="sidebar-bottom">
        <a
          href="#/settings"
          :class="{ chosen: area === 'settings' }"
          :aria-current="area === 'settings' ? 'page' : undefined"
          >项目设置</a
        >
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
    <main id="main-content" tabindex="-1">
      <header v-if="!id" class="page-header">
        <div>
          <p class="eyebrow">{{ info.project?.split("/").pop() || "AEL" }}</p>
          <h1>{{ titles[area] || "页面不存在" }}</h1>
        </div>
        <a v-if="area === 'tasks'" class="button primary" href="#/tasks/new"
          >＋ 创建任务</a
        ><button v-else @click="refreshInfo">刷新项目状态</button>
      </header>
      <div v-if="notices" role="status" class="notice">
        <span>{{ notices }}</span
        ><button @click="notices = ''" aria-label="关闭操作反馈">×</button>
      </div>
      <TaskList v-if="area === 'tasks' && !id" /><CreateTask
        v-else-if="area === 'tasks' && id === 'new'"
      /><TaskPage v-else-if="area === 'tasks'" :id="id" /><KnowledgePage
        v-else-if="area === 'knowledge'"
        :id="id"
      /><EvolutionPage v-else-if="area === 'evolution'" :id="id" /><SettingsPage
        v-else-if="area === 'settings'"
      />
      <section v-else class="empty">
        <h2>页面不存在</h2>
        <a href="#/tasks">返回工作台</a>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { api, params, remoteProject } from "../api";
import { taskHref, knowledgeHref, href } from "../router";
import { readableError } from "../display";
import AppIcon from "./AppIcon.vue";
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(), field = ref<HTMLInputElement>();
const previous = document.activeElement as HTMLElement | null;
const query = ref(""), loading = ref(false), errors = ref<string[]>([]);
const tasks = ref<any[]>([]), knowledge = ref<any[]>([]), materials = ref<any[]>([]);
let sequence = 0, timer: ReturnType<typeof setTimeout> | undefined;
onMounted(() => { dialog.value?.showModal(); field.value?.focus(); void search(); });
onUnmounted(() => { sequence++; clearTimeout(timer); previous?.focus({ preventScroll: true }); });
function changed() { clearTimeout(timer); timer = setTimeout(() => void search(), 220); }
// Searches only the current project: tasks, knowledge and (remote) materials.
async function search() {
  const q = query.value.trim();
  const current = ++sequence;
  loading.value = true; errors.value = [];
  const [taskResult, knowledgeResult, materialResult] = await Promise.allSettled([
    api("/api/tasks?" + params({ compact: 1, q, limit: q ? 6 : 5 })),
    q ? api("/api/knowledge-page?" + params({ view: "all", q, limit: 5 })) : Promise.resolve({ items: [] }),
    q && remoteProject.value ? api("/api/inventory?" + params({ kind: "files", q, limit: 5, order: "recent" })) : Promise.resolve({ items: [] }),
  ]);
  if (current !== sequence) return;
  tasks.value = taskResult.status === "fulfilled" ? (taskResult.value.items || []).slice(0, 6) : [];
  knowledge.value = knowledgeResult.status === "fulfilled" ? knowledgeResult.value.items || [] : [];
  materials.value = materialResult.status === "fulfilled" ? materialResult.value.items || [] : [];
  if (taskResult.status === "rejected") errors.value.push("任务：" + readableError(String(taskResult.reason)));
  if (knowledgeResult.status === "rejected") errors.value.push("规范：" + readableError(String(knowledgeResult.reason)));
  if (materialResult.status === "rejected") errors.value.push("资料：" + readableError(String(materialResult.reason)));
  loading.value = false;
}
const nothing = () => !loading.value && !errors.value.length && !tasks.value.length && !knowledge.value.length && !materials.value.length;
</script>
<template>
  <dialog ref="dialog" class="wb-dialog" aria-labelledby="global-search-title" @cancel.prevent="emit('close')" @click.self="emit('close')">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading"><h2 id="global-search-title">快速查找</h2><button class="icon-button" aria-label="关闭" @click="emit('close')"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body">
        <input ref="field" v-model="query" class="wb-search-input" type="text" role="searchbox" enterkeyhint="search" aria-label="全局搜索" placeholder="输入任务、规范或资料名称" @input="changed" @keydown.enter.prevent="search" />
        <div class="wb-search-results" aria-live="polite">
          <p v-if="loading" class="wb-search-group">正在查找…</p>
          <p v-for="error in errors" :key="error" class="wb-banner error" role="alert">{{ error }}</p>
          <template v-if="tasks.length"><p class="wb-search-group">{{ query.trim() ? "任务" : "最近任务" }}</p><a v-for="task in tasks" :key="task.id" class="wb-search-result" :href="taskHref(task.id)" @click="emit('close')"><AppIcon name="workbench" /><span>{{ task.title }}</span><small>任务</small></a></template>
          <template v-if="knowledge.length"><p class="wb-search-group">规范与决策</p><a v-for="item in knowledge" :key="item.id" class="wb-search-result" :href="knowledgeHref(item.id)" @click="emit('close')"><AppIcon name="book" /><span>{{ item.title }}</span><small>规范</small></a></template>
          <template v-if="materials.length"><p class="wb-search-group">资料</p><a v-for="item in materials" :key="item.id" class="wb-search-result" :href="href('/materials', { task: item.task_id, q: item.filename })" @click="emit('close')"><AppIcon :name="String(item.mime).startsWith('image/') ? 'image' : 'file'" /><span>{{ item.filename }}</span><em v-if="item.task_title || item.task_id">{{ item.task_title || item.task_id }}</em><small>资料</small></a></template>
          <div v-if="nothing()" class="wb-empty"><p>{{ query.trim() ? "没有找到匹配内容" : "当前项目还没有任务" }}</p></div>
        </div>
      </div>
      <div class="wb-dialog-footer" style="justify-content:flex-start"><span class="muted" style="font-size:12px">搜索当前项目 · Esc 关闭</span></div>
    </div>
  </dialog>
</template>

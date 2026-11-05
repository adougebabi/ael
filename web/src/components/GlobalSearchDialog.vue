<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { api, params } from "../api";
import { taskHref, knowledgeHref } from "../router";
import { readableError } from "../display";
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(), field = ref<HTMLInputElement>();
const previous = document.activeElement as HTMLElement | null;
const query = ref(""), searched = ref(false), loading = ref(false), errors = ref<string[]>([]);
const tasks = ref<any[]>([]), knowledge = ref<any[]>([]);
let sequence = 0;
onMounted(() => { dialog.value?.showModal(); field.value?.focus(); });
onUnmounted(() => { sequence++; previous?.focus({ preventScroll: true }); });
async function search() {
  const q = query.value.trim();
  if (!q) return;
  const current = ++sequence;
  loading.value = true; errors.value = []; searched.value = true;
  const [taskResult, knowledgeResult] = await Promise.allSettled([
    api("/api/tasks?" + params({ compact: 1, q, limit: 5 })),
    api("/api/knowledge-page?" + params({ view: "all", q, limit: 5 })),
  ]);
  if (current !== sequence) return;
  tasks.value = taskResult.status === "fulfilled" ? taskResult.value.items.slice(0, 5) : [];
  knowledge.value = knowledgeResult.status === "fulfilled" ? knowledgeResult.value.items : [];
  if (taskResult.status === "rejected") errors.value.push("任务：" + readableError(String(taskResult.reason)));
  if (knowledgeResult.status === "rejected") errors.value.push("资料：" + readableError(String(knowledgeResult.reason)));
  loading.value = false;
}
</script>
<template><dialog ref="dialog" class="action-dialog global-search-dialog" @cancel.prevent="emit('close')"><form @submit.prevent="search"><div class="section-heading"><h2>快速查找</h2><button type="button" aria-label="关闭" @click="emit('close')">×</button></div><label>搜索任务和资料<input ref="field" v-model="query" type="search" placeholder="标题、正文、路径或符号" /></label><button class="primary" :disabled="loading || !query.trim()">{{ loading ? '正在查找…' : '查找' }}</button><div v-for="error in errors" :key="error" class="alert" role="alert">{{ error }}</div><template v-if="searched && !loading"><h3>任务</h3><p v-if="!tasks.length" class="secondary">没有匹配的任务。</p><a v-for="task in tasks" :key="task.id" class="search-result" :href="taskHref(task.id)" @click="emit('close')">{{ task.title }} <small>{{ task.id }}</small></a><h3>规范与决策</h3><p v-if="!knowledge.length" class="secondary">没有匹配的资料。</p><a v-for="item in knowledge" :key="item.id" class="search-result" :href="knowledgeHref(item.id)" @click="emit('close')">{{ item.title }} <small>{{ item.description || '打开阅读详情' }}</small></a></template></form></dialog></template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { api, params, remoteProject } from '../api';
import { taskHref } from '../router';
import ReviewPanel from './ReviewPanel.vue';
import ManualReviewDialog from './ManualReviewDialog.vue';
import AppIcon from './AppIcon.vue';
// Wide review frame opened from the workbench. Previews and comparisons open
// above it and return here; only the selected check is ever submitted.
const props = defineProps<{ taskId: string; checkId?: string }>();
const emit = defineEmits<{ close: []; saved: [] }>();
const dialog = ref<HTMLDialogElement>();
const previous = document.activeElement as HTMLElement | null;
const lastReceipt = ref('');
const snapshot = ref<any>(null), objects = ref<any[]>([]), error = ref(''), loading = ref(true), selected = ref(props.checkId || ''), advanced = ref('');
const humanChecks = computed(() => (snapshot.value?.task?.definition?.checks || []).filter((c: any) => snapshot.value?.check_access?.[c.id]?.effective_method === 'human' && (snapshot.value.check_access[c.id].can_review || snapshot.value.current_checks?.[c.id])));
const check = computed(() => humanChecks.value.find((c: any) => c.id === selected.value) || humanChecks.value.find((c: any) => snapshot.value.check_access[c.id].can_review) || humanChecks.value[0]);
async function load(quiet = false) {
  if (!quiet) loading.value = true;
  error.value = '';
  try {
    const [n, meta] = await Promise.all([api('/api/task?' + params({ id: props.taskId })), remoteProject.value ? api('/api/review-evidence?' + params({ id: props.taskId })) : Promise.resolve([])]);
    snapshot.value = n; objects.value = meta;
    if (!selected.value && check.value) selected.value = check.value.id;
  } catch (e) { error.value = String(e); }
  finally { loading.value = false; }
}
onMounted(async () => { dialog.value?.showModal(); await load(); await nextTick(); dialog.value?.querySelector<HTMLElement>('.wb-review-controls button.primary, .wb-dialog-heading button')?.focus(); });
onUnmounted(() => { dialog.value?.close(); previous?.focus({ preventScroll: true }); });
function saved(_result: string, message: string) { lastReceipt.value = message; emit('saved'); void load(true); }
</script>
<template>
  <dialog ref="dialog" class="wb-dialog wide" aria-labelledby="review-dialog-title" @cancel.prevent="!advanced && emit('close')">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading"><h2 id="review-dialog-title">查看并验收</h2><button class="icon-button" aria-label="关闭" @click="emit('close')"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body">
        <p v-if="lastReceipt" class="wb-banner ok" role="status">{{ lastReceipt }}</p>
        <p v-if="loading" class="muted" role="status">正在读取当前候选和检查…</p>
        <p v-else-if="error" class="wb-banner error" role="alert">{{ error }} <button class="small" @click="load()">重试</button></p>
        <template v-else-if="snapshot && check">
          <p class="wb-review-task"><strong>{{ snapshot.task.definition.title }}</strong><span v-if="humanChecks.length > 1"> · 本任务有 {{ humanChecks.length }} 项人工检查，每项单独确认</span></p>
          <nav v-if="humanChecks.length > 1" class="wb-segmented" aria-label="选择人工检查"><button v-for="c in humanChecks" :key="c.id" :class="{ selected: c.id === check.id }" :aria-pressed="c.id === check.id" @click="selected = c.id">{{ c.id }}<span v-if="snapshot.current_checks?.[c.id]?.result === 'pass'"> · 已通过</span></button></nav>
          <ReviewPanel :key="snapshot.task.definition.id + check.id + snapshot.task.candidate" :snapshot="snapshot" :check="check" :objects="objects" compact @saved="saved" @advanced="advanced = check.id" />
        </template>
        <div v-else-if="!lastReceipt" class="wb-empty"><h3>这项任务当前没有需要人工确认的检查</h3><p>可能已经由他人处理，或检查条件已变化。</p></div>
      </div>
      <div class="wb-dialog-footer"><a class="button ghost" :href="taskHref(taskId)" @click="emit('close')">打开任务详情</a><button @click="emit('close')">稍后处理</button></div>
    </div>
  </dialog>
  <ManualReviewDialog v-if="advanced" :key="taskId + ':' + advanced" :task-id="taskId" :check-id="advanced" @select-check="advanced = $event" @close="advanced = ''; load(true)" @saved="saved('', '完整验收记录已提交，结果以任务中的记录为准。')" />
</template>

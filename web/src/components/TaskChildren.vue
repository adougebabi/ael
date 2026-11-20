<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted } from 'vue';
import { api, params, refreshRevision, remoteProject } from '../api';
import { route, setQuery, taskHref } from '../router';
import { useResource } from '../resource';
import { rowState, workFocus } from '../display';
import LoadState from './LoadState.vue';
import Pagination from './Pagination.vue';
import AppIcon from './AppIcon.vue';
const props = defineProps<{ taskId: string }>();
const emit = defineEmits<{ family: [value: any] }>();
const page = useResource<any>({ items: [], family: null, total: 0 });
const offset = computed(() => Number(route.value.query.get('family_offset') || 0));
const all = computed(() => route.value.query.get('family_view') === 'all');
const family = computed(() => page.data.value.family);
watch(family, (value) => emit('family', value || null), { immediate: true });
const canJudge = computed(() => !remoteProject.value || ['admin', 'reviewer'].includes(remoteProject.value.role));
async function load(quiet = false) { await page.load(() => api('/api/tasks?' + params({ view: 'workbench', parent: props.taskId, board: all.value ? 'all' : '', limit: 8, offset: offset.value })), quiet); }
watch(() => [props.taskId, offset.value, all.value], () => void load()); watch(refreshRevision, () => void load(true));
let timer: ReturnType<typeof setInterval>; onMounted(() => { void load(); timer = setInterval(() => void load(true), 8000); }); onUnmounted(() => clearInterval(timer));
const marks: Record<string, string> = { done: 'tick', review: 'user', decision: 'user', blocked: 'alert' };
</script>
<template>
  <section v-if="family?.descendants || page.error.value" class="wb-content-section" aria-labelledby="remaining-title">
    <div class="wb-section-header"><h2 id="remaining-title">剩余工作</h2><span v-if="family" class="muted">已完成 {{ family.completed }} / {{ family.descendants }}<template v-if="family.cancelled"> · 已取消 {{ family.cancelled }}</template></span></div>
    <LoadState :loading="page.loading.value && !family" :error="page.error.value" :last="page.last.value" @retry="load()" />
    <div v-if="family" class="wb-subtask-list">
      <div v-for="row in page.data.value.items" :key="row.task.id" class="wb-subtask-item" :data-task="row.task.id">
        <span class="wb-subtask-status" :class="rowState(row, canJudge).key"><AppIcon v-if="marks[rowState(row, canJudge).key]" :name="marks[rowState(row, canJudge).key]" /></span>
        <div class="wb-subtask-text"><a class="wb-task-title" :href="taskHref(row.task.id)">{{ row.task.title }}</a><p class="wb-subtask-sub">{{ row.path?.length > 1 ? '下级子任务 · ' : '' }}{{ workFocus(row.task) }}</p></div>
        <span class="tag" :class="rowState(row, canJudge).tone">{{ rowState(row, canJudge).label }}</span>
        <a class="icon-button" :href="taskHref(row.task.id)" :aria-label="'查看子任务 ' + row.task.title"><AppIcon name="chevron-right" /></a>
      </div>
      <p v-if="!page.loading.value && !page.data.value.items.length" class="wb-subtask-empty">{{ all ? '暂无此范围的子项。' : '没有未完成的子任务；父事项仍按自身状态与交付条件推进。' }}</p>
      <div class="wb-subtask-footer">
        <button class="text-button" @click="setQuery({ family_view: all ? undefined : 'all', family_offset: undefined })">{{ all ? '只看未完成子任务' : family.completed ? `展开 ${family.completed} 项已完成工作` : '查看全部子任务' }}</button>
        <small>直属子任务 · {{ family.direct }} 项<template v-if="family.descendants !== family.direct"> · 全部后代 {{ family.descendants }} 项</template></small>
      </div>
    </div>
    <Pagination v-if="page.data.value.total > 8 || offset" :offset="offset" :limit="8" :total="page.data.value.total" name="详情子任务" @change="setQuery({ family_offset: $event })" />
    <p v-if="family" class="wb-delivery-note"><AppIcon name="info" /><span>
      <template v-if="family.formal && family.required && family.satisfied === family.required">必需子任务已满足原条件，父级仍需集成、整体验收及原定交付。</template>
      <template v-else-if="family.formal && family.required">原计划还有 {{ family.required - family.satisfied }} 个必需子任务未满足父级条件；父级集成与交付单独验收。</template>
      <template v-else>子任务状态与父级完成条件分别保留：子项全部完成不会自动让父事项完成，父事项仍按自身的验收与交付推进。</template>
    </span></p>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from "vue";
import { api, params, refreshRevision } from "../api";
import { route, setQuery, taskHref } from "../router";
import { workPresentation, checkStatusSummary, label } from "../display";
import { useResource } from "../resource";
import LoadState from "./LoadState.vue";
import Pagination from "./Pagination.vue";

const props = defineProps<{ id: string; task: any }>();
const page = useResource<any>(null);
const limit = 8;
const offset = computed(() => Math.max(0, Number(route.value.query.get("related_offset")) || 0));
const parent = computed(() => props.task.definition.parent);
const membership = computed(() => props.task.epic_membership?.parent);
const archived = computed(() => !!props.task.legacy?.authority_import || props.task.historical);
function load(quiet = false) {
  return page.load(() => api("/api/task-relations?" + params({ id: props.id, limit, offset: offset.value })), quiet);
}
onMounted(() => void load());
watch(offset, () => void load());
watch(refreshRevision, () => void load(true));
</script>

<template>
  <section v-if="parent || membership || task.definition.feedback_of || task.definition.dependencies?.length || archived || page.data.value?.total || page.error.value" class="content-section" aria-label="任务关系">
    <div class="section-heading"><h2>任务关系</h2><small v-if="page.data.value?.total">{{ page.data.value.total }} 项关联</small></div>
    <div class="related-links">
      <a v-if="membership" :href="taskHref(membership)">所属大任务 {{ membership }} · {{ task.epic_membership.required ? '必需子项' : '可选子项' }}</a>
      <a v-if="parent && parent !== membership" :href="taskHref(parent)">定义中的父任务 {{ parent }}</a>
      <a v-if="task.definition.feedback_of" :href="taskHref(task.definition.feedback_of)">反馈来源 {{ task.definition.feedback_of }}</a>
      <a v-for="dep in task.definition.dependencies" :key="dep" :href="taskHref(dep)">前置任务 {{ dep }}</a>
    </div>
    <LoadState :loading="page.loading.value" :error="page.error.value" :last="page.last.value" @retry="load()" />
    <p v-if="page.data.value?.items.some((row: any) => row.definition_parent && !row.managed_member)" class="secondary">以下子项保留了原定义中的父任务关系。受管子项及其验收范围由大任务计划确定。以下关联成果不自动满足整体交付。</p>
    <p v-if="task.definition.type !== 'epic' && page.data.value?.items.some((row:any)=>row.definition_parent)" class="secondary">当前父任务未建立必需子项与整体验收计划；关联工作成果保留，整体交付仍需单独核对。</p>
    <ul v-if="page.data.value?.items.length" class="task-relation-list">
      <li v-for="item in page.data.value.items" :key="item.id">
        <a :href="taskHref(item.id)">{{ item.title }}</a>
        <span>{{ [item.definition_parent && !item.managed_member ? '定义关联子项' : '', item.feedback ? '后续反馈' : '', item.historical ? '历史记录 · 当前未复验' : label(item.status === 'open' ? item.step : item.status)].filter(Boolean).join(' · ') }}</span>
        <template v-if="item.summary"><p>{{ workPresentation(item.summary).latest || (item.historical ? '历史完成记录，当前未复验' : '尚未回报有效成果') }}</p><p>{{ workPresentation(item.summary).current }}</p><small>{{ checkStatusSummary(item.summary.checks) }}</small><a :href="taskHref(item.id,'execution')">查看工作结果</a><a :href="taskHref(item.id,'checks')">查看检查与证据</a></template>
      </li>
    </ul>
    <p v-else-if="archived && page.data.value?.total === 0 && !parent && !membership" class="secondary">当前记录没有明确的父子关系。可在执行记录中核对保留的导入资料。</p>
    <Pagination v-if="page.data.value && (page.data.value.total > limit || offset)" :offset="offset" :limit="limit" :total="page.data.value.total" :busy="page.loading.value" name="关联任务" @change="setQuery({ related_offset: $event })" />
  </section>
</template>

<style scoped>
.task-relation-list { list-style: none; padding: 0; }
.task-relation-list li { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 6px 16px; padding-block: 9px; border-top: 1px solid var(--line); overflow-wrap: anywhere; }
.task-relation-list span { color: var(--muted); font-size: .85rem; }
</style>

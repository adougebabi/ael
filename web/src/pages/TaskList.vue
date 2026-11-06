<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, info, params, refreshRevision } from "../api";
import { route, setQuery, taskHref, knowledgeHref } from "../router";
import { useResource } from "../resource";
import { boards, label, schedule, stamp, relativeStamp, blockerText } from "../display";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import AttentionCard from "../components/AttentionCard.vue";
import ManualReviewDialog from "../components/ManualReviewDialog.vue";
import AppIcon from "../components/AppIcon.vue";
import { pendingReviews } from "../reviewDraft";

const r = useResource<any>({ groups: {}, counts: {}, total: 0, attention: [] });
const { data, loading, error, last } = r;
const recent = useResource<any>({ items: [] });
const basis = useResource<any>({ items: [] });
const search = ref(route.value.query.get("q") || "");
const board = computed(() => route.value.query.get("board") || "");
const taskType = computed(() => route.value.query.get("type") || "");
const days = computed(() => route.value.query.get("days") || "30");
const manualTarget = ref<{ id: string; check: string } | null>(null);
const pendingTargets = ref(pendingReviews(info.value.project || ""));
watch(() => info.value.project, (project) => { pendingTargets.value = pendingReviews(project || ""); }, { immediate: true });
const attention = computed(() => (data.value.attention || []).filter((item: any) => !pendingTargets.value.some((pending) => pending.taskId === item.task.id)));
const attentionCount = computed(() => attention.value.length + pendingTargets.value.length);
const currentCount = computed(() => ["active", "pending", "confirmation"].reduce((sum, id) => sum + (data.value.counts[id] || 0), 0));
const visibleBoards = computed(() => boards.filter(([id]) => board.value === "all" ? !!data.value.counts[id] : board.value ? board.value === id : ["active", "pending", "confirmation"].includes(id) && !!data.value.counts[id]));
const filteredCount = computed(() => board.value === "all" ? data.value.total : board.value ? data.value.counts[board.value] || 0 : currentCount.value);
const boardName = (id: string, title: string) => ({ active: "推进中", completed: "已完成" } as Record<string, string>)[id] || title;
const initials = (name: string) => name.split(/[\s_-]+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase() || "—";
const offset = (id: string) => Number(route.value.query.get("p_" + id) || 0);
function filter(values: Record<string, string>) {
  setQuery({ ...Object.fromEntries(boards.map(([id]) => ["p_" + id, undefined])), ...values });
}
async function load(quiet = false) {
  const query = route.value.query.toString();
  await r.load(async () => {
    const [page, attentionResult] = await Promise.all([
      api("/api/tasks?" + params({ compact: 1, limit: 20,
        ...Object.fromEntries(boards.map(([id]) => ["offset_" + id, offset(id)])),
        days: days.value === "all" ? 0 : days.value, q: route.value.query.get("q") || "",
        type: taskType.value, board: board.value === "all" ? "" : board.value })),
      api("/api/attention").then((value) => ({ value, error: "" })).catch((error) => ({ value: null, error: String(error) })),
    ]);
    if (query !== route.value.query.toString()) throw new Error("请求已由新筛选替代");
    return {
      groups: Object.fromEntries(boards.map(([id]) => [id, page.items.filter((item: any) => item.board === id)])),
      counts: page.counts || {}, total: page.total || 0,
      attention: attentionResult.value?.items || [], attentionTruncated: !!attentionResult.value?.truncated,
      attentionError: attentionResult.error,
    };
  }, quiet);
}
function loadRecent(quiet = false) { return recent.load(() => api("/api/tasks?" + params({ compact: 1, board: "completed", days: 0, limit: 2 })), quiet); }
function loadBasis(quiet = false) { return basis.load(() => api("/api/knowledge-page?" + params({ view: "current", limit: 2 })), quiet); }
function reviewSaved() { pendingTargets.value = pendingReviews(info.value.project || ""); load(true); loadRecent(true); }
function reviewClosed() { manualTarget.value = null; pendingTargets.value = pendingReviews(info.value.project || ""); }
watch(() => route.value.key, () => { search.value = route.value.query.get("q") || ""; load(); });
watch(refreshRevision, () => { load(); loadRecent(); loadBasis(); });
let debounce: ReturnType<typeof setTimeout>;
function input() { clearTimeout(debounce); debounce = setTimeout(() => filter({ q: search.value }), 180); }
let timer: ReturnType<typeof setInterval>;
let refreshCount = 0;
onMounted(() => { load(); loadRecent(); loadBasis(); timer = setInterval(() => { load(true); refreshCount++; if (refreshCount % 5 === 0) loadRecent(true); if (refreshCount % 15 === 0) loadBasis(true); }, 4000); });
onUnmounted(() => { clearInterval(timer); clearTimeout(debounce); });
</script>
<template>
  <section class="attention-section">
    <div class="section-heading"><h2>需要你处理 <span class="count">{{ attentionCount }}</span></h2><small>验收、决策与阻塞，不必在任务里反复寻找</small></div>
    <div v-if="attentionCount" class="attention-grid"><article v-for="pending in pendingTargets" :key="pending.taskId + ':' + pending.checkId" class="attention-card featured"><div class="attention-meta"><span><AppIcon name="clock"/>验收提交待确认</span><small>{{ pending.taskId }}</small></div><h3>{{ pending.title }}</h3><p>上次提交结果尚未确认，请原样重试并核对回执。</p><div class="attention-bottom"><small>本标签页保存的验收请求</small><button class="primary" @click="manualTarget = { id: pending.taskId, check: pending.checkId }">查看并重试</button></div></article><AttentionCard v-for="item in attention" :key="item.kind" :kind="item.kind" :task="item.task" @review="(id, check) => manualTarget = { id, check }" /></div>
    <p v-else-if="!loading && !data.attentionError" class="attention-empty">目前没有需要你介入的事项。</p>
    <p v-if="data.attentionTruncated" class="secondary">待处理事项较多，此处仅检查了最近的部分任务；请使用下方分组查看全部任务。</p>
    <p v-if="data.attentionError" class="secondary">暂时无法读取待处理事项，请刷新工作台重试。</p>
  </section>
  <div class="workbench-controls">
    <nav class="group-counts" aria-label="任务分组定位">
      <a href="#/tasks" :aria-current="!board ? 'page' : undefined" :class="{ chosen: !board }" @click.prevent="filter({ board: '' })">当前工作 <strong>{{ currentCount }}</strong></a>
      <a v-for="[id, title] in boards" :key="id" :href="'#/tasks?board=' + id" :aria-current="board === id ? 'page' : undefined" :class="{ chosen: board === id }" @click.prevent="filter({ board: id })"><span>{{ boardName(id, title) }}</span><strong>{{ data.counts[id] || 0 }}</strong></a>
    </nav>
    <div class="toolbar workbench-filters">
      <label class="search"><span class="visually-hidden">搜索任务</span><AppIcon name="search"/><input v-model="search" type="search" placeholder="搜索任务…" @input="input" /></label>
      <select aria-label="任务类型" :value="taskType" @change="filter({ type: ($event.target as HTMLSelectElement).value })"><option value="">所有类型</option><option v-for="kind in ['feature', 'bug', 'improvement', 'refactor', 'research', 'docs', 'chore', 'test', 'qa', 'verify']" :key="kind" :value="kind">{{ label(kind) }}</option></select>
      <select v-if="['completed', 'all'].includes(board)" aria-label="完成历史" :value="days" @change="filter({ days: ($event.target as HTMLSelectElement).value })"><option value="7">最近 7 天</option><option value="30">最近 30 天</option><option value="all">全部历史</option></select>
    </div>
  </div>
  <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
  <p class="table-scroll-hint">左右滑动可查看执行者、验收和进展。</p><div class="board-scroll" tabindex="0" aria-label="任务分组列表，可横向滚动">
    <div class="task-table-head" aria-hidden="true"><span>工作内容</span><span>当前状态</span><span>执行 / 负责</span><span>必要验收</span><span>最近进展</span><span></span></div>
    <div class="board">
      <section v-for="[id, title] in visibleBoards" :key="id" :data-board="id" class="task-group">
        <div class="group-heading"><span class="group-dot" :class="id"></span><h2>{{ title }}</h2><span class="count">{{ data.counts[id] || 0 }}</span></div>
          <div v-for="s in data.groups[id] || []" :key="s.id" class="task task-row">
            <div class="task-title"><div class="task-title-line"><span class="task-icon"><AppIcon :name="s.type === 'research' ? 'file' : s.type === 'refactor' ? 'evolution' : 'layers'" /></span><a :id="'task-link-' + s.id" class="task-title-link" :href="taskHref(s.id)"><h3>{{ s.title }}</h3></a></div><p class="task-subline"><span :title="s.id">{{ s.id }}</span><span>{{ label(s.step) }}</span></p></div>
            <div class="task-state"><span class="status" :class="[s.board, { confirmation: s.manual_reviews?.length || s.next_action === 'wait_environment' }]">{{ schedule(s) }}</span><small :class="{ 'blocked-reason': s.blocker_reason || s.blocking_reasons?.length }">{{ s.blocker_reason ? blockerText(s.blocker_reason) : s.blocking_reasons?.length ? s.blocking_reasons.map(blockerText).join('；') : s.manual_reviews?.length ? '等待你操作' : s.running ? '有执行者在处理' : label(s.last_result) }}</small></div>
            <div class="task-owner"><span class="owner-avatar" aria-hidden="true">{{ s.executors.length ? initials(s.executors[0]) : '—' }}</span><span>{{ s.executors.length ? s.executors.join('、') : '当前无人执行' }}</span><small v-if="s.historical">历史记录 · 未在本机复验</small></div>
            <div class="task-checks"><template v-if="s.checks.required"><span>{{ s.checks.required_passed }} / {{ s.checks.required }} 通过</span><div v-if="s.checks.required <= 12" class="check-segments" role="progressbar" :aria-valuenow="s.checks.required_passed" :aria-valuemax="s.checks.required" aria-valuemin="0" aria-label="必需检查通过数"><i v-for="index in s.checks.required" :key="index" :class="{ done: index <= s.checks.required_passed }"></i></div><div v-else class="check-meter" role="progressbar" :aria-valuenow="s.checks.required_passed" :aria-valuemax="s.checks.required" aria-valuemin="0" aria-label="必需检查通过数"><span :style="{ width: (100 * s.checks.required_passed / s.checks.required) + '%' }"></span></div><small v-if="s.checks.running || s.checks.failed || s.checks.blocked">执行中 {{ s.checks.running }} · 失败 {{ s.checks.failed }} · 受阻 {{ s.checks.blocked }}</small></template><span v-else>未配置必需检查</span></div>
            <div class="task-activity"><span :title="stamp(s.activity_at)">{{ s.activity_at ? relativeStamp(s.activity_at) : '尚无有效活动' }}</span></div>
            <button v-if="s.manual_reviews?.length" class="task-row-action" @click="manualTarget = { id: s.id, check: s.manual_reviews[0].check }">验收</button><a v-else class="task-row-action button" :href="taskHref(s.id, s.requires_task_decision ? 'basis' : 'overview')">{{ s.requires_task_decision ? '审阅' : s.blocker_reason ? '处理' : '查看' }} →</a>
          </div>
          <p v-if="!loading && !(data.groups[id] || []).length" class="empty">{{ search ? '当前筛选无匹配任务。' : data.counts[id] || 0 ? '本页无任务，请返回上一页。' : '暂无' + title + '的任务。' }}</p>
          <Pagination v-if="(data.counts[id] || 0) > 20 || offset(id) > 0" :offset="offset(id)" :limit="20" :total="data.counts[id] || 0" :name="title" @change="setQuery({ ['p_' + id]: $event })" />
      </section>
      <p v-if="!loading && !visibleBoards.length" class="empty">{{ search ? '当前筛选无匹配任务。' : '当前分组暂无工作。' }}</p>
    </div>
    <footer class="task-table-footer"><span>{{ filteredCount }} 项工作 · {{ board === 'completed' ? '按所选时间查看完成记录' : '活跃任务不受完成时间限制' }}</span><button class="inline-link" @click="filter({ board: 'all' })">查看全部记录 →</button></footer>
  </div>
  <div class="dashboard-bottom">
    <section class="dashboard-panel"><div class="dashboard-panel-heading"><h2>最近交付</h2><a href="#/evolution?kind=delivery">查看演进 →</a></div><LoadState :loading="recent.loading.value" :error="recent.error.value" :last="recent.last.value" @retry="loadRecent" /><p v-if="!recent.loading.value && !recent.error.value && !recent.data.value.items.length" class="empty">尚无可追溯的交付结果。</p><a v-for="item in recent.data.value.items" :key="item.id" class="dashboard-item" :href="taskHref(item.id)"><span class="dashboard-item-icon"><AppIcon name="package" /></span><span class="dashboard-item-body"><strong>{{ item.title }}</strong><small>{{ label(item.last_result) }}<span v-if="item.historical"> · 历史记录，未复验</span></small></span><small>{{ stamp(item.completed_at) }}</small></a></section>
    <section class="dashboard-panel"><div class="dashboard-panel-heading"><h2>项目依据</h2><a href="#/knowledge">规范库 →</a></div><LoadState :loading="basis.loading.value" :error="basis.error.value" :last="basis.last.value" @retry="loadBasis" /><p v-if="!basis.loading.value && !basis.error.value && !basis.data.value.items.length" class="empty">尚无现行资料。</p><a v-for="item in basis.data.value.items" :key="item.id" class="dashboard-item" :href="knowledgeHref(item.id)"><span class="dashboard-item-icon"><AppIcon name="file" /></span><span class="dashboard-item-body"><strong>{{ item.title }}</strong><small>{{ label(item.status) }} · {{ item.paths?.join('、') || '项目级' }}</small></span><span aria-hidden="true">›</span></a></section>
  </div>
  <ManualReviewDialog v-if="manualTarget" :task-id="manualTarget.id" :check-id="manualTarget.check" @close="reviewClosed" @saved="reviewSaved" />
</template>

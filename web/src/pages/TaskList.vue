<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, params, refreshRevision } from "../api";
import { route, setQuery, taskHref, knowledgeHref, href } from "../router";
import { useResource } from "../resource";
import { boards, label, schedule, nextAction, stamp, blockerText } from "../display";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import AttentionCard from "../components/AttentionCard.vue";
import ManualReviewDialog from "../components/ManualReviewDialog.vue";
import AppIcon from "../components/AppIcon.vue";

const r = useResource<any>({ groups: {}, counts: {}, total: 0, attention: [] });
const { data, loading, error, last } = r;
const recent = useResource<any>({ items: [] });
const basis = useResource<any>({ items: [] });
const search = ref(route.value.query.get("q") || "");
const board = computed(() => route.value.query.get("board") || "");
const taskType = computed(() => route.value.query.get("type") || "");
const days = computed(() => route.value.query.get("days") || "30");
let savedClosed: Record<string, boolean> = { completed: true, cancelled: true };
try { savedClosed = { ...savedClosed, ...JSON.parse(sessionStorage.getItem("ael-closed-groups") || "{}") }; } catch {}
const closed = ref<Record<string, boolean>>(savedClosed);
const manualTarget = ref<{ id: string; check: string } | null>(null);
const attention = computed(() => data.value.attention || []);
watch(closed, (value) => sessionStorage.setItem("ael-closed-groups", JSON.stringify(value)), { deep: true });
const offset = (id: string) => Number(route.value.query.get("p_" + id) || 0);
function toggle(id: string) {
  closed.value[id] = !closed.value[id];
  sessionStorage.setItem("ael-closed-groups", JSON.stringify(closed.value));
}
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
        type: taskType.value, board: board.value })),
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
function reviewSaved() { load(true); loadRecent(true); }
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
    <div class="section-heading"><h2>需要你处理 <span class="count">{{ attention.length }}</span></h2><small>优先显示每类最近需要处理的事项</small></div>
    <div v-if="attention.length" class="attention-grid"><AttentionCard v-for="item in attention" :key="item.kind" :kind="item.kind" :task="item.task" @review="(id, check) => manualTarget = { id, check }" /></div>
    <p v-else-if="!loading && !data.attentionError" class="attention-empty">目前没有需要你介入的事项。</p>
    <p v-if="data.attentionTruncated" class="secondary">待处理事项较多，此处仅检查了最近的部分任务；请使用下方分组查看全部任务。</p>
    <p v-if="data.attentionError" class="secondary">暂时无法读取待处理事项，请刷新工作台重试。</p>
  </section>
  <div class="workbench-controls">
    <nav class="group-counts" aria-label="任务分组定位">
      <a href="#/tasks" :aria-current="!board ? 'page' : undefined" :class="{ chosen: !board }" @click.prevent="filter({ board: '' })">全部工作 <strong>{{ data.total }}</strong></a>
      <a v-for="[id, title] in boards" :key="id" :href="'#/tasks?board=' + id" :aria-current="board === id ? 'page' : undefined" :class="{ chosen: board === id }" @click.prevent="filter({ board: id }); closed[id] = false"><span>{{ title }}</span><strong>{{ data.counts[id] || 0 }}</strong></a>
    </nav>
    <div class="toolbar workbench-filters">
      <label class="search">搜索任务<input v-model="search" type="search" placeholder="标题、ID 或类型" @input="input" /></label>
      <label>状态分组<select :value="board" @change="filter({ board: ($event.target as HTMLSelectElement).value })"><option value="">全部分组</option><option v-for="[id, title] in boards" :key="id" :value="id">{{ title }}</option></select></label>
      <label>任务类型<select :value="taskType" @change="filter({ type: ($event.target as HTMLSelectElement).value })"><option value="">全部类型</option><option v-for="kind in ['feature', 'bug', 'improvement', 'refactor', 'research', 'docs', 'chore', 'test', 'qa', 'verify']" :key="kind" :value="kind">{{ kind }}</option></select></label>
      <label>完成历史<select :value="days" @change="filter({ days: ($event.target as HTMLSelectElement).value })"><option value="7">最近 7 天</option><option value="30">最近 30 天</option><option value="all">全部（含未知日期）</option></select></label>
    </div>
  </div>
  <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
  <p class="table-scroll-hint">左右滑动可查看执行者、验收和进展。</p><div class="board-scroll" tabindex="0" aria-label="任务分组列表，可横向滚动">
    <div class="task-table-head" aria-hidden="true"><span>工作内容</span><span>当前状态</span><span>执行 / 负责</span><span>必要验收</span><span>最近进展</span><span></span></div>
    <div class="board">
      <section v-for="[id, title] in boards.filter(([id]) => !board || board === id)" :key="id" :data-board="id" class="task-group">
        <div class="group-heading"><button class="text-button" :aria-expanded="!closed[id]" @click="toggle(id)"><span aria-hidden="true">{{ closed[id] ? "▸" : "▾" }}</span><h2>{{ title }}</h2><span class="count">{{ data.counts[id] || 0 }}</span></button></div>
        <template v-if="!closed[id]">
          <a v-for="s in data.groups[id] || []" :id="'task-link-' + s.id" :key="s.id" class="task task-row" :href="s.manual_reviews?.length ? href('/tasks/' + encodeURIComponent(s.id), { section: 'checks', review: s.manual_reviews[0].check }) : taskHref(s.id)">
            <div class="task-title"><div class="task-title-line"><span class="task-icon"><AppIcon name="layers" /></span><h3>{{ s.title }}</h3></div><p class="task-subline"><span>{{ s.id }}</span><span>{{ label(s.step) }}</span><span>{{ s.type }}</span></p><small class="task-next">下一步：{{ nextAction(s) }}</small></div>
            <div class="task-state"><span class="status" :class="s.board">{{ schedule(s) }}</span><small :class="{ 'blocked-reason': s.blocker_reason || s.blocking_reasons?.length }">{{ s.blocker_reason ? blockerText(s.blocker_reason) : s.blocking_reasons?.length ? s.blocking_reasons.map(blockerText).join('；') : label(s.last_result) }}</small></div>
            <div class="task-owner"><span>{{ s.executors.length ? s.executors.join('、') : '当前无人执行' }}</span><small v-if="s.historical">历史记录 · 未在本机复验</small></div>
            <div class="task-checks"><template v-if="s.checks.required"><span>{{ s.checks.required_passed }} / {{ s.checks.required }} 必需项通过</span><div class="check-meter" role="progressbar" :aria-valuenow="s.checks.required_passed" :aria-valuemax="s.checks.required" aria-valuemin="0" aria-label="必需检查通过数"><span :style="{ width: (100 * s.checks.required_passed / s.checks.required) + '%' }"></span></div><small v-if="s.checks.running || s.checks.failed || s.checks.blocked">执行中 {{ s.checks.running }} · 失败 {{ s.checks.failed }} · 受阻 {{ s.checks.blocked }}</small></template><span v-else>未配置必需检查</span></div>
            <div class="task-activity"><span>{{ s.activity_at ? stamp(s.activity_at) : '尚无有效活动' }}</span></div>
            <span class="task-view">{{ s.manual_reviews?.length ? '开始验收 →' : '查看 →' }}</span>
          </a>
          <p v-if="!loading && !(data.groups[id] || []).length" class="empty">{{ search ? '当前筛选无匹配任务。' : data.counts[id] || 0 ? '本页无任务，请返回上一页。' : '暂无' + title + '的任务。' }}</p>
          <Pagination v-if="(data.counts[id] || 0) > 20 || offset(id) > 0" :offset="offset(id)" :limit="20" :total="data.counts[id] || 0" :name="title" @change="setQuery({ ['p_' + id]: $event })" />
        </template>
      </section>
    </div>
  </div>
  <div class="dashboard-bottom">
    <section class="dashboard-panel"><div class="dashboard-panel-heading"><h2>最近交付</h2><a href="#/evolution?kind=delivery">查看演进 →</a></div><LoadState :loading="recent.loading.value" :error="recent.error.value" :last="recent.last.value" @retry="loadRecent" /><p v-if="!recent.loading.value && !recent.error.value && !recent.data.value.items.length" class="empty">尚无可追溯的交付结果。</p><a v-for="item in recent.data.value.items" :key="item.id" class="dashboard-item" :href="taskHref(item.id)"><span class="dashboard-item-icon"><AppIcon name="package" /></span><span class="dashboard-item-body"><strong>{{ item.title }}</strong><small>{{ label(item.last_result) }}<span v-if="item.historical"> · 历史记录，未复验</span></small></span><small>{{ stamp(item.completed_at) }}</small></a></section>
    <section class="dashboard-panel"><div class="dashboard-panel-heading"><h2>项目依据</h2><a href="#/knowledge">规范库 →</a></div><LoadState :loading="basis.loading.value" :error="basis.error.value" :last="basis.last.value" @retry="loadBasis" /><p v-if="!basis.loading.value && !basis.error.value && !basis.data.value.items.length" class="empty">尚无现行资料。</p><a v-for="item in basis.data.value.items" :key="item.id" class="dashboard-item" :href="knowledgeHref(item.id)"><span class="dashboard-item-icon"><AppIcon name="file" /></span><span class="dashboard-item-body"><strong>{{ item.title }}</strong><small>{{ label(item.status) }} · {{ item.paths?.join('、') || '项目级' }}</small></span><span aria-hidden="true">›</span></a></section>
  </div>
  <ManualReviewDialog v-if="manualTarget" :task-id="manualTarget.id" :check-id="manualTarget.check" @close="manualTarget = null" @saved="reviewSaved" />
</template>

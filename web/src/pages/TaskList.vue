<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { api, currentUser, info, params, refreshRevision, remoteProject } from '../api';
import { href, route, setQuery, taskHref } from '../router';
import { useResource } from '../resource';
import { label, rowState, rowSummary, workOwner, workFocus, workPresentation, relativeShort, historyReviewText, blockerText, handoffText } from '../display';
import { pendingReviews } from '../reviewDraft';
import { toast, copyText } from '../toast';
import LoadState from '../components/LoadState.vue';
import Pagination from '../components/Pagination.vue';
import ManualReviewDialog from '../components/ManualReviewDialog.vue';
import ReviewDialog from '../components/ReviewDialog.vue';
import AttentionThumb from '../components/AttentionThumb.vue';
import AppIcon from '../components/AppIcon.vue';

const r = useResource<any>({ items: [], counts: {}, total: 0, project_total: 0, project_children: 0, unfinished: 0, anomalies: 0 });
const { data, loading, error, last } = r;
const recent = useResource<any>({ items: [] });
const todo = useResource<any>({ items: [] });
const canJudge = computed(() => !remoteProject.value || ['admin', 'reviewer'].includes(remoteProject.value.role));
// Older links used ?view= from the reference layout; ?board= stays canonical.
const viewAlias: Record<string, string> = { all: '', done: 'completed', review: 'review', active: 'active', pending: 'pending', cancelled: 'cancelled' };
const board = computed(() => route.value.query.get('board') ?? viewAlias[route.value.query.get('view') || 'all'] ?? '');
const q = computed(() => route.value.query.get('q') || '');
const type = computed(() => route.value.query.get('type') || '');
const offset = computed(() => Number(route.value.query.get('offset') || 0));
const search = ref(q.value);
const tabs = [['', '未完成', 'unfinished'], ['active', '推进中', 'active'], ['pending', '待处理', 'pending'], ['review', '待确认', 'review'], ['completed', '已完成', 'completed'], ['cancelled', '已取消', 'cancelled']] as const;
const moreViews = [['human', '待人工验收'], ['blocked', '受阻'], ['repair', '待修复'], ['issues', '关系待核对'], ['all', '全部记录']] as const;
const types = ['feature', 'bug', 'improvement', 'refactor', 'docs', 'chore', 'research', 'test', 'qa', 'verify', 'epic'];
const isDefault = computed(() => !board.value && !q.value && !type.value);

const pendingTargets = ref(pendingReviews(info.value.project || ''));
watch(() => info.value.project, (project) => { pendingTargets.value = pendingReviews(project || ''); }, { immediate: true });
const kindOrder: Record<string, number> = { human: 0, decision: 1, history: 2 };
const attention = computed(() => (todo.data.value.items || []).filter((x: any) => ['human', 'decision', 'history'].includes(x.kind) && !pendingTargets.value.some((p) => p.taskId === x.task.id)).sort((a: any, b: any) => kindOrder[a.kind] - kindOrder[b.kind]));
const attentionTotal = computed(() => (todo.data.value.total || 0));
const manualTarget = ref<{ id: string; check: string } | null>(null);
const reviewTarget = ref<{ id: string; check: string } | null>(null);
// ?review=<task>[&check=<id>] opens the same review frame directly.
watch(() => route.value.query.get('review'), (id) => { if (!id) return; reviewTarget.value = { id, check: route.value.query.get('check') || '' }; setQuery({ review: undefined, check: undefined }); }, { immediate: true });

const childPages = ref<Record<string, any>>({});
const childErrors = ref<Record<string, string>>({});
const childBusy = ref<Record<string, boolean>>({});
const opened = computed(() => (route.value.query.get('open') || '').split(',').filter(Boolean));
let generation = 0; const childSequences: Record<string, number> = {};
const childOffset = (id: string) => Number(route.value.query.get('c_' + id) || 0);
const shownChildren = (row: any) => opened.value.includes(row.task.id) || (!!q.value && row.matches > 0);
function query(extra: Record<string, unknown> = {}) { return params({ view: 'workbench', limit: 20, board: board.value, q: q.value, type: type.value, days: ['completed', 'all'].includes(board.value) ? route.value.query.get('days') || 30 : 0, ...extra }); }
function filter(values: Record<string, string | undefined>) { const clear: Record<string, undefined> = { view: undefined }; route.value.query.forEach((_v, key) => { if (key.startsWith('c_')) clear[key] = undefined; }); setQuery({ ...clear, offset: undefined, open: undefined, ...values }); }
async function refreshChildren() { await Promise.all((data.value.items || []).filter(shownChildren).map((row: any) => children(row.task.id, childOffset(row.task.id)))); }
async function load(quiet = false) { await r.load(() => api('/api/tasks?' + query({ offset: offset.value })), quiet); await refreshChildren(); }
async function children(id: string, at = 0) {
  const gen = generation; const requestID = (childSequences[id] || 0) + 1; childSequences[id] = requestID; childBusy.value[id] = true;
  try {
    const value = await api('/api/tasks?' + query({ parent: id, offset: at, board: board.value || (q.value ? 'all' : '') }));
    if (gen !== generation || childSequences[id] !== requestID) return;
    if (at > 0 && at >= value.total) { setQuery({ ['c_' + id]: Math.max(0, Math.ceil(value.total / 20) - 1) * 20 }); return; }
    childPages.value[id] = value; childErrors.value[id] = '';
  } catch (e) { if (gen === generation && childSequences[id] === requestID) childErrors.value[id] = String(e); }
  finally { if (gen === generation && childSequences[id] === requestID) childBusy.value[id] = false; }
}
function toggle(id: string) { const open = opened.value.includes(id) ? opened.value.filter((x) => x !== id) : [...opened.value, id]; setQuery({ open: open.join(',') || undefined }); }
async function extras(quiet = false) { pendingTargets.value = pendingReviews(info.value.project || ''); await Promise.all([recent.load(() => api('/api/tasks?' + params({ compact: 1, board: 'completed', days: 0, limit: 3 })), quiet), todo.load(() => api('/api/attention?human=1'), quiet)]); }
function saved() { pendingTargets.value = pendingReviews(info.value.project || ''); void load(true); void extras(true); }
watch(() => route.value.key, () => { generation++; search.value = q.value; void load(); });
watch(refreshRevision, () => { void load(); void extras(); });
let debounce: ReturnType<typeof setTimeout>;
function input() { clearTimeout(debounce); const value = search.value; debounce = setTimeout(() => filter({ q: value || undefined }), 220); }
let timer: ReturnType<typeof setInterval>;
onMounted(() => { void load(); void extras(); timer = setInterval(() => { void load(true); void extras(true); }, 10000); });
onUnmounted(() => { generation++; clearInterval(timer); clearTimeout(debounce); });

const projectLabel = computed(() => remoteProject.value?.id || info.value.project?.split('/').filter(Boolean).pop() || '当前项目');
const handoffOpen = ref(false), handoffDialog = ref<HTMLDialogElement>();
let handoffReturn: HTMLElement | null = null;
async function openHandoff() { handoffReturn = document.activeElement as HTMLElement; handoffOpen.value = true; await nextTick(); handoffDialog.value?.showModal(); }
function closeHandoff() { handoffDialog.value?.close(); handoffOpen.value = false; handoffReturn?.focus({ preventScroll: true }); }
async function copyHandoff() { toast(await copyText(handoffText(projectLabel.value)) ? '已复制接续要求；复制不会启动或领取任务' : '浏览器未允许复制，请手动选择文字'); }

function icon(row: any) { const t = row.task; return t.board === 'completed' ? 'tick' : t.type === 'bug' ? 'alert' : t.type === 'docs' ? 'file' : 'workbench'; }
function segments(row: any) { const total = Math.min(row.children, 12); const done = Math.round((row.completed / row.children) * total); return Array.from({ length: total }, (_, i) => i < done); }
function attentionKind(kind: string) { return ({ human: '人工观察', decision: '需要决定', history: '历史核对' } as Record<string, string>)[kind] || '需要处理'; }
function attentionQuestion(item: any) {
  const t = item.task;
  if (item.kind === 'human') return [...new Set<string>((t.manual_reviews || []).flatMap((c: any) => c.covers?.length ? c.covers : [c.check]))].join('；') || '检查内容未记录';
  if (item.kind === 'history') return historyReviewText(workPresentation(t).reason);
  return workPresentation(t).reason || blockerText(t.blocking_reasons?.[0] || '审阅当前定义、计划和实际依据后作出决定。');
}
function attentionAction(item: any) { return item.kind === 'human' ? (canJudge.value ? '查看并验收' : '查看检查') : item.kind === 'decision' ? (canJudge.value ? '审阅并决定' : '查看方案') : '核对历史资料'; }
function attentionHref(item: any) { return item.kind === 'human' ? taskHref(item.task.id, 'overview', item.task.manual_reviews?.length === 1 ? item.task.manual_reviews[0].check : '') : taskHref(item.task.id); }
const emptyKind = computed(() => data.value.project_total === 0 && isDefault.value ? 'project' : !isDefault.value ? 'filter' : 'done');
</script>
<template>
  <div class="wb-page-header">
    <div>
      <h1>工作台</h1>
      <p class="wb-intro"><strong>{{ data.unfinished }} 项工作</strong>尚未完成<template v-if="canJudge && attentionTotal"><span class="wb-dot">·</span>{{ todo.data.value.truncated ? '至少 ' : '' }}{{ attentionTotal }} 项需要你确认</template><template v-else-if="attentionTotal"><span class="wb-dot">·</span>{{ attentionTotal }} 项等待有权限的人确认</template></p>
    </div>
    <div class="wb-header-actions">
      <a v-if="!remoteProject" class="button primary" :href="href('/tasks/new')"><AppIcon name="plus" />新建任务</a>
      <button v-else class="ghost" @click="openHandoff"><AppIcon name="terminal" />交给 Agent</button>
    </div>
  </div>

  <section v-if="isDefault && (attention.length || pendingTargets.length || todo.error.value)" class="wb-attention" aria-labelledby="attention-title">
    <div class="wb-section-header"><h2 id="attention-title">{{ canJudge ? '需要你看一下' : '等待有权限的人处理' }}</h2><span class="tiny-count">{{ attentionTotal + pendingTargets.length }}</span><a class="text-button" :href="href('/tasks', { board: 'review' })">查看全部</a></div>
    <LoadState :loading="todo.loading.value" :error="todo.error.value" :last="todo.last.value" @retry="extras()" />
    <div class="wb-attention-grid">
      <article v-for="pending in pendingTargets" :key="pending.taskId + pending.checkId" class="wb-attention-card">
        <div class="wb-attention-main"><div class="wb-attention-eyebrow">验收提交结果待确认</div><h3>{{ pending.title }}</h3><p>上一次提交没有收到回执。核对原请求后再决定是否重试，不会重复记录。</p><div class="wb-attention-actions"><button class="primary small" @click="manualTarget = { id: pending.taskId, check: pending.checkId }">核对原请求回执</button><a class="text-button" :href="taskHref(pending.taskId)">任务详情</a></div></div>
      </article>
      <article v-for="item in attention.slice(0, Math.max(0, 2 - pendingTargets.length))" :key="item.task.id" class="wb-attention-card">
        <AttentionThumb v-if="item.kind === 'human'" :task="item.task" @open="reviewTarget = { id: item.task.id, check: item.task.manual_reviews?.[0]?.check || '' }" />
        <div class="wb-attention-main">
          <div class="wb-attention-eyebrow">{{ attentionKind(item.kind) }}<template v-if="item.task.activity_at"> · {{ relativeShort(item.task.activity_at) }}</template></div>
          <h3>{{ item.task.title }}</h3>
          <p>{{ attentionQuestion(item) }}</p>
          <div class="wb-attention-actions"><button v-if="item.kind === 'human'" class="primary small" @click="reviewTarget = { id: item.task.id, check: item.task.manual_reviews?.length === 1 ? item.task.manual_reviews[0].check : '' }">{{ attentionAction(item) }}</button><a v-else class="button primary small" :href="attentionHref(item)">{{ attentionAction(item) }}</a><a class="text-button" :href="taskHref(item.task.id)">任务详情</a></div>
        </div>
      </article>
    </div>
  </section>

  <section aria-label="工作事项">
    <div class="wb-work-toolbar">
      <nav class="wb-status-tabs" aria-label="任务状态">
        <button v-for="[value, title, count] in tabs" :key="value" class="wb-status-tab" :class="{ selected: board === value }" :aria-pressed="board === value" @click="filter({ board: value || undefined })">{{ title }}<span class="tiny-count">{{ data.counts[count] || 0 }}</span></button>
      </nav>
      <div class="wb-toolbar-filters">
        <label class="wb-inline-search"><AppIcon name="search" /><span class="visually-hidden">搜索任务</span><input v-model="search" type="search" placeholder="搜索任务" @input="input" /></label>
        <select class="wb-filter-select" aria-label="任务类型" :value="type" @change="filter({ type: ($event.target as HTMLSelectElement).value || undefined })"><option value="">所有类型</option><option v-for="kind in types" :key="kind" :value="kind">{{ label(kind) }}</option></select>
        <select class="wb-filter-select" aria-label="更多视图" :value="moreViews.some(([v]) => v === board) ? board : ''" @change="filter({ board: ($event.target as HTMLSelectElement).value || undefined })"><option value="">更多视图</option><option v-for="[value, title] in moreViews" :key="value" :value="value">{{ title }}{{ value === 'issues' ? `（${data.anomalies}）` : '' }}</option></select>
        <select v-if="['completed', 'all'].includes(board)" class="wb-filter-select" aria-label="完成时间" :value="route.query.get('days') || '30'" @change="filter({ days: ($event.target as HTMLSelectElement).value })"><option value="7">最近 7 天</option><option value="30">最近 30 天</option><option value="0">全部历史</option></select>
      </div>
    </div>
    <div class="wb-task-list">
      <div class="wb-list-head" aria-hidden="true"><span>工作事项</span><span>当前情况</span><span>负责方</span><span></span></div>
      <LoadState :loading="loading && !data.items.length" :error="error" :last="last" @retry="load()" />
      <template v-for="row in data.items" :key="row.task.id">
        <div class="wb-task-row" :data-task="row.task.id">
          <div class="wb-task-main">
            <button v-if="row.children" class="wb-task-expand" :aria-expanded="shownChildren(row)" :aria-label="(shownChildren(row) ? '收起' : '展开') + '子任务 · ' + row.task.title" @click="toggle(row.task.id)"><AppIcon :name="shownChildren(row) ? 'chevron' : 'chevron-right'" /></button>
            <span v-else class="wb-task-icon"><AppIcon :name="icon(row)" /></span>
            <div class="wb-task-info">
              <a class="wb-task-title" :id="'task-link-' + row.task.id" :href="taskHref(row.task.id)">{{ row.task.title }}</a>
              <p class="wb-task-subtitle" :class="{ blocked: rowState(row, canJudge).key === 'blocked' }" :title="rowSummary(row)">{{ rowSummary(row) }}</p>
              <p v-if="row.issues?.length" class="wb-task-issue">{{ row.issues.length }} 项关系或资料待核对</p>
            </div>
          </div>
          <div class="wb-row-status"><span class="tag" :class="rowState(row, canJudge).tone">{{ rowState(row, canJudge).label }}</span><span v-if="row.children" class="wb-mini-progress" :aria-label="`已完成 ${row.completed} / ${row.children} 个子任务`"><span class="wb-progress-track"><i v-for="(done, index) in segments(row)" :key="index" :class="{ done }"></i></span><span>{{ row.completed }} / {{ row.children }}</span></span></div>
          <div class="wb-row-owner"><span class="avatar" :class="{ gray: workOwner(row.task, canJudge, currentUser, row).gray }">{{ workOwner(row.task, canJudge, currentUser, row).mark }}</span><span>{{ workOwner(row.task, canJudge, currentUser, row).name }}</span></div>
          <a class="icon-button wb-row-open" :href="taskHref(row.task.id)" :aria-label="'查看 ' + row.task.title"><AppIcon name="chevron-right" /></a>
        </div>
        <div v-if="row.children && shownChildren(row)" class="wb-row-children">
          <p v-if="childBusy[row.task.id] && !childPages[row.task.id]" class="wb-children-note" role="status">正在读取子任务…</p>
          <p v-if="childErrors[row.task.id]" class="wb-banner error" role="alert">{{ childErrors[row.task.id] }} <button class="small" @click="children(row.task.id)">重试</button></p>
          <div v-for="child in childPages[row.task.id]?.items || []" :key="child.task.id" class="wb-task-row child" :data-task="child.task.id">
            <div class="wb-task-main"><span class="wb-task-icon"><AppIcon :name="icon(child)" /></span><div class="wb-task-info"><a class="wb-task-title" :href="taskHref(child.task.id)">{{ child.task.title }}</a><p class="wb-task-subtitle" :class="{ blocked: rowState(child, canJudge).key === 'blocked' }">{{ workFocus(child.task) }}</p></div></div>
            <div class="wb-row-status"><span class="tag" :class="rowState(child, canJudge).tone">{{ rowState(child, canJudge).label }}</span></div>
            <div class="wb-row-owner"><span class="avatar" :class="{ gray: workOwner(child.task, canJudge, currentUser).gray }">{{ workOwner(child.task, canJudge, currentUser).mark }}</span><span>{{ workOwner(child.task, canJudge, currentUser).name }}</span></div>
            <a class="icon-button wb-row-open" :href="taskHref(child.task.id)" :aria-label="'查看 ' + child.task.title"><AppIcon name="chevron-right" /></a>
          </div>
          <p v-if="childPages[row.task.id]?.total === 0" class="wb-children-note">{{ q || board ? '当前筛选下没有命中的子任务。' : '没有未完成的子任务；父事项仍按自身条件推进。' }}</p>
          <Pagination v-if="childPages[row.task.id]?.total > 20" :offset="childPages[row.task.id].offset" :limit="20" :total="childPages[row.task.id].total" :busy="childBusy[row.task.id]" name="子任务" @change="setQuery({ ['c_' + row.task.id]: $event })" />
          <div class="wb-children-footer"><a class="text-button" :href="taskHref(row.task.id)">查看全部 {{ row.children }} 个子任务</a><span v-if="q || board">仅显示当前筛选命中的子任务</span></div>
        </div>
      </template>
      <div v-if="!loading && !error && !data.items.length" class="wb-empty wb-list-empty">
        <AppIcon :name="emptyKind === 'filter' ? 'search' : 'workbench'" />
        <template v-if="emptyKind === 'project'"><h3>这个项目还没有任务</h3><p>{{ remoteProject ? '由已接入的 Agent 通过 CLI 创建任务后，会出现在这里。' : '新建一个任务，写下目标和验收条件。' }}</p><a v-if="!remoteProject" class="button primary" :href="href('/tasks/new')">新建任务</a><a v-else class="button" :href="href('/settings')">查看项目接入</a></template>
        <template v-else-if="emptyKind === 'filter'"><h3>这里暂时没有匹配的事项</h3><p>试试其他状态或关键词。</p><a class="button" :href="href('/tasks')">查看未完成工作</a></template>
        <template v-else><h3>目前没有未完成的工作</h3><p>最近交付保留在下方。</p></template>
      </div>
      <div class="wb-list-footer"><span>{{ data.total }} 个主要事项<template v-if="q"> · 搜索包含子任务（命中 {{ data.matched_children || 0 }} 项）</template></span><span class="wb-secondary-footnote">父事项与子任务分层显示</span></div>
    </div>
    <Pagination v-if="data.total > 20 || offset" :offset="offset" :limit="20" :total="data.total" name="主要事项" :busy="loading" @change="setQuery({ offset: $event })" />
  </section>

  <section v-if="isDefault" class="wb-recent" aria-labelledby="recent-title">
    <div class="wb-section-header"><h2 id="recent-title">最近交付</h2><a class="text-button" :href="href('/tasks', { board: 'completed', days: '0' })">查看全部</a></div>
    <LoadState :loading="recent.loading.value" :error="recent.error.value" :last="recent.last.value" @retry="extras()" />
    <div v-if="recent.data.value.items.length" class="wb-recent-grid">
      <a v-for="item in recent.data.value.items" :key="item.id" class="wb-recent-card" :href="taskHref(item.id)">
        <span class="wb-recent-kind"><AppIcon name="tick" />{{ item.historical ? '历史完成 · 当前未复验' : '已交付' }}<template v-if="item.completed_at"> · {{ relativeShort(item.completed_at) }}</template></span>
        <h3>{{ item.title }}</h3>
        <p>{{ workPresentation(item).latest || label(item.last_result) }}</p>
        <span class="wb-recent-link"><AppIcon name="file" />查看交付结果</span>
      </a>
    </div>
    <p v-else-if="!recent.loading.value && !recent.error.value" class="wb-children-note">尚无可追溯的交付结果。</p>
  </section>

  <dialog v-if="handoffOpen" ref="handoffDialog" class="wb-dialog" aria-labelledby="handoff-title" @cancel.prevent="closeHandoff">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading"><h2 id="handoff-title">交给你的 Agent</h2><button class="icon-button" aria-label="关闭" @click="closeHandoff"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body">
        <p class="wb-dialog-text">复制下面的接续要求，交给本地或云电脑中已接入此项目的 Agent。AEL 记录进度与结果；工程执行仍由你的 Agent 负责。</p>
        <div class="wb-quote"><p>{{ handoffText(projectLabel) }}</p></div>
        <p class="wb-dialog-note">复制只生成文字，不会启动 Agent、领取任务或改变任务状态。</p>
      </div>
      <div class="wb-dialog-footer"><button @click="closeHandoff">关闭</button><button class="primary" @click="copyHandoff"><AppIcon name="copy" />复制接续要求</button></div>
    </div>
  </dialog>
  <ReviewDialog v-if="reviewTarget" :key="reviewTarget.id" :task-id="reviewTarget.id" :check-id="reviewTarget.check" @close="reviewTarget = null" @saved="saved" />
  <ManualReviewDialog v-if="manualTarget" :key="manualTarget.id + ':' + manualTarget.check" :task-id="manualTarget.id" :check-id="manualTarget.check" @select-check="manualTarget.check = $event" @close="manualTarget = null" @saved="saved" />
</template>

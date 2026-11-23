<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import { api, params, refreshRevision } from "../api";
import {
  route,
  setQuery,
  href,
  taskHref,
  knowledgeHref,
  back,
} from "../router";
import { useResource } from "../resource";
import { label, stamp } from "../display";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import AppIcon from "../components/AppIcon.vue";
const props = defineProps<{ id: string }>();
const r = useResource<any>({ items: [], scopes: [], total: 0 }),
  { data, loading, error, last } = r;
const search = ref(route.value.query.get("q") || "");
const offset = computed(() => Number(route.value.query.get("offset") || 0));
let searchTimer: ReturnType<typeof setTimeout> | undefined, typing = false;
async function load() {
  await r.load(() =>
    api(
      "/api/evolution?" +
        params(
          props.id
            ? { id: props.id }
            : {
                q: route.value.query.get("q") || "",
                kind: route.value.query.get("kind") || "",
                scope: route.value.query.get("scope") || "",
                from: localBoundary(route.value.query.get("from")),
                to: localBoundary(route.value.query.get("to"), true),
                limit: 25,
                offset: offset.value,
              },
        ),
    ),
  );
}
watch(
  () => route.value.key,
  () => {
    if (!typing) search.value = route.value.query.get("q") || "";
    load();
  },
  { flush: "post" },
);
onMounted(load);
onUnmounted(() => clearTimeout(searchTimer));
watch(refreshRevision, load);
function filter(v: Record<string, string>) {
  setQuery({ ...v, offset: undefined });
}
function searchNow() { clearTimeout(searchTimer); typing = false; filter({ q: search.value }); }
function searchInput() { typing = true; clearTimeout(searchTimer); searchTimer = setTimeout(searchNow, 220); }
function localBoundary(value: string | null, end = false) {
  return value
    ? new Date(value + (end ? "T23:59:59.999" : "T00:00:00")).toISOString()
    : "";
}
const referenceLink = (ref: any) =>
  ref.kind === "finding" && ref.task_id ? href("/tasks/" + encodeURIComponent(ref.task_id), { section: "findings", finding: ref.id }) : ref.kind === "task" ? taskHref(ref.id) : knowledgeHref(ref.id);
const changeKinds = [["", "全部变化"], ["delivery", "交付结果"], ["decision", "决策与取舍"], ["spec", "规范变化"]];
const rangeText = computed(() => { const dates = (data.value.items || []).map((c: any) => c.occurred_at).filter((v: any) => v && Number.isFinite(Date.parse(v))).sort(); if (!dates.length) return ""; const f = (v: string) => new Date(v).toLocaleDateString("zh-CN", { month: "long", day: "numeric" }); return (f(dates[0]) === f(dates.at(-1)) ? f(dates[0]) : f(dates[0]) + " — " + f(dates.at(-1))) + " · 本页 " + data.value.items.length + " / " + data.value.total + " 条"; });
function time(value: string | null) { return value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }) : "时间未确认"; }
function day(value: string | null) {
  return value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleDateString("zh-CN", { year: "numeric", month: "long", day: "numeric" }) : "日期未确认";
}
</script>
<template>
  <div>
    <template v-if="!id">
      <nav class="wb-page-tabs" aria-label="项目变化分类"><button v-for="[kind, title] in changeKinds" :key="kind" :class="{ selected: (route.query.get('kind') || '') === kind }" :aria-pressed="(route.query.get('kind') || '') === kind" @click="filter({ kind })">{{ title }}</button></nav>
      <div class="wb-knowledge-tools">
        <form class="wb-knowledge-search" @submit.prevent="searchNow"><AppIcon name="search" /><input v-model="search" type="search" aria-label="查找项目变化" placeholder="搜索变化或原因" @input="searchInput" /><button type="submit" class="text-button">搜索</button></form>
        <span class="wb-range-note">{{ rangeText }}</span>
      </div>
      <details class="wb-more-filters" :open="!!(route.query.get('scope') || route.query.get('from') || route.query.get('to'))"><summary>按范围和日期筛选</summary><div>
        <label>影响范围<select :value="route.query.get('scope') || ''" @change="filter({ scope: ($event.target as HTMLSelectElement).value })"><option value="">全部范围</option><option v-for="sc in data.scopes" :key="sc" :value="sc">{{ sc }}</option></select></label>
        <label>发生于 / 从<input type="date" :value="route.query.get('from') || ''" @change="filter({ from: ($event.target as HTMLInputElement).value })" /></label>
        <label>至<input type="date" :value="route.query.get('to') || ''" @change="filter({ to: ($event.target as HTMLInputElement).value })" /></label>
      </div></details>
      <p v-if="route.query.get('from') || route.query.get('to')" class="wb-banner">日期未确认的记录不进入明确日期范围。<button class="small" @click="filter({ from: '', to: '' })">清除日期限制</button></p>
      <LoadState :loading="loading && !data.items.length" :error="error" :last="last" @retry="load" />
      <div v-if="!loading && !error && !data.items.length" class="wb-empty"><AppIcon name="evolution" /><h3>暂无可追溯的项目变化</h3><p>现有记录尚不足以证明变化，或当前筛选没有匹配结果。导入、索引与普通进程事件保留在运行诊断中。</p><button v-if="route.query.get('q') || route.query.get('kind') || route.query.get('scope') || route.query.get('from') || route.query.get('to')" @click="search = ''; filter({ q: '', kind: '', scope: '', from: '', to: '' })">清除筛选</button></div>
      <div class="wb-change-timeline">
        <template v-for="(c, i) in data.items" :key="c.id">
          <h3 v-if="Number(i) === 0 || day(data.items[Number(i) - 1].occurred_at) !== day(c.occurred_at)" class="wb-change-date">{{ day(c.occurred_at) }}</h3>
          <article class="wb-timeline-event wb-change">
            <span class="wb-event-icon"><AppIcon :name="c.kind === 'delivery' ? 'tick' : c.kind === 'decision' ? 'branch' : 'file'" /></span>
            <div class="wb-event-body">
              <span class="wb-event-meta">{{ c.kind === "delivery" ? "交付结果" : c.kind === "decision" ? "决策与取舍" : c.kind === "spec" ? "规范变化" : label(c.kind) }}<span class="wb-change-time">{{ c.time_basis === "latest_delivery" ? "最近关联交付 · " : "" }}{{ time(c.occurred_at) }}</span></span>
              <h3><a :href="href('/evolution/' + encodeURIComponent(c.id))">{{ c.title }}</a></h3>
              <p>{{ c.content || c.reason || "变化内容未单独记录，请查看原始依据。" }}</p>
              <p v-if="c.content && c.reason && c.reason !== c.content && c.reason !== '未记录原因'" class="wb-change-reason">原因：{{ c.reason }}</p>
              <div class="wb-event-actions"><span class="tag" :class="['verified', 'released', 'accepted', 'effective'].includes(c.result) ? 'done' : ''">{{ label(c.result) }}</span><span v-if="c.historical" class="tag">历史记录 · 未复验</span><span class="muted wb-change-refs">{{ c.references.filter((ref: any) => ref.kind === 'task').length }} 项工作 · {{ c.references.filter((ref: any) => ref.kind !== 'task').length }} 份依据<template v-if="c.scopes?.length"> · {{ c.scopes.join("、") }}</template></span><a class="text-button" :href="href('/evolution/' + encodeURIComponent(c.id))">查看变化</a></div>
            </div>
          </article>
        </template>
      </div>
      <Pagination v-if="data.total > 25 || offset" :offset="offset" :limit="25" :total="data.total" name="项目变化" @change="setQuery({ offset: $event })" />
      <p class="wb-change-foot muted">迁移、索引和普通进程事件不进入默认变化流。<a :href="href('/settings', { section: 'diagnostics' })">查看原始项目事件</a></p>
    </template>
    <template v-else>
      <a class="text-button wb-back-line" href="#/evolution" @click.prevent="back('/evolution')"><AppIcon name="chevron-left" />项目演进</a>
      <LoadState :loading="loading" :error="error" :last="last" @retry="load" />
    </template>
    <template v-if="id && data.id"
      ><header class="object-header wb-detail-header">
        <div>
          <p class="eyebrow">
            项目变化 ·
            {{ data.kind === "delivery" ? "任务交付" : label(data.kind) }}
          </p>
          <h1>{{ data.title }}</h1>
          <p>
            <span class="status" :class="data.result">{{
              label(data.result)
            }}</span>
            · {{ data.time_basis === "latest_delivery" ? "最近关联交付：" : ""
            }}{{ stamp(data.occurred_at) }}
          </p>
        </div>
      </header>
      <div class="evolution-detail-layout"><div class="evolution-detail-main"><section class="content-section readable">
        <h2>这次改变</h2>
        <p>{{ data.content || "未单独记录变化说明，请查看关联工作结果。" }}</p>
        <h3>为什么要改</h3><p>{{ data.reason }}</p>
        <div class="change-comparison"><div><h3>变化前</h3><p>未单独记录。</p></div><div><h3>变化后</h3><p>未单独记录；已记录的变化事实见下方结果。</p></div></div>
      </section>
      <section class="content-section readable">
        <h2>取舍与没有采用的方案</h2>
        <p class="secondary">方案取舍未作为独立字段记录；如有关联决策，可阅读其原文。</p>
        <p v-if="data.result === 'rejected'">该方案被否决；具体理由保留在原决策中。</p>
        <a v-if="data.superseded_by" :href="knowledgeHref(data.superseded_by)">查看明确记录的后继方案</a>
      </section>
      <section class="content-section readable">
        <h2>关联工作与当前结果</h2>
        <p>来源能够证明的结论：{{ label(data.result) }}</p>
        <p v-if="data.delivery_facts?.length" class="secondary">
          规范或决策本身的记录日期：{{
            stamp(data.source_occurred_at)
          }}。关联任务的结果与发生时间分别保留如下。
        </p>
        <p v-if="!data.references.some((ref: any) => ref.kind === 'task')" class="secondary">未记录关联工作。</p>
        <article v-for="ref in data.references.filter((ref: any) => ref.kind === 'task')" :key="ref.id" class="record-row">
          <a :href="taskHref(ref.id)">{{ ref.title }}</a><small>{{ ref.id }}</small>
          <section v-for="fact in (data.delivery_facts || []).filter((f: any) => f.task === ref.id)" :key="fact.task + ':' + (fact.finding_id || '') + ':' + (fact.candidate_id || '') + ':' + fact.occurred_at">
            <p><strong v-if="fact.finding_id">{{ fact.title }}</strong> <span class="status">{{ label(fact.result) }}</span> · {{ stamp(fact.occurred_at) }}</p><p>{{ fact.content }}</p><p v-if="fact.candidate_id" class="secondary">复验候选 {{ fact.candidate_id }}</p>
          </section>
        </article>
        <p>影响范围：{{ data.scopes?.join("、") || "未记录具体范围" }}</p>
        <p v-if="data.historical" class="secondary">
          历史资料未经本机复验；不据此推断当前代码、交付或发布状态。
        </p>
      </section>
      </div><aside class="evolution-aside"><section><h2>结果边界</h2><p>{{ label(data.result) }}</p><p>{{ data.historical ? '历史资料，未在本机复验。' : '结果以已记录的任务和交付为准。' }}</p></section><section><h2>变化依据</h2><div v-for="ref in data.references.filter((ref: any) => ref.kind !== 'task')" :key="ref.id" class="record-row"><a :href="referenceLink(ref)">{{ ref.title }}</a><small>{{ label(ref.kind) }}</small></div><p v-if="!data.references.some((ref: any) => ref.kind !== 'task')">未记录关联资料。</p></section><section><h2>记录信息</h2><p>发生时间：{{ stamp(data.occurred_at) }}</p><p>影响范围：{{ data.scopes?.join('、') || '未记录具体范围' }}</p><p v-if="data.artifacts.length">实际产物 {{ data.artifacts.length }} 项，见对应任务的执行过程。</p></section></aside></div></template
    >
  </div>
</template>

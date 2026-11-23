<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import { api, params, refreshRevision } from "../api";
import { route, setQuery, href, back } from "../router";
import { useResource } from "../resource";
import { label } from "../display";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import AppIcon from "../components/AppIcon.vue";
import EvolutionDetail from "../components/EvolutionDetail.vue";

// Project changes: deliveries, decisions and spec changes in time order.
// A change opens as a reading dialog over the list; its own URL
// (/evolution/<id>) keeps deep links and back navigation working.
const props = defineProps<{ id: string }>();
const r = useResource<any>({ items: [], scopes: [], total: 0 }),
  { data, loading, error, last } = r;
const d = useResource<any>({}),
  { data: change, loading: changeLoading, error: changeError, last: changeLast } = d;
const search = ref(route.value.query.get("q") || "");
const offset = computed(() => Number(route.value.query.get("offset") || 0));
const listQuery = computed(() => { const q: Record<string, string> = {}; route.value.query.forEach((v, k) => { if (k !== "project") q[k] = v; }); return q; });
let searchTimer: ReturnType<typeof setTimeout> | undefined, typing = false, listKey = "";
async function loadList(force = false) {
  const key = new URLSearchParams(listQuery.value).toString();
  if (!force && key === listKey && data.value.items.length) return;
  listKey = key;
  await r.load(() =>
    api("/api/evolution?" + params({
      q: route.value.query.get("q") || "",
      kind: route.value.query.get("kind") || "",
      scope: route.value.query.get("scope") || "",
      from: localBoundary(route.value.query.get("from")),
      to: localBoundary(route.value.query.get("to"), true),
      limit: 25,
      offset: offset.value,
    })),
  );
}
async function loadChange() {
  if (!props.id) return;
  if (change.value.id !== props.id) change.value = {};
  await d.load(() => api("/api/evolution?" + params({ id: props.id })));
}
function load(force = false) { loadList(force); loadChange(); }
watch(
  () => route.value.key,
  () => {
    if (!typing) search.value = route.value.query.get("q") || "";
    load();
  },
  { flush: "post" },
);
onMounted(() => load(true));
onUnmounted(() => clearTimeout(searchTimer));
watch(refreshRevision, () => load(true));
function filter(v: Record<string, string>) {
  setQuery({ ...v, offset: undefined });
}
function searchNow() { clearTimeout(searchTimer); typing = false; filter({ q: search.value }); }
function searchInput() { typing = true; clearTimeout(searchTimer); searchTimer = setTimeout(searchNow, 220); }
function localBoundary(value: string | null, end = false) {
  return value ? new Date(value + (end ? "T23:59:59.999" : "T00:00:00")).toISOString() : "";
}
const filtered = computed(() => ["q", "kind", "scope", "from", "to"].some((k) => route.value.query.get(k)));
function clearFilters() { search.value = ""; filter({ q: "", kind: "", scope: "", from: "", to: "" }); }
const open = (id: string) => href("/evolution/" + encodeURIComponent(id), listQuery.value);
function closeChange() { back("/evolution" + (Object.keys(listQuery.value).length ? "?" + new URLSearchParams(listQuery.value) : "")); }
const changeKinds = [["", "全部变化"], ["delivery", "交付结果"], ["decision", "决策与取舍"], ["spec", "规范变化"]];
const kindText = (k: string) => (k === "delivery" ? "交付结果" : k === "decision" ? "决策与取舍" : k === "spec" ? "规范变化" : label(k));
const tagTone = (x: any) => (["verified", "released", "accepted", "effective"].includes(x.result) ? "done" : x.kind === "delivery" ? "blue" : "");
// Results of the linked deliveries, distinct from the change's own result.
const deliveryText = (c: any) => [...new Set((c.delivery_facts || []).map((f: any) => label(f.result)))].join("、");
const valid = (v: string | null) => !!v && Number.isFinite(Date.parse(v));
const thisYear = new Date().getFullYear();
function day(value: string | null) {
  if (!valid(value)) return "日期未确认";
  const t = new Date(value!);
  return (t.getFullYear() !== thisYear ? t.getFullYear() + " 年 " : "") + (t.getMonth() + 1) + " 月 " + t.getDate() + " 日";
}
function time(value: string | null) { return valid(value) ? new Date(value!).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }) : ""; }
const rangeText = computed(() => {
  const dates = (data.value.items || []).map((c: any) => c.occurred_at).filter(valid).sort();
  const count = "本页 " + data.value.items.length + " / " + data.value.total + " 条";
  if (!dates.length) return data.value.items.length ? count : "";
  const a = day(dates[0]), b = day(dates.at(-1));
  return (a === b ? a : b + " — " + a) + " · " + count;
});
</script>
<template>
  <div>
    <nav class="wb-page-tabs" aria-label="项目变化分类"><button v-for="[kind, title] in changeKinds" :key="kind" :class="{ selected: (route.query.get('kind') || '') === kind }" :aria-pressed="(route.query.get('kind') || '') === kind" @click="filter({ kind })">{{ title }}</button></nav>
    <div class="wb-knowledge-tools">
      <form class="wb-knowledge-search" @submit.prevent="searchNow"><AppIcon name="search" /><input v-model="search" type="search" aria-label="查找项目变化" placeholder="搜索变化或原因" @input="searchInput" /><button type="submit" class="text-button">搜索</button></form>
      <span class="wb-range-note">{{ rangeText }}</span>
    </div>
    <details class="wb-more-filters ev-filters" :open="!!(route.query.get('scope') || route.query.get('from') || route.query.get('to'))"><summary>更多筛选</summary><div>
      <label>影响范围<select :value="route.query.get('scope') || ''" @change="filter({ scope: ($event.target as HTMLSelectElement).value })"><option value="">全部范围</option><option v-for="sc in data.scopes" :key="sc" :value="sc">{{ sc }}</option></select></label>
      <label>发生于 / 从<input type="date" :value="route.query.get('from') || ''" @change="filter({ from: ($event.target as HTMLInputElement).value })" /></label>
      <label>至<input type="date" :value="route.query.get('to') || ''" @change="filter({ to: ($event.target as HTMLInputElement).value })" /></label>
    </div></details>
    <p v-if="route.query.get('from') || route.query.get('to')" class="wb-banner">日期未确认的记录不进入明确日期范围。<button class="small" @click="filter({ from: '', to: '' })">清除日期限制</button></p>
    <LoadState :loading="loading && !data.items.length" :error="error" :last="last" @retry="loadList(true)" />
    <div v-if="!loading && !error && !data.items.length" class="wb-empty">
      <AppIcon name="evolution" />
      <template v-if="filtered"><h3>没有匹配的变化</h3><p>切换分类或清除搜索条件。</p><button @click="clearFilters">清除筛选</button></template>
      <template v-else><h3>还没有可展示的变化</h3><p>任务形成交付、采纳决定或更新规范后，会在这里留下记录。导入、索引与普通进程事件保留在运行诊断中。</p><a class="button primary" :href="href('/tasks')">查看当前工作</a></template>
    </div>
    <section class="wb-change-timeline" aria-label="项目变化">
      <template v-for="(c, i) in data.items" :key="c.id">
        <h3 v-if="Number(i) === 0 || day(data.items[Number(i) - 1].occurred_at) !== day(c.occurred_at)" class="wb-change-date">{{ day(c.occurred_at) }}</h3>
        <article class="wb-change" :class="{ last: Number(i) === data.items.length - 1 || day(data.items[Number(i) + 1]?.occurred_at) !== day(c.occurred_at) }">
          <span class="wb-change-mark" :class="c.kind"><AppIcon :name="c.kind === 'delivery' ? 'tick' : c.kind === 'decision' ? 'branch' : 'file'" /></span>
          <div class="wb-change-body">
            <div class="wb-change-meta"><span>{{ kindText(c.kind) }}</span><span v-if="time(c.occurred_at)">{{ c.time_basis === "latest_delivery" ? "最近关联交付 · " : "" }}{{ time(c.occurred_at) }}</span></div>
            <h3><a :href="open(c.id)">{{ c.title }}</a></h3>
            <p>{{ c.content || c.reason || "变化内容未单独记录，请查看原始依据。" }}</p>
            <div class="wb-change-bottom"><span class="tag" :class="tagTone(c)">{{ label(c.result) }}</span><span v-if="c.historical" class="tag">历史记录 · 未复验</span><span v-if="c.delivery_facts?.length" class="wb-change-facts">关联交付 {{ deliveryText(c) }}</span><a class="text-button" :href="open(c.id)">查看变化</a></div>
          </div>
        </article>
      </template>
    </section>
    <Pagination v-if="data.total > 25 || offset" :offset="offset" :limit="25" :total="data.total" name="项目变化" @change="setQuery({ offset: $event })" />
    <p class="wb-change-foot muted">迁移、索引和普通进程事件不进入默认变化流。<a :href="href('/settings', { section: 'diagnostics' })">查看原始项目事件</a></p>
    <EvolutionDetail v-if="id" :key="id" :change="change.id === id ? change : null" :loading="changeLoading" :error="changeError" :last="changeLast" @close="closeChange" @retry="loadChange" />
  </div>
</template>

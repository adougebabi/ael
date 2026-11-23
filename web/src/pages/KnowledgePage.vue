<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import { api, info, params, refreshRevision } from "../api";
import { route, setQuery, knowledgeHref, taskHref, back } from "../router";
import { useResource } from "../resource";
import { label, stamp } from "../display";
import { readKnowledgeBookmarks, toggleKnowledgeBookmark } from "../knowledgeBookmarks";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import MarkdownBody from "../components/MarkdownBody.vue";
import CopyButton from "../components/CopyButton.vue";
import AppIcon from "../components/AppIcon.vue";

const props = defineProps<{ id: string }>();
const list = useResource<any>({ items: [], counts: {}, scopes: [], scope_counts: { all: 0, unclassified: 0, paths: {} }, total: 0 });
const record = useResource<any>(null);
const detail = record.data;
const search = ref(route.value.query.get("q") || "");
const view = computed(() => route.value.query.get("view") || "current");
const scope = computed(() => route.value.query.get("scope") || "");
const readerTab = computed(() => route.value.query.get("reader") || "body");
const offset = computed(() => Number(route.value.query.get("offset") || 0));
const resourceKey = computed(() => props.id
  ? JSON.stringify(["record", props.id, route.value.query.get("version") || "", route.value.query.get("context") || ""])
  : JSON.stringify(["list", view.value, scope.value, route.value.query.get("q") || "", offset.value]));
const activeResource = computed(() => props.id ? record : list);
const views = [["current", "现行规范"], ["decisions", "决策与取舍"], ["history", "历史资料"]];
const bookmarks = ref<string[]>([]), bookmarkError = ref("");

function syncBookmarks() { bookmarks.value = readKnowledgeBookmarks(info.value.project || ""); }
const saved = useResource<any[]>([]);
async function loadSaved() { const ids = bookmarks.value.slice(0, 25); await saved.load(async () => (await Promise.all(ids.map((id) => api("/api/knowledge-record?" + params({ id })).catch(() => ({ id, title: id, status: "unavailable", description: "这份收藏的资料当前不可读取（可能已移除或改名）。" }))))).filter((x: any) => !search.value || (x.title + " " + (x.description || "")).toLowerCase().includes(search.value.toLowerCase()))); }
const shown = computed<any[]>(() => view.value === "bookmarks" ? saved.data.value : list.data.value.items);
const startHere = computed(() => view.value === "current" ? (list.data.value.items || []).slice(0, 3) : []);
function toggleBookmarkFor(id: string) {
  const next = toggleKnowledgeBookmark(info.value.project || "", id);
  if (next === null) { bookmarkError.value = "浏览器无法保存收藏，请检查本地存储权限。"; return; }
  bookmarks.value = next; bookmarkError.value = "";
}
function toggleBookmark() {
  const next = toggleKnowledgeBookmark(info.value.project || "", props.id);
  if (next === null) { bookmarkError.value = "浏览器无法保存常用资料，请检查本地存储权限。"; return; }
  bookmarks.value = next;
  bookmarkError.value = "";
}
watch(() => info.value.project, syncBookmarks, { immediate: true });

async function load() {
  if (props.id) {
    const id = props.id, version = route.value.query.get("version") || "";
    await record.load(() => api("/api/knowledge-record?" + params({ id, version, context: route.value.query.get("context") || "" })));
  } else if (view.value === "bookmarks") {
    await loadSaved();
  } else {
    const query = { q: route.value.query.get("q") || "", view: view.value, scope: scope.value, limit: 25, offset: offset.value };
    await list.load(() => api("/api/knowledge-page?" + params(query)));
  }
}
let searchTimer: ReturnType<typeof setTimeout> | undefined;
let typing = false;
watch(resourceKey, () => {
  if (props.id) detail.value = null;
  else if (!typing) search.value = route.value.query.get("q") || "";
  void load();
}, { flush: "post" });
onMounted(() => { void load(); window.addEventListener("storage", syncBookmarks); });
onUnmounted(() => { clearTimeout(searchTimer); window.removeEventListener("storage", syncBookmarks); });
watch(refreshRevision, () => { void load(); });
function filter(values: Record<string, string>) { setQuery({ ...values, offset: undefined }); }
function searchNow() { clearTimeout(searchTimer); typing = false; filter({ q: search.value }); }
function searchInput() { typing = true; clearTimeout(searchTimer); searchTimer = setTimeout(searchNow, 220); }
function searchAcross() { clearTimeout(searchTimer); typing = false; filter({ q: search.value, view: "all", scope: "" }); }
function changeView(id: string) { clearTimeout(searchTimer); typing = false; filter({ view: id, scope: "", q: search.value }); }
function clearFilters() { clearTimeout(searchTimer); typing = false; search.value = ""; filter({ q: "", scope: "", view: "current" }); }
</script>

<template>
  <div class="knowledge-page">
    <template v-if="!id">
      <nav class="wb-page-tabs" aria-label="资料分类">
        <button v-for="[key, title] in views" :key="key" :class="{ selected: view === key }" :aria-current="view === key ? 'page' : undefined" @click="changeView(key)">{{ title }}<span class="tiny-count">{{ list.data.value.view_counts?.[key] ?? list.data.value.counts[key] ?? 0 }}</span></button>
        <button :class="{ selected: view === 'bookmarks' }" :aria-current="view === 'bookmarks' ? 'page' : undefined" @click="changeView('bookmarks')">我的收藏<span class="tiny-count">{{ bookmarks.length }}</span></button>
        <button v-if="view === 'all'" class="selected" aria-current="page">全部搜索结果</button>
      </nav>
      <div class="wb-knowledge-tools">
        <form class="wb-knowledge-search" @submit.prevent="view === 'bookmarks' ? searchAcross() : searchNow()"><AppIcon name="search" /><span class="visually-hidden">搜索规范</span><input v-model="search" type="search" placeholder="搜索标题、正文或路径" aria-label="搜索规范" @input="view !== 'bookmarks' && searchInput()" /><button type="submit" class="text-button">搜索</button></form>
        <button v-if="search && view !== 'all' && view !== 'bookmarks'" type="button" class="text-button" @click="searchAcross">在全部分类中搜索</button>
        <select v-if="view !== 'bookmarks'" aria-label="适用范围" :value="scope" @change="filter({ scope: ($event.target as HTMLSelectElement).value })"><option value="">全部范围（{{ list.data.value.scope_counts?.all || 0 }}）</option><option value="unclassified">未分类 / 项目级（{{ list.data.value.scope_counts?.unclassified || 0 }}）</option><option v-for="item in list.data.value.scopes" :key="item" :value="item">{{ item }}（{{ list.data.value.scope_counts?.paths?.[item] || 0 }}）</option></select>
      </div>
      <p v-if="view === 'history'" class="wb-banner">这里保留历史资料；阅读历史不等于采用它作为现行约束。</p>
      <div class="wb-knowledge-layout">
        <div class="wb-knowledge-main">
          <LoadState :loading="(view === 'bookmarks' ? saved.loading.value : list.loading.value) && !shown.length" :error="view === 'bookmarks' ? saved.error.value : list.error.value" :last="list.last.value" @retry="load" />
          <div v-if="!(view === 'bookmarks' ? saved.loading.value : list.loading.value) && !shown.length" class="wb-empty"><AppIcon :name="view === 'bookmarks' ? 'star' : 'search'" /><h3>{{ view === 'bookmarks' ? '还没有收藏的资料' : search || scope ? '没有匹配的资料' : '当前分类暂无资料' }}</h3><p>{{ view === 'bookmarks' ? '在列表或阅读页点星标收藏，常用规范会出现在这里（只保存在本浏览器）。' : '调整关键词或适用范围，或查看资料接入情况。' }}</p><button v-if="search || scope || !['current', 'bookmarks'].includes(view)" @click="clearFilters">清除筛选</button></div>
          <article v-for="item in shown" :key="item.id" class="wb-doc-row">
            <span class="wb-doc-icon"><AppIcon :name="item.kind === 'decision' ? 'branch' : item.kind === 'research' || ['historical', 'superseded'].includes(item.status) ? 'history' : 'book'" /></span>
            <div class="wb-doc-body">
              <h3><a :id="'knowledge-link-' + item.id" :href="knowledgeHref(item.id)" class="record-title">{{ item.title }}</a><span class="tag" :class="item.status === 'current' ? 'blue' : ['proposed', 'conflict'].includes(item.status) ? 'amber' : ''">{{ label(item.status) }}</span></h3>
              <p>{{ item.description || item.summary || '尚无摘要，打开阅读全文。' }}</p>
              <small>{{ item.paths?.join('、') || '未分类 / 项目级' }}<template v-if="item.task_count !== undefined"> · {{ item.task_count || 0 }} 个关联任务</template><template v-if="item.occurred_at"> · {{ stamp(item.occurred_at) }}</template><template v-else-if="item.hash"> · 指纹 {{ item.hash.slice(0, 8) }}</template></small>
            </div>
            <button class="icon-button wb-star" :class="{ on: bookmarks.includes(item.id) }" :aria-pressed="bookmarks.includes(item.id)" :aria-label="(bookmarks.includes(item.id) ? '取消收藏 ' : '收藏 ') + item.title" :disabled="!info.project" @click="toggleBookmarkFor(item.id)"><AppIcon name="star" /></button>
          </article>
          <Pagination v-if="view !== 'bookmarks' && (list.data.value.total > 25 || offset)" :offset="offset" :limit="25" :total="list.data.value.total" name="资料" @change="setQuery({ offset: $event })" />
          <p v-if="bookmarkError" role="alert" class="wb-banner error">{{ bookmarkError }}</p>
        </div>
        <aside class="wb-knowledge-aside" aria-label="阅读指引">
          <h3><AppIcon name="book" />从这里开始</h3>
          <p>执行前读现行规范；需要了解背景时，再看决策与历史。历史资料不会自动成为当前规则。</p>
          <a v-for="item in startHere" :key="item.id" :href="knowledgeHref(item.id)">{{ item.title }}</a>
          <p v-if="!startHere.length" class="muted">暂无现行规范。可在项目设置查看资料来源。</p>
          <hr />
          <p class="muted">规范正文、版本与关联工作在同一处阅读；关联任务列在每份资料的阅读页中。</p>
        </aside>
      </div>
    </template>
    <template v-else>
      <a class="text-button wb-back-line" :href="'#/knowledge'" @click.prevent="back('/knowledge')"><AppIcon name="chevron-left" />规范与决策</a>
      <LoadState :loading="activeResource.loading.value" :error="activeResource.error.value" :last="activeResource.last.value" @retry="load" />
      <template v-if="detail">
        <header class="wb-detail-header wb-detail-title-bar">
          <div><h1 class="wb-detail-title">{{ detail.title }}</h1><div class="wb-detail-meta"><span class="tag" :class="detail.status === 'current' ? 'blue' : ['proposed', 'conflict'].includes(detail.status) ? 'amber' : ''">{{ label(detail.status) }}</span><span>{{ label(detail.kind) }}</span><span class="wb-thin-divider"></span><span>{{ detail.paths?.join('、') || '未分类 / 项目级' }}</span><span v-if="detail.hash" :title="detail.hash">指纹 {{ detail.hash.slice(0, 8) }}</span></div></div>
          <button class="bookmark-button" :class="{ chosen: bookmarks.includes(detail.id) }" :aria-pressed="bookmarks.includes(detail.id)" :disabled="!info.project" title="仅保存在此浏览器" @click="toggleBookmark"><AppIcon name="star"/>{{ bookmarks.includes(detail.id) ? '已收藏' : '收藏' }}</button>
        </header>
        <p v-if="bookmarkError" role="alert" class="wb-banner error">{{ bookmarkError }}</p>
        <p v-if="route.query.get('version')" class="wb-banner">正在阅读任务实际使用的历史版本，不能据此认定当前实现已复验。</p>
        <section class="knowledge-reader">
          <section v-if="detail?.versions?.length" class="info-strip"><p>{{ detail.reason }}</p><a v-for="version in detail.versions" :key="version.hash" :href="knowledgeHref(detail.id, version.hash)">{{ version.hash }} · {{ label(version.status) }}</a></section>
        <MarkdownBody :id="detail.id" :body="detail.body" :version="detail.hash" :context="detail.context_id" :show-body="readerTab === 'body'" toc-right @show-body="setQuery({ reader: undefined, fragment: undefined })">
            <template #before>
              <nav class="tabs reader-tabs" aria-label="资料详情分区"><button v-for="[key, title] in [['body', '正文'], ['source', '版本与变化'], ['related', '关联工作']]" :key="key" :class="{ chosen: readerTab === key }" :aria-current="readerTab === key ? 'page' : undefined" @click="setQuery({ reader: key === 'body' ? undefined : key })">{{ title }}</button></nav>
              <p v-if="readerTab === 'body' && detail.summary" class="doc-intro">{{ detail.summary }}</p>
              <blockquote v-if="readerTab === 'body' && ['historical', 'superseded'].includes(detail.status)" class="knowledge-note">这是一份历史资料，不自动作为当前工作的执行依据。</blockquote>
              <blockquote v-if="readerTab === 'body' && ['proposed', 'conflict'].includes(detail.status)" class="knowledge-note">该资料仍待核实或决定，请以其真实状态和关联依据为准。</blockquote>
            </template>
            <template #after>
              <details v-if="readerTab === 'body'" class="source-detail"><summary>来源与技术信息</summary><p>来源：<code>{{ detail.source || detail.path }}</code></p><p>内容指纹：{{ detail.hash || '未记录' }} · 适用状态：{{ label(detail.status) }}</p><CopyButton :value="detail.source || detail.path" label="复制来源路径" /></details>
              <section v-if="readerTab === 'source'" class="source-details reader-section"><h2>版本与变化</h2><p>当前记录的内容指纹如下；尚未提供可比较的版本差异。</p><dl class="facts"><div><dt>来源文件</dt><dd>{{ detail.path }}</dd></div><div><dt>原始来源</dt><dd>{{ detail.source || detail.path }}</dd></div><div><dt>版本指纹</dt><dd>{{ detail.hash }} <CopyButton :value="detail.hash" label="复制版本" /></dd></div><div><dt>稳定引用</dt><dd>{{ detail.id }}</dd></div></dl><p v-if="detail.occurred_at">记录发生时间：{{ stamp(detail.occurred_at) }}</p><p v-else>原始发生日期未记录；不采用导入或文件更新时间代替。</p><p v-if="detail.reason" class="secondary">{{ detail.reason }}</p><details class="source-detail"><summary>原始 Markdown</summary><pre>{{ detail.body }}</pre></details></section>
              <section v-if="readerTab === 'related'" class="reader-section"><h2>使用或改变这份资料的工作</h2><p v-if="!detail.related_tasks?.length" class="secondary">没有明确关联任务。</p><div class="reader-related-list"><template v-for="link in detail.related_tasks || []" :key="link.id"><a v-if="link.available" :href="taskHref(link.id)"><small>{{ link.id }}</small><span>{{ link.title }}</span></a><span v-else class="secondary">{{ link.id }} · 关联任务已不可用（来源引用保留）</span></template></div><p v-if="detail.superseded_by"><a :href="knowledgeHref(detail.superseded_by)">查看后继资料</a></p></section>
            </template>
            <template #sidebar>
              <section class="reader-related"><h2>关联任务 <span class="count">{{ detail.related_tasks?.length || 0 }}</span></h2><p v-if="!detail.related_tasks?.length" class="secondary">没有明确关联任务。</p><div class="reader-related-list"><template v-for="link in detail.related_tasks || []" :key="link.id"><a v-if="link.available" :href="taskHref(link.id)"><small>{{ link.id }}</small><span>{{ link.title }}</span></a><span v-else class="secondary">{{ link.id }} · 关联任务已不可用</span></template></div></section>
              <section v-if="detail.superseded_by" class="reader-related"><h2>相关依据</h2><a :href="knowledgeHref(detail.superseded_by)">查看后继资料</a></section>
              <section class="reader-related"><a :href="'#/evolution?' + params({ q: detail.title })">查找相关演进 →</a></section>
            </template>
          </MarkdownBody>
        </section>
      </template>
    </template>
  </div>
</template>

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
  ? JSON.stringify(["record", props.id, route.value.query.get("version") || ""])
  : JSON.stringify(["list", view.value, scope.value, route.value.query.get("q") || "", offset.value]));
const activeResource = computed(() => props.id ? record : list);
const views = [["current", "现行规范"], ["decisions", "决策与取舍"], ["history", "历史资料"]];
const bookmarks = ref<string[]>([]), bookmarkError = ref("");

function syncBookmarks() { bookmarks.value = readKnowledgeBookmarks(info.value.project || ""); }
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
    await record.load(() => api("/api/knowledge-record?" + params({ id, version })));
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
      <nav class="tabs knowledge-tabs" aria-label="资料分类">
        <button v-for="[key, title] in views" :key="key" :class="{ chosen: view === key }" :aria-current="view === key ? 'page' : undefined" @click="changeView(key)">{{ title }} <span class="count">{{ list.data.value.view_counts?.[key] ?? list.data.value.counts[key] ?? 0 }}</span></button>
        <button v-if="view === 'all'" class="chosen" aria-current="page">全部搜索结果</button>
      </nav>
      <LoadState :loading="list.loading.value" :error="list.error.value" :last="list.last.value" @retry="load" />
      <div class="knowledge-library">
        <aside class="knowledge-scopes" aria-label="资料范围导航">
          <span>按适用范围</span>
          <button :class="{ chosen: !scope }" @click="filter({ scope: '' })"><span>全部范围</span><small>{{ list.data.value.scope_counts?.all || 0 }}</small></button>
          <button :class="{ chosen: scope === 'unclassified' }" @click="filter({ scope: 'unclassified' })"><span>未分类 / 项目级</span><small>{{ list.data.value.scope_counts?.unclassified || 0 }}</small></button>
          <button v-for="item in list.data.value.scopes.slice(0, 8)" :key="item" :class="{ chosen: scope === item }" :title="item" @click="filter({ scope: item })"><span>{{ item }}</span><small>{{ list.data.value.scope_counts?.paths?.[item] || 0 }}</small></button>
          <label v-if="list.data.value.scopes.length > 8" class="library-scope-select"><span class="visually-hidden">适用范围</span><select :value="scope" @change="filter({ scope: ($event.target as HTMLSelectElement).value })"><option value="">更多范围</option><option value="unclassified">未分类 / 项目级</option><option v-for="item in list.data.value.scopes" :key="item" :value="item">{{ item }}</option></select></label>
          <p>规范说明应该怎样做。<br />决策保留为什么。<br />历史不自动成为当前规则。</p>
        </aside>
        <div class="knowledge-main">
          <div class="library-heading">
            <div><h2>{{ views.find(([key]) => key === view)?.[1] || '全部搜索结果' }}</h2><small>{{ list.data.value.total }} 份资料 · 正文按需阅读</small></div>
            <form class="toolbar library-search" @submit.prevent="searchNow"><label class="search"><AppIcon name="search"/><span class="visually-hidden">搜索规范</span><input v-model="search" type="search" placeholder="搜索标题、正文或路径…" @input="searchInput" /></label><button v-if="search && view !== 'all'" type="button" class="inline-link" @click="searchAcross">搜索全部资料 →</button></form>
          </div>
          <p v-if="view === 'history'" class="info-strip">这里保留历史资料；阅读历史不等于采用它作为现行约束。</p>
          <div v-if="!list.loading.value && !list.error.value && !list.data.value.items.length" class="record-list library-empty"><AppIcon name="search"/><h2>{{ search || scope ? '没有匹配资料' : '当前分类暂无资料' }}</h2><p>调整关键词或适用范围，或查看资料接入情况。</p><button v-if="search || scope || view !== 'current'" @click="clearFilters">清除筛选</button><a href="#/settings?section=sources">查看资料来源</a></div>
          <div v-else class="record-list">
            <article v-for="item in list.data.value.items" :key="item.id" class="record-row knowledge">
              <span class="library-icon"><AppIcon :name="item.kind === 'decision' ? 'evolution' : 'file'" /></span>
              <div class="library-row-body">
                <h3><a :id="'knowledge-link-' + item.id" :href="knowledgeHref(item.id)" class="record-title">{{ item.title }}</a><AppIcon v-if="bookmarks.includes(item.id)" name="star" class="bookmark-mark" /></h3>
                <p class="record-description"><span v-if="item.excerpt" class="excerpt-label">正文摘录 · </span>{{ item.description || '尚无摘要，打开详情阅读。' }}</p>
                <div class="library-meta"><span>{{ item.paths?.join('、') || '未分类 / 项目级' }}</span><span v-if="item.hash" :title="item.hash">指纹 {{ item.hash.slice(0, 8) }}</span><span>{{ item.task_count || 0 }} 个关联任务</span><span v-if="item.occurred_at">{{ stamp(item.occurred_at) }}</span></div>
              </div>
              <div class="record-meta"><span class="status" :class="item.status">{{ label(item.status) }}</span><a :href="knowledgeHref(item.id)" :aria-label="'阅读' + item.title" class="library-arrow">›</a></div>
            </article>
          </div>
          <Pagination v-if="list.data.value.total > 25 || offset" :offset="offset" :limit="25" :total="list.data.value.total" name="资料" @change="setQuery({ offset: $event })" />
          <div class="library-hint"><span>点击标题阅读完整规范与关联依据</span><span>本页 {{ list.data.value.items.length }} / {{ list.data.value.total }} 份资料</span></div>
        </div>
      </div>
    </template>
    <template v-else>
      <button class="back-link" @click="back('/knowledge')">← 返回规范库</button>
      <LoadState :loading="activeResource.loading.value" :error="activeResource.error.value" :last="activeResource.last.value" @retry="load" />
      <template v-if="detail">
        <header class="object-header knowledge-detail-header">
          <div><p class="eyebrow">{{ label(detail.kind) }}</p><h1>{{ detail.title }}</h1><p class="knowledge-detail-meta"><span class="status" :class="detail.status">{{ label(detail.status) }}</span><span>{{ detail.paths?.join('、') || '未分类 / 项目级' }}</span><span v-if="detail.hash" :title="detail.hash">指纹 {{ detail.hash.slice(0, 8) }}</span></p></div>
          <button class="bookmark-button" :class="{ chosen: bookmarks.includes(detail.id) }" :aria-pressed="bookmarks.includes(detail.id)" :disabled="!info.project" title="仅保存在此浏览器" @click="toggleBookmark"><AppIcon name="star"/>{{ bookmarks.includes(detail.id) ? '已加入常用' : '加入常用' }}</button>
        </header>
        <p v-if="bookmarkError" role="alert" class="alert">{{ bookmarkError }}</p>
        <p v-if="route.query.get('version')" class="notice">正在阅读任务实际使用的历史版本，不能据此认定当前实现已复验。</p>
        <section class="knowledge-reader">
          <MarkdownBody :id="detail.id" :body="detail.body" :show-body="readerTab === 'body'" toc-right @show-body="setQuery({ reader: undefined, fragment: undefined })">
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

<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { api, params, refreshRevision } from "../api";
import { route, setQuery, knowledgeHref, taskHref, back } from "../router";
import { useResource } from "../resource";
import { label, stamp } from "../display";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import MarkdownBody from "../components/MarkdownBody.vue";
import CopyButton from "../components/CopyButton.vue";
import AppIcon from "../components/AppIcon.vue";
const props = defineProps<{ id: string }>();
const r = useResource<any>({ items: [], counts: {}, scopes: [], total: 0 }),
  { data, loading, error, last } = r;
const detail = ref<any>(null),
  search = ref(route.value.query.get("q") || "");
const view = computed(() => route.value.query.get("view") || "current"),
  scope = computed(() => route.value.query.get("scope") || ""),
  offset = computed(() => Number(route.value.query.get("offset") || 0));
const views = [
  ["current", "现行规范"],
  ["decisions", "决策与取舍"],
  ["history", "历史资料"],
];
async function load() {
  const id = props.id,
    key = route.value.key;
  await r.load(async () => {
    if (id) {
      const v = await api(
        "/api/knowledge-record?" +
          params({ id, version: route.value.query.get("version") || "" }),
      );
      if (id === props.id && key === route.value.key) detail.value = v;
      return data.value;
    }
    return api(
      "/api/knowledge-page?" +
        params({
          q: route.value.query.get("q") || "",
          view: view.value,
          scope: scope.value,
          limit: 25,
          offset: offset.value,
        }),
    );
  });
}
watch(
  () => route.value.key,
  () => {
    search.value = route.value.query.get("q") || "";
    detail.value = null;
    load();
  },
  { flush: "post" },
);
onMounted(load);
watch(refreshRevision, load);
function filter(v: Record<string, string>) {
  setQuery({ ...v, offset: undefined });
}
function searchAll() {
  filter({ q: search.value, view: search.value ? "all" : view.value });
}
</script>
<template>
  <div>
    <template v-if="!id"
      ><nav class="tabs" aria-label="资料分类">
        <button
          v-for="[id, title] in views"
          :key="id"
          :class="{ chosen: view === id }"
          :aria-current="view === id ? 'page' : undefined"
          @click="filter({ view: id })"
        >
          {{ title }}
          <span class="count">{{ data.counts[id] || 0 }}</span></button
        ><button v-if="view === 'all'" class="chosen">跨资料搜索</button>
      </nav></template
    >
    <button v-else class="back-link" @click="back('/knowledge')">
      ← 返回上一页
    </button>
    <LoadState :loading="loading" :error="error" :last="last" @retry="load" />
    <template v-if="!id"
      ><div class="knowledge-library"><aside class="knowledge-scopes" aria-label="资料范围导航"><span>按适用范围</span><button :class="{ chosen: !scope }" @click="filter({ scope: '' })">全部范围</button><button :class="{ chosen: scope === 'unclassified' }" @click="filter({ scope: 'unclassified' })">未分类 / 项目级</button><button v-for="s in data.scopes.slice(0, 8)" :key="s" :class="{ chosen: scope === s }" :title="s" @click="filter({ scope: s })">{{ s }}</button><p>规范说明应该怎样做。<br />决策保留为什么。<br />历史不自动成为当前规则。</p></aside><div class="knowledge-main"><div class="library-heading"><div><h2>{{ views.find(([key]) => key === view)?.[1] || '跨资料搜索' }}</h2><small>{{ data.total }} 份资料 · 正文按需阅读</small></div><form class="toolbar library-search" @submit.prevent="searchAll"><label class="search">搜索全部资料<input v-model="search" type="search" placeholder="标题、正文、路径或符号" /></label><button type="submit">搜索</button><label>适用范围<select :value="scope" @change="filter({ scope: ($event.target as HTMLSelectElement).value })"><option value="">全部范围</option><option value="unclassified">未分类 / 项目级</option><option v-for="s in data.scopes" :key="s" :value="s">{{ s }}</option></select></label></form></div><p v-if="view === 'current'" class="secondary">
        有效规范与待核实的规范分开标注；待决定、冲突内容保持原状态。
      </p>
      <div v-if="!loading && !error && !data.items.length" class="large-empty">
        <h2>{{ search ? "没有匹配资料" : "当前分类暂无资料" }}</h2>
        <p>可切换分类或调整搜索；没有资料不会被补成虚构规范。</p>
        <a href="#/settings?section=sources">查看资料接入与来源</a>
      </div>
      <div class="record-list">
        <article
          v-for="k in data.items"
          :key="k.id"
          class="record-row knowledge"
        >
          <span class="library-icon"><AppIcon :name="k.kind === 'decision' ? 'evolution' : 'file'" /></span><div>
            <a
              :id="'knowledge-link-' + k.id"
              :href="knowledgeHref(k.id)"
              class="record-title"
              >{{ k.title }}</a
            >
            <p class="record-description">
              <span v-if="k.excerpt" class="excerpt-label">正文摘录 · </span
              >{{ k.description || "尚无摘要，打开详情阅读。" }}
            </p>
            <small class="library-meta">{{ k.paths?.join("、") || "未分类 / 项目级" }}<span v-if="k.occurred_at"> · {{ stamp(k.occurred_at) }}</span></small>
          </div>
          <div class="record-meta">
            <span class="status" :class="k.status">{{ label(k.status) }}</span
            ><span aria-hidden="true">›</span>
          </div>
        </article>
      </div>
      <Pagination
        v-if="data.total > 25 || offset"
        :offset="offset"
        :limit="25"
        :total="data.total"
        name="资料"
        @change="setQuery({ offset: $event })"
    /><p class="library-hint">点击标题阅读完整资料与关联依据。</p></div></div></template>
    <template v-else-if="detail"
      ><header class="object-header">
        <div>
          <p class="eyebrow">{{ label(detail.kind) }}</p>
          <h1>{{ detail.title }}</h1>
          <p>
            <span class="status" :class="detail.status">{{
              label(detail.status)
            }}</span>
            · {{ detail.paths?.join("、") || "未分类 / 项目级" }}
          </p>
        </div>
      </header>
      <p v-if="route.query.get('version')" class="notice">
        正在阅读任务实际使用的历史版本，不能据此认定当前实现已复验。
      </p>
      <section class="knowledge-reader">
        <MarkdownBody :id="detail.id" :body="detail.body" toc-right><template #sidebar><section class="reader-related"><h2>关联任务与后继</h2><div class="related-links">
          <template v-for="link in detail.related_tasks || []" :key="link.id"
            ><a v-if="link.available" :href="taskHref(link.id)">{{
              link.title
            }}</a
            ><span v-else class="secondary"
              >一项关联任务已不可用（来源引用保留）</span
            ></template
          ><a
            v-if="detail.superseded_by"
            :href="knowledgeHref(detail.superseded_by)"
            >查看后继资料</a
          ><a :href="'#/evolution?' + params({ q: detail.title })"
            >查找相关演进</a
          >
        </div></section></template></MarkdownBody>
      </section>
      <details class="source-details">
        <summary>来源与版本</summary>
        <dl class="facts">
          <div>
            <dt>来源文件</dt>
            <dd>{{ detail.path }}</dd>
          </div>
          <div>
            <dt>原始来源</dt>
            <dd>{{ detail.source || detail.path }}</dd>
          </div>
          <div>
            <dt>版本指纹</dt>
            <dd>
              {{ detail.hash }}
              <CopyButton :value="detail.hash" label="复制版本" />
            </dd>
          </div>
          <div>
            <dt>稳定引用</dt>
            <dd>{{ detail.id }}</dd>
          </div>
        </dl>
        <p v-if="detail.occurred_at">
          记录发生时间：{{ stamp(detail.occurred_at) }}
        </p>
        <p v-else>原始发生日期未记录；不采用导入或文件更新时间代替。</p>
        <p class="secondary">{{ detail.reason }}</p>
      </details>
      <details>
        <summary>原始 Markdown</summary>
        <pre>{{ detail.body }}</pre>
      </details></template
    >
  </div>
</template>

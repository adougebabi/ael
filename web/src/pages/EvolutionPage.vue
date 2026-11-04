<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { api, params } from "../api";
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
const props = defineProps<{ id: string }>();
const r = useResource<any>({ items: [], scopes: [], total: 0 }),
  { data, loading, error, last } = r;
const search = ref(route.value.query.get("q") || "");
const offset = computed(() => Number(route.value.query.get("offset") || 0));
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
    search.value = route.value.query.get("q") || "";
    load();
  },
  { flush: "post" },
);
onMounted(load);
function filter(v: Record<string, string>) {
  setQuery({ ...v, offset: undefined });
}
function localBoundary(value: string | null, end = false) {
  return value
    ? new Date(value + (end ? "T23:59:59.999" : "T00:00:00")).toISOString()
    : "";
}
const referenceLink = (ref: any) =>
  ref.kind === "task" ? taskHref(ref.id) : knowledgeHref(ref.id);
</script>
<template>
  <div>
    <template v-if="!id"
      ><p class="secondary" style="margin-top: 18px">
        有明确依据的交付、规范与决策。导入、索引、心跳和普通进程事件保留在诊断中。
      </p>
      <form class="toolbar" @submit.prevent="filter({ q: search })">
        <label class="search"
          >查找项目变化<input
            v-model="search"
            type="search"
            placeholder="变化标题、内容或原因" /></label
        ><button type="submit">搜索</button
        ><label
          >变化类型<select
            :value="route.query.get('kind') || ''"
            @change="
              filter({ kind: ($event.target as HTMLSelectElement).value })
            "
          >
            <option value="">全部类型</option>
            <option value="delivery">任务交付</option>
            <option value="decision">决策与取舍</option>
            <option value="spec">规范变更</option>
          </select></label
        ><label
          >影响范围<select
            :value="route.query.get('scope') || ''"
            @change="
              filter({ scope: ($event.target as HTMLSelectElement).value })
            "
          >
            <option value="">全部范围</option>
            <option v-for="s in data.scopes" :value="s">{{ s }}</option>
          </select></label
        ><label
          >发生于 / 从<input
            type="date"
            :value="route.query.get('from') || ''"
            @change="
              filter({ from: ($event.target as HTMLInputElement).value })
            " /></label
        ><label
          >至<input
            type="date"
            :value="route.query.get('to') || ''"
            @change="filter({ to: ($event.target as HTMLInputElement).value })"
        /></label>
      </form>
      <p
        v-if="route.query.get('from') || route.query.get('to')"
        class="secondary"
      >
        日期未确认的资料不进入明确日期范围。<button
          @click="filter({ from: '', to: '' })"
        >
          清除日期限制
        </button>
      </p></template
    >
    <button v-else class="back-link" @click="back('/evolution')">
      ← 返回上一页</button
    ><LoadState :loading="loading" :error="error" :last="last" @retry="load" />
    <template v-if="!id"
      ><div v-if="!loading && !error && !data.items.length" class="large-empty">
        <h2>暂无可追溯的项目变化</h2>
        <p>现有记录尚不足以证明变化，或当前筛选没有匹配结果。</p>
        <div class="related-links">
          <a href="#/tasks">查看任务</a
          ><a href="#/knowledge?view=decisions">查看决策</a
          ><a href="#/knowledge?view=history">查看历史资料</a>
        </div>
      </div>
      <template v-for="(c, i) in data.items" :key="c.id"
        ><h2
          v-if="
            !c.occurred_at &&
            (Number(i) === 0 || data.items[Number(i) - 1].occurred_at)
          "
          class="content-section"
        >
          日期未确认
        </h2>
        <article class="change-row">
          <div>
            <time
              >{{ c.time_basis === "latest_delivery" ? "最近关联交付 · " : ""
              }}{{ stamp(c.occurred_at) }}</time
            >
            <p>{{ c.kind === "delivery" ? "任务交付" : label(c.kind) }}</p>
            <span class="status" :class="c.result">{{ label(c.result) }}</span>
          </div>
          <div>
            <h2>
              <a
                class="record-title"
                :href="href('/evolution/' + encodeURIComponent(c.id))"
                >{{ c.title }}</a
              >
            </h2>
            <p>{{ c.content || "变化内容未单独记录，请查看原始依据。" }}</p>
            <p><strong>原因：</strong>{{ c.reason }}</p>
            <p class="secondary">
              影响：{{ c.scopes?.join("、") || "未记录具体范围" }} ·
              {{ c.references.length }} 项真实关联
            </p>
            <p v-if="c.delivery_facts?.length" class="secondary">
              真实关联结果：{{
                c.delivery_facts
                  .map(
                    (f: any) =>
                      label(f.result) + "（" + stamp(f.occurred_at) + "）",
                  )
                  .join("；")
              }}
            </p>
            <small v-if="c.historical"
              >来源为历史记录，未声称当前实现已复验。</small
            >
          </div>
        </article></template
      ><Pagination
        v-if="data.total > 25 || offset"
        :offset="offset"
        :limit="25"
        :total="data.total"
        name="项目变化"
        @change="setQuery({ offset: $event })"
      /><a href="#/settings?section=diagnostics"
        >查看原始项目事件诊断</a
      ></template
    >
    <template v-else-if="data.id"
      ><header class="object-header">
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
      <section class="content-section readable">
        <h2>原状与变化原因</h2>
        <p>{{ data.reason }}</p>
      </section>
      <section class="content-section readable">
        <h2>变化内容与实际结果</h2>
        <p>{{ data.content || "未单独记录变化说明，请查看关联工作结果。" }}</p>
        <p>来源能够证明的结论：{{ label(data.result) }}</p>
        <p v-if="data.delivery_facts?.length" class="secondary">
          规范或决策本身的记录日期：{{
            stamp(data.source_occurred_at)
          }}。关联任务的结果与发生时间分别保留如下。
        </p>
        <article
          v-for="f in data.delivery_facts || []"
          :key="f.task"
          class="record-row"
        >
          <a :href="taskHref(f.task)">{{ f.title }}</a>
          <p>
            <span class="status">{{ label(f.result) }}</span> ·
            {{ stamp(f.occurred_at) }}
          </p>
          <p>{{ f.content }}</p>
          <small
            >对应当前候选与修订的产物
            {{ f.artifacts.length }}
            项；历史失败与旧产物在任务技术记录中查看。</small
          >
        </article>
        <p>影响范围：{{ data.scopes?.join("、") || "未记录具体范围" }}</p>
        <p v-if="data.historical" class="secondary">
          历史资料未经本机复验；不据此推断当前代码、交付或发布状态。
        </p>
      </section>
      <section class="content-section readable">
        <h2>选择、取舍与证据</h2>
        <p v-if="data.kind !== 'decision'" class="secondary">
          仅通过显式任务和资料引用列出关联依据；未记录的方案与因果不作推断。
        </p>
        <p v-if="data.result === 'rejected'">
          该方案被否决；具体理由保留在原决策中。
        </p>
        <div
          class="record-row"
          v-for="ref in data.references.filter(
            (ref: any) =>
              ref.kind !== 'task' ||
              !data.delivery_facts?.some((f: any) => f.task === ref.id),
          )"
        >
          <a :href="referenceLink(ref)">{{ ref.title }}</a
          ><small>{{
            ref.kind === "task" ? "实际关联任务" : label(ref.kind)
          }}</small>
        </div>
        <p v-if="data.artifacts.length" class="secondary">
          实际产物在对应任务“执行与验收”中读取（{{ data.artifacts.length }}
          项引用）。
        </p>
        <a v-if="data.superseded_by" :href="knowledgeHref(data.superseded_by)"
          >查看明确记录的后继方案</a
        >
        <p v-else class="secondary">未记录明确后继关系。</p>
      </section></template
    >
  </div>
</template>

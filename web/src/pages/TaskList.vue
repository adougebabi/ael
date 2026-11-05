<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, params } from "../api";
import { route, setQuery, taskHref } from "../router";
import { useResource } from "../resource";
import {
  boards,
  label,
  schedule,
  nextAction,
  stamp,
  blockerText,
} from "../display";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import AttentionCard from "../components/AttentionCard.vue";
import ManualReviewDialog from "../components/ManualReviewDialog.vue";
const r = useResource<any>({ groups: {}, counts: {}, total: 0, attention: [] });
const { data, loading, error, last } = r;
const search = ref(route.value.query.get("q") || "");
const board = computed(() => route.value.query.get("board") || "");
const taskType = computed(() => route.value.query.get("type") || "");
const days = computed(() => route.value.query.get("days") || "30");
let savedClosed: Record<string, boolean> = { completed: true, cancelled: true };
try { savedClosed = { ...savedClosed, ...JSON.parse(sessionStorage.getItem("ael-closed-groups") || "{}") }; } catch {}
const closed = ref<Record<string, boolean>>(savedClosed);
const manualTarget = ref<{ id: string; check: string } | null>(null);
const attention = computed(() => data.value.attention || []);
watch(
  closed,
  (value) => sessionStorage.setItem("ael-closed-groups", JSON.stringify(value)),
  { deep: true },
);
const offset = (id: string) => Number(route.value.query.get("p_" + id) || 0);
function toggle(id: string) {
  closed.value[id] = !closed.value[id];
  sessionStorage.setItem("ael-closed-groups", JSON.stringify(closed.value));
}
function filter(values: Record<string, string>) {
  setQuery({
    ...Object.fromEntries(boards.map(([id]) => ["p_" + id, undefined])),
    ...values,
  });
}
async function load(quiet = false) {
  const query = route.value.query.toString();
  await r.load(async () => {
    const [page, attentionResult] = await Promise.all([
      api(
      "/api/tasks?" +
        params({
          compact: 1,
          limit: 20,
          ...Object.fromEntries(
            boards.map(([id]) => ["offset_" + id, offset(id)]),
          ),
          days: days.value === "all" ? 0 : days.value,
          q: route.value.query.get("q") || "",
          type: taskType.value,
          board: board.value,
        }),
      ),
      api('/api/attention').then((value) => ({ value, error: '' })).catch((error) => ({ value: null, error: String(error) })),
    ]);
    if (query !== route.value.query.toString())
      throw new Error("请求已由新筛选替代");
    return {
      groups: Object.fromEntries(
        boards.map(([id]) => [
          id,
          page.items.filter((item: any) => item.board === id),
        ]),
      ),
      counts: page.counts || {},
      total: page.total || 0,
      attention: attentionResult.value?.items || [],
      attentionTruncated: !!attentionResult.value?.truncated,
      attentionError: attentionResult.error,
    };
  }, quiet);
}
watch(
  () => route.value.key,
  () => {
    search.value = route.value.query.get("q") || "";
    load();
  },
);
let debounce: ReturnType<typeof setTimeout>;
function input() {
  clearTimeout(debounce);
  debounce = setTimeout(() => filter({ q: search.value }), 180);
}
let timer: ReturnType<typeof setInterval>;
onMounted(() => {
  load();
  timer = setInterval(() => load(true), 4000);
});
onUnmounted(() => {
  clearInterval(timer);
  clearTimeout(debounce);
});
</script>
<template>
  <section v-if="attention.length || data.attentionTruncated || data.attentionError" class="attention-section"><div class="section-heading"><h2>需要你处理 <span class="count">{{ attention.length }}</span></h2><small>优先显示每类最近需要处理的事项</small></div><div class="attention-grid"><AttentionCard v-for="item in attention" :key="item.kind" :kind="item.kind" :task="item.task" @review="(id, check) => manualTarget = { id, check }" /></div><p v-if="data.attentionTruncated" class="secondary">待处理事项较多，此处仅检查了最近的部分任务；请使用下方分组查看全部任务。</p><p v-if="data.attentionError" class="secondary">暂时无法读取待处理事项，请刷新工作台重试。</p></section>
  <div class="toolbar">
    <label class="search"
      >搜索任务<input
        v-model="search"
        type="search"
        placeholder="标题、ID 或类型"
        @input="input" /></label
    ><label
      >状态分组<select
        :value="board"
        @change="filter({ board: ($event.target as HTMLSelectElement).value })"
      >
        <option value="">全部分组</option>
        <option v-for="[id, title] in boards" :key="id" :value="id">
          {{ title }}
        </option>
      </select></label
    ><label
      >任务类型<select :value="taskType" @change="filter({ type: ($event.target as HTMLSelectElement).value })"><option value="">全部类型</option><option v-for="kind in ['feature', 'bug', 'improvement', 'refactor', 'research', 'docs', 'chore', 'test', 'qa', 'verify']" :key="kind" :value="kind">{{ kind }}</option></select></label
    ><label
      >完成历史<select
        :value="days"
        @change="filter({ days: ($event.target as HTMLSelectElement).value })"
      >
        <option value="7">最近 7 天</option>
        <option value="30">最近 30 天</option>
        <option value="all">全部（含未知日期）</option>
      </select></label
    ><span>活跃任务不受完成时间限制</span>
  </div>
  <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
  <nav class="group-counts" aria-label="任务分组定位">
    <a
      v-for="[id, title] in boards"
      :key="id"
      :href="'#/tasks?board=' + id"
      @click.prevent="
        filter({ board: id });
        closed[id] = false;
      "
      ><span>{{ title }}</span
      ><strong>{{ data.counts[id] || 0 }}</strong></a
    ><button v-if="board" @click="filter({ board: '' })">查看全部分组</button>
  </nav>
  <div class="board">
    <section
      v-for="[id, title] in boards.filter(([id]) => !board || board === id)"
      :key="id"
      :data-board="id"
      class="task-group"
    >
      <div class="group-heading">
        <button
          class="text-button"
          :aria-expanded="!closed[id]"
          @click="toggle(id)"
        >
          <span aria-hidden="true">{{ closed[id] ? "▸" : "▾" }}</span>
          <h2>{{ title }}</h2>
          <span class="count">{{ data.counts[id] || 0 }}</span>
        </button>
      </div>
      <template v-if="!closed[id]">
        <a
          v-for="s in data.groups[id] || []"
          :id="'task-link-' + s.id"
          :key="s.id"
          class="task task-row"
          :href="taskHref(s.id)"
        >
          <div class="task-title">
            <h3>{{ s.title }}</h3>
            <p>
              <span class="status" :class="s.board">{{ schedule(s) }}</span
              ><span>{{ label(s.step) }}</span
              ><span class="secondary">{{ s.type }}</span>
            </p>
          </div>
          <div class="task-result">
            <p
              :class="{
                'blocked-reason': s.blocker_reason || s.blocking_reasons?.length,
              }"
            >
              {{
                s.blocker_reason
                  ? blockerText(s.blocker_reason)
                  : s.blocking_reasons?.length
                    ? s.blocking_reasons.map(blockerText).join("；")
                    : label(s.last_result)
              }}
            </p>
            <small>下一步：{{ nextAction(s) }}</small
            ><small v-if="s.checks.required"
              >检查：有效通过 {{ s.checks.passed }} 项；必需
              {{ s.checks.required }} 项；执行中 {{ s.checks.running }}；失败
              {{ s.checks.failed }}；未执行 {{ s.checks.not_run
              }}<span v-if="s.checks.historical_failed"
                >；历史失败 {{ s.checks.historical_failed }}</span
              ><span v-if="s.checks.blocked"
                >；受阻 {{ s.checks.blocked }}</span
              ></small
            >
          </div>
          <div class="task-owner">
            <span>{{
              s.executors.length ? s.executors.join("、") : "当前无人执行"
            }}</span
            ><small>{{
              s.activity_at
                ? "有效活动 " + stamp(s.activity_at)
                : "尚无有效活动记录"
            }}</small
            ><small v-if="s.historical">历史记录 · 未在本机复验</small>
          </div>
        </a>
        <p v-if="!loading && !(data.groups[id] || []).length" class="empty">
          {{
            search
              ? "当前筛选无匹配任务。"
              : data.counts[id] || 0
                ? "本页无任务，请返回上一页。"
                : "暂无" + title + "的任务。"
          }}
        </p>
        <Pagination
          v-if="(data.counts[id] || 0) > 20 || offset(id) > 0"
          :offset="offset(id)"
          :limit="20"
          :total="data.counts[id] || 0"
          :name="title"
          @change="setQuery({ ['p_' + id]: $event })"
        />
      </template>
    </section>
  </div>
  <ManualReviewDialog v-if="manualTarget" :task-id="manualTarget.id" :check-id="manualTarget.check" @close="manualTarget = null" @saved="load(true)" />
</template>

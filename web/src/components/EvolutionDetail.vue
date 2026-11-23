<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { href, taskHref, knowledgeHref } from "../router";
import { label } from "../display";
import LoadState from "./LoadState.vue";
import AppIcon from "./AppIcon.vue";

// Reading dialog for one project change. Shows only what the record holds:
// what happened, why, and the result it proves, plus links to the related
// work and specs. Missing parts stay missing; nothing is filled in.
const props = defineProps<{ change: any; loading: boolean; error: string; last: string }>();
const emit = defineEmits<{ close: []; retry: [] }>();
const dialog = ref<HTMLDialogElement>(), closeButton = ref<HTMLButtonElement>();
const previous = document.activeElement as HTMLElement | null;
const c = computed(() => props.change || {});
const refs = computed<any[]>(() => c.value.references || []);
const tasks = computed(() => refs.value.filter((r) => r.kind === "task"));
const sources = computed(() => refs.value.filter((r) => r.kind !== "task"));
const reason = computed(() => c.value.reason && c.value.reason !== "未记录原因" && c.value.reason !== c.value.content ? c.value.reason : "");
const kindText = (k: string) => (k === "delivery" ? "交付结果" : k === "decision" ? "决策与取舍" : k === "spec" ? "规范变化" : label(k));
const tagTone = (x: any) => (["verified", "released", "accepted", "effective"].includes(x.result) ? "done" : x.kind === "delivery" ? "blue" : "");
const sourceLink = (r: any) =>
  r.kind === "finding" && r.task_id ? href("/tasks/" + encodeURIComponent(r.task_id), { section: "findings", finding: r.id }) : r.kind === "task" ? taskHref(r.id) : knowledgeHref(r.id);
function moment(v: string | null | undefined) {
  if (!v || !Number.isFinite(Date.parse(v))) return "日期未确认";
  const t = new Date(v);
  return (t.getFullYear() !== new Date().getFullYear() ? t.getFullYear() + " 年 " : "") + (t.getMonth() + 1) + " 月 " + t.getDate() + " 日 " + t.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}
const when = computed(() => (c.value.time_basis === "latest_delivery" && c.value.occurred_at ? "最近关联交付 " : "") + moment(c.value.occurred_at));
onMounted(async () => { dialog.value?.showModal(); await nextTick(); closeButton.value?.focus(); });
onUnmounted(() => { dialog.value?.close(); previous?.focus({ preventScroll: true }); });
</script>
<template>
  <dialog ref="dialog" class="wb-dialog ev-dialog" aria-labelledby="evolution-detail-title" @cancel.prevent="emit('close')" @click.self="emit('close')">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading"><h2 id="evolution-detail-title">{{ c.title || "项目变化" }}</h2><button ref="closeButton" class="icon-button" aria-label="关闭变化详情" @click="emit('close')"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body">
        <LoadState :loading="loading && !c.id" :error="error" :last="last" @retry="emit('retry')" />
        <article v-if="c.id" class="ev-reader">
          <div class="ev-meta"><span class="tag" :class="tagTone(c)">{{ label(c.result) }}</span><span>{{ kindText(c.kind) }}</span><span>{{ when }}</span><span v-if="c.historical" class="tag">历史记录 · 未复验</span></div>
          <h3>发生了什么</h3>
          <p v-if="c.content">{{ c.content }}</p><p v-else class="ev-missing">未单独记录变化说明，请查看关联工作。</p>
          <h3>为什么调整</h3>
          <p v-if="reason">{{ reason }}</p><p v-else class="ev-missing">未记录调整原因。</p>
          <h3>目前的结果</h3>
          <p>来源能够证明的结论：{{ label(c.result) }}。<template v-if="c.historical">这是历史资料，未在本机复验；当时的决定与当前状态分开阅读，不据此推断当前代码、交付或发布状态。</template><template v-else-if="c.result === 'rejected'">该方案被否决，具体理由保留在原决策中。</template></p>
          <p v-if="c.superseded_by"><a class="ev-link" :href="knowledgeHref(c.superseded_by)">查看现行替代规范</a></p>
          <p v-if="c.delivery_facts?.length" class="ev-small">规范或决策本身的记录时间：{{ moment(c.source_occurred_at) }}；关联工作的结果与时间分别列出。</p>
          <ul v-if="tasks.length" class="ev-work">
            <li v-for="t in tasks" :key="t.id">
              <a :href="taskHref(t.id)">{{ t.title }}</a>
              <div v-for="fact in (c.delivery_facts || []).filter((f: any) => f.task === t.id)" :key="fact.task + ':' + (fact.finding_id || '') + ':' + (fact.candidate_id || '') + ':' + fact.occurred_at" class="ev-fact">
                <p><strong v-if="fact.finding_id">{{ fact.title }} · </strong>{{ label(fact.result) }} · {{ moment(fact.occurred_at) }}</p>
                <p v-if="fact.content">{{ fact.content }}</p>
                <p v-if="fact.candidate_id" class="ev-small">复验候选 {{ fact.candidate_id }}</p>
              </div>
            </li>
          </ul>
          <p v-else class="ev-missing">未记录关联工作。</p>
          <template v-if="sources.length"><h3>关联规范或决策</h3><p v-for="s in sources" :key="s.kind + ':' + s.id" class="ev-source"><a class="ev-link" :href="sourceLink(s)">{{ s.title }}</a><small>{{ label(s.kind) }}</small></p></template>
          <p class="ev-small ev-record">{{ tasks.length }} 项工作 · {{ sources.length }} 份依据 · 影响范围：{{ c.scopes?.join("、") || "未记录具体范围" }}<template v-if="c.artifacts?.length"> · 实际产物 {{ c.artifacts.length }} 项，见对应任务</template></p>
          <p class="ev-small">阅读变化记录不会改变任务或规范；查看关联任务可继续了解当前进度。</p>
        </article>
      </div>
      <div class="wb-dialog-footer"><a v-if="tasks.length === 1" class="button primary" :href="taskHref(tasks[0].id)">查看关联任务</a><button class="button" @click="emit('close')">关闭</button></div>
    </div>
  </dialog>
</template>

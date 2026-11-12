<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, params } from "../api";
import { taskHref } from "../router";
import { stamp } from "../display";

const props = defineProps<{ taskId: string; candidate: string; modelValue: { baseline: string; actual: string } }>();
const emit = defineEmits<{ "update:modelValue": [value: { baseline: string; actual: string }]; changed: [] }>();
const items = ref<any[]>([]), error = ref("");
const attachment = ref<any>(null), attachmentError = ref("");
const previews = ref<Record<string, any>>({});
const zoom = ref(100), overlay = ref(false), opacity = ref(50);
const point = ref<{ x: number; y: number } | null>(null);
const images = computed(() => items.value.filter(item => ["image/png", "image/jpeg"].includes(item.mime)));
const overlayReady = computed(() => overlay.value && !!previews.value[props.modelValue.baseline]?.image && !!previews.value[props.modelValue.actual]?.image);
const chosen = computed(() => [props.modelValue.baseline, props.modelValue.actual]);
let generation = 0;
const statusNames: Record<string,string> = { finalized: "原件可读取", missing: "原件缺失", unavailable: "原件不可用", purged: "原件已清理", uploading: "上传未完成", started: "上传未完成", aborted: "上传已取消" };
function status(item: any) { if (item && previews.value[item.id]?.error) return "原件不可用"; return item ? statusNames[item.status] || "上传未完成" : "证据不存在或不属于本任务"; }
function choose(key: "baseline" | "actual", value: string) {
  emit("update:modelValue", { ...props.modelValue, [key]: value });
  point.value = null;
  emit("changed");
}
async function load() {
  const version = ++generation;
  try {
    const list = await api("/api/review-evidence?" + params({ id: props.taskId }));
    const next: Record<string,any> = {};
    for (const id of chosen.value.filter(Boolean)) {
      const item = list.find((value: any) => value.id === id);
      if (item?.status !== "finalized") continue;
      try { next[id] = await api("/api/artifact?" + params({ task: props.taskId, ref: id })); }
      catch { next[id] = { error: "原件读取失败，请刷新证据" }; }
    }
    const attachmentID = attachment.value?.ref;
    let attachmentUnavailable = false;
    if (attachmentID) {
      try { attachmentUnavailable = (await api("/api/artifact-status?" + params({ task: props.taskId, ref: attachmentID }))).status !== "finalized"; }
      catch { attachmentUnavailable = true; }
    }
    if (version !== generation) return;
    if (attachmentUnavailable && attachment.value?.ref === attachmentID) {
      attachment.value = null; attachmentError.value = "关联原件已不可用，请重新核对证据。";
    }
    items.value = list;
    previews.value = next;
    error.value = "";
  } catch (e) {
    if (version === generation) { error.value = String(e); previews.value = {}; }
  }
}
async function openAttachment(id: string) {
  attachment.value = null; attachmentError.value = "";
  try { attachment.value = await api("/api/artifact?" + params({ task: props.taskId, ref: id })); }
  catch (e) { attachmentError.value = String(e); }
}
function mark(event: MouseEvent) {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
  point.value = { x: Math.round((event.clientX - rect.left) / rect.width * 100), y: Math.round((event.clientY - rect.top) / rect.height * 100) };
}
watch(() => [props.taskId, ...chosen.value], () => void load());
let timer: ReturnType<typeof setInterval>;
let polling = false;
onMounted(() => {
  void load();
  timer = setInterval(async () => {
    if (polling) return;
    polling = true;
    try { await load(); } finally { polling = false; }
  }, 4000);
});
onUnmounted(() => { ++generation; clearInterval(timer); });
</script>

<template>
  <section class="evidence-comparison" aria-label="证据图片比较">
    <h3>证据图片比较</h3>
    <p v-if="error" role="alert">{{ error }}<button type="button" @click="load">刷新证据</button></p>
    <div class="comparison-controls">
      <label>基准图片<select aria-label="基准图片" :value="modelValue.baseline" @change="choose('baseline', ($event.target as HTMLSelectElement).value)"><option value="">请选择已确认来源的基准</option><option v-for="item in images" :key="item.id" :value="item.id">{{ item.filename }} · {{ item.uploader }} · {{ item.candidate?.slice(0,12) || '候选未声明' }} · {{ status(item) }}</option></select></label>
      <label>实际图片<select aria-label="实际图片" :value="modelValue.actual" @change="choose('actual', ($event.target as HTMLSelectElement).value)"><option value="">请选择实际图片</option><option v-for="item in images" :key="item.id" :value="item.id">{{ item.filename }} · {{ item.uploader }} · {{ item.candidate?.slice(0,12) || '候选未声明' }} · {{ status(item) }}</option></select></label>
      <label>缩放 {{ zoom }}%<input v-model.number="zoom" aria-label="缩放" type="range" min="50" max="300" step="25" /></label>
      <label><input v-model="overlay" type="checkbox" />叠加比较</label>
      <label v-if="overlay">实际图片不透明度 {{ opacity }}%<input v-model.number="opacity" type="range" min="0" max="100" /></label>
    </div>
    <p v-if="!modelValue.baseline" class="secondary">基准来源待确认，请明确选择基准图片。</p>
    <div class="comparison-pair">
      <article v-for="(id,index) in chosen" :key="index">
        <h4>{{ index === 0 ? '基准来源' : '实际证据' }}</h4>
        <template v-for="item in [items.find(item => item.id === id)]" :key="id">
          <p v-if="id" role="status">{{ status(item) }}<span v-if="item?.candidate && item.candidate !== candidate"> · 历史候选</span></p>
          <template v-if="item">
            <p>{{ item.filename }} · 上传者 {{ item.uploader }} · {{ stamp(item.created_at) }}</p>
            <p>候选 <code>{{ item.candidate || '未声明' }}</code></p><p>来源 {{ item.source || '未声明' }}</p>
            <div v-for="observation in item.observations" :key="observation.id">
              <p>页面 {{ observation.route || '未声明' }} · 定位 {{ observation.location?.selector || observation.location?.path || '未声明' }} · 视口 {{ observation.viewport ? observation.viewport.width + ' × ' + observation.viewport.height : '未声明' }} · 环境 {{ observation.environment || '未声明' }}</p>
              <a :href="taskHref(taskId, 'findings') + '&finding=' + encodeURIComponent(observation.finding_id)">查看定位与复现记录</a>
              <button v-if="observation.report_artifact_id" type="button" @click="openAttachment(observation.report_artifact_id)">查看比较原始报告</button>
              <button v-for="id in observation.trace_artifacts" :key="id" type="button" @click="openAttachment(id)">查看比较交互证据</button>
            </div>
            <p v-if="!item.observations?.length" class="secondary">页面、视口与环境未随此图片上报。</p>
          </template>
          <p v-if="previews[id]?.error" role="alert">{{ previews[id].error }}</p>
          <div v-if="previews[id]?.image && (!overlayReady || index === 0)" class="comparison-scroll">
            <div class="comparison-canvas" :style="{ width: zoom + '%' }" @click="mark">
              <img :src="previews[id].image" :alt="index === 0 ? '基准图片' : '实际图片'" @error="previews[id] = { error: '图片原件不可用' }" />
              <img v-if="overlayReady" class="comparison-overlay" :src="previews[modelValue.actual].image" alt="叠加实际图片" @error="previews[modelValue.actual] = { error: '图片原件不可用' }" :style="{ opacity: opacity / 100 }" />
              <span v-if="point" class="comparison-point" :style="{ left: point.x + '%', top: point.y + '%' }" aria-label="问题定位">＋</span>
            </div>
          </div>
          <a v-if="previews[id]?.download" :href="previews[id].download" download>下载{{ index === 0 ? '基准' : '实际' }}原件</a>
        </template>
      </article>
    </div>
    <p v-if="attachmentError" role="alert">{{ attachmentError }}</p>
    <section v-if="attachment" aria-label="比较关联证据"><button type="button" @click="attachment = null">关闭比较关联证据</button><pre>{{ attachment.body }}</pre><a v-if="attachment.download" :href="attachment.download" download>下载关联原件</a></section>
    <div v-if="modelValue.actual || modelValue.baseline" class="comparison-controls"><button type="button" @click="point = point ? null : { x: 50, y: 50 }">{{ point ? '清除问题定位' : '添加问题定位' }}</button><template v-if="point"><label>定位横坐标 %<input v-model.number="point.x" type="number" min="0" max="100" /></label><label>定位纵坐标 %<input v-model.number="point.y" type="number" min="0" max="100" /></label></template></div>
  </section>
</template>

<style scoped>
.evidence-comparison { border: 1px solid var(--border, #ccd0d5); border-radius: 8px; padding: 16px; margin-block: 16px; }
.comparison-controls { display: flex; flex-wrap: wrap; gap: 16px; align-items: center; }
.comparison-controls label { display: grid; gap: 6px; max-width: 100%; }
.comparison-controls select { max-width: 340px; }
.comparison-pair { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 16px; }
.comparison-pair p { overflow-wrap: anywhere; }
.comparison-scroll { overflow: auto; max-height: 520px; border: 1px solid var(--border, #ccd0d5); }
.comparison-canvas { position: relative; min-width: 50%; }
.comparison-canvas img { width: 100%; height: auto; display: block; }
.comparison-canvas .comparison-overlay { position: absolute; inset: 0; }
.comparison-point { position: absolute; transform: translate(-50%,-50%); color: #b80000; background: white; border: 1px solid #b80000; border-radius: 50%; font-weight: bold; pointer-events: none; }
@media (max-width: 700px) { .comparison-pair { grid-template-columns: 1fr; } .comparison-controls select { max-width: 100%; } }
</style>

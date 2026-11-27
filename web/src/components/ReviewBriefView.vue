<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { readResult, submittedImplementation } from '../results';
import { isImage, type Material } from '../materials';
import { relativeShort } from '../display';
import MaterialPreview from './MaterialPreview.vue';
import AppIcon from './AppIcon.vue';

// What the reviewer is asked to decide, written by the executor for the exact
// current candidate. Without a current brief the reviewer is told so plainly
// and sees only facts the task records; nothing here records a result.
const props = defineProps<{ snapshot: any; checkId: string; objects?: any[] }>();
const task = computed(() => props.snapshot?.task || {});
const check = computed(() => (task.value.definition?.checks || []).find((c: any) => c.id === props.checkId) || {});
const brief = computed(() => (task.value.review_briefs || []).find((b: any) => b.check === props.checkId && task.value.candidate && b.candidate === task.value.candidate) || null);
const staleBrief = computed(() => !brief.value && (task.value.review_briefs || []).some((b: any) => b.check === props.checkId));
const work = computed(() => submittedImplementation(props.snapshot));
const covers = computed<string[]>(() => check.value.covers || []);

const PAGE = 12;
const shown = ref(PAGE);
watch(() => props.checkId, () => { shown.value = PAGE; });
const evidence = computed<Material[]>(() => (brief.value?.evidence || []).map((ref: string) => {
  const meta = (props.objects || []).find((o: any) => o.id === ref);
  return { id: ref, ref, task: task.value.definition?.id, name: meta?.filename || ref.split('/').pop() || ref, mime: meta?.mime || (/\.png$/i.test(ref) ? 'image/png' : /\.jpe?g$/i.test(ref) ? 'image/jpeg' : ''), size: meta?.size, source: meta?.source, candidate: task.value.candidate, state: 'current', role: 'evidence', checks: [props.checkId] } as Material;
}));
const images = computed(() => evidence.value.filter(isImage));
const files = computed(() => evidence.value.filter((m) => !isImage(m)));
const visibleImages = computed(() => images.value.slice(0, shown.value));

const thumbs = ref<Record<string, string>>({});
const failed = ref<Record<string, boolean>>({});
watch(visibleImages, async (list) => {
  for (const m of list) {
    if (thumbs.value[m.ref] || failed.value[m.ref]) continue;
    try { const v = await readResult(m.task, m.ref); if (v?.image) thumbs.value = { ...thumbs.value, [m.ref]: v.image }; else failed.value = { ...failed.value, [m.ref]: true }; }
    catch { failed.value = { ...failed.value, [m.ref]: true }; }
  }
}, { immediate: true });
const preview = ref<Material | null>(null);
</script>
<template>
  <section class="wb-brief" :class="{ missing: !brief }" :aria-label="'检查 ' + checkId + ' 的审核说明'">
    <template v-if="brief">
      <p class="wb-brief-eyebrow"><AppIcon name="user" />请你判断</p>
      <p class="wb-brief-summary">{{ brief.summary }}</p>
      <p v-if="brief.changes" class="wb-brief-changes">{{ brief.changes }}</p>
      <div v-if="brief.steps?.length" class="wb-brief-block"><h4>怎么查看</h4><ol><li v-for="(s, i) in brief.steps" :key="i">{{ s }}</li></ol></div>
      <div v-if="brief.focus?.length" class="wb-brief-block"><h4>重点看</h4><ul><li v-for="(s, i) in brief.focus" :key="i">{{ s }}</li></ul></div>
      <div v-if="evidence.length" class="wb-brief-block">
        <h4>相关材料 <span class="tiny-count">{{ evidence.length }}</span><small v-if="images.length"> · {{ images.length }} 张截图</small></h4>
        <div v-if="images.length" class="wb-brief-gallery">
          <button v-for="m in visibleImages" :key="m.ref" type="button" class="wb-brief-thumb" :aria-label="'放大查看 ' + m.name" :title="m.name" @click="preview = m">
            <img v-if="thumbs[m.ref]" :src="thumbs[m.ref]" :alt="m.name" loading="lazy" />
            <span v-else class="muted">{{ failed[m.ref] ? '无法读取' : '读取中…' }}</span>
            <small>{{ m.name }}</small>
          </button>
        </div>
        <button v-if="images.length > shown" type="button" class="small ghost" @click="shown += PAGE * 2">再显示 {{ Math.min(PAGE * 2, images.length - shown) }} 张（共 {{ images.length }} 张）</button>
        <div v-if="files.length" class="wb-review-files"><button v-for="m in files" :key="m.ref" type="button" class="small" @click="preview = m"><AppIcon name="file" />{{ m.name }}</button></div>
      </div>
      <p class="wb-brief-meta">由 {{ brief.actor || '执行者' }} 说明<template v-if="brief.recorded_at"> · {{ relativeShort(brief.recorded_at) }}</template> · 针对当前版本</p>
    </template>
    <template v-else>
      <p class="wb-brief-eyebrow"><AppIcon name="warning" />{{ staleBrief ? '审核说明已过期' : '执行者还没有说明要审核什么' }}</p>
      <p class="wb-brief-changes">{{ staleBrief ? '已有的说明针对更早的版本，当前版本改动后执行者尚未更新。' : '下面只有任务记录里已有的信息。' }}如果仍无法判断，可以选「需要调整」，请执行者说明要看哪里、改了什么。</p>
      <div v-if="covers.length" class="wb-brief-block"><h4>验收条件</h4><ul><li v-for="c in covers" :key="c">{{ c }}</li></ul></div>
      <div v-if="work?.summary" class="wb-brief-block"><h4>执行者提交时的说明{{ work.current ? '' : '（较早版本）' }}</h4><p class="wb-brief-changes">{{ work.summary }}</p></div>
    </template>
  </section>
  <MaterialPreview v-if="preview" :material="preview" :task-title="task.definition?.title" @close="preview = null" />
</template>

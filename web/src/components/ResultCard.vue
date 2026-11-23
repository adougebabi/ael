<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import { readResult, resultExcerpt } from '../results';
import { isImage, bytes, type Material } from '../materials';
import { relativeShort } from '../display';
import AppIcon from './AppIcon.vue';
// Image thumbnails and report covers load only when the card is visible.
const props = defineProps<{ material: Material; meta?: string }>();
defineEmits<{ open: [] }>();
const root = ref<HTMLElement>(), src = ref(''), summary = ref(''), failed = ref('');
let observer: IntersectionObserver | undefined, seq = 0;
const report = () => !isImage(props.material) && /\.(json|md|txt)$/i.test(props.material.name) && (props.material.size || 0) <= 262144;
async function load() {
  const current = ++seq;
  try { const v = await readResult(props.material.task, props.material.ref); if (current !== seq) return; src.value = v.image || ''; if (v.body) summary.value = resultExcerpt(v.body).summary; }
  catch { if (current === seq) failed.value = '原件不可用'; }
}
function observe() {
  observer?.disconnect(); src.value = ''; summary.value = ''; failed.value = ''; seq++;
  if (!(isImage(props.material) || report()) || !root.value) return;
  observer = new IntersectionObserver((entries) => { if (entries.some((x) => x.isIntersecting)) { observer?.disconnect(); void load(); } }, { rootMargin: '120px' });
  observer.observe(root.value);
}
onMounted(observe); watch(() => props.material.ref, observe); onUnmounted(() => { seq++; observer?.disconnect(); });
const kindLabel = () => /\.json$/i.test(props.material.name) ? 'RESULT REPORT' : /\.(md|txt)$/i.test(props.material.name) ? 'DOCUMENT' : 'FILE';
</script>
<template>
  <button ref="root" class="wb-result-card" :aria-label="'查看 ' + material.name" @click="$emit('open')">
    <div v-if="isImage(material)" class="wb-result-image"><img v-if="src" :src="src" :alt="material.name" loading="lazy" @error="failed = '图片无法显示'" /><span v-else class="wb-result-placeholder">{{ failed || '读取图片…' }}</span><span class="wb-zoom-hint"><AppIcon name="zoom" /></span></div>
    <div v-else class="wb-report-cover"><AppIcon :name="kindLabel() === 'DOCUMENT' ? 'book' : 'file'" /><span class="wb-report-label">{{ kindLabel() }}</span><strong>{{ summary || material.name }}</strong><small v-if="!isImage(material) && (material.size || 0) > 262144" class="wb-report-long">报告较长，打开读取结论与原文</small><span class="wb-report-bars"><span></span><span></span><span style="opacity:.4"></span></span></div>
    <div class="wb-result-info"><h3>{{ material.name }}</h3><p>{{ meta || [bytes(material.size), material.at ? relativeShort(material.at) : '', material.state === 'historical' ? '历史版本' : material.state === 'unknown' ? '适用性待核对' : ''].filter(Boolean).join(' · ') || '大小与时间未记录' }}</p></div>
  </button>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { remoteProject } from '../api';
import { href } from '../router';
import { snapshotResults, mergeResultMetadata, scopedResults, importedResults, type ResultFile } from '../results';
import { candidateMaterials, isImage, roleLabel, type Material } from '../materials';
import ResultCard from './ResultCard.vue';
import MaterialPreview from './MaterialPreview.vue';
import AppIcon from './AppIcon.vue';
// Current results first; inputs, history and unknown-applicability material
// stay readable below and are never counted as this task's results.
const props = defineProps<{ snapshot: any; objects: any[]; imported?: any; hide?: string[]; onlyOther?: boolean }>();
const task = computed(() => props.snapshot.task);
const toMaterial = (f: ResultFile, role: Material['role']): Material => ({ id: f.ref, ref: f.ref, task: f.task, name: f.name || f.ref.split('/').pop() || f.ref, mime: f.mime || '', size: f.size, at: f.at, state: f.state, role, checks: [...new Set((f.associations || []).map((a) => a.check).filter(Boolean) as string[])] });
const files = computed(() => mergeResultMetadata([...snapshotResults(props.snapshot), ...importedResults(props.imported, task.value.definition.id)], props.objects || [], task.value.definition.id));
const mats = computed(() => candidateMaterials(props.objects || [], task.value));
const current = computed<Material[]>(() => {
  const out = scopedResults(files.value, 'current').map((f) => toMaterial(f, f.associations?.some((a) => a.role === 'check') ? 'evidence' : 'output'));
  for (const m of mats.value.current) if (!out.some((x) => x.ref === m.ref)) out.push(m);
  return out.filter((m) => !(props.hide || []).includes(m.ref)).sort((a, b) => Number(isImage(b)) - Number(isImage(a)));
});
const other = computed<Material[]>(() => {
  const seen = new Set(current.value.map((m) => m.ref).concat(props.hide || []));
  const out: Material[] = [];
  const push = (m: Material) => { if (!seen.has(m.ref)) { seen.add(m.ref); out.push(m); } };
  for (const m of mats.value.inputs) push({ ...m, role: 'input' });
  for (const f of scopedResults(files.value, 'historical')) push(toMaterial(f, 'output'));
  for (const m of mats.value.prior) push(m);
  for (const f of scopedResults(files.value, 'related')) push(toMaterial(f, f.associations?.some((a) => a.role === 'input') ? 'input' : 'related'));
  return out;
});
const otherLabel = (m: Material) => m.role === 'input' ? '输入资料' : m.state === 'historical' ? '历史版本' : '适用性待核对';
const preview = ref<Material | null>(null);
const done = computed(() => task.value.status === 'completed');
</script>
<template>
  <section v-if="current.length && !onlyOther" class="wb-content-section" aria-labelledby="results-title">
    <div class="wb-section-header"><h2 id="results-title">{{ done ? '交付结果' : '本次成果' }}</h2><span class="tiny-count">{{ current.length }}</span><span class="muted">材料不等于验收通过</span></div>
    <div class="wb-result-cards"><ResultCard v-for="m in current.slice(0, 4)" :key="m.ref" :material="m" @open="preview = m" /></div>
    <div v-if="current.length > 4 && remoteProject" class="wb-result-footnote"><a class="text-button" :href="href('/materials', { task: task.definition.id })">查看全部 {{ current.length }} 份成果</a></div>
  </section>
  <p v-else-if="!onlyOther && task.status === 'open' && !snapshot.task.historical" class="muted wb-small-note">当前步骤尚未提交新的成果；已有进展见执行记录。</p>
  <details v-if="other.length" class="content-section" aria-label="历史成果与相关资料">
    <summary>历史成果与相关资料 · {{ other.length }} 份</summary>
    <div class="wb-other-list"><p v-for="m in other.slice(0, 30)" :key="m.ref"><button class="text-button" @click="preview = m"><AppIcon :name="isImage(m) ? 'image' : 'file'" />{{ m.name }}</button><span class="tag">{{ otherLabel(m) }}</span><span class="muted">{{ roleLabel[m.role] }}</span></p><p v-if="other.length > 30" class="muted">还有 {{ other.length - 30 }} 份，<a v-if="remoteProject" :href="href('/materials', { task: task.definition.id })">在资料中查看</a></p></div>
  </details>
  <MaterialPreview v-if="preview" :material="preview" :task-title="task.definition.title" :note="preview.state !== 'current' ? '这份材料不是本任务当前候选的成果，只供查阅。' : ''" @close="preview = null" />
</template>

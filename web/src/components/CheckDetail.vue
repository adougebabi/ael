<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { checkReading, checkHistory, evidenceSource, methodLabel } from '../checks';
import { label, stamp } from '../display';
import { remoteProject } from '../api';
import type { Material } from '../materials';
import AppIcon from './AppIcon.vue';
import MaterialPreview from './MaterialPreview.vue';

// Detail of one check of one task: status from the check record, the
// acceptance items it covers, and the materials that record references.
// Reading never changes the task.
const props = defineProps<{ snapshot: any; check: any; objects?: any[] }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(), closeButton = ref<HTMLButtonElement>();
const previous = document.activeElement as HTMLElement | null;
const n = computed(() => props.snapshot), t = computed(() => n.value.task), c = computed(() => props.check);
const r = computed(() => checkReading(n.value, c.value));
const record = computed(() => n.value.current_checks?.[c.value.id]);
const access = computed(() => n.value.check_access?.[c.value.id] || {});
const history = computed(() => checkHistory(n.value, c.value.id));
const object = (ref: string) => (props.objects || []).find((o: any) => o.id === ref);
const name = (ref: string) => object(ref)?.filename || ref.split('/').pop() || ref;
const material = (ref: string, state: Material['state']): Material => { const o = object(ref); return { id: ref, ref, task: t.value.definition.id, name: name(ref), mime: o?.mime || '', size: o?.size, at: o?.created_at, source: o?.source, candidate: o?.candidate, sha256: o?.sha256, status: o?.status, state, role: 'evidence', checks: [c.value.id] }; };
const preview = ref<Material | null>(null);
const candidateText = (x?: string) => !x ? '未记录' : x === t.value.candidate ? '当前候选 ' + x.slice(0, 12) : '候选 ' + x.slice(0, 12);
onMounted(async () => { dialog.value?.showModal(); await nextTick(); closeButton.value?.focus(); });
onUnmounted(() => { dialog.value?.close(); previous?.focus({ preventScroll: true }); });
</script>
<template>
  <dialog ref="dialog" class="wb-dialog cd-dialog" aria-labelledby="check-detail-title" @cancel.prevent="emit('close')" @click.self="emit('close')">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading"><div><p class="cd-kicker">{{ label(c.kind) }} · {{ c.required === false ? '辅助检查' : '必需检查' }} · {{ methodLabel(access.effective_method || c.method) }}</p><h2 id="check-detail-title">{{ c.id }} · {{ t.definition.title }}</h2></div><span class="tag" :class="r.tone === 'pass' ? 'done' : r.tone === 'fail' ? 'red' : r.tone === 'warn' ? 'amber' : ''">{{ r.label }}</span><button ref="closeButton" class="icon-button" aria-label="关闭检查详情" @click="emit('close')"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body">
        <section class="cd-box" :class="r.tone"><h3>{{ r.title }}</h3><p class="fact-full">{{ r.detail }}</p><p class="cd-next"><span>下一步</span>{{ r.next }}</p></section>
        <h3 class="cd-sub">验收条件</h3>
        <ul v-if="c.covers?.length" class="cd-covers"><li v-for="a in c.covers" :key="a"><AppIcon :name="r.key === 'pass' ? 'tick' : r.tone === 'fail' ? 'alert' : 'clock'" />{{ a }}</li></ul>
        <p v-else class="muted">定义未声明这项检查覆盖哪些条件。</p>
        <div v-if="record?.artifacts?.length || record?.diagnostic_artifacts?.length" class="cd-actions">
          <button v-for="(ref, i) in record.artifacts" :key="ref" :class="i === 0 ? 'primary' : 'button'" @click="preview = material(ref, record.candidate === t.candidate ? 'current' : 'historical')">打开 {{ name(ref) }}</button>
          <button v-for="ref in record.diagnostic_artifacts || []" :key="ref" class="button" @click="preview = material(ref, 'unknown')">诊断材料 {{ name(ref) }}</button>
        </div>
        <details class="wb-disclosure"><summary>检查来源与适用条件</summary><div class="wb-disclosure-body"><dl class="rd-facts">
          <dt>检查方式</dt><dd>{{ methodLabel(access.effective_method || c.method) }}</dd>
          <template v-if="c.command?.length"><dt>命令</dt><dd><code>{{ c.command.join(' ') }}</code></dd></template>
          <dt>当前候选</dt><dd>{{ candidateText(t.candidate) }}</dd>
          <template v-if="record"><dt>记录适用</dt><dd>{{ candidateText(record.candidate) }} · 定义 {{ record.acceptance_revision }} / 计划 {{ record.plan_revision }}</dd>
            <dt>记录来源</dt><dd>{{ evidenceSource(record) }}<template v-if="record.executor"> · {{ record.executor }}</template><template v-if="record.environment"> · {{ record.environment }}</template></dd>
            <template v-if="record.exit_code != null"><dt>退出码</dt><dd>{{ record.exit_code }}</dd></template>
            <dt>记录时间</dt><dd>{{ stamp(record.at) }}</dd>
            <template v-if="record.config_hash"><dt>配置摘要</dt><dd><code>{{ record.config_hash.slice(0, 16) }}…</code></dd></template></template>
        </dl>
          <template v-if="history.length"><h4>历史记录（{{ history.length }}）</h4><ul class="cd-history"><li v-for="(h, i) in history.slice(0, 10)" :key="i">{{ label(h.result) }} · {{ stamp(h.at) }} · {{ candidateText(h.candidate) }}<template v-if="h.reason"> — {{ h.reason }}</template><button v-for="ref in h.artifacts || []" :key="ref" v-show="remoteProject" class="text-button" @click="preview = material(ref, 'historical')">{{ name(ref) }}</button></li></ul></template>
        </div></details>
      </div>
      <div class="wb-dialog-footer"><span class="muted">阅读详情不会改变任务状态，其他必要条件仍独立保留。</span><span class="spacer"></span><button class="primary" @click="emit('close')">返回</button></div>
    </div>
    <MaterialPreview v-if="preview" :material="preview" :checks="n.current_checks" :current-candidate="t.candidate" :task-title="t.definition.title" @close="preview = null" />
  </dialog>
</template>

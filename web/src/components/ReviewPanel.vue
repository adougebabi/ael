<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { api, ApiError, info, remoteProject } from '../api';
import { readableError, label } from '../display';
import { readReviewDraft, writeReviewDraft, clearReviewDraft } from '../reviewDraft';
import { snapshotResults, mergeResultMetadata, scopedResults, readResult } from '../results';
import { candidateMaterials, isImage, type Material } from '../materials';
import MaterialPreview from './MaterialPreview.vue';
import AppIcon from './AppIcon.vue';

// One human check, reviewed where its result is visible. Submitting records
// only this check through the existing review protocol; the server decides
// what the task does next. Other checks and parent conditions stay in force.
const props = defineProps<{ snapshot: any; check: any; objects: any[]; compact?: boolean }>();
const emit = defineEmits<{ saved: [result: string, message: string]; advanced: [] }>();
const task = computed(() => props.snapshot.task);
const scope = info.value.project || '';
const access = computed(() => props.snapshot.check_access?.[props.check.id] || {});
const recorded = computed(() => props.snapshot.current_checks?.[props.check.id] || null);
const mayJudge = computed(() => !remoteProject.value || ['admin', 'reviewer'].includes(remoteProject.value.role));
const eligible = computed(() => task.value.status === 'open' && !!access.value.can_review);
const saved = readReviewDraft(scope, task.value.definition.id, props.check.id);
const target = ref({ candidate: saved?.candidate || task.value.candidate, revision: saved?.revision || props.snapshot.definition_revision, plan_revision: saved?.plan_revision || props.snapshot.plan_revision });
const changed = computed(() => target.value.candidate !== task.value.candidate || target.value.revision !== props.snapshot.definition_revision || target.value.plan_revision !== props.snapshot.plan_revision);
const pending = ref<any>(saved?.pending?.action === 'review_result' ? saved.pending : null);
const actor = ref(saved?.actor || sessionStorage.getItem('ael-actor') || '');
const mode = ref<'' | 'adjust'>(saved?.quickChoice === 'fail' ? 'adjust' : ''), reason = ref(saved?.reason || '');
const busy = ref(false), error = ref(''), receipt = ref(''), receiptTone = ref('');
function persist() { writeReviewDraft(scope, task.value.definition.id, props.check.id, { ...readReviewDraft(scope, task.value.definition.id, props.check.id), ...target.value, actor: actor.value, quickChoice: mode.value === 'adjust' ? 'fail' : '', checkId: props.check.id, taskTitle: task.value.definition.title, reason: reason.value, pending: pending.value }); }

// Materials: check-bound current evidence first, then this task's uploads on
// the current candidate. Prior-candidate images are only for comparison.
const checkFiles = computed<Material[]>(() => scopedResults(mergeResultMetadata(snapshotResults(props.snapshot), props.objects || [], task.value.definition.id), 'check', props.check.id)
  .map((f) => ({ id: f.ref, ref: f.ref, task: f.task, name: f.name || f.ref.split('/').pop() || f.ref, mime: f.mime || '', size: f.size, state: 'current', role: 'evidence', checks: [props.check.id] })));
const mats = computed(() => candidateMaterials(props.objects || [], task.value));
// Check-bound image first; otherwise the most recent upload on the current candidate.
const shownImage = computed(() => checkFiles.value.find(isImage) || latestImage(mats.value.current) || null);
function latestImage(list: Material[]) { const images = list.map((m, i) => ({ m, i, t: Date.parse(m.at || '') })).filter((x) => isImage(x.m)); images.sort((a, b) => (Number.isFinite(a.t) && Number.isFinite(b.t) && a.t !== b.t ? b.t - a.t : b.i - a.i)); return images[0]?.m; }
// Check-bound files plus images and reports of the current candidate; plain
// work files stay in the results section below.
const otherFiles = computed(() => [...checkFiles.value, ...mats.value.current.filter((m) => isImage(m) || /\.(json|md)$/i.test(m.name))].filter((m, i, all) => m.ref !== shownImage.value?.ref && all.findIndex((x) => x.ref === m.ref) === i).slice(0, 4));
const priorImage = computed(() => mats.value.prior.find(isImage) || null);
const evidenceMissing = computed(() => !!access.value.evidence_required && !checkFiles.value.length);
// Required specific evidence is attached in the full review record; a quick
// direct observation never stands in for it.
const evidenceRequired = computed(() => !!access.value.evidence_required);
const question = computed(() => props.check.covers?.join('；') || props.check.id);
const otherOpen = computed(() => (task.value.definition.checks || []).filter((c: any) => c.required !== false && c.id !== props.check.id && props.snapshot.current_checks?.[c.id]?.result !== 'pass'));

const imageSrc = ref(''), imageError = ref('');
let imageSeq = 0;
watch(shownImage, async (m) => {
  const current = ++imageSeq; imageSrc.value = ''; imageError.value = '';
  if (!m) return;
  try { const v = await readResult(m.task, m.ref); if (current === imageSeq) imageSrc.value = v.image || ''; }
  catch { if (current === imageSeq) imageError.value = '图片原件不可用或无权读取'; }
}, { immediate: true });
const preview = ref<Material | null>(null), previewNote = ref('');
function openPreview(m: Material, note = '') { previewNote.value = note; preview.value = m; }
const compareOpen = ref(false), compareDialog = ref<HTMLDialogElement>(), priorSrc = ref('');
async function openCompare() {
  compareOpen.value = true; priorSrc.value = '';
  await nextTick();
  compareDialog.value?.showModal();
  if (priorImage.value) try { priorSrc.value = (await readResult(priorImage.value.task, priorImage.value.ref)).image || ''; } catch { priorSrc.value = ''; }
}
function closeCompare() { compareDialog.value?.close(); compareOpen.value = false; }

async function submit(result: 'pass' | 'fail') {
  if (busy.value || (!pending.value && changed.value)) return;
  if (!pending.value && result === 'fail' && !reason.value.trim()) { error.value = '请写下具体需要调整的地方。'; return; }
  busy.value = true; error.value = '';
  try {
    const identity = remoteProject.value ? (await api('/api/info')).actor : actor.value.trim();
    if (!identity) throw new Error('请填写记录人。');
    if (!remoteProject.value) sessionStorage.setItem('ael-actor', identity);
    const payload = pending.value || { action: 'review_result', id: task.value.definition.id, unit: props.check.id, result, reason: result === 'fail' ? reason.value.trim() : '', recovery: '', artifacts: [], executor: identity, ...target.value, request_key: crypto.randomUUID() };
    pending.value = payload; persist();
    const next = await api('/api/action', payload);
    pending.value = null; clearReviewDraft(scope, task.value.definition.id, props.check.id);
    const done = next?.task?.status === 'completed' || next?.next_action === 'completed';
    receiptTone.value = payload.result === 'pass' ? 'ok' : '';
    receipt.value = payload.result === 'pass'
      ? (done ? '本项检查已通过，任务已按原有交付规则完成。父事项的条件仍独立判断。' : '本项检查已通过。' + (otherOpen.value.length ? '其他必要条件继续保留：' + otherOpen.value.map((c: any) => c.id + '（' + label(c.method) + '）').join('、') + '。' : '后续交付条件仍按原规则推进。'))
      : '反馈已关联到「' + task.value.definition.title + '」的当前检查，任务回到原有修复流程，等待执行者处理。';
    mode.value = ''; reason.value = '';
    emit('saved', payload.result, receipt.value);
  } catch (e) {
    if (e instanceof ApiError && /VERSION|CANDIDATE|NOT_ALLOWED|FORBIDDEN|REQUIRED|MISMATCH|TERMINAL|EVIDENCE/.test(e.code)) pending.value = null;
    error.value = pending.value ? '提交结果未知：服务可能已经记录。请原样重试，同一请求不会被重复记录。' : readableError(String(e));
    persist();
  } finally { busy.value = false; }
}
// A lost response is resolved by reading the authority: when the current
// record for this check matches the pending request, it was committed.
watch(recorded, (value) => {
  const p = pending.value;
  if (!p || !value || value.candidate !== p.candidate || value.result !== p.result || value.acceptance_revision !== p.revision) return;
  pending.value = null; clearReviewDraft(scope, task.value.definition.id, props.check.id); error.value = '';
  receiptTone.value = value.result === 'pass' ? 'ok' : '';
  receipt.value = '服务端已记录这次提交（' + label(value.result) + '），原请求已生效，无需重试。';
  emit('saved', value.result, receipt.value);
}, { immediate: true });
function recheck() { if (pending.value) return; target.value = { candidate: task.value.candidate, revision: props.snapshot.definition_revision, plan_revision: props.snapshot.plan_revision }; error.value = ''; persist(); }
const head = computed(() => recorded.value?.result === 'pass' ? '验收记录' : eligible.value && mayJudge.value ? '需要你的确认' : eligible.value ? '等待有权限的人确认' : '验收条件');
const tag = computed(() => recorded.value?.result === 'pass' ? ['已通过', 'done'] : recorded.value?.result === 'fail' ? ['需要调整', 'red'] : recorded.value?.result === 'blocked' ? ['暂无法验证', 'amber'] : eligible.value ? ['待验收', 'blue'] : ['尚未进入验收', '']);
</script>
<template>
  <section class="wb-inline-review" :class="{ compact }" :data-check="check.id" :aria-label="'人工检查 ' + check.id">
    <div class="wb-review-head"><AppIcon name="user" /><strong>{{ head }}</strong><span class="wb-review-check">{{ check.id }} · {{ label(check.kind) }}</span><span class="spacer"></span><span class="tag" :class="tag[1]">{{ tag[0] }}</span></div>
    <div class="wb-review-body">
      <p class="wb-review-question">{{ question }}</p>
      <button v-if="shownImage" class="wb-review-image" :aria-label="'放大查看 ' + shownImage.name" @click="openPreview(shownImage)"><img v-if="imageSrc && !imageError" :src="imageSrc" :alt="shownImage.name" @error="imageError = '图片原件损坏或无法显示'" /><span v-else class="muted">{{ imageError || '正在读取图片…' }}</span></button>
      <div v-if="otherFiles.length" class="wb-review-files"><button v-for="m in otherFiles" :key="m.ref" class="small" @click="openPreview(m)"><AppIcon :name="isImage(m) ? 'image' : 'file'" />{{ m.name }}</button></div>
      <p v-if="!shownImage && !otherFiles.length" class="wb-review-empty">暂无附件。{{ evidenceMissing ? '本项要求专项证据（' + (check.artifacts?.join('、') || (access.screenshot_required ? '真实截图' : '约定材料')) + '），当前尚未关联所需材料；需由执行者补充后再验收。' : eligible ? '可以直接观察实际结果；这项检查不要求额外上传附件。' : '进入验证阶段后，再由对应责任方核对这一条件。' }}</p>
      <p v-if="evidenceRequired && !evidenceMissing" class="wb-review-help">本项要求专项证据：请在完整验收记录中关联上面的材料后确认。</p>
      <p class="wb-review-help"><template v-if="shownImage && !checkFiles.length">图片来自本任务当前候选的上传，供查看实际效果；确认通过记录为具名直接观察，需要附图时用「更多选项」。</template>仅对当前检查作出判断，其他必要条件继续独立保留。</p>
      <p v-if="recorded" class="wb-review-recorded">已记录：{{ label(recorded.result) }} · {{ recorded.source?.replace('human:', '') || '记录人未知' }}<template v-if="recorded.reason"> · {{ recorded.reason }}</template></p>
      <div v-if="changed && !receipt" class="wb-banner error" role="alert">验收对象或修订已变化，旧判断不会套用到新版本；你的说明已保留。<button v-if="!pending" class="small" :disabled="busy" @click="recheck">已重新查看，采用当前版本</button></div>
      <p v-if="receipt" class="wb-banner" :class="receiptTone" role="status">{{ receipt }}</p>
      <p v-if="error" class="wb-banner error" role="alert">{{ error }}</p>
      <form v-if="mode === 'adjust' && !receipt" class="wb-adjust-form" @submit.prevent="submit('fail')">
        <label>具体问题<textarea v-model="reason" rows="3" required placeholder="例如：菜单关闭后焦点没有回到 owner 按钮" @input="persist" /></label>
        <p class="wb-review-help">反馈只关联「{{ task.definition.title }}」的这项检查，任务回到执行者修复。</p>
        <div class="wb-review-controls"><button class="primary" type="submit" :disabled="busy || changed">{{ busy ? '正在提交…' : '提交反馈' }}</button><button type="button" :disabled="busy" @click="mode = ''; persist()">取消</button></div>
      </form>
      <div v-else-if="!receipt && eligible && mayJudge" class="wb-review-controls">
        <label v-if="!remoteProject" class="wb-actor">记录人<input v-model="actor" autocomplete="name" @input="persist" /></label>
        <button v-if="pending" class="primary" :disabled="busy" @click="submit(pending.result)">原样重试提交</button>
        <template v-else>
          <button class="primary" :disabled="busy || changed || evidenceRequired" @click="submit('pass')"><AppIcon name="tick" />{{ busy ? '正在提交…' : '确认通过' }}</button>
          <button :disabled="busy || changed" @click="mode = 'adjust'; persist()">需要调整</button>
          <button v-if="shownImage && priorImage" class="text-button" @click="openCompare">对比改动前后</button>
        </template>
        <button class="text-button wb-review-more" :disabled="busy" @click="emit('advanced')">{{ evidenceRequired ? (evidenceMissing ? '补充专项证据与完整记录' : '关联专项证据并确认') : '更多选项' }}</button>
      </div>
      <p v-else-if="!receipt && eligible" class="wb-review-help">你的角色可以查看这项检查；正式结论由有验收权限的人提交。</p>
    </div>
  </section>
  <MaterialPreview v-if="preview" :material="preview" :task-title="task.definition.title" :note="previewNote" @close="preview = null" />
  <dialog v-if="compareOpen" ref="compareDialog" class="wb-dialog wide" aria-labelledby="compare-title" @cancel.prevent="closeCompare">
    <div class="wb-dialog-frame">
      <div class="wb-dialog-heading"><h2 id="compare-title">{{ task.definition.title }} · 改动对比</h2><button class="icon-button" aria-label="关闭对比" @click="closeCompare"><AppIcon name="x" /></button></div>
      <div class="wb-dialog-body">
        <div class="wb-comparison-grid">
          <div><h3><span class="tag">调整前 · 历史候选</span></h3><button v-if="priorImage" class="wb-comparison-image" :aria-label="'放大 ' + priorImage.name" @click="openPreview(priorImage, '这张图属于更早的候选，只作对比，不是当前验收材料。')"><img v-if="priorSrc" :src="priorSrc" :alt="priorImage.name" /><span v-else class="muted">正在读取…</span></button><p class="wb-compare-caption">{{ priorImage?.name }}<template v-if="priorImage?.source"> · {{ priorImage.source }}</template></p></div>
          <div><h3><span class="tag blue">调整后 · 当前候选</span></h3><button v-if="shownImage" class="wb-comparison-image" :aria-label="'放大 ' + shownImage.name" @click="openPreview(shownImage)"><img v-if="imageSrc && !imageError" :src="imageSrc" :alt="shownImage.name" @error="imageError = '图片原件损坏或无法显示'" /></button><p class="wb-compare-caption">{{ shownImage?.name }}<template v-if="shownImage?.source"> · {{ shownImage.source }}</template></p></div>
        </div>
      </div>
      <div class="wb-dialog-footer"><button @click="closeCompare">返回验收</button></div>
    </div>
  </dialog>
</template>

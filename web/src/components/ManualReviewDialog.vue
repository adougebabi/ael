<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, ApiError, params } from "../api";
import { goalLabel, label, nextAction, readableError, stamp } from "../display";
import { navigate } from "../router";
import AppIcon from "./AppIcon.vue";
import { readReviewDraft, writeReviewDraft, clearReviewDraft, readPriorWork, clearPriorWork } from "../reviewDraft";

const props = defineProps<{ taskId: string; checkId: string }>();
const emit = defineEmits<{ close: []; saved: [] }>();
const dialog = ref<HTMLDialogElement>();
const previous = document.activeElement as HTMLElement | null;
const snapshot = ref<any>(null);
const loading = ref(true), busy = ref(false), error = ref("");
const outcome = ref(""), reason = ref(""), recovery = ref(""), extraRefs = ref("");
const actor = ref(sessionStorage.getItem("ael-actor") || "");
const selected = ref<string[]>([]), confirmed = ref(false);
const receipt = ref<any>(null), receiptError = ref("");
const receiptKind = ref(""), receiptNote = ref<any>(null), selectedNoteId = ref(""), evidenceOpen = ref(false);
const pendingReview = ref<any>(null);
const requestKey = ref(globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`);
const draftReady = ref(false), draftVersionChanged = ref(false), storageWarning = ref(false), reviewedCandidate = ref("");
const projectScope = ref("");
const priorWork = ref<any>(null);
const check = computed(() => snapshot.value?.task.definition.checks.find((c: any) => c.id === props.checkId));
const access = computed(() => snapshot.value?.check_access?.[props.checkId]);
const canRecord = computed(() => snapshot.value?.task.status === "open" && (!props.checkId || (check.value && access.value?.effective_method === "human" && !check.value.command?.length)));
const provisional = computed(() => pendingReview.value ? pendingReview.value.action !== "review_result" : !props.checkId || !access.value?.can_review);
const reviewNotes = computed<any[]>(() => [...(snapshot.value?.task.human_review_notes || [])].reverse().filter((note: any) => !props.checkId || !note.check || note.check === props.checkId));
const availableRefs = computed<string[]>(() => {
  const task = snapshot.value?.task;
  if (!task) return [];
  const refs = new Set<string>();
  for (const attempt of task.attempts || []) {
    if (attempt.status === "pass" && attempt.candidate_version === task.candidate)
      for (const ref of attempt.outputs || []) refs.add(ref);
  }
  for (const evidence of task.evidence || []) {
    if (evidence.candidate === task.candidate && evidence.acceptance_revision === task.definition.definition_revision && evidence.plan_revision === task.plan.revision)
      for (const ref of evidence.artifacts || []) refs.add(ref);
  }
  for (const ref of pendingReview.value?.artifacts || []) refs.add(ref);
  return [...refs];
});
function saveDraft() {
  if (!draftReady.value || !snapshot.value || receipt.value) return;
  if (!outcome.value && !reason.value && !recovery.value && !extraRefs.value && !selected.value.length && !pendingReview.value) {
    clearReviewDraft(projectScope.value, props.taskId, props.checkId);
    return;
  }
  storageWarning.value = !writeReviewDraft(projectScope.value, props.taskId, props.checkId, {
    checkId: props.checkId, taskTitle: snapshot.value.task.definition.title, candidate: snapshot.value.task.candidate,
    revision: snapshot.value.definition_revision, plan_revision: snapshot.value.plan_revision,
    outcome: outcome.value, reason: reason.value, recovery: recovery.value, extraRefs: extraRefs.value,
    actor: actor.value, selected: selected.value, selectedNoteId: selectedNoteId.value, pending: pendingReview.value, requestKey: requestKey.value,
  });
}
function restoreDraft() {
  const saved = readReviewDraft(projectScope.value, props.taskId, props.checkId);
  if (saved && snapshot.value) {
    outcome.value = typeof saved.outcome === "string" ? saved.outcome : "";
    reason.value = typeof saved.reason === "string" ? saved.reason : "";
    recovery.value = typeof saved.recovery === "string" ? saved.recovery : "";
    extraRefs.value = typeof saved.extraRefs === "string" ? saved.extraRefs : "";
    evidenceOpen.value = !!extraRefs.value;
    actor.value = typeof saved.actor === "string" ? saved.actor : actor.value;
    selected.value = Array.isArray(saved.selected) ? saved.selected.filter((v: unknown) => typeof v === "string") : [];
    selectedNoteId.value = typeof saved.selectedNoteId === "string" && reviewNotes.value.some((note) => note.id === saved.selectedNoteId && note.status === "pending") ? saved.selectedNoteId : "";
    if (["review_result", "review_pending", "review_confirm"].includes(saved.pending?.action) && saved.pending.id === props.taskId && saved.pending.unit === props.checkId && saved.pending.request_key) {
      pendingReview.value = saved.pending;
      requestKey.value = saved.pending.request_key;
      confirmed.value = true;
    } else {
      draftVersionChanged.value = saved.candidate !== snapshot.value.task.candidate || saved.revision !== snapshot.value.definition_revision || saved.plan_revision !== snapshot.value.plan_revision;
      confirmed.value = false;
    }
  }
  draftReady.value = true;
}
watch([outcome, reason, recovery, extraRefs, actor, selected, selectedNoteId, pendingReview], saveDraft, { deep: true, flush: "sync" });
async function load() {
  loading.value = true;
  error.value = "";
  try {
    snapshot.value = await api("/api/task?" + params({ id: props.taskId }));
    priorWork.value = readPriorWork(props.taskId, snapshot.value.task.attempts || []);
    try { projectScope.value = (await api("/api/info")).project || ""; } catch { projectScope.value = ""; }
    if (props.checkId && !check.value) error.value = "CHECK_MISSING: 这项检查已不在当前定义中。";
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
    if (snapshot.value && !draftReady.value) restoreDraft();
  }
}
async function reloadVersion() {
  confirmed.value = false;
  await load();
}
onMounted(async () => {
  dialog.value?.showModal();
  await load();
});
onUnmounted(() => previous?.focus({ preventScroll: true }));
function close() {
  if (!busy.value) { saveDraft(); emit("close"); }
}
function goTo(path: string) {
  if (busy.value) return;
  emit("close");
  navigate(path);
}
function dismissPriorWork() {
  if (!priorWork.value) return;
  clearPriorWork(props.taskId, priorWork.value.attemptId);
  priorWork.value = readPriorWork(props.taskId, snapshot.value?.task.attempts || []);
}
async function submit() {
  if (busy.value || !snapshot.value || (!canRecord.value && !pendingReview.value)) return;
  error.value = "";
  const refs = [...new Set([...selected.value, ...extraRefs.value.split(",").map((x) => x.trim()).filter(Boolean)])];
  if (!pendingReview.value && (!outcome.value || !actor.value.trim() || !reason.value.trim() || !confirmed.value)) {
    error.value = "REVIEW_INPUT_REQUIRED: 请明确选择结论，填写观察和记录人，并确认审阅的候选。";
    return;
  }
  if (!pendingReview.value && !provisional.value && outcome.value !== "pass" && !recovery.value.trim()) {
    error.value = "RECOVERY_REQUIRED: 请填写修复方向或继续验证的条件。";
    return;
  }
  if (!pendingReview.value && !provisional.value && outcome.value !== "blocked" && !refs.length) {
    error.value = "HUMAN_EVIDENCE_REQUIRED: 通过或不通过都需要关联真实证据。";
    return;
  }
  const payload = pendingReview.value || {
      action: provisional.value ? "review_pending" : "review_result", id: props.taskId, unit: props.checkId,
      executor: actor.value.trim(), reason: reason.value.trim(), recovery: recovery.value.trim(),
      result: outcome.value, artifacts: refs, request_key: requestKey.value,
      candidate: snapshot.value.task.candidate,
      revision: snapshot.value.definition_revision, plan_revision: snapshot.value.plan_revision,
      ...(!provisional.value && selectedNoteId.value ? { review_note_id: selectedNoteId.value } : {}),
    };
  await send(payload);
}
function useNote(note: any) {
  outcome.value = note.result; reason.value = note.reason; recovery.value = note.recovery || "";
  extraRefs.value = (note.artifacts || []).join(",");
  evidenceOpen.value = !!extraRefs.value;
  if (!actor.value.trim()) actor.value = note.actor;
  selectedNoteId.value = note.id; confirmed.value = false;
}
async function confirmNote(note: any) {
  if (busy.value || !snapshot.value?.task.candidate) return;
  const reviewer = actor.value.trim() || note.actor;
  if (!reviewer) { error.value = "REVIEW_INPUT_REQUIRED: 请填写记录人。"; return; }
  await send({ action: "review_confirm", id: props.taskId, unit: props.checkId, review_note_id: note.id,
    executor: reviewer, request_key: requestKey.value, candidate: snapshot.value.task.candidate,
    revision: snapshot.value.definition_revision, plan_revision: snapshot.value.plan_revision });
}
async function send(payload: any) {
  busy.value = true;
  error.value = "";
  try {
    pendingReview.value = payload;
    reviewedCandidate.value = payload.candidate;
    const saved = await api("/api/action", payload);
    receiptKind.value = payload.action === "review_result" ? "formal" : saved.status === "confirmed" ? "confirmed" : "pending";
    receiptNote.value = payload.action === "review_result" ? null : saved;
    receipt.value = payload.action === "review_result" ? saved : snapshot.value;
    pendingReview.value = null;
    clearReviewDraft(projectScope.value, props.taskId, props.checkId);
    try { sessionStorage.setItem("ael-actor", actor.value.trim() || payload.executor); } catch { /* The result is already recorded. */ }
    emit("saved");
    try {
      receipt.value = await api("/api/task?" + params({ id: props.taskId }));
      if (receiptNote.value) {
        const latest = receipt.value.task?.human_review_notes?.find((note: any) => note.id === receiptNote.value.id);
        if (latest) {
          receiptNote.value = latest;
          if (latest.status === "confirmed") { receiptKind.value = "confirmed"; reviewedCandidate.value = latest.confirmed_candidate; }
        }
      }
    } catch (e) {
      receiptError.value = String(e);
    }
  } catch (e) {
    if (e instanceof ApiError && ["REVIEW_VERSION_MISMATCH", "CANDIDATE_CHANGED", "REVIEW_NOT_ALLOWED", "REVIEW_VERSION_REQUIRED", "REVIEW_EVIDENCE_CHANGED", "REVIEW_INPUT_REQUIRED", "HUMAN_EVIDENCE_REQUIRED", "RECOVERY_REQUIRED", "SCREENSHOT_REQUIRED", "MANUAL_CHECK_REQUIRED", "CHECK_METHOD_MISMATCH", "CHECK_MISSING", "ARTIFACT_MISSING", "UNSAFE_PATH", "SYMLINK_PATH", "INVALID_RESULT", "REVIEW_NOTE_MISSING", "REVIEW_NOTE_VERSION_MISMATCH", "REVIEW_NOTE_ALREADY_CONFIRMED", "REVIEW_NOTE_CHECK_MISMATCH", "REVIEW_CHECK_CONFIRM_REQUIRED", "TERMINAL_TASK"].includes(e.code)) pendingReview.value = null;
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <dialog ref="dialog" class="action-dialog manual-dialog" :class="{ 'manual-receipt-dialog': receipt }" aria-labelledby="manual-dialog-title" @cancel.prevent="close">
    <form @submit.prevent="submit">
      <header class="manual-header">
        <div><h2 id="manual-dialog-title">{{ receipt ? "验收结果已记录" : "记录人工验收" }}</h2><p v-if="snapshot">{{ snapshot.task.definition.id }} · {{ snapshot.task.definition.title }}</p></div>
        <button type="button" class="dialog-close" :disabled="busy" aria-label="关闭" @click="close">×</button>
      </header>
      <div class="manual-body">
      <p v-if="loading" role="status">正在读取当前候选和检查…</p>
      <template v-else-if="snapshot && !receipt">
        <div class="candidate-bar"><AppIcon name="lock"/><span>{{ pendingReview ? '待确认提交的候选' : '当前候选' }} <strong :title="pendingReview?.candidate || snapshot.task.candidate">{{ (pendingReview?.candidate || snapshot.task.candidate)?.slice(0, 12) || "尚未生成" }}</strong> · 定义修订 {{ pendingReview?.revision || snapshot.definition_revision }} · 计划修订 {{ pendingReview?.plan_revision || snapshot.plan_revision }}</span><small>{{ checkId ? '仅作用于此检查' : '任务级人工意见' }}</small></div>
        <p v-if="draftVersionChanged && !pendingReview" class="alert" role="status">候选或修订已变化，已保留草稿；请重新检查当前版本并再次确认。</p>
        <p v-if="storageWarning" class="alert" role="status">浏览器无法保存验收草稿，关闭页面后输入可能丢失。</p>
        <section v-if="priorWork" class="review-note"><strong>发现本标签页上次未提交的实施表单</strong><p>这些文字没有被自动提交或视为验收结论。</p><button v-if="priorWork.summary" type="button" @click="reason = reason ? reason + '\n' + priorWork.summary : priorWork.summary">带入上次填写的工作说明</button><details><summary>查看旧填写</summary><p v-if="priorWork.summary">工作说明：{{ priorWork.summary }}</p><p v-if="priorWork.artifacts">产物引用：{{ priorWork.artifacts }}</p><p v-if="priorWork.knowledge_reason">规范处理依据：{{ priorWork.knowledge_reason }}</p><p v-if="priorWork.knowledge_artifacts">规范产物：{{ priorWork.knowledge_artifacts }}</p><p v-if="priorWork.return_to">期望退回：{{ priorWork.return_to }}</p></details><button type="button" @click="dismissPriorWork">移除旧草稿</button></section>
        <p v-if="provisional && !pendingReview && canRecord" class="review-pending-explanation">{{ !snapshot.task.candidate ? '尚未生成验收候选；本次先记录人工意见，候选生成后需要你重新确认。' : checkId ? '此检查当前尚不能正式验收，本次只记录人工意见。' : '本次记录针对当前候选的任务级人工意见，不替代其他检查。' }}</p>
        <div v-if="!canRecord && !pendingReview" class="alert" role="alert">任务已结束，或此检查不支持人工验收；原有检查方式不会被修改。</div>
        <section v-if="reviewNotes.length && !pendingReview" class="review-note-list"><h3>已记录的人工意见</h3><article v-for="note in reviewNotes.slice(0, 3)" :key="note.id" class="review-note"><strong>{{ label(note.result) }} · {{ note.actor }}</strong><small>{{ stamp(note.recorded_at) }} · {{ note.status === 'pending' ? '待候选确认' : note.status === 'confirmed' ? '已确认候选' : '历史记录' }}</small><p>{{ note.reason }}</p><button v-if="note.status === 'pending' && access?.can_review && note.definition_revision === snapshot.definition_revision && note.plan_revision === snapshot.plan_revision" type="button" @click="useNote(note)">引用这条意见</button><button v-if="note.status === 'pending' && !checkId && !note.check && snapshot.task.candidate && note.definition_revision === snapshot.definition_revision && note.plan_revision === snapshot.plan_revision" type="button" :disabled="busy" @click="confirmNote(note)">确认这条意见适用于当前候选</button><small v-if="note.status === 'pending' && (note.definition_revision !== snapshot.definition_revision || note.plan_revision !== snapshot.plan_revision)">定义或计划已变化，请重新记录意见。</small></article></section>
        <fieldset v-if="canRecord || pendingReview" class="review-fields" :disabled="!!pendingReview">
          <section class="review-criteria"><h3>{{ checkId ? checkId + ' · ' + label(check?.kind) : '任务验收条件' }}</h3><ul v-if="(checkId ? check?.covers : snapshot.task.definition.acceptance)?.length"><li v-for="item in (checkId ? check.covers : snapshot.task.definition.acceptance)" :key="item">{{ item }}</li></ul><p v-else>未记录具体标准，请先核实任务定义。</p></section>
          <fieldset class="review-outcomes"><legend>验收结论 <span aria-hidden="true">*</span></legend><div>
            <label v-for="[value, text] in [['pass', '通过'], ['fail', '不通过'], ['blocked', '暂无法验证']]" :key="value" :class="{ selected: outcome === value }"><input v-model="outcome" type="radio" name="review-outcome" :value="value" /><AppIcon :name="value === 'pass' ? 'check' : value === 'fail' ? 'warning' : 'clock'" />{{ text }}</label>
          </div></fieldset>
          <label><span>实际观察与理由 <b aria-hidden="true">*</b></span><textarea v-model="reason" rows="3" placeholder="描述实际检查的内容与观察结果"></textarea></label>
          <label v-if="outcome && outcome !== 'pass'"><span>{{ outcome === 'fail' ? '修复方向' : '继续验证的条件' }} <b v-if="!provisional" aria-hidden="true">*</b><small v-else>（可选）</small></span><textarea v-model="recovery" rows="2" placeholder="说明下一步需要修复或恢复什么"></textarea></label>
          <details v-if="provisional && !availableRefs.length" class="optional-review-evidence" :open="evidenceOpen" @toggle="evidenceOpen = ($event.target as HTMLDetailsElement).open"><summary>关联证据（可选）</summary><label>补充已有证据引用<input v-model="extraRefs" placeholder="项目相对路径或 runtime: 引用，多个以逗号分隔" /></label><small>如填写引用，系统会核验文件真实存在。</small></details>
          <fieldset v-else class="evidence-field"><legend>关联真实证据 <span v-if="provisional">（可选）</span></legend><div class="evidence-picker"><p v-if="!availableRefs.length">当前候选没有已记录的产物引用，可填写已有项目相对路径或 runtime: 引用。</p><label v-for="ref in availableRefs" :key="ref"><input v-model="selected" type="checkbox" :aria-label="ref" :value="ref" /><AppIcon name="file"/><span><strong>{{ ref.split('/').pop() }}</strong><small>{{ ref }}</small></span></label><label class="extra-evidence">补充已有证据引用<input v-model="extraRefs" placeholder="项目相对路径或 runtime: 引用，多个以逗号分隔" /></label></div></fieldset>
          <p v-if="!provisional" class="secondary">通过和不通过需要真实证据；视觉检查还需要真实 PNG/JPEG 截图。暂无法验证可不附证据。</p>
          <div class="review-meta-grid"><label><span>记录人 <b aria-hidden="true">*</b></span><input v-model="actor" autocomplete="name" /></label><div><span>提交后的行为</span><p>{{ provisional ? (snapshot.task.candidate && !checkId ? '记录任务级意见并绑定当前候选，其他检查保持原样。' : '记录人工意见，候选生成后需要重新确认，不会自动通过检查。') : outcome === 'pass' ? '记录此项通过后重新核对其余门禁与交付目标。' : outcome === 'fail' ? '记录未通过，并由任务流程确定修复与下一步。' : outcome === 'blocked' ? '记录无法验证的原因和继续验证条件，不会视为通过。' : '请选择验收结论，查看对应的后续行为。' }}</p></div></div>
          <p v-if="selectedNoteId" class="secondary">已引用此前记录的意见；提交前请重新核对当前候选和证据。</p>
          <label class="inline"><input v-model="confirmed" type="checkbox" />{{ provisional && !snapshot.task.candidate ? '我确认以上是当前人工观察，候选生成后需要重新确认。' : '我确认以上结论针对所示候选和修订。' }}</label>
        </fieldset>
        <p v-if="pendingReview" role="status" class="alert">上次提交结果尚未确认；请原样重试，以免重复记录验收。</p>
      </template>
      <section v-if="receipt" class="review-receipt" role="status"><span class="receipt-icon"><AppIcon :name="receiptKind === 'formal' && receipt.task?.status === 'completed' ? 'check' : 'clock'" /></span>
        <h3>{{ receiptKind === 'pending' ? '人工验收意见已记录' : receiptKind === 'confirmed' ? '人工验收意见已确认' : receipt.task?.status === 'completed' ? '任务已完成' : receipt.current_step === 'delivery' ? '等待实际交付' : '任务仍有后续工作' }}</h3>
        <p>{{ receiptKind === 'pending' ? '待候选确认；本次记录不会改变检查通过状态。' : receiptKind === 'confirmed' ? '意见已绑定当前候选；其他检查仍按原有方式执行。' : receipt.task?.status === 'completed' ? '已记录本次验收并达到任务交付目标。' : '本次验收已记录，后续状态由实际检查与交付条件决定。' }}</p>
        <dl class="receipt-facts"><div><dt>验收范围</dt><dd>{{ checkId || '整个任务' }} · {{ label(receiptNote?.result || outcome) }}</dd></div><div><dt>必要检查</dt><dd>{{ receipt.summary?.checks ? receipt.summary.checks.required_passed + ' / ' + receipt.summary.checks.required + ' 项通过' : '状态待读取' }}</dd></div><div><dt>验收候选</dt><dd :title="reviewedCandidate">{{ reviewedCandidate?.slice(0, 12) || '尚未生成' }}</dd></div><div><dt>交付目标</dt><dd>{{ goalLabel(snapshot.task.definition.goal) }}</dd></div></dl>
        <p>{{ receipt.task?.status === 'completed' ? '实际结果：' + label(receipt.task.result) : '当前节点：' + label(receipt.current_step) }}</p>
        <p v-if="receiptKind === 'pending'">下一步：候选生成后，回来确认这条人工意见。</p>
        <p v-else-if="receipt.summary">下一步：{{ nextAction(receipt.summary) }}<span v-if="receipt.summary.waiting_checks">；仍有 {{ receipt.summary.waiting_checks }} 项检查待处理</span>。</p>
        <p v-else>状态读取尚未完成，请刷新任务查看最新结果。</p>
        <p v-if="receiptError" class="secondary">结果已记录，但刷新任务状态失败。</p>
      </section>
      <div v-if="error" role="alert" class="alert"><p>{{ readableError(error) }}</p><details><summary>具体原因</summary><pre>{{ error }}</pre></details><button v-if="snapshot && !receipt && /REVIEW_VERSION|CANDIDATE_CHANGED|REVIEW_NOT_ALLOWED/.test(error)" type="button" @click="reloadVersion">重新读取版本并保留输入</button></div>
      </div>
      <div class="actions dialog-actions"><small v-if="!receipt">候选或修订变化后，需要重新核对。</small><button v-if="!receipt" type="button" :disabled="busy" @click="close">取消</button><button v-if="!receipt && !loading && (canRecord || pendingReview)" type="submit" class="primary" :disabled="busy">{{ busy ? '正在提交…' : pendingReview ? '原样重试提交' : '提交验收结果' }}</button><button v-if="receipt" type="button" @click="goTo('/tasks')">回工作台</button><button v-if="receipt" type="button" class="primary" @click="goTo('/tasks/' + encodeURIComponent(taskId) + '?section=checks')">查看任务结果</button><button v-if="error && !snapshot" type="button" @click="load">重新读取</button></div>
    </form>
  </dialog>
</template>

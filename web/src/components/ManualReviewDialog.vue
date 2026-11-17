<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, ApiError, params, uploadReviewImage, remoteProject } from "../api";
import { goalLabel, label, nextAction, readableError, stamp } from "../display";
import { navigate } from "../router";
import AppIcon from "./AppIcon.vue";
import EvidenceComparison from "./EvidenceComparison.vue";
import { readReviewDraft, writeReviewDraft, clearReviewDraft, readPriorWork, clearPriorWork } from "../reviewDraft";

const props = defineProps<{ taskId: string; checkId: string }>();
const emit = defineEmits<{ close: []; saved: []; selectCheck: [check: string] }>();
const dialog = ref<HTMLDialogElement>();
const previous = document.activeElement as HTMLElement | null;
const snapshot = ref<any>(null);
const loading = ref(true), busy = ref(false), error = ref(""), inputError = ref("");
const candidateState = ref<"none" | "checking" | "current" | "changed" | "error">("none");
const blockedHumanCheck = ref("");
const uploading = ref(false);
const comparison = ref({ baseline: "", actual: "" }), comparisonOpen = ref(false);
const remoteEvidence = ref<any[]>([]);
const evidenceByID = computed(() => new Map(remoteEvidence.value.map((item) => [item.id, item])));
function evidenceName(ref: string) {
  return evidenceByID.value.get(ref)?.filename || ref.split("/").pop() || ref;
}
const outcome = ref(""), reason = ref(""), recovery = ref(""), extraRefs = ref("");
const actor = ref(sessionStorage.getItem("ael-actor") || "");
const selected = ref<string[]>([]), confirmed = ref(false);
const receipt = ref<any>(null), receiptError = ref("");
const postReviewChanged = ref(false);
const receiptKind = ref(""), receiptNote = ref<any>(null), selectedNoteId = ref(""), evidenceOpen = ref(false);
const composing = ref(true), showAllNotes = ref(false);
const pendingReview = ref<any>(null);
const requestKey = ref(globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`);
const draftReady = ref(false), draftVersionChanged = ref(false), storageWarning = ref(false), reviewedCandidate = ref("");
const projectScope = ref("");
const priorWork = ref<any>(null);
const check = computed(() => snapshot.value?.task.definition.checks.find((c: any) => c.id === props.checkId));
const access = computed(() => snapshot.value?.check_access?.[props.checkId]);
const canRecord = computed(() => snapshot.value?.task.status === "open" && (!remoteProject.value || (!!projectScope.value && ["admin", "reviewer"].includes(remoteProject.value.role))) && (!props.checkId || (check.value && access.value?.effective_method === "human" && !check.value.command?.length)));
const provisional = computed(() => pendingReview.value ? pendingReview.value.action !== "review_result" : !props.checkId || !access.value?.can_review);
const humanChecks = computed<any[]>(() => (snapshot.value?.task.definition.checks || []).filter((c: any) => snapshot.value?.check_access?.[c.id]?.effective_method === "human" && !c.command?.length && snapshot.value?.current_checks?.[c.id]?.result !== "pass"));
const selectingCheck = computed(() => !props.checkId && !pendingReview.value && snapshot.value?.task.status === "open" && snapshot.value?.current_step === "verify" && !!snapshot.value?.task.candidate && humanChecks.value.length > 0);
const pausedReview = computed(() => !!props.checkId && !!snapshot.value?.task.paused && snapshot.value?.current_step === "verify" && !!snapshot.value?.task.candidate);
const blockedHere = computed(() => pausedReview.value && blockedHumanCheck.value === props.checkId);
const candidateUnavailable = computed(() => !!snapshot.value?.task.candidate && candidateState.value !== "current");
const reviewNotes = computed<any[]>(() => [...(snapshot.value?.task.human_review_notes || [])].reverse());
const pendingNotes = computed(() => reviewNotes.value.some((note: any) => note.status === "pending"));
const currentPendingNotes = computed(() => reviewNotes.value.some((note: any) => note.status === "pending" && noteIsCurrent(note)));
const currentConfirmedNotes = computed(() => reviewNotes.value.some((note: any) => note.status === "confirmed" && !!snapshot.value?.task.candidate && note.confirmed_candidate === snapshot.value.task.candidate && note.definition_revision === snapshot.value?.definition_revision && note.plan_revision === snapshot.value?.plan_revision));
const visibleNotes = computed(() => showAllNotes.value ? reviewNotes.value : reviewNotes.value.slice(0, 3));
function noteIsCurrent(note: any) {
  return note.definition_revision === snapshot.value?.definition_revision && note.plan_revision === snapshot.value?.plan_revision && (!note.candidate || note.candidate === snapshot.value?.task.candidate);
}
function noteAppliesToCheck(note: any) { return !!props.checkId && (!note.check || note.check === props.checkId); }
const availableRefs = computed<string[]>(() => {
  const task = snapshot.value?.task;
  if (!task) return [];
  const refs = new Set<string>();
  if (task.candidate) {
    if (remoteProject.value) {
      for (const item of remoteEvidence.value) {
        if (item.status === "finalized" && item.candidate === task.candidate) refs.add(item.id);
      }
    } else {
      for (const attempt of task.attempts || []) {
        if (attempt.status === "pass" && attempt.candidate_version === task.candidate)
          for (const ref of attempt.outputs || []) refs.add(ref);
      }
      for (const evidence of task.evidence || []) {
        if (evidence.candidate === task.candidate && evidence.acceptance_revision === task.definition.definition_revision && evidence.plan_revision === task.plan.revision)
          for (const ref of evidence.artifacts || []) refs.add(ref);
      }
    }
  }
  for (const ref of pendingReview.value?.artifacts || []) refs.add(ref);
  for (const ref of selected.value) refs.add(ref);
  return [...refs];
});
function saveDraft() {
  if (!draftReady.value || !snapshot.value || receipt.value) return;
  if (!outcome.value && !reason.value && !recovery.value && !extraRefs.value && !selected.value.length && !comparison.value.baseline && !comparison.value.actual && !pendingReview.value) {
    clearReviewDraft(projectScope.value, props.taskId, props.checkId);
    return;
  }
  storageWarning.value = !writeReviewDraft(projectScope.value, props.taskId, props.checkId, {
    checkId: props.checkId, taskTitle: snapshot.value.task.definition.title, candidate: snapshot.value.task.candidate,
    revision: snapshot.value.definition_revision, plan_revision: snapshot.value.plan_revision,
    outcome: outcome.value, reason: reason.value, recovery: recovery.value, extraRefs: extraRefs.value,
    comparison: comparison.value, actor: actor.value, selected: selected.value, selectedNoteId: selectedNoteId.value, pending: pendingReview.value, requestKey: requestKey.value,
  });
}
function restoreDraft() {
  const saved = readReviewDraft(projectScope.value, props.taskId, props.checkId);
  let hasDraft = false;
  if (saved && snapshot.value) {
    comparison.value = { baseline: typeof saved.comparison?.baseline === "string" ? saved.comparison.baseline : "", actual: typeof saved.comparison?.actual === "string" ? saved.comparison.actual : "" };
    comparisonOpen.value = !!(comparison.value.baseline || comparison.value.actual);
    outcome.value = typeof saved.outcome === "string" ? saved.outcome : "";
    reason.value = typeof saved.reason === "string" ? saved.reason : "";
    recovery.value = typeof saved.recovery === "string" ? saved.recovery : "";
    extraRefs.value = typeof saved.extraRefs === "string" ? saved.extraRefs : "";
    evidenceOpen.value = !!extraRefs.value;
    actor.value = typeof saved.actor === "string" ? saved.actor : actor.value;
    selected.value = Array.isArray(saved.selected) ? saved.selected.filter((v: unknown) => typeof v === "string") : [];
    hasDraft = !!(outcome.value || reason.value || recovery.value || extraRefs.value || selected.value.length || comparison.value.baseline || comparison.value.actual);
    selectedNoteId.value = typeof saved.selectedNoteId === "string" && reviewNotes.value.some((note) => note.id === saved.selectedNoteId && note.status === "pending" && noteAppliesToCheck(note) && noteIsCurrent(note)) ? saved.selectedNoteId : "";
    if (["review_result", "review_pending", "review_confirm"].includes(saved.pending?.action) && saved.pending.id === props.taskId && saved.pending.unit === props.checkId && saved.pending.request_key) {
      pendingReview.value = saved.pending;
      requestKey.value = saved.pending.request_key;
      confirmed.value = true;
    } else {
      draftVersionChanged.value = saved.candidate !== snapshot.value.task.candidate || saved.revision !== snapshot.value.definition_revision || saved.plan_revision !== snapshot.value.plan_revision;
      confirmed.value = false;
    }
  }
  composing.value = !!pendingReview.value || hasDraft || !reviewNotes.value.some((note) => !props.checkId || noteAppliesToCheck(note));
  draftReady.value = true;
}
watch([outcome, reason, recovery, extraRefs, actor, selected, selectedNoteId, pendingReview, comparison], saveDraft, { deep: true, flush: "sync" });
watch([outcome, reason, actor, confirmed], () => { inputError.value = ""; });
async function load() {
  loading.value = true;
  inputError.value = "";
  error.value = "";
  try {
    snapshot.value = await api("/api/task?" + params({ id: props.taskId }));
    remoteEvidence.value = remoteProject.value && snapshot.value.task.candidate
      ? await api("/api/review-evidence?" + params({ id: props.taskId }))
      : [];
    if (selectedNoteId.value && !reviewNotes.value.some(note => note.id === selectedNoteId.value && noteIsCurrent(note))) selectedNoteId.value = "";
    priorWork.value = remoteProject.value ? null : readPriorWork(props.taskId, snapshot.value.task.attempts || []);
    try { const info = await api("/api/info"); projectScope.value = info.project || ""; if (remoteProject.value) actor.value = info.actor || ""; } catch (e) { if (remoteProject.value) { candidateState.value = "error"; throw e; } projectScope.value = ""; }
    if (props.checkId && !check.value) error.value = "CHECK_MISSING: 这项检查已不在当前定义中。";
    if (snapshot.value.task.candidate) {
      candidateState.value = "checking";
      try {
        const candidate = await api("/api/review-candidate?" + params({ id: props.taskId }));
        candidateState.value = candidate.candidate === snapshot.value.task.candidate && candidate.matches ? "current" : "changed";
        blockedHumanCheck.value = candidate.blocked_check || "";
      } catch (e) { candidateState.value = "error"; blockedHumanCheck.value = ""; error.value = String(e); }
    } else { candidateState.value = "none"; blockedHumanCheck.value = ""; }
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
    if (snapshot.value && !draftReady.value && (!remoteProject.value || projectScope.value)) restoreDraft();
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
  if (!busy.value && !uploading.value) { saveDraft(); emit("close"); }
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
function importPriorWork() {
  if (!priorWork.value?.summary) return;
  composing.value = true;
  reason.value = reason.value ? reason.value + "\n" + priorWork.value.summary : priorWork.value.summary;
}
async function resumeReview() {
  if (busy.value || !snapshot.value || !blockedHere.value || candidateState.value !== "current") return;
  busy.value = true; error.value = "";
  const candidate = snapshot.value.task.candidate, revision = snapshot.value.definition_revision, planRevision = snapshot.value.plan_revision;
  try {
    await api("/api/action", { action: "review_resume", id: props.taskId, unit: props.checkId, candidate, revision, plan_revision: planRevision, request_key: "resume-" + requestKey.value });
    emit("saved");
    await load();
  } catch (e) {
    // The response may be lost after the resume committed. Re-read the exact
    // task and candidate before offering a retry or the formal review form.
    const originalError = String(e);
    await load();
    if (snapshot.value?.task.candidate === candidate && snapshot.value?.definition_revision === revision && snapshot.value?.plan_revision === planRevision && !snapshot.value?.task.paused && access.value?.can_review && candidateState.value === "current") {
      emit("saved");
      error.value = "";
    } else error.value = originalError;
  }
  finally { busy.value = false; }
}
async function addImage(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0];
  if (!file || uploading.value) return;
  uploading.value = true; error.value = "";
  try {
    const ref = await uploadReviewImage(props.taskId, file);
    if (!selected.value.includes(ref)) selected.value = [...selected.value, ref];
  } catch (e) { error.value = String(e); }
  finally { uploading.value = false; input.value = ""; }
}
async function submit() {
  if (busy.value || uploading.value || !snapshot.value || (!pendingReview.value && (candidateUnavailable.value || pausedReview.value || selectingCheck.value)) || (!canRecord.value && !pendingReview.value)) return;
  error.value = "";
  inputError.value = "";
  const { baseline, actual } = remoteProject.value && snapshot.value.task.candidate ? comparison.value : { baseline: "", actual: "" };
  const refs = [...new Set([...selected.value, ...(remoteProject.value && actual ? [actual] : []), ...extraRefs.value.split(",").map((x) => x.trim()).filter(Boolean)])];
  if (!pendingReview.value) {
    const missing = [
      !outcome.value && "请选择验收结论",
      !reason.value.trim() && "请填写实际观察与理由",
      !actor.value.trim() && "请填写记录人",
      !confirmed.value && (snapshot.value.task.candidate ? "请勾选当前版本确认" : "请勾选人工观察确认"),
    ].filter(Boolean);
    if (missing.length) {
      inputError.value = missing.join("。") + "。";
      return;
    }
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
      ...(remoteProject.value && baseline && actual ? { comparison: { baseline, actual } } : {}),
      candidate: snapshot.value.task.candidate,
      revision: snapshot.value.definition_revision, plan_revision: snapshot.value.plan_revision,
      ...(!provisional.value && selectedNoteId.value ? { review_note_id: selectedNoteId.value } : {}),
    };
  await send(payload);
}
function useNote(note: any) {
  composing.value = true;
  outcome.value = note.result; reason.value = note.reason; recovery.value = note.recovery || "";
  extraRefs.value = (note.artifacts || []).join(",");
  evidenceOpen.value = !!extraRefs.value;
  if (!actor.value.trim()) actor.value = note.actor;
  if (note.comparison) {
    comparison.value = { baseline: note.comparison.baseline?.artifact_id || "", actual: note.comparison.actual?.artifact_id || "" };
    comparisonOpen.value = true;
  }
  selectedNoteId.value = note.id; confirmed.value = false;
}
async function confirmNote(note: any) {
  if (busy.value || candidateUnavailable.value || !snapshot.value?.task.candidate) return;
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
    const committedVersion = payload.action === "review_result" ? saved : snapshot.value;
    receiptKind.value = payload.action === "review_result" ? "formal" : saved.status === "confirmed" ? "confirmed" : "pending";
    receiptNote.value = payload.action === "review_result" ? null : saved;
    receipt.value = payload.action === "review_result" ? saved : snapshot.value;
    pendingReview.value = null;
    clearReviewDraft(projectScope.value, props.taskId, props.checkId);
    try { sessionStorage.setItem("ael-actor", actor.value.trim() || payload.executor); } catch { /* The result is already recorded. */ }
    emit("saved");
    try {
      receipt.value = await api("/api/task?" + params({ id: props.taskId }));
      postReviewChanged.value = receipt.value.task?.candidate !== committedVersion.task?.candidate || receipt.value.definition_revision !== committedVersion.definition_revision || receipt.value.plan_revision !== committedVersion.plan_revision;
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
    if (e instanceof ApiError && ["REVIEW_VERSION_MISMATCH", "CANDIDATE_CHANGED", "REVIEW_NOT_ALLOWED", "REVIEW_VERSION_REQUIRED", "REVIEW_EVIDENCE_CHANGED", "REVIEW_INPUT_REQUIRED", "HUMAN_EVIDENCE_REQUIRED", "RECOVERY_REQUIRED", "SCREENSHOT_REQUIRED", "FORBIDDEN", "MANUAL_CHECK_REQUIRED", "CHECK_METHOD_MISMATCH", "CHECK_MISSING", "ARTIFACT_MISSING", "UNSAFE_PATH", "SYMLINK_PATH", "INVALID_RESULT", "REVIEW_NOTE_MISSING", "REVIEW_NOTE_VERSION_MISMATCH", "REVIEW_NOTE_ALREADY_CONFIRMED", "REVIEW_NOTE_CHECK_MISMATCH", "REVIEW_CHECK_CONFIRM_REQUIRED", "TERMINAL_TASK"].includes(e.code)) pendingReview.value = null;
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
        <div><h2 id="manual-dialog-title">{{ receipt ? "验收结果已记录" : snapshot && provisional ? "记录人工意见" : "记录人工验收" }}</h2><p v-if="snapshot">{{ snapshot.task.definition.id }} · {{ snapshot.task.definition.title }}</p></div>
        <button type="button" class="dialog-close" :disabled="busy" aria-label="关闭" @click="close">×</button>
      </header>
      <div class="manual-body">
      <p v-if="loading" role="status">正在读取当前候选和检查…</p>
      <template v-else-if="snapshot && !receipt">
        <div class="candidate-bar"><AppIcon name="lock"/><span>{{ pendingReview ? '待确认提交的候选' : '当前候选' }} <strong :title="pendingReview?.candidate || snapshot.task.candidate">{{ (pendingReview?.candidate || snapshot.task.candidate)?.slice(0, 12) || "尚未生成" }}</strong> · 定义修订 {{ pendingReview?.revision || snapshot.definition_revision }} · 计划修订 {{ pendingReview?.plan_revision || snapshot.plan_revision }}</span><small>{{ selectingCheck ? '请选择具体检查' : checkId ? '本次操作：检查 ' + checkId : '本次操作：任务级人工意见' }}</small></div>
        <p v-if="candidateState === 'checking' && !pendingReview" class="alert" role="status">{{ remoteProject ? '正在核对中央候选与客户端后续观察…' : '正在核对工作区是否仍与验收候选一致…' }}</p>
        <div v-if="candidateState === 'changed' && !pendingReview" class="alert" role="alert">{{ remoteProject ? '中央候选与后续上报不一致，请由执行者核对实施结果并提交当前候选，再进行人工验收；你的输入会保留。' : '工作区在生成候选后发生了变化，当前候选及其检查结果需要重新核对。请由执行者重新提交实施结果并生成候选，再进行人工验收；你的输入会保留。' }}</div>
        <div v-if="candidateState === 'error' && !pendingReview" class="alert" role="alert">无法核对当前候选，请重新读取后再验收。<button type="button" @click="reloadVersion">重新读取</button></div>
        <section v-if="selectingCheck && !candidateUnavailable" class="review-criteria"><h3>选择要验收的检查</h3><p>每次记录一项检查，其他检查保留原状。</p><button v-for="c in humanChecks" :key="c.id" type="button" @click="emit('selectCheck', c.id)">{{ c.id }} · {{ label(c.kind) }} · {{ label(snapshot.current_checks?.[c.id]?.result || 'not_run') }}</button></section>
        <div v-if="pausedReview && !candidateUnavailable && !pendingReview" class="alert" role="status"><p>{{ blockedHere ? '此检查因等待处理而受阻。恢复后才能提交针对当前候选的正式验收；恢复本身不会通过检查。' : blockedHumanCheck ? '任务仍因其他人工检查受阻，请先处理该检查。' : '任务当前处于暂停，请先在任务详情核对并处理阻塞原因。' }}</p><button v-if="blockedHere" type="button" :disabled="busy" @click="resumeReview">恢复并继续人工验收</button><button v-else-if="blockedHumanCheck" type="button" @click="emit('selectCheck', blockedHumanCheck)">查看受阻检查 {{ blockedHumanCheck }}</button></div>
        <p v-if="draftVersionChanged && !pendingReview" class="alert" role="status">候选或修订已变化，已保留草稿；请重新检查当前版本并再次确认。</p>
        <p v-if="storageWarning" class="alert" role="status">浏览器无法保存验收草稿，关闭页面后输入可能丢失。</p>
        <section v-if="priorWork" class="review-note"><strong>发现本标签页上次未提交的实施表单</strong><p>这些文字没有被自动提交或视为验收结论。</p><button v-if="priorWork.summary" type="button" @click="importPriorWork">带入上次填写的工作说明</button><details><summary>查看旧填写</summary><p v-if="priorWork.summary">工作说明：{{ priorWork.summary }}</p><p v-if="priorWork.artifacts">产物引用：{{ priorWork.artifacts }}</p><p v-if="priorWork.knowledge_reason">规范处理依据：{{ priorWork.knowledge_reason }}</p><p v-if="priorWork.knowledge_artifacts">规范产物：{{ priorWork.knowledge_artifacts }}</p><p v-if="priorWork.return_to">期望退回：{{ priorWork.return_to }}</p></details><button type="button" @click="dismissPriorWork">移除旧草稿</button></section>
        <p v-if="provisional && !pendingReview && canRecord && !pausedReview && !selectingCheck && !candidateUnavailable" class="review-pending-explanation">{{ !composing && reviewNotes.length ? (currentPendingNotes ? (!snapshot.task.candidate ? '人工意见已保存，等待生成候选后重新确认，无需重复提交。' : '人工意见已保存，请核对当前候选后确认或引用。') : pendingNotes && !currentConfirmedNotes ? '先前意见的定义或计划已变化，请针对当前要求重新记录。' : '已保存的人工意见如下；需要补充时可记录新的意见。') : !snapshot.task.candidate ? '尚未生成验收候选；本次先记录人工意见，候选生成后需要你重新确认。' : checkId ? '此检查当前尚不能正式验收，本次只记录人工意见。' : '本次记录针对当前候选的任务级人工意见，不替代其他检查。' }}</p>
        <div v-if="!canRecord && !pendingReview" class="alert" role="alert">任务已结束，或此检查不支持人工验收；原有检查方式不会被修改。</div>
        <section v-if="reviewNotes.length && !pendingReview" class="review-note-list"><h3>已记录的人工意见 <span class="count">{{ reviewNotes.length }}</span></h3><article v-for="note in visibleNotes" :key="note.id" class="review-note"><strong>{{ label(note.result) }} · {{ note.actor }}</strong><small>{{ note.check ? '检查 ' + note.check : '任务级' }} · {{ stamp(note.recorded_at) }} · {{ note.status === 'pending' ? (!noteIsCurrent(note) ? '已过期' : '待候选确认') : note.status === 'confirmed' ? '已确认候选' : '历史记录' }}</small><p>{{ note.reason }}</p><button v-if="canRecord && !candidateUnavailable && note.status === 'pending' && noteAppliesToCheck(note) && access?.can_review && noteIsCurrent(note)" type="button" @click="useNote(note)">引用这条意见</button><button v-if="canRecord && !candidateUnavailable && note.status === 'pending' && !checkId && !note.check && snapshot.task.candidate && noteIsCurrent(note)" type="button" :disabled="busy" @click="confirmNote(note)">确认这条意见适用于当前候选</button><small v-if="note.status === 'pending' && (!noteIsCurrent(note))">候选、定义或计划已变化，请重新记录意见。</small></article><button v-if="reviewNotes.length > 3 && !showAllNotes" type="button" @click="showAllNotes = true">查看全部 {{ reviewNotes.length }} 条记录</button></section>
        <button v-if="canRecord && !composing && !pendingReview && !pausedReview && !selectingCheck && !candidateUnavailable" type="button" @click="composing = true">记录新的验收意见</button>
        <fieldset v-if="(canRecord && composing && !pausedReview && !selectingCheck && !candidateUnavailable) || pendingReview" class="review-fields" :disabled="!!pendingReview">
          <section class="review-criteria"><h3>{{ checkId ? checkId + ' · ' + label(check?.kind) : '任务验收条件' }}</h3><ul v-if="(checkId ? check?.covers : snapshot.task.definition.acceptance)?.length"><li v-for="item in (checkId ? check.covers : snapshot.task.definition.acceptance)" :key="item">{{ item }}</li></ul><p v-else>未记录具体标准，请先核实任务定义。</p></section>
          <fieldset class="review-outcomes"><legend>验收结论 <span aria-hidden="true">*</span></legend><div>
            <label v-for="[value, text] in [['pass', '通过'], ['fail', '不通过'], ['blocked', '暂无法验证']]" :key="value" :class="{ selected: outcome === value }"><input v-model="outcome" type="radio" name="review-outcome" :value="value" /><AppIcon :name="value === 'pass' ? 'check' : value === 'fail' ? 'warning' : 'clock'" />{{ text }}</label>
          </div></fieldset>
          <label><span>实际观察与理由 <b aria-hidden="true">*</b></span><textarea v-model="reason" rows="3" placeholder="描述实际检查的内容与观察结果"></textarea></label>
          <label v-if="outcome && outcome !== 'pass'"><span>{{ outcome === 'fail' ? '修复方向' : '继续验证的条件' }} <b v-if="!provisional" aria-hidden="true">*</b><small v-else>（可选）</small></span><textarea v-model="recovery" rows="2" placeholder="说明下一步需要修复或恢复什么"></textarea></label>
          <details v-if="provisional && !availableRefs.length" class="optional-review-evidence" :open="evidenceOpen" @toggle="evidenceOpen = ($event.target as HTMLDetailsElement).open"><summary>关联证据（可选）</summary><label>补充已有证据引用<input v-model="extraRefs" :placeholder="remoteProject ? '中央附件 ID，多个以逗号分隔' : '项目相对路径或 runtime: 引用，多个以逗号分隔'" /></label><small>{{ remoteProject ? '填写中央附件 ID 后，服务端核验原件与候选关联。' : '如填写引用，系统会核验文件真实存在。' }}</small></details>
          <fieldset v-else class="evidence-field"><legend>关联真实证据 <span v-if="provisional">（可选）</span></legend><div class="evidence-picker"><p v-if="!availableRefs.length">{{ remoteProject ? '当前候选尚无可用附件，请由外部客户端上传证据后重新读取。' : '当前候选没有已记录的产物引用，可填写已有项目相对路径或 runtime: 引用。' }}</p><label v-for="ref in availableRefs" :key="ref"><input v-model="selected" type="checkbox" :aria-label="ref" :value="ref" /><AppIcon name="file"/><span><strong>{{ evidenceName(ref) }}</strong><small v-if="evidenceName(ref) !== ref && evidenceByID.get(ref)?.source">{{ evidenceByID.get(ref).source }}</small></span></label><label v-if="!remoteProject && !provisional && check?.kind === 'ui'">添加验收截图（PNG/JPEG，最大 10 MB）<input type="file" accept="image/png,image/jpeg" :disabled="uploading || busy" @change="addImage" /></label><p v-if="uploading" role="status">正在保存截图…</p><label class="extra-evidence">补充已有证据引用<input v-model="extraRefs" :placeholder="remoteProject ? '中央附件 ID，多个以逗号分隔' : '项目相对路径或 runtime: 引用，多个以逗号分隔'" /></label></div></fieldset>
          <p v-if="!provisional" class="secondary">通过和不通过需要真实证据；视觉检查还需要真实 PNG/JPEG 截图。暂无法验证可不附证据。</p>
          <details v-if="remoteProject && snapshot.task.candidate" class="optional-review-evidence" :open="comparisonOpen" @toggle="comparisonOpen = ($event.target as HTMLDetailsElement).open">
            <summary>对照基准与实际截图（可选）</summary>
            <p class="secondary">可直接在上面选一张实际截图作为证据。这里用于对照两张图片。</p>
            <EvidenceComparison v-if="comparisonOpen" v-model="comparison" :task-id="taskId" :candidate="snapshot.task.candidate" @changed="confirmed = false" />
          </details>
          <div class="review-meta-grid"><label><span>记录人 <b aria-hidden="true">*</b></span><input v-model="actor" autocomplete="name" :readonly="!!remoteProject" /></label><div><span>提交后的行为</span><p>{{ provisional ? (snapshot.task.candidate && !checkId ? '记录任务级意见并绑定当前候选，其他检查保持原样。' : '记录人工意见，候选生成后需要重新确认，不会自动通过检查。') : outcome === 'pass' ? '记录此项通过后重新核对其余门禁与交付目标。' : outcome === 'fail' ? '记录未通过，并由任务流程确定修复与下一步。' : outcome === 'blocked' ? '记录无法验证的原因和继续验证条件，不会视为通过。' : '请选择验收结论，查看对应的后续行为。' }}</p></div></div>
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
        <p v-if="postReviewChanged" role="alert" class="alert">当前候选或修订在本次记录后发生变化，请重新审阅当前版本。</p>
        <p v-if="receiptError" class="secondary">结果已记录，但刷新任务状态失败。</p>
      </section>
      <p v-if="inputError" role="alert" class="alert">{{ inputError }}</p>
      <div v-if="error" role="alert" class="alert"><p>{{ readableError(error) }}</p><details><summary>具体原因</summary><pre>{{ error }}</pre></details><button v-if="snapshot && !receipt && /REVIEW_VERSION|CANDIDATE_CHANGED|REVIEW_NOT_ALLOWED/.test(error)" type="button" @click="reloadVersion">重新读取版本并保留输入</button></div>
      </div>
      <div class="actions dialog-actions"><small v-if="!receipt">候选或修订变化后，需要重新核对。</small><button v-if="!receipt" type="button" :disabled="busy || uploading" @click="close">{{ composing || pendingReview ? '取消' : '返回' }}</button><button v-if="!receipt && !loading && ((canRecord && composing && !pausedReview && !selectingCheck && !candidateUnavailable) || pendingReview)" type="submit" class="primary" :disabled="busy || uploading">{{ busy ? '正在提交…' : pendingReview ? '原样重试提交' : provisional ? '保存人工意见' : '提交验收结果' }}</button><button v-if="receipt" type="button" @click="goTo('/tasks')">回工作台</button><button v-if="receipt" type="button" class="primary" @click="goTo('/tasks/' + encodeURIComponent(taskId) + '?section=checks')">查看任务结果</button><button v-if="error && !snapshot" type="button" @click="load">重新读取</button></div>
    </form>
  </dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, ApiError, params } from "../api";
import { goalLabel, label, nextAction, readableError } from "../display";
import { navigate } from "../router";
import AppIcon from "./AppIcon.vue";
import { readReviewDraft, writeReviewDraft, clearReviewDraft } from "../reviewDraft";

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
const pendingReview = ref<any>(null);
const requestKey = ref(globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`);
const draftReady = ref(false), draftVersionChanged = ref(false), storageWarning = ref(false), reviewedCandidate = ref("");
const projectScope = ref("");
const check = computed(() => snapshot.value?.task.definition.checks.find((c: any) => c.id === props.checkId));
const access = computed(() => snapshot.value?.check_access?.[props.checkId]);
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
    actor: actor.value, selected: selected.value, pending: pendingReview.value, requestKey: requestKey.value,
  });
}
function restoreDraft() {
  const saved = readReviewDraft(projectScope.value, props.taskId, props.checkId);
  if (saved && snapshot.value) {
    outcome.value = typeof saved.outcome === "string" ? saved.outcome : "";
    reason.value = typeof saved.reason === "string" ? saved.reason : "";
    recovery.value = typeof saved.recovery === "string" ? saved.recovery : "";
    extraRefs.value = typeof saved.extraRefs === "string" ? saved.extraRefs : "";
    actor.value = typeof saved.actor === "string" ? saved.actor : actor.value;
    selected.value = Array.isArray(saved.selected) ? saved.selected.filter((v: unknown) => typeof v === "string") : [];
    if (saved.pending?.action === "review_result" && saved.pending.id === props.taskId && saved.pending.unit === props.checkId && saved.pending.request_key) {
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
watch([outcome, reason, recovery, extraRefs, actor, selected, pendingReview], saveDraft, { deep: true, flush: "sync" });
async function load() {
  loading.value = true;
  error.value = "";
  try {
    snapshot.value = await api("/api/task?" + params({ id: props.taskId }));
    try { projectScope.value = (await api("/api/info")).project || ""; } catch { projectScope.value = ""; }
    if (!check.value) error.value = "CHECK_MISSING: 这项检查已不在当前定义中。";
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
async function submit() {
  if (busy.value || !snapshot.value || (!access.value?.can_review && !pendingReview.value)) return;
  error.value = "";
  const refs = [...new Set([...selected.value, ...extraRefs.value.split(",").map((x) => x.trim()).filter(Boolean)])];
  if (!pendingReview.value && (!outcome.value || !actor.value.trim() || !reason.value.trim() || !confirmed.value)) {
    error.value = "REVIEW_INPUT_REQUIRED: 请明确选择结论，填写观察和记录人，并确认审阅的候选。";
    return;
  }
  if (!pendingReview.value && outcome.value !== "pass" && !recovery.value.trim()) {
    error.value = "RECOVERY_REQUIRED: 请填写修复方向或继续验证的条件。";
    return;
  }
  if (!pendingReview.value && outcome.value !== "blocked" && !refs.length) {
    error.value = "HUMAN_EVIDENCE_REQUIRED: 通过或不通过都需要关联真实证据。";
    return;
  }
  busy.value = true;
  try {
    const payload = pendingReview.value || {
      action: "review_result", id: props.taskId, unit: props.checkId,
      executor: actor.value.trim(), reason: reason.value.trim(), recovery: recovery.value.trim(),
      result: outcome.value, artifacts: refs, request_key: requestKey.value,
      candidate: snapshot.value.task.candidate,
      revision: snapshot.value.definition_revision, plan_revision: snapshot.value.plan_revision,
    };
    pendingReview.value = payload;
    reviewedCandidate.value = payload.candidate;
    const saved = await api("/api/action", payload);
    receipt.value = saved;
    pendingReview.value = null;
    clearReviewDraft(projectScope.value, props.taskId, props.checkId);
    sessionStorage.setItem("ael-actor", actor.value.trim());
    emit("saved");
    try {
      receipt.value = await api("/api/task?" + params({ id: props.taskId }));
    } catch (e) {
      receiptError.value = String(e);
    }
  } catch (e) {
    if (e instanceof ApiError && ["REVIEW_VERSION_MISMATCH", "CANDIDATE_CHANGED", "REVIEW_NOT_ALLOWED", "REVIEW_VERSION_REQUIRED", "REVIEW_EVIDENCE_CHANGED", "HUMAN_EVIDENCE_REQUIRED", "RECOVERY_REQUIRED", "SCREENSHOT_REQUIRED", "MANUAL_CHECK_REQUIRED", "CHECK_METHOD_MISMATCH", "ARTIFACT_MISSING", "UNSAFE_PATH", "SYMLINK_PATH", "INVALID_RESULT"].includes(e.code)) pendingReview.value = null;
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
        <div><h2 id="manual-dialog-title">{{ receipt ? "验收结果已记录" : "记录人工验收" }}</h2><p v-if="snapshot">{{ snapshot.task.definition.title }} · {{ checkId }}</p></div>
        <button type="button" class="dialog-close" :disabled="busy" aria-label="关闭" @click="close">×</button>
      </header>
      <div class="manual-body">
      <p v-if="loading" role="status">正在读取当前候选和检查…</p>
      <template v-else-if="snapshot && !receipt">
        <div class="candidate-bar"><AppIcon name="lock"/><span>{{ pendingReview ? '待确认提交的候选' : '当前候选' }} <strong :title="pendingReview?.candidate || snapshot.task.candidate">{{ (pendingReview?.candidate || snapshot.task.candidate)?.slice(0, 12) || "未记录" }}</strong> · 定义修订 {{ pendingReview?.revision || snapshot.definition_revision }} · 计划修订 {{ pendingReview?.plan_revision || snapshot.plan_revision }}</span><small>仅作用于此检查</small></div>
        <p v-if="draftVersionChanged && !pendingReview" class="alert" role="status">候选或修订已变化，已保留草稿；请重新检查当前版本并再次确认。</p>
        <p v-if="storageWarning" class="alert" role="status">浏览器无法保存验收草稿，关闭页面后输入可能丢失。</p>
        <div v-if="!access?.can_review && !pendingReview" class="alert" role="alert">当前条件不允许记录人工验收。请刷新任务并核对检查方法、执行状态和候选。</div>
        <fieldset v-else class="review-fields" :disabled="!!pendingReview">
          <section class="review-criteria"><h3>{{ checkId }} · {{ label(check?.kind) }}</h3><ul v-if="check?.covers?.length"><li v-for="item in check.covers" :key="item">{{ item }}</li></ul><p v-else>未记录具体标准，请先核实任务定义。</p></section>
          <fieldset class="review-outcomes"><legend>验收结论 <span aria-hidden="true">*</span></legend><div>
            <label v-for="[value, text] in [['pass', '通过'], ['fail', '不通过'], ['blocked', '暂无法验证']]" :key="value" :class="{ selected: outcome === value }"><input v-model="outcome" type="radio" name="review-outcome" :value="value" /><AppIcon :name="value === 'pass' ? 'check' : value === 'fail' ? 'warning' : 'clock'" />{{ text }}</label>
          </div></fieldset>
          <label><span>实际观察与理由 <b aria-hidden="true">*</b></span><textarea v-model="reason" rows="3" placeholder="描述实际检查的内容与观察结果"></textarea></label>
          <label v-if="outcome && outcome !== 'pass'"><span>{{ outcome === 'fail' ? '修复方向' : '继续验证的条件' }} <b aria-hidden="true">*</b></span><textarea v-model="recovery" rows="2" placeholder="说明下一步需要修复或恢复什么"></textarea></label>
          <fieldset class="evidence-field"><legend>关联真实证据</legend><div class="evidence-picker"><p v-if="!availableRefs.length">当前候选没有已记录的产物引用，可填写已有项目相对路径或 runtime: 引用。</p><label v-for="ref in availableRefs" :key="ref"><input v-model="selected" type="checkbox" :aria-label="ref" :value="ref" /><AppIcon name="file"/><span><strong>{{ ref.split('/').pop() }}</strong><small>{{ ref }}</small></span></label><label class="extra-evidence">补充已有证据引用<input v-model="extraRefs" placeholder="项目相对路径或 runtime: 引用，多个以逗号分隔" /></label></div></fieldset>
          <p class="secondary">通过和不通过需要真实证据；视觉检查还需要真实 PNG/JPEG 截图。暂无法验证可不附证据。</p>
          <div class="review-meta-grid"><label><span>记录人 <b aria-hidden="true">*</b></span><input v-model="actor" autocomplete="name" /></label><div><span>提交后的行为</span><p>{{ outcome === 'pass' ? '记录此项通过后重新核对其余门禁与交付目标。' : outcome === 'fail' ? '记录未通过，并由任务流程确定修复与下一步。' : outcome === 'blocked' ? '记录无法验证的原因和继续验证条件，不会视为通过。' : '请选择验收结论，查看对应的后续行为。' }}</p></div></div>
          <label class="inline"><input v-model="confirmed" type="checkbox" />我确认以上结论针对所示候选和修订。</label>
        </fieldset>
        <p v-if="pendingReview" role="status" class="alert">上次提交结果尚未确认；请原样重试，以免重复记录验收。</p>
      </template>
      <section v-if="receipt" class="review-receipt" role="status"><span class="receipt-icon"><AppIcon :name="receipt.task?.status === 'completed' ? 'check' : 'clock'" /></span>
        <h3>{{ receipt.task?.status === 'completed' ? '任务已完成' : receipt.current_step === 'delivery' ? '等待实际交付' : '任务仍有后续工作' }}</h3>
        <p>{{ receipt.task?.status === 'completed' ? '已记录本次验收并达到任务交付目标。' : '本次验收已记录，后续状态由实际检查与交付条件决定。' }}</p>
        <dl class="receipt-facts"><div><dt>本次检查</dt><dd>{{ checkId }} · {{ label(outcome) }}</dd></div><div><dt>必要检查</dt><dd>{{ receipt.summary?.checks ? receipt.summary.checks.required_passed + ' / ' + receipt.summary.checks.required + ' 项通过' : '状态待读取' }}</dd></div><div><dt>验收候选</dt><dd :title="reviewedCandidate">{{ reviewedCandidate?.slice(0, 12) || '未记录' }}</dd></div><div><dt>交付目标</dt><dd>{{ goalLabel(snapshot.task.definition.goal) }}</dd></div></dl>
        <p>{{ receipt.task?.status === 'completed' ? '实际结果：' + label(receipt.task.result) : '当前节点：' + label(receipt.current_step) }}</p>
        <p v-if="receipt.summary">下一步：{{ nextAction(receipt.summary) }}<span v-if="receipt.summary.waiting_checks">；仍有 {{ receipt.summary.waiting_checks }} 项检查待处理</span>。</p>
        <p v-else>状态读取尚未完成，请刷新任务查看最新结果。</p>
        <p v-if="receiptError" class="secondary">结果已记录，但刷新任务状态失败。</p>
      </section>
      <div v-if="error" role="alert" class="alert"><p>{{ readableError(error) }}</p><details><summary>具体原因</summary><pre>{{ error }}</pre></details><button v-if="snapshot && !receipt && /REVIEW_VERSION|CANDIDATE_CHANGED|REVIEW_NOT_ALLOWED/.test(error)" type="button" @click="reloadVersion">重新读取版本并保留输入</button></div>
      </div>
      <div class="actions dialog-actions"><small v-if="!receipt">候选或修订变化后，需要重新核对。</small><button v-if="!receipt" type="button" :disabled="busy" @click="close">取消</button><button v-if="!receipt && !loading && (access?.can_review || pendingReview)" type="submit" class="primary" :disabled="busy">{{ busy ? '正在提交…' : pendingReview ? '原样重试提交' : '提交验收结果' }}</button><button v-if="receipt" type="button" @click="goTo('/tasks')">回工作台</button><button v-if="receipt" type="button" class="primary" @click="goTo('/tasks/' + encodeURIComponent(taskId) + '?section=checks')">查看任务结果</button><button v-if="error && !snapshot" type="button" @click="load">重新读取</button></div>
    </form>
  </dialog>
</template>

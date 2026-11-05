<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { api, params } from "../api";
import { goalLabel, label, nextAction, readableError } from "../display";

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
const requestKey = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`;
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
  return [...refs];
});
async function load() {
  loading.value = true;
  error.value = "";
  try {
    snapshot.value = await api("/api/task?" + params({ id: props.taskId }));
    if (!check.value) error.value = "CHECK_MISSING: 这项检查已不在当前定义中。";
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
  }
}
async function reloadVersion() {
  confirmed.value = false;
  await load();
}
onMounted(() => {
  dialog.value?.showModal();
  load();
});
onUnmounted(() => previous?.focus({ preventScroll: true }));
function close() {
  if (!busy.value) emit("close");
}
async function submit() {
  if (busy.value || !snapshot.value || !access.value?.can_review) return;
  error.value = "";
  const refs = [...new Set([...selected.value, ...extraRefs.value.split(",").map((x) => x.trim()).filter(Boolean)])];
  if (!outcome.value || !actor.value.trim() || !reason.value.trim() || !confirmed.value) {
    error.value = "REVIEW_INPUT_REQUIRED: 请明确选择结论，填写观察和记录人，并确认审阅的候选。";
    return;
  }
  if (outcome.value !== "pass" && !recovery.value.trim()) {
    error.value = "RECOVERY_REQUIRED: 请填写修复方向或继续验证的条件。";
    return;
  }
  if (outcome.value !== "blocked" && !refs.length) {
    error.value = "HUMAN_EVIDENCE_REQUIRED: 通过或不通过都需要关联真实证据。";
    return;
  }
  busy.value = true;
  try {
    const saved = await api("/api/action", {
      action: "review_result", id: props.taskId, unit: props.checkId,
      executor: actor.value.trim(), reason: reason.value.trim(), recovery: recovery.value.trim(),
      result: outcome.value, artifacts: refs, request_key: requestKey,
      candidate: snapshot.value.task.candidate,
      revision: snapshot.value.definition_revision, plan_revision: snapshot.value.plan_revision,
    });
    sessionStorage.setItem("ael-actor", actor.value.trim());
    emit("saved");
    receipt.value = saved;
    try {
      receipt.value = await api("/api/task?" + params({ id: props.taskId }));
    } catch (e) {
      receiptError.value = String(e);
    }
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <dialog ref="dialog" class="action-dialog manual-dialog" @cancel.prevent="close">
    <form @submit.prevent="submit">
      <header class="manual-header">
        <div><h2>{{ receipt ? "验收结果已记录" : "记录人工验收" }}</h2><p v-if="snapshot">{{ snapshot.task.definition.title }} · {{ checkId }}</p></div>
        <button type="button" class="dialog-close" :disabled="busy" aria-label="关闭" @click="close">×</button>
      </header>
      <p v-if="loading" role="status">正在读取当前候选和检查…</p>
      <template v-else-if="snapshot && !receipt">
        <div class="candidate-bar">当前候选 <strong>{{ snapshot.task.candidate || "未记录" }}</strong> · 定义修订 {{ snapshot.definition_revision }} · 计划修订 {{ snapshot.plan_revision }}</div>
        <div v-if="!access?.can_review" class="alert" role="alert">当前条件不允许记录人工验收。请刷新任务并核对检查方法、执行状态和候选。</div>
        <template v-else>
          <section class="review-criteria"><h3>检查标准</h3><ul v-if="check?.covers?.length"><li v-for="item in check.covers" :key="item">{{ item }}</li></ul><p v-else>未记录具体标准，请先核实任务定义。</p></section>
          <fieldset class="review-outcomes"><legend>验收结论 <span aria-hidden="true">*</span></legend><div>
            <label v-for="[value, text] in [['pass', '通过'], ['fail', '不通过'], ['blocked', '暂无法验证']]" :key="value" :class="{ selected: outcome === value }"><input v-model="outcome" type="radio" name="review-outcome" :value="value" />{{ text }}</label>
          </div></fieldset>
          <label><span>实际观察与理由 <b aria-hidden="true">*</b></span><textarea v-model="reason" rows="3" placeholder="描述实际检查的内容与观察结果"></textarea></label>
          <label v-if="outcome && outcome !== 'pass'"><span>{{ outcome === 'fail' ? '修复方向' : '继续验证的条件' }} <b aria-hidden="true">*</b></span><textarea v-model="recovery" rows="2" placeholder="说明下一步需要修复或恢复什么"></textarea></label>
          <fieldset class="evidence-picker"><legend>关联真实证据</legend><p v-if="!availableRefs.length">当前候选没有已记录的产物引用，可填写已有项目相对路径或 runtime: 引用。</p><label v-for="ref in availableRefs" :key="ref"><input v-model="selected" type="checkbox" :value="ref" />{{ ref }}</label></fieldset>
          <label>补充已有证据引用<input v-model="extraRefs" placeholder="项目相对路径或 runtime: 引用，多个以逗号分隔" /></label>
          <p class="secondary">通过和不通过需要真实证据；视觉检查还需要真实 PNG/JPEG 截图。暂无法验证可不附证据。</p>
          <label><span>记录人 <b aria-hidden="true">*</b></span><input v-model="actor" autocomplete="name" /></label>
          <label class="inline"><input v-model="confirmed" type="checkbox" />我确认以上结论针对所示候选和修订。</label>
        </template>
      </template>
      <section v-if="receipt" class="review-receipt" role="status">
        <h3>{{ receipt.task?.status === 'completed' ? '任务已完成' : receipt.current_step === 'delivery' ? '等待实际交付' : '任务仍有后续工作' }}</h3>
        <p>本次检查：{{ checkId }} · {{ label(outcome) }}；候选：{{ snapshot.task.candidate }}</p>
        <p>交付目标：{{ goalLabel(snapshot.task.definition.goal) }}。{{ receipt.task?.status === 'completed' ? '实际结果：' + label(receipt.task.result) : '当前节点：' + label(receipt.current_step) }}</p>
        <p v-if="receipt.summary">下一步：{{ nextAction(receipt.summary) }}<span v-if="receipt.summary.waiting_checks">；仍有 {{ receipt.summary.waiting_checks }} 项检查待处理</span>。</p>
        <p v-else>状态读取尚未完成，请刷新任务查看最新结果。</p>
        <p v-if="receiptError" class="secondary">结果已记录，但刷新任务状态失败。</p>
      </section>
      <div v-if="error" role="alert" class="alert"><p>{{ readableError(error) }}</p><details><summary>具体原因</summary><pre>{{ error }}</pre></details><button v-if="snapshot && !receipt && /REVIEW_VERSION|CANDIDATE_CHANGED|REVIEW_NOT_ALLOWED/.test(error)" type="button" @click="reloadVersion">重新读取版本并保留输入</button></div>
      <div class="actions dialog-actions"><button type="button" :disabled="busy" @click="close">{{ receipt ? '关闭回执' : '取消' }}</button><button v-if="!receipt && !loading && access?.can_review" type="submit" class="primary" :disabled="busy">{{ busy ? '正在提交…' : '提交验收结果' }}</button><button v-if="error && !snapshot" type="button" @click="load">重新读取</button></div>
    </form>
  </dialog>
</template>

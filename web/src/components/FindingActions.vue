<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { api, ApiError, params, remoteProject } from "../api";
import { taskHref } from "../router";
import { stamp } from "../display";

const props = defineProps<{ finding: any }>();
const emit = defineEmits<{ changed: [] }>();
const kind = ref(""), mode = ref(""), action = ref("");
const reason = ref(""), repairID = ref(""), repairCheck = ref(""), appearance = ref("");
const snapshot = ref<any>(null), linkedTask = ref<any>(null), pending = ref<any>(null), receipt = ref<any>(null);
const confirmed = ref(false), busy = ref(false), error = ref(""), scope = ref("");
const identityReady = ref(false), storageWarning = ref(false);
function operationKey() { return globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`; }
const key = ref(operationKey());
const mayAct = computed(() => !!remoteProject.value && ["admin", "executor", "reviewer"].includes(remoteProject.value.role));
const mayRepair = computed(() => !!remoteProject.value && ["admin", "executor"].includes(remoteProject.value.role));
const mayJudge = computed(() => !!remoteProject.value && ["admin", "reviewer"].includes(remoteProject.value.role));
const statusNames: Record<string,string> = { open: "待处理", needs_evidence: "待补证", dismissed: "非问题", accepted_tradeoff: "已记录权衡", resolved: "已复验通过" };
const decisionNames: Record<string,string> = { fix: "需要修复", non_issue: "非问题", accepted_tradeoff: "接受权衡", more_evidence: "补充证据" };
const repairNames: Record<string,string> = { repair_pending: "待实施修复", awaiting_retest: "待有效复验", verified: "已记录有效复验", candidate_changed: "候选需要重新核对", unavailable: "关联任务不可用" };
const versionChanged = computed(() => !!snapshot.value && (snapshot.value.revision !== props.finding.revision || snapshot.value.source_task_revision !== props.finding.source_task_revision || snapshot.value.repair_task?.revision !== props.finding.repair_task?.revision));
const storageKey = () => "ael-finding-action-v1:" + encodeURIComponent(scope.value) + ":" + props.finding.id;

function freeze(value: any) {
  return JSON.parse(JSON.stringify({ id: value.id, title: value.title, revision: value.revision, source_task_revision: value.source_task_revision, candidate_id: value.candidate_id, definition_revision: value.definition_revision, plan_revision: value.plan_revision, repair: value.repair, repair_task: value.repair_task, repair_evidence_sha256: value.repair_evidence_sha256, repair_can_revalidate: value.repair_can_revalidate }));
}
function saveDraft() {
  if (!identityReady.value || !scope.value || receipt.value) return;
  try {
    if (!kind.value) { sessionStorage.removeItem(storageKey()); return; }
    sessionStorage.setItem(storageKey(), JSON.stringify({ kind: kind.value, mode: mode.value, action: action.value, reason: reason.value, repairID: repairID.value, repairCheck: repairCheck.value, appearance: appearance.value, snapshot: snapshot.value, linkedTask: linkedTask.value, pending: pending.value, key: key.value }));
    storageWarning.value = false;
  } catch { storageWarning.value = true; }
}
async function loadIdentity() {
  try {
    const info = await api("/api/info");
    if (!info.project) throw new Error("项目身份尚未确认，请重新读取。");
    scope.value = info.project;
    try {
    const stored = sessionStorage.getItem(storageKey());
    if (stored) {
      const draft = JSON.parse(stored);
      if (["decisions", "repair", "revalidations"].includes(draft.kind) && draft.snapshot?.id === props.finding.id) {
        kind.value = draft.kind; mode.value = draft.mode || ""; action.value = draft.action || "";
        reason.value = draft.reason || ""; repairID.value = draft.repairID || ""; repairCheck.value = draft.repairCheck || "";
        appearance.value = draft.appearance || ""; snapshot.value = draft.snapshot; linkedTask.value = draft.linkedTask;
        key.value = draft.key || operationKey();
        if (draft.pending?.body?.finding_id === props.finding.id && draft.pending.body.idempotency_key && draft.pending.kind === draft.kind) pending.value = draft.pending;
      }
    }
    } catch { storageWarning.value = true; }
    identityReady.value = true;
  } catch (e) { error.value = String(e); }
}
function open(nextKind: string, nextMode = "") {
  if (busy.value || !identityReady.value || pending.value) return;
  kind.value = nextKind; mode.value = nextMode; snapshot.value = freeze(props.finding);
  action.value = ""; reason.value = ""; repairID.value = ""; repairCheck.value = props.finding.check_id;
  linkedTask.value = null; appearance.value = props.finding.occurrences?.[0]?.id || "";
  confirmed.value = false; key.value = operationKey(); receipt.value = null; error.value = "";
}
function close() {
  if (busy.value || pending.value) return;
  kind.value = ""; snapshot.value = null; saveDraft();
}
async function readRepair() {
  if (!repairID.value.trim() || busy.value) return;
  busy.value = true; error.value = ""; confirmed.value = false;
  try {
    linkedTask.value = await api("/api/task?" + params({ id: repairID.value.trim() }));
    if (!linkedTask.value.task.definition.checks.some((check: any) => check.id === repairCheck.value)) repairCheck.value = "";
  } catch (e) { linkedTask.value = null; error.value = String(e); }
  finally { busy.value = false; }
}
async function refreshVersion() {
  if (busy.value || pending.value) return;
  busy.value = true; confirmed.value = false; error.value = "";
  try {
    const current = await api("/api/finding?" + params({ id: props.finding.id }));
    if (current.task_id !== props.finding.task_id) throw new Error("发现所属任务已变化，请重新打开。");
    snapshot.value = freeze(current); key.value = operationKey();
    emit("changed");
  } catch (e) { error.value = String(e); }
  finally { busy.value = false; }
}
async function submit() {
  if (busy.value || !identityReady.value || !snapshot.value) return;
  if (!mayAct.value || (kind.value === "repair" && !mayRepair.value) || (kind.value === "decisions" && ["non_issue", "accepted_tradeoff"].includes(action.value) && !mayJudge.value)) { error.value = "当前项目权限不足，输入已保留。"; return; }
  if (!pending.value && (!confirmed.value || !reason.value.trim() || (kind.value === "decisions" && !action.value))) {
    error.value = "请明确选择裁定，填写理由并确认审阅的版本。"; return;
  }
  if (!pending.value && kind.value === "repair" && mode.value === "link" && (!linkedTask.value || linkedTask.value.task.definition.id !== repairID.value.trim() || !repairCheck.value)) {
    error.value = "请先读取关联任务并选择对应检查。"; return;
  }
  if (!pending.value && kind.value === "revalidations" && !snapshot.value.repair_can_revalidate) {
    error.value = "需要关联修复任务在新候选上提供当前有效的通过证据。"; return;
  }
  const body: Record<string,any> = {
    finding_id: snapshot.value.id, expected_revision: snapshot.value.revision,
    expected_task_revision: snapshot.value.source_task_revision, reason: reason.value.trim(),
    operation_id: key.value, idempotency_key: key.value,
  };
  if (kind.value === "decisions") body.action = action.value;
  if (kind.value === "repair") {
    body.occurrence_id = appearance.value;
    if (mode.value === "link") {
      body.repair_task_id = repairID.value.trim(); body.repair_check_id = repairCheck.value;
      body.expected_repair_revision = linkedTask.value.revision;
    }
  }
  if (kind.value === "revalidations") {
    body.repair_task_id = snapshot.value.repair.task_id; body.expected_repair_revision = snapshot.value.repair_task.revision;
    body.candidate_id = snapshot.value.repair_task.candidate_id; body.evidence_sha256 = snapshot.value.repair_evidence_sha256;
  }
  const request = pending.value || { kind: kind.value, body };
  busy.value = true; error.value = ""; pending.value = request;
  try {
    receipt.value = await api("/api/finding-action?" + params({ kind: request.kind }), request.body);
    pending.value = null;
    try { sessionStorage.removeItem(storageKey()); } catch { storageWarning.value = true; }
    emit("changed");
  } catch (e) {
    const rejected = ["FINDING_MUTATION_INVALID", "FINDING_DECISION_INVALID", "FINDING_REVISION_CONFLICT", "REVISION_CONFLICT", "FINDING_REPAIR_EXISTS", "FINDING_REPAIR_INVALID", "FINDING_REPAIR_SCOPE_MISMATCH", "FINDING_REPAIR_CHECK_MISMATCH", "FINDING_REPAIR_REQUIRED", "FINDING_NEW_CANDIDATE_REQUIRED", "FINDING_RETEST_REQUIRED", "FINDING_SOURCE_UNAVAILABLE", "FINDING_EVIDENCE_INVALID", "FORBIDDEN", "CANDIDATE_CHANGED", "POLICY_CHANGED", "PLAN_APPLY_IN_PROGRESS", "IDEMPOTENCY_CONFLICT"];
    if (e instanceof ApiError && rejected.includes(e.code)) { pending.value = null; confirmed.value = false; key.value = operationKey(); }
    error.value = String(e);
  } finally { busy.value = false; }
}
watch([kind, mode, action, reason, repairID, repairCheck, appearance, snapshot, linkedTask, pending, key], saveDraft, { deep: true, flush: "sync" });
onMounted(() => void loadIdentity());
onUnmounted(saveDraft);
</script>

<template>
  <section class="content-section finding-actions" aria-label="发现裁定与复验">
    <h3>裁定与修复</h3>
    <p>当前状态 {{ statusNames[finding.status] || finding.status }}</p>
    <p v-if="finding.decision">{{ decisionNames[finding.decision.action] }} · {{ finding.decision.actor }} · {{ finding.decision.authority === 'human_session' ? '授权人工会话' : '客户端报告' }}<br />{{ finding.decision.reason }}</p>
    <p v-if="finding.repair"><a :href="taskHref(finding.repair.task_id)">查看修复任务 {{ finding.repair.task_id }}</a> · {{ repairNames[finding.repair_state] || '等待任务状态' }}</p>
    <p v-if="finding.repair_task?.candidate_id">修复候选 <code>{{ finding.repair_task.candidate_id }}</code></p>
    <p v-if="finding.resolution">已记录复验候选 <code>{{ finding.resolution.candidate_id }}</code> · {{ finding.resolution.actor }} · {{ stamp(finding.resolution.created_at) }}<span v-if="finding.resolution_validity === 'historical'"> · 历史版本复验</span><span v-if="finding.resolution_validity === 'unavailable'"> · 复验原件不可用</span></p>
    <p class="secondary">裁定与修复关联保留理由。任务的必需检查和交付门禁继续执行，复验结论绑定具体候选。</p>
    <div v-if="mayAct && !kind" class="actions">
      <button type="button" :disabled="!identityReady" @click="open('decisions')">裁定发现</button>
      <button v-if="mayRepair && !finding.repair" type="button" :disabled="!identityReady" @click="open('repair', 'create')">创建修复任务</button>
      <button v-if="mayRepair && !finding.repair" type="button" :disabled="!identityReady" @click="open('repair', 'link')">关联已有修复任务</button>
      <button v-if="finding.repair && (finding.status !== 'resolved' || finding.resolution_validity !== 'current')" type="button" :disabled="!identityReady" @click="open('revalidations')">记录新候选复验</button>
    </div>
    <form v-if="kind && snapshot && !receipt" @submit.prevent="submit">
      <p>审阅候选 {{ snapshot.candidate_id }} · 定义 {{ snapshot.definition_revision }} · 计划 {{ snapshot.plan_revision }}</p>
      <p v-if="versionChanged && !pending" role="status" class="alert">发现已更新，提交前请重新核对版本；输入仍保留。</p>
      <fieldset :disabled="busy || !!pending">
        <label v-if="kind === 'decisions'">裁定<select v-model="action" aria-label="裁定"><option value="">请选择裁定</option><option value="fix">需要修复</option><option value="more_evidence">补充证据</option><option v-if="mayJudge" value="non_issue">非问题</option><option v-if="mayJudge" value="accepted_tradeoff">接受权衡</option></select></label>
        <template v-if="kind === 'repair'">
          <p>{{ mode === 'create' ? '新任务继承来源范围、验收和复现材料，由已有执行者领取。' : '关联任务需要覆盖来源范围，并声明对应的验收检查。' }}</p>
          <label>来源出现记录<select v-model="appearance" aria-label="来源出现记录"><option v-for="item in finding.occurrences" :key="item.id" :value="item.id">{{ stamp(item.created_at) }} · {{ item.source.actor }} · {{ item.id }}</option></select></label>
          <template v-if="mode === 'link'">
            <label>修复任务 ID<input v-model="repairID" @input="linkedTask = null; confirmed = false" /></label><button type="button" @click="readRepair">读取关联任务</button>
            <template v-if="linkedTask"><p>{{ linkedTask.task.definition.title }} · 范围 {{ linkedTask.task.definition.scope?.join('、') || '未声明' }}</p><label>对应检查<select v-model="repairCheck" aria-label="对应检查"><option value="">请选择检查</option><option v-for="item in linkedTask.task.definition.checks.filter((check: any) => check.required)" :key="item.id" :value="item.id">{{ item.id }} · {{ item.covers.join('、') }}</option></select></label></template>
          </template>
        </template>
        <template v-if="kind === 'revalidations'">
          <p v-if="snapshot.repair_can_revalidate">关联检查已在所示新候选上提供有效通过证据。</p><p v-else class="alert">需要关联修复任务在新候选上提供当前有效的通过证据。</p>
          <p>复验候选 {{ snapshot.repair_task?.candidate_id || '尚未提交' }} · 检查 {{ snapshot.repair.check_id }}</p>
        </template>
        <label>裁定或操作理由<textarea v-model="reason" required maxlength="8000" rows="3"></textarea></label>
        <label class="inline"><input v-model="confirmed" type="checkbox" />我已核对所示发现、来源及版本。</label>
      </fieldset>
      <p v-if="pending" role="status" class="alert">上次操作尚未确认，请按原请求重试。</p>
      <div class="actions"><button v-if="!pending" type="button" :disabled="busy" @click="refreshVersion">重新读取版本并保留输入</button><button v-if="!pending" type="button" :disabled="busy" @click="close">取消操作</button><button type="submit" class="primary" :disabled="busy">{{ busy ? '正在提交…' : pending ? '原样重试操作' : kind === 'repair' ? (mode === 'create' ? '确认创建修复任务' : '确认关联修复任务') : kind === 'revalidations' ? '确认有效复验' : '保存裁定' }}</button></div>
    </form>
    <section v-if="receipt" role="status" class="alert"><p>操作已记录 · {{ statusNames[receipt.status] || receipt.status }}</p><p v-if="receipt.repair"><a :href="taskHref(receipt.repair.task_id)">查看关联修复任务</a></p><p v-if="receipt.resolution">复验候选 {{ receipt.resolution.candidate_id }}</p><button type="button" @click="receipt = null; close()">返回发现记录</button></section>
    <p v-if="storageWarning" role="status">浏览器无法保存草稿，请保留当前页面。</p>
    <p v-if="error" role="alert" class="alert">{{ error }}<button v-if="!identityReady" type="button" @click="loadIdentity">重新读取项目身份</button></p>
    <details v-if="finding.history?.length"><summary>裁定、关联与复验历史 {{ finding.history.length }} 条</summary><article v-for="item in finding.history" :key="item.id"><p>{{ stamp(item.created_at) }} · {{ item.data.actor || item.data.decision?.actor }} · {{ decisionNames[item.data.action] || (item.kind === 'finding_revalidated' ? '有效复验' : '修复关联') }}</p><p>{{ item.data.reason || item.data.decision?.reason }}</p><p v-if="item.data.candidate_id">候选 {{ item.data.candidate_id }}</p></article></details>
  </section>
</template>

<style scoped>
.finding-actions form { display: grid; gap: 14px; margin-top: 16px; }
.finding-actions fieldset { display: grid; gap: 14px; min-width: 0; border: 0; padding: 0; }
.finding-actions label { display: grid; gap: 6px; }
.finding-actions .inline { display: flex; gap: 8px; align-items: center; }
.finding-actions code, .finding-actions p { overflow-wrap: anywhere; }
.finding-actions select { max-width: 100%; }
</style>

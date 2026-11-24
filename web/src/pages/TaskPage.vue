<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from "vue";
import { api, ApiError, currentUser, info, notices, params, refreshRevision, remoteProject } from "../api";
import { toast, copyText } from "../toast";
import {
  route,
  setQuery,
  taskHref,
  back,
  navigate,
  href,
} from "../router";
import { useResource } from "../resource";
import { useObjectReader } from "../objectReader";
import {
  workPresentation,
  progressFacts,
  workFocus,
  historyReviewText,
  label,
  stamp,
  relativeStamp,
  schedule,
  reviewNoteStatus,
  nextAction,
  goalLabel,
  blockerText,
  checkStatusSummary,
  workState,
  workOwner,
  relativeShort,
  handoffText,
  rowState,
  rowSummary,
  familyRow,
} from "../display";
import LoadState from "../components/LoadState.vue";
import ActionDialog from "../components/ActionDialog.vue";
import ManualReviewDialog from "../components/ManualReviewDialog.vue";
import MarkdownBody from "../components/MarkdownBody.vue";
import AppIcon from "../components/AppIcon.vue";
import MenuPopover from "../components/MenuPopover.vue";
import ScopeSummary from "../components/ScopeSummary.vue";
import CopyButton from "../components/CopyButton.vue";
import TaskRelations from "../components/TaskRelations.vue";
import ReviewPanel from "../components/ReviewPanel.vue";
import TaskResults from "../components/TaskResults.vue";
import DeliverySummary from "../components/DeliverySummary.vue";
import MaterialPreview from "../components/MaterialPreview.vue";
import TaskDiagnostics from "../components/TaskDiagnostics.vue";
import { stuckAt } from "../diagnostics";
import { checkReading, evidenceSource } from "../checks";
import { submittedImplementation } from "../results";
import CheckDetail from "../components/CheckDetail.vue";
import StepBar from "../components/StepBar.vue";
import QueueOwnership from "../components/QueueOwnership.vue";
import { candidateMaterials, isImage, type Material } from "../materials";
import TaskChildren from "../components/TaskChildren.vue";
import EpicOverview from "../components/EpicOverview.vue";
import FindingPanel from "../components/FindingPanel.vue";
import ImportedHistory from "../components/ImportedHistory.vue";
import { pendingReviewForTask, preservePriorWork } from "../reviewDraft";
const props = defineProps<{ id: string }>();
const responsibilityNames: Record<string,string> = { active:"责任有效", paused:"责任已暂停", cancelled:"任务已取消", superseded:"责任版本已失效", expired:"责任已到期", interrupted:"责任已中断", ended:"责任已结束" };
const r = useResource<any>(null),
  { data: n, loading, error, last } = r;
const t = computed(() => n.value?.task),
  rawSection = computed(() => route.value.query.get("section") || "overview"),
  section = computed(() => {
    if (["execution", "technical"].includes(rawSection.value)) return "execution";
    if (["checks", "findings"].includes(rawSection.value) || route.value.query.has("finding") || route.value.query.has("check")) return "checks";
    return "overview";
  });
const sections = [
  ["overview", "概览"],
  ["checks", "验收与问题"],
  ["execution", "执行记录"],
] as const;
function selectSection(id: string) {
  setQuery({ section: id, check: undefined, finding: undefined });
}
const basis = ref<any>(null),
  basisError = ref(""),
  basisLoading = ref(false);
const basisReader = useObjectReader<any>(),
  contextReader = useObjectReader<any>();
const {
  data: reading,
  error: readingError,
  loading: readingLoading,
} = basisReader;
const {
  data: supplied,
  error: contextError,
  loading: contextLoading,
} = contextReader;
const busy = ref(false),
  op = ref<any>(null),
  opError = ref(""),
  confirmationStale = ref(false),
  confirmationRefreshed = ref(false),
  actor = ref(sessionStorage.getItem("ael-actor") || "dashboard-user"),
  reason = ref(""),
  ack = ref(false),
  resumeTarget = ref(""),
  edit = ref("");
const failureReview = ref({ tried: "", excluded: "", hypothesis: "", next: "", recovery: "", evidence: "" });
const currentBlocker = computed(() => n.value?.summary?.blocker_reason && !n.value.summary.needs_history_review ? t.value?.blocker : undefined);
const activeFailure = computed(() => t.value?.status === "open" && !n.value?.summary?.needs_history_review && (t.value?.paused || n.value?.next_action === "repair") ? t.value?.failure_cycles?.[t.value?.active_failure] : undefined);
const needsRootReview = computed(() => t.value?.paused && ["review_required", "blocked"].includes(activeFailure.value?.state));
const manualCheck = ref<string | null>(null);
const pendingReviewCheck = ref<string | null>(null);
const workFormOpen = ref(false);
const availableHumanChecks = computed(() =>
  (t.value?.definition.checks || []).filter((c: any) => n.value?.check_access?.[c.id]?.can_review),
);
const mayJudge = computed(() => !remoteProject.value || ["admin", "reviewer"].includes(remoteProject.value.role));
const showReviewEntry = computed(() => mayJudge.value &&
  ((t.value?.status === "open" && t.value.definition.type !== "epic") ||
    !!n.value?.summary?.human_review_count || pendingReviewCheck.value !== null)
);
const claimed = ref<any>(null),
  result = ref({
    status: "pass",
    summary: "",
    artifacts: "",
    failure_class: "",
    knowledge_status: "unchanged",
    knowledge_reason: "",
    knowledge_artifacts: "",
    return_to: "",
  });
const advancedResult = ref('{"status":"pass","summary":"","artifacts":[]}'),
  deliveryRef = ref("");
const pendingSubmission = ref<any>(null), storageWarning = ref("");
const emptyResult = () => ({ status: "pass", summary: "", artifacts: "", failure_class: "", knowledge_status: "unchanged", knowledge_reason: "", knowledge_artifacts: "", return_to: "" });
const claimKey = (id: string) => "ael-dashboard-claim-v1:" + id;
function saveClaim(id: string, attempt: any, draft = result.value, pending = pendingSubmission.value) {
  try {
    sessionStorage.setItem(claimKey(id), JSON.stringify({ attempt, draft, pending, advanced: advancedResult.value, deliveryRef: deliveryRef.value }));
    storageWarning.value = "";
  } catch {
    storageWarning.value = "浏览器无法保存接手凭据；刷新或关闭此标签页后将无法继续提交。";
  }
}
function removeClaim(id: string, attemptId: string) {
  try {
    const stored = JSON.parse(sessionStorage.getItem(claimKey(id)) || "null");
    if (stored?.attempt?.id === attemptId) sessionStorage.removeItem(claimKey(id));
  } catch { /* A corrupt record is never used as proof of ownership. */ }
}
watch([result, advancedResult, deliveryRef], () => {
  if (claimed.value) saveClaim(props.id, claimed.value);
}, { deep: true, flush: "sync" });
async function restoreClaim(id: string) {
  let saved: any;
  try { saved = JSON.parse(sessionStorage.getItem(claimKey(id)) || "null"); } catch { return; }
  if (!saved?.attempt?.id || !saved.attempt.token || !n.value || n.value.task?.definition?.id !== id) return;
  const a = n.value.task.attempts?.find((item: any) => item.id === saved.attempt.id);
  const valid = a && a.status === "running" && Date.parse(a.lease_until) > Date.now() &&
    !a.pid && !a.owner_pid && !a.session && n.value.task.status === "open" && !n.value.task.paused &&
    a.unit === n.value.task.current_step && a.unit === saved.attempt.unit &&
    a.definition_revision === n.value.definition_revision && a.plan_revision === n.value.plan_revision &&
    a.definition_revision === saved.attempt.definition_revision && a.plan_revision === saved.attempt.plan_revision;
  if (!valid && !saved.pending) {
    if (!a) {
      if (claimed.value?.id === saved.attempt.id) claimed.value = null;
      return;
    }
    if (!preservePriorWork(id, saved.attempt, saved.draft)) {
      storageWarning.value = "浏览器无法另存旧接手的未提交内容；原记录暂时保留，请先复制需要的文字。";
      if (claimed.value?.id === saved.attempt.id) claimed.value = null;
      return;
    }
    removeClaim(id, saved.attempt.id);
    if (claimed.value?.id === saved.attempt.id) claimed.value = null;
    return;
  }
  if (claimed.value?.id !== saved.attempt.id) {
    result.value = { ...emptyResult(), ...(saved.draft || {}) };
    advancedResult.value = saved.advanced || advancedResult.value;
    deliveryRef.value = saved.deliveryRef || "";
    pendingSubmission.value = saved.pending || null;
    claimed.value = saved.attempt;
    workFormOpen.value = true;
    actor.value = saved.attempt.executor;
    if (valid && !saved.pending) await contextReader.read(() => api("/api/context?" + params({ id })));
  }
}
const reviewObjects = ref<any[]>([]), importedArchive = ref<any>(null);
let importedKey = "";
// Pending human checks shown inline; non-judges see them read-only.
const reviewChecks = computed(() => (t.value?.definition.checks || []).filter((c: any) => n.value?.check_access?.[c.id]?.effective_method === 'human' && (n.value.check_access[c.id].can_review || (n.value?.current_checks?.[c.id] && t.value?.status === 'open') || pendingReviewCheck.value === c.id || n.value?.summary?.manual_reviews?.some((m: any) => m.check === c.id))));
const reviewImageRefs = computed(() => { const image = candidateMaterials(reviewObjects.value, t.value).current.find(isImage); return image ? [image.ref] : []; });
const receiptReader = useObjectReader<any>();
const {
  data: receipt,
  error: receiptError,
  loading: receiptLoading,
} = receiptReader;
const checkOpen = ref<Record<string, boolean>>({});
let openedCheckKey = "", openedLegacyKey = "";
let timer: ReturnType<typeof setInterval>;
async function extras() {
  const id = props.id;
  const importID = n.value?.task?.legacy?.authority_import;
  if (remoteProject.value && importID && importedKey !== importID) {
    try { const archive = await api('/api/authority-import?' + params({ id: importID, task: id })); if (id === props.id) { importedArchive.value = archive; importedKey = importID; } } catch { /* ImportedHistory reports its own read errors. */ }
  }
  if(remoteProject.value) {
    try {const objects=await api('/api/review-evidence?'+params({id}));if(id===props.id){reviewObjects.value=objects;artifactNames.value=Object.fromEntries(objects.map((o:any)=>[o.id,o.filename]));}}catch{/* Evidence itself still reports read errors. */}
  }
  if (!basis.value && !basisLoading.value) {
    basisLoading.value = true;
    try {
      const v = await api("/api/task-knowledge?" + params({ id, compact: 1 }));
      if (id === props.id) { basis.value = v; basisError.value = ""; }
    } catch (e) {
      if (id === props.id) basisError.value = String(e);
    } finally {
      if (id === props.id) basisLoading.value = false;
    }
  }
}
async function load(quiet = false) {
  const id = props.id;
  await r.load(() => api("/api/task?" + params({ id })), quiet);
  if (id !== props.id) return;
  if (!remoteProject.value) await restoreClaim(id);
  pendingReviewCheck.value = pendingReviewForTask(info.value.project || "", id);
  const requestedReview = route.value.query.get("review");
  if (requestedReview && n.value?.task?.definition?.id === id) {
    if (n.value.check_access?.[requestedReview]?.can_review || requestedReview === pendingReviewCheck.value) manualCheck.value = requestedReview;
    setQuery({ review: undefined });
  }
  // ?detail=<check> opens that check's detail dialog directly.
  const requestedDetail = route.value.query.get("detail");
  if (requestedDetail && n.value?.task?.definition?.id === id) {
    detailCheck.value = n.value.task.definition.checks?.find((c: any) => c.id === requestedDetail) || null;
    setQuery({ detail: undefined });
  }
  const legacyTarget = rawSection.value === "basis" && !n.value?.summary?.requires_task_decision
    ? "task-basis"
    : rawSection.value === "findings" || route.value.query.has("finding") ? "task-findings" : "";
  const legacyKey = legacyTarget ? id + ":" + route.value.key : "";
  if (legacyKey && n.value?.task?.definition?.id === id && openedLegacyKey !== legacyKey) {
    openedLegacyKey = legacyKey;
    await nextTick();
    document.getElementById(legacyTarget)?.scrollIntoView({ block: "start" });
  } else if (!legacyKey) {
    openedLegacyKey = "";
  }
  const requestedCheck = route.value.query.get("check");
  const checkKey = id + ":" + requestedCheck;
  if (!requestedCheck) openedCheckKey = "";
  if (!route.value.query.has("finding") && section.value === "checks" && requestedCheck && openedCheckKey !== checkKey && n.value?.task?.definition?.checks?.some((check: any) => check.id === requestedCheck)) {
    openedCheckKey = checkKey;
    checkOpen.value[requestedCheck] = true;
    await nextTick();
    requestAnimationFrame(() => document.getElementById("task-check-" + requestedCheck)?.scrollIntoView({ block: "start" }));
  }
  try {
    await extras();
  } catch (e) {
    error.value = String(e);
  }
}
watch(
  () => props.id,
  () => {
    op.value = null;
    n.value = null;
    basis.value = null;
    basisError.value = "";
    basisLoading.value = false;
    basisReader.clear();
    contextReader.clear();
    receiptReader.clear();
    claimed.value = null;
    reviewObjects.value = [];
    importedArchive.value = null;
    importedKey = "";
    pendingSubmission.value = null;
    result.value = emptyResult();
    manualCheck.value = null;
    pendingReviewCheck.value = null;
    checkOpen.value = {};
    openedCheckKey = "";
    workFormOpen.value = false;
    load();
  },
);
watch(section, () => {
  basisReader.clear();
  load(true);
});
watch([rawSection, () => route.value.query.get("check"), () => route.value.query.get("finding")], () => {
  void load(true);
});
// The shell's refresh button re-reads the task; say whether that worked.
watch(refreshRevision, async () => {
  await load();
  toast(error.value ? "刷新失败，页面仍显示 " + (last.value || "之前") + " 读取的内容" : "已重新读取任务状态");
});
watch(() => info.value.project, (project) => { pendingReviewCheck.value = pendingReviewForTask(project || "", props.id); });
onMounted(() => {
  load();
  timer = setInterval(() => {
    if (!busy.value && !op.value) load(true);
  }, 4000);
});
onUnmounted(() => clearInterval(timer));
const runnable = computed(
  () =>
    ["continue", "repair"].includes(n.value?.next_action) &&
    t.value?.current_step !== "verify",
);
const unownedRunning = computed(() => !claimed.value && t.value?.status === "open" && t.value?.attempts?.some((a: any) => a.status === "running" || a.status === "interrupted"));
const recoverable = computed(
  () =>
    t.value?.status === "open" &&
    (t.value.paused ||
      t.value.blocker ||
      n.value?.blocking_reasons?.some((reason: string) =>
        reason.includes("applicable knowledge changed after execution"),
      ) ||
      t.value.attempts?.some(
        (a: any) =>
          a.status === "interrupted" ||
          (a.status === "running" && Date.parse(a.lease_until) <= Date.now()),
      )),
);
const blockedHumanCheck = computed(() => {
  const unit = currentBlocker.value?.unit;
  if (!unit?.startsWith("check:") || n.value?.next_action !== "wait_environment" || !t.value?.candidate) return "";
  const check = unit.slice(6);
  return n.value?.check_access?.[check]?.effective_method === "human" &&
    n.value?.current_checks?.[check]?.result === "blocked" ? check : "";
});
const primaryKind = computed(() => {
  if (t.value?.status === "completed") return "delivery";
  if (t.value?.status !== "open") return "";
  if (n.value?.summary?.needs_history_review) return mayJudge.value ? "restore" : "history";
  if (n.value?.summary?.requires_task_decision) return "";
  if (n.value?.next_action === "wait_confirmation") return "basis";
  if (mayJudge.value && blockedHumanCheck.value && !needsRootReview.value) return "blocked_review";
  if (mayJudge.value && availableHumanChecks.value.length) return "review";
  if (remoteProject.value) {
    if (t.value.legacy?.authority_import && t.value.paused && !t.value.attempts?.length && ["admin", "executor"].includes(remoteProject.value.role)) return "resume";
    // A plain pause is lifted by the same authorized resume action.
    if (t.value.paused && !t.value.confirmation && !needsRootReview.value && ["admin", "executor"].includes(remoteProject.value.role)) return "resume";
    return t.value.current_step === "verify" ? "checks" : "";
  }
  if (needsRootReview.value) return "root_review";
  if (t.value.definition.type === "epic" && n.value?.next_action === "wait_integration") return "prepare_integration";
  if (claimed.value) return "submit";
  if (recoverable.value) return "resume";
  if (runnable.value) return "claim";
  if (n.value?.board === "active") return "execution";
  if (t.value?.current_step === "verify" && t.value?.status === "open") return "checks";
  return "";
});
const primaryLabels: Record<string, string> = { blocked_review: "打开受阻人工检查", history: "核对历史资料", restore: "确认核对并准备恢复", prepare_integration: "准备整体验收", root_review: "记录根因复核", review: "查看待验内容", resume: "恢复任务", basis: "查看待核实规范", claim: "接手当前工作", checks: "查看验收与运行检查", submit: "填写实施结果", execution: "查看当前执行", delivery: "查看交付结果" };
const presentation = computed(() => workPresentation(n.value?.summary));
const progress = computed(() => progressFacts(n.value));
const implementation = computed(() => submittedImplementation(n.value));
const recordedChecks = computed(() => (t.value?.definition.checks || [])
  .filter((check: any) => n.value?.current_checks?.[check.id] && n.value?.check_access?.[check.id]?.effective_method !== 'human'));
const situationFact=computed(()=>{
  if(familyLed.value)return rowSummary(familyRow(n.value?.summary,family.value))+'。';
  if(n.value?.summary?.needs_history_review)return historyReviewText(t.value?.confirmation?.reason ? blockerText(t.value.confirmation.reason,false) : '');
  const fact=workFocus(n.value?.summary);
  // Avoid repeating the title; show the recorded goal instead of a placeholder.
  if(fact==='等待执行者接手')return '目标：'+(t.value?.definition?.original_request || t.value?.definition?.acceptance?.join('；') || '原目标未记录');
  return fact;
});
const epicDetailsOpen=ref(route.value.query.has('leaf_offset') || route.value.query.has('progress'));
const bannerTitle = computed(() => presentation.value.title);
const bannerSummary = computed(() => presentation.value.latest || (t.value?.historical ? '历史成果尚未在当前版本复验' : '最近有效成果尚未回报'));
const bannerReasons = computed(() => (n.value?.blocking_reasons || []).filter((reason: string) =>
  reason !== currentBlocker.value?.reason && reason !== t.value?.confirmation?.reason && (t.value?.definition?.type !== "epic" || (!reason.startsWith("required child ") && reason !== "required leaves completed; parent integration remains to be accepted"))));
const reviewState = computed(() => reviewNoteStatus(n.value?.summary));
// Header and situation read the same summary the workbench rows use.
const family = ref<any>(null);
watch(() => props.id, () => { family.value = null; });
const ownState = computed(() => workState(n.value?.summary, mayJudge.value));
const headState = computed(() => rowState(familyRow(n.value?.summary, family.value), mayJudge.value));
// A parent whose own step is idle but whose children are moving reads as the
// family; its own next step is still shown separately below.
const familyLed = computed(() => !!family.value?.descendants && headState.value.key !== ownState.value.key);
const parentId = computed(() => t.value?.epic_membership?.parent || t.value?.definition?.parent || "");
const updatedAt = computed(() => n.value?.summary?.health?.last_progress_at || n.value?.summary?.health?.reported_at || n.value?.summary?.activity_at || t.value?.completed || "");
const situationTitle = computed(() => {
  const key = headState.value.key;
  const titles: Record<string, string> = {
    blocked: "当前受阻", review: mayJudge.value ? "本次结果已准备好，等待你确认" : "本次结果等待人工验收", history: "历史结果需要对应到当前版本",
    pending: "等待执行者接手", done: t.value?.historical ? "历史完成记录 · 当前未复验" : "本项工作已交付", cancelled: "这项工作已取消",
    decision: "需要作出决定", repair: "等待修复", resume: "恢复已批准，等待执行者继续", children: "等待子任务", integration: "等待整体验收", dependency: "等待前置交付",
  };
  if (familyLed.value) return ({ active: "子任务正在推进", review: mayJudge.value ? "有子任务等待你确认" : "有子任务等待人工验收", blocked: "有子任务受阻" } as Record<string, string>)[key] || headState.value.label;
  return key === "active" ? "正在推进 · " + label(t.value?.current_step) : titles[key] || presentation.value.title;
});
const situationIcon = computed(() => ({ blocked: "alert", done: "tick", history: "history", review: "user", decision: "shield", cancelled: "x" } as Record<string, string>)[headState.value.key] || "workbench");
async function copyTaskHandoff() {
  const text = handoffText(remoteProject.value?.id || info.value.project?.split("/").filter(Boolean).pop() || "当前项目", { id: props.id, title: t.value?.definition?.title || props.id, next: t.value?.status === "open" ? presentation.value.next : "" });
  toast(await copyText(text) ? "已复制接续要求；复制不会启动或领取任务" : "浏览器未允许复制，请在任务信息中手动复制");
}
async function copyTaskInfo() {
  const project = remoteProject.value ? remoteProject.value.name + "（" + remoteProject.value.id + "）" : info.value.project || "本地项目";
  toast(await copyText(`${t.value?.definition?.title || props.id}\n任务：${props.id}\n项目：${project}\n当前：${headState.value.label}`) ? "已复制任务信息" : "浏览器未允许复制");
}
async function openBasisFromAside(k?: any) {
  if (section.value !== "overview") selectSection("overview");
  await nextTick();
  const el = document.getElementById("task-basis") as HTMLDetailsElement | null;
  if (el) { el.open = true; if (!k) el.scrollIntoView({ block: "start" }); }
  if (k) await showBasis(k);
}
function primaryAction() {
  if (primaryKind.value === "restore") confirmation("approve", "核对历史工作");
  else if (primaryKind.value === "history") selectSection("execution");
  else if (primaryKind.value === "prepare_integration") start("prepare_integration", "准备整体验收");
  else if (primaryKind.value === "root_review") start("review_failure", "记录根因复核");
  else if (primaryKind.value === "blocked_review") manualCheck.value = blockedHumanCheck.value;
  else if (primaryKind.value === "review") {
    if(section.value==="overview" && document.getElementById("overview-review")){document.getElementById("overview-review")?.scrollIntoView({block:"start"});return;}
    selectSection("checks");
    if (availableHumanChecks.value.length === 1) setQuery({ check: availableHumanChecks.value[0].id });
  }
  else if (primaryKind.value === "resume") start("resume", "恢复任务");
  else if (primaryKind.value === "basis") {const el=document.getElementById("task-basis") as HTMLDetailsElement|null;if(el){el.open=true;el.focus();}}
  else if (primaryKind.value === "claim") start("claim", "接手当前工作");
  else if (primaryKind.value === "checks") selectSection("checks");
  else if (primaryKind.value === "submit") void showWorkForm();
  else if (primaryKind.value === "execution") selectSection("execution");
  else if (primaryKind.value === "delivery") void showDelivery();
}
function openTaskReview() {
  if (busy.value) return;
  workFormOpen.value = false;
  manualCheck.value = pendingReviewCheck.value !== null ? pendingReviewCheck.value : availableHumanChecks.value.length === 1 ? availableHumanChecks.value[0].id : "";
}
function closeReview() { manualCheck.value = null; pendingReviewCheck.value = pendingReviewForTask(info.value.project || "", props.id); }
function reviewSaved(_result?: string, message?: string) { if (message) toast(message); pendingReviewCheck.value = pendingReviewForTask(info.value.project || "", props.id); void load(true); }
async function showWorkForm() {
  selectSection("overview");
  await nextTick();
  workFormOpen.value = true;
  await nextTick();
  document.getElementById("work-result")?.scrollIntoView({ block: "start" });
}
async function showDelivery() {
  selectSection("overview");
  await nextTick();
  const detail = document.getElementById("delivery-result") as HTMLDetailsElement | null;
  if (detail) { detail.open = true; detail.scrollIntoView({ block: "start" }); }
}
function stageDone(step: string) {
  if (step === "verify") return !!n.value?.summary?.checks?.required && n.value.summary.checks.required_passed === n.value.summary.checks.required;
  return !!t.value?.attempts?.some((a: any) => a.unit === step && a.status === "pass" && a.definition_revision === n.value.definition_revision && a.plan_revision === n.value.plan_revision);
}
const decisionEvidence = computed(() => {
  const confirmation = t.value?.confirmation;
  if (confirmation?.kind !== "plan") return null;
  return [...(t.value?.attempts || [])].reverse().find((item: any) =>
    item.unit === "analysis" && item.status === "pass" &&
    item.candidate_version === confirmation.candidate &&
    item.definition_revision === confirmation.definition_revision && item.plan_revision === confirmation.plan_revision
  );
});
const deleteBlocked = computed(() => !!t.value?.attempts?.some((a: any) => ["running", "interrupted"].includes(a.status)));
const stuck = computed(() => stuckAt(n.value));
const currentResponsibilities = computed(() => (t.value?.attempts || []).filter((attempt: any) =>
  t.value?.status === "open" && attempt.status === "running" && Date.parse(attempt.lease_until) > Date.now()
));
watch(
  () =>
    JSON.stringify([
      n.value?.definition_revision,
      t.value?.applicable_knowledge,
      t.value?.definition.knowledge,
    ]),
  () => {
    basis.value = null;
    basisReader.clear();
  },
);
const localBasis = computed(() => (t.value?.applicable_knowledge || []).filter((ref: any) => ref.local_only));
const unresolvedBasisIDs = computed<string[]>(() => !basis.value ? [] :
  [...new Set<string>(t.value?.definition.knowledge || [])].filter((id) =>
    !basis.value.records.some((record: any) => record.id === id) && !localBasis.value.some((ref: any) => ref.id === id)
  )
);
function explicitReference(k: any) {
  return (
    t.value?.definition.knowledge?.includes(k.id) || (!remoteProject.value && k.tasks?.includes(props.id))
  );
}
function start(action: string, title: string, extra: any = {}) {
  if (busy.value) return;
  op.value = {
    action,
    title,
    id: props.id,
    objectTitle: t.value.definition.title,
    revision: n.value.definition_revision,
    remote_revision: n.value.revision,
    request_key: remoteProject.value ? crypto.randomUUID() : undefined,
    candidate: t.value.candidate,
    plan_revision: n.value.plan_revision,
    ...extra,
  };
  opError.value = "";
  confirmationStale.value = false;
  confirmationRefreshed.value = false;
  reason.value = "";
  if (action === "review_failure") failureReview.value = { tried: t.value?.blocker?.tried || "", excluded: "", hypothesis: "", next: "", recovery: "", evidence: "" };
  ack.value = false;
  resumeTarget.value =
    action === "resume" &&
    n.value.blocking_reasons?.some((reason: string) =>
      reason.includes("applicable knowledge changed after execution"),
    )
      ? "execute"
      : "";
  if (action === "edit")
    edit.value = JSON.stringify(t.value.definition, null, 2);
}
async function reloadConfirmation() {
  const current = op.value;
  confirmationRefreshed.value = false;
  await load();
  if (op.value === current && !error.value) confirmationRefreshed.value = true;
}
const refreshedConfirmation = computed(() => {
  const latest = t.value?.confirmation;
  return confirmationRefreshed.value && latest && latest.kind === op.value?.extra?.kind ? latest : null;
});
function useRefreshedConfirmation() {
  const target = refreshedConfirmation.value;
  if (!target || !op.value) return;
  op.value.extra = confirmationTarget(target, op.value.extra.decision);
  op.value.revision = target.definition_revision;
  op.value.plan_revision = target.plan_revision;
  op.value.remote_revision = n.value.revision;
  if (remoteProject.value) op.value.request_key = crypto.randomUUID();
  confirmationStale.value = false;
  confirmationRefreshed.value = false;
  opError.value = "";
}
async function perform() {
  if (busy.value || !op.value || confirmationStale.value) return;
  busy.value = true;
  opError.value = "";
  const current = { ...op.value };
  try {
    let extra = current.extra || {};
    if (current.action === "edit")
      extra = { definition: JSON.parse(edit.value) };
    if (current.action === "resume") extra = { unit: resumeTarget.value };
    if (current.action === "review_failure") extra = { failure_review: { ...failureReview.value, actor: actor.value, evidence: failureReview.value.evidence.split(",").map((ref) => ref.trim()).filter(Boolean) } };
    const v = await api("/api/action", {
      action: current.action,
      id: current.id,
      executor: actor.value,
      reason: reason.value,
      ack_stopped: ack.value,
      ...(remoteProject.value ? { remote_revision: current.remote_revision, request_key: current.request_key } : {}),
      ...extra,
    });
    if (current.action === "claim" && v.attempt?.id && v.attempt?.token) {
      saveClaim(current.id, v.attempt, emptyResult(), null);
    }
    if (current.id !== props.id) {
      notices.value = current.title + "已记录，请在对应任务查看结果。";
      return;
    }
    sessionStorage.setItem("ael-actor", actor.value);
    if (current.action === "claim") {
      claimed.value = v.attempt;
      workFormOpen.value = true;
      pendingSubmission.value = null;
      contextReader.clear();
      receiptReader.clear();
      deliveryRef.value = "";
      result.value = emptyResult();
    }
    notices.value =
      current.title +
      "已记录。" +
      (v.next_action ? "下一步：" + label(v.next_action) + "。" : "");
    op.value = null;
    if (current.action === "delete") {
      navigate("/tasks");
      return;
    }
    basis.value = null;
    await load(true);
    if (current.action === "claim" && current.id === props.id)
      await contextReader.read(() =>
        api("/api/context?" + params({ id: current.id })),
      );
  } catch (e) {
    if (op.value) {
      opError.value = String(e);
      if (current.action === "confirm" && e instanceof ApiError &&
        ["REVISION_CONFLICT", "CONFIRMATION_VERSION_MISMATCH", "CONFIRMATION_MISMATCH", "CANDIDATE_CHANGED"].includes(e.code)) {
        confirmationStale.value = true;
      }
    } else error.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function submit(advanced = false) {
  if (busy.value || !claimed.value) return;
  const taskId = props.id, attempt = claimed.value;
  busy.value = true;
  error.value = "";
  try {
    let stepResult: any = {
      status: result.value.status,
      summary: result.value.summary,
      artifacts: result.value.artifacts
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean),
    };
    if (attempt.unit === "execute" && result.value.status === "pass" && result.value.knowledge_reason.trim())
      stepResult.knowledge = {
        status: result.value.knowledge_status,
        reason: result.value.knowledge_reason,
        artifacts: result.value.knowledge_artifacts
          .split(",")
          .map((x) => x.trim())
          .filter(Boolean),
      };
    if (result.value.status === "fail" && result.value.return_to)
      stepResult.return_to = result.value.return_to;
    if (result.value.status === "fail" && result.value.failure_class.trim())
      stepResult.failure_class = result.value.failure_class.trim();
    if (attempt.unit === "delivery" && receipt.value) {
      stepResult.delivery = receipt.value.receipt;
      if (!stepResult.artifacts.includes(receipt.value.ref))
        stepResult.artifacts.push(receipt.value.ref);
    }
    if (stepResult.status === "blocked")
      stepResult.blocker = {
        category: "environment",
        reason: result.value.summary,
        scope: t.value.definition.scope,
        tried: "见本次工作结果",
        recovery: result.value.knowledge_reason || "核实环境后恢复",
        resume_at: attempt.unit,
      };
    if (advanced) stepResult = JSON.parse(advancedResult.value);
    const payload = pendingSubmission.value || {
      action: "submit",
      submission: {
        task: taskId,
        attempt: attempt.id,
        token: attempt.token,
        key: attempt.id + "-dashboard",
        result: stepResult,
      },
    };
    pendingSubmission.value = payload;
    saveClaim(taskId, attempt, result.value, payload);
    await api("/api/action", payload);
    removeClaim(taskId, attempt.id);
    if (props.id !== taskId || claimed.value?.id !== attempt.id) {
      notices.value = "工作结果已提交，请在对应任务查看下一步。";
      return;
    }
    pendingSubmission.value = null;
    claimed.value = null;
    notices.value = "工作结果已提交，状态由协议重新计算。";
    await load(true);
  } catch (e) {
    // These errors are returned before a submission is committed; other failures
    // may mean the response was lost after commit and must be retried verbatim.
    const stale = e instanceof ApiError && ["STALE_TOKEN", "ATTEMPT_MISSING", "STALE_EXECUTION_UNIT", "TERMINAL_TASK"].includes(e.code);
    if (props.id === taskId && claimed.value?.id === attempt.id && e instanceof ApiError && (stale || ["RESULT_REQUIRED", "INVALID_RESULT", "ARTIFACT_REQUIRED", "ARTIFACT_MISSING", "UNSAFE_PATH", "SYMLINK_PATH", "TASK_CONTEXT_REQUIRED", "KNOWLEDGE_OUTCOME_REQUIRED", "KNOWLEDGE_ARTIFACT_REQUIRED", "DELIVERY_RECEIPT_REQUIRED", "DELIVERY_RECEIPT_INVALID", "DELIVERY_RECEIPT_MISMATCH", "WRONG_ENVIRONMENT", "TARGET_REQUIRED"].includes(e.code))) {
      pendingSubmission.value = null;
      saveClaim(taskId, attempt, result.value, null);
      if (e.code === "TASK_CONTEXT_REQUIRED") void contextReader.read(() => api("/api/context?" + params({ id: taskId })));
      if (stale) await load(true);
    }
    if (props.id === taskId) error.value = String(e);
    else notices.value = "工作结果提交未确认，请回到对应任务查看并重试。";
  } finally {
    busy.value = false;
  }
}
async function readReceipt() {
  const id = props.id,
    ref = deliveryRef.value;
  await receiptReader.read(async () => ({
    receipt: JSON.parse(
      await api("/api/artifact?" + params({ task: id, ref }), undefined, true),
    ),
    ref,
  }));
}
const artifactNames = ref<Record<string,string>>({});
function artifactName(ref:string) { return artifactNames.value[ref] || (remoteProject.value ? '附件原件' : ref.split('/').pop() || ref); }
const detailCheck = ref<any>(null);
// Evidence and outputs open in the shared MaterialPreview dialog: it owns
// loading, focus, Esc/backdrop closing and returning focus to the trigger.
const preview = ref<{ material: Material; relations: any[] } | null>(null);
function openArtifact(ref: string, sourceTask = props.id) {
  const o = reviewObjects.value.find((x: any) => x.id === ref);
  const attempt = sourceTask === props.id ? t.value?.attempts?.find((a: any) => a.outputs?.includes(ref)) : undefined;
  const candidate = o?.candidate || attempt?.candidate_version || '';
  const evidence = sourceTask === props.id && [...(t.value?.evidence || []), ...(t.value?.evidence_history || [])].some((e: any) => e.artifacts?.includes(ref) || e.diagnostic_artifacts?.includes(ref));
  const material: Material = {
    id: ref, ref, task: sourceTask, name: o?.filename || (sourceTask === props.id ? artifactName(ref) : ref.split('/').pop() || ref), mime: o?.mime || '',
    size: o?.size, at: o?.created_at || attempt?.ended, source: o?.source, candidate, sha256: o?.sha256, status: o?.status,
    state: !candidate ? 'unknown' : candidate === t.value?.candidate ? 'current' : 'historical', role: evidence ? 'evidence' : 'output',
  };
  // Other objects with the same recorded content keep their own relations.
  const relations = o ? reviewObjects.value.filter((x: any) => x.id === ref || (o.sha256 && x.sha256 === o.sha256 && x.size === o.size))
    .map((x: any) => ({ ref: x.id, name: x.filename, label: x.source === 'knowledge snapshot' ? '执行输入（规范快照）' : x.attempt_id ? '提交记录 ' + x.attempt_id.slice(0, 12) : x.source || '附件', at: x.created_at, candidate: x.candidate || undefined })) : [];
  preview.value = { material, relations };
}
async function readBasis(k: any) {
  const b = t.value.applicable_knowledge?.find((b: any) => b.id === k.id);
  await basisReader.read(async () => {
    const record = await api(
      "/api/knowledge-record?" +
        params({ id: k.id, version: b?.version || "", ...(remoteProject.value ? { task: props.id } : {}) }),
    );
    return {
      ...record,
      supplied: !!b?.version,
      explicit: explicitReference(k),
    };
  });
}
function basisRelations(k: any) {
  const supplied = t.value?.applicable_knowledge?.some((item: any) => item.id === k.id);
  const explicit = explicitReference(k);
  return [
    ...(supplied ? ["实际提供"] : []),
    ...(explicit ? ["显式引用"] : []),
    ...(!supplied && !explicit ? ["范围相关 · 待核实"] : []),
  ];
}
let basisTrigger: HTMLElement | null = null;
async function showBasis(k: any) {
  basisTrigger = document.activeElement as HTMLElement;
  await readBasis(k);
  await nextTick();
  document.getElementById("basis-reading")?.focus();
}
function closeBasis() {
  basisReader.clear();
  basisTrigger?.focus();
}
function confirmationTarget(c: any, decision: string) {
  return { kind: c.kind, revision: c.definition_revision, plan_revision: c.plan_revision, candidate: c.candidate, decision };
}
function confirmation(decision: string, title: string) {
  start("confirm", title, { extra: confirmationTarget(t.value.confirmation, decision) });
}
function checkState(check: string) {
  const run = n.value?.current_runs?.[check];
  if (run) return run.started || run.reported ? "running" : "claimed";
  return n.value?.current_checks?.[check]?.result || "not_run";
}
function coverageLabel(c: any) {
  const acceptance = t.value?.definition?.acceptance || [];
  return (c.covers || []).map((text: string) => {
    const index = acceptance.indexOf(text);
    return index >= 0 ? `条件 ${index + 1}` : text;
  }).join("、");
}
function historyForCheck(check: string) {
  const current = n.value?.current_checks?.[check];
  const rows = [...(t.value?.evidence || [])].reverse();
  const index = rows.findIndex((item: any) => current && item.check === check &&
    item.at === current.at && item.candidate === current.candidate &&
    item.acceptance_revision === current.acceptance_revision && item.plan_revision === current.plan_revision &&
    item.config_hash === current.config_hash);
  if (index >= 0) rows.splice(index, 1);
  return [...rows, ...(t.value?.evidence_history || []).slice().reverse()].filter((item) => item.check === check);
}
async function showCheckFindings(check: string) {
  setQuery({ section: "checks", check, finding: undefined, finding_offset: undefined });
  await nextTick();
  document.getElementById("task-findings")?.focus();
}
function canCheck(c: any) {
  return (
    t.value.status === "open" && !t.value.paused && !t.value.confirmation &&
    t.value.current_step === "verify" && t.value.candidate &&
    ["continue", "repair", "wait_executor"].includes(n.value.next_action) &&
    !t.value.attempts?.some(
      (a: any) =>
        (a.unit === c.id || a.unit === "check:" + c.id) &&
        a.status === "running",
    )
  );
}
function checkReason(c: any) {
  if (n.value?.current_runs?.[c.id] || t.value.status !== "open") return "";
  const access = n.value?.check_access?.[c.id];
  if (access?.effective_method === "human") {
    if (checkState(c.id) === "pass" || access.can_review) return mayJudge.value ? "" : "当前角色可查看验收记录。正式结论由有权限的验收人提交。";
    return mayJudge.value ? "等候当前候选与验收条件就绪。可先记录人工意见。" : "";
  }
  if (!t.value.candidate) return "待执行者提交候选后运行检查。";
  if (remoteProject.value || !c.command?.length) return "由具备对应能力的执行者记录实际检查结果。";
  return canCheck(c) ? "" : "当前条件尚未允许执行该检查。";
}
</script>
<template>
  <div class="detail-page detail" aria-label="任务详情">
    <a class="text-button wb-back-line" :href="href('/tasks')" @click.prevent="back('/tasks')"><AppIcon name="chevron-left" />工作台</a>
    <header v-if="!t" class="wb-detail-header"><h1 class="wb-detail-title">任务详情</h1><CopyButton :value="id" label="复制任务 ID" /></header>
    <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
    <template v-if="n && t">
      <p v-if="storageWarning && !claimed" class="wb-banner error" role="alert">{{ storageWarning }}</p>
      <header class="wb-detail-header">
        <div class="wb-detail-title-bar">
          <div>
            <h1 class="wb-detail-title">{{ t.definition.title }}</h1>
            <div class="wb-detail-meta"><span class="tag" :class="headState.tone">{{ headState.label }}</span><span>{{ label(t.definition.type) }}</span><template v-if="updatedAt"><span class="wb-thin-divider"></span><span>{{ relativeShort(updatedAt) }}更新</span></template><a v-if="parentId" class="text-button" :href="taskHref(parentId)">所属主要事项</a></div>
          </div>
          <div class="wb-detail-actions">
            <button v-if="primaryKind && !n.summary.requires_task_decision" class="primary" :disabled="busy" @click="primaryAction">{{ primaryLabels[primaryKind] }}</button>
            <button v-if="t.status === 'open'" @click="copyTaskHandoff"><AppIcon name="copy" />复制给 Agent</button>
            <MenuPopover label="更多任务操作" menu-id="task-menu" menu-class="task-menu" trigger-class="icon-button" trigger-title="更多任务操作"><template #trigger><AppIcon name="more" /></template><button role="menuitem" v-if="showReviewEntry" :disabled="busy" @click="openTaskReview"><AppIcon name="user" />记录人工意见</button><button role="menuitem" @click="copyTaskInfo"><AppIcon name="copy" />复制任务信息</button><a role="menuitem" :href="taskHref(id, 'technical')"><AppIcon name="terminal" />查看技术详情</a><button role="menuitem" v-if="t.status === 'open' && !t.paused && (!remoteProject || ['admin','executor'].includes(remoteProject.role))" :disabled="busy" @click="start('pause', '暂缓任务')"><AppIcon name="pause" />暂缓任务</button><button role="menuitem" v-if="t.status === 'open' && !remoteProject" :disabled="busy" @click="start('edit', '修订任务定义')"><AppIcon name="file" />编辑任务定义</button><a role="menuitem" :href="href('/evolution', { q: t.definition.title })"><AppIcon name="evolution" />查看相关演进</a><hr /><button role="menuitem" v-if="t.status === 'open' && (!remoteProject || ['admin','executor'].includes(remoteProject.role))" :disabled="busy" class="danger" @click="start('cancel', '取消任务')"><AppIcon name="x" />取消任务</button><button role="menuitem" class="danger" :disabled="busy || deleteBlocked" :title="deleteBlocked ? '有正在执行或未核实停止的执行，暂不能删除' : undefined" @click="start('delete', '永久删除任务')"><AppIcon name="trash" />永久删除任务</button></MenuPopover>
          </div>
        </div>
      </header>
      <div class="wb-detail-layout">
      <div class="wb-detail-content">
      <nav class="wb-detail-tabs" aria-label="任务详情分区">
        <button v-for="[sid, title] in sections" :key="sid" :aria-current="section === sid ? 'page' : undefined" :class="{ selected: section === sid }" @click="selectSection(sid)">{{ title }}<span v-if="sid === 'checks' && t.definition.checks?.length" class="tiny-count">{{ t.definition.checks.length }}</span></button>
      </nav>
      <div class="task-detail-main">

      <section v-if="section === 'overview'" class="overview">
        <StepBar v-if="t.status === 'open' && !familyLed" :task="t" :blocked="headState.key === 'blocked'" />
        <DeliverySummary v-if="t.status === 'completed'" :key="'delivery-' + id" :snapshot="n" :objects="reviewObjects" :imported="importedArchive" />
        <section v-else class="wb-situation" :class="headState.key === 'blocked' ? 'blocked' : ['review','decision','integration'].includes(headState.key) ? 'review' : ''" aria-label="当前情况">
          <div class="wb-situation-top"><AppIcon :name="situationIcon" />{{ situationTitle }}</div>
          <template v-if="!n.summary.requires_task_decision"><details v-if="situationFact.length>220" class="wb-situation-more"><summary>{{ situationFact.slice(0,220) }}… 完整情况</summary><p>{{ situationFact }}</p></details><p v-else>{{ situationFact }}</p></template>
          <section v-if="n.summary.requires_task_decision && t.confirmation" class="task-decision">
          <p>{{ blockerText(t.confirmation.reason, false) }}</p>
          <details><summary>决定对象与版本</summary><p>
            仅针对定义修订 {{ t.confirmation.definition_revision }}、计划修订
            {{ t.confirmation.plan_revision
            }}<span v-if="t.confirmation.candidate">及候选 {{ t.confirmation.candidate.slice(0, 12) }}</span>。
          </p></details>
          <p v-if="decisionEvidence?.reason"><strong>本次方案</strong> {{ blockerText(decisionEvidence.reason, false) }}</p>
          <div v-if="decisionEvidence?.outputs?.length" class="actions">
            <button v-for="ref in decisionEvidence.outputs" :key="ref" @click="openArtifact(ref)">查看方案产物 {{ ref }}</button>
          </div>
          <p v-if="!mayJudge" class="secondary">等待具备确认权限的审核人员决定。</p>
          <div v-else class="actions">
            <button
              class="primary"
              @click="confirmation('approve', '批准当前版本')"
            >
              批准当前版本</button
            ><button @click="confirmation('adjust', '要求调整')">
              要求调整</button
            ><button @click="confirmation('reject', '否决方案')">
              否决方案
            </button>
          </div>
        </section>
          <p v-if="currentBlocker" class="wb-situation-extra">解除条件：{{ currentBlocker.recovery ? blockerText(currentBlocker.recovery, false) : '尚未记录' }}；恢复位置：{{ currentBlocker.resume_at ? label(currentBlocker.resume_at) : '尚未记录' }}</p>
          <div v-if="stuck && !currentBlocker" class="wb-situation-stuck" role="note"><p><strong>最近失败于 {{ stuck.step }}</strong><template v-if="stuck.summary"> · {{ stuck.summary.length > 200 ? stuck.summary.slice(0, 200) + '…' : stuck.summary }}</template></p><div class="actions"><button v-for="ref in stuck.outputs.slice(0, 1)" :key="ref" class="small" @click="openArtifact(ref)"><AppIcon :name="/\.log$/.test(ref) ? 'terminal' : 'file'" />{{ /\.log$/.test(ref) ? '查看失败日志' : '查看输出 ' + artifactName(ref) }}</button><a class="text-button" :href="href('/tasks/' + encodeURIComponent(id), { section: 'execution', attempt: stuck.id })">在执行诊断中查看</a></div></div>
          <a v-else-if="stuck" class="text-button wb-situation-link" :href="href('/tasks/' + encodeURIComponent(id), { section: 'execution', attempt: stuck.id })">在执行诊断中查看受阻的执行</a>
          <p v-if="reviewState" class="wb-situation-extra review-note-status">{{ reviewState }}</p>
          <div class="actions"><button v-if="n.summary.needs_history_review" @click="selectSection('execution')">查看相关历史资料</button><button v-if="showReviewEntry && (pendingReviewCheck!==null || reviewState)" :disabled="busy" @click="openTaskReview">{{ reviewState || pendingReviewCheck!==null?'查看人工意见与记录':'记录人工意见' }}</button></div>
          <details v-if="unownedRunning && !remoteProject"><summary>当前接手无法继续？</summary><p>当前标签页没有原责任凭据。若原执行确已停止，可按原规则核实并恢复。</p><button @click="start('resume','恢复无法继续的接手')">核实并恢复接手</button></details>

          <dl v-if="t.status==='open' && !n.summary.requires_task_decision" class="wb-situation-facts">
            <dt>{{ familyLed ? '父事项下一步' : '下一步' }}</dt><dd>{{ n.summary.needs_history_review ? '核实上述缺口后，按原有恢复门禁继续；具体未记录项可从相关资料核对' : !mayJudge && availableHumanChecks.length ? '等待有验收权限的人员处理当前检查' : presentation.next }}</dd>
            <dt>负责处理</dt><dd>{{ progress.owner || presentation.role }}</dd>
            <template v-if="progress.record"><dt>最近记录</dt><dd>{{ progress.record }}</dd></template>
            <template v-if="progress.contact && n.summary.running"><dt>最近联系</dt><dd>{{ progress.contact }}</dd></template>
          </dl>
          <QueueOwnership v-if="remoteProject && t.status === 'open' && !n.summary.running" :key="'queue-' + id" :task-id="id" :summary="n.summary" @changed="load(true)" />
          <div v-if="progress.quiet" class="wb-situation-note warn" role="note"><strong>最近上报不等于在线状态</strong><p>距离最近一次有效进展已有 {{ progress.quiet.minutes }} 分钟{{ progress.quiet.threshold ? '，超过本项约定的 ' + progress.quiet.threshold + ' 分钟' : '' }}。当前只能确认没有新进展；不能据此判断进程已经退出，也不能标记任务失败。</p></div>
          <div v-else-if="progress.long" class="wb-situation-note" role="note"><strong>已声明的长耗时工作</strong><p>执行者声明预计 {{ stamp(progress.long.until) }} 前完成；在此之前没有新进展不视为异常，也不代表执行者在线。</p></div>
        </section>
        <TaskChildren :key="id" :task-id="id" @family="family = $event" />
        <section v-if="t.status !== 'completed' && (implementation || t.historical)" class="wb-content-section" aria-label="实施结果">
          <h2>{{ implementation && !implementation.current ? '最近实施记录' : '实施结果' }}</h2>
          <p v-if="implementation?.summary" class="fact-full">{{ implementation.summary }}</p>
          <p v-else class="secondary">未记录可读的实施说明。</p>
        </section>
        <section v-if="t.status !== 'completed' && recordedChecks.length" class="wb-content-section" aria-label="检查结果">
          <h2>检查结果</h2>
          <article v-for="check in recordedChecks" :key="check.id" class="overview-review-check">
            <h3>{{ check.covers?.join('；') || check.id }} · {{ label(n.current_checks[check.id].result) }}</h3>
            <p v-if="n.current_checks[check.id].reason" class="fact-full">{{ n.current_checks[check.id].reason }}</p>
            <small>{{ evidenceSource(n.current_checks[check.id]) }} · <a :href="taskHref(id, 'checks', check.id)">查看检查记录</a></small>
          </article>
        </section>
        <section v-if="reviewChecks.length" id="overview-review" aria-label="当前人工验收"><ReviewPanel v-for="check in reviewChecks" :key="id + check.id + (t.candidate || '')" :snapshot="n" :check="check" :objects="reviewObjects" @saved="reviewSaved" @advanced="manualCheck = check.id" /></section>
        <TaskResults :key="'results-' + id" :snapshot="n" :objects="reviewObjects" :imported="importedArchive" :hide="reviewChecks.length ? reviewImageRefs : []" :only-other="t.status === 'completed'" />

        <details v-if="t.definition.type==='epic'" class="content-section" :open="epicDetailsOpen" @toggle="epicDetailsOpen=($event.target as HTMLDetailsElement).open"><summary>父级计划与整体验收详情</summary><EpicOverview v-if="epicDetailsOpen" :key="id" :id="id" @open-artifact="(ref,task)=>openArtifact(ref,task)" /></details>
        <details class="content-section task-description">
          <summary>完整任务说明与验收条目</summary>
          <p v-if="t.definition.intent">{{ t.definition.intent }}</p>
          <details v-if="t.definition.original_request && t.definition.original_request !== t.definition.intent" :open="!t.definition.intent && t.definition.original_request.length < 550">
            <summary>任务原文</summary>
            <MarkdownBody :id="t.definition.id" :body="t.definition.original_request" />
          </details>
          <dl class="facts">
            <div>
              <dt>交付目标</dt>
              <dd>{{ goalLabel(t.definition.goal) }}</dd>
            </div>
            <div>
              <dt>任务范围</dt>
              <dd><ScopeSummary :items="t.definition.scope" /></dd>
            </div>
            <div>
              <dt>明确不做</dt>
              <dd><ScopeSummary :items="t.definition.non_goals" empty="未额外声明" name="不做事项" /></dd>
            </div>
            <div>
              <dt>交付约束</dt>
              <dd>{{ [t.definition.target, t.definition.environment, ...(t.definition.risk || [])].filter(Boolean).join(' · ') || '按验收条件交付' }}</dd>
            </div>
          </dl>
          <h3>验收条件</h3>
          <ul>
            <li v-for="a in t.definition.acceptance">{{ a }}</li>
          </ul>
        </details>
        <details class="content-section"><summary>关系与历史关联</summary><TaskRelations :key="id" :id="id" :task="t" /></details>
        <details id="task-basis" tabindex="-1" class="content-section" :open="rawSection==='basis'"><summary>相关资料与规范</summary>
        <div class="section-heading"><h2>相关依据</h2><small v-if="basis">{{ basis.records.length }} 份可核对<template v-if="unresolvedBasisIDs.length"> · {{ unresolvedBasisIDs.length }} 项缺正文</template></small></div>
        <p v-if="!basis && !basisError" role="status" class="loading">
          正在读取任务关联依据…
        </p>
        <div v-if="basisError" class="alert" role="alert">无法读取任务关联依据。<button @click="load(true)">重试</button></div>
        <p v-if="basis?.health?.state === 'load_failed'" class="secondary">项目资料读取不完整，已保存的依据快照仍可核对。</p>
        <p v-if="basis && !basis.records.length && !unresolvedBasisIDs.length && !localBasis.length && basis.health?.state !== 'load_failed'" class="empty">
          本任务尚无已提供或显式引用的规范与决策。
        </p>
        <div v-if="basis?.records?.length" class="record-list task-basis-list"><article v-for="k in basis.records" :key="k.id" class="record-row"><span class="basis-icon"><AppIcon name="file" /></span><div>
          <h3><button class="basis-title-button" :aria-label="(basisRelations(k).includes('实际提供') ? '阅读实际提供版本' : explicitReference(k) ? '阅读当前显式引用' : '阅读待核实规范') + ' · ' + (k.title || k.id)" @click="showBasis(k)">{{ k.title || k.id }}</button></h3>
          <p v-if="k.summary || k.rationale">{{ k.summary || k.rationale }}</p>
          <small>{{ [label(k.kind), ...basisRelations(k), k.hash?.slice(0, 12)].filter(Boolean).join(' · ') }}</small>
        </div></article></div>
        <details v-if="localBasis.length">
          <summary>工作区中的规范依据（{{ localBasis.length }} 项）</summary>
          <ul class="secondary">
            <li v-for="ref in localBasis" :key="ref.id">{{ ref.source }} · 正文保存在执行工作区 · {{ ref.version }}</li>
          </ul>
        </details>
        <ul v-if="unresolvedBasisIDs.length" class="secondary">
          <li v-for="source in unresolvedBasisIDs" :key="source">{{ source }} · 显式引用，当前未取得正文版本</li>
        </ul>
        <LoadState
          :loading="readingLoading"
          :error="readingError"
          @retry="basisReader.retry"
        />
        <section v-if="reading" id="basis-reading" tabindex="-1" class="basis-reading">
          <div class="section-heading">
            <h2>{{ reading.title }}</h2>
            <button @click="closeBasis">关闭正文</button>
          </div>
          <p class="secondary">
            {{
              reading.supplied
                ? "此处是任务实际提供版本；当前资料可能已更新。"
                : reading.explicit
                  ? "此处为当前显式引用的资料，尚未向本任务执行者提供，不能当作已使用的依据。"
                  : "此处为影响当前任务的待核实规范，尚未向执行者提供，不能当作已使用的依据。"
            }}
          </p>
          <MarkdownBody :body="reading.body" :id="reading.id" :version="reading.hash" :context="reading.context_id" />
          <details>
            <summary>来源与版本</summary>
            <dl class="facts">
              <div>
                <dt>来源</dt>
                <dd>{{ reading.path }}</dd>
              </div>
              <div>
                <dt>
                  {{ reading.supplied ? "实际提供版本" : "当前来源版本" }}
                </dt>
                <dd>{{ reading.hash }}</dd>
              </div>
            </dl>
          </details>
        </section>
        </details>
        <details v-if="claimed" id="work-result" class="work-form" :open="workFormOpen" @toggle="workFormOpen = ($event.target as HTMLDetailsElement).open">
          <summary>填写实施工作结果</summary>
          <h2>提交本次工作结果</h2>
          <p>
            本次接手：{{ actor }} · {{ label(claimed.unit) }}。{{
              supplied
                ? "已提供当前工作所需依据，请在提交前阅读。"
                : "已接手；准确版本依据尚未读取成功。"
            }}
          </p>
          <LoadState
            :loading="contextLoading"
            :error="contextError"
            @retry="contextReader.retry"
          />
          <details v-if="supplied?.knowledge?.length">
            <summary>
              阅读已提供的准确版本依据（{{ supplied.knowledge.length }} 份）
            </summary>
            <section v-for="k in supplied.knowledge">
              <h3>{{ k.title }}</h3>
              <MarkdownBody :id="k.id" :body="k.body" />
            </section>
          </details>
          <p v-if="storageWarning" role="alert" class="alert">{{ storageWarning }}</p>
          <p v-if="pendingSubmission" role="status" class="alert">上次提交结果尚未确认。请原样重试；系统会使用同一请求，避免重复提交。</p>
          <form @submit.prevent="submit()"><fieldset :disabled="!!pendingSubmission" class="work-fields">
            <label
              >工作结论<select v-model="result.status">
                <option value="pass">本步骤完成</option>
                <option value="fail">未达到预期</option>
                <option value="blocked">环境阻塞</option>
              </select></label
            ><label
              >实际工作与结果<textarea
                v-model="result.summary"
                required
                rows="3"
              ></textarea></label
            ><label
              >附件{{ t.definition.deliverables?.length ? '（按约定提供）' : '（可选）' }}<input
                v-model="result.artifacts"
                placeholder="已存在的项目相对路径或 runtime: 引用" /></label
            ><p v-if="t.definition.deliverables?.length">约定文件 {{ t.definition.deliverables.join('、') }}</p>
            <details v-if="claimed.unit === 'execute' && result.status === 'pass'">
              <summary>补充规范处理情况</summary>
              <label>规范处理<select v-model="result.knowledge_status">
                <option value="unchanged">沿用现有规范</option>
                <option value="updated">已更新规范与决策</option>
              </select></label>
              <label>说明<textarea v-model="result.knowledge_reason"></textarea></label>
              <label v-if="result.knowledge_status === 'updated'">关联规范文件（可选）
                <input v-model="result.knowledge_artifacts" placeholder="项目相对路径，逗号分隔" />
              </label>
            </details>
            <label v-if="result.status === 'blocked'">恢复条件
              <textarea v-model="result.knowledge_reason"></textarea>
            </label>
            <label v-if="result.status === 'fail'"
              >失败类别（可选）<input v-model="result.failure_class" maxlength="128" placeholder="有实际失败证据时填写稳定类别，如 http:401" /></label
            ><label v-if="result.status === 'fail'"
              >需要回到哪个责任节点<select v-model="result.return_to">
                <option value="">保留当前范围并修复</option>
                <option
                  v-for="step in t.plan.steps
                    .slice(0, t.plan.steps.indexOf(t.current_step) + 1)
                    .filter((s: string) =>
                      ['define', 'analysis', 'execute'].includes(s),
                    )"
                  :value="step"
                >
                  {{ label(step) }}
                </option>
              </select></label
            >
            <section v-if="claimed.unit === 'delivery'" class="receipt-form">
              <h3>读取真实交付回执</h3>
              <p>
                先完成已授权的实际交付，再读取已有 JSON
                回执。系统仍会核对候选、目标与回执，不以按钮点击当作发布。
              </p>
              <label
                >真实交付回执引用<input
                  v-model="deliveryRef"
                  @input="receiptReader.clear"
                  placeholder="runtime:delivery-receipt.json" /></label
              ><button
                type="button"
                :disabled="!deliveryRef || receiptLoading"
                @click="readReceipt"
              >
                读取交付回执</button
              ><LoadState
                :loading="receiptLoading"
                :error="receiptError"
                @retry="receiptReader.retry"
              />
              <p v-if="receipt">
                回执：{{ receipt.receipt.run_id }} ·
                {{ label(receipt.receipt.status) }} ·
                {{ receipt.receipt.environment || receipt.receipt.target }}
              </p>
            </section>
            </fieldset><button
              class="primary"
              :disabled="
                busy ||
                (!pendingSubmission && (!supplied || contextLoading)) ||
                (!pendingSubmission && claimed.unit === 'delivery' &&
                  result.status === 'pass' &&
                  !receipt)
              "
            >
              {{ busy ? "正在提交…" : pendingSubmission ? "原样重试提交" : "提交并查看下一步" }}
            </button>
          </form>
          <details class="wb-disclosure">
            <summary>改用完整步骤结果 JSON 提交</summary>
            <p class="secondary">当前已领取工作的完整 StepResult；仍经过原协议、授权、候选和修订校验。</p>
            <label>完整步骤结果 JSON<textarea v-model="advancedResult" rows="8" spellcheck="false"></textarea></label>
            <button :disabled="busy" @click="submit(true)">{{ busy ? "正在提交…" : "提交完整步骤结果" }}</button>
          </details>
        </details>
        <details v-if="t.status !== 'open' || t.delivery || t.knowledge_outcome" id="delivery-result" class="source-detail delivery-detail">
          <summary>交付、规范处理与后续工作</summary>
          <p>
            实际结果：{{ label(t.result || "尚未交付") }}；{{
              t.completed ? "完成于 " + stamp(t.completed) : "尚无完成时间"
            }}
          </p>
          <p v-if="t.delivery">
            交付：{{ label(t.delivery.status) }} ·
            {{ t.delivery.environment || t.delivery.target
            }}<button
              v-if="t.delivery.artifact"
              @click="openArtifact(t.delivery.artifact)"
            >
              查看交付回执
            </button>
          </p>
          <p>
            {{
              t.knowledge_outcome
                ? label(t.knowledge_outcome.status) +
                  "：" +
                  t.knowledge_outcome.reason
                : "尚未记录规范处理结果。"
            }}
          </p>
        </details>
      </section>
      <section v-if="section === 'execution' || section === 'checks'">
        <section v-if="section === 'checks'" class="content-section check-list">
          <div class="wb-section-header"><h2>验收与问题</h2><span class="muted">{{ n.summary.checks?.passed || 0 }} / {{ t.definition.checks?.length || 0 }} 项通过 · {{ checkStatusSummary(n.summary.checks) }}</span></div>
          <p class="secondary">{{ t.candidate ? '当前候选 ' + t.candidate.slice(0, 12) : '候选尚未生成' }} · {{ goalLabel(t.definition.goal) }}</p>
          <details v-if="t.definition.acceptance?.length" class="check-criteria">
            <summary>完整验收条件（{{ t.definition.acceptance.length }} 项）</summary>
            <ol><li v-for="(criterion, index) in t.definition.acceptance" :key="index">{{ criterion }}</li></ol>
          </details>
          <article v-for="c in t.definition.checks" :id="'task-check-' + c.id" :key="c.id" class="check">
            <ReviewPanel v-if="mayJudge && n.check_access?.[c.id]?.can_review" :key="'review-' + id + c.id + (t.candidate || '')" :snapshot="n" :check="c" :objects="reviewObjects" @saved="reviewSaved" @advanced="manualCheck = c.id" />
            <div class="check-row">
              <span class="check-state-icon" :class="checkState(c.id)"><AppIcon :name="checkState(c.id) === 'pass' ? 'check' : checkState(c.id) === 'fail' ? 'warning' : checkState(c.id) === 'running' ? 'play' : 'clock'" /></span>
              <div class="check-main">
                <div class="check-heading"><h3>{{ c.id }} · {{ label(c.kind) }}</h3><span class="status" :class="checkState(c.id)">{{ checkReading(n, c).label }}</span></div>
                <p class="check-tags">{{ label(n.check_access?.[c.id]?.effective_method || c.method) }} · {{ c.required ? '必需检查' : '辅助检查' }}<template v-if="coverageLabel(c)"> · 覆盖 {{ coverageLabel(c) }}</template></p>
                <p v-if="n.current_runs[c.id]">{{ n.current_runs[c.id].executors.join('、') }} {{ n.current_runs[c.id].started ? '已启动检查。' : n.current_runs[c.id].reported ? '已上报本项检查执行状态。' : '已领取本项检查，等待执行上报。' }}</p>
                <p v-else-if="n.current_checks[c.id]?.reason" class="check-description">{{ blockerText(n.current_checks[c.id].reason) }}</p>
                <p v-else-if="historyForCheck(c.id).length" class="secondary">有历史结果，当前版本仍待验收。</p>
              </div>
              <div class="actions check-actions">
                <button class="ghost" @click="detailCheck = c">检查详情</button>
                <button :aria-expanded="!!checkOpen[c.id]" :aria-controls="'check-evidence-' + c.id" @click="checkOpen[c.id] = !checkOpen[c.id]">{{ checkOpen[c.id] ? '收起检查依据' : checkState(c.id) === 'pass' ? '查看证据' : '查看检查依据与证据' }}</button>
                <button v-if="!remoteProject && c.command?.length && checkState(c.id) !== 'pass'" :disabled="busy || !canCheck(c)" @click="start('verify', '运行检查', { extra: { unit: c.id } })">运行真实检查</button>
              </div>
            </div>
            <div v-if="checkOpen[c.id]" :id="'check-evidence-' + c.id" class="check-detail">
              <h4>本项覆盖条件</h4>
              <ul v-if="c.covers?.length"><li v-for="a in c.covers" :key="a">{{ a }}</li></ul>
              <p v-else class="secondary">定义未声明覆盖条件。</p>
              <p v-if="t.definition.type === 'epic' && c.affected_tasks?.length">关联排查任务 <template v-for="affected in c.affected_tasks" :key="affected"><a :href="taskHref(affected)">{{ affected }}</a> </template></p>
              <pre v-if="c.command?.length">{{ c.command.join(" ") }}</pre>
              <section v-if="n.current_checks[c.id]" class="evidence">
                <h4>当前检查结果 · {{ label(n.current_checks[c.id].result) }}</h4>
                <p>{{ stamp(n.current_checks[c.id].at) }} · {{ evidenceSource(n.current_checks[c.id]) }}<template v-if="n.current_checks[c.id].executor"> · {{ n.current_checks[c.id].executor }}</template></p>
                <p v-if="n.current_checks[c.id].reused_from" class="secondary">复用已有记录，已按当前定义核对。</p>
                <p v-if="n.current_checks[c.id].reason" class="fact-full">{{ n.current_checks[c.id].reason }}</p>
                <details class="source-detail">
                  <summary>检查版本与来源</summary>
                  <p>候选 {{ n.current_checks[c.id].candidate }} · 定义 {{ n.current_checks[c.id].acceptance_revision }} / 计划 {{ n.current_checks[c.id].plan_revision }}</p>
                  <p>{{ n.current_checks[c.id].source }}<template v-if="n.current_checks[c.id].exit_code != null"> · 退出码 {{ n.current_checks[c.id].exit_code }}</template></p>
                </details>
                <div v-if="n.current_checks[c.id].artifacts?.length || n.current_checks[c.id].diagnostic_artifacts?.length" class="actions">
                  <button v-for="ref in n.current_checks[c.id].artifacts" :key="ref" @click="openArtifact(ref)">查看证据 {{ artifactName(ref) }}</button>
                  <button v-for="ref in n.current_checks[c.id].diagnostic_artifacts" :key="ref" @click="openArtifact(ref)">查看诊断材料 {{ artifactName(ref) }}</button>
                </div>
              </section>
              <details v-if="historyForCheck(c.id).length" class="source-detail" :open="!n.current_checks[c.id] && route.query.get('check') === c.id">
                <summary>历史结果（{{ historyForCheck(c.id).length }} 项）</summary>
                <section v-for="(item, i) in historyForCheck(c.id)" :key="i" class="evidence">
                  <h4>{{ label(item.result) }} · {{ stamp(item.at) }}</h4>
                  <p class="fact-full">{{ blockerText(item.reason, false) }}</p>
                  <p class="secondary">归档候选 {{ item.candidate?.slice(0, 12) || '未知' }} · 定义 {{ item.acceptance_revision }} / 计划 {{ item.plan_revision }}</p>
                  <p v-if="!remoteProject && item.artifacts?.length" class="secondary">本地旧记录保存了路径引用。读取时显示该路径现存内容，原始字节需核对。</p>
                  <div class="actions"><button v-for="ref in item.artifacts" :key="ref" @click="openArtifact(ref)">{{ remoteProject ? '查看历史原件' : '读取现存引用' }} {{ ref }}</button></div>
                </section>
              </details>
              <p v-if="checkReason(c)" class="secondary">{{ checkReason(c) }}</p>
              <div class="actions">
                <button v-if="mayJudge && n.check_access?.[c.id]?.can_review" class="primary" :disabled="busy" @click="manualCheck = c.id">进行人工验收</button>
                <button v-else-if="mayJudge && t.status === 'open' && n.check_access?.[c.id]?.effective_method === 'human' && checkState(c.id) !== 'pass'" :disabled="busy" @click="manualCheck = c.id">记录人工意见</button>
                <button v-if="!remoteProject && checkState(c.id) === 'pass' && c.command?.length" :disabled="busy || !canCheck(c)" @click="start('verify', '运行检查', { extra: { unit: c.id } })">重新运行检查</button>
                <button v-if="remoteProject" class="inline-link" @click="showCheckFindings(c.id)">查看本项关联发现</button>
              </div>
            </div>
          </article>
        </section>
        <div v-if="remoteProject && section === 'checks'" id="task-findings" tabindex="-1"><FindingPanel :task-id="id" @artifact="openArtifact" /></div>
        <TaskDiagnostics v-if="section === 'execution'" :key="'diag-' + id" :snapshot="n" :state-label="headState.label" :names="artifactNames" :remote="!!remoteProject" @artifact="openArtifact" />
        <section v-if="section === 'execution' && currentResponsibilities.length" class="wb-content-section" aria-labelledby="responsibility-title">
          <div class="wb-section-header"><h2 id="responsibility-title">当前责任</h2><span class="muted">执行者上报记录，不代表本机进程实时在线</span></div>
          <div class="wb-subtask-list"><div v-for="item in currentResponsibilities" :key="item.id" class="wb-subtask-item"><span class="avatar gray">A</span><div class="wb-subtask-text"><strong class="wb-resp-name">{{ n.collaboration?.find((c: any) => c.attempt === item.id)?.alias || item.executor }}</strong><p class="wb-subtask-sub">{{ label(item.unit) }}<template v-if="item.last_contact"> · 最近上报 {{ relativeShort(item.last_contact) }}</template></p></div></div></div>
        </section>
        <ImportedHistory v-if="section === 'execution' && remoteProject && t.legacy?.authority_import" :key="props.id + t.legacy.authority_import" :task-id="props.id" :import-id="t.legacy.authority_import" :task="t" @artifact="openArtifact" />
      </section>
      </div>
      </div>
      <aside class="wb-detail-aside" aria-label="任务信息">
        <section class="wb-aside-block"><h3 class="wb-aside-title">任务信息</h3>
          <div class="wb-aside-fact"><span>当前阶段</span><strong>{{ t.status === 'open' ? label(t.current_step) : label(t.status) }}</strong></div>
          <div class="wb-aside-fact"><span>{{ t.status !== 'open' ? '记录状态' : familyLed ? '父事项责任方' : '下一责任方' }}</span><strong>{{ t.status === 'open' ? workOwner(n.summary, mayJudge, currentUser).name : headState.label }}</strong></div>
          <div class="wb-aside-fact"><span>任务类型</span><strong>{{ label(t.definition.type) }}</strong></div>
          <div class="wb-aside-fact"><span>交付目标</span><strong>{{ goalLabel(t.definition.goal) }}</strong></div>
          <div class="wb-aside-fact"><span>检查</span><strong>{{ checkStatusSummary(n.summary.checks) }}</strong></div>
          <div class="wb-aside-fact"><span>项目</span><strong>{{ remoteProject?.name || info.project?.split('/').filter(Boolean).pop() || '本地项目' }}</strong></div>
          <button class="wb-aside-link" @click="copyTaskInfo"><AppIcon name="copy" />复制任务信息</button>
        </section>
        <section class="wb-aside-block"><h3 class="wb-aside-title">关联规范</h3>
          <template v-if="basis?.records?.length"><button v-for="k in basis.records.slice(0, 5)" :key="k.id" class="wb-aside-link" @click="openBasisFromAside(k)"><AppIcon name="book" /><span>{{ k.title || k.id }}</span></button><button v-if="basis.records.length > 5" class="wb-aside-link" @click="openBasisFromAside()"><AppIcon name="book" /><span>全部 {{ basis.records.length }} 份依据</span></button></template>
          <button v-if="localBasis.length" class="wb-aside-link" @click="openBasisFromAside()"><AppIcon name="book" /><span>工作区依据 {{ localBasis.length }} 项</span></button>
          <p v-else-if="basisError" class="wb-aside-empty">关联依据读取失败</p>
          <p v-else-if="basis && !basis.records?.length" class="wb-aside-empty">暂无显式关联</p>
          <p v-else class="wb-aside-empty">正在读取…</p>
        </section>
        <section class="wb-aside-block"><h3 class="wb-aside-title">相关资料</h3>
          <a v-if="remoteProject" class="wb-aside-link" :href="href('/materials', { task: id })"><AppIcon name="folder" />查看任务全部资料</a>
          <a class="wb-aside-link" :href="taskHref(id, 'technical')"><AppIcon name="terminal" />版本与技术详情</a>
          <a v-if="parentId" class="wb-aside-link" :href="taskHref(parentId)"><AppIcon name="branch" />所属主要事项</a>
        </section>
      </aside>
      </div>
    </template>
    <CheckDetail v-if="detailCheck && n" :snapshot="n" :check="detailCheck" :objects="reviewObjects" @close="detailCheck = null" />
    <MaterialPreview v-if="preview" :material="preview.material" :relations="preview.relations" :checks="n?.current_checks" :current-candidate="t?.candidate" :task-title="preview.material.task === id ? t?.definition?.title : preview.material.task" @close="preview = null" />
    <ActionDialog
      v-if="op"
      :title="op.title"
      :description="
        op.objectTitle +
        ' · 定义 ' +
        op.revision +
        ' / 计划 ' +
        op.plan_revision
      "
      :busy="busy"
      :submit-disabled="confirmationStale"
      :error="opError"
      :danger="
        ['delete', 'cancel', 'confirm'].includes(op.action) &&
        op.extra?.decision !== 'approve'
      "
      @close="op = null"
      @submit="perform"
    >
      <section v-if="confirmationStale" class="source-detail">
        <p>已保留操作理由。请重新核对确认对象后再提交。</p>
        <button type="button" :disabled="loading" @click="reloadConfirmation">读取最新确认对象</button>
        <template v-if="refreshedConfirmation">
          <p>{{ blockerText(refreshedConfirmation.reason, false) }}</p>
          <p>定义 {{ refreshedConfirmation.definition_revision }} · 计划 {{ refreshedConfirmation.plan_revision }}<template v-if="refreshedConfirmation.candidate"> · 候选 {{ refreshedConfirmation.candidate }}</template></p>
          <button type="button" @click="useRefreshedConfirmation">我已核对以上版本</button>
        </template>
        <p v-else-if="confirmationRefreshed">当前任务暂无同类确认对象。待新方案提交后可以在这里重新读取。</p>
      </section>
      <p v-if="remoteProject && ['pause','cancel'].includes(op.action)" class="info-strip">撤销后续协作结果提交与推进，并请求外部执行者停工。宿主进程状态未知。</p>
      <label v-if="!remoteProject && ['claim', 'confirm', 'review_failure'].includes(op.action)"
        >执行者 / 决策者<input v-model="actor" required
      /></label>
      <label v-if="!['claim', 'verify', 'resume', 'review_failure', 'prepare_integration'].includes(op.action)"
        >操作理由<textarea v-model="reason" required rows="3"></textarea>
      </label>
      <template v-if="op.action === 'review_failure'">
        <p>复核不会清除失败次数，也不会替代必需检查。请保留真实依据和下一条验证假设。</p>
        <label>已尝试<textarea v-model="failureReview.tried" required rows="2"></textarea></label>
        <label>已排除<textarea v-model="failureReview.excluded" required rows="2"></textarea></label>
        <label>根因假设<textarea v-model="failureReview.hypothesis" required rows="2"></textarea></label>
        <label>下一动作<textarea v-model="failureReview.next" required rows="2"></textarea></label>
        <label>解除条件<textarea v-model="failureReview.recovery" required rows="2"></textarea></label>
        <label>复核证据（逗号分隔）<input v-model="failureReview.evidence" required placeholder="项目相对路径或 runtime: 引用" /></label>
      </template>
      <label v-if="op.action === 'resume' && unownedRunning" class="inline"
        ><input type="checkbox" v-model="ack" required />我已核实此任务的所有旧执行进程均已停止</label
      >
      <label v-if="op.action === 'resume'"
        >恢复位置<select v-model="resumeTarget">
          <option value="">保留当前责任节点</option>
          <option
            v-for="step in t.plan.steps.slice(
              0,
              t.plan.steps.indexOf(t.current_step),
            )"
            :value="step"
          >
            回到{{ label(step) }}
          </option>
        </select></label
      >
      <p v-if="op.action === 'resume'" class="secondary">
        恢复会结束此任务所有正在执行或中断的旧接手；请先确认它们都已停止。保留当前节点或回到选择的更早责任，仍由后端检查进程状态。
      </p>
      <label v-if="op.action === 'edit'"
        >任务定义 JSON<textarea
          v-model="edit"
          rows="14"
          spellcheck="false"
        ></textarea>
      </label>
      <p v-if="op.action === 'delete'" class="failure">
        将永久删除此任务及其项目记录，不能撤销。
      </p>
      <p v-if="op.action === 'verify'">
        运行已配置的真实命令，结果绑定当前候选和修订。
      </p>
      <p v-if="op.action === 'prepare_integration'">核对必要叶子的验收和内容纳入，并记录当前父候选；准备后仍需运行实际整体验收检查。</p>
    </ActionDialog>
    <ManualReviewDialog v-if="manualCheck !== null" :key="id + ':' + manualCheck" :task-id="id" :check-id="manualCheck" @select-check="manualCheck = $event" @close="closeReview" @saved="reviewSaved" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from "vue";
import { api, ApiError, info, notices, params, refreshRevision } from "../api";
import {
  route,
  setQuery,
  taskHref,
  knowledgeHref,
  back,
  navigate,
  href,
} from "../router";
import { useResource } from "../resource";
import { useObjectReader } from "../objectReader";
import {
  label,
  stamp,
  relativeStamp,
  schedule,
  reviewNoteStatus,
  nextAction,
  goalLabel,
  blockerText,
} from "../display";
import LoadState from "../components/LoadState.vue";
import ActionDialog from "../components/ActionDialog.vue";
import ManualReviewDialog from "../components/ManualReviewDialog.vue";
import MarkdownBody from "../components/MarkdownBody.vue";
import AppIcon from "../components/AppIcon.vue";
import ScopeSummary from "../components/ScopeSummary.vue";
import CopyButton from "../components/CopyButton.vue";
import EpicOverview from "../components/EpicOverview.vue";
import { pendingReviewForTask, preservePriorWork } from "../reviewDraft";
const props = defineProps<{ id: string }>();
const r = useResource<any>(null),
  { data: n, loading, error, last } = r;
const t = computed(() => n.value?.task),
  rawSection = computed(() => route.value.query.get("section") || "overview"),
  section = computed(() => rawSection.value === "technical" ? "execution" : rawSection.value);
const sections = [
  ["overview", "概览"],
  ["checks", "检查与验收"],
  ["execution", "执行过程"],
  ["basis", "规范与决策"],
];
const basis = ref<any>(null),
  events = ref<any[]>([]),
  eventsLoaded = ref(false),
  basisError = ref(""),
  basisLoading = ref(false);
const attachments = useObjectReader<any>(),
  basisReader = useObjectReader<any>(),
  contextReader = useObjectReader<any>();
const {
  data: artifact,
  error: artifactError,
  loading: artifactLoading,
} = attachments;
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
  actor = ref(sessionStorage.getItem("ael-actor") || "dashboard-user"),
  reason = ref(""),
  ack = ref(false),
  resumeTarget = ref(""),
  edit = ref("");
const manualCheck = ref<string | null>(null);
const pendingReviewCheck = ref<string | null>(null);
const workFormOpen = ref(false);
const taskMenu = ref<HTMLDetailsElement>();
const availableHumanChecks = computed(() =>
  (t.value?.definition.checks || []).filter((c: any) => n.value?.check_access?.[c.id]?.can_review),
);
const showReviewEntry = computed(() => t.value?.definition.type !== "epic" || !!availableHumanChecks.value.length || !!n.value?.summary?.human_review_count);
const claimed = ref<any>(null),
  result = ref({
    status: "pass",
    summary: "",
    artifacts: "",
    knowledge_status: "unchanged",
    knowledge_reason: "",
    knowledge_artifacts: "",
    return_to: "",
  });
const advancedResult = ref('{"status":"pass","summary":"","artifacts":[]}'),
  deliveryRef = ref("");
const pendingSubmission = ref<any>(null), storageWarning = ref(""), technicalOpen = ref(false);
const emptyResult = () => ({ status: "pass", summary: "", artifacts: "", knowledge_status: "unchanged", knowledge_reason: "", knowledge_artifacts: "", return_to: "" });
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
const receiptReader = useObjectReader<any>();
const {
  data: receipt,
  error: receiptError,
  loading: receiptLoading,
} = receiptReader;
const checkOpen = ref<Record<string, boolean>>({});
let openedCheckKey = "";
let timer: ReturnType<typeof setInterval>;
async function extras() {
  const id = props.id;
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
  if (section.value === "execution" && (technicalOpen.value || rawSection.value === "technical")) {
    const v = await api("/api/events?" + params({ id }));
    if (id === props.id) {
      events.value = v;
      eventsLoaded.value = true;
    }
  }
}
async function load(quiet = false) {
  const id = props.id;
  await r.load(() => api("/api/task?" + params({ id })), quiet);
  if (id !== props.id) return;
  await restoreClaim(id);
  pendingReviewCheck.value = pendingReviewForTask(info.value.project || "", id);
  const requestedReview = route.value.query.get("review");
  if (requestedReview && n.value?.task?.definition?.id === id) {
    if (n.value.check_access?.[requestedReview]?.can_review || requestedReview === pendingReviewCheck.value) manualCheck.value = requestedReview;
    setQuery({ review: undefined });
  }
  const requestedCheck = route.value.query.get("check");
  const checkKey = id + ":" + requestedCheck;
  if (section.value === "checks" && requestedCheck && openedCheckKey !== checkKey && n.value?.task?.definition?.checks?.some((check: any) => check.id === requestedCheck)) {
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
    events.value = [];
    eventsLoaded.value = false;
    attachments.clear();
    basisReader.clear();
    contextReader.clear();
    receiptReader.clear();
    claimed.value = null;
    pendingSubmission.value = null;
    result.value = emptyResult();
    technicalOpen.value = false;
    manualCheck.value = null;
    pendingReviewCheck.value = null;
    checkOpen.value = {};
    openedCheckKey = "";
    workFormOpen.value = false;
    load();
  },
);
watch(section, () => {
  attachments.clear();
  basisReader.clear();
  load(true);
});
watch(rawSection, () => { if (rawSection.value === "technical") { technicalOpen.value = true; void load(true); } });
watch(refreshRevision, () => load());
watch(() => info.value.project, (project) => { pendingReviewCheck.value = pendingReviewForTask(project || "", props.id); });
onMounted(() => {
  if (rawSection.value === "technical") technicalOpen.value = true;
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
const primaryKind = computed(() => {
  if (pendingReviewCheck.value !== null) return "review_pending";
  if (claimed.value) return "submit";
  if (availableHumanChecks.value.length) return "review";
  if (recoverable.value) return "resume";
  if (t.value?.confirmation) return "decision";
  if (t.value?.status === "completed") return "delivery";
  if (runnable.value) return "claim";
  if (n.value?.board === "active") return "execution";
  if (t.value?.current_step === "verify" && t.value?.status === "open") return "checks";
  return "";
});
const primaryLabels: Record<string, string> = { review: "开始人工验收", review_pending: "查看待确认的验收", resume: "恢复任务", decision: "审阅当前方案", claim: "接手当前工作", checks: "查看验收与运行检查", submit: "填写实施结果", execution: "查看当前执行", delivery: "查看交付结果" };
const bannerTitle = computed(() => {
  if (t.value?.status === "completed") return "已达到本次交付目标";
  if (t.value?.status === "cancelled") return "任务已取消，保留已有结果";
  if (n.value?.next_action === "wait_children") return "等待必需子任务";
  if (n.value?.next_action === "wait_integration") return "子任务已完成，等待整体验收";
  if (t.value?.paused || t.value?.blocker) return t.value?.blocker ? "工作受到阻塞" : "任务已暂缓";
  if (t.value?.confirmation) return "这一步需要你的决定";
  if (availableHumanChecks.value.length) return "还有人工验收需要你处理";
  if (n.value?.next_action === "repair") return "验收未通过，需要修复";
  if (n.value?.board === "active") return "当前工作正在执行";
  if (runnable.value) return "工作已就绪，等待接手";
  return "当前工作状态";
});
const bannerSummary = computed(() => availableHumanChecks.value.length
  ? `${n.value.summary.checks.required_passed} / ${n.value.summary.checks.required} 项必要检查通过，等待人工验收。`
  : label(n.value?.summary?.last_result));
const bannerReasons = computed(() => (n.value?.blocking_reasons || []).filter((reason: string) =>
  reason !== t.value?.blocker?.reason && (t.value?.definition?.type !== "epic" || (!reason.startsWith("required child ") && reason !== "required leaves completed; parent integration remains to be accepted"))));
const acceptanceState = computed(() => {
  const checks = n.value?.summary?.checks;
  if (!checks?.required) return "尚无必要检查";
  if (checks.blocked) return `${checks.blocked} 项受阻`;
  if (availableHumanChecks.value.length) return `${availableHumanChecks.value.length} 项待人工验收`;
  if (t.value?.candidate && checks.required_passed === checks.required) return "必要检查已通过";
  return "验证进行中";
});
const reviewState = computed(() => reviewNoteStatus(n.value?.summary));
function primaryAction() {
  if (primaryKind.value === "review") manualCheck.value = availableHumanChecks.value.length === 1 ? availableHumanChecks.value[0].id : "";
  else if (primaryKind.value === "review_pending") manualCheck.value = pendingReviewCheck.value;
  else if (primaryKind.value === "resume") start("resume", "恢复任务");
  else if (primaryKind.value === "decision") setQuery({ section: "basis" });
  else if (primaryKind.value === "claim") start("claim", "接手当前工作");
  else if (primaryKind.value === "checks") setQuery({ section: "checks" });
  else if (primaryKind.value === "submit") void showWorkForm();
  else if (primaryKind.value === "execution") setQuery({ section: "execution" });
  else if (primaryKind.value === "delivery") void showDelivery();
}
function openTaskReview() {
  if (busy.value) return;
  workFormOpen.value = false;
  manualCheck.value = pendingReviewCheck.value !== null ? pendingReviewCheck.value : availableHumanChecks.value.length === 1 ? availableHumanChecks.value[0].id : "";
}
function closeReview() { manualCheck.value = null; pendingReviewCheck.value = pendingReviewForTask(info.value.project || "", props.id); }
function reviewSaved() { pendingReviewCheck.value = pendingReviewForTask(info.value.project || "", props.id); void load(true); }
function technicalToggle(event: Event) {
  technicalOpen.value = (event.target as HTMLDetailsElement).open;
  if (technicalOpen.value && !eventsLoaded.value) void load(true);
}
async function showWorkForm() {
  setQuery({ section: "overview" });
  await nextTick();
  workFormOpen.value = true;
  await nextTick();
  document.getElementById("work-result")?.scrollIntoView({ block: "start" });
}
async function showDelivery() {
  setQuery({ section: "overview" });
  await nextTick();
  const detail = document.getElementById("delivery-result") as HTMLDetailsElement | null;
  if (detail) { detail.open = true; detail.scrollIntoView({ block: "start" }); }
}
function stageDone(step: string) {
  if (step === "verify") return !!n.value?.summary?.checks?.required && n.value.summary.checks.required_passed === n.value.summary.checks.required;
  return !!t.value?.attempts?.some((a: any) => a.unit === step && a.status === "pass" && a.definition_revision === n.value.definition_revision && a.plan_revision === n.value.plan_revision);
}
const work = computed(() => {
  const attempts = (t.value?.attempts || []).filter((a: any) => a.ended || a.status === "running" || a.status === "interrupted");
  const approvals = (t.value?.approvals || []).map((a: any, index: number) => ({
    id: "approval:" + index + ":" + a.at,
    display_unit: ({ plan: "方案决定", revision: "修订决定", delivery: "交付决定" } as Record<string, string>)[a.kind] || "决定",
    display_status: ({ approve: "已批准", adjust: "要求调整", reject: "已否决" } as Record<string, string>)[a.decision] || a.decision,
    status: a.decision === "approve" ? "pass" : a.decision === "reject" ? "rejected" : "confirmation",
    executor: a.actor, reason: a.reason, started: a.at, ended: a.at, outputs: [],
  }));
  const activities = (n.value?.activity || []).filter((event: any) => event.kind !== "confirmation" || event.historical || !(t.value?.approvals || []).length).map((event: any) => ({
    id: "event:" + event.id,
    display_unit: ({ paused: "任务暂缓", resumed: "任务恢复", review_resumed: "人工检查恢复", cancelled: "任务取消", definition_revised: "任务定义修订", confirmation: "历史决定记录" } as Record<string, string>)[event.kind] || "任务状态变化",
    display_status: event.kind === "confirmation" ? "详情未保留" : event.historical ? "历史记录" : "已记录", status: "", executor: "",
    reason: event.kind === "confirmation" ? "历史决定不代表当前授权。" : event.summary ? blockerText(event.summary) : (event.kind === "cancelled" && t.value?.status === "cancelled" ? t.value.result : ""),
    started: event.at, ended: event.at, outputs: [],
  }));
  if (t.value?.historical && t.value.status === "cancelled" && t.value.completed && !(n.value?.activity || []).some((event: any) => event.kind === "cancelled")) {
    activities.push({ id: "historical:cancelled", display_unit: "历史任务取消", display_status: "已记录", status: "", executor: "", reason: t.value.result || "", started: t.value.completed, ended: t.value.completed, outputs: [] });
  }
  return [...attempts, ...approvals, ...activities].sort((a: any, b: any) => Date.parse(b.ended || b.started) - Date.parse(a.ended || a.started));
});
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
function explicitReference(k: any) {
  return (
    t.value?.definition.knowledge?.includes(k.id) || k.tasks?.includes(props.id)
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
    candidate: t.value.candidate,
    plan_revision: n.value.plan_revision,
    ...extra,
  };
  opError.value = "";
  reason.value = "";
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
function startFromMenu(action: string, title: string) {
  taskMenu.value?.removeAttribute("open");
  taskMenu.value?.querySelector("summary")?.focus();
  start(action, title);
}
async function perform() {
  if (busy.value || !op.value) return;
  busy.value = true;
  opError.value = "";
  const current = { ...op.value };
  try {
    let extra = current.extra || {};
    if (current.action === "edit")
      extra = { definition: JSON.parse(edit.value) };
    if (current.action === "resume") extra = { unit: resumeTarget.value };
    const v = await api("/api/action", {
      action: current.action,
      id: current.id,
      executor: actor.value,
      reason: reason.value,
      ack_stopped: ack.value,
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
    if (op.value) opError.value = String(e);
    else error.value = String(e);
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
    if (attempt.unit === "execute")
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
async function openArtifact(ref: string) {
  const id = props.id;
  await attachments.read(async () => {
    if (/\.(png|jpe?g)$/i.test(ref)) {
      const blob = await api(
        "/api/artifact?" + params({ task: id, ref, format: "image" }),
        undefined,
        "blob",
      );
      const image = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = reject;
        reader.readAsDataURL(blob);
      });
      return { ref, image };
    } else
      return {
        ref,
        body: await api(
          "/api/artifact?" + params({ task: id, ref }),
          undefined,
          true,
        ),
      };
  });
}
async function readBasis(k: any) {
  const b = t.value.applicable_knowledge?.find((b: any) => b.id === k.id);
  await basisReader.read(async () => {
    const record = await api(
      "/api/knowledge-record?" +
        params({ id: k.id, version: b?.version || "" }),
    );
    return {
      ...record,
      supplied: !!b?.version,
      explicit: explicitReference(k),
    };
  });
}
async function showBasis(k: any) {
  setQuery({ section: "basis" });
  await nextTick();
  await readBasis(k);
  await nextTick();
  document.getElementById("basis-reading")?.scrollIntoView({ block: "start" });
}
function confirmation(decision: string, title: string) {
  const c = t.value.confirmation;
  start("confirm", title, {
    extra: {
      kind: c.kind,
      revision: c.definition_revision,
      plan_revision: c.plan_revision,
      candidate: c.candidate,
      decision,
    },
  });
}
function priorCheck(c: any) {
  return [...(t.value?.evidence_history || []), ...(t.value?.evidence || [])]
    .filter((e: any) => e.check === c.id)
    .sort((a: any, b: any) => Date.parse(b.at) - Date.parse(a.at))[0];
}
function canCheck(c: any) {
  return (
    t.value.status === "open" && !t.value.paused && !t.value.confirmation &&
    t.value.current_step === "verify" &&
    ["continue", "repair", "wait_executor"].includes(n.value.next_action) &&
    !t.value.attempts?.some(
      (a: any) =>
        (a.unit === c.id || a.unit === "check:" + c.id) &&
        a.status === "running",
    )
  );
}
function checkReason(c: any) {
  if (n.value?.check_access?.[c.id]?.effective_method === "human" && !n.value?.check_access?.[c.id]?.can_review)
    return "可先记录人工意见，候选和检查条件满足后再正式确认。";
  if (!canCheck(c)) return "当前未到此检查的可执行节点，或检查已在运行。";
  if (!c.command?.length && n.value?.check_access?.[c.id]?.effective_method !== "human")
    return "需要具备对应能力的执行者提交真实输入与报告。";
  return "";
}
</script>
<template>
  <div class="detail-page detail" aria-label="任务详情">
    <button class="back-link" @click="back('/tasks')">← 返回上一页</button>
    <LoadState :loading="loading" :error="error" :last="last" @retry="load()" />
    <template v-if="n && t">
      <p v-if="storageWarning && !claimed" class="alert" role="alert">{{ storageWarning }}</p>
      <header class="object-header">
        <div>
          <p class="eyebrow">{{ t.definition.id }} · {{ label(t.definition.type) }}</p>
          <h1>{{ t.definition.title }}</h1>
          <p class="inline">
            <span class="status" :class="[n.board, { confirmation: availableHumanChecks.length || n.next_action === 'wait_environment' }]">{{
              schedule(n.summary)
            }}</span
            ><span v-if="reviewState" class="status confirmation">{{ reviewState }}</span><span>{{ label(n.current_step) }}</span><span>定义修订 {{ n.definition_revision }}</span
            ><span v-if="n.summary.activity_at" :title="stamp(n.summary.activity_at)">{{ relativeStamp(n.summary.activity_at) }}</span>
          </p>
        </div>
        <div class="task-heading-actions"><button v-if="showReviewEntry && t.status === 'open' && !['review', 'review_pending'].includes(primaryKind)" class="primary" :disabled="busy" @click="openTaskReview">{{ reviewState ? '查看验收记录' : '开始人工验收' }}</button><button v-if="t.status !== 'open' && n.summary.human_review_count" :disabled="busy" @click="openTaskReview">查看验收记录</button><button v-if="primaryKind" :class="{ primary: ['review', 'review_pending'].includes(primaryKind) }" :disabled="busy" @click="primaryAction">{{ primaryKind === 'claim' ? '接手工作' : primaryKind === 'checks' ? '查看检查' : primaryKind === 'review' && reviewState ? '查看验收记录' : primaryLabels[primaryKind] }}</button><details ref="taskMenu" class="task-more"><summary aria-label="更多任务操作" title="更多任务操作">···</summary><div class="task-more-menu"><button v-if="t.status === 'open' && !t.paused" :disabled="busy" @click="startFromMenu('pause', '暂缓任务')">暂缓</button><button v-if="t.status === 'open'" :disabled="busy" @click="startFromMenu('edit', '修订任务定义')">编辑任务定义</button><button v-if="t.status === 'open'" :disabled="busy" class="danger-text" @click="startFromMenu('cancel', '取消任务')">取消任务</button><a :href="href('/evolution', { q: t.definition.title })">查看相关演进</a></div></details></div>
      </header>
      <ol class="stage-track" aria-label="任务流程位置"><li v-for="(step, index) in t.plan.steps" :key="step" :class="{ current: t.status === 'open' && step === t.current_step, done: stageDone(step) && !(t.status === 'open' && step === t.current_step) }"><span class="stage-node">{{ stageDone(step) && !(t.status === 'open' && step === t.current_step) ? '✓' : Number(index) + 1 }}</span><span>{{ label(step) }}</span></li></ol>
      <nav class="tabs" aria-label="任务详情分区">
        <button
          v-for="[id, title] in sections"
          :key="id"
          :aria-current="section === id ? 'page' : undefined"
          :class="{ chosen: section === id }"
          @click="setQuery({ section: id })"
        >
          {{ title }}
          <span v-if="id === 'checks'" class="count" aria-hidden="true">{{ n.summary.checks.required_passed }}/{{ n.summary.checks.required }}</span>
          <span v-if="id === 'basis' && t.applicable_knowledge?.length" class="count" aria-hidden="true">{{ t.applicable_knowledge.length }}</span>
        </button>
      </nav>
      <div class="task-detail-layout"><div class="task-detail-main">
      <section v-if="section === 'overview'" class="overview">
        <div class="conclusion result-banner" :class="{ amber: t.blocker || t.paused || t.confirmation, success: t.status === 'completed' }">
          <span class="banner-icon"><AppIcon :name="t.blocker || t.paused ? 'warning' : t.status === 'completed' ? 'check' : t.confirmation ? 'shield' : availableHumanChecks.length ? 'user' : 'play'" /></span><div>
          <h2>{{ bannerTitle }}</h2>
          <p v-if="reviewState" class="review-note-status">{{ reviewState }}</p>
          <p v-if="reviewState">人工意见已保存；实施与检查仍按当前状态继续。</p>
          <p>{{ bannerSummary }}</p>
          <p v-if="t.status === 'open' && t.blocker?.reason" class="blocked-reason">
            {{ blockerText(t.blocker.reason) }}
          </p>
          <details v-if="t.blocker?.reason?.length > 180"><summary>查看完整阻塞信息</summary><pre>{{ t.blocker.reason }}</pre></details>
          <p
            v-for="b in bannerReasons"
            class="blocked-reason"
          >
            {{ blockerText(b) }}
          </p>
          <p v-if="t.blocker || n.blocking_reasons?.length" class="next-action">下一步：{{ nextAction(n.summary) }}</p>
          <p v-if="t.status === 'open' && t.blocker">
            解除条件：{{
              t.blocker.recovery || "尚未记录，请核实阻塞原因"
            }}；已尝试：{{ t.blocker.tried || "未记录" }}
          </p>
          <small v-if="t.historical">历史记录，未在本机复验</small>
          <button v-if="showReviewEntry && t.status === 'open'" class="primary banner-action" :disabled="busy" @click="openTaskReview">{{ pendingReviewCheck !== null ? '查看待确认的验收' : reviewState ? '查看验收记录' : '开始人工验收' }}</button>
          <button v-if="n.next_action === 'wait_confirmation' && !t.confirmation" class="banner-action" @click="setQuery({ section: 'basis' })">查看待核实规范</button>
          </div>
        </div>
        <EpicOverview v-if="t.definition.type === 'epic'" :key="id" :id="id" />
        <section class="content-section">
          <h2>目标与范围</h2>
          <p>
            {{
              t.definition.original_request ||
              "未补充背景说明，以以下验收条件为准。"
            }}
          </p>
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
              <dt>采用的流程</dt>
              <dd>{{ t.plan.steps.map((step: string) => label(step)).join(' → ') }}</dd>
            </div>
          </dl>
          <h3>验收条件</h3>
          <ul>
            <li v-for="a in t.definition.acceptance">{{ a }}</li>
          </ul>
        </section>
        <div v-if="t.definition.type !== 'epic'" class="overview-cards"><section class="overview-card"><div class="section-heading"><h2>验收概况</h2><button @click="setQuery({ section: 'checks' })">全部检查 →</button></div><div class="overview-card-body"><div class="acceptance-summary"><p v-if="n.summary.checks.required" class="acceptance-count"><strong>{{ n.summary.checks.required_passed }}</strong> / {{ n.summary.checks.required }} 项必要检查通过</p><p v-else>尚未配置必需检查。</p><span class="status" :class="{ blocked: n.summary.checks.blocked || availableHumanChecks.length, pass: t.candidate && n.summary.checks.required && n.summary.checks.required_passed === n.summary.checks.required }">{{ acceptanceState }}</span></div><small>检查覆盖不等于研发进度；当前候选、验收方法和交付目标分别核对。</small></div></section><section class="overview-card"><div class="section-heading"><h2>最近进展</h2><button @click="setQuery({ section: 'execution' })">完整过程 →</button></div><div class="overview-card-body"><div v-if="work.length" class="work-timeline"><article v-for="a in work.slice(0, 3)" :key="a.id" class="timeline-item"><span class="timeline-marker"><AppIcon :name="a.status === 'pass' ? 'check' : 'clock'" /></span><div><h3>{{ a.display_unit || label(a.unit) }} · {{ a.display_status || label(a.status) }}</h3><p v-if="a.reason">{{ a.reason }}</p><p v-else-if="!a.display_unit">已接手，尚未提交实际结果。</p><time>{{ a.executor ? a.executor + ' · ' : '' }}{{ stamp(a.ended || a.started) }}</time></div></article></div><p v-else class="secondary">尚未提交有效工作进展。</p></div></section></div>
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
              >本次产物（逗号分隔）<input
                v-model="result.artifacts"
                placeholder="已存在的项目相对路径或 runtime: 引用" /></label
            ><template v-if="claimed.unit === 'execute'"
              ><label
                >规范处理<select v-model="result.knowledge_status">
                  <option value="unchanged">沿用现有规范</option>
                  <option value="updated">已更新规范与决策</option>
                </select></label
              ><label
                >规范处理依据 / 阻塞解除条件<textarea
                  v-model="result.knowledge_reason"
                  required
                ></textarea></label
              ><label v-if="result.knowledge_status === 'updated'"
                >已变更的规范或决策产物（逗号分隔）<input
                  v-model="result.knowledge_artifacts"
                  required
                  placeholder="实际已存在的项目相对路径" /></label></template
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
        </details>
        <details id="delivery-result" class="source-detail delivery-detail">
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
          <div class="related-links">
            <a v-if="t.epic_membership?.parent || t.definition.parent" :href="taskHref(t.epic_membership?.parent || t.definition.parent)"
              >父任务</a
            ><a
              v-if="t.definition.feedback_of"
              :href="taskHref(t.definition.feedback_of)"
              >源反馈任务</a
            ><a v-for="id in t.definition.dependencies" :href="taskHref(id)"
              >前置任务 {{ id }}</a
            ><a v-for="id in t.feedback" :href="taskHref(id)"
              >后续反馈 {{ id }}</a
            >
          </div>
        </details>
      </section>
      <section v-if="section === 'execution' || section === 'checks'">
        <div v-if="section === 'checks'" class="info-strip"><AppIcon name="shield" /><span>这些结果属于{{ t.candidate ? '当前候选 ' + t.candidate.slice(0, 12) : '当前任务，候选尚未生成' }}和定义修订 {{ n.definition_revision }}、计划修订 {{ n.plan_revision }}。人工验收只影响对应检查，不替代其他门禁。</span></div>
        <section v-if="section === 'checks'" class="content-section check-list">
          <div class="check-list-header"><h2>检查与验收</h2><small>{{ n.summary.checks.required_passed }} / {{ n.summary.checks.required }} 通过</small></div>
          <article v-for="c in t.definition.checks" :id="'task-check-' + c.id" :key="c.id" class="check">
            <div class="check-row"><span class="check-state-icon" :class="n.current_runs[c.id] ? 'running' : n.current_checks[c.id]?.result || 'not_run'"><AppIcon :name="n.current_runs[c.id] ? 'play' : n.current_checks[c.id]?.result === 'pass' ? 'check' : n.current_checks[c.id]?.result === 'fail' ? 'warning' : 'clock'" /></span><div class="check-main">
            <div class="check-heading">
              <h3>{{ c.id }} · {{ label(c.kind) }}</h3>
              <span
                class="status"
                :class="
                  n.current_runs[c.id]
                    ? 'running'
                    : n.current_checks[c.id]?.result
                "
                >{{
                  label(
                    n.current_runs[c.id]
                      ? "running"
                      : n.current_checks[c.id]?.result || "not_run",
                  )
                }}</span
              >
            </div>
            <p v-if="n.current_runs[c.id]">
              执行者：{{
                n.current_runs[c.id].executors.join("、")
              }}；正在执行，等待本次真实结果。
            </p>
            <p class="check-description">
              {{
                n.current_checks[c.id]?.reason || c.covers?.[0] ||
                "尚无当前候选和修订上的有效检查结果。"
              }}
            </p>
            <div class="check-tags"><span>{{ label(n.check_access?.[c.id]?.effective_method || c.method) }}</span> {{ c.required ? '必需检查' : '辅助检查' }}<template v-if="n.current_runs[c.id]"> · {{ n.current_runs[c.id].executors.join('、') }}</template></div>
            <p
              v-if="!n.current_checks[c.id] && priorCheck(c)"
              :class="{ failure: priorCheck(c).result === 'fail' }"
            >
              上次结果：{{
                label(priorCheck(c).result)
              }}（历史证据，当前候选需重新验收）· {{ priorCheck(c).reason }}
            </p>
            <small v-if="n.current_checks[c.id]"
              >{{
                n.current_checks[c.id].executor || n.current_checks[c.id].source
              }}
              · {{ stamp(n.current_checks[c.id].at) }}</small
            >
              <button v-if="n.current_checks[c.id]?.result !== 'pass'" class="check-evidence-toggle"
                :aria-expanded="!!checkOpen[c.id]"
                @click="checkOpen[c.id] = !checkOpen[c.id]"
              >
                {{
                  checkOpen[c.id] ? "收起检查依据" : "查看检查依据与证据"
                }}</button>
            </div><div class="actions check-actions"><button v-if="n.current_checks[c.id]?.result === 'pass'" :aria-expanded="!!checkOpen[c.id]" @click="checkOpen[c.id] = !checkOpen[c.id]">{{ checkOpen[c.id] ? '收起证据' : '查看证据' }}</button><button v-else-if="n.current_runs[c.id]" @click="setQuery({ section: 'execution' })">查看执行</button><button
                v-else-if="c.command?.length"
                :disabled="busy || !canCheck(c)"
                :title="checkReason(c)"
                @click="start('verify', '运行检查', { extra: { unit: c.id } })"
              >
                运行真实检查</button
              ><button
                v-else-if="n.check_access?.[c.id]?.effective_method === 'human'"
                :disabled="busy || t.status !== 'open'"
                :title="checkReason(c)"
                @click="manualCheck = c.id"
              >
                记录人工验收</button
              ><span v-else class="secondary"
                >需要{{ label(c.method) }}能力；请由执行者提交真实报告。</span
              >
            </div></div>
            <p v-if="c.command?.length && !canCheck(c)" class="secondary">
              {{ checkReason(c) }}
            </p>
            <div v-if="checkOpen[c.id]" class="check-detail">
              <h4>覆盖条件</h4>
              <ul>
                <li v-for="a in c.covers">{{ a }}</li>
              </ul>
              <p
                v-if="
                  t.definition.checks.some(
                    (x: any) =>
                      x.id !== c.id &&
                      JSON.stringify(x.covers) === JSON.stringify(c.covers),
                  )
                "
                class="secondary"
              >
                其他检查也配置了相同覆盖条件；仍分别保留原门禁。
              </p>
              <pre v-if="c.command?.length">{{ c.command.join(" ") }}</pre>
              <button v-if="n.current_checks[c.id]?.result === 'pass' && c.command?.length" :disabled="busy || !canCheck(c)" @click="start('verify', '运行检查', { extra: { unit: c.id } })">重新运行检查</button>
              <section
                v-for="e in [
                  ...(t.evidence_history || []),
                  ...(t.evidence || []),
                ].filter((e: any) => e.check === c.id)"
                class="evidence"
              >
                <strong :class="e.result">{{ label(e.result) }}</strong> ·
                {{ e.executor || e.source }} · {{ stamp(e.at) }}
                <p>{{ e.reason }}</p>
                <small>{{
                  n.current_checks[c.id]?.at === e.at
                    ? "当前候选与修订的有效证据"
                    : "历史证据，不能满足当前门禁"
                }}</small>
                <p
                  v-for="[ref, status] in Object.entries(
                    e.artifact_status || {},
                  )"
                >
                  {{ status }}
                </p>
                <button v-for="ref in e.artifacts" @click="openArtifact(ref)">
                  查看证据 {{ ref }}
                </button>
              </section>
            </div>
          </article>
        </section>
        <section v-if="section === 'checks'" class="content-section check-next"><h3>检查通过后会发生什么？</h3><p>系统会根据当前候选、必要检查和“{{ goalLabel(t.definition.goal) }}”的交付目标判断下一步。未执行或受阻的检查不会被当作通过。</p><details class="check-statistics"><summary>查看检查状态统计</summary><p>有效结果通过 {{ n.summary.checks.passed }} 项；必需 {{ n.summary.checks.required }} 项；执行中 {{ n.summary.checks.running }}；失败 {{ n.summary.checks.failed }}；未执行 {{ n.summary.checks.not_run }}；受阻 {{ n.summary.checks.blocked }}<span v-if="n.summary.checks.historical_failed">；历史失败 {{ n.summary.checks.historical_failed }}</span>。历史证据不能满足当前版本门禁。</p></details></section>
        <section v-if="section === 'execution'" class="content-section">
          <div class="section-heading"><h2>工作进展</h2><span class="status">结果与下一动作</span></div>
          <p v-if="!work.length" class="empty">
            尚未接手；没有已提交的实际工作结果。
          </p>
          <div class="work-timeline"><article v-for="a in work" :key="a.id" class="timeline-item">
            <span class="timeline-marker"><AppIcon :name="a.status === 'pass' ? 'check' : 'clock'" /></span><div>
              <h3>{{ a.display_unit || label(a.unit) }}<template v-if="a.executor"> · {{ a.executor }}</template></h3>
              <span class="status" :class="a.status">{{
                a.display_status || label(a.status)
              }}</span>
            <p v-if="a.reason">{{ a.reason }}</p><p v-else-if="!a.display_unit">已接手，尚未提交实际结果。</p>
            <time
              >{{ stamp(a.started) }} →
              {{ a.ended ? stamp(a.ended) : "尚未结束" }}</time
            >
            <div class="actions">
              <button v-for="ref in a.outputs" @click="openArtifact(ref)">
                查看产物 {{ ref }}
              </button>
            </div>
            </div>
          </article></div>
          <p v-if="n.activity_truncated" class="secondary">这里只显示最近 100 条任务状态变化；完整记录可在下方技术记录中查看。</p>
        </section>
        <details v-if="section === 'execution'" class="technical source-detail" :open="technicalOpen" @toggle="technicalToggle"><summary>技术记录</summary>
        <details v-if="claimed">
          <summary>高级协议结果提交</summary>
          <p>
            当前已领取工作的完整
            StepResult；仍经过原协议、授权、候选和修订校验。
          </p>
          <label
            >完整步骤结果 JSON<textarea
              v-model="advancedResult"
              rows="8"
              spellcheck="false"
            ></textarea></label
          ><button :disabled="busy" @click="submit(true)">
            提交完整步骤结果
          </button>
        </details>
        <h2>执行计划</h2><ol class="plan"><li v-for="step in t.plan.steps" :key="step" :class="{ current: step === t.current_step }">{{ label(step) }}<span v-if="step === t.current_step"> · 当前节点</span></li></ol><small>计划修订 {{ t.plan.revision }} · 计划节点不代表完成百分比</small>
        <dl class="facts">
          <div
            v-for="[key, value] in Object.entries({
              任务: t.definition.id,
              定义修订: n.definition_revision,
              计划修订: n.plan_revision,
              候选: t.candidate,
              原始下一动作: n.next_action,
              原始来源: t.definition.source,
            })"
          >
            <dt>{{ key }}</dt>
            <dd>{{ value || "未记录" }}</dd>
          </div>
        </dl>
        <details>
          <summary>Attempt、租约与交付记录</summary>
          <pre>{{
            JSON.stringify(
              {
                attempts: t.attempts,
                delivery: t.delivery,
                history: t.history,
              },
              null,
              2,
            )
          }}</pre>
        </details>
        <p v-if="technicalOpen && !eventsLoaded && !error" role="status" class="loading">
          正在读取任务技术记录…
        </p>
        <details v-if="eventsLoaded">
          <summary>原始事件（{{ events.length }} 条）</summary>
          <article v-for="e in events" class="record-row">
            <strong>{{ e.kind }}</strong> · {{ stamp(e.at) }}
            <pre>{{ JSON.stringify(e.body, null, 2) }}</pre>
          </article>
        </details>
        <details>
          <summary>完整定义与规范指纹</summary>
          <pre>{{
            JSON.stringify(
              {
                definition: t.definition,
                basis: t.applicable_knowledge,
                policy: t.policy_hash,
              },
              null,
              2,
            )
          }}</pre>
        </details>
        <CopyButton label="复制诊断摘要" :value="JSON.stringify({ task: t.definition.id, current_step: t.current_step, next_action: n.next_action, definition_revision: n.definition_revision, plan_revision: n.plan_revision, candidate: t.candidate, event_count: events.length }, null, 2)" />
        <div class="actions">
          <button
            :disabled="busy || t.status !== 'open'"
            @click="start('edit', '修订任务定义')"
          >
            编辑任务定义</button
          ><button
            class="danger-text"
            :disabled="
              busy ||
              t.attempts?.some((a: any) =>
                ['running', 'interrupted'].includes(a.status),
              )
            "
            @click="start('delete', '永久删除任务')"
          >
            永久删除任务
          </button>
        </div>
        <p class="secondary">
          活动或未核实停止的执行会阻止删除；入向依赖与引用仍由内核保护。
        </p>
      </details>
      </section>
      <section v-if="section === 'basis'">
        <p v-if="!basis && !basisError" role="status" class="loading">
          正在读取任务关联依据…
        </p>
        <div v-if="basisError" class="alert" role="alert">无法读取任务关联依据。<button @click="load(true)">重试</button></div>
        <section v-if="t.confirmation" class="conclusion">
          <h2>需要你的决定</h2>
          <p>{{ blockerText(t.confirmation.reason) }}</p>
          <p>
            仅针对定义修订 {{ t.confirmation.definition_revision }}、计划修订
            {{ t.confirmation.plan_revision
            }}<span v-if="t.confirmation.candidate">及本次候选</span>。
          </p>
          <div class="actions">
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
        <div class="info-strip"><AppIcon name="knowledge" /><span>显示本任务实际提供过的准确版本及显式引用；不把范围相关性当作已经使用的证明。</span></div>
        <p v-if="basis && !basis.records.length" class="empty">
          本任务尚无已提供或显式引用的规范与决策。
        </p>
        <div v-if="basis?.records?.length" class="record-list task-basis-list"><article v-for="k in basis.records" :key="k.id" class="record-row"><span class="basis-icon"><AppIcon name="file" /></span><div>
          <h3><button class="basis-title-button" @click="readBasis(k)">{{ k.title }}</button></h3>
          <p v-if="k.summary || k.rationale">{{ k.summary || k.rationale }}</p>
          <p>
            <span class="status"
              >{{ label(k.kind) }} · {{ label(k.status) }}</span
            >
            {{
              t.applicable_knowledge?.some((b: any) => b.id === k.id)
                ? "已向执行者提供准确版本"
                : explicitReference(k)
                  ? "定义显式引用，尚未提供"
                  : "影响当前任务的规范冲突，尚未提供给执行者"
            }}
          </p>
          <small>{{ k.tasks?.length ? k.tasks.length + ' 项关联任务' : '' }}<template v-if="k.occurred_at"> · {{ stamp(k.occurred_at) }}</template><template v-if="t.applicable_knowledge?.find((b: any) => b.id === k.id)?.version"> · 实际提供版本 {{ t.applicable_knowledge.find((b: any) => b.id === k.id).version.slice(0, 12) }}</template></small>
          <div class="actions">
            <button @click="readBasis(k)">
              {{
                t.applicable_knowledge?.some((b: any) => b.id === k.id)
                  ? "阅读实际提供版本"
                  : explicitReference(k)
                    ? "阅读当前显式引用"
                    : "阅读待核实规范"
              }}</button
            ><a :href="knowledgeHref(k.id)">查看当前资料与来源</a>
          </div>
        </div></article></div>
        <LoadState
          :loading="readingLoading"
          :error="readingError"
          @retry="basisReader.retry"
        />
        <section v-if="reading" id="basis-reading" class="content-section">
          <div class="section-heading">
            <h2>{{ reading.title }}</h2>
            <button @click="basisReader.clear">关闭正文</button>
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
          <MarkdownBody :body="reading.body" :id="reading.id" />
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
        <section class="content-section">
          <h2>已有决定</h2>
          <p v-if="!t.approvals?.length" class="empty">尚无已记录的决定。</p>
          <article v-for="a in t.approvals" class="record-row">
            <h3>
              {{
                a.decision === "approve"
                  ? "批准"
                  : a.decision === "adjust"
                    ? "要求调整"
                    : "否决"
              }}
              · {{ a.actor }}
            </h3>
            <p>{{ a.reason }}</p>
            <small
              >{{ stamp(a.at) }} · 定义 {{ a.definition_revision }} / 计划
              {{ a.plan_revision }}</small
            >
          </article>
        </section>
      </section>
      </div>
      <aside class="next-panel">
        <section>
          <h2>下一步</h2>
          <p>{{ nextAction(n.summary) }}</p>
          <button v-if="showReviewEntry && t.status === 'open' && !['review', 'review_pending'].includes(primaryKind)" class="primary" :disabled="busy" @click="openTaskReview">{{ reviewState ? '查看验收记录' : '开始人工验收' }}</button>
          <button v-if="primaryKind" :class="{ primary: ['review', 'review_pending'].includes(primaryKind) }" :disabled="busy" @click="primaryAction">{{ primaryKind === 'review' && reviewState ? '查看验收记录' : primaryLabels[primaryKind] }}</button>
          <div v-if="unownedRunning" class="lost-claim-help"><p>当前标签页没有这次接手的凭据。若原执行确已停止，可以确认后恢复任务并重新接手。</p><button @click="start('resume', '恢复无法继续的接手')">恢复无法继续的接手</button></div>
        </section>
        <section class="next-panel-facts">
          <h3>工作信息</h3>
          <dl>
            <div><dt>执行 / 负责</dt><dd>{{ n.summary.executors.length ? n.summary.executors.join('、') : '当前无人执行' }}</dd></div>
            <div><dt>交付目标</dt><dd>{{ goalLabel(t.definition.goal) }}</dd></div>
            <div><dt>当前候选</dt><dd :title="t.candidate || ''">{{ t.candidate ? t.candidate.slice(0, 12) : '尚未生成' }}</dd></div>
            <div class="sidebar-scope"><dt>影响范围</dt><dd><ScopeSummary :items="t.definition.scope" :limit="1" /></dd></div>
          </dl>
        </section>
        <section class="next-panel-facts sidebar-basis">
          <h3>相关依据</h3>
          <p v-if="basisLoading && !basis">正在读取关联依据…</p>
          <p v-else-if="basisError">关联依据读取失败。<button class="inline-link" @click="load(true)">重试</button></p>
          <template v-else-if="basis?.records?.length">
            <button v-for="k in basis.records.slice(0, 3)" :key="k.id" class="sidebar-basis-link" @click="showBasis(k)"><AppIcon name="file"/><span>{{ k.title }}</span></button>
            <button class="inline-link" @click="setQuery({ section: 'basis' })">查看全部 {{ basis.records.length }} 份依据 →</button>
          </template>
          <p v-else>尚无已提供或明确关联的依据。</p>
          <a class="sidebar-evolution" :href="href('/evolution', { q: t.definition.title })">项目演进 →</a>
        </section>
      </aside></div>
    </template>
    <section
      v-if="artifact || artifactError || artifactLoading"
      class="artifact-panel"
    >
      <div class="section-heading">
        <h2>证据原件</h2>
        <button @click="attachments.clear">关闭证据</button>
      </div>
      <LoadState
        :loading="artifactLoading"
        :error="artifactError"
        @retry="attachments.retry"
      />
      <p v-if="artifact">{{ artifact.ref }}</p>
      <img
        v-if="artifact?.image"
        :src="artifact.image"
        alt="任务验收证据原件"
        class="artifact-image"
      />
      <pre v-else-if="artifact">{{ artifact.body || "（空文件）" }}</pre>
    </section>
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
      :error="opError"
      :danger="
        ['delete', 'cancel', 'confirm'].includes(op.action) &&
        op.extra?.decision !== 'approve'
      "
      @close="op = null"
      @submit="perform"
    >
      <label v-if="['claim', 'confirm'].includes(op.action)"
        >执行者 / 决策者<input v-model="actor" required
      /></label>
      <label v-if="!['claim', 'verify', 'resume'].includes(op.action)"
        >操作理由<textarea v-model="reason" required rows="3"></textarea>
      </label>
      <label v-if="op.action === 'resume'" class="inline"
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
    </ActionDialog>
    <ManualReviewDialog v-if="manualCheck !== null" :key="id + ':' + manualCheck" :task-id="id" :check-id="manualCheck" @select-check="manualCheck = $event" @close="closeReview" @saved="reviewSaved" />
  </div>
</template>

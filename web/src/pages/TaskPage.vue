<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import { api, notices, params } from "../api";
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
  schedule,
  nextAction,
  goalLabel,
  blockerText,
} from "../display";
import LoadState from "../components/LoadState.vue";
import ActionDialog from "../components/ActionDialog.vue";
import ManualReviewDialog from "../components/ManualReviewDialog.vue";
import MarkdownBody from "../components/MarkdownBody.vue";
const props = defineProps<{ id: string }>();
const r = useResource<any>(null),
  { data: n, loading, error, last } = r;
const t = computed(() => n.value?.task),
  section = computed(() => route.value.query.get("section") || "overview");
const sections = [
  ["overview", "概览"],
  ["checks", "检查与验收"],
  ["execution", "执行过程"],
  ["basis", "规范与决策"],
  ["technical", "技术记录"],
];
const basis = ref<any>(null),
  events = ref<any[]>([]),
  eventsLoaded = ref(false);
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
const manualCheck = ref("");
const availableHumanChecks = computed(() =>
  (t.value?.definition.checks || []).filter((c: any) => n.value?.check_access?.[c.id]?.can_review),
);
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
const receiptReader = useObjectReader<any>();
const {
  data: receipt,
  error: receiptError,
  loading: receiptLoading,
} = receiptReader;
const checkOpen = ref<Record<string, boolean>>({});
let timer: ReturnType<typeof setInterval>;
async function extras() {
  const id = props.id;
  if (section.value === "basis" && !basis.value) {
    const v = await api("/api/task-knowledge?" + params({ id, compact: 1 }));
    if (id === props.id) basis.value = v;
  }
  if (section.value === "technical") {
    const v = await api("/api/events?" + params({ id }));
    if (id === props.id) {
      events.value = v;
      eventsLoaded.value = true;
    }
  }
}
async function load(quiet = false) {
  await r.load(() => api("/api/task?" + params({ id: props.id })), quiet);
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
    events.value = [];
    eventsLoaded.value = false;
    attachments.clear();
    basisReader.clear();
    contextReader.clear();
    receiptReader.clear();
    claimed.value = null;
    manualCheck.value = "";
    load();
  },
);
watch(section, () => {
  attachments.clear();
  basisReader.clear();
  load(true);
});
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
const work = computed(() =>
  [...(t.value?.attempts || [])]
    .reverse()
    .filter(
      (a: any) =>
        a.ended || a.status === "running" || a.status === "interrupted",
    ),
);
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
    if (current.id !== props.id) {
      notices.value = current.title + "已记录，请在对应任务查看结果。";
      return;
    }
    sessionStorage.setItem("ael-actor", actor.value);
    if (current.action === "claim") {
      claimed.value = v.attempt;
      contextReader.clear();
      receiptReader.clear();
      deliveryRef.value = "";
      result.value = {
        status: "pass",
        summary: "",
        artifacts: "",
        knowledge_status: "unchanged",
        knowledge_reason: "",
        knowledge_artifacts: "",
        return_to: "",
      };
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
    if (t.value.current_step === "execute")
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
    if (t.value.current_step === "delivery" && receipt.value) {
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
        resume_at: t.value.current_step,
      };
    if (advanced) stepResult = JSON.parse(advancedResult.value);
    await api("/api/action", {
      action: "submit",
      submission: {
        task: props.id,
        attempt: claimed.value.id,
        token: claimed.value.token,
        key: claimed.value.id + "-dashboard",
        result: stepResult,
      },
    });
    claimed.value = null;
    notices.value = "工作结果已提交，状态由协议重新计算。";
    await load(true);
  } catch (e) {
    error.value = String(e);
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
    return "当前检查不可验收，请核对任务状态、候选或已有执行。";
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
      <header class="object-header">
        <div>
          <p class="eyebrow">任务 · {{ t.definition.type }}</p>
          <h1>{{ t.definition.title }}</h1>
          <p class="inline">
            <span class="status" :class="n.board">{{
              schedule(n.summary)
            }}</span
            ><span>{{ label(n.current_step) }}</span
            ><span>{{
              n.summary.executors.length
                ? n.summary.executors.join("、")
                : "当前无人执行"
            }}</span>
          </p>
        </div>
        <div class="task-heading-actions"><button v-if="availableHumanChecks.length" class="primary" @click="manualCheck = availableHumanChecks[0].id">开始人工验收</button><a :href="href('/evolution', { q: t.definition.title })">查看相关演进</a></div>
      </header>
      <ol class="stage-track" aria-label="任务流程位置"><li v-for="step in t.plan.steps" :key="step" :class="{ current: step === t.current_step }"><span>{{ step === t.current_step ? '●' : '○' }}</span>{{ label(step) }}</li></ol>
      <nav class="tabs" aria-label="任务详情分区">
        <button
          v-for="[id, title] in sections"
          :key="id"
          :aria-current="section === id ? 'page' : undefined"
          :class="{ chosen: section === id }"
          @click="setQuery({ section: id })"
        >
          {{ title }}
        </button>
      </nav>
      <div class="task-detail-layout"><div class="task-detail-main">
      <section v-if="section === 'overview'" class="overview">
        <div class="conclusion">
          <h2>当前结论与下一步</h2>
          <p>{{ label(n.summary.last_result) }}</p>
          <p v-if="t.status === 'open' && t.blocker?.reason" class="blocked-reason">
            {{ blockerText(t.blocker.reason) }}
          </p>
          <details v-if="t.blocker?.reason?.length > 180"><summary>查看完整阻塞信息</summary><pre>{{ t.blocker.reason }}</pre></details>
          <p
            v-for="b in n.blocking_reasons.filter(
              (b: string) => b !== t.blocker?.reason,
            )"
            class="blocked-reason"
          >
            {{ blockerText(b) }}
          </p>
          <p class="next-action">下一步：{{ nextAction(n.summary) }}</p>
          <p v-if="t.status === 'open' && t.blocker">
            解除条件：{{
              t.blocker.recovery || "尚未记录，请核实阻塞原因"
            }}；已尝试：{{ t.blocker.tried || "未记录" }}
          </p>
          <small
            >{{
              n.summary.activity_at
                ? "最近有效活动 " + stamp(n.summary.activity_at)
                : "尚无已提交的有效进展"
            }}<span v-if="t.historical"> · 历史记录，未在本机复验</span></small
          >
        </div>
        <div class="actions primary-actions">
          <button
            v-if="availableHumanChecks.length"
            class="primary"
            @click="manualCheck = availableHumanChecks[0].id"
          >记录人工验收</button><button
            v-else-if="t.current_step === 'verify'"
            @click="setQuery({ section: 'checks' })"
          >
            查看验收与运行检查</button
          ><button
            v-if="t.confirmation"
            class="primary"
            @click="setQuery({ section: 'basis' })"
          >
            审阅并作出决定</button
          ><button
            v-if="n.next_action === 'wait_confirmation' && !t.confirmation"
            @click="setQuery({ section: 'basis' })"
          >
            查看待核实规范</button
          ><button
            v-if="t.status === 'open' && !t.paused"
            :disabled="busy"
            @click="start('pause', '暂缓任务')"
          >
            暂缓</button
          ><button
            v-if="t.status === 'open'"
            :disabled="busy"
            @click="start('cancel', '取消任务')"
          >
            取消任务
          </button>
        </div>
        <div v-if="claimed" class="work-form">
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
          <form @submit.prevent="submit()">
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
            ><template v-if="t.current_step === 'execute'"
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
            <section v-if="t.current_step === 'delivery'" class="receipt-form">
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
            <button
              class="primary"
              :disabled="
                busy ||
                !supplied ||
                contextLoading ||
                (t.current_step === 'delivery' &&
                  result.status === 'pass' &&
                  !receipt)
              "
            >
              {{ busy ? "正在提交…" : "提交并查看下一步" }}
            </button>
          </form>
        </div>
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
              <dt>影响范围</dt>
              <dd>{{ t.definition.scope?.join("、") || "未限定路径" }}</dd>
            </div>
            <div>
              <dt>明确不做</dt>
              <dd>{{ t.definition.non_goals?.join("、") || "未额外声明" }}</dd>
            </div>
            <div>
              <dt>时间预算</dt>
              <dd>
                {{
                  (t.time_limit_seconds ?? t.definition.max_seconds) > 0
                    ? (t.time_limit_seconds ?? t.definition.max_seconds) + " 秒"
                    : "不限时"
                }}
              </dd>
            </div>
          </dl>
          <h3>验收条件</h3>
          <ul>
            <li v-for="a in t.definition.acceptance">{{ a }}</li>
          </ul>
        </section>
        <section class="content-section">
          <h2>交付、规范处理与后续工作</h2>
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
            <a v-if="t.definition.parent" :href="taskHref(t.definition.parent)"
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
        </section>
      </section>
      <section v-if="section === 'execution' || section === 'checks'">
        <section v-if="section === 'execution'" class="content-section">
          <h2>执行计划</h2>
          <ol class="plan">
            <li
              v-for="step in t.plan.steps"
              :class="{ current: step === t.current_step }"
            >
              {{ label(step)
              }}<span v-if="step === t.current_step"> · 当前节点</span>
            </li>
          </ol>
          <small
            >计划修订 {{ t.plan.revision }} · 计划节点不代表完成百分比</small
          >
        </section>
        <section v-if="section === 'checks'" class="content-section check-list">
          <h2>检查与验收</h2>
          <p>
            有效结果通过 {{ n.summary.checks.passed }} 项；必需
            {{ n.summary.checks.required }} 项；执行中
            {{ n.summary.checks.running }}；失败
            {{ n.summary.checks.failed }}；未执行
            {{ n.summary.checks.not_run }}；受阻 {{ n.summary.checks.blocked
            }}<span v-if="n.summary.checks.historical_failed"
              >；历史失败 {{ n.summary.checks.historical_failed }}</span
            >
          </p>
          <p class="secondary">
            检查完成与候选通过分别判断；历史证据保留，不能满足当前版本门禁。
          </p>
          <article v-for="c in t.definition.checks" :key="c.id" class="check">
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
              ><span
                >{{ c.required ? "必需" : "非必需" }} ·
                {{
                  label(n.check_access?.[c.id]?.effective_method || c.method)
                }}</span
              >
            </div>
            <p v-if="n.current_runs[c.id]">
              执行者：{{
                n.current_runs[c.id].executors.join("、")
              }}；正在执行，等待本次真实结果。
            </p>
            <p>
              {{
                n.current_checks[c.id]?.reason ||
                "尚无当前候选和修订上的有效检查结果。"
              }}
            </p>
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
            <div class="actions">
              <button
                :aria-expanded="!!checkOpen[c.id]"
                @click="checkOpen[c.id] = !checkOpen[c.id]"
              >
                {{
                  checkOpen[c.id] ? "收起检查依据" : "查看检查依据与证据"
                }}</button
              ><button
                v-if="c.command?.length"
                :disabled="busy || !canCheck(c)"
                :title="checkReason(c)"
                @click="start('verify', '运行检查', { extra: { unit: c.id } })"
              >
                运行真实检查</button
              ><button
                v-else-if="n.check_access?.[c.id]?.effective_method === 'human'"
                :disabled="busy || !n.check_access?.[c.id]?.can_review"
                :title="checkReason(c)"
                @click="manualCheck = c.id"
              >
                记录人工验收</button
              ><span v-else class="secondary"
                >需要{{ label(c.method) }}能力；请由执行者提交真实报告。</span
              >
            </div>
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
        <section v-if="section === 'execution'" class="content-section">
          <h2>实际工作进展</h2>
          <p v-if="!work.length" class="empty">
            尚未接手；没有已提交的实际工作结果。
          </p>
          <article v-for="a in work" class="work-unit">
            <div>
              <h3>{{ label(a.unit) }} · {{ a.executor }}</h3>
              <span class="status" :class="a.status">{{
                label(a.status)
              }}</span>
            </div>
            <p>{{ a.reason || "已接手，尚未提交实际结果。" }}</p>
            <small
              >{{ stamp(a.started) }} →
              {{ a.ended ? stamp(a.ended) : "尚未结束" }}</small
            >
            <div class="actions">
              <button v-for="ref in a.outputs" @click="openArtifact(ref)">
                查看产物 {{ ref }}
              </button>
            </div>
          </article>
        </section>
      </section>
      <section v-if="section === 'basis'">
        <p v-if="!basis && !error" role="status" class="loading">
          正在读取任务关联依据…
        </p>
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
        <h2>适用规范与参考依据</h2>
        <p class="secondary">
          列出实际提供过的准确版本及显式引用；不把范围相关性当作已经使用的证明。
        </p>
        <p v-if="basis && !basis.records.length" class="empty">
          本任务尚无已提供或显式引用的规范与决策。
        </p>
        <article v-for="k in basis?.records || []" class="record-row">
          <h3>{{ k.title }}</h3>
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
        </article>
        <LoadState
          :loading="readingLoading"
          :error="readingError"
          @retry="basisReader.retry"
        />
        <section v-if="reading" class="content-section">
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
      <section v-if="section === 'technical'" class="technical">
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
        <h2>技术记录</h2>
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
        <p v-if="!eventsLoaded && !error" role="status" class="loading">
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
      </section>
      </div><aside class="next-panel"><h2>下一步</h2><p>{{ nextAction(n.summary) }}</p><button v-if="availableHumanChecks.length" class="primary" @click="manualCheck = availableHumanChecks[0].id">开始人工验收</button><button v-else-if="recoverable" @click="start('resume', '恢复任务')">恢复任务</button><button v-else-if="t.confirmation" @click="setQuery({ section: 'basis' })">审阅当前方案</button><button v-else-if="runnable" @click="start('claim', '接手当前工作')">接手当前工作</button><button v-else-if="t.current_step === 'verify'" @click="setQuery({ section: 'checks' })">查看检查与验收</button><div class="next-panel-facts"><h3>工作信息</h3><p>交付目标：{{ goalLabel(t.definition.goal) }}</p><p>当前候选：{{ t.candidate ? t.candidate.slice(0, 12) : '尚未生成' }}</p><p>执行者：{{ n.summary.executors.length ? n.summary.executors.join('、') : '当前无人执行' }}</p></div></aside></div>
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
        ><input type="checkbox" v-model="ack" />我已核实旧执行进程停止</label
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
        保留当前节点或回到选择的更早责任；未知或仍在运行的进程不会被擅自释放。
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
    <ManualReviewDialog v-if="manualCheck" :task-id="id" :check-id="manualCheck" @close="manualCheck = ''" @saved="load(true)" />
  </div>
</template>

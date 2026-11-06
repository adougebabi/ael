<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { api, info, notices, params, refreshInfo, refreshRevision } from "../api";
import { route, setQuery, taskHref, knowledgeHref } from "../router";
import { useResource } from "../resource";
import { useObjectReader } from "../objectReader";
import { label, stamp, blockerText, readableError } from "../display";
import LoadState from "../components/LoadState.vue";
import Pagination from "../components/Pagination.vue";
import ActionDialog from "../components/ActionDialog.vue";
import MarkdownBody from "../components/MarkdownBody.vue";
import CopyButton from "../components/CopyButton.vue";
import AppIcon from "../components/AppIcon.vue";
import { theme, setTheme } from "../theme";
const section = computed(() => route.value.query.get("section") || "overview");
const batch = computed(() => route.value.query.get("batch") || "");
const offset = computed(() => Number(route.value.query.get("offset") || 0));
const r = useResource<any>({}),
  { data, loading, error, last } = r;
const status = ref<any>(null),
  probe = ref<any>(null),
  probeBusy = ref(false),
  busy = ref(false),
  op = ref<any>(null),
  opError = ref(""),
  digest = ref(""),
  verification = ref<Record<string, any>>({});
const sourceReader = useObjectReader<any>();
const {
  data: source,
  error: sourceError,
  loading: sourceLoading,
} = sourceReader;
const sections = [
  ["overview", "接入概览"],
  ["sources", "资料来源"],
  ["migrations", "迁移批次"],
  ["diagnostics", "运行诊断"],
];
const categories = [
  ["current", "阻断当前工作"],
  ["conflicts", "待核实冲突"],
  ["history", "历史资料告警"],
  ["unknown", "影响未知"],
];
const kindLabels: Record<string, string> = {
  links: "未解析引用",
  conflict: "资料或映射冲突",
  integrity: "完整性问题",
  baseline: "继承基线待验收",
  integration: "旧集成待审阅",
  other: "其他记录",
};
async function load() {
  const key = route.value.key;
  sourceReader.clear();
  await r.load(async () => {
    let v: any = {};
    if (section.value === "overview") v = await api("/api/project-status");
    if (section.value === "sources")
      v = await api(
        "/api/sources?" + params({ offset: offset.value, limit: 25 }),
      );
    if (section.value === "migrations")
      v = await api(
        "/api/migration-view?" +
          params({
            id: batch.value,
            offset: offset.value,
            limit: 25,
            category: route.value.query.get("category") || "",
          }),
      );
    if (section.value === "diagnostics")
      v = {
        events: await api(
          "/api/timeline?" + params({ limit: 25, offset: offset.value }),
        ),
      };
    if (key !== route.value.key) throw new Error("请求已由新页面替代");
    return v;
  });
  if (section.value === "overview") status.value = data.value;
  else if (!status.value) {
    try {
      status.value = await api("/api/project-status");
    } catch (e) {
      error.value = String(e);
    }
  }
}
watch(() => route.value.key, load);
onMounted(load);
watch(refreshRevision, load);
function change(values: Record<string, string>) {
  setQuery({ ...values, offset: undefined });
}
async function probeExecutor() {
  if (probeBusy.value) return;
  probeBusy.value = true;
  try {
    probe.value = await api("/api/executor-probe");
  } catch (e) {
    error.value = String(e);
  } finally {
    probeBusy.value = false;
  }
}
function start(action: string, id = "") {
  if (busy.value) return;
  opError.value = "";
  op.value = {
    action,
    id,
    title: (
      {
        preview: "扫描与预览",
        apply: "批准并导入当前计划",
        verify: "核验迁移",
        repair: "修复中断",
        rollback: "回滚本次导入",
      } as Record<string, string>
    )[action],
    digest: data.value.digest || "",
  };
}
async function perform() {
  if (busy.value || !op.value) return;
  busy.value = true;
  opError.value = "";
  const current = { ...op.value };
  try {
    const v = await api("/api/migrations", {
      action: current.action,
      id: current.id,
      approved_digest: digest.value,
    });
    if (current.action === "verify") verification.value[current.id] = v;
    notices.value =
      current.action === "verify"
        ? v.valid
          ? "本次迁移核验通过，结果来自当前核验回执。"
          : "本次迁移核验发现问题，请查看下方结果。"
        : current.title +
          "已完成。" +
          (v.state ? "批次结果：" + label(v.state) + "。" : "");
    op.value = null;
    digest.value = "";
    if (current.action === "preview")
      change({ section: "migrations", batch: "preview", mode: "sources" });
    else if (current.action === "apply")
      change({ section: "migrations", batch: v.id, mode: "warnings" });
    else await load();
    await refreshInfo();
    status.value = await api("/api/project-status");
  } catch (e) {
    opError.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function readSource(id: string) {
  const batchID = batch.value;
  await sourceReader.read(() =>
    api("/api/migration-view?" + params({ id: batchID, source: id })),
  );
}
const material = computed(() =>
  ["ready", "conflict"].includes(status.value?.knowledge?.state)
    ? "资料可读取"
    : label(status.value?.knowledge?.state),
);
</script>
<template>
  <div>
    <nav class="tabs" aria-label="项目设置分区">
      <button
        v-for="[id, title] in sections"
        :key="id"
        :class="{ chosen: section === id }"
        :aria-current="section === id ? 'page' : undefined"
        @click="change({ section: id, batch: '', category: '', mode: '' })"
      >
        {{ title }}
      </button>
    </nav>
    <LoadState :loading="loading" :error="error" :last="last" @retry="load" />
    <section v-if="section === 'overview' && status">
      <p class="settings-notice">接入状态、迁移应用、工作流激活和执行器能力分别读取；未探测的能力不会被当作可用。</p>
      <div class="settings-summary-grid"><section class="settings-summary-card"><span class="settings-icon"><AppIcon name="knowledge" /></span><div><h2>资料可用性 <span class="status">{{ material }}</span></h2><p v-if="status.knowledge?.state === 'conflict'">存在需要核实的规范冲突。</p><p v-else>规范、决策与历史资料可按实际来源查看。</p><a href="#/knowledge">打开规范库 →</a></div></section><section class="settings-summary-card"><span class="settings-icon"><AppIcon name="loop" /></span><div><h2>工作流激活 <span class="status">{{ status.workflow_active ? '已激活' : '尚未激活' }}</span></h2><p>{{ status.workflow_active ? '工作流已接入，仍按任务权限执行。' : '资料导入与正式接管是两个独立步骤；激活使用原 CLI 授权。' }}</p><a href="#/settings?section=diagnostics">查看接入信息 →</a></div></section></div>
      <section class="settings-panel"><div class="settings-panel-head"><h2>执行器能力</h2><button :disabled="probeBusy" @click="probeExecutor">{{ probeBusy ? '探测中…' : '探测受管执行器接口' }}</button></div><div class="capability-line"><span class="settings-icon"><AppIcon name="layers" /></span><div><h3>受管执行器接口</h3><p>{{ probe ? probe.reason : status.executor?.reason }}</p></div><span class="status">{{ probe ? label(probe.state) : '尚未探测' }}</span></div><details v-if="probe"><summary>探测详情</summary><pre>{{ JSON.stringify(probe, null, 2) }}</pre></details></section>
      <section class="settings-panel settings-migration"><div><h2>初始化与资料迁移</h2><p>{{ status.applied_migrations ? '已应用 ' + status.applied_migrations + ' 个批次。' : '尚无已应用的迁移批次。' }} 先审阅来源与影响，再确认当前计划。</p><a href="#/settings?section=migrations">查看已有迁移 →</a></div><button :disabled="busy" @click="start('preview')">扫描与预览</button></section>
      <section class="settings-panel settings-appearance"><h2>外观</h2><p>选择工作台强调色；等待使用琥珀提示，失败与危险使用红色。</p><div><button :class="{ chosen: theme === 'indigo' }" :aria-pressed="theme === 'indigo'" @click="setTheme('indigo')"><span class="theme-palette indigo"><i></i><i></i><i></i><i></i></span>靛蓝 · 默认 <span v-if="theme === 'indigo'" aria-hidden="true">✓</span></button><button :class="{ chosen: theme === 'graphite' }" :aria-pressed="theme === 'graphite'" @click="setTheme('graphite')"><span class="theme-palette graphite"><i></i><i></i><i></i><i></i></span>石墨 · 更克制 <span v-if="theme === 'graphite'" aria-hidden="true">✓</span></button></div></section>
      <section class="content-section">
        <h2>当前需要处理的问题</h2>
        <p v-if="!status.issues?.length" class="empty">
          当前没有任务报告明确阻断；这不代表尚未探测的能力已通过。
        </p>
        <article v-for="issue in status.issues" class="record-row">
          <a :href="taskHref(issue.id)" class="record-title">{{
            issue.title
          }}</a>
          <p
            v-for="reason in issue.blocker_reason
              ? [issue.blocker_reason]
              : issue.reasons"
            class="blocked-reason"
          >
            {{ blockerText(reason) }}
          </p>
          <a :href="taskHref(issue.id)">查看解除条件与下一步</a>
        </article>
      </section>
    </section>
    <section v-if="section === 'sources'">
      <h2>原始资料与权威来源</h2>
      <p>
        {{
          label(data.health?.state)
        }}。来源文件仍是唯一可编辑正文；读取和索引问题不会被包装成资料可用。
      </p>
      <p class="secondary">
        来源变更后，先审阅原资料，再通过现有 CLI
        更新索引。此页面不修改业务规范。
      </p>
      <article v-for="s in data.items" class="record-row source-row">
        <span class="settings-icon"><AppIcon name="file" /></span><div><h3>{{ s.record?.title || s.path.split("/").pop() }}</h3>
        <p>
          {{ label(s.record?.status) }} ·
          {{
            s.action === "retired"
              ? "已切换到明确的权威来源"
              : s.action === "connected"
                ? "已接入原资料"
                : s.action === "read_failed"
                  ? "读取失败"
                  : label(s.action)
          }}
        </p>
        <a v-if="s.action === 'connected' && s.record?.id" :href="knowledgeHref(s.record.id)">阅读已接入资料 →</a>
        <details>
          <summary>来源路径与读取诊断</summary>
          <p>来源：{{ s.path }}</p>
          <p v-if="s.authority_path">当前权威：{{ s.authority_path }}</p>
          <p>{{ s.reason }}</p>
          <code>{{ s.hash }}</code>
        </details></div>
      </article>
      <p v-if="!loading && !data.total" class="empty">
        暂无来源账本条目，可回到接入概览核实资料状态。
      </p>
      <Pagination
        v-if="data.total > 25 || offset"
        :offset="offset"
        :limit="25"
        :total="data.total"
        name="资料来源"
        @change="setQuery({ offset: $event })"
      />
    </section>
    <section v-if="section === 'migrations'">
      <template v-if="!batch"
        ><div class="section-heading">
          <div>
            <h2>迁移批次</h2>
            <p class="secondary">
              扫描与预览不会执行旧脚本；导入与正式激活保持原边界。
            </p>
          </div>
          <button :disabled="busy" @click="start('preview')">扫描与预览</button>
        </div>
        <a
          v-if="data.preview?.available"
          href="#/settings?section=migrations&batch=preview&mode=sources"
          >继续审阅当前预览（{{ data.preview.count }} 条来源）</a
        >
        <p v-if="!loading && !data.runs?.length" class="empty">
          尚无迁移记录。可先扫描并审阅隔离或已明确授权的项目来源。
        </p>
        <article v-for="m in data.runs" class="record-row migration-card">
          <a
            class="record-title"
            :href="
              '#/settings?' + params({ section: 'migrations', batch: m.id })
            "
            >{{ label(m.state) }} · {{ m.counts.sources }} 条来源</a
          >
          <div class="migration-counts"><div><strong>{{ m.counts.sources }}</strong><small>来源</small></div><div><strong>{{ m.counts.imported }}</strong><small>已导入</small></div><div><strong>{{ m.counts.pending }}</strong><small>待核实</small></div><div><strong>{{ m.counts.preserved }}</strong><small>保留原件</small></div></div><small>忽略 {{ m.counts.ignored }}；错误数量未单独记录。</small>
          <small
            >计划生成于 {{ stamp(m.created) }}；执行时间未单独记录。{{
              m.activated ? "已执行工作流激活" : "本批次未记录工作流激活"
            }}</small
          >
          <p>
            待核实冲突
            {{
              m.groups.find((g: any) => g.category === "conflicts")?.count || 0
            }}
            · 历史告警
            {{
              m.groups.find((g: any) => g.category === "history")?.count || 0
            }}
            · 影响未知
            {{
              m.groups.find((g: any) => g.category === "unknown")?.count || 0
            }}
          </p>
        </article></template
      >
      <template v-else-if="data.id"
        ><button
          class="back-link"
          @click="change({ batch: '', category: '', mode: '' })"
        >
          ← 返回批次列表
        </button>
        <section class="content-section">
          <h2>
            {{ batch === "preview" ? "待审阅计划" : "迁移批次" }} ·
            {{ label(data.state) }}
          </h2>
          <p>
            来源 {{ data.counts.sources }}；已导入
            {{ data.counts.imported }}；待核实 {{ data.counts.pending }}；忽略
            {{ data.counts.ignored }}；错误数量未单独记录。
          </p>
          <div class="migration-counts"><div><strong>{{ data.counts.sources }}</strong><small>来源</small></div><div><strong>{{ data.counts.imported }}</strong><small>已导入</small></div><div><strong>{{ data.counts.pending }}</strong><small>待核实</small></div><div><strong>{{ data.counts.preserved }}</strong><small>保留原件</small></div></div>
          <small
            >计划生成于 {{ stamp(data.created) }}；执行时间未单独记录。</small
          >
          <div v-if="batch !== 'preview'" class="actions">
            <button :disabled="busy" @click="start('verify', batch)">
              核验迁移</button
            ><button
              :disabled="busy || data.state === 'rolled_back'"
              @click="start('repair', batch)"
            >
              修复中断</button
            ><button
              :disabled="busy || data.state === 'rolled_back'"
              @click="start('rollback', batch)"
            >
              回滚本次导入
            </button>
          </div>
          <p v-if="data.activated" class="failure">
            本批次包含已授权的激活；回滚范围仍由原接口核验与保护。
          </p>
          <section v-if="verification[batch]" class="conclusion">
            <h3>
              {{
                verification[batch].valid ? "本次核验通过" : "本次核验发现问题"
              }}
            </h3>
            <p v-for="issue in verification[batch].issues">{{ issue }}</p>
            <p class="secondary">
              此为刚执行的真实核验回执；后续内容变化后需重新核验。
            </p>
          </section>
        </section>
        <section v-if="status?.issues?.length" class="conclusion">
          <h3>阻断当前工作 · {{ status.issues.length }} 项任务</h3>
          <article v-for="issue in status.issues.slice(0, 5)">
            <a :href="taskHref(issue.id)">{{ issue.title }}</a>
            <p
              v-for="reason in issue.blocker_reason
                ? [issue.blocker_reason]
                : issue.reasons"
            >
              {{ blockerText(reason) }}
            </p>
          </article>
          <small
            >这些来自当前任务权威查询；不据告警数量推断阻断，也不自动归因为本批次。</small
          ><a v-if="status.issues.length > 5" href="#/settings"
            >查看全部当前问题</a
          >
        </section>
        <nav class="tabs" aria-label="批次内容">
          <button
            :class="{ chosen: route.query.get('mode') !== 'sources' }"
            @click="change({ mode: 'warnings' })"
          >
            告警影响汇总</button
          ><button
            :class="{ chosen: route.query.get('mode') === 'sources' }"
            @click="change({ mode: 'sources', category: '' })"
          >
            审阅来源与原文
          </button>
        </nav>
        <template v-if="route.query.get('mode') !== 'sources'"
          ><div class="warning-group" v-for="[category, title] in categories">
            <div class="section-heading">
              <h3>
                {{ title }}
                <span class="count">{{
                  data.groups.find((g: any) => g.category === category)
                    ?.count || 0
                }}</span>
              </h3>
              <button
                v-if="
                  data.groups.find((g: any) => g.category === category)?.count
                "
                @click="change({ category })"
              >
                查看此类记录
              </button>
            </div>
            <p v-if="category === 'current'" class="secondary">
              仅有当前任务明确报告阻断才进入上方问题区。未解析引用的影响保守保留为未知。
            </p>
            <p
              v-for="[kind, count] in Object.entries(
                data.groups.find((g: any) => g.category === category)?.kinds ||
                  {},
              )"
            >
              {{ kindLabels[kind] || "其他记录" }} · {{ count }} 条
            </p>
          </div>
          <template v-if="route.query.get('category')"
            ><h3 class="content-section">
              {{
                categories.find(
                  ([id]) => id === route.query.get("category"),
                )?.[1]
              }}
              · 具体对象
            </h3>
            <article v-for="w in data.warnings" class="record-row">
              <h3>{{ w.title }}</h3>
              <p>{{ w.source ? w.source.split("/").pop() : "批次级记录" }}</p>
              <p>{{ w.action }}</p>
              <details>
                <summary>来源与原始告警</summary>
                <p>{{ w.source || "未定位来源" }}</p>
                <pre>{{ w.raw }}</pre>
              </details>
            </article>
            <Pagination
              :offset="offset"
              :limit="25"
              :total="data.warning_total"
              name="迁移告警"
              @change="setQuery({ offset: $event })" /></template
        ></template>
        <template v-else
          ><article v-for="s in data.sources" class="record-row">
            <h3>{{ s.path.split("/").pop() }}</h3>
            <p>{{ label(s.kind) }} · {{ label(s.action) }}</p>
            <button @click="readSource(s.id)">审阅来源原文</button>
            <details>
              <summary>来源与目标路径</summary>
              <p>{{ s.path }} → {{ s.target || "保留原件，不导入正文" }}</p>
              <p v-for="risk in s.risks">{{ risk }}</p>
            </details>
          </article>
          <Pagination
            v-if="data.source_total > 25 || offset"
            :offset="offset"
            :limit="25"
            :total="data.source_total"
            name="批次来源"
            @change="setQuery({ offset: $event })"
        /></template>
        <LoadState
          :loading="sourceLoading"
          :error="sourceError"
          @retry="sourceReader.retry"
        />
        <section v-if="source" class="content-section preview">
          <div class="section-heading">
            <h2>{{ source.path.split("/").pop() }}</h2>
            <button @click="sourceReader.clear">关闭原文</button>
          </div>
          <MarkdownBody
            :id="source.id"
            :body="source.body || '（无可阅读正文；原件按迁移策略保留。）'"
          />
          <details>
            <summary>来源技术详情</summary>
            <p>{{ source.path }}</p>
            <code>{{ source.hash }}</code>
          </details>
        </section>
        <section v-if="batch === 'preview'" class="content-section preview">
          <h2>批准当前内容版本</h2>
          <p>
            请先分页审阅来源、原文与风险。导入会按当前计划写入 AEL
            的迁移映射和资料；不会自动执行旧 hooks 或激活工作流。
          </p>
          <p>
            确认摘要：<code>{{ data.digest }}</code>
          </p>
          <label
            >审阅后填写完整确认摘要<input v-model="digest" autocomplete="off"
          /></label>
          <div class="actions">
            <button
              class="primary"
              :disabled="busy || digest !== data.digest"
              @click="start('apply')"
            >
              批准此计划并导入
            </button>
          </div>
        </section>
      </template>
    </section>
    <section v-if="section === 'diagnostics'" class="settings-diagnostics">
      <div class="section-heading"><h2>运行实例</h2><CopyButton label="复制诊断摘要" :value="JSON.stringify({ project: info.project, data_dir: info.data_dir, service_version: info.service_version, service_build: info.service_build, instance_id: info.instance_id, executable: info.executable, asset_version: info.config?.asset_version, schema: info.config?.schema }, null, 2)" /></div>
      <dl class="facts">
        <div
          v-for="[name, value] in Object.entries({
            项目根: info.project,
            运行数据: info.data_dir,
            服务版本: info.service_version,
            服务构建: info.service_build,
            实例: info.instance_id,
            实际二进制: info.executable,
            入口资产: info.config?.asset_version,
            Schema: info.config?.schema,
          })"
        >
          <dt>{{ name }}</dt>
          <dd>
            {{ value }}
            <CopyButton v-if="value" :value="String(value)" />
          </dd>
        </div>
      </dl>
      <h2>原始项目事件</h2>
      <p class="secondary">
        扫描、导入、索引、心跳、领取与进程记录仅供诊断，不视为项目演进。
      </p>
      <article v-for="e in data.events" class="record-row">
        <h3>{{ e.kind }}</h3>
        <small
          >{{ stamp(e.at) }}
          <a v-if="e.task" :href="taskHref(e.task, 'technical')"
            >查看所属任务</a
          ></small
        >
        <details>
          <summary>原始事件载荷</summary>
          <pre>{{ JSON.stringify(e.body, null, 2) }}</pre>
        </details>
      </article>
      <div class="pagination">
        <button
          :disabled="offset === 0"
          @click="setQuery({ offset: Math.max(0, offset - 25) })"
        >
          上一页</button
        ><span>第 {{ Math.floor(offset / 25) + 1 }} 页</span
        ><button
          :disabled="!data.events || data.events.length < 25"
          @click="setQuery({ offset: offset + 25 })"
        >
          下一页
        </button>
      </div>
    </section>
    <ActionDialog
      v-if="op"
      :title="op.title"
      :busy="busy"
      :error="opError"
      :danger="op.action === 'rollback'"
      @close="op = null"
      @submit="perform"
      ><p v-if="op.id">目标批次：{{ op.id }}</p>
      <p v-if="op.action === 'preview'">
        只扫描当前项目明确来源并保存审阅计划，不执行旧集成。
      </p>
      <p v-if="op.action === 'apply'">
        批准并导入已审阅的准确计划。<code>{{ op.digest }}</code>
      </p>
      <p v-if="op.action === 'rollback'">
        按原始映射回滚本批导入；已有用户修改或运行中的任务可能阻止回滚。原接口的授权与冲突检查保持有效。
      </p>
      <p v-if="op.action === 'repair'">
        针对当前批次继续原有中断恢复流程；不会跳过版本或写锁检查。
      </p>
      <p v-if="op.action === 'verify'">
        读取当前源与映射，核对实际导入结果。
      </p></ActionDialog
    >
  </div>
</template>

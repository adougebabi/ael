<script setup lang="ts">
import { computed, ref } from 'vue';
import { snapshotResults, mergeResultMetadata, scopedResults, importedResults, type ResultFile } from '../results';
import { candidateMaterials, bytes, isImage, type Material } from '../materials';
import { contentGroups, readerKind, listingLabel, nameParts } from '../reading';
import { checkReading, methodLabel } from '../checks';
import { label, stamp } from '../display';
import AppIcon from './AppIcon.vue';
import MaterialPreview from './MaterialPreview.vue';
import CheckDetail from './CheckDetail.vue';
import ResultCard from './ResultCard.vue';
import type { Relation } from './MaterialBody.vue';

// What this task delivered, read from the task record: the result the state
// machine set, the submitted summary, the check records and the materials
// bound to the current candidate. Nothing here is a fixed example.
const props = defineProps<{ snapshot: any; objects: any[]; imported?: any }>();
const n = computed(() => props.snapshot), t = computed(() => n.value.task), d = computed(() => t.value.definition);
const done = computed(() => t.value.status === 'completed');
const outcome = computed(() => {
  const r = t.value.result, target = t.value.delivery?.target || d.value.target, env = t.value.delivery?.environment || d.value.environment;
  if (r === 'report_delivered_with_findings') return { tone: 'warn', title: '报告已交付，报告中的问题仍待处理', nature: '交付验收报告：报告本身已提交；报告记录的失败项不因提交而修复。' };
  if (r === 'report_delivered') return { tone: 'pass', title: '验收报告已交付', nature: '交付验收报告：报告与对应检查记录已提交；不包含新的实现改动。' };
  if (r === 'analysis_delivered') return { tone: 'pass', title: '分析结论已交付', nature: '交付分析结论：没有实施或发布改动，后续工作需要另行安排。' };
  if (r === 'merged') return { tone: 'pass', title: '改动已合并' + (target ? '到 ' + target : ''), nature: '合并到指定分支：合并不等于已经发布。' };
  if (r === 'released') return { tone: 'pass', title: '已发布' + (env ? '到 ' + env : ''), nature: '发布到指定环境：以交付回执为准。' };
  if (r === 'verified') return { tone: 'pass', title: '经过验证的改动已交付', nature: '交付经过验证的改动：任务定义的必需检查在当前候选上通过；没有发布动作。' };
  return { tone: 'muted', title: label(r || 'completed'), nature: '交付目标：' + (d.value.goal || '未记录') };
});
// The submitted description of the delivering step, newest first.
const delivering = computed(() => [...(t.value.attempts || [])].reverse().find((a: any) => a.status === 'pass' && ['execute', 'integrate', 'delivery', 'analysis'].includes(a.unit) && a.reason));
const deliveredText = computed(() => delivering.value?.reason || '');
const checks = computed(() => (d.value.checks || []).map((c: any) => ({ c, r: checkReading(n.value, c), record: n.value.current_checks?.[c.id] })));
const passed = computed(() => checks.value.filter((x: any) => x.r.key === 'pass').length);
const required = computed(() => checks.value.filter((x: any) => x.c.required !== false));
const recordLine = computed(() => !checks.value.length ? '任务定义没有检查条目。' : '现有记录显示：' + required.value.length + ' 项必需检查中 ' + required.value.filter((x: any) => x.r.key === 'pass').length + ' 项通过' + (checks.value.length > required.value.length ? '，另有 ' + (checks.value.length - required.value.length) + ' 项辅助检查' : '') + '。');
// Acceptance items no check covers are stated, not hidden.
const uncovered = computed(() => (d.value.acceptance || []).filter((a: string) => !(d.value.checks || []).some((c: any) => (c.covers || []).includes(a))));
const boundary = computed(() => [...(d.value.non_goals || []).map((x: string) => '不在范围内：' + x), ...uncovered.value.map((x: string) => '没有对应检查的验收条件：' + x)]);

// Current materials grouped by recorded content identity.
const files = computed(() => {
  const id = d.value.id;
  const all = mergeResultMetadata([...snapshotResults(n.value), ...importedResults(props.imported, id)], props.objects || [], id);
  const current = scopedResults(all, 'current');
  for (const m of candidateMaterials(props.objects || [], t.value).current) if (!current.some((f) => f.ref === m.ref)) { const unit = (t.value.attempts || []).find((a: any) => a.id === meta(m.ref)?.attempt_id)?.unit || ''; current.push({ ref: m.ref, task: m.task, name: m.name, mime: m.mime, size: m.size, sha256: m.sha256, state: 'current', at: m.at, associations: [{ role: 'related', state: 'current', candidate: m.candidate, attempt: meta(m.ref)?.attempt_id, label: unit.startsWith('check:') ? '检查 ' + unit.slice(6) + ' 提交的材料' : unit ? label(unit) + ' 提交的材料' : '当前候选附件 · 用途未记录' }] }); }
  return current;
});
const meta = (ref: string) => (props.objects || []).find((o: any) => o.id === ref);
const groups = computed(() => contentGroups(files.value.map((f) => ({ ...f, sha256: f.sha256 || meta(f.ref)?.sha256, size: f.size ?? meta(f.ref)?.size }))).map((g) => {
  const first = g.objects[0];
  const name = first.name || meta(first.ref)?.filename || first.ref.split('/').pop() || first.ref;
  const kind = readerKind({ name, mime: first.mime || meta(first.ref)?.mime });
  const relations: Relation[] = g.objects.flatMap((o) => (o.associations || []).map((a) => ({ ref: o.ref, name: o.name, label: a.label, at: a.at || o.at, candidate: a.candidate, current: a.state === 'current' })));
  const isCheck = g.objects.some((o) => o.associations?.some((a) => a.role === 'check'));
  const purposes = [...new Set(g.objects.flatMap((o) => (o.associations || []).map((a) => a.role === 'check' ? '检查 ' + (a.check || '') + ' 依据' : a.role === 'output' ? '实施交付' : a.label)))];
  return { ...g, name, kind, relations, isCheck, purposes, first };
}).sort((a, b) => Number(b.isCheck && nameParts(b.name).ext === 'json') - Number(a.isCheck && nameParts(a.name).ext === 'json') || Number(b.isCheck) - Number(a.isCheck)));
const objectCount = computed(() => groups.value.reduce((sum, g) => sum + g.objects.length, 0));
const relationCount = computed(() => groups.value.reduce((sum, g) => sum + g.relations.length, 0));
const isJSON = (g: { name: string }) => nameParts(g.name).ext === 'json';
const primaryReport = computed(() => groups.value.find((g) => g.isCheck && isJSON(g)) || groups.value.find((g) => isJSON(g) && g.purposes.some((p) => p.includes('报告'))));

const preview = ref<{ material: Material; relations: Relation[] } | null>(null);
// Images are shown directly; every group stays in the list below as well.
const imageGroups = computed(() => groups.value.filter((g) => isImage({ name: g.name, mime: g.first.mime || meta(g.first.ref)?.mime })));
function toMaterial(g: (typeof groups.value)[number]): Material {
  const f = g.first as ResultFile;
  const m = meta(f.ref);
  return { id: f.ref, ref: f.ref, task: f.task, name: g.name, mime: f.mime || m?.mime || '', size: g.size, at: f.at || m?.created_at, source: m?.source, candidate: m?.candidate, sha256: g.sha256, status: m?.status, state: 'current', role: g.isCheck ? 'evidence' : 'output', checks: [...new Set(g.objects.flatMap((o) => (o.associations || []).map((a) => a.check).filter(Boolean) as string[]))] };
}
function open(g: (typeof groups.value)[number]) { preview.value = { material: toMaterial(g), relations: g.relations }; }
const openCheck = ref<any>(null);
const boundaryEl = ref<HTMLElement>();
const groupMeta = (g: (typeof groups.value)[number]) => [listingLabel({ name: g.name, mime: g.first.mime }), g.size !== undefined ? bytes(g.size) : '', g.purposes.slice(0, 2).join('、'), g.objects.length > 1 ? '同内容 ' + g.objects.length + ' 次提交' : ''].filter(Boolean).join(' · ');
</script>
<template>
  <section class="wb-content-section ds-card" aria-labelledby="delivery-title">
    <h2 id="delivery-title">{{ done ? '交付结论' : '当前结果' }}</h2>
    <p class="ds-headline" :class="outcome.tone"><AppIcon :name="outcome.tone === 'warn' ? 'alert' : 'tick'" />{{ outcome.title }}</p>
    <p class="ds-lead">{{ recordLine }}</p>
    <div class="ds-two">
      <div><h3>这次交付了什么</h3><p v-if="deliveredText" class="ds-text">{{ deliveredText }}</p><p v-else class="muted">执行者没有留下文字说明。结果与材料仍按下方记录阅读，这里不补写结论。</p>
        <p v-if="groups.length" class="ds-text muted">当前候选关联 {{ groups.length }} 份材料<template v-if="objectCount > groups.length">（{{ objectCount }} 个附件对象）</template>。</p></div>
      <div><h3>这次工作的性质</h3><p class="ds-text muted">{{ outcome.nature }}</p><p v-if="t.completed" class="ds-text muted">完成于 {{ stamp(t.completed) }}</p></div>
    </div>
    <div class="ds-actions"><button v-if="primaryReport" class="primary" @click="open(primaryReport)">阅读{{ primaryReport.isCheck ? '验收' : '' }}报告</button><button class="button" @click="boundaryEl?.scrollIntoView({ block: 'center' })">查看范围与限制</button></div>
  </section>
  <section class="wb-content-section ds-card" aria-labelledby="verify-title">
    <h2 id="verify-title">验证结果</h2>
    <div v-if="checks.length" class="rd-table-scroll"><table class="rd-table ds-checks"><thead><tr><th>检查</th><th>方式</th><th>记录结果</th></tr></thead><tbody>
      <tr v-for="x in checks" :key="x.c.id"><td><button class="text-button ds-check-link" @click="openCheck = x.c">{{ x.c.id }} · {{ label(x.c.kind) }}</button><small v-if="x.c.covers?.length" class="ds-covers">{{ x.c.covers.join('；') }}</small></td><td>{{ methodLabel(n.check_access?.[x.c.id]?.effective_method || x.c.method) }}{{ x.c.required === false ? ' · 辅助' : '' }}</td><td><span class="ds-result" :class="x.r.tone"><AppIcon :name="x.r.tone === 'pass' ? 'tick' : x.r.tone === 'fail' || x.r.tone === 'warn' ? 'alert' : 'clock'" />{{ x.r.label }}</span></td></tr>
    </tbody></table></div>
    <p v-else class="muted">尚未明确检查条目：任务定义没有声明检查，完成依据以任务记录为准。</p>
    <div ref="boundaryEl" class="ds-boundary" tabindex="-1"><h3>本次验证的边界</h3>
      <ul v-if="boundary.length"><li v-for="b in boundary" :key="b">{{ b }}</li></ul>
      <p v-else>任务定义没有记录范围外内容，所有验收条件都有对应检查。报告中写明的限制请在报告内阅读。</p>
      <p v-if="passed && t.historical">这是导入的历史记录，未在本系统重新执行。</p>
    </div>
  </section>
  <section v-if="groups.length" class="wb-content-section ds-card" aria-labelledby="basis-title">
    <div class="ds-head"><h2 id="basis-title">验证依据</h2><span class="muted">{{ groups.length }} 份不同内容 · 保留 {{ objectCount }} 个对象 / {{ relationCount }} 次关联</span></div>
    <div v-if="imageGroups.length" class="wb-result-cards ds-images"><ResultCard v-for="g in imageGroups.slice(0, 4)" :key="g.key" :material="toMaterial(g)" @open="open(g)" /></div>
    <div class="ds-materials">
      <div v-for="g in groups" :key="g.key" class="ds-material"><span class="rd-icon"><AppIcon :name="isImage({ name: g.name, mime: g.first.mime }) ? 'image' : g.kind === 'source' || g.kind === 'log' ? 'terminal' : g.kind === 'markdown' ? 'book' : 'file'" /></span><div class="ds-material-text"><strong>{{ g.name }}</strong><small>{{ groupMeta(g) }}</small></div><button class="button ghost" :aria-label="'查看 ' + g.name" @click="open(g)">查看</button></div>
    </div>
    <details class="wb-disclosure ds-relations"><summary>附件提交关系 · 以上 {{ groups.length }} 份内容对应 {{ objectCount }} 个对象</summary>
      <div class="rd-table-scroll"><table class="rd-table"><thead><tr><th>内容</th><th>对象</th><th>关联用途</th><th>时间</th></tr></thead><tbody>
        <template v-for="g in groups" :key="g.key"><tr v-for="(r, i) in g.relations" :key="g.key + i"><td>{{ i === 0 ? g.name : '' }}</td><td><code>{{ r.ref.slice(0, 18) }}…</code></td><td>{{ r.label }}</td><td>{{ r.at ? stamp(r.at) : '未记录' }}</td></tr></template>
      </tbody></table></div>
      <p class="muted">相同内容按已记录的 SHA-256 合并阅读入口；每个附件对象及其提交关系都保留，原件未被删除或改写。</p>
    </details>
  </section>
  <MaterialPreview v-if="preview" :material="preview.material" :relations="preview.relations" :checks="n.current_checks" :current-candidate="t.candidate" :task-title="d.title" @close="preview = null" />
  <CheckDetail v-if="openCheck" :snapshot="n" :check="openCheck" :objects="objects" @close="openCheck = null" />
</template>

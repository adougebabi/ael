// Reading state of one check, derived only from the task snapshot the
// authority returned (current_checks, current_runs, evidence history and
// check_access). It explains; it never changes or infers a verdict.
export type CheckTone = 'pass' | 'fail' | 'warn' | 'info' | 'muted';
export type CheckReading = { key: string; label: string; tone: CheckTone; title: string; detail: string; next: string };

export function checkHistory(n: any, check: string) {
  const t = n?.task, current = n?.current_checks?.[check];
  const same = (e: any) => current && e.at === current.at && e.candidate === current.candidate && e.config_hash === current.config_hash;
  return [...(t?.evidence || []), ...(t?.evidence_history || [])].filter((e: any) => e.check === check && !same(e)).sort((a: any, b: any) => String(b.at).localeCompare(String(a.at)));
}

const short = (c?: string) => c ? c.slice(0, 12) : '未记录';

export function checkReading(n: any, c: any): CheckReading {
  const t = n?.task || {}, run = n?.current_runs?.[c.id], current = n?.current_checks?.[c.id], access = n?.check_access?.[c.id] || {};
  const required = c.required !== false;
  const reason = current?.reason || '';
  if (current && ['not_applicable', 'waived'].includes(current.result)) return { key: 'not-applicable', label: current.result === 'waived' ? '已豁免' : '不适用', tone: 'muted', title: current.result === 'waived' ? '这项检查已按记录豁免' : '本任务不涉及这项检查', detail: reason || '记录没有写明原因。', next: '无需继续处理；不计为通过证据。' };
  if (run) return { key: 'running', label: run.started || run.reported ? '执行中' : '已领取', tone: 'info', title: run.started || run.reported ? '已上报检查开始，等待结果' : '检查已领取，等待执行上报', detail: (run.executors || []).join('、') + (run.started || run.reported ? ' 已上报开始执行。' : ' 已领取这项检查。') + '开始上报不等于通过，也不代表执行者仍在线。', next: '等待执行者提交检查结果。' };
  if (current?.result === 'pass') return { key: 'pass', label: '通过', tone: 'pass', title: (current.candidate && current.candidate === t.candidate ? '当前候选的' : '') + '检查通过', detail: reason || '检查记录没有附文字说明。', next: '本项检查无需继续处理；其他任务条件独立判断。' };
  if (current?.result === 'fail') return required
    ? { key: 'fail', label: '未通过', tone: 'fail', title: '检查失败，问题需要处理', detail: reason || '检查记录没有附文字说明，可打开关联材料定位。', next: t.status === 'completed' ? '报告已交付；失败项仍需按发现处理，不因报告提交而修复。' : '由执行者修复后，在新候选上重新检查。' }
    : { key: 'optional', label: '建议项有发现', tone: 'warn', title: '辅助检查有发现', detail: reason || '检查记录没有附文字说明。', next: '这是辅助检查，不阻断必需条件；发现仍按实际情况处理。' };
  if (current?.result === 'blocked') return { key: 'blocked', label: '受阻', tone: 'warn', title: '环境条件阻断检查', detail: reason || '记录没有写明受阻原因。', next: '解除环境条件后重新执行；受阻不等于检查失败。' };
  if (current && ['error', 'interrupted', 'abandoned'].includes(current.result)) return { key: 'execution-error', label: '未能执行', tone: 'warn', title: '检查未能执行', detail: reason || '记录没有写明原因。', next: '核对执行条件后重新运行；这不是检查结论。' };
  const history = checkHistory(n, c.id);
  const lastPass = history.find((e: any) => e.result === 'pass');
  if (lastPass) return { key: 'stale', label: '待复验', tone: 'warn', title: '旧版本通过，当前待复验', detail: '最近一次通过属于候选 ' + short(lastPass.candidate) + '（定义 ' + (lastPass.acceptance_revision ?? '?') + ' / 计划 ' + (lastPass.plan_revision ?? '?') + '），不适用于当前候选 ' + short(t.candidate) + '。', next: '在当前候选上重新检查；旧结果只供参考。' };
  if (access.evidence_required || access.screenshot_required) return { key: 'missing-evidence', label: '缺少证据', tone: 'warn', title: '专项证据尚未齐备', detail: '这项检查要求' + (access.screenshot_required ? '真实截图' : (c.artifacts?.length ? '约定材料（' + c.artifacts.join('、') + '）' : '专项材料')) + '，当前候选还没有关联所需材料。', next: '由执行者补充材料后再验收；没有材料时不能通过。' };
  return { key: 'pending', label: '未执行', tone: 'muted', title: required ? '必要检查尚未执行' : '辅助检查尚未执行', detail: t.candidate ? '当前候选 ' + short(t.candidate) + ' 还没有这项检查的记录。' : '候选尚未生成，检查还不能执行。', next: access.effective_method === 'human' ? '等待有权限的验收人核对。' : '等待执行者运行并提交检查结果。' };
}

export const methodLabel = (m?: string) => ({ command: '命令检查', human: '人工判断', model: '模型判断', scenario: '实际场景', review: '审阅' } as Record<string, string>)[m || ''] || m || '未记录方式';

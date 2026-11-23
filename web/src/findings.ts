// Reading of one finding's lifecycle from the authority's finding view:
// status, decision, linked repair and revalidation validity. A delivered
// report or a completed repair task never closes a finding by itself.
export type FindingReading = { key: string; label: string; tone: 'pass' | 'warn' | 'fail' | 'info' | 'muted'; stage: number; title: string; detail: string };
export const findingStages = ['记录发现', '核对问题', '关联修复', '新版本复验'];

export function findingReading(f: any): FindingReading {
  const repair = f?.repair_task, state = f?.repair_state;
  const repairName = repair ? '「' + (repair.title || repair.id) + '」' : '';
  if (f?.status === 'dismissed') return { key: 'dismissed', label: '不属于问题', tone: 'muted', stage: 1, title: '已核对，不属于问题', detail: '裁定记录保留原因与裁定人；这不改变原检查记录。' };
  if (f?.status === 'accepted_tradeoff') return { key: 'tradeoff', label: '已接受取舍', tone: 'muted', stage: 1, title: '已确认接受这项取舍', detail: '问题仍然存在，只是按记录的理由暂不修复；以后变化时需要重新核对。' };
  if (f?.status === 'needs_evidence') return { key: 'need-evidence', label: '待补证据', tone: 'warn', stage: 1, title: '需要补充复现依据', detail: '现有材料不足以判断；补齐缺失项后再裁定。' };
  if (f?.status === 'resolved' && state === 'verified') return { key: 'retest-passed', label: '复验通过', tone: 'pass', stage: 4, title: '新版本复验通过', detail: '修复任务' + repairName + '在当前候选上的检查记录已确认这项问题解决。' };
  if (f?.status === 'resolved' || state === 'candidate_changed') return { key: 'regressed', label: '复验失效', tone: 'warn', stage: 3, title: '旧复验失效，需要重新核对', detail: '修复任务' + repairName + '的候选或检查已变化，原复验结果不适用于当前版本。' };
  if (repair && state === 'awaiting_retest') return { key: 'retest-pending', label: '待复验', tone: 'warn', stage: 4, title: repair.status === 'completed' ? '修复任务完成，发现尚未关闭' : '已有新候选，等待复验', detail: '需要用修复任务' + repairName + '新候选的检查结果复验；旧候选的检查不能替代。' };
  if (repair) return { key: 'repair-running', label: '修复中', tone: 'info', stage: 3, title: '已关联修复，正在实施', detail: '修复任务' + repairName + (repair.status === 'open' ? '正在推进' : '状态为 ' + repair.status) + '；完成后仍需复验。' };
  if (state === 'unavailable') return { key: 'repair-missing', label: '修复不可读', tone: 'warn', stage: 3, title: '关联的修复任务无法读取', detail: '修复关系仍保留，但当前无法读取该任务；请核对权限或任务是否存在。' };
  if (f?.decision?.action === 'fix') return { key: 'confirmed', label: '待关联修复', tone: 'warn', stage: 2, title: '问题已确认，待关联修复', detail: '裁定需要修复；关联修复任务后才能在新版本上复验。' };
  return { key: 'new', label: '待处理', tone: 'info', stage: 1, title: '新发现，尚待处理', detail: '还没有裁定；先核对现象与证据，再决定修复、补证或接受取舍。' };
}

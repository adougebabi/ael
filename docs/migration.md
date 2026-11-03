# Trellis 迁移与切换

没有真实旧项目路径的本次实施仅使用 testdata/trellis 与 /tmp 夹具副本。未扫描 HOME、未迁移摇光、未修改真实 hooks，也没有业务发布。

## 分开的四个动作

1. 安装 AEL 二进制：不接触项目工作流。
2. init：检测旧工作流、生成资产，Trellis 项目默认 shadow；不执行扫描到的命令。
3. scan/plan/validate/apply：只读盘点与可审阅导入，旧资料一直保留；结构和正文由程序确定性处理，语义建议经 proposal 校验。
4. activate：显式批准、核对旧运行已排空、来源 hash 与迁移核验后，才停用可精确识别的 Trellis 受管入口/受管 hook。自定义入口拒绝自动切换。

## 可重复的夹具演练

以下 PATH 应是新临时副本，真实旧项目默认只做 scan/plan。

```sh
cp -R testdata/trellis /tmp/ael-legacy-rehearsal
export AEL_DATA_DIR=/tmp/ael-legacy-runtime
ael init --project /tmp/ael-legacy-rehearsal
ael migrate scan --project /tmp/ael-legacy-rehearsal --out /tmp/ael-legacy-report
ael migrate plan --inventory /tmp/ael-legacy-report/inventory.json --out /tmp/ael-legacy-report
ael migrate validate --plan /tmp/ael-legacy-report/plan.json
# 审阅实际 inventory/plan，使用 validate 返回的准确摘要：
ael migrate apply --project /tmp/ael-legacy-rehearsal --plan /tmp/ael-legacy-report/plan.json --approved-digest HASH
ael migrate verify --project /tmp/ael-legacy-rehearsal --migration ID
ael migrate repair --project /tmp/ael-legacy-rehearsal --migration ID
ael migrate rollback --project /tmp/ael-legacy-rehearsal --migration ID
```

所有命令输出 JSON，源变更拒绝旧计划，目标被用户改动拒绝覆盖。报告默认写在显式 out；浏览器预览只将计划保存到运行库，不改源目录。原文、大小、哈希、来源类型、风险、目标映射均可逐条审阅。所有来源都计入 import/historical/preserve/pending/ignore 对账，未解析内容仍有原文和风险。

## 语义 proposal

`ael-migrate-legacy` 只处理明确批次，输出 schemas/migration-proposal.json 对应数据。每条携带 source_id/source_hash、真实原文 quote、理由、目标引用、待确认项。action=classify 可以将知识分类为 spec/decision/research/reference，status=current/historical/conflict；目标必须与原计划完全一致。action=task_status 只处理旧状态歧义，不能把未完成来源变成历史完成。

```sh
ael migrate validate --plan /tmp/ael-legacy-report/plan.json --proposal /tmp/proposal.json
ael migrate apply --project /tmp/ael-legacy-rehearsal --plan /tmp/ael-legacy-report/plan.json --proposal /tmp/proposal.json --approved-digest NEW_HASH
```

proposal 不包含任意 shell、不自行写数据库、不删除原文、不改业务。正文混合现行约束和未来计划时默认 conflict，不把 .trellis/spec 出身等同“当前实现事实”。只有经审阅的 current/accepted 知识才作为有效约束，相关冲突通过显式引用/范围阻止任务执行。语义分类与项目真实事实核验仍需 Agent/人判断，程序不声称能自动解决任意自然语言矛盾。

任务 task.json 未识别字段保存在 legacy 元数据；PRD/design/implement/JSONL 等逐文件保留。JSONL 引用映射到新知识引用；缺失、越界与破损条目报告风险。原父子关系与显式依赖分别映射，无法解析的保留警告和待确认，不猜新关系。同名文件分开；task ID 稳定，归档移动不会新建任务。

旧 completed/done 仅显示“历史完成 · 导入未复验”，无时间则未知，不编造候选、通过数量或发布。旧 in_progress 没有新租约，默认暂停/交接；歧义状态进入待确认。任务开始产生 AEL Attempt/审批/反馈后，旧资料再导入不能覆盖其新进展。

## 激活与恢复边界

只有用户明确授权切换真实项目后，才执行：

```sh
ael activate --project PATH --migration ID --approved-digest HASH --ack-drained
```

--ack-drained 表示已核实旧执行停止。非空旧运行指针继续阻断，必须在单独授权的旧流程停用中核实处理，再重新扫描。激活只移除精确匹配的 TRELLIS 受管 AGENTS 块，以及已识别的 python3 .trellis/scripts/session-start.py、inject-subagent-context.py、check-subagent-stop.py hooks。其他内容/权限/无关 hooks 保留；自定义命令、CLAUDE/Codex 集成等无法安全识别时返回冲突，不提供 force 绕过。

每次目标改写前保存 hash 与必要备份到运行目录。outbox 保存意图、原版本与新内容，原子替换；中断可 repair。rollback 逐文件比较应用后 hash，遇到用户新修改保留内容并报告 rollback_conflict。不会以整目录旧备份覆盖新项目。

已回滚的计划不能直接重用；重新扫描/计划并审阅新摘要。源目录始终不由迁移器改写。运行数据导出恢复的迁移记录标记 restored_review_required，不自动激活旧计划；先重新核查来源、当前目标与备份路径。

## 当前保守限制

- 本次识别常见 Trellis 布局及 JSON/JSONL；未知自定义元数据原样保留，缺失结构按 pending/historical 报告，不自动发明任务。
- 只扫描显式来源目录；symlink 越界不读取。大文件只保留/备份，需明确范围再语义整理。
- Markdown 相对文件链接做映射，缺失/目录/无法确定的链接保留原文并报告，不猜测网页锚点或任意模板语义。
- 原文与新记录可独立保存；备份和大报告不进入业务 Git。没有完整运行数据备份时，Git clone 不能恢复旧租约和原始产物。

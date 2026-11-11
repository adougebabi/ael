# V2-07 本地恢复记录

本文件是实施现场与证据的快照；任务状态和当前步骤以 `WORKLOG.md` 为唯一权威。本记录不是验收回执，也不表示 V2-07 已通过。

## 现场

- 工作目录：`/Users/vinson/Documents/project/个人/ael`；分支 `main`；当前 HEAD `3ed6099`（V2-06 检查点）。
- 截至本记录创建时，V2-00 至 V2-06 为 DONE，V2-07 为 ACTIVE，V2-08 及后续为 PENDING。V2-07 的实现、测试与文档仍在工作区中，尚未建立本项完成检查点；恢复前以 `git status --short --untracked-files=all` 核实，保留全部既有改动。
- 本项已接入叶子候选及纳入证明、父集成候选、CLI/API/页面和隔离验收入口。该描述不代表当前修改已经满足全部验收。
- 任务外的非阻断问题单独记于 `NON-BLOCKING-ISSUES.md`，不在本项顺手修复。

## 未完成与待核验

1. 以下三类问题已由 `internal/core/epic_integration_concurrency_test.go` 中的隔离测试复现，失败日志为 `/private/tmp/ael-v2-v200.xLJaSd/logs/v207-concurrency-red.log`；初步修复后的对应专项通过 `v207-concurrency-first.log`，恢复、交付、待写队列及并发边界仍须核验：
   - 必需父检查可以并行进行；当其中一项正在复验而另一项提交时，旧的通过记录可能让父任务提前关闭，正在进行的结果随后无法提交。核查 `internal/core/service.go` 的检查领取、提交和 `advance`。
   - 已领取的父级模型或场景检查执行期间，若实际候选变化，提交可能返回 `CANDIDATE_CHANGED`，但没有将真实结果保存为失效的历史证据。核查 `internal/core/assessment.go` 和 `internal/core/service.go` 的提交事务。
   - 最后一次工作区快照和父任务关闭事务之间可能有写入窗口。需要限定 AEL 合作写者能保证的边界，并明确说明不受 AEL 控制的外部写者不能由本地数据库锁完全隔离。核查 `internal/core/service.go`、`internal/core/epic_integration.go` 与工作区写入路径。
2. 待写队列专项已通过；发布回执等待中的恢复修复及前述专项通过 `logs/v207-concurrency-delivery-after.log`。其后隔离 overlay 复现了三条新的恢复死路，见 `logs/v207-writer-review-repro.log`：
   - 旧普通任务已完成却遗留未确认停止的检查时，新父级工作区门禁会等待，但该旧任务无法通过现有 resume、删除或迟到提交清除记录；不可因租约到期假定进程已停止。
   - 父级等待后显式返回 `verify` 清空当前候选，再准备集成时幂等判断错误地返回，候选仍为空。再次返回 `integrate` 可以绕过，不等于已修复。
   - 交付回执已保存、等待中的候选又改变时，核对旧回执的确认无法拒绝或调整，重新准备也被该确认挡住。必须保留旧回执并避免盲目重做交付。
   上述 overlay 位于 `/private/tmp/ael-review-QlB7a8/`；其测试通过意味着**重现了缺陷**，而非验收通过。三条路径已补入本地测试并初步修复，复验通过见 `logs/v207-recovery-after2.log`；仍需最终回归。
3. 可选并行检查 blocked、候选变化覆盖命令退出结论、输入删除导致模型/inspect 结果丢失的失败用例与初步修复见 `logs/v207-evidence-boundary-red.log`、`logs/v207-evidence-boundary-after.log`。检查期间合作写者将候选 A 暂改为 B 再还原 A 的错误通过也已复现并初步修复；写者先后领取及写者记录被删除三种情况见 `logs/v207-transient-red.log`、`logs/v207-transient-after.log`，合并专项见 `logs/v207-concurrency-all-after.log`。人工确认和同一运行库的两个连接并发领取测试通过三轮（`logs/v207-cross-store-first.log`）；不同运行库及不合作外部写者不受同一 SQLite 事务保护，仍须明确边界。仅解决属于本项门禁的必要问题，非任务外非阻断问题只记录。不得伪造结果、删除失败测试或降低门禁。
   最新两条当前任务失败用例见 `logs/v207-parent-workspace-red.log`：父检查在其他绑定工作区执行，以及同一父级多个检查共享可写工作区时，可能错误通过。已初步修复并通过专项 `logs/v207-parent-workspace-after.log`；之前的全量 Go、竞态测试与隔离包不能作为新修改后的最终验收。
4. 当前代码的完整 Go 与竞态测试、离线包构建，以及打包程序三叶和八叶浏览器交互通过，分别见 `logs/v207-go-final.log`、`logs/v207-race-final.log`、`logs/v207-package-final2.log` 和 `logs/v207-accept-final-authorized.log`。仍须对同一构建执行 V2-04/05/06 及普通任务并发回归，并在回执逐条记录 verified/released、反馈完成时间和证据；涉及 UI 的验收须交互真实打包程序。
5. 最终通过后才生成 `receipts/V2-07.md`，保存独立本地 Git 或补丁检查点，更新 `WORKLOG.md` 并检查账本；随后按顺序处理下一任务。当前不存在 V2-07 PASS 回执。

## 已有证据及限制

- 临时证据根目录：`/private/tmp/ael-v2-v200.xLJaSd`。系统可能清理该目录，使用前需核对；日志只证明其执行时的代码，不自动证明后续修改。
- 早期完整 Go 回归：`logs/v207-go-first.log`。早期打包浏览器两项通过：`logs/v207-browser-second.log`；浏览器数据位于 `v207-browser-second/`。这些运行均早于后续修复，不能作为最终验收。
- 先前审查的失败与修复专项：`logs/v207-audit-red.log`、`logs/v207-recheck-red.log`、`logs/v207-recheck-first.log`、`logs/v207-proof-after-recheck.log`、`logs/v207-optional-red.log`、`logs/v207-optional-after.log`。真实测试内容以相应日志和 `internal/core/epic_integration*_test.go` 为准。
- 一次额外只读复核被工具安全过滤中断，未产出可验证结论，不作为通过证据。
- 验收脚本为 `scripts/epic_integration_acceptance.py`，浏览器测试为 `web/tests/epic-integration.spec.ts`。应沿用隔离测试工程与隔离构建目录，不能用本仓库作为 AEL 流程测试项目。

## 恢复步骤

1. 读取 `00-EXECUTION-CONTRACT.md`、`01-TASK-ORDER.md`、`WORKLOG.md` 和账本指定的当前任务；核对 Git 状态及现有改动，不重置、不覆盖用户文件。
2. 在仓库根目录运行 `python3 docs/v2-execution/tools/check_worklog.py docs/v2-execution/WORKLOG.md`；该检查只验证账本顺序与 WIP，不等于产品验收。
3. 按 `WORKLOG.md` 中的下一动作继续，保存复现输入、日志和实际结果。最终打包与回归必须在最后一次相关修复后重跑。
4. 不在本仓库执行 `ael init`，不使用 AEL 的 claim/submit/loop 管理本次开发；不 push、打 tag、发布或部署真实环境。

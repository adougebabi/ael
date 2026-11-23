# 排队归属 · 执行记录

恢复规则：上下文压缩或会话恢复后，先读本文件 → `git diff --stat` → 接续「下一动作」。不回退 docs/full-state-restore 中正在进行的 UI 改动。

## 核对结论（开工时）

- 任务状态：`core.Task`（Status/Step/Paused/Attempts），看板与 next_action 由 `Task.BaseNext` + `nextWithConstraints` 派生。
- 「待处理」= board pending（无 running attempt）；「已领取」= running attempt 尚未上报 stage（`summary.running` 且无进展）；「执行中」= attempt 已上报 progress/stage。
- 远程领取：`runner claim` → `portableStore.claimRunner` → `core.RemoteClaimTx`，在 `Store.Write`（SQLite 串行写事务）内完成；幂等由 `portableStore.mutate`（actor+key+请求摘要）保证。
- 可复用：`metadata` 键值表（已有 `remote-recovery-target:TASK` 接收方限定）、runner 身份（`remote_runners.actor` + 别名）、CLI 持久待提交记录与 `runner retry`。
- 无既有排队/分配能力。Bot 适配代码不在本仓库（外部）。
- 工作区未提交改动：全状态还原 UI（见 docs/full-state-restore/LEDGER.md），本轮只做增量。

## 设计

- 记录：`metadata` 键 `remote-queue:TASK`，含 runner、actor、名称快照、scope（runner|executor）、对应 step/definition/plan/candidate、queued_at/updated_at、随机 version。不改任务正文，不影响 task revision。
- 生命周期：`core.save` 后核对；任务非 open、暂停、出现 running/interrupted attempt、step/定义/计划/候选变化 → 删除并记事件。
- Claim：`RemoteClaimTx` 内校验归属（同一事务）；成功后删除记录。
- 接口：POST `/runners/R/queue|queue-release|queue-transfer`（沿用 mutate 幂等 + CLI 待提交记录）；GET `/queue` 查询。

## 步骤

| # | 内容 | 状态 | 验证 |
| --- | --- | --- | --- |
| 1 | 数据与 CLI/API：登记、查询、释放、转交 | 完成 | `go test ./internal/server -run TestQueue`；`scripts/queue_ownership_acceptance.py` 18/18 |
| 2 | Claim 集成：归属校验、并发、生命周期 | 完成 | 竞态测试 8 轮结果唯一；取消/修订清除；`TestQueueStaleFollowsPendingResponsibility` |
| 3 | UI 标签/筛选/操作入口 + 完整链路验证 | 完成 | `web/tests/queue-ownership.spec.ts` 4/4；workbench-demo 回归 8/8 |

## 验证记录

- Go：`go test ./internal/core ./internal/server` 通过；`./cmd/ael` 仅既有环境失败 `TestRunnerWorkspaceFingerprintIncludesFilePermissions`（umask 077）。
- 竞态 `TestQueueTransferAndClaimRaceHasOneOutcome`：8 轮结果均唯一；本机 8 轮均为转交先成功。「领取先成功 → 转交返回 QUEUE_STARTED」由顺序测试与验收脚本覆盖，未在并发中实际出现。
- 真实 CLI：`python3 scripts/queue_ownership_acceptance.py --binary BIN --out NEW_DIR` 18/18（含服务停机 → AGENT_OPERATION_PENDING → 恢复后 retry）。
- 浏览器：脚本加 `--keep` 后，`AEL_QUEUE_FIXTURE=NEW_DIR/manifest.json npx playwright test --config=playwright.queue-ownership.config.ts`。

## 未完成 / 外部

- 真实 Bot 适配代码不在本仓库，未联调；以上「Bot」均为调用真实 CLI 的模拟客户端。接入点见 assets/skills/ael-work/references/remote-agent.md「排队归属」。
- 不同步 Bot 的队列位置、预计开始时间；无心跳/常驻进程。

## 下一动作

无（本轮完成）。

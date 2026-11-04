# AEL 并发行为与卡点检查

日期：2026-11-04（Asia/Shanghai）。本报告针对源码和离线分发 **0.1.7 / 58efd8c-93214cbf1336**。检查和写入均使用专用临时项目与独立 `AEL_DATA_DIR`，未修改真实业务工程、用户级 AEL 安装或活动服务。

## 已调整并验证

旧版本 `Claim` 将整个规范化工作区当作排他资源；默认工作区是项目根目录，所以即使是不同任务，只要一个任务正在执行或中断，另一个也可能收到 `WORKSPACE_BUSY`。0.1.7 移除这个跨任务门禁：不同任务可以在同一目录同时执行，验证与另一个任务也可以同时领取。代码在 `internal/core/service.go` 的 `Claim`。

- 同一任务的重复领取、租约、过期 token、幂等、进程恢复检查和不同检查的证据边界保留，防止同一个执行单元被重复推进或旧结果串入。
- 新领取不再写入 `workspace_locks`。升级前遗留的旧锁不会阻挡新内核领取其他任务；所属旧 Attempt 提交或确认旧进程停止后恢复时会清理自己的旧记录。旧锁表仍为旧版本兼容和更新维护保护而保留，不会被新内核批量删除。
- 并发不自动隔离文件或外部资源。两个执行者同时写同一文件、共享测试数据库或账号时，AEL 不保证结果可合并，执行者应协调或自行隔离。

## 实测证据

1. `GOCACHE=/tmp/ael-go-cache go test -race ./...` 与 `go vet ./...` 通过。专门回归覆盖同目录不同任务、两个 Store 并发领取、中断任务和旧锁不影响其他任务、验证与其他任务并行、同任务重复领取、过期 token 和更新维护保护。相关并发回归重复运行 20 次通过。
2. `python3 -B scripts/concurrency_acceptance.py --bundle dist --out /tmp/ael-concurrency-20261104-run1`：两个实际 CLI 进程分别启动独立的受管子进程；两者的执行区间重叠 8.39 ms，在同一工作区各自写 `a.txt`、`b.txt`，两个真实命令检查均通过，任务和证据未串。执行器是确定性测试程序，**不是实际模型 Agent**，没有声称验证两个真实 Agent 会话。
3. `scripts/workflow_acceptance.py` 对安装版 0.1.7 的 T01—T12 确定性 CLI 回归通过，其中 T06 同一工作区并发领取通过。这不是全部协议场景的真实 Agent 验收；先前新 Codex CLI 宿主配置阻塞仍未因此消失。
4. `scripts/update_acceptance.py` 在隔离目录实测从真实 0.1.3 更新本地安装和项目到 0.1.7；验证了旧任务运行时拒绝更新、停止并恢复后更新、项目身份和自定义配置/代码/任务记录保留。未在正式项目执行。
5. 前端用已有安装的依赖重新构建，四个平台构建与包校验完成；仅 macOS ARM64 在本机实际运行。没有重装依赖或声称其他平台运行实测。
6. 另外在隔离安装目录验证：由 0.1.5 本机程序直接 `upgrade --from` 0.1.7，再在 0.1.6 项目当前目录执行预览和 `update`，项目启动器实际变为同一 0.1.7 构建。命令与结果在 `/tmp/ael-017-upgrade-compatibility`。

结果及命令记录分别位于 `/tmp/ael-concurrency-20261104-run1`、`/tmp/ael-workflow-20261104-parallel`、`/tmp/ael-update-20261104-concurrency`；临时文件可能被系统清理。

## 仍存在的其他卡点

| 情况 | 依据和影响 | 本次处理 |
|---|---|---|
| 全工作区候选互相影响 | `internal/core/candidate.go` 指纹包括整个工作区的受检文件和 Git HEAD，而非只按任务 scope；`internal/core/service.go` 和 `verify.go` 会拒绝不匹配的候选。隔离 CLI 实测在任务 A 进入验证后改动无关的 `unrelated-b.txt`，A 收到 `CANDIDATE_CHANGED`。只读检查也可能因其他进程改动整个工作区而中断。 | 保留，以免将旧输入的检查冒充新输入的有效证据；需要协调稳定验证时机，或绑定隔离工作区。不能据此声称不同范围的修改一定独立。 |
| 一个文件同步冲突影响其他任务 | `internal/store/store.go` 的 `Flush` 遍历全项目 pending outbox，第一处冲突返回错误；`internal/bootstrap/bootstrap.go` 在 CLI 打开项目时先全局 Flush。隔离库注入一个冲突意图后，实测独立的 `task list` 因 `FILE_CONFLICT` 无法启动，用户修改的文件未被覆盖。 | 记录为尚未修复的跨任务卡点。先审阅冲突文件及待写内容，再通过项目已有修复流程解决；不要为了放行而覆盖用户文件。对于已打开的服务，代码路径中数据库事务可能先提交、后因 Flush 返回错误；这一额外表现是源码分析，本次未单独动态复现。 |
| 较慢的候选扫描延长数据库写等待 | `internal/store/store.go` 每个 Store 使用单连接，写事务用 SQLite `BEGIN IMMEDIATE`；`internal/core/service.go` 的 Submit 在事务内调用 `Fingerprint`，其中扫描文件且同步执行 `git rev-parse HEAD`。在独立夹具给 Git 注入 2 秒延迟，实测另一任务的 CLI 心跳等待 2.107 秒；跨进程 busy timeout 配置为 5 秒。 | 记录为尚未优化的性能卡点。延迟由故障注入产生，不代表实测业务项目必然等待同样时间；超过超时可能出错是源码推断。 |
| 更新维护窗 | 更新会拒绝运行/中断 Attempt、旧工作区锁或待修复 outbox，并在维护期间阻止任务写入；中断的更新还需按恢复流程处理。见 `internal/bootstrap/update_guard.go`、`update_maintenance.go` 和 `docs/updating.md`。 | 保留工具替换的数据保护；它不是普通任务之间的工作区互斥。更新后需重启已有看板或运行器。 |
| 默认看板端口 | 所有项目默认绑定 `0.0.0.0:7331`，第二个看板使用相同端口会监听失败。见 `internal/server/server.go`。 | 源码核查，未在真实服务上试占端口。需要同时运行多个看板时分别指定 `--addr`；不影响任务 CLI 的领取。 |

任务自身的依赖、被暂停、缺失环境/能力、准确版本审批和租约恢复仍可能使该任务等待；这些不是不同任务共享工作区导致的互斥。前端首次获取认证 session 的请求本身没有超时；这可能使该页面的初始请求共同等待，属于源码核查，未单独注入复现。

## 使用新版本

本机当前通过 PATH 找到的全局 AEL 仍为 0.1.5；原有项目及已启动的服务不会因为源码或分发包更新而自动变成 0.1.7。明确选择项目并在维护窗口内，可先升级本机工具，再预览并更新该项目：

```sh
ael upgrade --from '/Users/vinson/Documents/project/个人/ael/dist'
cd '/path/to/your/project'
ael update --dry-run
ael update
```

只有在项目任务安全结束或交接、旧进程已核实停止且更新预览无冲突时再执行更新。项目若有独立看板/运行器，更新后重启，避免旧版代码仍按旧规则拒绝并发。`ael update` 不自动升级其他项目，也不会删除用户自定义文件。

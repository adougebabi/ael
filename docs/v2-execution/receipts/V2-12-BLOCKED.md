# V2-12 · 阻塞现场与恢复依据

本文件记录当前任务的证据和恢复动作；状态仍以 `../WORKLOG.md` 为准。**本项没有 PASS，也没有进入 V2-13。**

## 已完成的当前任务工作

- 基线为本地 `cb0b35e`。已实现待验收的远程 Runner 注册、按身份和工作区领取、租约/fence、诊断进度、候选上报、受租约保护的附件、结果回执和父叶顺序；结果推进复用 `internal/core` 的现有状态机。相关代码、测试和 `scripts/remote_runner_acceptance.py` 保留在本地检查点中。
- **仅在安全缺陷发现之前**，隔离合成进程完成过“修改文件→命令检查失败→修复→检查通过”，并覆盖缺最终结果/产物、未授权工作区和检查、模拟发布命令、场景来源、人工等待、父叶顺序与响应丢失后查询原回执。日志：`/private/tmp/ael-v2-v200.xLJaSd/logs/v212-accept-fourth.log`。这些结果不证明进程隔离安全，也不能作为本项通过结论。
- 已登录的真实 Codex 仅在一次性工程执行了一个 `define` 责任步骤；`thread.started`、`turn.completed` 和结构化结果实际存在，任务仍未完成。日志：`/private/tmp/ael-v2-v200.xLJaSd/logs/v212-codex-smoke.log`；私有执行日志位于 `v212-codex-smoke/runner-runtime-codex/attempts/`。没有声称真实业务任务或外部三机联调成功。
- 复核后修复了检查制品哈希与候选不一致、远程失败证据/诊断日志分类、命令与场景方法歧义及已确认中断后的 Runner 占用同步；专项日志为 `logs/v212-server-final-redfix.log`。现有 Local 状态机和执行器回归通过，见 `logs/v212-local-final.log`；普通附件专项通过，见 `logs/v212-artifact-regression.log`。

## 真实阻塞及安全保护

独立合成复现 `/private/tmp/ael-v212-client-audit/overlay.json` 表明：子进程可另建会话逃离原进程组并在取消后继续写；Runner 崩溃后旧子进程也能继续写；同用户执行者能读取 `0600` 的 lease 文件并编码绕过简单脱敏。Server 的租约和 fence 能拒绝过期提交，但**不能证明本机旧写者停止，也不能把同用户执行者与凭据隔开**。

限定复核在 `/private/tmp/ael-v212-macos-probe-767ebsbr/` 验证：macOS 沙箱可继承文件访问限制，但静态沙箱不会因 Runner 崩溃自动撤销已允许的工作区写入；禁止 `process-fork` 虽能阻断派生，却使普通 `go test` 的编译进程和当前 Codex Node 包装器无法启动。`sandbox-exec` 在默认外层沙箱内被拒绝，提升权限后可运行，但它本身已弃用，实验没有证明可同时满足正常检查、Codex、进程树生命周期和凭据隔离。Linux 未进行实机验证。本机 Docker 服务可只读查询，但把 Docker/额外系统服务作为产品强依赖会改变原任务设定，当前没有已验证、符合原边界的实现。

为避免半成品被当成可安全执行的 Runner，客户端 `runner run` 和 Server 的远程 claim/observe/submit、Runner 附件写入暂以 `RUNNER_ISOLATION_REQUIRED` 拒绝；没有可配置的绕过开关。原来的正向验收测试仍保留并实际失败于该安全门禁，见 `logs/v212-blocked-acceptance.log`；防止误启动的测试通过，见 `logs/v212-fail-closed.log`。完整 `go test ./...` 结果见 `logs/v212-blocked-go-full.log`：两个远程 Runner 正向测试因该门禁失败，其余包通过。不能用之前的合成通过结果抵消这一失败。

## 恢复本项所需动作

1. 为 Runner 建立可在目标宿主实际运行、符合原任务授权边界的隔离：旧执行树在失权或 Runner 崩溃后不能继续影响授权工作区，执行者不能读取 Server profile、lease 或绕过凭据隔离；同时保留正常子进程检查与 Codex 适配。不得仅依赖提示词、同用户文件权限或进程组。
2. 在明确隔离的 fixture 中复现并验证：脱组、父进程崩溃、旧写者、令牌读取/编码、检查产物候选哈希、过期 fence、失败类别/日志不重置计数、网络回执重放和父计划顺序；不改低断言或删除现有正向测试。
3. 证明上述隔离后才移除两处 fail-closed 判断，重跑保留的正向 Runner 验收、真实 Codex 条件检查、Local Managed/Attached 和完整 Go 回归；写独立 PASS 回执及最终本地检查点，然后才可进入 V2-13。

保留现场：`/private/tmp/ael-v2-v200.xLJaSd/v2-12-blocked.tar` 及其 `.files` 清单；该路径为临时目录，可能被系统清理。没有 push、tag、发布、真实 Oracle 部署或业务工程切换。

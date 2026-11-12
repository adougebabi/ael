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

## 继续执行后的现场（仍未 PASS）

- 继续时本地 HEAD 为 `405d33b`，其上保留的未完成控制目录拆分已接通：`control/claims` 与 `control/attempts` 保存领取、租约、待提交内容和回执，`io/attempts` 保存执行器输入输出。新的待提交内容不重复保存租约 token；重放时从保留的 lease 补入。新旧路径可恢复，旧 attempt 已存在时拒绝新建，保留原现场。控制目录拆分仍不能阻止同用户进程读取，不能作为隔离证明。
- 进程记录包含 PID、可获得时的进程身份、身份状态及 `descendants_unknown`；主进程退出时立即检查原进程组并阻断同组后台子进程。输出按完整行先脱敏再落盘，识别跨写入和跨行凭据，超长输出保留脱敏后的安全前缀。未完成的一行在 Runner 非正常退出时可能丢失；进程组检查不能发现已脱组后代，也不证明失权写者已经停止。
- 复核先发现三项回归并实际保留失败：旧 attempt 被重复建立、短命后台子进程漏检、运行中日志缺失；另有跨行脱敏与超长单行丢失的失败。相应修复及测试已通过，原始记录分别为 `../evidence/V2-12-resume/review-regressions-red.log`、`stream-red.log` 和 `stream-green.log`。
- 最新相关回归：`GOCACHE=/private/tmp/ael-v212-go-build go test -race ./cmd/ael ./internal/execution -count=1` 通过，需允许本机回环端口；记录 `../evidence/V2-12-resume/cli-execution-race.log`。本轮较早运行的 `GOCACHE=/private/tmp/ael-v212-go-build go test ./... -count=1` 中，`TestRemoteRunnerClaimIsOwnedFencedAndIdempotent` 与 `TestRemoteRunnerSubmitsFinalizedAttemptThroughTaskStateMachine` 仍因 `RUNNER_ISOLATION_REQUIRED` 失败，其余包通过；原始记录 `../evidence/V2-12-resume/go-full-before-final-fixes.log`。该全量结果发生在上述最终日志修复之前，不能当作最终全量通过。
- 可选容器限定探针：`../evidence/V2-12-resume/container-controlled-probe.py` 使用本机已有 Alpine 镜像、临时目录、禁网和无额外能力。`container-parent-results.json` 记录对照组和父进程崩溃：写者已确认脱组后，普通容器仍运行并发生旧写入；专门监听 stdin EOF 的 PID 1 退出后未观察到旧写入。`container-client-results.json` 另记录直接终止 Docker 客户端时的同类单次观察。每个测试只清理自己的容器。最早的单次探针因写者启动竞态和状态查询解释不严谨，不能作为证明。
- 这些容器结果仅覆盖本机受控故障场景。测试镜像没有 Go、Git、Codex 和 Node，禁网也不能直接满足真实 Codex；尚未验证凭据隔离、租约失权、工作区交付、网络授权、正常检查和真实 Codex，不引入 Docker 作为产品强依赖，也未解除客户端或服务端门禁。
- 保留的实施改动是未验收的本地检查点；不得因上述局部测试通过将 V2-12 标记 DONE，也不得开始 V2-13。恢复仍从 WORKLOG 所列本项动作开始。

## 本轮原生隔离限定复核（仍未 PASS）

- 从 `781dc1c` 的干净工作区继续，只改本项账本和证据，没有修改产品代码或后续任务。先后进行本机临时 APFS、macOS sandbox 与 Codex 直接命令沙箱合成探针；完整原始结果和边界见 `../evidence/V2-12-native/README.md`。
- APFS 强制卸载**完成后**，本次两个已脱组写者持有的旧普通文件/目录句柄与旧 cwd 操作失败，重新挂载只读到卸载前内容；绝对路径的 `EACCES` 同时受下层挂载点目录权限影响。外层默认沙箱中映像创建失败，允许本机系统操作后完成试验。实验未验证写入与卸载并发，不能把它当作实时撤销证明。
- 单独 sandbox fixture 在修正规则后通过最小离线 `go test` 和 `git status`，拒绝读取指定的假控制文件/映像、修改另一合成目录及向自有监督进程发信号；故意继承的合成控制文件 FD 仍可读。组合崩溃探针的重新挂载时间线中有两条写入起始时间戳晚于监督器 EOF；该脚本没有逐次记录系统调用完成时间，且异常清理路径需要加固，不能当作可直接复用的产品隔离器。
- 本机 APFS 的 `mount -u -r -f` 因不支持 `force` 失败，未验证只读重挂载后的旧 FD 行为；对已打开文件设置 `UF_IMMUTABLE` 后，`pwrite`、`ftruncate`、既有共享 mmap 写调用未报错，沙箱进程还能清除文件标志。并发调用没有逐项重新挂载核对最终字节。
- 临时 `0600` 假租约、临时 Git 仓库和回环正向对照表明：直接运行 `codex sandbox --permission-profile :workspace` 的进程可读取并编码该假租约，普通仓库创建标签和回环连接本次被拒绝。它没有运行 AEL 实际 `codex exec` 路径或使用真实认证材料，不能推广为全部发布和网络能力的证明。
- 本轮本机系统手册与 SDK 还显示普通文件不支持 `revoke(2)`，`NOTE_TRACK` 不再支持；这些发现没有消除上面的实测窗口。三个测试映像均报告成功卸载；随后只读查询本轮已知的三个旧写者 PID、按记录路径筛选这三张映像，均未发现匹配。合成文件和映像留在记录的临时目录供排查。
- 结论仍为 BLOCKED：没有放宽验收或解除 fail-closed，不进入 V2-13。若恢复，应先找到可验证的宿主隔离和失权写入控制，再覆盖 Codex、授权、证据及原正向闭环；不要直接重跑尚未加固异常清理的探针脚本。

## 按原任务边界恢复实施（进行中）

- 用户询问处理方式后重新核对原契约：它明确要求隔离工作区、失权后不开始新写操作、过期提交拒绝，并提醒失联不等于旧进程已停止；并没有指定崩溃瞬间由操作系统杀死所有后代，或要求防御同用户任意恶意进程。此前将这些更强的实现假设一并作为硬门槛，范围过大。原始安全事实、失败用例和门禁仍保留，不以此宣布任务通过。
- 新增尚未接入 Runner 循环的独立 Git 副本创建与测试；普通暂存、未暂存、删除、未跟踪、规范索引和写入分离已在临时仓库验证。被排除的已跟踪运行时文件、特殊索引标志、含 include/worktree 的 Git 配置、未捕获 filter/attributes 等情况会明确拒绝；保留了先失败再修复的日志 `../evidence/V2-12-scope-recovery/workspace-audit-red.log`、`workspace-filter-red.log` 和 `workspace-filter-green.log`。上下文实现已支持身份项目与文件工作区分离，并用假服务验证，但 Runner 循环仍使用原路径。
- 新增本机保守租约截止与实际发送前检查：续租阻塞不会延长本机执行；在途 HTTP 请求随失权取消、在续租后可继续；证据分块与待提交结果恢复覆盖了过期情形。Server 心跳增加签发时间，旧 Server 缺少该字段时新客户端会拒绝并提示升级。真实回环测试、受控时间测试、Server 心跳契约和竞态回归见 `../evidence/V2-12-scope-recovery/lease-final.log`、`server-heartbeat.log`、`latest-cli-remote-knowledge-race.log`、`cli-remote-execution-race.log` 和 `server-race.log`。睡眠后的轮询检测仍有约一秒及调度延迟，不能把它说成即时停写或旧进程已停止。
- 已脱组的合成旧进程继续向旧副本写相对路径时，原 checkout 和从已保留副本创建的新副本未受影响，见 `relative-writer.log`；测试不证明同用户进程无法主动访问其他路径。发现的现有便携候选索引执行位歧义记为 `../NON-BLOCKING-ISSUES.md` 的 NB-010，未顺手修改原候选协议。
- 仍未完成：将副本与领取、候选/规范、执行、制品、Server 回执、断线恢复和原 checkout 安全交付接成一条链；补齐必要的失权、旧写者及未授权发布验收。客户端和 Server 总门禁仍关闭，两个原远程正向测试仍未重新通过。本项状态以 `../WORKLOG.md` 为准，不得进入 V2-13。

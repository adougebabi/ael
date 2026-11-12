# AEL V2 实施账本

> 本账本初始状态不代表任何实施已开始。只有实际执行者才能根据代码和验收结果更新状态。
> 状态仅允许 PENDING、ACTIVE、VERIFYING、DONE、BLOCKED。

当前任务: V2-12

| ID | 任务 | 状态 | 回执 |
|---|---|---|---|
| V2-00 | 基线核对与串行实施账本 | DONE | receipts/V2-00.md |
| V2-01 | CLI 默认可读，JSON 显式选择 | DONE | receipts/V2-01.md |
| V2-02 | 父任务与叶子任务：最小可用任务树 | DONE | receipts/V2-02.md |
| V2-03 | 拆分计划：复用用户分项与 Agent 辅助补全 | DONE | receipts/V2-03.md |
| V2-04 | 顺序推进与后继领取 | DONE | receipts/V2-04.md |
| V2-05 | 大任务进度与工作台接通 | DONE | receipts/V2-05.md |
| V2-06 | 停滞检测、重复失败与可恢复停止 | DONE | receipts/V2-06.md |
| V2-07 | 父级集成验收与整体验收门禁 | DONE | receipts/V2-07.md |
| V2-08 | 独立 Server 与远程身份边界 | DONE | receipts/V2-08.md |
| V2-09 | 远程任务与 CLI：唯一权威的第一条路径 | DONE | receipts/V2-09.md |
| V2-10 | 中心证据存储与授权上传下载 | DONE | receipts/V2-10.md |
| V2-11 | 跨设备候选、规范快照与执行上下文 | DONE | receipts/V2-11.md |
| V2-12 | 远程 Runner 的真实单任务闭环 | BLOCKED | receipts/V2-12-BLOCKED.md |
| V2-13 | 双执行者协作、断网与恢复 | PENDING | — |
| V2-14 | 统一远程 Dashboard 与节点可观测性 | PENDING | — |
| V2-15 | 结构化验收发现与问题归并 | PENDING | — |
| V2-16 | 截图比较、人工结论与版本保护 | PENDING | — |
| V2-17 | 发现 → 修复任务 → 复验闭环 | PENDING | — |
| V2-18 | 发行包与可核验的版本清单 | PENDING | — |
| V2-19 | CLI 与项目资产的轻量升级 | PENDING | — |
| V2-20 | Server 部署包、备份与安全升级 | PENDING | — |
| V2-21 | 旧本地状态导入与单权威切换 | PENDING | — |
| V2-22 | 打包后的全链路验收与交付核对 | PENDING | — |
| V2-23 | 授权后的 Oracle 部署与实际双机验收 | PENDING | — |

## 当前现场

- 本地工作目录：/Users/vinson/Documents/project/个人/ael
- 当前分支 / 基线提交：main / c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4；开工工作区干净。
- 当前任务未完成的验收项：按原契约建立稳定隔离的执行工作区，失权后不得启动新的写操作或发布，过期提交须拒绝；接通真实单任务、父叶、权限、Codex 条件及完整回归。
- 最近实际进展：此前的隔离 fixture 和单步真实 Codex smoke 及后续发现的脱组、崩溃后旧写者和 lease 泄漏见 `receipts/V2-12-BLOCKED.md`。继续工作后完成控制区与执行器 I/O 布局、主进程身份及“后代未知”记录，修复新旧 claim/提交恢复；复核发现的旧 attempt 重建、同组后台子进程漏检、日志脱敏/诊断回归均有保留的失败用例及通过记录。最新 CLI 与旧执行器完整竞态回归通过。进程身份和目录分离不证明宿主隔离。
- 范围复核：原契约要求隔离工作区、失权后不得开始新写操作、过期提交拒绝，并明确失联不等于旧写进程已停止；并未指定崩溃瞬间必须由操作系统杀死所有后代，也未逐字要求抵御同用户任意恶意进程。先前回执把后两者作为更强的实现门槛；相关实际风险和证据仍保留，不能以改写描述或配置布尔值绕过。`runner run` 和 Server 写入仍 fail-closed，正向验收测试仍保留。
- 最近限定复核：APFS 强制卸载返回后，两个脱组写者的旧文件/目录句柄操作失败；但组合崩溃实验中，重新挂载的时间线仍有两条写入起始时间戳晚于监督器收到 EOF，卸载返回后没有记录新的起始时间戳。只读重挂载命令失败，`UF_IMMUTABLE` 未阻止已打开文件的若干写调用。另一个合成 fixture 中，Codex 的直接 `:workspace` 命令沙箱可读取 `0600` 假租约；该探针不是实际 `codex exec` 全链路。探针的范围和清理限制见 `evidence/V2-12-native/README.md`。
- 副本小步：已新增独立 Git 副本创建与 fixture 验证，覆盖暂存/未暂存/删除/未跟踪、规范索引、未知输入和原 checkout 写入分离。独立复核发现被排除的已跟踪运行时文件、特殊索引标志、换行转换配置、不可读规范及 Git filter 调用顺序问题；对应失败用例现已通过，暂不支持安全复制的情况会在创建前明确拒绝。上下文读取已拆分身份项目与文件工作区，在本机假服务上验证读取隔离副本、远程 profile 仍绑定原项目。复制结果只证明受信执行者的目录副本，不是操作系统安全沙箱。已有便携候选 ID 的索引执行位歧义记为 `NON-BLOCKING-ISSUES.md` 中 NB-010，副本另比较完整索引，不能称作已修复候选协议。
- 租约小步：Server 心跳返回签发时间与到期时间，客户端从该区间及本机请求开始时间计算保守截止时间；监视器在续租请求卡住时仍会取消本地执行，受租约保护的 HTTP 写入和证据分块在实际发送前核对截止时间，在途请求随失权取消、随成功续租延长。重放待提交结果先查询原回执，未找到时须先成功续租；临时回环假服务的延迟、过期 fence、分块和恢复竞态测试通过。旧 Server 缺租约区间时会拒绝并提示升级；机器睡眠后仍有最长约一秒加调度延迟的检查窗口。这不证明旧脱组进程已停止，也不代替 Server 的 fence 检查。
- 最新小步：Runner 内部循环已接入独立阶段副本、候选/规范读取、制品和回执匹配；临时合成 Server 与真实 fixture 子进程跑通 define → execute 两步，服务端回执确认后保留结果副本，原 checkout 尚未回写。已补回执引用缺失、journal 丢失、计划先落盘、已保存回执及原操作键恢复测试；合成服务依次丢失上传开始、分块、finalize 和提交响应后仍以相同键与字节恢复、最终只提交一次。真实 Server 的公开入口仍被安全门禁关闭，不能把这些测试称为本项端到端验收。
- 范围收敛后的现场：工作树还保留未接线、尚未完成验证的原 checkout 交付尝试，以及提交计划、日志脱敏和恢复相关改动；上次仅执行 Go 编译检查，不能称为测试通过。已有验收脚本要求在隔离 fixture 的原 checkout 中看到修复，交付尝试仍须先验证其不覆盖已有修改并检查实际接线。此前额外的宿主级隔离实验不再扩展，已记录的风险与失败测试继续保留。
- 交付 fixture 核验：首次运行两个测试均因测试夹具没有保存回执而失败；补上和真实流程一致的私有回执后，`go test ./cmd/ael -run '^TestRunnerDelivery' -count=1` 通过，日志 `evidence/V2-12-scope-recovery/delivery-fixture.log`。这仅覆盖已有暂存/未跟踪状态和交付前用户编辑，不代表交付已接线、消除并发竞态或通过真实 Server 验收。
- 完成动作现已接线：Server 回执匹配后尝试将结果交到授权 checkout；原 checkout 冲突或回执缺失时保留副本并返回待交付。临时仓库中保留了接线前失败 `delivery-stop-red.log`，随后五项交付/停止测试通过，见 `delivery-stop-green.log`；测试中的 macOS `/var` 与 `/private/var` 路径等价问题也已修正。该局部结果仍不证明真实 Server 闭环或消除写入期间的并发竞态。
- 真实 Server 定位：仅为找出后续接线故障，在 `/private/tmp/ael-v212-diagnostic-sevow5le/` 构建了通过 Go overlay 暂时绕过两处门禁的诊断程序，未改产品门禁；无论测试结果如何均不是正式验收。首次运行因本机沙箱拒绝回环监听而停止，见 `real-server-diagnostic.log`；获准回环监听后的诊断通过了单任务、响应丢失恢复及后续场景，最终父任务运行超过原测试的 90 秒并失败，见 `real-server-diagnostic-loopback.log`。已检查该 Runner 已记录的十个直接执行进程均退出；继续定位耗时，不放宽超时。补充了验收脚本对隔离副本内未授权命令标记的检查，新断言尚未运行。
- 父任务超时定位：在同一独立交付 fixture 上记录到 1,062 次 Git 子进程；去掉候选的嵌套重复采样并合并本地配置查询后降至 542 次，仍保留两次完整样本对比。样本间修改拒绝、原有工作区/交付专项与 `internal/remote` 回归已通过，见 `git-calls-before.log`、`git-calls-final.log`、`workspace-delivery-optimized.log` 和 `portable-candidate-regression.log`。保持原 90 秒限制重跑诊断后父任务仍超时，但执行步骤从 5 个增至 8 个，见 `real-server-diagnostic-optimized.log`；已检查记录的执行进程均退出。进一步合并工作区复制中的重复候选读取，保留复制前源、复制结果和复制后源三者一致的检查，并增加复制期间源文件变化测试，专项验证进行中。所有诊断版结果仍不代替正式验收。
- 复制专项已通过 `workspace-delivery-copy-optimized.log`，并补测无值的无关 Git 配置；该优化修复后第三次真实 Server 诊断按原 90 秒上限完整跑过独立 fixture，包括新增的副本内未授权命令断言，结果见 `real-server-diagnostic-copy.log`。此程序通过临时 overlay 绕过两处门禁，且未请求 Codex smoke；正式入口、正式验收和失权/凭据风险仍未解决。
- 当前产品代码的完整 Go 回归见 `go-full-current.log`：`cmd/ael`、`internal/execution` 及其他非 Server 包通过；Server 的 `TestRemoteRunnerClaimIsOwnedFencedAndIdempotent` 与 `TestRemoteRunnerSubmitsFinalizedAttemptThroughTaskStateMachine` 仍因 `RUNNER_ISOLATION_REQUIRED` 返回 503 而失败。该结果发生在上述配置兼容修复之后，不能以诊断版通过抵消。
- 在一次性工程补跑了已登录真实 Codex 的一个 `define` 步骤，Server 任务仍为 open，输出与私有执行记录见 `codex-diagnostic.log` 和其中所列临时路径；此 smoke 使用同一个绕过门禁的诊断程序，跳过其他验收场景，不是正式产品入口的通过证据。
- 原任务边界的独立限定审查：Server 在暂停或定义变化时可先拒绝责任，而 Runner 要到续租时才发现，提前撤权窗口目前是代码推导、尚未按真实链路复现；原 checkout 的交付发生在结果被 Server 接受之后，目前没有明确区分交付授权与已结束的 attempt 租约。直接命令和配置中的检查有授权匹配，但间接调用发布与未标注的其他凭据输出尚未被现有测试证明安全；已脱组旧写者可写旧副本的实验证据也仍在。上述事项不能通过声称强制杀死全部后代是原需求来处理，也不能通过诊断通过就忽略；正式门禁保持关闭，具体代码位置和恢复边界补记在当前回执。
- 原验收缺陷已实测：本地明确许可 `make test` 的检查时，fixture 的 Makefile 间接执行 `git push`，测试用假 Git 记录到启动并以 42 退出；没有执行真实发布。失败测试源码 `evidence/V2-12-scope-recovery/indirect-publish-test.go.txt`，Go overlay 及输出见 `indirect-publish-red.log`。进一步分别用 PATH 查找和绝对路径调用假 Git，两个测试均失败，见 `indirect-publish-paths-red.log`；这也说明仅在 PATH 前插拦截器不足以覆盖两种普通调用方式。该缺陷必须修复或保留阻塞，不能仅根据顶层参数黑名单或将它列为任务外问题而解除门禁。
- 当前阻塞与恢复动作：获准检查通过子进程启动未授权发布已在隔离 fixture 中实测；顶层黑名单与 PATH 包装均不能覆盖绝对路径，仓库中没有可直接复用的受验证执行约束提供者。先找到不改变原任务设定、能约束检查及其后代且仍能运行正常检查与 Codex 的实际机制，用保留的两个间接发布失败用例验证；同时处理提前撤权、旧写者、交付授权及凭据边界。只有实际验证后才能调整门禁，重跑正式真实 Server 单任务、Codex smoke 和完整回归。用户已指定 V2-12 完成后暂停，不得自动启动 V2-13。

## 修订记录

初始任务包创建；尚未执行仓库修改或产品测试。

- V2-00 完成：基线、隔离构建和账本检查结果见 receipts/V2-00.md。
- V2-01 完成：输出协议、实际验证与旧版本升级限制见 receipts/V2-01.md。
- V2-02 完成：任务树、兼容性和验收结果见 receipts/V2-02.md。
- V2-03 完成：计划预览/应用、恢复验证及宿主限制见 receipts/V2-03.md。
- V2-04 完成：顺序领取、失败修复和 checkpoint 验证见 receipts/V2-04.md。
- V2-05 完成：大任务汇总、工作台、范围变化及打包浏览器验收见 receipts/V2-05.md。
- V2-06 完成：停滞判断、限定复核、安全恢复及打包验收见 receipts/V2-06.md。
- V2-07 完成：父级候选、集成与交付门禁、三叶及八叶打包验收见 receipts/V2-07.md。
- V2-08 完成：独立 Server、账户与项目授权、浏览器登录和打包重启验收见 receipts/V2-08.md。
- V2-09 完成：远程单权威任务、计划和双客户端验收见 receipts/V2-09.md。
- V2-10 完成：中心附件、受权预览、清理恢复与双客户端验收见 receipts/V2-10.md。
- V2-11 完成：可移植候选、规范快照、检查报告与跨端验收见 receipts/V2-11.md。

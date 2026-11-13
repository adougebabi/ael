# AEL V2 实施账本

> 本账本初始状态不代表任何实施已开始。只有实际执行者才能根据代码和验收结果更新状态。
> 状态仅允许 PENDING、ACTIVE、VERIFYING、DONE、BLOCKED。

当前任务: V2-21

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
| V2-12 | 外部 Agent 主导的远程工程闭环 | DONE | receipts/V2-12.md |
| V2-13 | 双 Agent 客户端协作、断网与责任恢复 | DONE | receipts/V2-13.md |
| V2-14 | 统一 Dashboard 与协作状态可观测性 | DONE | receipts/V2-14.md |
| V2-15 | 外部 UI/UX 发现接入与问题归并 | DONE | receipts/V2-15.md |
| V2-16 | 证据比较、人工结论与版本保护 | DONE | receipts/V2-16.md |
| V2-17 | 发现、修复任务与外部复验闭环 | DONE | receipts/V2-17.md |
| V2-18 | Server 与薄客户端的发行包和版本清单 | DONE | receipts/V2-18.md |
| V2-19 | CLI 与项目资产的轻量升级 | DONE | receipts/V2-19.md |
| V2-20 | Server 部署包、备份与安全升级 | DONE | receipts/V2-20.md |
| V2-21 | 旧本地状态导入与单权威切换 | PENDING | — |
| V2-22 | 打包后的全链路验收与交付核对 | PENDING | — |
| V2-23 | 授权后的 Oracle 部署与实际双机验收 | PENDING | — |

## V2-12 暂停检查点（历史）

V2-12 已 DONE，正式回执 `receipts/V2-12.md`，证据 `evidence/V2-12-agent-driven/`。本轮暂停于 V2-12；V2-13—23 仍 PENDING、没有开始。下面保留的是旧托管实施历史，旧隔离/间接发布/自动回写恢复要求已由本次明确产品边界替代，不作为当前执行入口。

## 历史现场（修订前原文保留，不作当前恢复指令）

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

## AD-AGENT-DRIVEN-01 当前恢复（取代上方历史恢复指令）

- 2026-11-12：基线 7f88a84，工作区干净；完整包存入 docs/v2-agent-driven-revision。旧隔离/托管失败仍为历史事实，不再是通信前置。
- R12.1：归档原契约、全部任务正文和账本；V2-00—11 保留 DONE，V2-12—23 采用修订正文。唯一当前项 V2-12，结束即暂停。
- 依次完成 R12.2 薄客户端、R12.3 Skill、R12.4 正式验收、R12.5 回执检查点；未经最终验收不标 DONE。

- R12.2 已通过：独立外部驱动正式 CLI 完成 define → execute → verify fail → repair execute → verify pass；结果 /private/tmp/ael-v212-agent-r12-2/summary.json。旧托管三个入口明确不支持。
- R12.3：分发 Skill、workflow、commands、CLI help、README 和当前验收入口统一为外部 Agent；结构化报告保留方法、来源、候选、版本、覆盖与实际观察。

- R12.4 最终通过：正式外部驱动失败修复/后继/越权/降权/报告负例/实际响应丢失、V2-11 候选/规范/浏览器、V2-09/10、稳定 Local 回归通过；最终 Go 全量、相关 race、vet 通过。NB-010 候选执行位漏洞已修复；NB-011 仍为断电级持久性未验证限制。证据 evidence/V2-12-agent-driven/。
- R12.5：V2-12 DONE，正式新回执 receipts/V2-12.md，旧 BLOCKED 和失败不覆盖。检查点为包含此记录的本地 Git 提交（以 git log 核对）；不 push/tag/发布。当前任务保留 V2-12 并显式暂停，V2-13—23 PENDING，未开始。

## V2-13 实施记录

用户授权开始 V2-13，基线 5558b5d，初始工作区干净。当前唯一 ACTIVE 为 V2-13。先补双身份 HTTP 验收与断网恢复，再完成权限/候选/证据及全量回归。复用 V2-12 正式通信链路。

- 已补齐 runner retry 私有操作记录、原始请求摘要核对、固定上传快照、原责任恢复，以及 runner recover 工作区声明/未知状态保留。双身份真 HTTP、实际响应丢失/重启、并发领取、分支真实截图、取消/修订负例已通过开发构建。独立复核发现的跨 checkout 幂等回执混用和历史实施覆盖活跃验收已修复并保留负例。正在正式构建与最终回归。

## 当前现场

V2-13 已 DONE，正式回执 receipts/V2-13.md，证据 evidence/V2-13/。最终 Go 全量、相关 race、vet 与正式分发双身份 HTTP、故障恢复、分支截图、Local/上下文/附件回归通过。本地检查点为包含此记录和回执的 Git 提交。下一编号 V2-14 保持 PENDING，未实施。

- 最终补丁验收通过，接收方领取前失联可以显式改派，原接收方的旧领取受拒绝。最终全量与 race 记录已替换为补丁后结果。V2-14 仍 PENDING。

## V2-14 当前实施

用户授权持续完成全部后续任务。基线 03de32f，初始工作区干净。当前唯一 ACTIVE 为 V2-14。统一远程页面、授权读取、游标恢复、客户端标签与独立责任/联系/宿主状态已实现。

- 开发构建实际浏览器已通过双客户端更新、断网恢复、旧响应、Server 重启、父子/证据/规范往返、筛选恢复、暂停取消和读者权限。
- 独立复核指出待处理计数未刷新及历史规范链接未绑定上下文，已修正并增加回归。远程视图跳过本地接手草稿恢复，避免展示其他项目草稿。
- 正在执行最终正式分发、原登录/附件/规范入口、Local 和全量回归。
- 文档同步脚本误写本账本后，依据本轮基线 03de32f 恢复全部原文，并重新追加本项实际进展。已完成任务状态和原回执保持原始记录，账本检查器继续核对顺序及 WIP。

- V2-14 最终通过，回执 receipts/V2-14.md，证据 evidence/V2-14/。最终正式二进制的真实浏览器、全量 Go、相关 race、vet 与原认证/附件/规范/Local/V2-13 回归通过。下一项 V2-15 保持 PENDING，检查点保存后立即继续。

## V2-15 当前实施

基线 3d52103，开工工作区干净。V2-14 已形成 PASS 回执与本地检查点。当前唯一 ACTIVE 为 V2-15，先复用检查与附件入口接入结构化发现，再完成归并、列表/详情及正式外部上报与浏览器验收。

- V2-15 开发构建已通过正式 CLI 的同版本归并、新出现/原键重放、不同定位、UI/UX 缺项、模型/场景/会话来源、候选变化、跨候选证据拒绝及批次原子性。实际浏览器可筛选并查看原始报告/截图/版本。正在最终正式构建和回归。

- V2-15 最终通过，回执 receipts/V2-15.md，证据 evidence/V2-15/。正式 CLI/API/浏览器、全量 Go、相关 race、vet 及原 context/附件/工作台回归均通过。下一项 V2-16 当前 PENDING，检查点后继续。

## V2-16 当前实施

基线 b7c0d7f。V2-14 与 V2-15 已完成正式验收及独立本地检查点。用户“继续直到所有都完成”的目标保持 active。当前唯一 ACTIVE 为 V2-16，已完整读取本项要求，下一步核对既有人工结论内核、远程 artifact 绑定和比较界面，再逐项实现及验收。V2-17—23 保持 PENDING。

- V2-16 已接入远程会话人工 pass/fail/blocked、非门禁意见及精确恢复，共享既有输入校验和 submit 状态机。领取、结果和幂等回执使用同一事务，失败回滚。比较界面支持显式基准/实际、缩放、叠加、定位、来源和草稿。独立核验修复旧输入观察误挡已接受输出、远程 blocked 恢复及比较降级。领域/HTTP 定向测试通过；首轮全量 Go 通过。正式双客户端夹具已通过 HTTP 三结论与重放，浏览器首轮定位到嵌套选择器标签匹配问题，正在修正后的打包复验。本项仍 ACTIVE，V2-17—23 PENDING。

- V2-16 A16-01—05、八项打包浏览器、Local 59 项全套界面、全量 Go、相关 race 和 vet 已通过。当前为最终同二进制协议回归与回执/检查点收口，V2-17—23 仍 PENDING。

- V2-16 最终 PASS，回执 receipts/V2-16.md，证据 evidence/V2-16/。八项正式浏览器、Local 59 项、全量 Go、相关 race、vet 与旧协议回归通过。旧候选意见在界面显示过期并清除旧关联，原输入保留。检查点为包含本记录的本地提交，下一项 V2-17 仍 PENDING，提交后继续。

## V2-17 当前实施

基线 a4b4b2cd78211c2ef760d4297d7c1538aece1e85，开工工作区干净。V2-16 已完成正式验收、完整相关回归、回执和独立本地检查点。当前唯一 ACTIVE 为 V2-17，先核对发现、任务关联、父级范围与授权的既有规则，再接通裁定、幂等修复关联和外部新候选复验。V2-18—23 保持 PENDING。

- V2-17 第一步已抽出共享任务创建/父子关联的事务入口，保留原规范化、政策、版本、计划预留、历史定义与必需叶子规则。新增回滚测试证明关联失败时任务、反馈、成员关系、文件投影与回执一起回滚；原 Create/Epic/Plan/Feedback/Shadow 相关回归通过。下一步在同一事务接入发现裁定与修复关联，再接有效新候选复验。

- V2-17 裁定、唯一修复创建/关联、精确新候选复验、旧结果与复发关系、来源上下文和实际演进事实已接通 CLI/API/界面。定向领域/权限/回滚/政策兼容测试通过，开发正式分发已由外部宿主完成 B/C 两候选真实修改、提交、浏览器与命令、历史复验和复发场景。修正了只验收执行报告的源码文件误门禁。当前在最终分发与新旧全量回归，V2-18—23 PENDING。

- V2-17 最终 PASS，回执 receipts/V2-17.md，证据 evidence/V2-17/。正式四场景贯通实际 B/C 实施与检查、复验、原失败和复发，Local 59 项、V2-16 八项及旧协议、全量 Go、相关 race、vet 通过。检查点为包含本记录的本地提交，V2-18 当前 PENDING，提交后继续。

## V2-18 当前实施

基线 b81aa06496a06112bb2aa7f96adb1c936140ba78，开工工作区干净。V2-17 已完成正式验收、相关全量回归、回执和本地检查点。当前唯一 ACTIVE 为 V2-18，已读取本项要求，先核对现有离线包/安装/版本与 CI 入口，再接实际 Server/薄客户端/Skill 清单与可信发行选择。V2-19—23 PENDING。

- V2-18 初始核对已完成，记录 evidence/V2-18/initial-inventory.md。现有打包/安装只支持完整四平台离线包，缺独立发行清单和 CI；已只读确认本机 OrbStack Docker 29.8.2 linux/aarch64 可用，并通过 Context7 核对 Cosign 3.1.4 的 bundle 与固定工作流身份验证。尚未实现本项发行功能或创建镜像，下一步接独立交付和 manifest。

- V2-18 已接独立当前平台/四平台 CLI、Server、Skill 和 manifest，真实 OpenSSH 签名及 HTTP 负例通过。本机独立非特权镜像通过登录/权限/Origin 和重建持久化。独立复核修正草稿转公开、预发布标记及镜像跨卷导出。离线 gh 六场景和旧四平台实际安装通过，Go 全量通过。正在最终相同产物的新旧回归及回执收口，V2-19—23 仍 PENDING。

- V2-18 最终 PASS，回执 receipts/V2-18.md，证据 evidence/V2-18/。最终十项发行资产、正式 CLI 签名源十一场景、离线发布六场景、镜像导入/登录/持久化、Local 59 项和全部相关旧协议/浏览器、全量 Go、相关 race、vet 通过。磁盘耗尽失败保留，清理可重建缓存后全量重跑通过。本地产物 local_unpublished，真实 GitHub/Oracle 和其他架构实机未验证。检查点为包含本记录的本地提交，下一项 V2-19 当前 PENDING，提交后继续。

## V2-19 当前实施

基线 eb63016f5daedd94b33546a7425d00fb99000208，开工工作区干净。V2-18 已完成正式验收与独立本地检查点。当前唯一 ACTIVE 为 V2-19，先核对旧安装/更新事务、项目连接和协议元数据，再接当前平台机器级升级、轻量入口与精确版本检查。V2-20—23 保持 PENDING。

- V2-19 第一小步已接单平台客户端包到原机器级 Upgrade 事务，使用独立 clients/<version> 不可变缓存。明确版本/dry-run、源删除后的缓存复用、篡改保持旧工具、Local 完整包拒绝单平台源及旧 Upgrade 全部定向测试通过。下一步接正式 CLI 的版本检查/下载选择与安全中断恢复，再处理 Remote 轻入口。

- V2-19 已贯通独立包安装、明确版本/目标前缀检查、签名在线源、旧版协议兼容、实际 executable 协议绑定、机器安装中断恢复和 Remote 轻入口。开发分发真实 0.1.18→0.1.19 升级、两项目共享 CLI、入口 hash 保留、实际 kill updater/权限失败、旧备份与旧缓存可运行通过。只读复核指出的目标安装检查、协议绑定、大 AGENTS 日志和 GET 握手四处已修正并保留失败/通过用例。正在冻结后正式打包及最终全量回归，V2-20—23 仍 PENDING。

- 最终旧协议回归定位到新增 --check 全局布尔注册误吞 context record-check 的检查 ID。已限定维护命令的布尔解析并保留原 value 参数，新增回归覆盖 context/verify 的两种赋值和 version/upgrade 的布尔用法。原正式失败与单元失败均保留，继续同一 V2-19 复验，不开始 V2-20。

- V2-19 最终 PASS，回执 receipts/V2-19.md，证据 evidence/V2-19/。0.1.19 最终正式 43 升级事件、真实新旧 Local 默认 update、双 Remote 共享 CLI、原 Context/协作/证据/发现/人工/复验、独立 Server 和 Local 59 场景、全量 Go、相关 race、vet 通过。旧完整来源和新客户端缓存分离，原失败留存。检查点为包含本记录的本地提交，V2-20 当前 PENDING，提交后继续。

## V2-20 当前实施

基线 93f6013f835ae30d37541d328da0a03fdd39695f，开工工作区干净。V2-19 已完成正式验收、相关全量回归和本地检查点。当前唯一 ACTIVE 为 V2-20，先核对中央数据库/附件/授权锁与已有备份接缝，再接本地单实例 Compose、维护与一致恢复。V2-21—23 保持 PENDING，Oracle 实际操作留待 V2-23 明确授权。

- V2-20 第一步通过开发验证，本机单实例 Compose + 官方固定 Caddy digest 的真实 HTTPS、显式公共 CA、Secure cookie、正式 CLI 建任务/上传 PNG、Server 重启后任务与图片 hash 保留，现场 /private/tmp/ael-v220-compose-development-3/summary.json。客户端 PATH 不含 Docker，临时服务和卷已清理。私有健康路径只允许原始 loopback peer/Host，公网代理访问被拒绝。CA 统一接入普通、持久重试及租约通信，未引入 insecure 开关。
- V2-20 第二步已接管理员 session/CSRF 的 drain/quiesce/resume 状态与正式维护 CLI。复用 authorization.lock，登录写入也进入栅栏；新领取受 drain 拒绝，quiesce 将旧 API 责任置 interrupted、清 lease 并保留 unknown workspace hold，原幂等回执继续可读，resume 后旧 token 写入仍拒绝。维护单元 TestMaintenanceDrainsRevokesAndPreservesReceipts 已通过。当前还需外部维护 CLI/并发/截止与中断复验，以及全量备份恢复和升级格式保护，尚无本项 PASS 或检查点。V2-21—23 PENDING。

- V2-20 外部正式维护 CLI 在真实 TLS Compose 开发分发通过 drain、deadline 后写入拒绝、quiesce、原键重放、resume 和重启后任务/图片读取，现场 /private/tmp/ael-v220-maintenance-compose/summary.json，build 93f6013-efb9df7432ad。自有容器和卷已清理。当前继续第三步中央全量备份/恢复与格式回退保护，尚未实现这两部分，也未形成 V2-20 PASS 或检查点。

- V2-20 最终 PASS，回执 receipts/V2-20.md，证据 evidence/V2-20/。真实 TLS Compose、中央全量空卷恢复、旧责任失效/回执保留、真实旧新程序迁移和恢复 SIGKILL 续跑通过。最终正式分发/镜像、升级及全部相关旧协议/浏览器、Local 59 项、Go 全量、相关 race、vet 通过。恢复清除旧 session 并撤销旧项目令牌，管理员重登/重新签发。macOS arm64 与本机 linux/aarch64 实测，其他架构实机、Oracle、真实公网域名未验证。检查点为包含本记录的本地提交，下一项 V2-21 PENDING，提交后继续。

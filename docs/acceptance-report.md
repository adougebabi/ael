# AEL V1 验收报告

日期：2026-11-03，Asia/Shanghai。源码根 `/Users/vinson/Documents/project/个人/ael`。完整原始实施文档已逐段读取并保留。本报告对应实际实现与可执行测试；没有真实摇光项目路径，所有迁移、hooks 切换和发布回执演练均为夹具，不是摇光或生产环境迁移/发布。

## 状态口径

- **已实现且验证**：核心 Loop、SQLite 事务/持久化、四预设、五板块看板、命令连续执行/交接、并行检查、版本证据、审批/回退/恢复、知识与演进视图、init/refresh/两个 Skill、确定性迁移与回滚/激活保护、本机安装与打包。
- **已实现但因环境未验证**：Codex CLI 适配已核对官方文档及本机0.144.1帮助，实际启动被用户 config.toml 中 features 的 map/boolean 类型错误阻止。没有忽略配置、修改凭据或关闭安全限制，未宣称真实模型成功执行任务。
- **仅构建，未运行**：Linux amd64 包。当前宿主为 macOS arm64。
- **没有执行的真实外部动作**：摇光迁移/切换、真实合并与生产发布。交付目标、授权与回执校验用完整夹具验证，不冒充实际外部交付。

## 工作包

| 包 | 代码与产物 | 验证与结论 |
|---|---|---|
| P0 | cmd/aelbench、internal/store；capacity-results.json、capacity-results-full-model.json | 1k/10k；3 Attempts/项、20事件/项；四进程claim竞争、kill事务恢复、一致备份、prune/VACUUM容量；SQLite为唯一后端 |
| P1 | internal/core、internal/server、web | TestP1RealFailureRepairLoop + 浏览器真实闭环；P1已上线持久化五板块，非最终静态补页 |
| P2 | core/model/service/verify/policy/candidate/query | 四预设、按需UI/UX、修订/审批/依赖/反馈/预算、内容与权限指纹、回执、真实分页/计数/历史过滤通过回归 |
| P3 | execution、process、core/service、cmd/ael | 实际命令连续失败修复、节点交接、进程隔离、工作区锁、fencing、心跳、幂等、多进程协议验证；真实Codex模型执行环境阻塞 |
| P4 | knowledge、server、web | 中文/英文/路径/符号、关键约束预算、替代链、内容版本演进记录、基线/决策/避坑视图；浏览器中文检索通过 |
| P5 | bootstrap、assets/skills、schemas、scripts | init dry-run/幂等/用户冲突、两个Skill校验；安装/升级/卸载在/tmp前缀实测；单文件内嵌前端，无CDN |
| P6 | migration、testdata/trellis、迁移UI | 只读盘点、来源/目标hash、proposal校验、结构/正文/未知字段、幂等、归档移动、repair、二次修订rollback、shadow、受管激活；UI预览/摘要批准/应用/核验/回滚通过 |
| P7 | Go回归、Playwright、包与校验清单 | race、vet、真实浏览器截图、离线资源、macOS本机二进制验证；Linux仅构建，真实Codex未通过环境门槛 |

## 回归场景 1—42 证据映射

表中测试名可在源码 rg 检索。发布相关结果只说明受控夹具行为，不代表生产发布。

| # | 场景 | 对应代码/测试与结果 |
|---|---|---|
| 1 | 后端Fast无UI/UX | TestPlansAndNoAutomaticPass；MakePlan |
| 2 | 交互独立检查与失败阻断 | TestPlansAndNoAutomaticPass、TestUIAndUXCannotPassWithoutRequiredRunningEvidence、浏览器真实失败闭环 |
| 3 | 研究结论证据，无部署 | TestPlansAndNoAutomaticPass：真实conclusion.md与明确human审阅 |
| 4 | 同Task失败回开发新Attempt | TestP1RealFailureRepairLoop、TestManagedCommandContinuousFailureRepair |
| 5 | 定义/方案修订旧证据审批失效 | TestCandidateAndDefinitionInvalidation、TestStrictResumeCannotSkipApprovalAndModeChangesInvalidate |
| 6 | 候选/未跟踪/规则/验收/权限变化失效 | TestCandidateAndDefinitionInvalidation、TestInputChangesDuringCheck、权限位回归；政策配置参与指纹 |
| 7 | 无检查/未执行/环境缺失不全绿 | TestPlansAndNoAutomaticPass、TestProcessZeroExitWithoutResultAndRecursion |
| 8 | 预算停止与恢复 | TestBudgetsAndFeedback、核心runtimeUsed/重复失败检测、Resume检查点 |
| 9 | 发布目标不能只凭代码完成 | TestDeliveryGoalReceiptAndInterruptedRecovery |
| 10 | 回执完整且未知交付不盲重 | TestDeliveryGoalReceiptAndInterruptedRecovery、TestDeliveryReceiptBindsTargetAndKey；真实外部发布未执行 |
| 11 | 完成后反馈新任务 | TestBudgetsAndFeedback |
| 12 | 取消/否决/计划调整有区别 | TestRejectedPlanIsNotCancelledTask、TestWorkspaceMutexDependenciesAndCancellation、浏览器要求调整 |
| 13 | 单执行器连续闭环 | TestManagedCommandContinuousFailureRepair：真实sh进程，明确fixture执行器 |
| 14 | 多Bot按节点交接 | TestP1RealFailureRepairLoop中one/two/checker，租约释放后独立claim |
| 15 | 并行检查同候选 | TestParallelIndependentChecks；UI/UX与功能统一检查领取单元 |
| 16 | 竞争、幂等、过期拒绝 | TestMultiProcessProtocol（6个进程）、TestClaimsIdempotencyAndFencing |
| 17 | 隔离目录并发、同目录互斥 | TestIsolatedWorkspaceParallelWriters、TestWorkspaceMutexDependenciesAndCancellation |
| 18 | 中断不旧写者覆盖/重复发布 | TestBackgroundProcessNeverReleasesWorkspaceAsSuccess、过期token、未知交付恢复回归 |
| 19 | 子进程不启动完整Loop | TestProcessZeroExitWithoutResultAndRecursion；AEL_MANAGED_CHILD及直接递归拒绝 |
| 20 | 文件/DB中断收敛且保留新修改 | TestOutboxCrashRepairAndConflict、迁移中断repair回归 |
| 21 | 空项目init/重复身份稳定 | TestInitDryRunIdempotentAndUserConflict |
| 22 | 用户AGENTS/Skill保留及刷新冲突 | 同上，TestShadowAndSymlink；资产哈希与受管块 |
| 23 | dry-run无项目写入/不执行旧hooks | InitDryRun、TestReadOnlyInventoryAndLosslessIdempotentApply；危险fixture hook从未执行 |
| 24 | Spec正文/来源、不把计划当现行 | TestReadOnlyInventoryAndLosslessIdempotentApply；默认spec conflict，原文hash及映射 |
| 25 | 旧完成未复验、旧运行不复活 | 同上，completed时间未知仍未知、in_progress无Attempt |
| 26 | PRD/JSONL/父子/依赖/未知字段 | testdata/trellis逐文件保留；importedTask映射与legacy元数据、缺失项警告 |
| 27 | 破损/缺失/同名/移动/循环/中文长路径 | TestArchiveMoveIdentityAndUnsafeInputs、TestProposalSupersessionCyclesAndLongChinesePaths、知识替代链回归 |
| 28 | 源变更失效、apply幂等 | TestProposalValidationSourceDriftAndUserRollbackConflict、ReadOnlyInventory... |
| 29 | Skill提案受程序校验 | 同上：quote/hash/固定目标/动作白名单，schemas/migration-proposal.json |
| 30 | repair/rollback/对账/新修改保护 | TestInterruptedApplyRepairAndSafeActivation、TestRollbackSecondRevisionRestoresPriorRuntimeAndOutcome、目标冲突回归 |
| 31 | 旧执行/hooks阻断双控制 | TestUnknownHooksAndActiveOldRuntimeBlockActivation；已知hook精确移除、无关权限/hook保留 |
| 32 | 没有真实路径仅fixture | 所有迁移测试从testdata/trellis复制到t.TempDir；本次真实旧项目迁移未执行 |
| 33 | 五板块互斥且失联不假运行 | Task.Board、BaseNext、TestRealBoardPaginationCountsAndFilters、过期租约回归 |
| 34 | 同时多任务/失败/确认/完成可见 | 浏览器“准确修订确认、五板块同时存在、离线本地资源”，five-boards.png |
| 35 | 确认准确版本/批准/调整/否决 | DefinitionInvalidation、DeliveryGoal、RejectedPlan；浏览器点击要求调整 |
| 36 | 历史按原时间/未知时间 | 迁移完成时间回归、QueryPage历史过滤；不以导入时间补完成时间 |
| 37 | CLI刷新/断线/异常/空状态 | 浏览器“服务断开显示stale错误”；2.5秒真实API刷新 |
| 38 | 真实UI交互和截图 | web/tests/dashboard.spec.ts、migration.spec.ts；Chromium真实服务，无API mock完成流程 |
| 39 | 单文件离线看板 | Go embed ui；浏览器拦截所有非loopback请求仍运行；安装后二进制启动本地服务 |
| 40 | 全部运行文件统计、不进业务Git | TestBackupRestoreAndPruneSummary；StorageStats含db/WAL/SHM/log/artifact/backup；portable outcome无token/PID/绝对workspace回归 |
| 41 | 导出/恢复与Git clone区别 | TestBackupRestoreAndPruneSummary、TestRestoreRefusesExistingHumanStateBeforeWritingArtifacts；完整事件恢复但不复活凭证 |
| 42 | 注入/越界/未经授权写 | TestSessionHostOriginAndPaths、Init symlink、migration symlink；浏览器HTML以文本显示 |

## 独立核验与修复

两个独立只读探子核验定位了6项问题，由主线程修复及最终测试：strict向前resume绕过审批、后台子进程遗留、候选权限位遗漏、回执Target/Key绑定不全、二次迁移回滚丢运行态、Restore覆盖无Attempt的人工状态。新增对应回归均归入测试集。另由主线程补充终态专用投影、迁移轻摘要去除本机路径、真实分页、知识内容版本时间线及UI/UX缺少产物阻断。

## 已知限制与下一步边界

- macOS arm64实际运行，Linux amd64仅交叉构建；不声称Windows支持。
- Codex真实模型闭环未通过宿主配置门槛；应由用户修正其CLI配置后，在同一隔离试点继续验证。Attached/通用命令路径不受此配置错误影响。
- 外部合并/发布由授权执行者完成并提供准确回执；AEL验证候选/目标/环境/运行ID/幂等键并保留判断来源，不声称能独立证明任意第三方平台的实际状态。
- 同机本地SQLite，不支持网络盘跨机；保守全目录输入指纹（排除明确AEL投影/.git/node_modules），symlink输入拒绝。未实现按声明输入范围复用旧绿灯，候选变化统一失效。
- 自定义旧工作流或歧义自然语言须经proposal/人工审阅；未知集成默认冲突，程序不会强行猜测。运行导出恢复的迁移历史需要重新核查后才可激活。
- 没有生产部署、没有远程仓库创建、没有推送、没有真实旧工作流停用。缺少真实旧项目路径的试点边界始终保留。

最终运行摘要如下，均来自实际执行产物。

## 0.1.0 首轮运行摘要（历史）

- 代码构建提交：`82947e7`；二进制 `ael version` 为0.1.0/build82947e7。
- `GOCACHE=/tmp/ael-go-cache go test -race -json ./...`：48个测试项通过（含子测试），0失败；`go vet ./...`通过。
- 最终发布包启动的真实Chromium：5/5通过，0跳过、0失败、0 flaky，总耗时约11.7秒。覆盖真实闭环、创建取消、断线、准确修订、五板块、离线资源、迁移和知识检索。
- 用户级安装前缀/tmp/ael-verified-install：安装、升级、version、卸载均实测；项目及运行库未删除。两个Skill的quick_validate均通过。
- npm ci/package审计：0漏洞。macOS Mach-O arm64实际运行，Linux ELF x86-64静态二进制仅构建。
- 真实Codex：最终82947e7在全新隔离任务启动，真实错误为`Error loading config.toml: invalid type: map, expected a boolean in features`；结果wait_environment/interrupted，未冒充模型执行成功。
- 首轮临时夹具地址（服务已停止）：[任务](http://127.0.0.1:7336)、[迁移](http://127.0.0.1:7337)。它们是本机验收数据，不是真实业务项目。

外置持久证据目录：`/Users/vinson/.codex/visualizations/2026/11/03/01a19f63-8ed6-7763-a617-c71ccf2e64df/ael-evidence`。其中run-summary.json汇总结果，go-test-race.jsonl为逐测试原始记录，browser/report.json及PNG为最终浏览器结果，codex-integration.json为真实环境阻塞，doctor/storage-stats/prune-preview/backup-manifest和release-runtime-export记录实际外置运行文件与一致备份。pilot-paths.json记录本轮临时工程实际路径。

| 二进制 | SHA-256 |
|---|---|
| ael-darwin-arm64 | `76119e8cc5df64c8c8346d8067f5f2326cf5d4b0c655c6597db722edec6e6558` |
| ael-linux-amd64 | `d3905ee8a307d025c6313dbf2b6738b4705758e5909b4791784b410dc927977d` |

分发包与tar.gz校验清单位于dist/SHA256SUMS；包内含安装/卸载脚本、README、docs和THIRD_PARTY_NOTICES。分发包最后重新封装了本报告，二进制保持上述已验收代码版本。


## 0.1.3 实际使用问题修复验收

此增量替代首轮关于默认一小时预算、两分钟租约和loopback监听的行为；原始实施文档保留为历史输入。

| 实际问题 | 实现与验证 |
|---|---|
| 默认一小时阻断 | 默认max_seconds=0；显式正数仍有效、-1覆盖项目限制；task budget单独调度调整；有效区间合并、不累计过期后的空等。operational_test.go、budget_test.go回归 |
| 不能局域网打开 | 默认0.0.0.0；合法IP/本机名端口与同源Origin校验；真实192.168.6.227地址HTTP/Chromium通过 |
| 换机失去任务现场 | records脱敏检查点/历史、同事务outbox、新clone暂停复核并重新验收；已有运行库更新/活writer冲突保护；完整backup可接续Git记录恢复；恢复步骤从权威定义重新生成，可移植plan不能删掉Execute/Verify门禁 |
| 误建任务不能删除 | CLI/API/真实UI删除、清理owned文件/日志/任务事件和提交，deleted标记跨clone收敛；多任务依赖删除顺序、禁止复用已删稳定ID均有回归 |
| 验证类型受限 | test/qa/verify原名保存、Fast默认、风险升级Strict；领域创建/编辑和浏览器表单通过 |
| 排队期间租约过期 | 默认30分钟、owner绑定独立20秒续约、启动握手、PID incarnation核对；活owner禁止ack释放；Managed/verify续约失败取消进程组；未知旧工具仍须核实，不宣称能终止宿主Agent |

已执行：`go test -race -json ./...` 81项通过（含子测试），0失败；`go vet ./...`通过。真实Chromium任务看板5/5通过，包含创建取消/文本安全、失败修复完成、断线stale、五板块/修订/离线、验证类型和永久删除后重载。所有临时看板测试后停止。

原生分发验证以PATH=/usr/bin:/bin运行项目入口：后台keeper握手并在调用方等待时实际续约；活owner阻止resume --ack-stopped；真实sh检查失败→修复→完成；五次Attempt摘要随项目复制恢复，凭据不进入记录；误建probe-x真实删除；旧预算调整保留验收修订；新clone无需init，记录恢复后可继续完整storage restore，恢复不复活token/PID/owner。

四平台产物：macOS ARM64已运行；macOS AMD64、Linux AMD64/ARM64仅交叉构建和校验，未在目标平台执行。未连接或修改用户box；没有远程推送或实际发布。

`teal` 已确认指业务功能；用户明确本轮不再处理。未新增或执行该业务专项测试，通用UI检查不计作其覆盖。也未安排后台定期备份：可移植任务记录自动保存，原始日志/截图仍需显式storage backup。

持久证据：`/Users/vinson/.codex/visualizations/2026/11/03/01a19f63-8ed6-7763-a617-c71ccf2e64df/ael-operational-verification/verification.json`；同目录保留逐测试JSONL、浏览器报告与截图、原生分发验证及可重跑脚本。源码/项目提交和最终二进制build信息以该JSON为准，避免把历史二进制hash冒充新版本。

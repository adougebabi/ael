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

最终运行命令、测试计数、包校验值和外置证据路径在下方运行摘要补录；这些信息以实际执行产物为准。

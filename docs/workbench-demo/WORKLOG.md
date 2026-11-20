# AEL 工作台按 Demo 还原 · 工作记录

参考包：`AEL-Workbench-Demo-20261120.zip`（解压于仓库外的会话临时目录，不入库）。
依据：包内 `handoff/REPRODUCE.md`、`FUNCTION-MAP.md`、`DESIGN.md`、`VALIDATION.md` 与 `dist/` 源码。

## 基准核对（2026-11-20）

- 仓库 main，开工时本地 HEAD `8226662`，落后 Demo 核对基线 `683e963`（origin/main）一个提交；已 `git merge --ff-only` 快进到 `683e963`，未改写历史。
- 开工时工作区仅有未跟踪的 Demo zip；无 `AGENTS.md`（仅 testdata 下夹具）。
- 工具链：本机原无 Go，使用 mise 安装 Go 1.26.11 至用户目录；`web/` 执行 `npm ci`。`/tmp` 为 noexec，夹具数据放在 `~/.cache/ael-workbench-fixture/`。
- 夹具：`scripts/workbench_demo_fixture.py` 用真实 Server + CLI 协议生成一次性项目（feature 父任务 + 6 子任务 3 完成、两项独立人工检查、环境受阻、进行中、待接手、完成与取消、另一个项目）。`definition.parent` 只在一次性库中注入，模拟导入历史（正式 epic 成员关系要求 epic 父项）。
- 开发：`AEL_DEV_SERVER=<夹具 URL> npx vite` 代理到夹具 Server（`web/vite.config.ts` 仅开发期生效）。

## 切片状态

| 切片 | 状态 |
| --- | --- |
| 1 共享界面与入口 | 已验证 |
| 2 工作台 → 任务详情 → 进度说明 | 已验证 |
| 3 成果 → 人工验收 → 反馈/继续 | 已验证 |
| 4 资料、规范与演进 | 已验证 |
| 5 设置、接入、服务与收尾 | 已验证 |

## 后续清单（不阻塞当前切片）

- 本地模式没有跨任务资料索引接口（仅有逐任务附件读取）。`资料` 在本地模式显示真实说明并指向任务详情，不编造列表。
- 旧 style.css 中已无引用的外壳规则（.sidebar/.topbar/.workspace 等）仍保留，未逐条删除；新样式在 workbench.css 中以更高优先级覆盖。清理需配合全量视觉回归，留作后续。
- `cmd/ael` 的 `TestRunnerWorkspaceFingerprintIncludesFilePermissions` 在本机失败，基线 683e963 的独立 worktree 上同样失败（本机 umask 077 环境相关），与本次改动无关，未处理。
- 未迁移的既有浏览器套件：remote-dashboard、standalone-server、authority-transfer、epic-progress、finding-lifecycle 等。它们在基线时已引用不存在的旧界面（如「执行过程」页签、`.task-table-head`、「当前工作空状态」），不在基线 34/31 项回归之内；本轮未修复其过期写法。

## 切片记录

### 切片 1 · 共享界面与入口 — 已验证

范围：`App.vue` 外壳按 Demo 重做（216/195px 侧栏、品牌、项目卡、项目空间导航含「N 待验收」、底部项目设置/服务管理、连接状态、owner 按钮；62px 顶栏面包屑「项目 › 页面」、⌘K 搜索、刷新）；`workbench.css` 统一令牌（靛蓝 #5956dc / 石墨 #3b4559，旧 --brand/--paper/--tint 映射到同一组变量）、控件（8px 圆角、36/30px 按钮）、弹层、对话框、空态、横幅、toast；`AppIcon` 换为 Demo 图标集并保留旧名称别名；`GlobalSearchDialog` 改为 Demo 快速查找（任务/规范/远程资料三组、实时、Esc 关闭回焦）；`StandaloneApp` 项目切换弹层、owner 菜单（个人设置/复制项目连接信息/外观/退出登录）、无权限服务管理空态、登录与项目选择页；`RemoteAccountPanel` 个人设置（身份、外观双卡、会话）；新增 `/materials` 入口（远程复用 ProjectStoragePanel 资料模式，切片 4 重做；本地显示说明）；路由别名 `/workbench`、`/task/:id`、`settings?tab=`，旧 `/tasks` 等保留。

真实契约：登录/退出/会话广播、项目范围缓存与切换代次、角色可见性、服务端鉴权均沿用；远程 transport 新增 `/api/inventory` → `artifacts/inventory`（只读，全体成员可读，服务端仍校验）。

验证（夹具 Server + Vite 代理，Chromium 1440）：reviewer 无服务管理入口且直达显示无权限；owner 切换到「另一个项目」后主区不含原项目任务、面包屑随之变化；`/workbench`、`/task/child-model`、`settings?tab=storage` 别名可达；浏览器后退/前进正确；owner 菜单 Enter 打开、Esc 关闭且焦点回 owner 按钮；石墨外观作用于品牌与导航；本地模式（真实 `ael init` + `ael dashboard`）外壳、本机菜单、资料说明页正常。`vue-tsc` 与 `vite build`（输出到临时目录）通过。截图位于会话临时目录 `shots/s1/`。

### 切片 2 · 工作台 → 任务详情 → 进度说明 — 已验证

后端（只读投影，未改状态机）：`core.Workbench` 新增 `review` 视图（人工检查或待决定，对应 Demo「待确认」）并计入 counts；行与 family 新增未完成后代的 `active_children / review_children / blocked_children`；`TaskSummary` 新增 `imported`（带权威导入账本，执行者可 `task reconcile-import`）与 `paused`。新增 `TestWorkbenchReviewViewAndChildReadingStates`；`go test ./internal/core` 全部通过（70s）。

前端：`TaskList.vue` 按 Demo 重做——标题与未完成/待确认计数、「需要你看一下」（人工观察/决定/历史核对卡片 + 未知回执卡片，最多 2 张，查看全部=待确认视图）、状态页签（未完成/推进中/待处理/待确认/已完成/已取消，计数取服务端 counts）、搜索与类型筛选、「更多视图」（待人工验收/受阻/待修复/关系待核对/全部记录，保留原能力）、三列行（工作事项/当前情况/负责方）、父事项展开未完成子项与「查看全部 N 个子任务」、搜索命中子项自动展开、分页、最近交付卡片；远程「交给 Agent」弹窗只复制接续要求。`display.ts` 新增共享阅读层 `workState/rowState/rowSummary/workOwner/familyRow/handoffText`：待接手按步骤区分（待接手/待分析/待实施/待验证…），父事项本身闲置而子项推进时显示「推进中」并在说明中写明子项分布，父事项自身下一步单列；历史任务按是否有导入账本给出执行者或负责人作为下一责任方。`TaskPage.vue` 保留全部原逻辑，模板改为 Demo 结构：返回工作台、标题与状态/类型/更新时间/所属主要事项、主操作 + 复制给 Agent + 更多菜单（复制任务信息/技术详情/暂缓/编辑/演进/取消，按真实角色显示）、概览/验收与问题(数)/执行记录页签、右侧任务信息/关联规范/相关资料；概览为「当前情况」框（标题、事实、解除条件、下一责任方）→ 剩余工作（`TaskChildren` 改为 Demo 子任务列表，已完成数、展开已完成、直属/后代数量、父级条件说明）→ 原有成果/检查/说明/关系/依据等折叠区；执行记录改为时间线与「当前责任」（注明上报不等于在线）。远程暂停任务对 admin/executor 显示「恢复任务」（服务端 resume 原接口）。服务端默认英文恢复提示译为中文。

验证（新夹具 fx2，owner/observer，Chromium）：父任务行 3/6 + 推进中，展开 3 个未完成子项与「查看全部 6 个子任务」；详情头部与列表同为「推进中」、已完成 3/6、展开后 6 项、父级完成条件说明；`media-retry`（分析已完成）显示「待实施」，`child-memory` 显示「待接手」；`legacy-trellis` 显示「历史待核对」与「确认核对并准备恢复」；已取消/已完成页签；搜索自动展开命中子项；筛选无结果与空项目分别提示；「复制给 Agent」后服务端任务状态与 attempts 不变，toast 说明不会启动；菜单暂缓→服务端 paused=true→头部「受阻」→「恢复任务」→服务端 paused 解除；observer 无暂缓/取消且看到「待验收」而非「待你验收」；1280/390 宽度三页无横向溢出；本地模式工作台「新建任务」与详情「接手当前工作」可用。截图 `shots/s2/`。

剩余差异：Demo 行内负责方显示「开发 Agent」等别名；工作台行投影只有 runner 标识（详情的当前责任使用 collaboration 别名）。列表排序沿用服务端（最近更新优先），不强制父事项置顶。

### 切片 3 · 成果 → 人工验收 → 反馈/继续 — 已验证

新增组件：`ReviewPanel`（单项人工检查的就地验收：问题、当前候选图片/报告、确认通过、需要调整、对比改动前后、更多选项→原 ManualReviewDialog）、`ReviewDialog`（工作台 1120px 验收弹窗，多项人工检查逐项切换、逐项提交）、`MaterialPreview`（图片/报告/文本统一预览，叠在验收弹窗之上，关闭回原位）、`ResultCard` + `TaskResults`（本次成果卡片；输入资料、历史候选、适用性待核对材料折叠到「历史成果与相关资料」）、`AttentionThumb`（待办卡缩略图）、`materials.ts`（按任务/当前候选/知识快照输入归类，不因文件存在给出资格）。`TaskPage` 概览与「验收与问题」页签改用 ReviewPanel，旧 DirectHumanReview 不再在详情中使用；`#/tasks?review=<任务>[&check=]` 可直接打开验收弹窗。

契约：提交仍走 `/reviews` 的 review_result（request_key 幂等、候选/定义/计划版本随请求提交，版本变化时禁用并提示重新查看）；「确认通过」只附带本任务当前候选的已终结附件（服务端再校验 task+candidate），无附件且原契约允许直接观察时可直接通过；`evidence_required` 时禁用并说明缺什么、指向补充证据；反馈要求具体问题，记录为该检查的 fail，由状态机转入修复；响应丢失时保持「提交结果未知 · 原样重试」，若服务端当前记录已与待定请求一致则直接显示「原请求已生效」并清除草稿；工作台未知回执卡片沿用原核对入口。

回归用例（可重复，`scripts/workbench_demo_fixture.py --browser-test`，`web/tests/workbench-demo.spec.ts` + `playwright.workbench-demo.config.ts`）：上下文压缩后会话临时目录被清空，此前的临时核对脚本丢失，故把切片 1–3 的核对整合为仓库内用例，每次在全新夹具上运行，状态断言读取 Server API。2026-11-20 结果：3/3 通过（1.1min，经 Vite 代理）。覆盖：账户菜单两项人工检查只通过 visual（服务端 evidence 仅 visual pass、附带 account-after 图、keyboard 未动、任务仍 open、提示其余 unit/keyboard）；keyboard「需要调整」记录为 fail、行状态变为待修复；预览 Esc 回到验收弹窗且焦点回图片；对比标注「调整前 · 历史候选 / 调整后 · 当前候选」；child-schedule 丢失响应后不显示成功、重试后证据仍只有 1 条、子任务仍 open、父任务仍 3/6；evidence-review 缺截图时禁用通过并说明；copy-review 无附件直接观察通过后服务端 completed；observer 无通过按钮；390 宽无溢出。截图在夹具目录 `browser-shots/`。

本片顺带修复：非管理员访问服务管理时多余的「重试」报错横幅；owner 菜单切换外观后关闭并回焦（与 Demo 一致）；待办卡按人工观察优先排序。

### 切片 4 · 资料、规范与演进 — 已验证

资料：新增 `MaterialsPage`（远程 `/materials`），筛选、计数与分页全部在服务端清单接口完成（关键词、类型取自服务端 file_types、所属任务取自 tasks、来源、上传日期），网格/列表切换（仅本浏览器记住），卡片复用 `ResultCard`/`MaterialPreview`，可下载原件并回到所属任务。适用性按本页涉及任务读取 `task` + `review-evidence` 的候选标注「当前候选 / 历史候选 · 当前未复验 / 适用性待核对」，输入规范快照标为「输入资料」。服务端清单新增只读来源值 `source=results`（除输入快照外全部），作为默认视图，输入快照、执行证据、导入历史、全部来源可选；`TestMaterialFiltersBeforePagingAndLogicalSize` 增补断言并通过。规范与决策：列表改为 Demo 的分类页签（含「我的收藏」，读取本浏览器收藏并逐条读取真实记录，不可读的收藏如实标注）、搜索 + 范围下拉、分隔行（图标、标题+状态、摘要、范围与关联任务数）、行内星标、右侧「从这里开始」；阅读页保留路由式阅读器（版本/上下文参数、正文/版本与变化/关联工作），头部改为统一样式。项目演进：按日分组时间线（类型、时间、标题、内容、原因、结果标签、工作/依据数量、查看变化），范围与日期筛选收进折叠区，服务端占位「未记录原因」不再显示为原因，原始事件仍指向诊断。

夹具新增：`context-guide` 把 `AGENTS.md` 作为本任务产出（与同名输入快照并存）；`evidence-review` 上传一张随后在一次性库中损坏摘要的图片。

回归用例 4/4 通过（1.5min）。切片 4 覆盖：任务侧栏「查看任务全部资料」→ 资料页 → 放大 → Esc 焦点回卡片 → 回到所属任务；默认视图同名 AGENTS.md 只出现产物，「所有来源」下输入与产物分别标注，列表视图写明「执行时读取的规范快照」；损坏原件在资料卡和预览中报告不可用，且不会充当 evidence-review 所需截图；规范收藏→我的收藏→阅读正文；演进时间线可见；另一个项目的资料、规范、演进分别显示真实空态；390 宽三页无溢出。

差异：Demo 的「所有版本（当前/历史/待核对）」筛选未提供——服务端清单没有适用性过滤，只在当前页过滤会误导；改为逐条标注。本地模式无项目级资料清单（见后续清单）。

### 切片 5 · 设置、接入、服务与收尾 — 已验证

项目设置拆为「项目接入 / 成员与令牌 / 存储管理」三个页签。项目接入：复制接入信息（服务地址 + 项目标识 + `ael init --server … --remote-project …`）、当前权限、规范资料状态、协议与检查配置折叠、最近接入的客户端（明确写「最近一次上报，不代表 Agent 或本机进程当前在线」，显示上报时间与兼容性）、项目约定、按任务检索依据快照与附件（原排查能力保留）。成员与令牌：后端新增只读 `GET /api/v1/projects/{id}/members`（仅服务管理员会话，返回用户名与角色；`TestRemoteTasksAreScopedRevisionedAndIdempotent` 增补：列表正确、令牌认证 403）；添加/调整成员走原写接口且只能选 executor/reviewer/reader；令牌按成员签发、继承其角色，完整内容只在创建弹窗显示一次，关闭后不可再见；撤销需确认。非管理员看到说明而非表单。存储管理：容量卡（用量/配额/进度/可用比例）、「查看统计口径」折叠（物理占用、逻辑大小、内容复用、临时与预留、未纳入的数据库与备份）、文件组成（服务端 file_types）、项目配额、可清理原件（服务端预览→确认）、附件与上传记录默认折叠（放弃上传、分页、搜索保留）；资料浏览不再嵌在存储页，由独立「资料」承担。服务管理：中心服务、客户端发行信息（平台仅服务端 `client_release.go` 接受的四种，未配置发行源时不显示虚构版本、复制按钮禁用；复制的是更新说明，不下载或执行）、注册项目（列出可见项目）、创建账户（服务没有账户列表接口，不编造列表）。接入指引 `/local-setup`：远程/本地两套真实命令，可复制，声明网页不连接、扫描或控制本机。`ActionDialog` 改用统一弹窗框架；按钮内图标、旧 `.tabs` 统一为页签样式；证据抽屉层级修正到顶栏之上。

收尾修复：移除无引用组件 AttentionCard / WorkSummary / DirectHumanReview；ReviewPanel 快速通过恢复为既有契约「具名直接观察、不附附件」（附图走完整验收记录），`evidence_required` 的检查一律不提供快速通过；无候选任务也读取附件元数据（与旧画廊一致）；审阅图片优先当前候选最近一次上传；本地连接状态行显示本机服务版本。

## 最终验证（2026-11-20）

- 后端：`go vet ./...` 通过；`go test ./... -count=1` 除上述既有环境用例外全部通过（9 个有测试的包 ok）；本轮新增/增补测试 `TestWorkbenchReviewViewAndChildReadingStates`、`TestMaterialFiltersBeforePagingAndLogicalSize`（results 来源）、`TestRemoteTasksAreScopedRevisionedAndIdempotent`（成员列表与权限）通过。
- 前端：`vue-tsc --noEmit` 与 `vite build` 通过；`scripts/package.sh` 完整分发 4 平台 8 个产物，`release verify` content_integrity=verified（build `683e963-b5dd944edc50`，local_unpublished，未发布）。`internal/server/ui` 内嵌资源已随构建更新。
- 浏览器（全部在全新的一次性夹具、真实 Server/本地服务上运行）：
  - 新增 `workbench-demo.spec.ts` 5/5（切片 1–5），分别经 Vite 代理与生产内嵌界面各跑通。
  - 基线 Remote 真实验收 `remote_review_acceptance.py` 16/16，含双客户端协议、幂等回执、候选/定义变化、只读权限；脚本最终 PASS。
  - 基线 Remote 资料与存储 `remote_artifacts_acceptance.py` 3/3，PASS。
  - 基线本地交互 `ui_usability_acceptance.py` 子集 13/13（A12b、A12f、A23、A44、A45、TD-05、WB02、R1、R2、R3、R4×3；R4 对照使用从 683e963 提取的旧内嵌界面作同数据前后比较）。
  - 迁移原则：这些既有用例只替换了界面定位方式，状态机、回执、证据、权限断言保持不变；本地/远程「暂无法验证」改经完整验收记录提交。
- 视觉对照：同数据同视口 1440 / 1280 / 390 对照 Demo 的工作台、父任务详情、资料、规范、存储、个人设置，另截 owner 菜单与验收弹窗；18 个页面视口组合横向溢出均为 0。代表截图在 `docs/workbench-demo/evidence/`（`*-ael.png` 为本实现，`*-demo.png` 为参考）。

## 与 Demo 的已知差异及原因

- 行内负责方显示 runner 标识（如 fixture-c），Demo 为「开发 Agent」别名；工作台投影不带别名，详情「当前责任」使用协作记录中的别名。
- 资料页没有「所有版本」筛选：服务端清单无适用性过滤，仅过滤当前页会误导，改为逐条标注当前/历史/待核对/输入资料。
- 服务账户卡不列出账户：服务没有账户列表接口。
- 发行信息未配置正式发行源时只显示状态，不显示 Demo 中的示例版本。
- 本地模式无项目级资料清单，「资料」页说明在任务详情中查看。
- 父事项本身闲置而子任务推进时，列表与详情都显示「推进中」，并单列「父事项本身」的下一步——这是从真实计数推导的阅读状态，父事项自身状态与完成条件不变。
- 用户暂停与环境阻塞都带 blocker，统一显示「受阻」+ 原因，未区分「已暂缓」。

## 交付状态

全部改动在工作区，未提交、未推送、未发布、未部署；夹具与临时服务均在 `~/.cache/ael-workbench-fixture/` 的一次性目录，已停止。

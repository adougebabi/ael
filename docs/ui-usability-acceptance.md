# AEL 页面可用性整改验收（0.1.7）

验收日期：2026-11-04（Asia/Shanghai）。本轮直接修改现有 AEL，完成 U0—U5。最终 **A01—A14 全覆盖，20 / 20 个真实 Chromium 场景通过，0 跳过、0 失败、0 flaky**；浏览器执行约 72.9 秒，不含隔离资料准备。Go 全仓竞态测试和 vet 均通过。

实施要求完整读取自用户指定的 `/Users/vinson/Downloads/AEL-UI-Usability-Implementation-Prompt.md`。仓库原先没有同名文件。本轮保留已有工作树修改，不重置、不推送、不修改接入的业务项目，也没有重启真实业务服务。隔离工程仅作为验收夹具，不是新产品或独立 Demo。

## 交付与事实边界

| 工作包 | 结果与主要位置 |
| --- | --- |
| U0 | 页面、入口、操作、数据权威及缺失字段盘点见 `ui-usability-implementation.md`。统一展示适配在 `web/src/display.ts`，请求/导航/读取保护在 `api.ts`、`router.ts`、`resource.ts`、`objectReader.ts`。 |
| U1 | `TaskList.vue` 保留五分组，使用紧凑列表、每组独立分页、搜索与完成时间过滤。`TaskPage.vue` 提供独立宽详情及四个分区，保留接手、提交、检查、人工验收、准确版本决定、暂缓、恢复、取消、删除、定义修订、产物、反馈和高级协议提交。 |
| U2 | `KnowledgePage.vue` 区分现行规范、决策与取舍、历史资料；分页列表不带正文，单条按需读取。`MarkdownBody.vue` 渲染目录、标题、表格、代码与链接。关联入口使用真实任务标题，失效引用不伪装成可访问链接。 |
| U3 | `EvolutionPage.vue` 与 `internal/knowledge/changes.go` 形成只读变化投影。源记录与关联任务的结果、发生时间和当前产物分别保留；不存储第二套演进状态。 |
| U4 | `SettingsPage.vue` 承接接入概览、资料来源、迁移批次、运行诊断。资料、导入、激活、执行器接口分别表达，告警先按影响汇总再分页展开。 |
| U5 | 统一浅色与墨绿、页头、列表、状态、表单、对话框与分页。加载、空、断线、失败、重试、输入保留、键盘焦点与返回位置均有实测。剪贴板受限时提供可选中的手动复制内容。 |

任务列表以一次请求携带各组独立 offset，在共享查询中先收集并去重 ID，再按 `Task.Board` / `Service.Next` 返回的权威分组展示。有效执行与执行者来自真实租约；`check:<id>` 与等待检查分别统计。检查执行中项与尚未运行项分开，前端写操作等待真实回执，不套用读取请求的 20 秒恢复超时。检查使用内核 `CurrentEvidence` 同时校验候选、定义修订、计划修订和配置。人工通过还用 `ReviewExact` 绑定操作者审阅时的候选与修订，明确选择“通过”才提交；不会把任意理由推断为通过。历史失败不冒充当前检查状态；心跳与普通更新时间不作为进展。批准操作使用实际 `confirmation` 事件与批准记录时间。

规范冲突与任务版本审批分开呈现：前者显示“等待规范核实”，可以直接阅读后端明确指出的阻断规范；它不产生任务批准按钮。恢复可以保留当前节点或调用既有接口回到更早责任节点，旧进程核实、写锁和准确版本授权仍由原内核保护。

新增/补齐的接口：`/api/tasks?compact=1`、任务详情的 `summary/current_checks`、`/api/knowledge-page`、`/api/knowledge-record`、`/api/knowledge-link`、`/api/evolution`、`/api/project-status`、`/api/sources`、`/api/migration-view`、`/api/executor-probe`。原始时间线支持分页且读取时不再制造观察事件。任务证据沿用原权限，仅真实 PNG/JPEG 字节可以作为图片展示。

## 实际运行版本与产物

- 版本：**0.1.7**；构建：**58efd8c-e2b98db1d3dd**。
- 版本递增用于避免既有升级器的“同版本不同分发内容”保护；保留工作区并行更新后的实际 0.1.7 版本，流程协议未改。
- 实际浏览器服务来自安装后的 `many/project/.ael/bin/darwin-arm64/ael`，不是 Vite 开发服务器。
- 测试服务二进制与 `dist/ael-darwin-arm64` 的 SHA-256 相同：`915762ef35f9705c16b253b181bdee0cbd29982e86e2bdac40f47d8fbd5a81f4`。
- 完整四平台分发：`dist/ael-0.1.7-unix.tar.gz`；平台包括 darwin-arm64、darwin-amd64、linux-amd64、linux-arm64；校验值在 `dist/SHA256SUMS`。
- 最后仅把本报告重新收进归档；二进制和内嵌资源与上述实测版本保持一致。

## 命令、数据与证据

实际命令：

```sh
./scripts/package.sh
python3 scripts/ui_usability_acceptance.py --bundle dist --out /private/tmp/ael-usability-evidence/verified-delivery-0.1.7
GOCACHE=/tmp/ael-go-cache go test -race ./...
GOCACHE=/tmp/ael-go-cache go vet ./...
git diff --check
```

浏览器使用真实 HTTP、CLI、文件产物、SQLite 与 Chromium。断线和乱序场景仅让真实请求失败或延迟，不用模拟 JSON 替代页面数据。工程变化夹具实际写入文件，并运行内容比对命令。交付测试实际复制到临时本地目录并比较内容，以准确候选回执提交；不涉及生产环境。

隔离目录：`/private/tmp/ael-usability-evidence/verified-delivery-0.1.7`。其中 `single` 从一个任务开始；`many` 验收结束时有 **55 个任务、1007 份知识资料、1137 条事件（含 1007 条知识观察事件）**；`empty` 保持零任务与零事件；`migration` 用于真实预览、应用与回滚。所有条目明确标为隔离夹具，不声称是 Coding Agent 的真实业务成果。

集中证据：

- [完整浏览器结果](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/report.json)
- [运行版本与实际路径](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/embedded-version.json)
- [版本、SHA 与数据计数](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/evidence-summary.json)
- [隔离项目清单](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/manifest.json)
- [实际夹具 CLI 命令](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/commands.json)
- [浏览器执行日志](/tmp/ael-usability-evidence/final-browser.log)
- [打包日志](/tmp/ael-usability-evidence/package.log)
- [Go 竞态回归](/tmp/ael-usability-evidence/go-test-race.log) / [vet](/tmp/ael-usability-evidence/go-vet.log)

## A01—A14 结果

下表的操作步骤对应 `web/tests/usability.spec.ts` 中同编号场景；A11 和 A12 各拆成多个实际交互测试，总计 20 项。

| 验收 | 实际操作及结论 | 代表性截图（共保留 26 张） |
| --- | --- | --- |
| A01 通过 | 一个任务、五组数量与入口；列表宽度/行高断言；直接读到等待接手、无人执行与下一步。 | [单任务](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A01-single-task.png) |
| A02 通过 | 对照权威查询验证两个检查执行中、一个待接手；真实环境阻塞、版本确认；规范冲突可阅读，且没有错误的批准入口。 | [环境阻塞](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A02-environment.png) / [阻断规范](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A02-scoped-conflict-basis.png) |
| A03 通过 | 搜索 43 个分页任务，进入第二页、滚动、开详情、刷新、后退与前进；筛选、分页、位置保留；最近完成组展开状态亦保留。 | [返回第二页](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A03-returned-page-two.png) |
| A04 通过 | 一项命令真实失败、一项未运行；修复阶段明确区分历史失败与当前候选未执行，展开并读取真实证据原件。 | [失败依据](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A04-failed-evidence.png) |
| A05 通过 | 千条资料按分类、范围、搜索及分页读取；列表无 body，打开前正文请求为零；只对选中资料请求正文。 | [按需阅读](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A05-knowledge-lazy-reader.png) |
| A06 通过 | current/conflict 原状态不改写；Markdown 表格/代码/目录可读，HTML 按文字显示；本地关联、失效/越界链接、来源与替代关系均操作。 | [正文与来源](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A06-markdown-source.png) |
| A07 通过 | 超千条观察事件保留在诊断；默认仅展示五项有依据的变化，实际工程交付没有被导入事件淹没。 | [项目演进](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A07-project-evolution.png) |
| A08 通过 | 一个显式变化汇总两个交付任务、规范和决策；子任务不重复刷屏，真实结果与各自日期保留；跨对象往返成功。 | [显式关联](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A08-explicit-relations.png) |
| A09 通过 | 否决理由与未知日期保留；已实现未发布、已决定未认定实现分别显示；类型及日期过滤真实有效。 | [未知日期与否决](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A09-rejected-undated.png) |
| A10 通过 | 当前阻断优先可见；同一批次的 180 条历史引用告警先汇总再分页，原始 unresolved link 只在技术详情展开。 | [影响汇总](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A10-migration-summary.png) / [历史详情](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A10-history-warning-detail.png) |
| A11 通过 | 请求断开后显示过期数据并可重试；真实空、后端错误、快速搜索乱序；证据重试与正文对象隔离；未提供资料不冒充任务已使用版本。 | [断线](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A11-disconnected.png) / [空演进](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A11-empty-evolution.png) / [未提供依据](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A11-unprovided-basis.png) |
| A12 通过 | 创建、接手、提交、真实命令、暂缓、恢复及返回实施、取消、删除；过期确认失败保留输入；迁移错误摘要、应用防重复、核验、修复、回滚；领取后上下文失败只重试读取；超过 20 秒的真实检查等待回执；人工通过明确绑定候选/定义/计划版本，旧审阅不能批准新产物；准确授权下提交真实本地交付回执。 | [真实检查与图片证据](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A12-real-check-passed.png) / [过期确认](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A12-stale-confirmation-error.png) / [本地交付](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A12-local-delivery-receipt.png) / [长检查回执](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A12-long-check-receipt.png) / [过期人工验收](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A12-stale-human-review.png) |
| A13 通过 | 路由刷新、键盘 Escape/焦点恢复/跳过导航；1440×900、1280×800、760×900 无全页横向溢出；来源、运行诊断、执行器接口探测、拒绝剪贴板后的退路与授权后复制均实测。 | [1280 诊断](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A13-diagnostics-1280.png) / [760 宽度](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A13-diagnostics-760.png) / [能力分离](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A13-project-capabilities.png) |
| A14 通过 | 从项目内锁定二进制启动；页面请求内嵌 hash 资源，无 Vite 与外部资源依赖；实际版本、可执行路径和 SHA 一致。 | [最终内嵌工作台](/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/browser/A14-embedded-workbench.png) |

已核看代表性截图中的长标题、空态、历史失败、错误输入保留、Markdown 来源、作用范围冲突、跨对象详情及多种宽度。截图辅助判断；通过结论同时依赖真实点击结果、接口数据、文件内容与内核断言。

## 演进纳入与降级规则

纳入：有明确结果的任务；匹配当前候选、定义及计划修订的成功实施产物；明确的 proposed/accepted/rejected/superseded 决策；有显式变化描述的有效规范。发布/合并结果还要有成功且候选一致的交付记录。导入的占位“历史完成·未复验”不充当工程交付。

聚合只依据明确的记录引用。共享路径、近似标题、相邻日期和普通相关性不合并变化。显式多任务记录保留每项交付的结果、日期、实现说明和当前产物；旧候选及失败产物只在任务历史中查看。同日记录增加稳定 ID 排序，跨页不随机重复。

发生时间只使用明确记录。聚合摘要可按最近有依据的关联交付排序，源记录日期与子任务日期分别展示；日期筛选匹配源记录或关联交付的明确日期，并按浏览器本地日边界查询。缺日期进入“日期未确认”。观察、导入、扫描、索引、哈希改变、心跳、领取及普通进程事件保留为诊断，不作为工程变化。

## 源数据限制、未验证与未完成

- **已实现且验证**：U0—U5；上表 A01—A14；全仓 Go 回归；四平台构建及本机安装后的内嵌运行。
- **未验证**：真实接入业务项目的数据完整性与服务升级、真实模型登录/权限/连续 Agent 执行、生产发布、另外三个平台的真机运行以及 Firefox/Safari。它们未在本轮被标为通过。执行器实测只证明本机 Codex CLI 接口可调用，不能证明登录或模型执行成功。
- **本轮未完成项**：无已知未完成的 U0—U5 页面工作。旧版抽屉浏览器脚本保留为历史参考，本轮以独立隔离入口 `test:usability` 为准。
- **源数据限制**：缺原因就显示未记录；缺明确发生日期就显示日期未确认；只接受显式发生时间，不从导入、mtime 或正文自述猜日期。迁移没有独立记录的执行时间/错误总数显示未知。命令不产生日志时显示真实空原件和退出结果，不编造失败原因。缺失版本快照报错，不用当前正文替代历史版本。
- **容量限制**：本轮验证 1007 份资料与 55 个任务。响应与 DOM 有分页、正文按需读取，但后端仍从文件权威来源扫描元数据，未声称已实现大型索引数据库或任意规模容量保证。
- **安全校验边界**：自动脚本只覆盖其支持的文件类型，不能把没有扫描 Vue 模板当作无风险。构建 JS 中的 innerHTML 模式告警，经源码核对与实际 XSS/协议/越界/symlink 测试归因为受控 Vue/Markdown 渲染；原始 HTML 和图片自动加载关闭，文档链接走 SafePath 和已接入来源白名单，现有 Host/Origin/session 与写操作授权保留。

## 本机复看与重新验收

重新验收使用新目录，脚本拒绝复用非空输出目录：

```sh
python3 scripts/ui_usability_acceptance.py --bundle dist --out /tmp/ael-ui-NEW
```

本轮测试服务已正常退出。可用已经验证过的同一项目入口重新只读查看夹具：

```sh
AEL_DATA_DIR=/private/tmp/ael-usability-evidence/verified-delivery-0.1.7/many/runtime \
  /private/tmp/ael-usability-evidence/verified-delivery-0.1.7/many/project/ael dashboard \
  --project /private/tmp/ael-usability-evidence/verified-delivery-0.1.7/many/project \
  --addr 127.0.0.1:7352
```

再打开 `http://127.0.0.1:7352`。运行时间过去后测试租约会按真实规则过期，页面应显示待恢复，不会把夹具永久伪装成正在执行。真实业务实例尚未替换，本报告不声称原业务页面已经运行新构建。

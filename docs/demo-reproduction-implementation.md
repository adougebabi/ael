# Demo 还原实施记录

基线：`8ff6f84111d919862e0fc97843c18c94e947826e`；开始时工作树干净。交互参考来自用户提供的 AEL Interactive Demo；实际状态仍以现有领域服务为准。

| 页面与组件 | 真实接口 / 权威来源 | 基线差异与实施范围 |
| --- | --- | --- |
| 外壳、工作台与待处理事项 | `/api/tasks`、`/api/attention`、`Next`、任务摘要 | 靛蓝外壳、紧凑分组、人工验收/决定/阻塞入口；保留五个权威分组和分页。待处理卡片每类最多检查最近 200 个候选，超出时明确提示 |
| 任务详情、检查与人工验收 | `/api/task`、`/api/action`、`/api/artifact`、`CurrentEvidence` | 分开检查与执行，统一有效方法和操作条件；明确提交通过、失败或受阻，并绑定候选与修订 |
| 规范与决策 | `/api/knowledge-page`、`/api/knowledge-record`、`/api/knowledge-link` | 保留分页、按需正文、来源及真实关联，按参考调整列表和阅读器 |
| 项目演进 | `/api/evolution` | 展示明确变化及关联；来源未记录的前后状态或取舍不推断 |
| 项目设置与迁移 | `/api/project-status`、`/api/sources`、`/api/migration-view`、`/api/migrations` | 资料、迁移、激活和执行能力分别显示，保留原有写入条件 |
| 验证与打包 | 隔离工程、内嵌前端、浏览器回执 | 覆盖真实人工结果和其它操作，验证多视口与构建后 Go 内嵌资源 |

浏览器策略不允许直接打开提供的 `file://` Demo；视觉核对使用随包静态截图和参考源码，不能将其说成已实际交互体验。隔离工程的真实接口验收单独记录。

## 人工验收闭环证据

以下均来自标记为 `ISOLATED-UI-FIXTURE` 的临时项目，并非真实业务验收。完整的数据库只读摘录分别在 [通过、失败和受阻回执](/private/tmp/ael-demo-qa-20261105-4/human-review-receipts.json) 和 [最终构建的失败、受阻回执](/private/tmp/ael-demo-qa-20261105-5/human-review-receipts.json)。

| 情况 | 实际结果 | 截图 |
| --- | --- | --- |
| 明确通过 | 旧候选提交被拒绝；重新审阅新候选后记录一条 human/pass 证据，目标为 verified 的任务返回 completed | [过期提交](/private/tmp/ael-demo-qa-20261105-4/browser/A12-stale-human-review.png)、[通过回执](/private/tmp/ael-demo-qa-20261105-4/browser/A12-human-review-receipt.png) |
| 明确不通过 | 旧 method 空值、无命令的检查从工作台进入人工验收；提交 fail 后任务回到 execute/repair。最终构建还验证了服务端已保存而浏览器回执丢失时，重试只产生一条证据 | [失败回执](/private/tmp/ael-demo-qa-20261105-5/browser/A12-human-failed-receipt.png) |
| 暂无法验证 | 记录 human/blocked 和解除条件，任务等待环境；恢复之后证据仍为 blocked，不自动生成通过 | [受阻回执](/private/tmp/ael-demo-qa-20261105-5/browser/A12-human-blocked-receipt.png) |

领域测试另覆盖显式 model/scenario/command 不可改为人工、缺失证据、真实视觉截图、候选或修订错误、重复和并发请求、剩余检查、只读验收报告中的失败，以及 released 目标不会提前完成。写入使用 `review_result`，通过、失败和受阻均经候选/修订/证据重验后交给原有提交与门禁逻辑。旧 `review` 路径仍仅表示通过。

## 页面与截图

| 页面 / 链路 | 实现与接口 | 1480 宽度截图 |
| --- | --- | --- |
| 工作台、待处理、创建任务 | `App.vue`、`TaskList.vue`、`CreateTask.vue`、`AttentionCard.vue`；`/api/tasks`、`/api/attention` | [工作台](/private/tmp/ael-demo-qa-20261105-5/browser/A15-workbench-1480.png)、[创建任务](/private/tmp/ael-demo-qa-20261105-5/browser/A15-create-task-1480.png) |
| 任务概览、检查与执行 | `TaskPage.vue`、`ManualReviewDialog.vue`；`/api/task`、`/api/action`、`/api/artifact` | [概览](/private/tmp/ael-demo-qa-20261105-5/browser/A15-task-overview-1480.png)、[检查](/private/tmp/ael-demo-qa-20261105-5/browser/A15-checks-1480.png)、[执行](/private/tmp/ael-demo-qa-20261105-5/browser/A15-execution-1480.png)、[验收表单](/private/tmp/ael-demo-qa-20261105-4/browser/A12-manual-review-form.png) |
| 规范与决策 | `KnowledgePage.vue`、`MarkdownBody.vue`；`/api/knowledge-page`、`/api/knowledge-record`、`/api/knowledge-link` | [列表](/private/tmp/ael-demo-qa-20261105-5/browser/A15-knowledge-list-1480.png)、[正文](/private/tmp/ael-demo-qa-20261105-5/browser/A15-knowledge-1480.png) |
| 项目演进 | `EvolutionPage.vue`；`/api/evolution` | [变化列表](/private/tmp/ael-demo-qa-20261105-5/browser/A15-evolution-list-1480.png)、[变化详情](/private/tmp/ael-demo-qa-20261105-5/browser/A15-evolution-1480.png) |
| 项目设置、来源和迁移 | `SettingsPage.vue`；`/api/project-status`、`/api/sources`、`/api/migration-view`、`/api/migrations` | [设置](/private/tmp/ael-demo-qa-20261105-5/browser/A15-settings-1480.png)、[来源](/private/tmp/ael-demo-qa-20261105-5/browser/A15-settings-sources-1480.png)、[迁移](/private/tmp/ael-demo-qa-20261105-5/browser/A15-migration-1480.png) |

同一隔离数据还生成了 1280×900、768×900、390×844 截图；例如 [390 宽工作台](/private/tmp/ael-demo-qa-20261105-5/browser/A15-workbench-390.png) 和 [390 宽验收表单](/private/tmp/ael-demo-qa-20261105-4/browser/A12-manual-review-form-390.png)。由于无法运行参考 Demo、两边数据也未统一，这些截图是人工结构对照，**不是同数据像素级验收通过的证明**。

## 构建和检查

- `npm --prefix web run build`、`GOCACHE=/private/tmp/ael-go-cache go test -race ./...`、`go vet ./...` 和 `git diff --check` 通过。
- 打包版完整隔离浏览器回归：构建 `8ff6f84-dd739d6a7470` 为 **25/25 通过**。随后只调整了演进详情的未记录提示及对应样式，并加强回执丢失测试；最终构建 `8ff6f84-c9d977cb8a37` 针对受影响页面、四种视口、内嵌资源和重试执行 **6/6 通过**。报告分别为 [完整回归](/private/tmp/ael-demo-qa-20261105-4/browser/report.json) 与 [最终构建复验](/private/tmp/ael-demo-qa-20261105-5/browser/report.json)。
- 最终包在隔离副本生成，单独复制至 [ael-0.1.9-unix.tar.gz](/Users/vinson/Documents/project/个人/ael/dist/ael-0.1.9-unix.tar.gz)，包含四个平台；macOS ARM64 的项目内锁定二进制实际启动并提供内嵌 hash 资源，响应包含 `Cache-Control: no-store`。原工作区的 0.1.8 分发内容未覆盖，也未升级本机或真实项目。

## 已实现但未验证

- 浏览器策略禁止直接打开提供的本地 Demo，因此未完成 Demo 本身的点击体验；也未在统一数据下做逐像素比较。没有在真实手机、Firefox 或 Safari 上实测。
- 未指定可只读核查的真实业务项目，故未检查其存量 schema，也未更改真实任务、迁移、hooks 或发布。工作台待处理卡片每类最多检查最近 200 个候选，超出时提示从分组查看；超过该规模的性能未实测。

## 未实现与现有数据限制

- 浏览器内上传附件未实现；验收只能选择当前候选已记录的真实产物，或填写后端可验证的已有引用。没有模拟文件上传成功。
- 演进来源没有结构化的变化前、变化后和方案取舍字段，界面明确显示未单独记录并保留真实关联；未编造历史。激活仍使用现有 CLI 的原有授权与校验，未新增看板激活接口。

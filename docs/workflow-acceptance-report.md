# AEL 通用工程流程整改验收

> 0.1.7 并发行为更新：此前 T06 中“同一写工作区竞争被拒绝”是旧版历史结果，不再是当前行为。新的隔离 CLI 回归已验证同目录不同任务可并行领取；真实进程演练和仍存在的候选、文件同步与数据库限制见 [并发检查报告](concurrency-report.md)。

日期：2026-11-04（Asia/Shanghai）。整改分支：`codex/workflow-protocol-v1`，基线 `58efd8c`。

**结论：R1—R4 已落地并实测；R5 的安装、隔离工程、Attached 真宿主、命令、模型审查、浏览器及本地交付已验证。完整新 Codex CLI 会话仍因宿主配置错误阻塞，不能宣布协议全部通过。**

本报告将用户给定 Protocol v1 / Remediation Prompt 作为实现和验收要求，未把其中的示例、历史工程或测试任务硬编码成业务流程。唯一分发协议正文是 `assets/skills/ael-work/references/workflow.md`；短 Skill 按节点引用它，真实命令由 CLI help 与随包命令说明提供。

## 1. 实际源码、安装与运行实例

| 对象 | 实际核对结果 |
|---|---|
| 源码 | `/Users/vinson/Documents/project/个人/ael`；本轮开始 master 干净；独立分支整改，无 reset、清空或业务迁移 |
| 原全局安装 | `/Users/vinson/.local/bin/ael`，`0.1.3 / ada13c4`；本轮结束仍为此版本，未覆盖 |
| 最终分发 | `dist/ael-0.1.4-unix.tar.gz`，四平台独立运行时；本机原生运行 `darwin-arm64` |
| 最终构建 | **`0.1.4 / 58efd8c-60020764eaf8`**，版本来自 bootstrap 常量，构建号包含源码摘要，lock 校验 SHA-256 |
| 主验收项目 | `/private/tmp/ael-workflow-final/project` |
| 实际二进制 | `/tmp/ael-workflow-final/project/.ael/bin/darwin-arm64/ael` |
| 实际数据 | `/tmp/ael-workflow-final/runtime/bf06bfcf6857631d5c85561c`；SQLite、日志、截图和规范版本快照均在项目外 |
| 看板 | <http://127.0.0.1:7339>；`/api/info` 与 CLI 返回同一服务版本、构建、项目及数据路径 |
| 最终包全新安装演练 | `/private/tmp/ael-workflow-delivery-checks/project`，独立 runtime；12 项 CLI 分支复验 |
| 本机工具 | Go 1.26.3，Node 24.12.0，npm 9.4.0，Codex CLI 0.144.1 |

实际服务信息保存在 `/tmp/ael-workflow-final/service-info-final.json`。没有以配置中的 asset_version 代替服务版本；界面分别展示入口资产、服务构建、可执行路径与数据实例。原默认端口检查未见 AEL 7331；本轮只启动并管理上述隔离测试实例。

## 2. 工作包落地

| 工作包 | 实现与证据 | 状态 |
|---|---|---|
| R1 | `internal/knowledge/catalog.go:169` 来源索引/逐项对账；`knowledge.go:220` 同源 context；`versions.go:57` 已提供版本原文；`projection.go:42` 任务与规范关联；bootstrap 安装短入口、协议、命令引用 | 已实现且实测 |
| R2 | `core/service.go:196` 按影响修订，`:494` 互斥领取，`:662` 产物/候选/执行版本校验；`workflow.go:156` 事实进展；持久 repair；Managed 单单元执行并消费下一动作 | 已实现且实测；Managed Codex 新会话见阻塞 |
| R3 | `core/assessment.go:26` 独立模型/场景/人工证据；真实命令日志；InspectionCandidate 跨失败和恢复固定；最新有效检查判定；report 与 verified 分开；知识收敛、精确交付回执和关联反馈 | 已实现且实测 |
| R4 | 五板块不改视觉；稳定详情、依据正文/版本、实际执行、验收覆盖、历史证据、卡点、交付及反馈；默认时间线显示事实摘要，原始事件折叠；轮询当前页、断线提示及重新握手 | 已实现且 Chromium 实测 |
| R5 | 四平台构建、原生隔离安装、12 项 CLI 演练、实际 Attached 修复/模型审查、真实 Chromium 场景及 8 项浏览器回归；用户资产冲突保护、卸载与独立本地交付 | 可用环境下已实测；新 CLI 会话验收未完成 |

沿用现有 Service / WorkItem / Plan / Attempt、SQLite、outbox、租约、fencing、幂等、候选与门禁；未引入新框架、数据库、DAG、SDK、模型平台或第二套看板状态。CSS 构建文件保持原 `index-B-x7eKDx.css`，未做配色、字体、装饰或动效调整。

## 3. T01—T12 逐项结果

确定性协议驱动器：`scripts/workflow_acceptance.py`。它实际调用安装后的 CLI、实际编辑夹具和运行命令，但**不是 Coding Agent**。其 `passed_installed_cli` 仅代表对应 CLI 分支通过，不能外推为新宿主兼容或所有场景全通过。

最终包运行命令：

```sh
python3 scripts/workflow_acceptance.py --bundle dist --out /tmp/ael-workflow-delivery-checks
```

完整输入、命令、stdout/stderr、结果：`/tmp/ael-workflow-delivery-checks/commands.json`、`results.json`。真实宿主演练使用另一份主项目，并在相同最终运行库展示。

| 编号 | 实际输入、操作及证据 | 结论与边界 |
|---|---|---|
| T01 | 现存 AGENTS、current 算术 Spec、rejected 决策；init 保留原文，来源计数对账；knowledge read/context 返回相同 hash；浏览器从 T03 打开正文并反查任务；T02 仍可打开更新前原文 | 安装/CLI/浏览器通过；继承不声称原业务实现合规 |
| T02 | 确定性算术夹具真实失败→修复；另由当前真实 Codex Attached 宿主领取 `attached-normalize`，读 text-contract，运行 3 个 unittest，先 2 fail，改 `strip()` 为 split/join 后 3 pass，保留失败历史 | 真 Attached 修复闭环通过；不是新 CLI 会话证明 |
| T03 | 普通 feature 的 define/analysis/execute/verify；实际新增 scale 及回归，同步算术 Spec、reindex/context 后验证最终候选；无逐步人工批准 | 安装 CLI 实际产物链通过；feature 驱动器明确为确定性脚本 |
| T04 | inspect/report 对故意不成立的等式执行真实失败检查，交付 `report_delivered_with_findings`，建立 feedback_of；源码 hash 不变。Go 回归另验证 pass/fail/恢复都不能接纳污染源码基线 | 报告交付与候选通过分离；未越权修业务代码 |
| T05 | fixture public-contract strict 计划；拒绝过期修订；真实记录 adjust 和准确版本 approve，保留原决定，批准后继续执行及检查 | 流转通过；fixture-project-owner 是隔离测试决策者，不能当作用户业务授权 |
| T06 | 两个 CLI 执行者逐单元交接；两个真实并发 CLI 进程在两个绑定 checkout 领取独立任务；同一写工作区竞争被拒绝；结果不串任务 | CLI 多执行者通过；不是其他厂商 Agent 实测 |
| T07 | 缺失真实 checker 可执行文件，得到 blocked/恢复节点 verify；恢复不重做已完成步骤。Go 真实进程测试覆盖中断、旧 writer、续约与恢复 | 环境分支及进程保护通过；缺失工具任务继续显示阻塞 |
| T08 | progress 和标题编辑不改定义修订；政策排版不改语义；独立检查更新只复用同候选的未变检查，标 reused_from；前提回退中断并行旧检查 | 安装 CLI 与专项回归通过 |
| T09 | `attached-semantic` 为真实 Codex 模型审查，记录具体输入、报告、限制，来源 model；`actual-dashboard-release` 为实际 Chromium 场景，保存 PNG、请求记录、服务/候选/规范版本，来源 scenario。另保留 T09-scenario 的缺能力 blocked 分支 | 本机模型/实际 AEL 页面场景通过；无能力不转人工 pass；不外推到任意业务 UI |
| T10 | 非发布目标无 delivery；真实独立目录 cp、hash 比对及准确候选回执完成 fixture-local 发布；新反馈关联旧任务，旧完成时间不变；最终包重验是 `attached-release-recheck` 新轮次 | 独立本地交付/反馈通过；未触发真实生产或远程流水线 |
| T11 | 真空项目 empty、删索引 not_indexed、损坏 index_error、symlink/加载 read_failed、混合规范 scoped conflict；缺 critical 依据拒绝继续；ignored 来源变更也查 hash | CLI/源码回归通过，未制造推测规范 |
| T12 | 最终包项目启动器、Skill/引用实际存在；当前真实 Attached 宿主实际读取并使用；临时用户级安装→卸载后项目仍运行；用户改 Skill 时 refresh 拒绝覆盖；旧资料保留；**新 Codex CLI 进程未完成入口及 Loop** | 资产/卸载/回滚边界通过；完整新 CLI 会话为真实阻塞，未验证 |

专项回归入口：`internal/core/workflow_test.go`、`internal/knowledge/catalog_test.go`、`internal/execution/runner_test.go:88`。模型 fixture 测试在名称、报告和 executor 中明确标注 fixture，不算真实模型审查；真实模型证据在主验收项目。

## 4. 真实宿主、模型和浏览器事实

主项目 `attached-normalize`：实际读源码和现行文本契约，先用实际 unittest 暴露内部空格和 Unicode 换行两条失败，再由当前 Codex 修改实现。失败、修复、成功证据保存在同一任务，最终候选 `82077ce452f357dc5f03b55d83a24a12706abe32ef0dacb115937d38be5fbb62`。这是本轮真实宿主工具操作，不是 shell fixture 自称完成。

事实时间线及 UI/UX 默认覆盖修正改变了工具链输入，故保留原任务历史，并在最终构建 `58efd8c-60020764eaf8` 下新建关联的 `attached-release-recheck`。当前真实宿主重读依据、只写 runtime 报告，实际运行 3 个回归均通过，交付 report；新候选 `b2258d100474f70fc9058f8339ce52b77f927acada563bec54eeee98f9bd722d`，不把旧候选证据伪称为新版本执行。

真实模型审查：`attached-semantic`，输入为 normalize.py、test_normalize.py、docs/spec/text.md；记录 split/join、Unicode、空输入及无 I/O 的判断和限制，`source=model:codex-desktop-attached`，报告交付。它是模型判断，不是形式证明或人工裁定。

真实页面场景：`actual-dashboard-release`，当前 Codex 驱动 Chromium，打开已完成的 Attached 工作，断言实际执行者、fail/pass、契约和默认时间线中的真实工作摘要，展开依据、滚动结果；保存 `dashboard-final-basis.png`、`dashboard-final-results.png`、`dashboard-final-requests.json`、`dashboard-final-review.md`。`source=scenario:chromium-playwright-by-codex`；输入及报告均有 hash。

原始记录：`/tmp/ael-workflow-final/actual-failure.json`、`actual-pass.json`、`actual-semantic-pass.json`、`final-recheck-pass.json`、`dashboard-final-pass.json`。运行原件位于上述 data_dir；小型完整演练见 [workflow-rehearsal.md](workflow-rehearsal.md)。

## 5. 构建与检查

- `GOCACHE=/tmp/ael-go-cache go test -race ./...`：通过；日志 `/tmp/ael-workflow-race-final.log`。
- `GOCACHE=/tmp/ael-go-cache go vet ./...`：通过；日志 `/tmp/ael-workflow-vet-final.log`。
- 包含 UI/UX 覆盖修正的全部最终源码重新通过全包 race/vet、TypeScript/Vite 构建、四平台打包、最终包 12 项 CLI 和完整 Chromium 验收。
- `npm --prefix web run test:e2e`：最终 **8/8 通过**，日志 `/tmp/ael-workflow-browser-final.log`，JSON `/tmp/ael-browser-results/report.json`。
- `quick_validate.py assets/skills/ael-work`：通过。verify-change 辅助报告与 README/架构/命令/验收同步人工复核；辅助脚本的 0 行统计没有被采用为覆盖证明。
- `git diff --check`：通过；原生项目启动器在 `PATH=/usr/bin:/bin` 下 version/doctor 正常，不依赖全局 AEL/Go/Node。

浏览器命令使用：任务回归 7337、新迁移夹具 7348、主流程与真实 Attached 7339；全部在 `/tmp/ael-workflow-*` 独立项目与 runtime。最终报告仅保留主看板服务供复查。

故障历史未重写：首轮 Chromium 在宿主沙箱被 MachPort 权限拒绝，沙箱外真实执行；一次新测试 locator 匹配到原始 JSON 的同名文本，修正后通过；一次复用迁移夹具使旧测试单记录定位器产生冲突，全新夹具实跑通过，失败日志保留 `/tmp/ael-workflow-browser-reused-fixture-failure.log`。独立复核发现的 8 个边界问题全部补充了回归并修复；收尾另移除 UI/UX 自动复制全部条件的旧默认，未明确覆盖的 UI/UX 计划准确报 CHECK_COVERAGE_REQUIRED，不生成虚构映射。场景产物使用独立 Attempt 目录，保留旧证据原件。见架构增量记录。

## 6. 未实测、未完成与真实阻塞

**已实现未实测：**其他厂商 Coding Agent 只有通用执行者协议设计和明确 fixture 适配测试；没有声称厂商实测。darwin-amd64、linux-amd64、linux-arm64 构建成功但本机未运行。通用业务项目的场景环境、账号、发布平台未接入，不能由本地看板场景推定其能力。

**未完成的验收：**R5/T12 的完整新 Codex CLI 会话入口→工作→恢复验收；不能据此宣称全部 T01—T12 全绿或完整 Agent 兼容完成。

**真实阻塞：**最终包实际调用本机 `codex-cli 0.144.1`，宿主在读取用户配置时退出：

```text
Error loading config.toml: invalid type: map, expected a boolean
in `features`
```

最终结果 `/tmp/ael-workflow-final-codex/host-result-final.json` 为 define / wait_environment，包含具体 Blocker 和真实 process.log 引用，没有产物和通过证据。没有修改用户 config.toml、忽略用户配置、绕过宿主安全设置或用 shell fixture 替代成功。恢复条件是先由配置所有者纠正对应类型问题，核对旧进程停止，再从该 define 检查点恢复；不是业务发布审批。

## 7. 业务保护、接入与回退

未给定业务项目路径，故没有扫描其他业务仓库、全量会话或执行旧脚本。本轮只改 AEL 源码/构建及 `/tmp` 测试夹具；未修改既有业务代码、AGENTS、活动工作流、hooks、数据库、测试账号或发布配置。全局 0.1.3 未升级。

正式业务接入需要另列路径与授权；本轮不执行。可审阅的变更清单：项目入口/锁定运行时、两个 Skill 及流程引用、来源索引、运行库中的新字段与知识快照、看板投影。实际升级前备份项目工作区和 `.ael` 用户资产、原 toolchain.lock/运行时、旧入口和原文；用 storage backup 保存项目外 SQLite/原件。先对独立副本 dry-run/refresh，遇用户资产冲突停在准确路径。回退恢复旧受管资产及匹配数据快照，不擅自回滚业务源码；历史材料保留，不复活旧租约/PID/审批。工具升级、资料迁移、控制器激活和发布是分开的授权边界。

主验收运行库已有一致性备份 `/tmp/ael-workflow-release-runtime-delivery`；证据包保存在本次 Codex 可视化工作目录下的 `ael-workflow-evidence.zip`。原始 `/tmp` 工程会受系统临时目录清理影响，证据包与仓库报告用于保留可复查记录。

# AEL 全状态还原 · 执行账本

参考包：`AEL-Full-State-Demo-20261123.zip`（解压在仓库外，不入库）；顺序以包内 `handoff/RESTORE-ALL-STATES.md` 为准，状态 ID 取自 `handoff/STATE-INDEX.json`（151 条）。
开工基线：`811a72d`（main）。工作区开工时仅有两个未跟踪的 Demo zip。

## 并行工作

2026-11-22 22:55 起，同一 checkout 中有另一项「排队归属」工作（`docs/queue-ownership/`、`internal/core/remote_queue.go`，并改动 `internal/core/remote_runner.go`、`service.go`）。本账本的改动不包含这些文件，也不修改它们；回归中若出现与领取/排队相关的失败，先区分来源。

## 恢复规则

上下文压缩或会话恢复后：先读本账本 → `git status` / `git diff --stat` → 最近一次验证结果，再接续「下一动作」。不要重开已闭合的包。

## 环境

- Go：`~/.local/share/mise/installs/go/1.26.11/bin`；`/tmp` 为 noexec，`go test` 需 `GOTMPDIR=~/.cache/ael-fs/gotmp`。
- 二进制与夹具：`~/.cache/ael-fs/`（`ael`、`fx*/` 一次性夹具）。夹具脚本 `scripts/workbench_demo_fixture.py`。
- 基线回归：`workbench-demo.spec.ts` 5/5 通过（开工时，全新夹具 fx1）。

## 包状态

| 包 | 范围 | 状态 |
| --- | --- | --- |
| 1 | 统一外壳与共享组件 | 上一轮（58d8c93）已还原；本轮按需补共享读取/错误状态 |
| 2 | 交付结果 → 材料阅读 → 检查依据 | 已验证（见包 2） |
| 3 | 当前工作 → 具体进度 → 人工处理 | 已验证（见包 3） |
| 4 | 发现 → 修复 → 复验；父级进展 | 已验证（见包 4） |
| 5 | 资料、规范、设置与服务 | 按差异补齐，见包 5 |
| 6 | 边界补齐与真实回归 | 已验证（见包 6） |

## 包 2 · 交付结果 → 材料阅读 → 检查依据

状态 ID：task-npc-completed, task-report-findings, task-report-passed, task-analysis-delivered, task-legacy-summary-missing, material-*（20 条）, check-*（12 条）, library-mixed。

### 链路核对（真实上报）

- CLI `artifact upload`：保留本地文件名，类型按扩展名（`.json/.md/.log/.txt/.csv` → text/plain）。
- Agent/Runner 提交（`runnerOutputFile`，`agent submit` 与 runner 共用）：**原先把交付物统一改名为 `runner-<hash>.txt`，原文件名丢失**。已修复：保留原名；服务端不接受该扩展名时追加类型扩展（`recovery_test.go` → `recovery_test.go.txt`）；隐藏文件等无效名回退旧命名。测试 `TestRunnerArtifactsKeepSubmittedNames`。
- 服务端类型白名单只有 png/jpeg/text/plain/mp4/octet-stream：JSON、Markdown、源码、日志都存为 text/plain。阅读器按「已记录文件名 + 内容可解析性」选择阅读方式，类型不确定时回退纯文本，不据扩展名给出结论。
- 已知报告结构：外部检查报告（`internal/server/remote_runner_submit.go`）`{candidate, check, definition_revision, plan_revision, method, source, covers[], observations}`，`observations` 为必填文本。报告的通过与否以对应检查记录（`current_checks` / `evidence`）为准，报告正文里的字样不作判定。
- 同内容多对象：真实数据中同一知识快照会按每次上下文发布各存一个对象（相同 sha256）。按 sha256 合并阅读入口，保留每个对象与关联。

### 映射

| 状态 ID | 产品入口/组件 | 事实来源 | 条件 | 操作结果 | 验证 |
| --- | --- | --- | --- | --- | --- |
| （实施中补充） | | | | | |

## 包 6 · 回归结果（2026-11-23）

| 门禁 | 结果 |
| --- | --- |
| `go vet ./...` | 通过 |
| `go test ./... -count=1` | 除 `TestRunnerWorkspaceFingerprintIncludesFilePermissions`（本机 umask 077，基线即失败）外全部通过 |
| `workbench-demo.spec.ts`（全新夹具，生产内嵌界面） | 8/8（原 5 切片 + 包2×2 + 包3） |
| `remote_review_acceptance.py` | 16/16，PASS |
| `remote_artifacts_acceptance.py` | 3/3，PASS |
| `remote_finding_lifecycle_acceptance.py` | 4/4，PASS（修正过期登录定位后恢复为可用门禁） |
| 本地 `ui_usability_acceptance.py` 子集（A12b、A12f、A23、A44、A45、TD-05、WB02、R1、R2、R3） | 10/10 |

既有用例的调整只涉及界面定位：TD-05 已完成任务的主区由「交付结论」承担；WB02 成果在「验证依据」中直接显示图片；R1「来源与关联」改名「来源与提交关联」；usability R3 的下一步行改为 `.wb-situation-facts`。断言不变。

代表截图（仓库外）：`~/.cache/ael-fs/final/`，`ael-*` 为本实现（1440 / 390），`demo-*` 为参考 Demo。

## 未覆盖或仅逻辑覆盖

- 夹具未造：task-merged / released / release-pending / delivery-blocked / delivery-unknown（需要真实交付流程）；task-quiet（需降低项目无进展阈值，会影响同夹具其他用例）；material-upload-pending；check-stale / optional / not-applicable / execution-error 的真实数据。逻辑已实现，未做浏览器验证。
- material-empty：远程服务端拒绝 0 字节上传，真实数据不可达。
- 设置的离开前未保存确认（settings-unsaved）、清理部分失败的逐项重试（library-cleanup-failed）未新增，沿用现有存储协议的行为。
- `#/examples` 状态目录等演示工具按要求未带入产品。

## 下一动作

无未完成实施项。提交 / 部署需负责人决定（工作区同时含并行的「排队归属」改动，提交时需分开）。

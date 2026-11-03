# AEL — Agent Engineering Loop

本机、项目无关的工程闭环工具。Go CLI 与本地 HTTP 服务共用领域内核，Vue 3 + TypeScript 看板随二进制嵌入。一个 Agent 可连续执行，多 Agent 可逐步骤交接；验证失败保留在同一 WorkItem 内。无需数据库服务器、Node 前端服务、模型 API Key 或云基础设施。

实现状态与实测边界以 [验收报告](docs/acceptance-report.md) 为准。原始需求完整保留在 `AEL-V1-local-implementation-prompt.md`。当前仅在测试夹具和临时项目验证，未迁移摇光、未执行真实生产发布。

## 本地构建、安装与升级

开发需要 Go 1.26、Node 24/npm；安装后的项目只需要 AEL 二进制（自动编码另需已有执行器）。依赖锁定在 go.mod/go.sum、web/package-lock.json。

```sh
(cd web && npm ci --ignore-scripts && npm run build)
GOCACHE=/tmp/ael-go-cache go build -o bin/ael ./cmd/ael
./bin/ael version
./scripts/install.sh /tmp/ael-install ./bin/ael
/tmp/ael-install/bin/ael help
```

正式用户级安装：`./scripts/install.sh "$HOME/.local" ./bin/ael`，将 `$HOME/.local/bin` 加入 PATH。升级重新构建后运行同一安装脚本，保存上一版本；已被用户替换的未知二进制拒绝覆盖。卸载 `./scripts/uninstall.sh "$HOME/.local"` 只删除已校验的受管二进制与安装摘要，保留项目/知识/数据库/备份。`scripts/package.sh` 构建 macOS arm64、Linux amd64，生成 tar.gz 与 SHA256SUMS。Linux 产物是否实际运行请看验收报告。解开 macOS 分发包后可运行 `./scripts/install.sh "$HOME/.local" ./ael-darwin-arm64`；安装脚本已随包附带。

工具升级与 `ael refresh --project PATH` 更新项目资产分开。后者基于受管哈希，遇到用户修改返回 ASSET_CONFLICT；没有强制覆盖用户内容的开关。

## 初始化与查看

```sh
ael init --project /path/to/project --dry-run
ael init --project /path/to/project
ael doctor --project /path/to/project
ael dashboard --project /path/to/project --addr 127.0.0.1:7331
```

初始化不运行安装、构建、旧 hooks 或发布。只生成必要配置、短 AGENTS 受管块与 `.agents/skills/ael-work`、`.agents/skills/ael-migrate-legacy`。已有 Trellis 时进入 shadow，不接管任务。现有构建清单列入检测结果；尚未配置的检查/环境不会被猜测。`project.yaml`、policy.yaml 与任务 frontmatter 使用 JSON（合法 YAML 子集），便于确定解析与审阅。政策也可写标准 YAML；知识 frontmatter 支持 YAML。

运行数据位于用户配置目录 `ael/runtime/<canonical-checkout-hash>/`；可用 `AEL_DATA_DIR` 指定基目录。doctor 显示实际路径。同 project_id 的独立 clone 不自动共享租约；显式绑定 worktree 可共用一个运行实例。运行库、WAL、原始命令输出、截图、备份都在仓库之外。

## 最小真实闭环

创建 definition.json（放项目外可避免把协议中间文件计入代码候选）：

```json
{
  "title":"修复输出内容","type":"bug","goal":"verified",
  "scope":["result.txt"],"non_goals":["不发布"],
  "acceptance":["内容正确"],
  "checks":[{"id":"content","kind":"functional","command":["sh","-c","test \"$(cat result.txt)\" = fixed"],"covers":["内容正确"],"required":true}]
}
```

```sh
ael task create --file /tmp/definition.json --project PATH
ael claim --task ID --executor session-A --project PATH
ael context --task ID --project PATH
```

claim 返回 Attempt ID 与 token。创建 submission.json：

```json
{"task":"ID","attempt":"AT-ID","token":"lease-TOKEN","key":"unique-submit-key","result":{"status":"pass","summary":"定义已明确","artifacts":[]}}
```

`ael submit --file /tmp/submission.json --project PATH` 推进 Define。再 claim Execute，实际修改文件，提交 `artifacts:["result.txt"]`。调用 `ael verify --task ID --check content --project PATH` 运行真实命令；失败返回 `repair` / execute；同一任务重新领取修复、再 verify 后完成。看板 2.5 秒内反映外部 CLI 变更。

所有输出 JSON；命令参数/状态非法时 stderr JSON、exit 1。exit 0 表示协议调用成功，是否完成必须读 `next_action` 和 evidence；blocked、等待确认或命令执行失败形成的检查点不会被当作成功验收。`ael help` 输出全部实际命令及结构样例。

Fast/Standard/Strict/Research 为同一有序步骤引擎的预设。后端不带 UI/UX；界面影响独立增加视觉/交互门禁。未配置命令时生成必须人工验收的 review，不会自动通过：

```sh
ael confirm --kind review --task ID --check review --executor person --reason '审阅结论与证据' --artifacts report.md --project PATH
```

视觉人工验收还需真实 PNG/JPEG；交互需产物与明确人类判断。人工结论始终标记 human，不冒充确定性检查。检查结果显示 pass/fail/blocked；未执行显示 not_run。只有非必需项可用 `confirm --kind check-status --status not_applicable|waived|not_run` 记录理由，不能满足必需门禁。

## 连续执行与交接

Attached 使用 claim/context/submit/next 持续执行；Handoff 提交后释放执行权，下一个执行者自己 claim，不需要前一 Bot 安排下游。租约两分钟，长步骤用 heartbeat；过期 token 拒绝。相同幂等键及相同结果返回原响应，内容不同拒绝。

```sh
ael heartbeat --task ID --attempt AT-ID --token TOKEN --project PATH
ael next --task ID --project PATH
ael resume --task ID --ack-stopped --project PATH
```

`--ack-stopped` 是操作者对旧进程已停的明确核实；AEL 不把超时当作进程已停止。中断执行保留工作区保护。隔离 worktree 用 `workspace bind --path WORKTREE`（需同 project_id），随后 claim --workspace；同一可写目录默认互斥，验证检查可独立并行。

Managed 通用命令配置：`{"command":["/absolute/executor"],"max_steps":20,"timeout_seconds":300}`。子进程得到 AEL_CONTEXT、AEL_RESULT、AEL_STEP，只执行当前 Attempt，最终结果写 AEL_RESULT，Schema 在 schemas/step-result.json。stdout/stderr 是原始进程日志，不是节点完成证明。

```sh
ael loop run --task ID --config /tmp/executor.json --project PATH
ael executor probe --project PATH
ael loop run --task ID --codex --project PATH
ael loop run --task ID --codex --session EXACT_SESSION_ID --project PATH
```

Codex 适配已核对本机 0.144.1 help 与官方文档，不使用 --last、不关闭 sandbox/审批。真实运行可能因 CLI 配置、登录或模型不可用阻塞，Attached 仍可用。当前宿主试点遇到 config.toml features 类型错误，未改写用户配置。merged目标在定义中填写target，released目标填写environment，不能结束时临时指定或降低目标。Managed 不自主发布；交付目标 merged/released 必须准确候选审批及真实回执 JSON（run_id、幂等键、候选、环境、状态、制品引用），不能仅凭退出码完成。中断交付先核对已有运行，再确认，避免重复副作用。

## 知识、修订与恢复

知识置于 `.ael/knowledge/`，frontmatter 至少有 id/title/kind/status/paths/source，可有 superseded_by/tasks/critical。Spec、Decision、Research/Reference 分开；current/accepted 是有效约束，historical/proposed/rejected/conflict 不冒充当前事实。替代关系校验断链、自引用和循环。

`knowledge search --query '取消不满足'` 支持中文子串、英文、路径和代码符号；`knowledge read --id ID` 展开原文。context 选择显式引用/路径/查询，有来源 hash、命中原因和预算；关键约束超预算也保留并提示。工程时间线按内容hash观察知识版本，明确区分观察时间与原决策日期。policy.yaml 的 critical_knowledge 指定必载条目，缺失或相关冲突阻止执行。

`task list` 返回 items/counts/total，支持 --limit、--offset、--board、--query、--days（分页按板块）。

task edit --file 定义 --reason 理由生成新定义/计划修订，清空旧证据并请求准确版本确认。外部直接编辑定义也会被检测。已完成任务的新反馈用 feedback_of 创建关联新任务，保留旧历史。父子关系不自动变依赖，取消依赖不能满足门禁。

```sh
ael storage stats --project PATH
ael storage backup --out /tmp/ael-export-new --project PATH
ael storage restore --from /tmp/ael-export-new --project CLONE
ael storage prune --before 2026-01-01T00:00:00Z --project PATH
# 审阅预览后再加 --apply
ael repair --project PATH
```

备份使用 VACUUM INTO 一致数据库快照，加原始产物与校验清单。恢复到匹配定义的全新运行实例，拒绝覆盖已有 Attempt；不复活旧租约/token/PID/审批。Git clone 只能恢复定义及终态摘要，历史显示未复验，不能恢复完整事件/证据；完整运行数据需要 export。prune 默认预览，仅清理终态任务的旧产物，不清理进行中与待确认依赖；摘要/hash 保留，UI 显示原件已清理或缺失。

迁移流程见 [migration.md](docs/migration.md)，数据边界见 [architecture.md](docs/architecture.md)，实测见 [capacity-report.md](docs/capacity-report.md)。本机同用户工具不是恶意程序的防篡改沙箱，SQLite 不支持网络盘跨机并发。

## 测试

```sh
GOCACHE=/tmp/ael-go-cache go test -race ./...
GOCACHE=/tmp/ael-go-cache go vet ./...
# 先启动隔离项目看板，再运行真实 Chromium 测试：
AEL_TEST_URL=http://127.0.0.1:7332 AEL_TEST_PROJECT=/tmp/ael-p7-workspace AEL_MIGRATION_URL=http://127.0.0.1:7333 npm --prefix web run test:e2e
```

截图与浏览器报告默认在 `/tmp/ael-browser-results`，不进入业务 Git。确定性测试执行器明确标记为 fixture，不代表真实 Codex 或生产发布。

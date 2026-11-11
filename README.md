# AEL — Agent Engineering Loop

项目可携带、同机进程协作的工程闭环工具。Go CLI 与本地 HTTP 服务共用领域内核，Vue 3 + TypeScript 看板随二进制嵌入。一个 Agent 可连续执行，多 Agent 可逐步骤交接；验证失败保留在同一 WorkItem 内。无需数据库服务器、Node 前端服务、模型 API Key 或云基础设施。

本轮通用协议实现与实测边界以 [工作流程验收](docs/workflow-acceptance-report.md) 为准，历史实现见 [原验收报告](docs/acceptance-report.md)。原始需求完整保留在 `AEL-V1-local-implementation-prompt.md`。工具分发与项目初始化已在本机项目使用；迁移器的通用验收使用完整夹具。未执行真实生产发布。

## 项目内直接使用

0.1.8 已修正同版本分发内容冲突并完成本机升级与授权看板重启，核查见 [updating.md](docs/updating.md)。完整页面验收记录仍对应已验证的 0.1.7 构建。

当前源码版本为 0.1.18；构建与隔离验收不代表已升级任何真实项目或重启实际服务。不同任务可在同一工作区并行领取；任务自身的租约和结果校验仍有效。工作台保留五个权威分组，默认展示当前工作，每组独立分页；任务完整详情分为概览、检查与验收、执行过程、规范与决策，技术记录在执行过程中按需展开。列表搜索、分页、浏览器往返与滚动位置随导航保留。

“规范与决策”默认查看现行规范，决策与历史资料分别浏览；分页列表不传输全文，正文按需使用本地 Markdown 阅读器打开。项目演进来自真实任务结果及明确的规范/决策记录，资料观察与进程事件在项目设置的运行诊断中保留。资料可读取、迁移已应用、工作流已激活与执行器能力分别表达。详情地址使用 `#/tasks/<id>`、`#/knowledge/<id>`、`#/evolution/<id>`，适用于内嵌离线页面。

只读展示契约、数据限制和实际验收见 [页面整改记录](docs/ui-usability-implementation.md) 与 [浏览器和打包验收](docs/ui-usability-acceptance.md)。验证全套新页面时，先执行 `./scripts/package.sh`，再运行 `python3 scripts/ui_usability_acceptance.py --bundle dist --out /tmp/ael-ui-NEW`。该命令只在新建且明确标记的隔离项目中执行写入，使用安装后的项目入口启动内嵌资源，截图与报告保留在指定输出目录。旧版抽屉页面的浏览器脚本保留供历史参考；本轮以 `test:usability` 的 A01—A14 为界面验收入口。

初始化后的项目自带 `./ael`、`.ael/toolchain.lock` 和 `.ael/bin/<platform>/ael`，全部随Git提交。检出项目后直接运行，不需要全局AEL、Go、Node、Python或下载：

```sh
./ael version
./ael doctor
./ael task list
./ael dashboard
```

启动器检测macOS/Linux与ARM64/AMD64，校验锁定SHA-256后运行对应二进制。不回退到PATH中的其他版本；未知平台、资源缺失或校验失败会明确报错。需要native Windows时不能把此Unix分发标为已支持，可在WSL使用Linux版本。

新机器不重复init、不改变project_id。首次运行时在仓库外建立SQLite，读取已提交定义、`.ael/records/<id>.json` 检查点和脱敏历史；不复活旧租约、PID、会话或审批。终态保留原结果和完成时间，标为历史未复验。未完任务进入恢复确认，历史检查不能满足当前门禁，验证/交付检查点回到执行并重新验收。原始日志、截图和完整事件仍需 `storage backup/restore`。

## 工具构建与首次初始化

开发AEL本身需要Go1.26与Node24/npm。构建完整四平台离线分发：

```sh
./scripts/package.sh
```

产物包括四个运行时、`ael-toolchain.lock`、许可证与`ael-0.1.7-unix.tar.gz`。使用适合初始化机器的二进制，例如macOS ARM64：

```sh
./dist/ael-darwin-arm64 init --project /path/to/project --bundle-dir ./dist
```

`init`自动向目标写入项目入口和全部锁定运行时；`--bundle-dir`也可指向完整解包目录。缺少完整分发时返回BUNDLE_MISSING，不制造虚构下载URL。

可选用户级安装：

```sh
./scripts/install.sh "$HOME/.local" ./dist/ael-darwin-arm64 ./dist
```

安装器同时保留版本化分发资产，后续`ael init --project PATH`可自动发现。项目自带二进制也可`./ael init --project ANOTHER_PATH`，不需要全局安装。本机工具用 `ael upgrade` 升级；项目内工具用 `ael update` 更新当前目录，或 `ael update --project PATH` 指定其他项目。用户修改的启动器/受管资产冲突会拒绝覆盖。工具升级、项目更新与迁移/激活保持独立。卸载全局可执行文件不影响已初始化项目内的工具或运行资料。

## 本机升级与项目更新（0.1.5）

```sh
# 首次引导：旧版默认 JSON 无法探测默认文本的新发行包，从新分发运行一次
./dist/ael-darwin-arm64 upgrade

# 此后升级本机已安装的 AEL，默认读取已登记的本地分发源
ael upgrade

# 在已接入项目的当前目录，自动检查并更新项目工具、入口及 Skill
ael update
# 项目自带的入口也支持
./ael update
# 从其他目录更新指定项目
ael update --project /path/to/project
```

`upgrade` 只更新本机安装及版本化分发缓存；`update` 默认使用该已保存分发包，保留项目 ID、模式、自定义配置、业务源码与任务定义/历史。当前目录无 `.ael/project.yaml` 会准确报未接入，不自动 init，也不会改为升级本机。旧刷新命令已移除，不保留别名。

两者均支持 `--dry-run`。初次或更换来源可用 `--from /path/to/unpacked-distribution`（`--bundle-dir` 仍为来源参数兼容写法）；`--prefix DIR` 或 `AEL_INSTALL_PREFIX` 可选择自定义本机安装。默认安装前缀从当前受管可执行文件/PATH 推导，无法推导时为 `~/.local`。设置过 `AEL_DATA_DIR` 的项目须沿用原值，预览和结果会报告实际数据路径。

目前来源是本地完整分发目录或 `AEL_BUNDLE_DIR`，没有虚构的在线发行接口。首次从本仓库 dist 安装会记住该目录；本机已安装支持输出协议 1 的客户端后，可用 `ael upgrade`，再对项目执行 `ael update`。默认 JSON 的旧客户端无法探测默认文本的新发行包，必须先直接运行新分发二进制 `upgrade --from DIR`；项目更新也应使用已升级的本机二进制或新分发二进制，而非尚未更新的旧项目入口。新版探测与委派会兼容旧版的严格参数检查，避免混用二进制和 Skill。

预览执行完整受管资产冲突检查，不写项目/运行库。正式项目更新拒绝运行中、未核实停止的 Attempt、工作区 fence 和待修复文件意图，生成项目外资产备份；新内核也拒绝在工具更新锁期间领取写单元。本机升级保留旧可执行文件及安装元数据、校验分发、原子替换，并保存中断恢复意图。旧服务不会自动被停止或热替换，更新后要重启对应看板/运行器。回退与限制见 [updating.md](docs/updating.md)，实测见 [update-acceptance-report.md](docs/update-acceptance-report.md)。

## 初始化与查看

```sh
./dist/ael-darwin-arm64 init --project /path/to/project --bundle-dir ./dist --dry-run
./dist/ael-darwin-arm64 init --project /path/to/project --bundle-dir ./dist
/path/to/project/ael doctor
/path/to/project/ael dashboard --addr 0.0.0.0:7331
```

看板默认监听 `0.0.0.0:7331`，可用本机或局域网 IP 访问；只想本机访问时显式用 `--addr 127.0.0.1:7331`，此时本机可使用 `http://localhost:7331` 或 `http://127.0.0.1:7331`。保留端口、Host 和同源 Origin 校验；会话 token 是防跨站凭证，不是用户登录，能连到端口的用户可操作看板。

初始化不运行安装、构建、旧 hooks 或发布。生成必要配置、项目启动器、锁定平台二进制、短 AGENTS 受管块与 `.agents/skills/ael-work`、`.agents/skills/ael-migrate-legacy`。已有 Trellis 时进入 shadow，不接管任务。现有构建清单列入检测结果；尚未配置的检查/环境不会被猜测。`project.yaml`、policy.yaml 与任务 frontmatter 使用 JSON（合法 YAML 子集），便于确定解析与审阅。政策也可写标准 YAML；知识 frontmatter 支持 YAML。

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

本文后续的`ael`子命令，在目标项目中均使用项目根的`./ael`调用。

命令默认输出无色可读文本；`--verbose` 显示更多任务版本信息，`--no-color` 明确选择无色。脚本和 Agent 使用 `--json` 获取兼容既有字段的 JSON（CLI 输出协议 1；`ael version --json` 包含 `cli_output_protocol`），错误写 stderr 且返回码为 1。exit 0 表示协议调用成功，是否完成必须读 `next_action` 和 evidence；blocked、等待确认或检查命令失败形成的检查点不会被当作成功验收。`ael help` 列出命令，`ael help --json` 提供机器结构样例。Dashboard 启动时输出一次监听地址并持续运行；Managed 子进程日志写入运行目录，`loop run --json` 在结束时返回一个 JSON 结果。

Fast/Standard/Strict/Research 为同一有序步骤引擎的预设。后端不带 UI/UX；界面影响独立增加视觉/交互门禁。未配置命令时生成 method=model 的语义审查，不会自动通过；由真实 Agent 读取具体输入、产出报告，再用 `assess --file assessment.json` 提交准确候选。明确声明 method=human 的检查，以及旧记录中未指定方法且没有命令的检查，可使用人工入口；显式 model、scenario 或 command 不会被替换为人工判断：

```sh
ael confirm --kind review --task ID --check review --executor person --reason '审阅结论与证据' --artifacts report.md --project PATH
```

视觉人工验收还需真实 PNG/JPEG；交互需产物与明确人类判断。人工结论始终标记 human，不冒充确定性检查。检查结果显示 pass/fail/blocked；未执行显示 not_run。只有非必需项可用 `confirm --kind check-status --status not_applicable|waived|not_run` 记录理由，不能满足必需门禁。

## 连续执行与交接

Attached 使用 claim/context/submit/next 持续执行；Handoff 提交后释放执行权，下一个执行者自己 claim，不需要前一 Bot 安排下游。默认租约三十分钟，可通过 `policy.lease_seconds` 配置（至少60秒）；过期 token 拒绝。Attached 可在 claim 时用 `--owner-pid PERSISTENT_AGENT_PID` 启动独立续约器（每20秒），不受模型排队和工具等待影响。PID 必须是当前 Agent 的常驻进程，不能绑定短命 shell 或无关进程；owner 身份仅保存在本机运行库，owner 退出时暂停该任务，核实后才能恢复；owner 仍存活时，即使 --ack-stopped 也不能接管该任务。keeper 启动需握手成功，PID 在 claim 的 keeper_pid 返回，错误日志在运行目录 artifacts/<attempt>/keeper.log。Attached 无权杀掉宿主 Agent，续约失败时记录中断；受管子进程则会被停止。无法确定 owner 时省略该参数，在租约有效期内手动 heartbeat。相同幂等键及相同结果返回原响应，内容不同拒绝。

```sh
ael heartbeat --task ID --attempt AT-ID --token TOKEN --project PATH
ael next --task ID --project PATH
ael resume --task ID --ack-stopped --project PATH
```

`--ack-stopped` 是操作者对旧进程已停的明确核实；AEL 不把超时当作进程已停止。中断只暂停相应任务，不再阻挡其他任务领取。不同任务可以在同一可写目录并行，包括执行与验证同时进行；AEL 不协调文件写入，同一文件或共享外部数据的冲突需要执行者自行避免。候选指纹目前覆盖整个工作区，其他任务改动任意受检文件可能使已有验证失效，需基于稳定候选重新验证。需要文件隔离时可用 `workspace bind --path WORKTREE`（需同 project_id），随后 claim --workspace。

并发实测、其他已复现的阻塞点与恢复边界见 [并发检查报告](docs/concurrency-report.md)；隔离回归可运行 `python3 -B scripts/concurrency_acceptance.py --bundle dist`。

默认不设任务时间上限（`policy.max_seconds: 0`），重试和重复失败保护保留。正数 `max_seconds` 仍限制有效执行区间，过期后的空等不再累计，并行检查的重叠时间只计算一次；定义中 `-1` 可显式覆盖项目预算为不限时。旧任务可直接执行 `./ael task budget --task ID --max-seconds 0 --reason 调整时间预算`，不改变验收版本或门禁。update 只升级完全未修改的旧默认政策，用户自定义政策和显式任务预算不会被擅自改写。

Managed 通用命令配置：`{"command":["/absolute/executor"],"max_steps":20,"timeout_seconds":0}`。子进程得到 AEL_CONTEXT、AEL_RESULT、AEL_STEP，只执行当前 Attempt，最终结果写 AEL_RESULT，Schema 在 schemas/step-result.json。stdout/stderr 是原始进程日志，不是节点完成证明。`timeout_seconds: 0` 不设置子进程时间上限，正数才设置；独立续约覆盖准备到提交，续约或进程登记失败会停止执行器，不吞掉错误。

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

支持 feature、bug、improvement、refactor、research、docs、chore、test、qa、verify；验证类名称原样保存，默认 Fast，风险仍升级 Strict。

误建任务可用 `./ael task delete --task ID --reason 理由` 真正删除，或在看板填写理由后点击“永久删除任务”。活跃/未核实停止的执行和入向依赖/父子/反馈引用会阻止删除。删除清理定义、记录、终态、任务事件、提交和所属原始日志；小型 `.ael/deleted/<id>.json` 删除标记随 Git 传播，避免其他已打开过项目的机器复活任务。

task edit --file 定义 --reason 理由按语义影响修订：标题/展示保留证据与审批；独立检查变化只复用同一候选上未变的检查；范围变化重做相关工作；验收/权限/门禁边界变化请求准确版本确认。历史证据保留。外部直接编辑定义也会被检测。已完成任务的新反馈用 feedback_of 创建关联新任务，保留旧历史。父子关系不自动变依赖，取消依赖不能满足门禁。

```sh
ael storage stats --project PATH
ael storage backup --out /tmp/ael-export-new --project PATH
ael storage restore --from /tmp/ael-export-new --project CLONE
ael storage prune --before 2026-01-01T00:00:00Z --project PATH
# 审阅预览后再加 --apply
ael repair --project PATH
```

备份使用 VACUUM INTO 一致数据库快照，加原始产物与校验清单。恢复到匹配定义的全新运行实例，拒绝覆盖已有 Attempt；不复活旧租约/token/PID/审批。Git clone 会恢复定义、检查点与脱敏 Attempt/检查摘要，历史显示未复验；不会恢复完整事件、原始证据和运行授权。已有运行库在重新打开或执行 `repair` 时导入 Git 更新的记录，活跃本地执行与外部检查点冲突时拒绝覆盖；这不是跨机并发运行数据库。完整运行数据仍需要 backup/restore。请将 `.ael/work-items`、`records`、`outcomes`、`deleted` 一起提交；没有提交/传输的本地修改不会自动出现在另一台电脑。prune 默认预览，仅清理终态任务的旧产物，不清理进行中与待确认依赖；摘要/hash 保留，UI 显示原件已清理或缺失。

迁移流程见 [migration.md](docs/migration.md)，数据边界见 [architecture.md](docs/architecture.md)，实测见 [capacity-report.md](docs/capacity-report.md)。本机同用户工具不是恶意程序的防篡改沙箱，SQLite 不支持网络盘跨机并发。

## 测试

```sh
GOCACHE=/tmp/ael-go-cache go test -race ./...
GOCACHE=/tmp/ael-go-cache go vet ./...
# 先启动隔离项目看板，再运行真实 Chromium 测试：
AEL_TEST_URL=http://127.0.0.1:7332 AEL_TEST_PROJECT=/tmp/ael-p7-workspace AEL_MIGRATION_URL=http://127.0.0.1:7333 npm --prefix web run test:e2e
```

截图与浏览器报告默认在 `/tmp/ael-browser-results`，不进入业务 Git。确定性测试执行器明确标记为 fixture，不代表真实 Codex 或生产发布。

## 通用工程流程协议 v1（0.1.4）

唯一流程正文在 `assets/skills/ael-work/references/workflow.md`；初始化与 update 安装同一版本到 `.agents/skills/ael-work/references/`。短 AGENTS 入口引导宿主按需读取。`./ael help` 维护准确的可执行命令和结果结构；详细命令契约随 Skill 安装。

新请求先按目的分流：讨论、功能查询、分析、审阅和仅供审核的材料直接处理，材料即使保存为文件也不因此自动创建任务或运行项目流程；项目迭代、修复和其他实际工程变更才默认进入工作流程。用户明确要求某项 AEL 操作或继续现有 AEL 任务时，按明确要求处理。过期来源索引仍会保护需要工程流程的工作，但不会成为普通讨论的前置门槛。

单次完成的代码任务在必要预检后直接提交本任务改动，再基于提交后的 HEAD 生成候选并完成验证；提交不会自动推送、合并或发布，也不纳入无关改动。

初始化盘点当前项目 AGENTS、Spec、docs/ADR、notes 及 Trellis 规范，`.ael/knowledge-sources.json` 仅保存原文件索引、内容版本与接入结论。它不复制可编辑正文、不执行旧脚本、不激活旧控制器。显式审阅的迁移若安装了规范副本，索引将旧路径标为 retired 并列出 authority_path；后续只读新的权威路径，旧目录作为历史，避免长期双写。明确规范继承为基线，混杂未决计划的内容保留冲突，继承不声称代码已验证。其他自定义目录可先以 `.ael/knowledge/` 原有记录显式接入；自动发现仅限上述项目目录。索引正文改变会阻止继续使用旧索引；审阅后执行：

```sh
./ael knowledge status
./ael knowledge index
./ael knowledge search --query addition
./ael knowledge read --id ID
./ael knowledge read --id ID --version SHA256
```

任务的 `knowledge` 显式引用、路径范围及项目级关键约束共同决定适用依据。`claim` 后执行 `context` 获取原文/hash/适用理由；程序记录“已提供”，不把它包装成模型已理解或代码已合规的证明。`execute` 提交需当前执行上下文与 `knowledge:{status:updated|unchanged,reason,artifacts:[]}`。来源版本变化需补读、评估影响并验证最终候选。已提供版本在仓库外保留不可变快照；任务可打开原正文，版本缺失准确报错。

`progress --task ID --attempt AT --token TOKEN --file progress.json` 记录实际事实、产物与 next，不修改定义。每次 submit 后消费 next_action；repair 可以从 Next 重建，Attached 持续执行或准确交接，Managed 父运行器独占推进责任。没有执行者时显示待接手。

只验收用 `intent:inspect`，交付 `goal:report` 或 analysis。报告放 `runtime:` 目录，所有结果及恢复重领均绑定最初受检候选，失败不能接纳新源码基线；这是一道检测门禁，并非恶意程序沙箱。失败检查仍可交付 `report_delivered_with_findings`，不等于受检候选通过。类型 qa/verify 在未给交付目标时默认只验收；编写测试代码的 test 任务仍可交付 verified。

视觉/交互标志不能推断覆盖关系；必须提供具体 UI/UX 检查及其验收映射，缺失时拒绝不完整定义，不将功能条件复制到每个检查器。

检查的 kind 描述覆盖领域，method 描述执行依据：command、scenario、model、human。自动命令有真实日志；模型与场景用 `assess` 关联准确候选、具体 inputs、执行者、发现理由与真实报告/trace。缺环境提交 blocked，不改成人工 pass；UI 需要真实截图。模型结果永远标记模型判断。CLI 受同一 OS 用户信任边界约束，不是防伪签名服务。

看板五个权威分组保持不变，页面布局与详情入口以本轮可用性整改为准。同一运行库上的依据、版本、工作/检查分离、失败历史、卡点恢复、交付与反馈和知识反查均保留。项目实例的实际二进制、服务构建、入口资产版本及数据目录位于运行诊断，断线数据明确过期。

隔离验收：`python3 scripts/workflow_acceptance.py --bundle dist --out /tmp/ael-workflow-NEW`。它是明确标注的确定性协议驱动器，不能代替真实 Coding Agent；真实宿主、浏览器、最终安装后的证据及限制见工作流程验收报告。业务工程升级/激活需要另行授权，本轮不执行。

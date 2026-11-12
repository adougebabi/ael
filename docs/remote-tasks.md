# 远程任务与客户端连接

Remote 模式由一个独立 Server 保存项目任务的定义、计划和运行状态。每个客户端分别保存 profile 和令牌；checkout 只保存 profile 名称及远程项目 ID，不保存令牌。Server 根据注册项目分配自己的隔离存储，不打开客户端声明的绝对路径，也不运行任务中的命令。

管理员先在 Server 页面注册项目、添加执行者并签发项目令牌。客户端使用安全输入源保存令牌：

```sh
# 不要把真实令牌写进命令行、Git 或日志。
printf '%s\n' "$AEL_SERVER_TOKEN" | ael server profile add \
  --name central --url http://127.0.0.1:7332 --token-stdin \
  --project /path/to/checkout --config-dir /absolute/private/client-config
ael project connect --profile central --remote-project PROJECT_ID \
  --project /path/to/checkout --config-dir /absolute/private/client-config
```

`--config-dir` 必须在 Git checkout 外，目录、profile 和缓存使用私有权限；也可用 `AEL_REMOTE_CONFIG_DIR` 指定，不指定时使用用户配置目录。非 loopback Server 必须使用 HTTPS，URL 不得携带凭据。连接会先检查 Server 协议、项目权限和网络；失败不会写绑定或初始化本地任务。已有本地项目或本地记录不会被自动切换；需要迁移时先保留并核对原数据。

连接后的 `.ael/remote.json` 只包含 schema、profile 名称和项目 ID。remote 标记存在但损坏时也不会回退本地；本地 `Open`/`init` 拒绝该模式。正在运行的本地服务与远程连接使用同一项目目录锁互斥。不同客户端的本地代码目录和运行目录可完全独立。

## 任务、修订和操作回执

远程命令仍使用 `task create/show/list/edit/tree/progress/link/pause/cancel`、`next`、`resume` 和 `plan prepare/preview/apply/abandon`；未支持的本地命令会明确报错，不静默运行本地实现。`--file` 在客户端读取 JSON，绝对文件名不会作为 Server 文件路径使用。

```sh
ael task create --file /tmp/parent.json --operation-id create-parent \
  --idempotency-key create-parent --json
ael task show --task PARENT_ID --json
ael task edit --task PARENT_ID --file /tmp/revised.json --reason '更新范围' \
  --expected-revision STATE_REVISION --operation-id edit-parent-1 \
  --idempotency-key edit-parent-1 --json
ael operation show --idempotency-key edit-parent-1 --json
```

写操作必须提供 `--operation-id`、`--idempotency-key` 和 `--expected-revision`；新建任务的预期值默认为 `absent`。`revision` 是 Server 返回的完整任务状态摘要，与定义中的 `definition_revision` 不同；展示字段变化也会改变它。基于旧摘要的写入返回 `REVISION_CONFLICT`。相同键和相同请求返回原回执；同键不同请求返回冲突，不能重复创建。超时或断网后先用 `operation show` 查询原键，不要换键盲目重做。

计划准备需要父任务当前 `revision`、operation ID 和幂等键；预览返回绑定具体任务状态的 `digest`。计划应用的 `--operation-id` 与 `--idempotency-key` 必须都等于 proposal 的 `id`，并携带预览摘要和父任务预期修订。中断时原键查询为 `pending`，可对同一提议和摘要重放；确需放弃时使用新的操作键执行 `plan abandon --task PARENT --plan-id PLAN --reason REASON --expected-revision REV --operation-id OP --idempotency-key KEY`。已产生的叶子保留，原操作记录 `abandoned`，不会假装应用成功。

在线读取标记 `mode: remote`、`online: true` 和读取时间。网络中断时只有先前成功读取的缓存可显示，输出同时标记 `online: false`、`authoritative: false` 和 `cached_at`；写入返回 `REMOTE_UNAVAILABLE`，不会创建本地任务或声称完成。在线认证失败、令牌撤销或项目授权失败不会用缓存掩盖。若操作已提交但服务端文件投影有冲突，查询回执显示 `projection_blocked`，写入返回 `PROJECTION_BLOCKED`；需由服务端管理员保留冲突现场并核对自己的 pending outbox，恢复后再查询原操作。

## 中心附件

已连接项目可上传本机文件；Server 只收到文件名、内容及任务元数据，不读取或向其他客户端公开本机路径。以下示例的 `STATE_REVISION` 是 `task show --json` 返回的完整状态修订；任务已有候选时还须提供 `--candidate`，有对应 attempt 时可加 `--attempt`。

```sh
ael artifact upload --task TASK_ID --file /path/to/screen.png \
  --expected-revision STATE_REVISION --operation-id upload-1 --idempotency-key upload-1 --json
ael artifact list --task TASK_ID --json
ael artifact show --artifact ARTIFACT_ID --json
ael artifact download --artifact ARTIFACT_ID --out /path/to/new-file.png --json
ael artifact reference --artifact ARTIFACT_ID --task TASK_ID --purpose reference \
  --expected-revision STATE_REVISION --operation-id ref-1 --idempotency-key ref-1 --json
```

支持 PNG、JPEG、UTF-8 纯文本（`.txt/.md/.log/.json/.csv`）和 MP4，单文件上限 10 MiB；Server 校验后才 finalize。图片和文本可在 Server 页面按项目、任务预览，也可按任务 ID 查找；MP4 按附件下载。页面每两秒核对已打开预览的原件状态，清理、权限失效或无法确认时关闭预览，刷新列表可查看当前状态。`artifact download` 校验长度和 SHA-256，只创建新文件，不覆盖原文件。附件读取必须实时授权，断网不会返回旧缓存。引用 `--purpose evidence` 必须绑定与任务及附件相同的非空 `--candidate`；关联本身不等于某项检查通过，也不会自动改变现有验收门禁。

上传中断时以完全相同文件、参数和 operation/幂等键重试原上传命令；无需更换键。若创建响应丢失或源文件已经不可用，用 `ael artifact upload-status --idempotency-key upload-1 --json` 按当前身份实时查询 upload ID 和状态。确需放弃时，可用 `ael artifact cancel --upload UPLOAD_ID --operation-id upload-1 --idempotency-key upload-1 --json` 取消未完成上传并释放预留配额；取消不能撤销已 finalize 的附件。服务端管理员可先对 `/api/v1/projects/PROJECT_ID/artifacts/prune` 提交 `{"before":"RFC3339时间"}` 预览，再使用预览返回的 `digest`、相同 `before`、`apply:true`、`operation_id` 和 `idempotency_key` 执行；需管理员浏览器会话及 CSRF。清理保留元数据和引用，活动任务及仍被活动父任务或依赖需要的原件不列为候选。执行中断后以同一请求和键重放；如果保护关系已变化则返回 `PRUNE_STALE`，应重新预览。页面将清理中、已清理或缺失的原件显示为不可预览。

项目容量可通过 `GET /api/v1/projects/PROJECT_ID/artifacts/stats` 查询，返回已用、临时文件、附件及预留字节。管理员浏览器会话可向对应的 `/artifacts/quota` 提交 `limit_bytes`、从统计读取的 `expected_quota_bytes`、`operation_id` 和 `idempotency_key`；旧配额值冲突时需重新读取，相同请求可用同一键安全重放。新上限不能小于已用与预留之和。

## 跨设备候选与规范依据

先明确在项目内注册仓库 ID；不要用目录名或 remote URL 推断仓库身份。每个客户端提供自己的 checkout、workspace 和 runner 标识，它们是观察来源，不参与候选内容 ID，也不替代 Server 从令牌识别的操作者。以下命令均在已连接的 **Git checkout 根目录**执行：

```sh
ael repository register --repository REPO_ID --operation-id repo-1 --idempotency-key repo-1 --json
ael knowledge index --json
ael task show --task TASK_ID --json
ael candidate capture --task TASK_ID --repository REPO_ID --checkout CHECKOUT_ID \
  --workspace WORKSPACE_ID --runner RUNNER_ID --expected-revision STATE_REVISION \
  --operation-id observation-1 --idempotency-key observation-1 --json
ael context publish --task TASK_ID --observation OBSERVATION_ID --expected-revision STATE_REVISION \
  --operation-id context-1 --idempotency-key context-1 --json
ael context show --context CONTEXT_ID --json
ael context show --task TASK_ID --json
ael candidate compare --before CANDIDATE_ID_A --after CANDIDATE_ID_B --json
```

候选包含显式仓库 ID、完整 commit、暂存区对象及相对路径/内容哈希清单，不包含绝对路径、分支名、进程 ID 或 mtime。Git 跟踪、暂存、删除及未忽略的新文件均会核对；AEL 运行记录和客户端绑定不改变候选。未跟踪的常见凭据文件名会阻断采集，不上传原文；Git 忽略的其他输入不会上传，但会使 `inputs_known` 为 false。普通文件读取不能证明外部写者在最后一次读取后仍然停止，因此采集记录明确标记 `capture_stability_proven: false`；候选对齐是一次观测，不是持续稳定或检查复用的证明。不同 commit 不视为可复用；`candidate compare` 给出变化路径，单凭远程清单不证明检查环境或忽略输入相同，`exactly_equal` 保守为 false。

`knowledge index` 只更新本机索引。发布前重新发现来源并核对任务修订、候选和索引；未知显式引用、缺失必需文件、范围内冲突或源码变化会阻断发布。客户端只上传范围内实际选中的规范正文；其他发现的来源只发送路径、哈希和状态等清单元数据，不发送其正文。未选中的旧文档若使用未识别状态，或已被删除，不会被强制升级为当前契约；快照会标记 `inventory_unverified`，不能声称依据完整。Server 复核已上传正文与候选哈希，保存只读快照；清单由客户端身份报告，Server 不隐式 checkout 或独立证明未上传正文的分类。当前、候选和历史状态分别显示。

`context show --context ID` 可读取历史快照，并返回 `validity`、`workspace_matches`、`usable_for_context` 和 `input_equivalence_proven`。`--task ID` 要求当前任务与本机候选仍匹配，否则返回 `CONTEXT_STALE` 和已知变化路径；未知的其他影响也会明确提示。检查复用前必须证明实际输入、配置和环境等价，忽略输入不明或仅有相同分支名时不能复用。Dashboard 读取同一快照原件，任务修订或后续候选改变后提示旧记录过期。

如 Agent 已实际运行某个任务中声明的检查，可先用 `artifact upload` 上传经审阅的报告，再记录它所使用的上下文：

```sh
ael artifact upload --task TASK_ID --file /path/to/report.txt \
  --source "context-check:CONTEXT_ID:CHECK_ID" --expected-revision STATE_REVISION \
  --operation-id report-1 --idempotency-key report-1 --json
ael context record-check --context CONTEXT_ID --task TASK_ID --check CHECK_ID --artifact REPORT_ID \
  --expected-revision STATE_REVISION --operation-id check-1 --idempotency-key check-1 --json
```

报告须在对应上下文创建后由同一操作者按上述来源上传；不能把其他上下文的旧附件直接挂到新候选。该记录由已认证客户端报告，绑定候选、检查配置哈希和报告哈希，但不能单凭上传时间证明检查何时执行。Server 不执行命令，也不把报告自动转成门禁通过或可复用证据。网络中断时用相同操作键重试以查询原回执，不重复运行检查。上下文规范原件受清理保护并计入项目容量；检查报告按普通附件的保留规则处理。

`python3 scripts/remote_tasks_acceptance.py --bundle /path/to/dist --out /tmp/ael-remote-fixture-NEW` 在全新临时目录启动一个仅监听本机的 Server 与两个独立 CLI 配置，验证父子任务、版本冲突、操作重放、计划、撤销及断网恢复。夹具不运行真实开发执行器，不代表外部机器部署。

## AD-AGENT-DRIVEN-01 执行者接入

正式执行链路见 [远程 Agent 协议](../assets/skills/ael-work/references/remote-agent.md)。Agent 自行执行，runner API 标识兼容保留；不要求客户端 Docker。托管执行及其恢复入口已退出，当前验收从外部驱动主动调用 CLI 开始。

V2-13 的双客户端领取、持久待提交记录、固定字节恢复与显式工作区交接已接入正式 CLI。具体请求和恢复条件见 [远程 Agent 协议](../assets/skills/ael-work/references/remote-agent.md)。

## 外部发现接口

POST `/api/v1/projects/ID/findings` 复用 context 检查报告事务和当前修订/候选校验。GET 同路径提供任务、候选、严重性和完整性筛选，GET `/findings/FINDING_ID` 返回不可覆盖的出现记录、报告来源、证据 hash 及当前可用性。项目角色按每次请求重新校验。

观察阶段附件可显式带 context_id，它必须属于当前任务修订和最新观察候选。此绑定用于诊断资料接入，执行结果提交仍校验原 Attempt、fence、租约及 finalized 关联。

## 远程人工验收与图片比较

项目 reviewer/admin 登录中央 Dashboard 后，可在任务“检查与验收”中记录人工意见或对声明为 human 的检查提交 pass/fail/blocked。记录人来自登录身份。正式提交绑定显示的候选、定义和计划，包含实际证据、观察及失败/阻塞恢复条件；其余必要检查继续参与门禁。普通 Agent 令牌不能提交人工裁定。

图片比较从明确选择基准和实际图开始，可并排、缩放、叠加、定位及下载中央原件。图像由外部客户端通过原 artifact upload 上传。未知基准保持待选择，来源、上传者和候选可追溯；发现报告携带的页面、视口、环境及交互证据一同展示。候选变化时保留输入和选择，重新读取后须再次确认。只有明确相同版本的当前检查会得到正式结论。

会话 API 为 POST `/api/v1/projects/ID/reviews`。请求字段为 action（review_result、review_pending、review_confirm、review_resume）、id、unit、reason、recovery、result、artifacts、candidate、revision、plan_revision、request_key，可附 review_note_id 和 comparison（baseline、actual 中央附件 ID）。executor 兼容旧表单字段，服务端始终使用实际会话身份。相同请求和 request_key 可恢复同一回执，修改内容须用新键。原始图片与比较来源在正式证据中保存。读取和上传未完成状态分别由 Dashboard 的 review-evidence 与原附件接口投影。

隔离正式验收入口为 `python3 scripts/remote_review_acceptance.py --bundle dist --out /tmp/ael-review-NEW`，包括两个客户端正式 CLI 上传、真实浏览器图像、三种结论和打包界面操作。

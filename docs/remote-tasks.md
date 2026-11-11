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

`python3 scripts/remote_tasks_acceptance.py --bundle /path/to/dist --out /tmp/ael-remote-fixture-NEW` 在全新临时目录启动一个仅监听本机的 Server 与两个独立 CLI 配置，验证父子任务、版本冲突、操作重放、计划、撤销及断网恢复。夹具不运行真实开发执行器，不代表外部机器部署。

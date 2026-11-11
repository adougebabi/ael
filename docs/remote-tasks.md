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

`python3 scripts/remote_tasks_acceptance.py --bundle /path/to/dist --out /tmp/ael-remote-fixture-NEW` 在全新临时目录启动一个仅监听本机的 Server 与两个独立 CLI 配置，验证父子任务、版本冲突、操作重放、计划、撤销及断网恢复。夹具不运行真实开发执行器，不代表外部机器部署。

# 独立 Server：登录与项目注册

独立 Server 使用专用持久目录保存账户、项目注册、会话、令牌摘要和附件根，不要求业务 checkout。入口提供管理员初始化、浏览器登录、项目注册与成员授权；已有本地 `dashboard --project` 保持独立。远程客户端可使用 [远程任务](remote-tasks.md) 路径读写同一 Server 的任务和计划；Server 不接收客户端路径，也不执行任务中的命令。

选择一个仅由服务账户写入、位于本机文件系统的空目录。macOS 要求系统将该卷标记为本地，并拒绝数据目录、附件目录或数据库文件上的 ACL；Linux 只接受已识别的本地文件系统，未知类型及 FUSE 拒绝。容器叠加层的底层卷仍须由部署者确认不是网络挂载。Server 拒绝非空且没有自身标记的目录、数据库及 SQLite 辅助文件的符号链接和硬链接，避免覆盖其他数据。

```sh
# 从安全的输入源传入密码；不要把真实密码写进命令行或日志。
printf '%s\n' "$AEL_SERVER_PASSWORD" | ael server bootstrap \
  --data-dir /absolute/path/to/ael-server-data --username admin --password-stdin
ael server start --data-dir /absolute/path/to/ael-server-data
```

`bootstrap` 只允许创建首个管理员，密码至少 12 位；服务端使用随机盐和 Go 标准库 PBKDF2-HMAC-SHA-256 保存密码摘要。`start` 默认监听 `127.0.0.1:7332`；`--addr 127.0.0.1:0` 可选择临时端口，返回实际地址。本阶段拒绝非 loopback 监听。浏览器登录后管理员可以注册项目、创建账号、设置项目执行者/验收者/只读者，以及签发或撤销项目范围内的 Agent 令牌。令牌明文仅在签发时显示一次，数据库保存其安全摘要；每次使用均重新检查项目成员关系和撤销状态。

如果由反向代理终止 TLS，必须显式设置 `--origin https://expected-host` 和 `--trusted-proxy IP_OR_CIDR`。代理须保留该 Host，并覆盖或安全追加 `X-Forwarded-For`、设置 `X-Forwarded-Proto: https`；服务端只从配置的代理信任转发信息，按可信代理链的第一个非可信来源限流。不要信任客户端自行提供的转发头。本阶段不部署或配置公网代理。

浏览器使用 HttpOnly、SameSite=Strict 会话 cookie；写操作还要求同源 Origin 和会话绑定的 CSRF 值。Agent 令牌与浏览器会话分离，权限由服务端账户及成员关系决定，请求内的 executor 字段不能提升权限。登录错误和 HTTP 错误不回显密码、令牌或数据库路径；登录同时按来源和账号限流，代理后的其他本机进程即使能连接 loopback 也不能通过更换转发地址无限尝试同一账号。

## 协议和接口

`GET /api/v1/meta` 可匿名发现协议版本及当前能力。所有变更要求 `X-AEL-Protocol: 1`，不兼容时返回 426 和更新提示。登录、会话和退出分别使用 `/api/v1/auth/login`、`/api/v1/auth/session`、`/api/v1/auth/logout`；浏览器写操作还需 `X-AEL-CSRF`。Agent 使用 `Authorization: Bearer TOKEN`，不使用浏览器 cookie。

管理员会话可使用 `POST /api/v1/projects`、`POST /api/v1/users`、`POST /api/v1/projects/ID/members`、`POST /api/v1/tokens`、`POST /api/v1/tokens/revoke`；`GET /api/v1/tokens` 只返回令牌元数据，不返回已签发的明文。`GET /api/v1/projects` 和 `GET /api/v1/projects/ID` 依据当前项目成员关系过滤；无权访问的项目按不存在处理。认证前不能访问项目、任务或证据。完整输入和响应可以通过打包验收脚本及浏览器操作核对，不通过请求体中的路径访问服务器文件。

在新建隔离目录验证打包程序：

```sh
python3 scripts/standalone_server_acceptance.py --bundle /path/to/dist --out /tmp/ael-server-fixture-NEW
```

脚本只创建合成账号、项目和令牌，启动本机临时端口，实际操作浏览器并重启服务；完成后停止自身进程。它不部署真实服务或操作业务数据。

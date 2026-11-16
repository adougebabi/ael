# CLI 接入、工作与恢复

当前源码版本为 0.1.28。已部署客户端为 0.1.27，Oracle Server/Dashboard 为 0.1.26，协议兼容。local-ai-companion 已在 2026-11-16 按用户要求完成 Remote 迁移，项目入口连接 https://ael.adoumi.site，project_id 为 local-ai-companion。Server、机器 CLI、项目入口/Skill 分别报告版本，兼容依据为 Server 的最低客户端与 API/Skill 范围。

## 首次安装与接入

解压当前平台客户端包。从该包的可执行文件安装到本机共享目录，或直接用该文件执行 init。以下目录和地址由使用者替换为实际值，密码通过受保护的 stdin 输入，终端交互时会隐藏回显。

```sh
/path/to/client/ael upgrade --from /path/to/client-package.tar.gz --prefix /path/to/install
/path/to/install/bin/ael init --project /path/to/project --server https://your-server.example --create "项目名称" --username USER --password-stdin --host codex
```

默认共享安装前缀为现有受管安装或 `~/.local`。客户端包的安装缓存、凭据、检查点及工具会话均在仓库之外。项目入口通过本机 AEL 配置映射找到选定安装与 profile，不依赖把另一台机器的安装路径提交到 Git。

也可使用交互入口，选择 Server/profile、登录、新建或搜索已有项目及更新策略。

```sh
ael init --project /path/to/project
```

`--project` 表示当前机器上的目录。`--remote-project` 表示 Server 上稳定的项目 ID。新机器接入已有项目使用以下形式。

```sh
ael init --project /path/to/checkout --server https://your-server.example --remote-project STABLE_PROJECT_ID --username USER --password-stdin --host both
```

已有 profile 可用 `--profile NAME`。已有项目范围令牌可用 `--token-stdin` 接入允许访问的项目。创建项目沿用账号现有注册权限，普通项目令牌不能注册新项目。同名项目显示各自 ID，选择明确 ID。重复 init 保留原绑定，远端已创建而本地安装失败时，解决报告的冲突后重试同一选择。

init 的结果分别显示连接、允许读取的配置、规范快照差异、任务上下文准备、Hook 状态与更新策略。任务执行仍需要当前候选、规范上下文和明确责任。已有 Local 工程的权威切换使用现有显式迁移入口。

## 开始工作

Agent 进入已授权工程工作后，先准备当前宿主会话的工具版本，读取返回的 `session.skill_path`。

```sh
./ael session start --session HOST_SESSION_ID --runner RUNNER_ID --json
./ael recovery check --session HOST_SESSION_ID --json
```

后续 Remote 命令带相同 `--session HOST_SESSION_ID`。也可由使用者在启动 Agent 时明确提供 `AEL_SESSION_ID`。宿主 Hook 的子进程无法设置父 Agent 的环境。

使用现有 `ael-work` 协议登记执行者、准备候选和 context，再明确领取。编码、测试、浏览器、截图和 Git 由 Agent 使用自己的工具完成。领取会把租约保存到新的私有文件，上报和提交继续使用已有私有 JSON 及幂等键。

```sh
./ael session show --session HOST_SESSION_ID --json
./ael version --check --session HOST_SESSION_ID --json
```

一个开放工具会话绑定一个 runner。同一 runner 的另一个工具会话需等待已有责任结束或明确交接，再结束旧工具绑定。未传会话 ID 的旧 runner 调用会定位它的既有工具绑定，首次兼容接入按 runner ID 隔离。

```sh
./ael session end --session HOST_SESSION_ID --json
```

这个命令结束本机工具绑定，Server 仍有活动责任时会给出阻止原因。新的工作会话使用新的稳定 ID。

## 上下文恢复

压缩、恢复、新会话或进度不确定后，先运行当前项目的 recovery check。读取 Server 的父/叶任务、步骤、责任、候选、规范修订、已通过项、最近失败与下一动作。离线结果明确标为未核对，仅用于定位与保存现场。

```sh
./ael recovery save --session HOST_SESSION_ID --runner RUNNER_ID --parent EPIC_ID --json
./ael recovery check --session HOST_SESSION_ID --json
```

每个可验证单元、提交回执和任务切换后更新 save。必要的短交接可以用私有、至多 4 KiB 的 JSON，只接受 `next`、`tried`、`decisions`。保留证据引用，避免放入凭据、租约、聊天或全量日志。

检查点会在线核对并清理已完成任务指向。候选/规范/责任变化会给出明确状态，失效责任沿用现有 `runner recover` 规则，丢失响应先查原回执或 `runner retry`。恢复查询不会隐式领取、创建任务、提交结果、重发发布或修改工作区。

## Codex 与 Claude Code Hook

```sh
./ael recovery install --host codex
./ael recovery install --host claude
```

安装合并项目级配置并保护用户 Hook。Codex 通过 `/hooks` 审阅项目信任和 Hook 定义，Claude Code 遵循其宿主信任流程。`pending_host_trust` 表示仍需在宿主确认。宿主支持探测、配置存在与实际触发分别报告。

PreCompact 仅保存已有恢复索引。SessionStart 的 startup/resume/clear/compact/fork 提供短恢复规则与引用。缺少支持、未信任或失败时，使用短入口、Skill 和显式 recovery check。

本轮 Codex 0.144.1 已实际验证精确 resume。Claude Code 2.1.154 完整模型交互按用户要求暂缓。真实 compact 的覆盖以验收报告为准。

## 更新策略与手动云端升级

```sh
ael client policy --prefix /path/to/install
ael client policy --prefix /path/to/install --mode notify
ael client policy --prefix /path/to/install --mode auto --source /path/to/trusted-source.json
ael client policy --prefix /path/to/install --mode pinned --version 0.1.28
```

策略作用于这个共享安装的新工具会话。auto 只从已明确选择且身份指纹一致的可信源安装兼容稳定版。新的会话/责任准备会在线核对，普通查询、心跳、上传和 Hook 不自动升级。活动会话引用既有缓存 CLI 和冻结 Skill。固定版本需存在可用缓存并支持工具会话协议。

```sh
ael upgrade --prefix /path/to/install --source /path/to/trusted-source.json --check
ael upgrade --prefix /path/to/install --source /path/to/trusted-source.json --version EXACT --dry-run
ael upgrade --prefix /path/to/install --source /path/to/trusted-source.json --version EXACT
ael upgrade --prefix /path/to/install --rollback --from /reported/backup_dir --dry-run
ael upgrade --prefix /path/to/install --rollback --from /reported/backup_dir
```

已配置来源时，手动 upgrade 可读取该来源。显式 `--source` 也可独立使用。签名、字节 hash、内包与 executable 探针通过后才进入原安装事务。来源不可用而当前版本兼容时会报告并继续，当前版本或协议不兼容时阻止新责任。

项目入口/Skill 用显式 `update --project PATH` 更新，旧离线入口需 `--migrate-entry`。用户修改会形成冲突，已有工具会话继续引用其冻结版本。

## 正式发行与 Server 配置

仓库提供 [当前 GitHub 来源配置](../../packaging/client-source.github.json)，绑定实际仓库 `adougebabi/ael` 的 main 分支发行工作流与 Cosign keyless 身份。正式发行由推送版本标签触发，在 main 上构建、签名并公开，实际可用版本由 `release check` 在线核对。隔离 OpenSSH 源的机制实测记录在验收报告。

Server 可显式设置兼容与发行描述。

```sh
ael server start --data-dir /data --client-min-version 0.1.22 --client-release-source /path/to/trusted-source.json
```

HTTPS 来源需要公共 CA 信任，签名查询需要该来源所选的成熟验证器。现有 scratch 镜像仅包含 AEL，运维需通过受管镜像或只读挂载提供验证器和公共 CA，并将 source 的 verifier 指向实际路径。工具不会代装这些依赖。未具备验证条件时，Server 报告不可验证并保持推荐版本为空。

正式发布流程为审阅并提交源码到 main、确认 main 构建通过，再推送匹配版本 tag。标签运行自动调度受信 main 工作流发布并验证真实平台包/签名。Oracle 的镜像、兼容配置与重启由后续明确授权执行。本轮实现和测试没有修改真实部署。

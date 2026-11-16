# CLI 接入优化工作账本

## 当前检查点

- OPT-00 至 OPT-05、部署与Online迁移已完成。当前唯一任务为用户明确授权的GitHub提交、发行流程修复与正式发布。
- 分支为 main，基线为 259cd8139c4bedac1ab8d91c6b6466bc536ec79d，全部修改保留在工作区。
- 当前客户端/入口0.1.27 / 259cd81-536b3a1781e8，Oracle Server/Dashboard0.1.26 / 259cd81-b393c60af771，协议兼容。publication为local_unpublished，既往版本证据保留。
- 最终包初始化、恢复、签名并发升级、手动升级/回退、Remote 协作、Local T01 至 T12、内嵌 Dashboard 两项真实浏览器及全 Go 回归全部通过。适用源码 hash 与回执位于 evidence/OPT-05。
- 用户原有归档.zip 曾出现在未提交基线，后续 Git 状态已消失，本轮没有修改或删除它。
- Claude 完整真实交互按用户指示暂缓。Codex 精确 resume 已实测，真实 compact 和完整真实工程 Agent 压缩恢复闭环尚未验收。正式在线发行尚无可用版本。
- 用户已明确授权Online迁移。项目 ./ael doctor 实测Remote、online=true、authoritative=true，90任务及历史已在Oracle，最终CLI/浏览器/数据库对账均通过，下一动作交付后暂停。
- 具体使用方式与覆盖边界分别见 USAGE.md 和 ACCEPTANCE.md。

## 顺序与完成条件

OPT-00 → OPT-01 → OPT-02 → OPT-03 → OPT-04 → OPT-05，全局 WIP=1。
每项必须完成实现、实际验证、修复、回执与本账本更新后标记 DONE。
OPT-00 DONE，OPT-01 DONE，OPT-02 DONE，OPT-03 DONE，OPT-04 DONE，OPT-05 DONE。旧回执只用于定位，不作为本轮通过证据。

## 恢复规则

上下文压缩或恢复后，先读取用户任务全文、本账本和当前项验收要求，核对 Git 实际改动与最近证据。明确当前任务、已完成、未完成与下一动作，只继续当前未完成项。不得凭摘要重拆任务、覆盖失败记录或越序。
原始任务位于 /Users/vinson/.codex/attachments/341d4513-09a3-4180-b44f-64b8128525b2/已粘贴的文本.txt。

## 范围约束

- 复用现有 AEL，不重新设计 Loop、重做 V2 或建设 Agent 托管平台。
- Remote 由 Server 维护权威状态，CLI 前台按需执行，不增加客户端后台服务、数据库、模型或消息队列。
- AEL 不管理 Agent 工程进程、登录或模型，不要求客户端 Docker。
- 保留 Local 稳定能力，Remote 新能力不能依赖旧 Managed 执行器。
- 不在本仓库执行产品 init、claim、submit 或 loop。所有产品写入测试使用隔离 Server、目录和配置。
- 原 OPT 实施阶段禁止真实操作。2026-11-16 用户新增授权 Oracle 部署与 local-ai-companion AEL 升级，按此新范围执行。正式公开发行、业务权威迁移与全局 Agent 配置仍需对应授权。
- 子代理只能探索或核验当前项。实现、方案取舍与最终验证由主代理负责。

## 交付记录

### OPT-00 实际基线

- Remote 入口为 assets/project-launcher.sh → 机器 CLI → cmd/ael/remote.go。Remote 托管进程命令在 remote_agent.go 明确拒绝，正常通信不启动后台。Local 历史 keeper/Managed 功能仍保留。
- init 目前只支持 Local。Remote project connect 复用 remote.Connect，安装复用 bootstrap.UpdateRemoteEntry 的受管哈希、锁与恢复 journal。OPT-02 修改这些入口，不重复建设安装器。
- upgrade 复用 bootstrap.Upgrade、release.CheckVersion 与已有签名和安装事务。当前无默认正式发行源。OPT-03 需补更新策略、任务前检查与真实会话版本绑定。
- Server /api/v1/meta 已有版本与协议，internal/remote/protocol.go 有兼容校验。尚缺兼容发行建议。Dashboard info 使用 bootstrap 全局版本，需与 handler 注入版本核对。
- 当前通信恢复见 remote_agent_operation.go，含私有请求和租约，不能直接作为普通恢复提示。Server runner show 与 runner next 可复用，现有 core.WorkCheckpoint 不含 Agent 恢复索引。
- 受管短入口为 bootstrap.block，Skill 位于 assets/skills/ael-work。当前没有 AEL Hook 安装。
- 页面入口为 StandaloneApp.vue、App.vue、remoteDashboard.ts。项目与证据/账户入口有重复，Remote 项目设置被隐藏。OPT-04 沿这些入口修改。
- 验证入口为 Go 定向包、remote_agent_acceptance.py、remote_collaboration_acceptance.py、client_upgrade_acceptance.py、release_source_acceptance.py、remote_dashboard_acceptance.py。
- 基线执行 `GOCACHE=/tmp/ael-opt-go-cache go test ./cmd/ael ./internal/bootstrap ./internal/release ./internal/remote ./internal/server -count=1`，退出 0。日志 /tmp/ael-opt-evidence/opt00-tests.log，适用基线 259cd813。首次在沙箱内因禁止 httptest 监听失败，沙箱外重跑通过。
- 本机 codex-cli 为 0.144.1，help 有 Hook trust 选项。已获取 Context7 /openai/codex 文档与官方 https://learn.chatgpt.com/docs/hooks ，保存 /tmp/ael-opt-evidence/codex-hooks.md。官方明确 PreCompact 普通 stdout 被忽略，SessionStart 使用 hookSpecificOutput.additionalContext，项目与 Hook 都需信任。实际本机事件支持与真实恢复尚待 OPT-01 验收。

### OPT-01 验收待办

检查点字段与脱敏、在线版本/责任/候选核对、父任务部分完成后的恢复、会话隔离、无当前任务、离线限制、短入口与 Skill、真实 Hook 支持探测及信任降级、真实宿主恢复。

### OPT-01 当前可验证单元

- 用户补充 Claude Code 支持，纳入当前项。两宿主共用 recovery save/check 索引与在线核对，配置分别为 .codex/hooks.json 和 .claude/settings.json。Claude 使用 CLAUDE.md 受管块和 .claude/skills/ael-work 的短引用。
- 检查点使用仓库外私有配置目录，按 Server/profile/project/checkout/session/凭据身份哈希分隔，原子写入且加操作锁。正文不含授权 token 或租约。保存短 handoff，不采集聊天或日志。
- `go test ./cmd/ael -run '^TestRecovery' -count=1` 通过，日志 /tmp/ael-opt-evidence/opt01-unit.log。覆盖版本变化、失效责任、其他执行者、离线、会话/checkout 分隔与凭据脱敏。
- `go test ./cmd/ael ./internal/bootstrap ./internal/server -count=1` 通过，日志 /tmp/ael-opt-evidence/opt01-packages.log，适用在角色投影和剩余验收字段补充之前的当前改动。
- `python3 scripts/remote_agent_acceptance.py --binary /tmp/ael-opt-evidence/ael --verify-recovery --out /tmp/ael-opt-evidence/opt01-remote` 退出 0。真实 CLI/Server 的确定性驱动完成两个叶子，包含 fail → repair → pass。第二叶子恢复保留第一叶子已完成引用，独立 session 无检查点，完成后清除任务指向。非真实工程 Agent 验收。
- Codex 0.144.1 已在独立 CODEX_HOME 和隔离工程中通过真实项目/Hook trust 审阅，宿主显示 PreCompact、SessionStart 各一个 Active。未使用 bypass-hook-trust。真实 exec 精确恢复 01a1d42f-7571-7b23-9af0-f4918c9a5c64 成功，模型返回同 session 的 recovery check 命令。日志 /tmp/ael-opt-evidence/codex-start.jsonl、codex-resume.jsonl、codex-hook-observation.json。覆盖真实 resume 注入，尚未声明真实 compact 或完整工程恢复通过。
- Claude Code 2.1.154 已在独立 CLAUDE_CONFIG_DIR、仅 project 配置来源中真实触发 SessionStart startup，hook_response outcome=success。记录 /tmp/ael-opt-evidence/claude-start-2.jsonl。首次调用因 CLI 可变参数消费提示词而失败，修正后成功触发启动 Hook，模型请求等待过久，已结束该测试进程。真实 resume 正在有界核对。
- Claude 精确 resume 已记录真实 SessionStart:resume 的 success 回应，注入命令引用原会话 754f7e66-bf1b-491e-be43-02b26c95fe3c，日志 /tmp/ael-opt-evidence/claude-resume.jsonl。模型请求在 30 秒界限结束，没有完整模型交互通过。用户随后明确本机 Claude 未登录，允许本轮先不验证 Claude。后续暂停 Claude 真实宿主验证，保留实现与确定性验证，不将 Hook success 记为完整 Claude 工程恢复通过。
- 独立只读复核发现两个问题，已修复。恢复现在额外比较本地候选与责任 context 的候选，避免首次绑定吸收错误 checkout 为新基线。安装和注入命令携带实际 config-dir，避免自定义配置目录丢失。对应定向回归日志 /tmp/ael-opt-evidence/opt01-review-fixes.log。
- 最终 CLI/Server 驱动重跑 /tmp/ael-opt-evidence/opt01-final-remote/summary.json 为 PASS，包含真实权限降为 reader 时 recovery 显示 permission_revoked。相关三个包重跑 /tmp/ael-opt-evidence/opt01-final-tests.log 全部通过。上述两项复核修复之后仍需完成实际 CLI 重跑。
- OPT-01 最终验收 DONE。相关包完整回归为 cmd/ael 105.578s、bootstrap 5.488s、server 37.496s，全部 PASS。独立复核修复后实际 CLI 驱动 /tmp/ael-opt-evidence/opt01-complete-remote/summary.json 为 PASS，包含两个叶子的真实失败修复。默认短摘要实测完成，模拟重复 PreCompact 与宿主不可用降级完成。
- 本项持久证据在 docs/cli-onboarding-optimization/evidence/OPT-01，source.json 保存适用源码逐文件 SHA-256，go-test.log、remote-summary.json、hook-deterministic.json 和 host-coverage.json 分开记录确定性驱动与真实宿主覆盖。没有声称真实 compact、完整 Claude 工程恢复或正式在线发行通过。
- 独立宿主认证/配置副本已删除，未改全局 Agent 配置。产品进程仅为隔离测试 Server/宿主，已按测试收尾退出。

### OPT-02 当前验收待办

统一交互与明确参数、认证与 TLS/协议、权限内注册及创建后本地中断恢复、已有项目稳定 ID/同名歧义、允许读取的配置、共享 CLI 安装与受管冲突、更新策略明确选择、重复 init 与清晰文本/JSON 结果。所有写入继续使用隔离 Server 和测试目录。

### OPT-02 已验证单元与当前失败

- Server 新 onboarding 使用现有账号会话、权限锁和同一 SQLite metadata 事务。客户端预存随机项目范围凭据，Server 仅保留摘要。稳定 operation_id 重放不会重复项目或 token，普通项目 token 不能注册新项目。定向测试 /tmp/ael-opt-evidence/opt02-server.log PASS。
- 统一 init 已接入 Server/profile、账号或 stdin token、明确项目选择、私有恢复意图、remote.Connect、bootstrap.Upgrade、受管入口、Hook 安装与更新策略。共享机器映射只含安装/配置路径，位于用户 AEL 数据目录，测试通过 AEL_CLIENT_CONFIG_DIR 隔离，项目不复制二进制。
- 新配置接口只返回项目标识、协议、政策检查描述与规范来源索引，不执行 Server 命令、不覆盖仓库原文。初始化结果分开 connected、specification_state、task_executable、Hook 状态与更新策略。客户端接入信息通过有限 POST 上报最近版本，供后续页面使用。
- 复用 serverMaintenance 现有登录传输为 serverSession，保持独立会话 Cookie/CSRF 及退出。定向回归日志 /tmp/ael-opt-evidence/opt02-unit.log PASS。旧测试把所有 Remote init 视为不支持，已按新行为改为验证 Local 资料保护错误，仍验证无本地 runtime。
- 打包客户端 259cd81-148ad5005602 的实际脚本 scripts/onboarding_acceptance.py 已通过第一轮 12 项，回执 /tmp/ael-opt-evidence/opt02-onboarding-r2/summary.json。覆盖创建、第二机器配置同 ID、重复 init、同名显式新建、权限不足、断网、HTTPS 边界、远端创建后本地 launcher 冲突及重试、指定安装/配置的 ./ael 调用、用户受管块冲突。没有客户端 runtime 或项目二进制复制。
- 扩展驱动已完成注册响应丢失后的稳定项目恢复，之后在真实 PTY 密码隐藏验收失败。错误为先显示输入提示后启动 stty -echo，快速输入可能被回显。只使用 fixture 密码。日志 /tmp/ael-opt-evidence/opt02-onboarding-r2-expanded.log，当前已调整顺序为先关闭回显、成功后显示提示，尚待重新打包与实测。不得标记本项 DONE。
- 下一动作是重跑真实交互与响应丢失验收，并完成当前项独立复核与全相关包测试，然后保存 OPT-02 回执。
- OPT-02 DONE。真实 PTY 的密码隐藏、同名歧义与稳定 ID 连接已通过，注册响应丢失后复用原项目也通过。脚本最终回执 /tmp/ael-opt-evidence/opt02-onboarding-r4/summary.json 为 PASS。打包版本 0.1.22，build 259cd81-591d4fd687b8，publication=local_unpublished，publisher_identity=unverified，未发布线上。
- 独立复核发现来源一致性校验位于共享安装提交之后，已前移至现有 upgrade dry-run 预检。扩展实测同时给出不同 build 的真实客户端包，确认 INIT_CLI_SOURCE_MISMATCH 且目标 bin/ael 未创建。
- 完整相关包回归通过，cmd/ael 94.500s、bootstrap 8.750s、remote 8.800s、server 35.818s。日志 /tmp/ael-opt-evidence/opt02-full-tests.log。
- 持久回执位于 evidence/OPT-02，包含源码哈希、Go 日志、打包机制摘要与脱敏交互日志。协议资产补充统一 init 指引后，受管入口测试再次通过，说明已在 source.json 记录。
- 测试均在隔离目录/Server/机器配置中进行，无 AEL 本仓库初始化、真实项目注册、正式发布或 Oracle 操作。Claude 真实模型交互继续按用户指示暂缓。

### OPT-03 当前验收待办

复用签名来源与升级事务，提供 Server 兼容/真实发行信息、手动升级独立读取、安装级明确策略、领取前短期检查、活动会话 CLI/Skill/协议实质固定、并发升级一次与失败保留旧版、无正式发行源的准确状态。不得自动升级 Server 或其他工具。正式云端发布未获授权，只能隔离签名网络实测并列剩余发布动作。

### OPT-03 已选实施路径

- 已亲自读取发行设计、升级缓存/备份/意图、版本探针及更新说明。现有脚本能消费真实两版包并构造真实 OpenSSH 签名 HTTP 源，现有活动宿主快照尚不能证明真实 CLI 会话固定。旧回退只有匹配备份恢复说明，没有 rollback 命令。
- Server 元数据将提供协议范围、最低客户端和经显式可信源实际校验的推荐发行。未配置源时准确返回 no_configured_source。客户端继续只使用本机明确授权的可信源，Server 推荐不能改变发布身份/来源。
- 机器级策略沿用 OPT-02 的 update-policy.json，补显式来源及身份指纹。任务前检查限定在会话准备/runner register/claim，普通查询、上报、上传、Hook/help/version 不自动更新。
- 新增明确 session start/show/end。版本固定依托现有不可变客户端/完整分发缓存和仓库外每会话 JSON/Skill 快照，项目入口按 --session 或 AEL_SESSION_ID 选旧缓存。宿主 Hook 只给固定提示，不修改父 Agent 环境。
- 既有无显式 session 的 runner 调用保留兼容，通过 runner ID 分隔隐式工具会话；未安装轻入口时可用本 CLI 内嵌 Skill。已初始化共享安装使用真正不可变缓存，旧未受管可执行文件发生变化则拒绝静默换版。
- 同安装更新检查使用有限前台锁与短期结果缓存，继续复用签名下载和 bootstrap.Upgrade。升级后以有限新 CLI 调用委派继续，避免旧进程常量冒充新版本。活动 pin 不被自动切换，也不删除旧缓存。
- 手动回退将显式选择既有备份，复用原升级事务与恢复意图，普通 upgrade 继续拒绝降级。
- 接下来先实现并实测 Server 兼容/发行元数据这一单元，再接任务前检查与会话工具固定。
- 当前源版本已按不可变分发要求递增为 0.1.23，Go 与 web 包/锁同步。没有升级真实安装或服务。
- Server 元数据单元已实测，新增显式 client-release-source/min/recommended 配置，默认 minimum=0.1.22，协议范围 API/Skill 1。只有显式 updates=1 查询会核验发行源，普通任务/心跳读取不拉发行目录。真实 OpenSSH + loopback catalog 的推荐/坏签名/无来源测试通过，日志 /tmp/ael-opt-evidence/opt03-meta-signed.log。该单元的包内容为 fixture 描述，真实包下载仍待后续链路。
- 明确的 client policy 可保存安装级 notify/auto/pinned 与可信源/公钥内容指纹，保留既有字段。签名策略或目录变化要求显式重新配置，Server 推荐不替换本机信任。手动 upgrade 可读取已授权来源，显式 --source 仍独立可用。
- 手动 upgrade --rollback --from BACKUP_DIR 复用既有事务/备份/恢复意图，测试证明精确旧版本恢复、重复回退幂等、篡改备份拒绝、两个版本缓存保留，日志 /tmp/ael-opt-evidence/opt03-rollback.log PASS。
- 工具会话实现已接上 session start/show/end，仓库外 JSON 与 AEL-owned Skill 快照，缓存 CLI 的路径/hash/build/协议固定。已受管安装使用既有版本缓存，旧未受管 CLI 的文件变化会拒绝继续。启动器与主 CLI 都按 session 派发，主入口使用前台 exec 替换，避免结束后遗留委派子进程。
- 首轮会话测试通过，覆盖规则冻结、项目用户改动拒绝新会话、Server 最低版本变化、离线不创建新会话、结束幂等，日志 /tmp/ael-opt-evidence/opt03-pin-unit.log。尚未完成真实两版本升级/会话并发/最终打包验收，不得将 OPT-03 标为 DONE。
- 下一动作是完成版本派发与恢复引用接线，构造两个真实 native CLI 包和既有签名 HTTP 源，验证自动更新一次、旧会话旧缓存/新会话新版以及失败保留旧版。测试继续设置 AEL_CLIENT_CONFIG_DIR 到 /tmp，避免全局客户端映射写入。
- 独立复核确认并正在验证四项修复。已有未绑定 runner 的工具会话首次绑定须核对活动责任，未绑定会话不能代别的 runner 写通信。同 runner 的开放工具绑定按项目/checkout/身份隔离且唯一，旧调用可定位既有绑定。未托管 CLI 也进入现有机器 clients 目录的内容固定缓存。注册失败导致 runner 不存在时允许结束工具绑定。已有 pinned 策略单独修改 --version 现在真实写入。
- 实际两版本任务驱动首次失败为 context publish 返回 KNOWLEDGE_MISSING: AGENTS.md。隔离空项目只有 AEL-owned 受管块，去掉工具块后正文为空，却仍被分类为 critical spec。当前已修正空 AGENTS/CLAUDE 入口为非关键历史引用，保留用户规范正文的 current/critical 行为，新增针对回归。错误证据 /tmp/ael-opt-evidence/opt03-task-updates.log，不得清空或当通过。
- 未托管会话原本只固定原可执行路径，现改为在 share/ael/clients/standalone/<sha256> 保存一个共享固定副本并用现有 version probe 校验。启动器允许在全局入口被删除后直接使用会话缓存。高层会话测试改用真实构建 CLI，避免 Go test binary 冒充可执行产品。
- 当前真实 Git origin 为 github.com/adougebabi/ael。只读查询真实 releases API 返回 HTTP 200 空数组。新增 packaging/client-source.github.json 固定 main 分支现有 release.yml 的 Cosign keyless 身份及真实目录地址，实际 release check 返回 no_available_version。此配置没有被写入真实安装，未 dispatch CI/发布/tag/部署。当前本机未检测到 Cosign，采用该正式身份时仍需提供成熟验证器，scratch Server 使用发行建议时也需运维提供验证器及公共 CA；未具备时准确报告不可验证。
- 下一动作是完成复核修复回归，重新构建 0.1.23 基线和通过 Go overlay 生成的 0.1.24 隔离夹具包，然后重跑真实活动责任、双 Agent 自动更新与失败回退驱动。Go overlay 已核对本地 go help build 和 Context7 /golang/go/go1.26.7，源码不被 fixture 版本修改。
- 当前复核修复回归已通过，日志 /tmp/ael-opt-evidence/opt03-review-fixes-final.log。工具会话测试使用真实 CLI，并实测原始未托管程序删除后，项目入口仍可运行缓存版本。独立完整 Go 回归 /tmp/ael-opt-evidence/opt03-all-tests.log 全部通过，适用在最终空入口上传修复之前。
- 第二次真实链路的空入口仍在 Server 的强制入口快照检查处失败。已保留 Server 门禁，客户端将 AGENTS/CLAUDE 作为数据快照上传，空工具块继续标为非关键历史引用。第三次链路推进到附件字节校验，发现前置 HTML 注释使 Markdown 被 http.DetectContentType 误判为 HTML。已改为仅跳过前置完整注释后检测实际正文，仍拒绝 HTML/SVG/XML 和 NUL。新回归 /tmp/ael-opt-evidence/opt03-markdown-entry.log PASS。失败日志依次保留 opt03-task-updates-reviewed.log、opt03-task-updates-r3.log。
- 正在打包 opt03-base-r4 与 opt03-next-r4，下一动作是重跑 scripts/task_update_acceptance.py 的真实任务链路。不得因文件已存在或单位测试通过切入 OPT-04。
- OPT-03 DONE。真实 driver /tmp/ael-opt-evidence/opt03-task-updates-final/summary.json PASS。两个活动新任务并发领取只下载一次升级包。Server 0.1.23 保持运行，旧会话实际调用缓存 0.1.23，新会话实际调用 0.1.24。0.1.24 为 Go overlay 隔离夹具，未修改本仓库版本。项目 Skill 显式更新后新冻结版本为 0.1.24，原冻结文件 hash 保持，且新项目 Skill 含实际 overlay marker。
- 覆盖坏签名、目录网络失败、下载中断、空发行目录、固定版本、最低版本与协议不兼容、发布身份文件变化、活动 runner 不能换工具、普通查询/心跳不拉目录、最近上报版本查询。最后曾误将合法受管资产备份目录视为 runtime 创建，检查确认只有备份文件，驱动断言修正为无 runtime.db 后完整重跑通过。错误日志 opt03-task-updates-r4.log 保留。
- 现有真实手动升级脚本 /tmp/ael-opt-evidence/opt03-manual-upgrade/summary.json PASS，覆盖 0.1.22 → 0.1.23 的签名升级、指定 prefix、旧 Local 来源、真实 updater kill/恢复、权限错误和坏包保留旧版。额外实际 rollback-summary.json PASS，精确回退到 0.1.22，重复回退 up_to_date，0.1.23 缓存保留。
- 全 Go 最终回归通过，日志 /tmp/ael-opt-evidence/opt03-final-all-tests.log。Claude 短 shim 改为指向冻结 Skill 后，受管入口测试单独重跑 PASS。完整证据与适用源码 hash 位于 evidence/OPT-03。
- 唯一最终 CLI 使用说明为 USAGE.md，涵盖统一初始化、已有项目、开始工作、恢复、更新策略、手动升级与回退。正式来源目录仍为空，Cosign/公共 CA 与后续授权发布/Oracle 部署边界已记录。

### OPT-04 当前验收待办

一个可搜索项目切换器，工作台/规范与决策/项目演进主导航，证据回到任务详情，项目设置/账户/有权服务管理分离，查看真实最近上报客户端版本与策略，紧凑空状态，接入结果分层，不同项目数据隔离、长名称、退出、刷新返回与权限失败均实测。不得重新设计整站或改变配色方向。

### OPT-04 当前进展

- 已读取 frontend-design 与既有 .impeccable 设计约束，保留冷浅色/靛蓝、系统中文字体和原工作台结构。子代理仅定位现有接线与验收，主代理已亲自读取将修改的 App、StandaloneApp、TaskList 和路由。
- 顶部唯一可搜索项目切换器已接入现有 view/generation 机制，完整长名称换行可读。底部分开项目设置、管理员服务管理、账户菜单。三项主导航保留。设置页显示接入/规范状态、真实最近 CLI/Skill/策略和 Hook 上报，技术详情折叠。既有项目级依据检索保留为设置内诊断，主证据仍在任务页。
- 新后台 GET /api/v1/admin/client-release 仅允许管理员会话，项目 token/reader 实测 403。最近客户端版本由后端补兼容判断，始终标 last_reported 与宿主状态未知。定向后端日志 /tmp/ael-opt-evidence/opt04-backend.log PASS。
- 空工作台收起完整表头/空分组，保留最近交付和查看全部入口。实际首轮截图 /tmp/ael-opt-evidence/opt04-browser/remote-long-project.png 已人工查看，长名称与空状态正确，沿用原视觉。
- 首轮真实浏览器已通过单切换器、长名搜索、空项目、设置上报、服务管理、账户、刷新和主要任务流程。失败为旧测试对“阅读实际提供版本”的单一匹配假设，当前入口多了已核验的 AGENTS/CLAUDE 数据引用，故按钮有三个。已将选择限定到 Shared rules 条目，保存 /tmp/ael-opt-evidence/opt04-browser.log 与失败 trace。
- 同时补页面读取序号防同项目旧面板响应覆盖新请求，路由变化关闭账户菜单，移动端设置链接关闭抽屉。正在重新打包，下一动作完整重跑浏览器验收，仍未标 DONE。
- 第二轮浏览器跑到最后失权检查，实际已经清理了旧项目并显示权限变化，但旧反馈为绿色 status。已改为明确 error alert、递增代次并清理敏感旧页资料，保留失败证据 opt04-browser-r2。
- 独立复核发现延迟全局 token 创建响应可进入其他项目，以及零项目管理员无服务/账户入口。令牌现在只在内存中按项目保存并随会话清空，旧操作完成不回写其他项目反馈，设置页重新读取令牌元数据。服务/账户面板抽为两处复用的实际组件，零项目也可访问并创建首个项目。
- 新浏览器回归已加入延迟 token 响应时切项目的确定性竞态，以及独立零项目 Server 的服务/账户/首个项目创建场景。Vue TypeScript 与 Vite 构建通过，正在打包 opt04-package-r4。下一动作完整浏览器重跑，未开始 OPT-05。
- 真实宿主测试复制必要认证/服务配置到 /tmp 的私有隔离目录，未改写全局 Agent 配置，未打印凭据。收尾需删除隔离认证副本并结束本轮宿主进程。
- Git 后续状态中用户原有归档.zip 已不在 untracked 列表。本轮没有对它执行修改或删除，保留用户外部变化。

- OPT-04 DONE。最终打包 build 259cd81-5e916d4aec6c 的真实浏览器两项测试全部通过，含跨项目延迟令牌竞态、零项目管理员、首项目创建、长名搜索、任务证据/交付、重启刷新、失权和退出。持久证据位于 evidence/OPT-04。

### OPT-05 当前验收

- 下一动作是最终包的初始化、恢复、任务前升级、手动升级、Remote 协作及 Local 相关回归，并核对进程清理、帮助与交付说明。Claude 完整真实调用按用户指示跳过。正式发布和 Oracle 部署仍需另行授权。

- OPT-05 首轮最终包初始化、恢复失败修复、Remote 协作、双 Agent 签名自动升级通过，全 Go 回归通过。Local workflow 的 T11 暴露纯受管入口使健康状态由 empty 变 ready，已改为按实际规则正文判断并补特定回归，knowledge 包通过。正在重新打包与最终重跑。首次手动升级驱动传入了仅 release 的旧版目录，随后缺少其发行清单，属于测试夹具组装错误，已补齐完整旧版 bundle 后等待最终包重跑。失败日志保留 opt05-local.log、opt05-manual-upgrade.log 和 opt05-manual-upgrade-r2.log。

- OPT-05 DONE。最终包 259cd81-2f3750e5d7e7 完成初始化/来源不匹配保护、两叶子失败修复与恢复、并发升级一次、会话实际版本固定、手动签名升级与 kill 恢复、精确幂等回退、Remote 10 项协作、Local 12 项、Dashboard 2 项浏览器、最终全量 Go 回归。首次错误旧版夹具的锁文件也已补齐，最后 opt05-final-manual-upgrade-r2 全通过。
- 最终重复 Hook 模拟事件两宿主均有限退出。独立只读核验确认用户示例与实际命令一致。进程检查无本轮 AEL 客户端/Server 遗留，另一个 local-ai-companion 工程的既有 AEL 进程未纳入本轮。真实宿主认证副本在 OPT-01 已清理。
- 证据位于 evidence/OPT-05，最终报告 ACCEPTANCE.md 明确划分确定性驱动、真实宿主覆盖和正式发行边界。没有 Oracle 操作、真实项目注册或正式发布。下一动作是交付后暂停。

## 2026-11-16 授权部署

- 用户明确要求部署 Oracle 并升级 /Users/vinson/Documents/project/个人/local-ai-companion。Oracle 实测 0.1.22 服务健康、ARM64、168 GB 可用，现有 Lucky/derper 正常运行。项目为 Local 0.1.18、Git 干净，按现有 Local 模式升级。
- 当前待办为活动责任核对、维护/完整备份、镜像切换/公网核验/恢复，随后本机项目更新与读回核对。使用现有管理员密码私有文件，避免输出凭据。

- Oracle 部署已完成。镜像 ael-server:0.1.23-2f3750e5d7e7-arm64，完整备份 /backup/pre-0123-20261116T011419Z，位于 ael-v2_backup_data；原配置 /opt/ael-v2/.env.pre-20261116T011419Z。新容器健康，公网版本/构建匹配，维护 active，3 个任务和 29 个对象字节 hash 与升级前一致。公网真实浏览器登录、单切换器、服务版本、退出全部 PASS。专用共享 CLI /Users/vinson/.local/share/ael-oracle-v2 已从 0.1.22 升到 0.1.23。证据 evidence/DEPLOY-20261116。
- 部署中的 Python urllib 公网请求返回 403，正式 Go CLI 和 curl 标准 TLS 请求成功，改用 curl 完成公网检查后立即 resume。服务没有遗留维护冻结。
- local-ai-companion 当前因 PROJECT_BUSY 阻止升级，旧 meromero-first-humanization-plugin-governance 的 AT-4b10a9060912ca46074e 在 2026-11-10 到期、状态仍 running 且无 PID。已向用户请求确认旧执行者停止；确认前保留现场，仅保存只读预检与数据库备份。当前唯一未完成项为该项目升级。

- 用户质疑任务不应阻挡升级。复核确认旧 Local 门禁把所有 running/interrupted 历史一律当有效写入者。已调整预检与同事务复检，过期租约且无存活绑定进程的记录可原样保留并升级，有效租约/存活 PID 或 owner/工作区锁继续保护。没有对业务任务执行 resume/cancel/claim。新增回归覆盖完整升级后任务正文不变与两层活写入者保护。
- 为遵守同版本缓存不可变约束，修复作为 0.1.24 补丁构建。Oracle 0.1.23 已健康上线，接下来回归 0.1.24、更新项目，并把 Oracle 与共享 CLI 对齐该补丁。当前测试/打包进行中。

- 0.1.24 补丁最终全量 Go 回归通过，独立只读复核未发现租约/进程/事务复检缺陷。实际项目 update 从 0.1.18 成功到 0.1.24，90 个 tasks.body 逐字一致，630 个业务资料/宿主配置文件 SHA 一致。旧过期 Attempt 保留原样，没有 resume/cancel/claim。原本的 Local Dashboard 在同一 127.0.0.1:7331 刷新为新二进制，认证后的 info 实测版本与构建匹配。
- Oracle 已对齐 0.1.24 / 259cd81-ca59ab062a95，镜像 ael-server:0.1.24-ca59ab062a95-arm64。再次维护备份 /backup/pre-0124-20261116T012807Z 位于 ael-v2_backup_data，原配置 /opt/ael-v2/.env.pre-20261116T012807Z。新容器 healthy，维护 active，公网真实浏览器登录/单项目切换器/服务版本/退出 PASS。现有3任务与29上传对象 SHA仍一致，Lucky/derper 保持既有持续运行。专用共享 CLI 也已对齐0.1.24。
- 项目升级选择保留现有 Local 权威与任务库，符合当前升级范围。Claude 真实登录交互继续按用户指示暂缓。项目新增受管改动保留工作区。
- 项目 Dashboard 首次就绪探针遗漏本机 API 认证导致超时，补为 /api/session 内存取令牌后调用 info 已通过，未输出令牌。只读数据库备份验收改用 immutable 方式读取已完成的独立快照，生产实时库继续使用正常 WAL 读取。均属验证脚本修正。

- 最终0.1.24隔离Dashboard完整浏览器两项PASS，动态版本断言跟随实际测试Server。真实项目两个入口版本/构建再读一致，Dashboard刷新后90任务正文仍逐字一致，最终全部9个发行资产hash核对通过。所有部署与升级验收已完成，当前任务DONE并交付。

## 2026-11-16 用户要求 Online 模式

- 用户明确指出本地项目尚未同步，要求改online，现有Local保留决定已经由新指示替代。当前范围包含真实项目注册、历史导入、单权威切换、共享轻CLI与宿主恢复接入。
- 已完整读取 authority-transfer.md 和实际 transfer 实现。只读scan有90任务、1601文件引用，1594 available、3 missing、4 unsafe path，7条原本不可用引用保留状态。每任务去重对象1587个，总计39.6MB，单文件最大2.03MB。扫描清单2.2MB。
- 发现迁移scan仍把过期历史当活动责任，已复用更新门禁的有效租约/活绑定进程判定，历史正文不变。导入清单原2MB限制不足，已为明确imports接口提供8MB有界请求容量，普通API仍2MB、文件仍10MB。作为0.1.25补丁实现，目标测试进行中。
- 下一动作为回归/打包/部署0.1.25，冻结并导出本地一致备份，导入/校验/commit后cutover，安装共享轻入口与双宿主恢复，最后公网与CLI对账。当前Local尚未冻结或切换。

- 0.1.25全Go回归与修正旧导航入口后的隔离迁移浏览器PASS，Oracle已健康部署0.1.25。原Local已冻结并保存私有一致恢复备份于 /Users/vinson/Library/Application Support/ael/transfers/local-ai-companion-20261116，digest03e9c9c871d1b2906d07ee1e277bba03bd67ba66219fa29152d1d6cdfbaf770e，90任务1601引用。
- 实际首包stage失败EPIC_NOT_OPEN。真实旧库的普通feature/bug作为Parent用于归属，原迁移器误套Epic创建校验。失败同事务回滚，未创建半批任务/上传；在原已冻结导出上续跑。修复仅对没有显式EpicMembership的历史归属暂以独立节点创建并恢复映射Parent/定义hash/原文投影，原始类型与历史保留，显式Epic仍正常校验，作为0.1.26补丁测试打包。
- 导入采用Oracle同机有限桥接命令复用原CLI/鉴权API，避免1587个对象绕行公网。桥接仅loopback，沿现有可信反代Host/HTTPS头访问，项目token权限不变，finally退出并删除临时token/config。Mac持久profile始终为正式HTTPSURL。

- 0.1.26全Go回归PASS，独立隔离Server使用实际已冻结90任务清单stage通过，并逐条GET核对60个旧Parent映射与原类型，1601引用完整声明。未上传fixture文件，私有实际历史预验仅留/tmp。补丁镜像hash7065b870e783626311ec5181e1f09adb564d0c4bda545e2a7da2b31dc8dbb5bb，Oracle切换进行中，原digest/source-id保持，下一动作重放原import。

- Oracle 0.1.26 / 259cd81-b393c60af771 已healthy、maintenance active。同一计划重放stage成功，90历史任务已暂存，当前原生CLI逐对象上传中，最近340个 finalized，日志/tmp/ael-online-20261116/import-progress-r2.log，有限SSH进程session59025。不得重新建source/project或重做export；完成后沿原verify/commit自动继续，再Mac transfer cutover。
- 当前原项目Local数据库frozen，旧Dashboard进程已停止；Mac专用共享CLI0.1.26已安装，尚无remote binding。原一致恢复目录 /Users/vinson/Library/Application Support/ael/transfers/local-ai-companion-20261116 需保留。

- 上传最近1519/1587 finalized，原import session59025持续正常。另提前核对收尾发现普通Remote入口/重复init原先一律拒绝保留Local配置，会阻挡已正式切换的恢复资料保留。0.1.27客户端补丁允许已存在绑定且原Local由匹配导出冻结的checkout安装入口/幂等init，未冻结Local继续拒绝，新增回归及cmd/ael+bootstrap全包PASS。Oracle当前0.1.26继续服务，协议兼容。共享CLI已升0.1.27，待原import/verify/commit成功后cutover/install-entry/init。

- Online迁移DONE。原同一导出完成import→verify→commit→cutover，云端归档digest c3ba51a4aba21bdc542a9113fb53e8bd0ad2aa7d50e97357a6d8fd51fd986f75，id import-c3ba51a4aba21bdc542a9113fb53e8bd，state active。90任务，46完成/2取消/42待恢复确认；60个旧父子归属完整映射。1594可用文件引用字节核验，3原缺失+4不安全绝对路径保留原状态。
- 原Local90任务正文与升级前逐字一致，authority_transfer冻结仍在，用户AGENTS正文与原Claude settings.local保留，旧Local Dashboard退出。当前项目轻入口实际使用 /Users/vinson/.local/share/ael-oracle-v2/bin/ael 0.1.27，原Local安装资料与一致备份保留，未新复制0.1.27多平台二进制进项目。
- 通过普通项目 ./ael doctor/task list/version --check 验证正式HTTPS、Remote权威、90任务、协议兼容和入口current无用户冲突。公网浏览器核对同一项目90任务、46/2/42计数、客户端最近上报0.1.27、原主任务导入历史与退出，全部PASS。
- 额外按初始化范围注册仓库索引并为原主任务发布当前候选/规范来源快照，4来源均可读且与checkout无差异，未领取责任、未重做历史检查。重复init与Hook安装幂等完成，Codex/Claude均pending_host_trust，Claude真实登录调用继续按用户说明暂缓。更新策略notify。
- 最终全Go0.1.27、客户端/入口包和隔离迁移/浏览器回归均通过。Oracle有限迁移代理/CLI已退出，临时token/config自动删除，云端暂存的旧恢复DB和私有导出副本已清理，原Mac持久一致恢复目录继续保留。最终证据 evidence/ONLINE-20261116。

## 2026-11-16 用户授权GitHub正式发布

- 用户在核实仅手动触发、远端main仍0.1.22且没有tag/run后明确要求处理。本轮授权包含提交/推送代码、版本标签、触发并跟进正式发布。
- SSH推送身份adougebabi已实测可用，当前机器无GitHub CLI/API登录凭据。将main push接为自动构建，v* tag push使用actions:write仓库令牌调度main发行，保持Cosign身份@refs/heads/main；手动入口保留。发布checkout所选tag，固定实际source_commit并验证版本和main祖先关系。
- 独立复核发现普通构建会挤掉pending发行，已分离普通构建、各tag调度、各版本发布并发组，避免相互替换。actionlint和发行脚本6场景回归进行中，全Go回归进行中。
- 本地0.1.27已安装为unpublished，正式CI字节不同，按既有版本缓存不可变要求递增0.1.28作为首个正式在线版本。Oracle0.1.26和实际项目客户端0.1.27继续现有兼容状态。
- 下一动作是完成本地检查，提交并推送完整0.1.28源码及发行workflow，先跟进main构建，再创建并推送匹配tag，跟进正式release并验证签名、平台资产与真实发行源可用性。

- 正式发布源码已提交并推送main，commit ecc9ef0b73a0833658f06fa74db0135a9b43968f，版本0.1.28。包含187个源码/受管资源/文字回执文件；8张验收截图保留本地。提交前归一化了文字回执副本行尾空白，原现场日志仍保留/tmp。全Go与发行6场景回归、actionlint通过。
- main自动构建已实际触发，run43812202292，https://github.com/adougebabi/ael/actions/runs/43812202292。当前唯一动作跟进该构建；通过后创建v0.1.28指向同提交并推送，让标签自动调度main上的正式发布。尚未创建tag，避免为失败构建产生发行标签。

- main首轮run43812202292已执行到签名工具安装，旧cosign-installer@v3请求Cosign3.1.4已取消的detached .sig，HTTP404导致失败。已读取官方v4.1.3实际action配置并改为该确定版本，支持.sigstore.json，保留Cosign3.1.4与完整验证链。下一动作推送CI修复并跟进新的main构建，尚未创建发行tag。

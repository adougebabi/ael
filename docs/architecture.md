# AEL 结构与数据边界

Go 单进程服务/CLI，Vue3/TypeScript 编译并 embed。仅依赖本机文件系统与嵌入 SQLite，无云服务。任务定义在 .ael/work-items Markdown JSON frontmatter（JSON 是 YAML 子集）；SQLite 保存实际调度、Attempt、事件、证据。终态 JSON 是内核投影。运行目录按 canonical checkout 路径 hash 隔离；AEL_DATA_DIR 可配置。

共享领域 Service：CLI/HTTP 调用同一 create/claim/submit/verify/confirm/next。四预设产生有序步骤；验证项独立 claim。不同任务不再对同一工作区互斥；同任务的领取与恢复仍检查已有 Attempt 和租约。旧版 workspace_locks 只保留兼容与更新期间的保护，旧记录由所属 Attempt 提交或恢复清理。文件 outbox 存意图、原 hash、新内容，flush 原子替换，repair 对比 hash 并保留用户冲突。

候选包含 Git HEAD 和所有参与目录中的普通文件（含未跟踪），保守失效。不遍历 .git/node_modules 和 AEL 任务/摘要/导入投影；不跟随 symlink。检查前后核对指纹，配置与验收修订入证据。人工检查显式记录人、理由与产物。与同一 OS 用户下具有文件写权限的恶意程序之间不构成安全沙箱。

0.1.7 并发调整：共享工作区的不同任务可以同时运行；AEL 不协调它们对业务文件或外部资源的写入，也不把工作范围当作完整依赖图。其他任务的文件变动可能使整个工作区候选、验证或只读检查失效。保留完整候选校验以避免错误复用证据；需稳定验证时可协调时机或使用已绑定的隔离工作区。运行库的 SQLite 写事务、全局 outbox 和工具更新维护窗仍有各自的限制，见并发检查报告。

参考核查（2026-11-03）：mini-trellis 0.3.2 @ 3b404f59cc8389d6d25e1a2b121a7465093abbcb（AGPL-3.0）；write-notes-like-deepseek 0.1.0 @72573a5b179de7798bbb7c4aeacdc0df6bf62439（未发现 LICENSE，授权未知）；Trellis0.6.17 @f089cb3286071199e84118dee86b6d76aab032f1（AGPL-3.0）。仅借鉴短入口、主题更新/替代、决策理由/时间线，不复制源码或模板。

Codex官方 noninteractive/skills 文档核对于2026-11-03；本机0.144.1。exec --json 是事件，--output-last-message 是结果文件，--output-schema 约束最终 JSON。resume 必须精确 session ID，禁止 --last；保留默认安全机制。技能安装路径 .agents/skills。官方入口：https://developers.openai.com/codex/noninteractive 与 https://developers.openai.com/codex/skills。

终态使用专用Outcome投影，排除完整Attempt、租约token、PID和工作区绝对路径；迁移提交文件仅携带相对映射与hash，原始快照在运行目录。项目policy.yaml内容hash绑定任务，变化时生成新定义/计划修订并等待确认；显式轻量预设不能降低风险信号所要求的门禁。

0.1.3项目分发：./ael为POSIX入口，.ael/toolchain.lock绑定平台与SHA-256，四平台独立可执行文件随项目Git提交。工具文件与本机运行状态分离；候选包含启动器和锁文件，.ael/bin不重复作为源码输入。init/update通过受管哈希保护与bundle-install恢复记录安装工具资产，平台包不写入SQLite BLOB。

0.1.3可用性修正：默认无时间硬上限，显式正预算计有效执行区间并合并重叠；可单独调整运行预算，不改验收版本。默认30分钟租约，Attached可绑定真实常驻进程身份（含启动时间防PID复用）独立续约，Managed/verify持有生命周期续约并在失败时停止子进程；过期fence及未知旧writer仍不得自动恢复。

新增records为仓库内脱敏检查点/历史投影，与SQLite更新使用同一outbox事务。只导入缺失/外部更新的记录，不把旧检查/批准当作当前授权；未完任务暂停待恢复，验证/交付需重新执行。DB仍是本机调度权威；records不携带租约、token、PID、session、workspace或审批。删除清理owned文件/DB记录，deleted小型标记传播跨clone删除，依赖与活跃writer保护保留。Host与Origin跟随实际请求authority校验，默认允许0.0.0.0监听；会话token不充当远程身份认证。


0.1.4 通用协议整改：保留领域内核/SQLite/outbox/候选/租约。新增知识来源账本指向原文，ReadAll/Search/Context/看板共享读取链。任务运行态记录 BasisReference（ID、源路径、hash、状态、适用理由）和 ContextAttempt；不是第二套任务定义。execute 提交校验实际获得当前版本，知识收敛声明与工件在验证前固定。不可变知识历史快照位于运行目录 knowledge-versions，hash 核对原始字节，当前原文仍是唯一可写规范。

显示修订不改验收版本；检查独立变化在同一候选复用未变证据，标 reused_from 并保留旧记录；实质范围变化重新实施/分析，验收、权限与政策边界准确审批。policy 和候选中的 policy 输入按解析语义归一，排版不失效。

失败 repair 为运行态 Route；Blocker 给类别/原因/范围/已尝试/解除条件/恢复点。EvidenceHistory 保留失效证据。Inspection 模式在每次成功提交核对候选未变，工件必须放 runtime；它检测越权修改，不保证外部进程不能先写文件。report 交付只要求必要检查实际获得 pass/fail，环境 blocked 不计执行完成；verified 仍要求全部必要检查通过。模型/场景/人工 Assessment 与命令证据分开，不强制每次新起 Agent。

看板使用同一 Service 读取稳定任务详情、知识来源及使用关系；轮询当前视图，401 重新取 session，失败保留 stale 提示。服务 build/可执行路径/运行数据/入口版本分别显示。没有引入新数据库、DAG 或 SDK。


独立复核后的边界修正：必需且适用的依据不受普通参考资料预算裁剪；额外查询命中不进入必需依据集合。ignored 来源先核对 hash；显式迁移权威切换在来源账本标 retired/authority_path，未声明时拒绝静默选择副本。任务详情从已提供 Basis 快照出发，当前来源损坏也保留旧版本可读。InspectionCandidate 跨失败/恢复固定，Attempt 绑定定义与计划版本；离开 verify 时中断旧同伴，不能用旧检查推进新责任。所有 managed 审查失败通过 Assessment 记录 blocked，最终门禁使用最新有效检查结果。

UI/UX 标志只声明需要对应能力，不推断具体验收映射；自动占位不再复制所有 acceptance，缺具体 covers 的定义明确拒绝。已有明确 UI/UX 检查默认 scenario，可显式指定其他真实方法。纯后端保留原最小检查路径。


0.1.5 升级接口：upgrade 只负责本机安装和不可变版本分发缓存，update 只负责明确的已初始化项目（默认当前目录）。来源登记保存在受管安装中，项目更新从已保存分发取包；旧客户端按来源可执行文件的版本/构建/校验值委派给新程序执行，避免旧 Skill 混装。没有新增在线平台或自动扫描业务项目。旧命令拒绝执行，不保留别名。

项目更新直接复用 InitProject 的受管资产检查/outbox 与安装过程，dry-run 提前完成所有受管文本冲突检查而不打开写库。运行状态查询使用 SQLite file URI mode=ro，仅检查已解析的同一运行实例，拒绝活动/中断单元、workspace fence 和 pending outbox；工具维护锁防止新内核并发领取，不自动杀旧进程。项目外备份由原资产内容 hash 定位，保留所有旧受管文件及用户配置；升级只替换 config.asset_version，未知配置字段保留。

本机安装复用相同分发校验，要求四个平台与 notice 路径互不冲突，拒绝降级、用户二进制改动与同版本不同 payload。旧可执行文件和安装元数据保留；upgrade-intent.json 在替换前持久化，精确包可完成中断元数据提交。安装脚本只是 upgrade 的兼容入口，避免两套安装规则。

升级并发复核修正：维护标记与空闲检查在 BEGIN IMMEDIATE 内提交，现有 tasks/workspace_locks/submissions/events 写路径由条件触发器保护，兼容旧内核 SQL；Claim 事务读取同一维护标记与代次，拒绝迟到的更新前快照。异常中断保留维护记录/原备份，已停止 owner 可用 exact-source update --ack-stopped 恢复，不放行未知 writer。来源程序按自身嵌入资产校验完整安装并出具源包 hash 回执，委派方核对所有必要文件和 manifest，不能只校验版本字段与四个二进制。

页面可用性整改（2026-11-04）：新增只读任务摘要、资料分页/单条阅读、显式关联的项目变化、迁移影响汇总。它们不是新的调度或演进状态；任务仍由 Board/Next 决定分组与下一动作，验收仍使用 CurrentEvidence 的候选、定义、计划和配置校验。摘要只从实际工作/结果事件取有效活动时间，心跳与 Updated 不代表进展。历史失败单独显示，不作为当前候选失败或通过的门禁依据。

演进投影只接纳明确任务结果、有产物的成功实施节点、显式决策与具有变化描述的规范。共享路径、近似标题、相同日期和一般范围匹配不用于合并；有明确多任务声明的变化资料可汇总这些任务，其关联证据仍可逐个打开。历史导入的占位完成记录、普通历史资料及扫描/观察/进程事件不进入默认变化流。发生时间缺失则保持未知，发布要求成功且候选相符的交付回执。可选 summary/rationale/occurred_at 为源资料显式元数据，不批量补写旧正文。

Markdown 使用随应用打包的 markdown-it，关闭原始 HTML、图片自动读取和自动链接，保留默认危险协议校验并拒绝 data/协议相对链接。相对文档链接经后端已索引来源白名单及 SafePath 检查；不存在、越界和 symlink 目标不能通过阅读器获得额外文件权限。任务证据沿用 TaskArtifact 的可访问范围，只有经真实字节类型检测的 PNG/JPEG 可以作为图片展示，其他内容仍为纯文本。无外部 CDN 或远程字体。列表响应有分页且不带正文；当前后端仍从文件权威来源读取全部元数据，不声称已经建立大型索引数据库。

最终交互边界：任务列表以一次请求携带各组 offset，先收集并去重 ID，再调用共享 Next；前端仅按返回的权威 board 分组。检查执行状态与有效验收结果分别投影，执行中项不计入尚未运行；不从执行中推断通过。HTTP 人工通过入口使用 ReviewExact，将候选、定义与计划修订绑定到操作者实际审阅的版本，原 Review CLI 入口保留；原声明方法、产物和候选指纹校验继续执行。前端只有读取请求设置恢复超时，命令检查和迁移写入等待实际回执，不因读取超时阈值把仍在执行的操作标为失败。

## 2026-11-12 · AD-AGENT-DRIVEN-01

远程新默认是 Agent → Client → Server：外部 Agent 管宿主编辑/检查/截图/Git，Client 只通信和读取选定输入；Server 复用身份、项目权限、责任租约/fence、候选、规范、附件、幂等及状态机判定。取消通信对宿主隔离的总门禁；旧远程 run/resume-result/recover-claim 明确拒绝，不可进入历史 loop/process/copy/delivery。稳定 Local Managed 仍为显式兼容功能。Oracle Docker 部署属于后续授权任务，客户端不强制 Docker。

租约保护接口接收权，不证明宿主 OS 停写或进程终止；Agent 必须遵守停工请求。共享未知工作区不能自动安全接管。最小权限与撤销、项目隔离、固定 finalized 字节、版本/候选绑定及基本凭据防泄露继续保留。请求与领取租约使用项目外私有文件；CLI 输出去除 token，不默认采集 HOME/聊天/整库。哈希与外部命令报告不构成执行真实性签名，也不能替代独立审批。

verify 结果必须携带 runtime:report.json，绑定 candidate/check/definition/plan/method/source/covers/observations，不能用单独摘要、任意文件或退出码满足门禁。远程证据可用性由授权附件接口查询，不按 Server 的源码目录推断原件丢失。

候选清单增加可选 index_executable 位（纳入现有内容 ID），修复仅暂存执行位变化遗漏；普通非可执行索引的 ID 不变。旧清单继续作为历史读取；含该位的新候选必须由支持它的 Server 与 CLI 配套使用，升级后重采样并复验，不将旧候选 ID 改写。NB-010 的旧失败原文保留，修复测试见 TestPortableCandidateTracksStagedExecutableMode。本项不承诺同 UID 对抗隔离、任意命令拦截或断电级文件系统持久性。

## V2-13 双客户端恢复

正式通信入口将固定请求和上传快照保存到项目外私有配置目录，使用操作文件锁防止同一操作并发改写。最多三次有界通信尝试。重连先查服务端持久回执，命中后校验原请求摘要，未命中则用原 token/fence 续租核对责任，再重放原请求。服务端从严格解码前的规范化 JSON 计算 request_digest，与普通操作回执或提交事务一起持久化。旧回执继续使用原接口完整幂等校验恢复。

新增 remote_workspace_holds 记录已声明工作区的未知旧责任，metadata 保存一次交接的接收方领取约束。runner recover 复用 Store.Write、领域 save、事件、任务版本和 p.mutate。显式确认停工或选择独立工作区后撤销旧接口责任，旧 Attempt 保留历史。实施已完成时可把完全相同候选交给另一身份验收，历史 PASS 保留，接收工作区重新提交检查。对未知声明工作区的后续领取保持拒绝，直至停工确认或独立交接。

远程 cancel 可以撤销活动责任的有效提交权，旧执行保留 interrupted/unknown 状态供后续确认。Local 的原进程恢复规则继续适用于其显式入口。新增状态字段仅描述接口责任和最近上报，宿主隔离由外部执行环境负责。数据迁移为两个 CREATE TABLE IF NOT EXISTS 和可选响应字段，旧回执与任务数据保留。

## V2-14 统一远程读取与协作状态

StandaloneApp 保留登录、项目和账户管理，将项目工作区接入既有 App/TaskList/TaskPage/KnowledgePage/EvolutionPage。api 的远程传输适配绑定项目及会话代次，页面资源沿用请求序号丢弃旧响应，滚动与焦点按项目和会话区分。sync 提供项目、实例与事件水位组成的游标，发生变化或重连后重新获取权威摘要。

远程 Dashboard 读取逐次执行项目授权，任务投影使用 RemoteTaskView，角色控制继续沿用原 mutation 入口。责任有效性、最后上报与未知宿主状态分别投影，心跳不会制造工程成果。可选客户端标签使用 remote_runner_labels 保存，旧登记回执和旧客户端结构继续可用。

中央知识读取仅使用已 finalize 且哈希匹配的 context 来源，复用现有资料分页、Markdown 阅读和演进投影。多个当前任务版本冲突时提供选择。正文相对链接沿同一 context 解析并携带目标版本，无法接入的链接明确失败。Server 不从客户端路径读取规范或证据。

## V2-15 结构化发现

Finding 使用既有 portable check report 作为接入点，存储于同一中央项目数据库的 findings/finding_occurrences。p.mutate 将检查报告、发现、出现记录、幂等回执和事件置于同一事务。领域规则只规范字段、构造身份和计算证据缺项。Task/Next/CurrentEvidence 保持原有验收职责。

归并键包含任务、检查、配置、定义/计划版本、候选、规则、种类、路由和精确定位。出现不可覆盖，保留真实鉴权 actor 与声明 method/tool/run。最新 portable observation 决定诊断候选是否仍为当前，版本变化不套用旧结果。证据读取核对 finalized、任务/候选或合法引用、实际字节及 hash，页面区分报告时完整性与当前可用性。

资料可通过明确 context 绑定上传，校验当前修订与候选，未改变任务已接受候选或执行责任。结构化发现为非门禁记录，模型建议不改任务状态。中心 SQLite 备份包含发现表，Git TaskRecord 沿用既有边界。

## V2-16 远程人工结论与图片比较

人工入口复用 validateHumanReview、ReviewAccess、意见确认与 submitTx 状态机。远程传输在原身份/项目/Origin/CSRF 授权边界内要求 reviewer/admin 浏览器会话，actor 从会话生成。p.mutate 在同一中央事务内保存短期无进程人工 Attempt、Evidence、流转与请求回执。正式提交以请求时 Next 的状态修订和候选/定义/计划二次核对，附件读取只使用 finalized 中央对象和合法任务引用。普通执行者令牌继续不能声明人工裁定。

意见继续非门禁，正式 pass/fail/blocked 保存既有恢复条件与下一动作。旧候选意见不能绑定新候选。ResumeBlockedHumanReview 的原阻塞归属、无旧 writer、预算与精确版本规则供远程恢复共用。一般候选观察只在晚于已接受实施结果时使不同候选失配，实施前的输入观察不会覆盖其正式输出。

比较图像由外部宿主生成并上传。界面明确选择基准与实际，显示中央 ID、候选、上传者、来源、发现中的页面/视口/环境，并提供缩放、叠加及问题定位。选择随草稿保留，变化后重新确认。正式记录保留所选图片 ID、hash 和来源，历史基准可显示，实际图仍须匹配当前候选。未完成上传、缺失和清理状态来自中央对象，不访问客户端路径。可视比较辅助人工判断，门禁继续由必要检查与交付目标决定。

远程仅检查定义变化的候选复用通过 MatchRemoteCandidate 传输环境核对中央不可变候选清单及后续观察顺序，保留同一候选下配置未变的检查证据和原版本历史。定义与计划仍生成新修订，旧表单受到拒绝。此校验描述服务端已接受输入；客户端未上报的宿主变化继续由外部执行者负责。Local 使用原 Fingerprint 核对路径。

## V2-17 发现、修复与复验

finding_history 在中央项目 SQLite 中追加裁定、修复关联和复验记录，Finding 当前指针与同一事务回执一起保存。裁定从真实身份得到 actor、角色和会话/客户端来源，非问题与接受权衡要求 reviewer/admin 会话。裁定改变发现的调查状态，任务门禁继续由原定义和 CurrentEvidence 决定。

修复创建/关联复用 createTaskTx、linkEpicTx 和原 RemoteOperation 版本/计划预留守卫，在 p.mutate 事务中同时保存任务、反馈、必需父成员、发现关联、历史与回执。相同发现只保留一个当前修复关联，跨操作键也返回已有关联。新叶子采用 change/verified，继承来源范围、验收、检查、规范、非目标及复现信息。可选政策检查保持原配置，通过独立必需检查承载修复验收。已结束父任务的反馈使用新的后续任务，原父结果保留。

上下文保存一次对应版本的任务定义快照，旧发现可从当前定义或原定义修订事件恢复来源。修复上下文提供原始 finding_sources 诊断材料，执行权限继续依据当前任务和项目授权。外部 Agent 自行领取、管理工作区、修改、提交和检查。

复验只引用关联任务的最新有效通过证据，核对准确任务版本、新候选、同一显式仓库、原检查映射及 finalized 字节/hash。修复提交和裁定均不自动关闭发现。源出现记录和旧结果保留，新候选的同规则、检查、路由与精确位置通过同任务或明确修复关系建立版本关联，原 fingerprint 继续包含候选与定义/计划版本。

演进投影将明确修复来源和实际复验证据附加到已有任务交付，规范/决策聚合保留这些事实和引用。同一有效证据的重复确认按证据标识去重，发生时间来自原检查。普通扫描和裁定不会制造工程进度。

## V2-18 独立发行与显式来源

发行层独立于项目初始化与远程绑定，release verify/check/download 在本地数据库打开前处理。独立 CLI、Linux Server、Skill 资源和兼容四平台离线包共用 manifest，记录完整 commit、build、源码 dirty、发布状态、协议及字节 hash。Hash 校验和发布身份各自返回事实。在线选择仅用用户显式来源的真实资产 URL，并先通过成熟 OpenSSH 或 Cosign 验证器绑定固定发行身份，再接收兼容的 released 清单。客户端不安装验证器、不管理签名私钥，也不执行下载内容。

Server 镜像从 scratch 打包现有静态程序和 Dashboard，保留 notice，以非特权 UID/GID 和专用空数据卷运行。非 loopback 监听要求实际 Origin；既有 Host/Origin、身份、项目权限、CSRF、限流和可信代理守卫继续生效。本地未发布构建、CI build artifact、草稿及公开发布分别标明。手动工作流将产物构建和签名与显式授权的发布 job 分开，已公开版本拒绝覆盖，预发布元数据与 SemVer 一致。实际 Oracle 部署仍待对应授权任务。

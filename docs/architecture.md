# AEL 结构与数据边界

Go 单进程服务/CLI，Vue3/TypeScript 编译并 embed。仅依赖本机文件系统与嵌入 SQLite，无云服务。任务定义在 .ael/work-items Markdown JSON frontmatter（JSON 是 YAML 子集）；SQLite 保存实际调度、Attempt、事件、证据。终态 JSON 是内核投影。运行目录按 canonical checkout 路径 hash 隔离；AEL_DATA_DIR 可配置。

共享领域 Service：CLI/HTTP 调用同一 create/claim/submit/verify/confirm/next。四预设产生有序步骤；验证项独立 claim。写锁在整个可写 Attempt 保留，过期后也不擅自抢占。文件 outbox 存意图、原 hash、新内容，flush 原子替换，repair 对比 hash 并保留用户冲突。

候选包含 Git HEAD 和所有参与目录中的普通文件（含未跟踪），保守失效。不遍历 .git/node_modules 和 AEL 任务/摘要/导入投影；不跟随 symlink。检查前后核对指纹，配置与验收修订入证据。人工检查显式记录人、理由与产物。与同一 OS 用户下具有文件写权限的恶意程序之间不构成安全沙箱。

参考核查（2026-11-03）：mini-trellis 0.3.2 @ 3b404f59cc8389d6d25e1a2b121a7465093abbcb（AGPL-3.0）；write-notes-like-deepseek 0.1.0 @72573a5b179de7798bbb7c4aeacdc0df6bf62439（未发现 LICENSE，授权未知）；Trellis0.6.17 @f089cb3286071199e84118dee86b6d76aab032f1（AGPL-3.0）。仅借鉴短入口、主题更新/替代、决策理由/时间线，不复制源码或模板。

Codex官方 noninteractive/skills 文档核对于2026-11-03；本机0.144.1。exec --json 是事件，--output-last-message 是结果文件，--output-schema 约束最终 JSON。resume 必须精确 session ID，禁止 --last；保留默认安全机制。技能安装路径 .agents/skills。官方入口：https://developers.openai.com/codex/noninteractive 与 https://developers.openai.com/codex/skills。

终态使用专用Outcome投影，排除完整Attempt、租约token、PID和工作区绝对路径；迁移提交文件仅携带相对映射与hash，原始快照在运行目录。项目policy.yaml内容hash绑定任务，变化时生成新定义/计划修订并等待确认；显式轻量预设不能降低风险信号所要求的门禁。

0.1.3项目分发：./ael为POSIX入口，.ael/toolchain.lock绑定平台与SHA-256，四平台独立可执行文件随项目Git提交。工具文件与本机运行状态分离；候选包含启动器和锁文件，.ael/bin不重复作为源码输入。init/refresh通过受管哈希保护与bundle-install恢复记录安装工具资产，平台包不写入SQLite BLOB。

0.1.3可用性修正：默认无时间硬上限，显式正预算计有效执行区间并合并重叠；可单独调整运行预算，不改验收版本。默认30分钟租约，Attached可绑定真实常驻进程身份（含启动时间防PID复用）独立续约，Managed/verify持有生命周期续约并在失败时停止子进程；过期fence及未知旧writer仍不得自动恢复。

新增records为仓库内脱敏检查点/历史投影，与SQLite更新使用同一outbox事务。只导入缺失/外部更新的记录，不把旧检查/批准当作当前授权；未完任务暂停待恢复，验证/交付需重新执行。DB仍是本机调度权威；records不携带租约、token、PID、session、workspace或审批。删除清理owned文件/DB记录，deleted小型标记传播跨clone删除，依赖与活跃writer保护保留。Host与Origin跟随实际请求authority校验，默认允许0.0.0.0监听；会话token不充当远程身份认证。

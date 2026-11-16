# 本轮优化验收

本轮 OPT-00 至 OPT-05 已完成实施与隔离验收。基线为 main 的 259cd8139c4bedac1ab8d91c6b6466bc536ec79d。最终源码版本 0.1.23，打包 build 为 259cd81-2f3750e5d7e7，publication 为 local_unpublished。当前改动保留在工作区。2026-11-16 的授权实际部署与后续 0.1.24 修复见文末。

## 最终包的实际验证

| 范围 | 结果与实际覆盖 | 持久证据 |
| --- | --- | --- |
| 全量 Go | `go test ./... -count=1` 全部通过 | evidence/OPT-05/go-test.log |
| 初始化 | 真实 CLI/Server 创建、第二机器配置同 ID、重复接入、同名歧义、权限与断网、HTTPS 边界、丢响应恢复、本地冲突保护、隐藏密码、来源不匹配预检 | evidence/OPT-05/onboarding-summary.json |
| 任务与恢复 | 同父任务两个叶子、实际命令失败后修复通过、保留已完成项、独立会话、完成后清理指向、权限撤销识别 | evidence/OPT-05/recovery-summary.json |
| 恢复变化识别 | 候选/规范/责任变化、其他执行者、工作区与会话隔离、无当前任务、离线限制、凭据脱敏由最终完整 Go 回归覆盖 | cmd/ael/recovery_test.go 与 go-test.log |
| 自动升级 | 两个新任务并发只下载一次、旧活动会话实际执行 0.1.23、新会话执行 0.1.24、冻结 Skill、固定策略、坏签名/网络/下载中断保留旧版、最低版本/协议限制 | evidence/OPT-05/task-update-summary.json |
| 手动升级与回退 | 真实签名 HTTP 源 0.1.22 升至最终 0.1.23、指定安装、双项目共享、updater kill 后恢复、权限失败与篡改包保护，精确回退及重复回退，保留新版缓存 | evidence/OPT-05/manual-upgrade-summary.json 与 rollback-summary.json |
| Dashboard | 最终内嵌页面的两项真实浏览器测试通过，覆盖单切换器、长名、空项目、任务/证据/交付、刷新重启、失权、退出、延迟 token 跨项目竞态、零项目管理员入口和首项目创建 | evidence/OPT-05/browser.log、dashboard-summary.json 与截图 |
| Remote 协作 | 同单元抢占、独立任务并行、丢响应、重启、显式交接、失权、跨工作区幂等冲突、候选与证据隔离、开发/验证交接 | evidence/OPT-05/collaboration-summary.json |
| Local 保留能力 | 安装后 CLI 的 T01 至 T12 全通过，覆盖任务/规范、失败修复、门禁与确认、实际检查、附件和交付、健康状态、卸载及用户冲突保护 | evidence/OPT-05/local-summary.json |
| Hook 与退出 | 最终 CLI 重复 PreCompact 和 SessionStart 的有限调用通过，安装/未信任/不支持与用户配置保护测试通过。进程快照没有本轮 AEL 客户端或测试 Server 遗留 | evidence/OPT-05/hook-deterministic.json、processes.json 与 go-test.log |
| 接口与文档 | 独立只读核验 init/session/recovery/upgrade 示例均有实际实现，Codex/Claude 入口语义一致。`git diff --check` 通过 | USAGE.md、Skill 与命令源码 |

0.1.24 是 Go overlay 生成的隔离升级夹具。签名网络源使用临时 OpenSSH 发行身份。最终 Server 在自动客户端升级期间维持 0.1.23。测试工程、Server、机器配置、安装与凭据均隔离于真实业务。

## 真实宿主覆盖

- Codex CLI 0.144.1 已在隔离 CODEX_HOME 和工程中完成真实项目/Hook 信任审阅与精确 session resume。恢复后的模型返回了同会话 recovery check 命令。真实 compact 尚未验收。
- Claude Code 2.1.154 的实际 startup/resume Hook 曾返回 success。完整模型交互按用户关于本机未登录的说明暂缓。本轮交付 Claude 项目短入口、Skill shim、Hook 合并与显式恢复命令，保留配置与确定性验证证据。
- 确定性工程驱动完成实际文件修改和命令 fail → repair → pass。完整真实工程 Agent 的压缩恢复闭环尚未验收。实际宿主覆盖保存在 evidence/OPT-01/host-coverage.json，模拟事件另存 hook-deterministic.json。
- 真实宿主测试的认证配置副本已清理。最终进程检查发现另一个 local-ai-companion 工程已有 AEL 进程，该进程属于本轮范围之外。

## 验收中修复的问题

- 真实 PTY 暴露密码提示先于关闭回显的竞态。修正输入顺序后重跑通过。
- 恢复核对补上本地候选与责任 context 的直接比较，并保留显式 config-dir。
- init 的 CLI 来源一致性检查前移到共享安装提交之前。
- 纯受管 AGENTS/CLAUDE 入口按非关键历史引用提供快照，保留 Server 的入口快照门禁。Markdown 前置完整注释后按实际正文检测内容类型。
- Dashboard 延迟响应按项目归属保存令牌，页面读取按代次隔离。零项目管理员可以直接进入服务与账户。
- 最终 Local 回归发现纯受管入口使空工程显示 ready，已修复为依据实际规则正文判断 empty，并补针对回归。最终包全量 Go、Local、恢复、升级和浏览器重跑通过。
- 手动升级验收初期旧版夹具目录缺少完整发行与锁文件，补齐夹具后最终驱动通过。失败日志保留于 /tmp/ael-opt-evidence/opt05-*.log，工作账本记录修复经过。

## 发行与部署

已提供实际 GitHub 仓库的来源配置 packaging/client-source.github.json，绑定现有 main 分支工作流的 Cosign 身份。只读检查得到真实 releases 目录为空，CLI 返回 no_available_version。正式在线发行尚未可用。

使用该来源仍需有权人员审阅并发布匹配平台产物和签名。采用 Cosign 来源的环境需要成熟验证器与公共 CA。Oracle 的镜像部署、兼容配置和服务重启需要后续明确授权。

## 交付入口

- 唯一进度账本为 WORKLOG.md。
- 唯一用户操作说明为 USAGE.md，覆盖首次接入、连接已有项目、开始工作、恢复、更新策略、手动升级和回退。
- 最终包位于 dist/release，包含 macOS arm64/amd64、Linux arm64/amd64 客户端以及 Linux Server 与 Skill 包。manifest.json 保存各资产 hash。
- evidence/OPT-05/source.json 保存最终适用源码逐文件 SHA-256。独立变更扫描给出的 README/DESIGN 提示已核对，本轮新增流程集中记录于指定 WORKLOG、USAGE 和本报告。构建已更新内嵌资源引用。

## 2026-11-16 授权实际部署与项目更新

用户明确授权部署 Oracle，并升级 local-ai-companion。实际执行先上线0.1.23，在用户指出升级不应受过期任务影响后，修复 Local 更新器对所有 running/interrupted 历史一律阻挡的问题。修复预检和事务内判断，过期且没有存活绑定进程的历史可保留原文并升级，有效租约、存活绑定 PID/owner 和工作区锁继续保护。

修复采用0.1.24补丁，遵守已有版本缓存字节不可变约束。最终版本与构建均为0.1.24 / 259cd81-ca59ab062a95。

| 目标 | 实际结果 |
| --- | --- |
| Oracle Server 与 Dashboard | 0.1.24，容器 healthy，维护 active，公网登录/工作台/服务版本/退出通过 |
| Oracle 数据 | 现有3个任务与29个上传对象的数量、字节SHA与升级前一致 |
| Oracle 备份 | ael-v2_backup_data 卷 /backup/pre-0124-20261116T012807Z，完整备份校验通过，前一轮0.1.23升级备份也保留 |
| Mac Oracle 专用共享 CLI | /Users/vinson/.local/share/ael-oracle-v2/bin/ael 已升0.1.24 |
| local-ai-companion | 0.1.18升0.1.24，保留原Local模式，90个任务正文逐字相同，630个业务/宿主文件SHA相同 |
| Local Dashboard | 原有127.0.0.1:7331入口刷新为0.1.24，新进程的认证info返回匹配版本/构建 |
| 回归 | 最终全量Go及最终包隔离Dashboard两项浏览器回归通过，新增过期历史/有效租约/绑定进程/owner及事务复检测试通过，独立复核通过 |

升级前向用户询问是否停止旧执行者，是旧门禁造成的流程障碍。此后按用户纠正修复了门禁，本次升级保留过期任务原样，未通过任务恢复或取消来获得更新权限。

实际部署与验证证据位于evidence/DEPLOY-20261116/patch0124。生产镜像以本地校验包上传并固定引用，正式公开在线发行仍无可用版本。Claude完整登录调用继续按用户说明暂缓。

## 2026-11-16 实际Online迁移

用户明确要求将local-ai-companion改为online。现已完成Local到Oracle Remote权威迁移，项目目录正常入口实测mode=remote、online=true、authoritative=true。直接页面为 https://ael.adoumi.site/#/tasks?project=local-ai-companion。

| 验收项 | 实际结果 |
| --- | --- |
| 项目与版本 | project_id local-ai-companion，Server/Dashboard 0.1.26 / 259cd81-b393c60af771，共享CLI与入口0.1.27 / 259cd81-536b3a1781e8，compatible=true |
| 任务 | 90个全部导入，46完成、2取消、42未完成项待恢复确认，60父子归属逐条核验 |
| 历史文件 | 1594可用引用核验字节，3个原件缺失、4个旧绝对路径不可安全读取均保留原状态 |
| 当前来源 | 原主任务的4个当前规范/入口快照可读，与checkout无差异，业务责任未领取 |
| 权威与恢复 | 原Local任务库冻结，90任务正文完整保留；仓库外持久一致备份在 /Users/vinson/Library/Application Support/ael/transfers/local-ai-companion-20261116 |
| 客户端接入 | 共享轻入口、Codex/Claude短入口与恢复配置已安装，两个Hook均待宿主信任；策略notify |
| 真实验收 | 项目CLI读回、公网浏览器、中央库归档摘要/90状态/60归属核验通过，最终全量Go与隔离迁移回归通过 |
| 收尾 | 旧Local Dashboard退出，Oracle有限迁移进程与私有暂存/临时凭据清理完成 |

实际历史暴露了2.2MB清单超过旧2MB限制、过期责任误计为活跃，以及旧普通任务Parent被误当Epic成员三项兼容缺口，均已修复并有对应回归。导入失败事务完整回滚，沿原导出/操作键恢复。客户端补齐了保留冻结Local恢复资料时的轻入口和幂等初始化接线。

迁移曾使用Oracle同机有限loopback桥接，复用原CLI与现有可信反代头及项目token鉴权，减少千余对象绕行公网。命令结束后退出并清除临时凭据，正式Mac profile始终使用原HTTPS地址。生产项目图片仅用于仓库外浏览器验收。

完整回执与适用源码hash位于evidence/ONLINE-20261116。正式公开在线发行仍无默认可用版本，Claude完整登录调用按用户要求暂缓，宿主Hook信任仍待用户在宿主审阅。

## 2026-11-16 正式在线发行

用户授权处理GitHub发布。产品源码已提交并推送，正式版本采用0.1.28，为已安装的本地0.1.27构建保留独立版本标识。

main构建43813390505、标签调度43814091823与正式发行43814104078均成功。发布流程实际修复了旧Cosign installer对已取消.sig文件的依赖，以及新runner缺少rg造成的打包探针失败。独立复核补齐了构建与正式发行并发队列隔离。

正式Release为https://github.com/adougebabi/ael/releases/tag/v0.1.28，源码a176ada242ca574cd93aed9f34e6fbd2059718f8，build a176ada-42b8537d40ba。12个文件包含4平台客户端、2平台Server包、2平台Server镜像、Skill、完整离线包、manifest和Sigstore bundle。

真实发行源已通过Cosign固定main身份独立验签，实际下载macOS arm64包、核对压缩包与包内可执行文件hash，并执行正式客户端version确认。真实签名升级源对现有0.1.27安装到0.1.28的dry-run成功。本项验证涉及发布与更新路径，实际运行中的Oracle为0.1.26、项目共享CLI为0.1.27。

当前main push自动构建artifact，版本tag push自动调度main正式发布，workflow_dispatch手动选择继续可用。完整回执在evidence/RELEASE-20261116。

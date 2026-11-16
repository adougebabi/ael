# Local 历史导入与单权威切换

transfer 命令处理明确选择的旧 Local checkout 和现有运行实例。每个来源使用操作者审阅的稳定 source-id，同一项目的两个 clone 使用各自的来源 ID。Server 按原项目、来源、原任务 ID 派生中央 ID，同名任务和冲突修订分别保留，原始 ID、版本与完整 Task 快照可追溯。

## 盘点、计划与私有导出

先协调旧 Agent/Bot 的当前责任，使用已有 Local 恢复入口解决 running/interrupted 记录、工作区锁与待落盘投影。未知执行状态需要明确核实。scan 以 SQLite 只读事务盘点现有实例，报告仍未解决的责任、映射、历史记录、可读取文件及缺失状态。Git-only clone 可先通过原 Local 读取入口恢复其已有 records，再盘点实际运行实例。

    ael transfer scan --project LOCAL --source-id mac-old --json > /private/path/scan.json
    ael transfer plan --plan /private/path/scan.json --out /private/path/plan.json
    ael transfer export --plan /private/path/plan.json --approved-digest DIGEST --ack-drained --out /private/path/new-export

plan 和 export 重新核对同一来源与摘要。--ack-drained 表示外部执行者已协调，数据库中的有效租约、存活绑定进程及工作区锁仍会阻止导出。已过期且无存活绑定进程的责任作为原历史保留。导出在 BEGIN IMMEDIATE 中检查空闲并安装旧版本兼容触发器，冻结 tasks、events、submissions、workspace_locks、outbox 与 metadata 的写入。当前 CLI 也在正常 Local 打开入口提示冻结状态。

冻结前校验统一来源 ID、现有单文件 10 MiB 和迁移清单请求 8 MiB 上限（普通请求仍为 2 MiB）。超限返回具体错误并保持 Local 可写，先核对容量与迁移范围。旧媒体在通过现有解码/像素约束后提供图片预览，其余选定历史制品按原字节保存为可下载对象。

导出目录位于 checkout 和运行库之外，保持私有权限。local-recovery.db 是一致 SQLite 恢复备份，包含原始运行数据和本地授权信息，应按私有数据保管。plan.json 保存去除执行授权的中央导入清单，objects 按 SHA256 保存选定引用的字节。Evidence 输入/输出、历史检查、人类意见、Attempt 输出、交付回执及已供应规范快照均参与对账。相对文件沿任务已绑定的 CandidateWorkspace 读取，runtime 引用沿原运行目录读取。原 source 路径保留在本地计划中。

中断后用原 plan、digest 和目录续跑。已保存对象先校验并复用，尚未保存对象核对原源文件字节。缺失和不可读引用保留明确状态。历史哈希与当前可读取文件的哈希分别保存，差异可在看板中检查。源 runtime、工作区和 Git 文件继续由外部宿主保管。

## 导入、核验与激活

使用具有项目 executor 权限的连接 profile。导入者拥有该批次的核验、激活与回滚权限，管理员 session 可处理管理范围内的批次，reader/reviewer 保持原角色边界。账号角色和权限继续由既有 Server 身份及成员规则判定。

    ael transfer import --project LOCAL --from EXPORT --profile central --remote-project PROJECT
    ael transfer verify --project LOCAL --from EXPORT --profile central --remote-project PROJECT
    ael transfer commit --project LOCAL --from EXPORT --profile central --remote-project PROJECT

import 使用确定的操作键分阶段创建历史和上传文件，也接受显式 operation-id/idempotency-key。业务数据与回执同事务保存。附件复用原配额、分块、SHA/类型检查、授权下载和私有重试文件，ImportID 仅接收该批次声明的字节。普通执行上传继续使用原租约门禁。

verify 将原引用映射到实际 finalized 附件，重新检查原件字节、大小、SHA 和 MIME。已知空文件由长度零和空内容 SHA 完整表示。缺失引用继续保留来源状态。待提交导入的文件受到清理保护。commit 再核验原件并激活中央任务，同时保存回滚事实基线。

历史结果保存于原始快照与原 RecordHistory。当前 Task 的 candidate、evidence、attempts、approvals 和 Plan.Approved 从新的责任边界建立。已完成/取消状态显示历史标记，当前验收仍要求现行候选与检查。未完任务暂停并要求恢复确认，计划修订、已有非验收检查点、失败记录和显式预算保留，verify/delivery 从新 execute 或 integrate 重新建立证据。父子关系先通过原创建校验，再恢复历史终态，optional 成员与里程碑保留。

执行过程页的导入历史显示来源、原 ID、版本、旧执行与人工记录、规范引用及附件。完整旧 Task 可展开审阅。原 SQLite 事件日志及其他运行元数据保留于本地恢复备份，中央历史由迁移清单中的 Task/RecordHistory 承载。

## 原地切换

    ael transfer cutover --project LOCAL --from EXPORT --profile central --remote-project PROJECT --ack-drained

切换取得同一个 projectmode 独占锁，核对原来源冻结及实时中央 active 回执，再复用普通 profile、协议、项目访问和原子 binding 安装。Remote binding 使当前入口读取唯一 Server 权威，旧数据库触发器继续约束已打开的旧 Local 写者。旧 Local records/outcomes 和项目 payload 保留，可按现有 update --migrate-entry 流程选择机器级轻入口。

两端实际编码、编译、截图、工作区和宿主生命周期仍由外部 Agent 处理。冻结记录描述协作数据写权限，宿主执行状态依赖外部协调。

## 冲突保护与回滚

    ael transfer rollback-freeze --project LOCAL --from EXPORT --profile central --remote-project PROJECT --ack-drained
    ael transfer rollback --project LOCAL --from EXPORT --profile central --remote-project PROJECT --ack-drained

rollback-freeze 在事务内比较激活时的任务、事件、附件、上下文、发现、责任及关联元数据。新增中央事实返回 IMPORT_NEW_FACTS，继续使用中心并先核对新增结果。基线一致时将该来源范围置 rollback_frozen，持久触发器拒绝任务与证据相关写入，原历史继续可读。

未激活批次使用创建时的范围基线核对，允许中止 importing/verified。中央尚未创建批次时，服务端持久记录该摘要的终止标记，迟到 stage 请求受到拒绝。相关 metadata 同时覆盖实际哈希编码的计划锁，冻结和完成回执均核验事实。

rollback 取得项目模式锁并实时读取冻结回执，再在原 Local 库提交释放 journal 和冻结解除。原数据原地保留，binding 经精确 hash 核对后移除，最后记录中央 rolled_back 回执。中断可重复同一命令，完成前中心保持冻结。新增 Local 事实继续保留。原导入副本及证据保留于中心历史。

回滚完成后，CLI 按原操作键取消本批次未完成的上传并释放暂存容量。已完成对象继续作为原历史保存。

同一来源出现不同清单时返回 IMPORT_SOURCE_CONFLICT，操作者先核对差异与映射。重新建立来源或改动权威范围需要新的明确计划。实际业务切换由对应授权处理。

隔离演练入口为 scripts/authority_transfer_acceptance.py，使用显式 --binary、--source-bundle、--out 和 --browser。旧历史为声明的合成数据，CLI、SQLite、HTTP、真实进程中断和浏览器操作均在本机隔离目录中执行。

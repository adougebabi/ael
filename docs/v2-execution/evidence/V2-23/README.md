# V2-23 实际 Oracle 单客户端证据

用户授权目标为 Oracle 129.154.48.38，SSH 使用本机指定密钥与 sudo。用户进一步明确采用本机单 Agent，并选择既有 Lucky。正式入口为 https://ael.adoumi.site，后端为 Oracle 回环 `127.0.0.1:17332`。实际部署目录 `/opt/ael-v2`，Server 版本 0.1.22 / f838a94-e5074a4d4148。

`summary.json` 来自 final-audit.py 对实时任务、实际图片、规范、旧客户端、维护和恢复结果的逐项断言。三个 oracle-*-final.json 保存两个完成叶子与完成父级，父级候选为 b642770dad38717481d7f60e81711e34189bcb4aecaf34ae481dd5b202831a92。pilot-source.tar、pilot-source-commits.log 与各 build/check 文件记录当前 Mac Codex 会话的实际源码编辑、Git、Python 编译和声明命令检查。

人工角色由独立 mac-reviewer 账号经外部 Playwright 操作实际公网 Dashboard。human-reviews.jsonl 保留原候选裁切失败、新候选复验通过和父级通过，PNG 为实际渲染与看板截图。规范和图片通过正式 CLI 下载，SHA 与原件及恢复实例一致。已完成任务的上下文保留终态 stale 与 usable_for_current_work=false，规范快照仍可按授权读取。

outage-summary.json 对应真实停止本轮 Oracle AEL 服务、CLI 非权威缓存与领取拒绝、同数据重启后修订/候选一致。backup.json 和 verify-backup.json 为正式维护栅栏下的完整备份清单。restore-exercise.log、restore-summary.json 对应 Oracle 独立空卷恢复、恢复进程健康、完成任务/图片哈希一致、管理员重新登录和旧令牌 401。恢复检查容器已结束，主服务为 active。

当前部署状态记录在 final-deployed-state.log。initial-remote-check.json 和 deployed-state.log 记录用户选择 Lucky 前的 Caddy/SSH 私人入口阶段，随后用户要求沿用 Lucky，当前 Compose 配置为 compose.lucky.yaml。原 Lucky 与 derper 在最终核对时均为 Up 8 days。

## 失败与恢复记录

额外 codex exec 沿用宿主只读配置，创建 fixture 受到拒绝，其最终原文保存在 readonly-agent-final.json，完整事件与 stderr 保留在私有现场 `/private/tmp/ael-v223`。当前已获授权的 Mac Codex 会话完成了后续实际编码，宿主模型、登录及只读策略保持原配置。

初始试点计划缺少 non_goals，被 PLAN_UNVERIFIABLE_LEAF 拒绝。补全原计划后按原 ID 应用。外部通信报告首次 source 声明不符，被 ASSESSMENT_EVIDENCE_REQUIRED 拒绝；修正为正式协议 external-command:mac-pilot 后沿原 Attempt 提交。旧候选人工 fail 保留在完成叶子的 evidence_history。一条收口后的额外 next 出现传输错误，随后实时读取和 next 确认同一完成结果，另有实际服务中断与恢复测试。

## 现场与凭据

原始私有现场为 `/private/tmp/ael-v223`，隔离工程为其 agent-pilot/project。脚本保留了当时的明确目标、项目 ID 和现场路径，重现应先审阅这些具体值并选择新的隔离数据范围。

本机 CLI 位于 `/Users/vinson/.local/share/ael-oracle-v2/bin/ael`，持久配置位于 `/Users/vinson/.config/ael-oracle-v2`，正式 profile 为 oracle-public。管理员和试点身份的密码、令牌、领取租约与恢复校验输入保存在私有目录。仓库证据仅包含脱敏响应、脚本、清单、fixture 源码和图片。

本项采用用户明确修订后的单客户端实测范围。工程数据来自隔离合成项目，原 V2-22 同机双客户端与八叶回归证据继续独立保留。

仓库文本日志去除了行尾空白，原始 stdout/stderr 保存在上述私有现场。

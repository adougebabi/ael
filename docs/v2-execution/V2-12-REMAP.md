# AD-AGENT-DRIVEN-01：R12.1 小范围实施映射

基线：7f88a84；原工作区干净。完整修订包：../v2-agent-driven-revision/。
旧版本：history/pre-AD-AGENT-DRIVEN-01/；旧失败：receipts/V2-12-BLOCKED.md 及 evidence/V2-12-*，原检查点均保留。

| 代码/测试/入口 | 处理 | 新验收 |
|---|---|---|
| internal/server/remote_runner.go、remote_runner_submit.go | 保留身份、claim、fence、版本、候选、状态机、幂等 | A12-02/03/04/06/08 |
| portable_candidate.go、artifact_store.go 的隔离总门禁 | 移除通信与宿主隔离耦合；保留其后的全部业务检查 | A12-01/03/04/05 |
| cmd/ael/remote_runner.go run/resume-result/recover-claim | 退出活动路径，明确拒绝；不调用 loop/copy/delivery/process | A12-09 |
| 正式 runner register/claim/heartbeat/progress/observe/submit/next | 新薄通信分发，复用原 API；不启动工程执行器 | A12-01/02/07/09 |
| scripts/remote_runner_acceptance.py | 旧托管验收归档，不作为当前验收入口；独立外部驱动替代 | A12-01—09 |
| 两个 Server 正向 Runner 测试 | 保留，恢复验证真实权限与证据 | A12-03/04/11 |
| Server 隔离 503 断言 | 改为非法 claim 仍被业务验证拒绝；不是测试开关 | A12-04 |
| CLI 隔离断言 | 改为旧托管入口永久不支持 | A12-09 |
| workspace/process/delivery 历史内部单测 | 留历史实现回归，不接到新路径，不代表宿主强隔离 | A12-11 |
| assets/skills/ael-work、help、README | 远程主动通信与外部执行，准确停止/上报语义 | A12-07/09 |
| V2-00—11、Local Managed/Attached | DONE/PASS 不改，当前项内定向回归 | A12-08/11 |
| V2-13—23 | 只替换说明，原文归档；不实施 | R12.5 暂停 |

宿主同 UID 凭据隔离、APFS、脱组后代回收、任意间接发布拦截、自动源码回写停止作为当前要求。历史失败并未修复或追认为 PASS。当前模式的权限、候选、状态和证据缺陷仍是硬失败。

NB-010 修复同步：TestRunnerWorkspaceFingerprintIncludesStagedMode 原来断言便携候选 ID 不变（已知缺陷）而独立指纹改变；现在要求两者均改变，复制等价断言保留。旧测试完整原文在基线 7f88a84；首次最终回归失败日志保留。这不是移除候选保护，而是加强共享候选断言。

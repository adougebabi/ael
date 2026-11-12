# V2-13 证据索引

formal.log 与 summary.json 对应正式 dist CLI/Server。外部双客户端测试驱动位于 scripts/remote_collaboration_acceptance.py。最终 fixture 为 /private/tmp/ael-v213-delivery，客户端分别使用独立配置和工作区。摘要只包含测试结论，私有令牌、待提交请求和数据库未复制到 Git。

- branch-a.png、branch-b.png 来自外部 Playwright 真实浏览器截图。跨任务截图和跨候选报告拒绝已验证。
- recovery-unit.log 给出逐项文件完整性、文件锁释放、结构体候选磁盘恢复和真实 TCP 租约过期测试。期限在服务端测试夹具中注入为过去，产品没有测试绕过开关。
- go-full.log、race.log、vet.log 为最终规范化摘要代码上的回归。package.log、binary-sha256.json 固定正式构建及字节校验值。
- v12-regression.log、artifact-regression.log、context-regression.log、local-regression.log 保留相关正式回归结果。
- initial-fixture-red.log 保存最初取消后未明确确认停工导致 RUNNER_WIP 的失败。后续固定夹具明确确认停工，产品继续保留未知责任保护。
- security-client.json 中的私钥头告警来自原脱敏检测字面量，两个密码告警来自假令牌测试。security-server.json 的 SQL 告警拼接的是固定列名常量，输入使用参数绑定。生成前端的 innerHTML 告警对应 MarkdownBody 的 MarkdownIt html:false 渲染，前端源与静态资源本项均无修改。这些为经原文核对的模式误报。
- change-analysis.json 为辅助变更扫描，其行数统计限制沿用既有工具，以 Git diff 为准。

本项日志对应隔离 fixture 与生产代码入口。真实外部三机和独立模型运行环境留在相应后续验收。

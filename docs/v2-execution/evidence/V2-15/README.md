# V2-15 证据索引

正式 fixture 为 /private/tmp/ael-v215-final，使用打包 CLI/Server、两个独立客户端配置和真实外部 Chromium。actual.png 来自候选 screen.html 的真实渲染，trace 记录实际点击前后状态，finding-detail.png 来自发现详情页面。

报告中 model/human 为确定性的来源分类和权限测试。model 未调用真实模型，human 由合成管理员登录会话提交，结论保持非门禁。未声称真实人类完成验收。实际观察、PNG、点击和正式 HTTP/CLI 传输真实执行。

- formal.log/summary.json 验证六个发现、重复出现、原键重放、不同定位、UI/UX 缺项、新候选、跨候选证据拒绝、批次原子回滚和角色权限。任务状态、revision 和 evidence 前后保持原值。
- unit.log 验证精确身份、种类证据、路径根限制、context 诊断附件与非门禁语义。
- go-full.log/race.log/vet.log 为最终代码全量及相关并发验证。
- context/dashboard/artifact-regression.log 保留旧报告 hash、工作台、固定原件与清理回归。
- 初始 context 上传被原 task.Candidate 前置拒绝的输出及筛选标签夹具失败保留。
- security.json 的固定列名 SQL 拼接与原 MarkdownIt html:false 对应生成 JS 告警沿用既有模式误报，输入仍参数绑定，正文关闭原始 HTML。

私有令牌、操作记录、候选源文件和数据库未复制到 Git。二进制字节固定在 binary-sha256.json。

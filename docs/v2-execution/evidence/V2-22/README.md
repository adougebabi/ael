# V2-22 验收证据

正式冻结版本为 0.1.22 / f838a94-e5074a4d4148，完整临时现场在 `/private/tmp/ael-v222-final-1`。发行清单记录 dirty=true、local_unpublished，包含四平台客户端、两平台 Server、Skill 和两平台本地镜像。验收结论以本项正式回执和实际结束日志为准。

`formal-full-chain-summary.json` 对应正式打包程序的八叶工程、真实命令与编译、双身份同任务交接、浏览器图片和独立 reviewer 账户、失败修复、父级九项重新检查、新反馈修复与复验。人工角色由外部 Playwright 驱动。源码修改、Git、命令和浏览器均由外部宿主执行。客户端 PATH 只含宿主选定的 Git/Python，AEL 负责协议与结果门禁。

`formal-real-agent-proof.json` 和 `formal-real-agent-final.json` 对应现有已登录 Codex 主动调用正式 AEL CLI 的只读 smoke，沿用原模型、认证和权限。原始模型事件和 stderr 保存在上述私有现场。八叶修改由确定性外部驱动完成，真实 Agent smoke 单独验证主动 CLI 调用。

`formal-*.png` 为同一 fixture 的实际 Chromium 截图和看板审阅。`development-*-summary.json` 为开发阶段结果，正式结果单独保留。旧 V2-12 托管与隔离实验继续保存在原证据目录，其归属沿用 `docs/v2-agent-driven-revision/03-REQUIREMENTS-TEST-REMAP.md`。

## 自动扫描与人工核查

原始扫描报告保留在 security-server.json、security-client.json、security-core.json。Core 扫描没有命中项。Server 和 CLI 的规则扫描报告有以下既有命中，已核对源码。

- artifact_http.go 的 SQL 拼接部分为编译期固定 `artifactColumns`，任务 ID 使用 `?` 参数绑定。该命中没有拼接用户 SQL。
- embed 的 Vue 产物含框架 innerHTML 实现。实际 MarkdownBody 使用 markdown-it、html=false、禁用图片、校验危险链接与 data/协议相对链接。当前修改没有新增 HTML 渲染入口，原真实浏览器读取/人工/发现回归继续核查原界面。
- remote_runner_submit.go 的 PRIVATE KEY 文本是拒绝敏感内容的匹配字面量。两个旧 Runner 测试的短 token 是临时夹具值，用于验证已知凭据泄露保护。

扫描器按模式命中，原始 passed=false 结果保留。变更分析器能枚举新增文件，但其行数统计返回 0，实际变更量另以 Git diff 和新增文件核对。文档已同步 remote-agent、remote-tasks、architecture、CLI help 与源码版本。

## 实测环境

本机 macOS arm64 双 checkout、独立 CLI 配置、真实 HTTP，以及本机 linux/arm64 Docker。linux/amd64 镜像完成构建与导出，其他架构二进制完成构建。实际 Oracle、云电脑 Agent 和业务项目切换继续由 V2-23 按明确授权验证。

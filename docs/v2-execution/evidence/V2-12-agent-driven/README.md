# AD-AGENT-DRIVEN-01 正式证据

基线 7f88a84，工作区初始干净。正式二进制由 scripts/package.sh 构建；binary-sha256.json 固定校验值。无 overlay、忽略安全开关或恒真放行。

- formal.log / external-driver-summary.json：正式 dist CLI，独立外部驱动执行实际文件修改与 Python 检查，失败→修复、两叶顺序、父级未达标、权限拒绝、提前降权、固定证据、报告来源/候选/版本/覆盖拒绝、实际代理丢响应→receipt→原键重试。AEL 不启动驱动或工程执行器。fixture 原始目录 /private/tmp/ael-v212-delivery-check（私有请求/凭据不提交）。
- actual-agent-smoke.json：本次真实 Codex desktop 会话主动调用正式构建 CLI，读取 fixture 规范，完成 define/execute，Agent 自己修改 app.txt 并通过宿主内容断言；最终正式 dist CLI/Server 完成 verify，接收报告返回 completed。最小声明命令 true 与额外内容断言都由外部宿主执行。不是完整自主模型故障恢复，也不是另行启动 codex exec 或三机实测；完整失败修复 Loop 由明确标注的确定性驱动覆盖。临时 Server 已停止。
- context.log：最终构建上的 V2-11 双 checkout、规范快照、候选变化、暂停/责任版本与浏览器验收。context-red*.log 保存错误夹具断言：浏览器主动暂停、恢复改变责任版本，必须恢复并发布当前 context 后独立测试候选不匹配。产品暂停/候选门禁未放宽。
- artifacts.log / remote-tasks.log / local-workflow.log：正式分发的 V2-09/10 与稳定 Local/Attached/Managed 定向回归；截图为外部 Playwright 产生。候选补丁后最终全量/race 覆盖相关包，远程 driver/context 另在最终构建重跑。
- candidate-old-assertion-*.log：修复 NB-010 后旧 workspace 测试因断言“候选不变”失败。新断言同时要求候选和独立指纹变化，复制等价断言保留；go-full.log / race.log 为同步后最终结果。
- security-patterns.json：模式扫描的 1 critical/2 high 经逐条核对均是旧代码中的私钥头检测字面量和两个假令牌脱敏测试，不是真实秘密。单文件扫描器扫描数为 0，不作为证据；此目录扫描实际覆盖 39 个文件。无未处理的新高危发现；它不能代替权限/证据测试或证明宿主隔离。
- change-analysis.json：文档同步提示工具；其增删行计数为 0 的工具限制不作为代码规模证据，以 Git diff 为准。设计决策写入 docs/architecture.md。

旧远程托管脚本保存在 scripts/history，旧原生/容器/间接发布失败保持原处。它们未被修复为宿主强隔离；因明确产品边界变更退出本轮受支持远程路径。稳定 Local 功能及其历史单元回归保留。

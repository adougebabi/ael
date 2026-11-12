# V2-14 正式证据

formal.log、summary.json 和三张 remote-*.png 来自 /private/tmp/ael-v214-final。正式 Server 与两个客户端在独立临时目录运行，产品以真实 HTTP 接收声明和结果，外部 Playwright 操作打包页面。客户端没有共享 SQLite。失联显示用例由服务端测试夹具注入已过期 deadline，产品客户端继续使用正式 HTTP，未添加安全绕过开关。

- 工作台断网时保留最后成功数据并提示过期，重连重读。实际旧 open 响应在 completed 已显示后才释放，结果未倒退。
- 父子、检查证据、实际提供规范、资料库往返，含非零滚动与焦点恢复。两个项目同 ID、在途响应与只读账户变更拒绝都实际验证。
- unit.log 覆盖权限、游标、未净化 revision、一致的规范来源、历史 context 相对链接与旧私有路径脱敏。
- go-full.log、race.log、vet.log 是最终源码结果。旧认证/附件/上下文/Local/V2-13 回归各自独立保存，原始依据与附件入口仍实际可用。
- worklog-recovery-audit.json 核对完整基线历史。文档同步误写 WORKLOG 后已恢复，允许的历史前缀差异仅当前编号及 V2-14 表行，进展记录均追加。
- 浏览器早期失败来自延迟路由释放及既有交互选择器假设，分别保留日志。原认证测试发现新增远程标题区域重复刷新控件，已移除重复控件。
- 安全模式扫描的固定列名 SQL 拼接为参数绑定，生成 JS 的 innerHTML 对应原 MarkdownIt html:false 阅读器，前端源码扫描未报告新增项。模式扫描不代替实际授权与状态测试。

二进制 SHA-256 固定在 binary-sha256.json。私有请求、令牌、测试数据库、浏览器 trace 保留在临时 fixture，未复制到仓库。三机与生产部署按后续任务实施。

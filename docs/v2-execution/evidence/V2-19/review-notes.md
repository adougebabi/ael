# V2-19 核验与修正记录

两名只读探查者定位旧 Upgrade 事务和 Remote 协议/入口边界，主线程完整阅读实际修改代码后实施。新客户端缓存复用原安装意图、备份、锁和原子替换，Local 完整包与单平台包分开。

第一轮两路独立审查提出四项具体问题，现已逐项修正，并保留 independent-audit-red.log 与对应通过回归。

1. 在线检查曾只查询已签名发行，忽略指定 prefix 的实际安装。当前 CheckUpgradeTarget 校验该安装的 marker、实际 executable version/build，准确区分当前版、新版和降级。
2. 客户端包的声明协议曾未与 executable probe 对应。当前同时核对 API、Skill、CLI output、version、build，以及签名外层的 commit。0.1.18 缺省 API/Skill 字段按已知协议 1 兼容，明确不支持的值仍拒绝。
3. 大 AGENTS 生成的恢复日志曾超出旧 1 MiB 读取上限。当前日志上限 16 MiB、AGENTS 读取上限 12 MiB，序列化后在项目替换前检查容量。约 900 KB 原文的实际中断现场恢复通过。
4. GET 曾跳过服务协议检查。当前读写均先取兼容 metadata，身份拒绝和协议不兼容继续失败。网络不可达的历史缓存仍标 online=false、authoritative=false，跨凭据缓存隔离和撤销测试保留。

后续只读复核确认上述四项修复的代码与断言，未发现新的必修问题。该复核期间曾误调用一次只读 pwd，之后使用文件读取，未修改文件或执行测试。

正式旧入口验收捕获 --check 布尔注册误吞 context/verify 检查 ID。当前按 version/upgrade 命令上下文解析维护布尔，其他检查 ID 仍为值参数，等号形式保留。正式双端 context record-check 和浏览器回归通过，原失败保存在 check-flag-context-red.log、check-flag-unit-red.log。

兼容审计另发现单平台升级替换 Local 默认完整来源，以及额外 package.json 导致完整包误分类。当前 Bundle 继续保存完整分发来源，client_bundle 单独记录客户端缓存，旧 Local 调用者可忽略新增字段继续默认 update。完整 lock 优先识别，原失败见 local-default-source-red.log。真正的 0.1.18 Local 项目分别通过新机器 CLI 和旧固定项目入口的默认 update，保留原版本；两种包之间的重放/恢复经最后只读复核未发现新问题。

旧 HTTP 测试补齐实际服务已有的 meta 端点。新增 GET 握手会复用连接，Go Transport 可自动重试一次幂等 GET，原故障夹具因此需同时丢弃重用连接和新连接上的查询，保持原响应丢失场景确实不可读。原上传/提交次数和字节断言保持，并增加掉包消耗断言，未放宽成功标准。

静态 release/bootstrap 安全扫描通过。remote 扫描唯一 high 指向原有 profile_test.go:22 的 ael_synthetic_test_token。该常量是公开合成测试身份，实际安全边界断言要求它不泄露，并非真实凭据。保留扫描原始结果并人工判定，不为静音删除回归。模块与质量扫描通过，质量工具对 Go 的语义覆盖有限，实际代码还依赖 Go test/race/vet 与独立审查。

压缩包解包完成后继续消费有界 tar padding，验证 gzip footer 并拒绝额外载荷，正常/坏 checksum/附加压缩流测试通过。一次自动审批用量错误曾使该补丁和测试没有执行，服务恢复后原操作获准并执行，最终代码已重新打包验证。

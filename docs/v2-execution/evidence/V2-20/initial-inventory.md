# V2-20 初始现场

基线 93f6013f835ae30d37541d328da0a03fdd39695f，开工工作区干净，前项回执和检查点已核对。

中央数据由根认证/项目注册 runtime.db、projects/<project hash>/runtime/runtime.db、配套 workspace 投影和中央 attachments 组成。Local Restore 只选择性恢复部分任务表，不能代替中央全量恢复。既有 Store.Backup 使用 SQLite VACUUM INTO，outbox 含服务器绝对路径，恢复时只重定位已知服务器投影路径。

已有 authorization.lock 共享/独占跨进程栅栏，当前 meta、静态页面和 login 在它之前。维护一致点将复用此栅栏并纳入登录写入。附件已有配额、活动任务/待裁定意见保护与可恢复 prune，继续复用。

当前本机 Docker context 为 orbstack，Unix socket 为 /Users/vinson/.orbstack/run/docker.sock，Compose v5.5.0。只操作本机独立随机项目、容器和卷。官方 Caddy 当前本机 arm64 digest 经真实清单解析并拉取，版本 v2.11.9，固定引用保存在 /private/tmp/ael-v220-caddy-ref.txt。没有连接 Oracle 或业务工程。

通过 Context7 CLI 核对官方 Docker Compose 与 Caddy 文档。Compose 使用 named volumes、独立健康命令和 service_healthy，Caddy 保留 HTTP 上游 Host 并安全生成 forwarded headers，localhost 自动内部 TLS 使用持久 CA，skip_install_trust 避免自动修改宿主信任。客户端采用显式公共 CA 和标准 TLS 验证，不增加不安全开关。

按顺序实施三个内部小步。第一步为 Compose、私有健康检查与实际 TLS/任务/图片重启，第二步为管理员维护和责任撤销，第三步为全量备份/恢复、格式兼容保护、升级中断及最终回归。当前唯一任务 V2-20，V2-21—23 PENDING。

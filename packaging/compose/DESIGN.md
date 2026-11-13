# Compose 部署设计

一个 AEL Server 与内嵌 Dashboard 使用专用 named volume，container_name 保持模板单实例。Caddy 采用固定镜像引用并独立持久化数据与配置，负责标准 TLS。应用健康检查由自身二进制完成，scratch 镜像保持无 shell。

反代到 HTTP 上游保留真实 Host，AEL 只信任配置的代理 IP，继续精确 Origin、CSRF、会话/令牌和项目授权。Docker socket、业务源码与客户模型凭据均不作为服务挂载。

内部健康路径要求原始 TCP peer 和 Host 都为 loopback，同时拒绝跨站 Origin。它只报告数据库与管理员初始化状态，业务 API 的 canonical Origin 规则保留。

本地验收使用随机 Compose 项目/卷、选定空闲端口和未重叠网络。TLS 使用实际证书链，客户端只增加选定公共证书的信任，默认系统信任、主机名和证书校验继续执行。实际 Oracle 环境仍需后续授权核对。

2026-11-13 V2-20 新增模板，先验证服务入口，再完成维护与统一恢复。

运维服务使用同一固定 AEL 镜像和 network_mode none，通过文件系统权限及现有授权栅栏建立一致点。旧格式显式迁移，根 marker 保护不兼容二进制回退。恢复阶段日志支持挂载卷内的文件提升，避免替换挂载点。恢复后清除快照 session、撤销旧项目令牌，并保留账号/成员与历史回执。

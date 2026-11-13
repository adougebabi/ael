# 单实例 AEL Server 部署

此目录提供固定 AEL 镜像、官方 Caddy 反代、持久卷、私有健康检查和 HTTPS 入口。服务端容器只挂中央数据，代理只挂其配置与 TLS 数据。

配置实际 AEL_IMAGE、CADDY_IMAGE、AEL_PUBLIC_ORIGIN、AEL_SITE_HOST。生产镜像使用已核验的固定版本或 digest，当前没有默认线上发行来源。首次管理员用 compose run --rm --no-deps -T server server bootstrap --data-dir /data --username NAME --password-stdin 初始化，再启动服务。密码从标准输入传入。

默认只在 127.0.0.1 发布 HTTPS，后端不发布主机端口。代理固定 IP 与 AEL 的 trusted-proxy 对应，网络 CIDR/IP 可按真实环境调整。启动前核对 CPU、现有端口、网络、磁盘和现有代理。

localhost 使用 Caddy 内部 CA，public root.crt 可复制给客户端的显式 --ca-file。公共域名按 Caddy 标准自动 HTTPS，实际域名/入口和部署授权在真实部署时提供。模板不安装宿主根证书，也不修改 Agent 配置。

容器内部 /ael server health 使用 loopback 私有探针。外部 healthz 受拒绝，项目、任务和证据仍走实际身份/项目权限。无网络 operator 服务共享数据与备份卷，供显式 backup/restore/migrate 一次性运维。维护、格式保护、权限及完整恢复流程见 docs/server-operations.md。

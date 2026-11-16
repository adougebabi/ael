# 单机 Server 部署与维护

当前数据格式为 2。部署文件位于 packaging/compose，应用和代理各使用固定镜像引用。Server 数据、代理证书/配置和备份各有 named volume。正常服务只有一个 Server 实例，运维容器使用同一 Server 镜像、无网络配置。

## 环境与初始化

部署前在实际目标核对 CPU/OS、Docker/Compose、磁盘空间、端口占用、已有反代和网络 CIDR。镜像平台必须对应实际 CPU。固定 AEL_IMAGE、CADDY_IMAGE，设置真实 AEL_SITE_HOST 和 AEL_PUBLIC_ORIGIN。默认仅在 127.0.0.1 发布 HTTPS，已有代理可按实际入口配置监听与 trusted-proxy。模板的后端端口留在私有网络中。

复制 .env.example 并填入实际值，执行 compose config 核对最终挂载和端口。首次管理员通过标准输入提供密码。

    docker compose run --rm --no-deps -T server server bootstrap --data-dir /data --username owner --password-stdin
    docker compose up -d --wait

私有健康检查由 /ael server health 在容器 loopback 执行，检查根数据库及首个管理员。外部 healthz 请求被拒绝，业务 API 保持 canonical Host/Origin、身份、项目权限和 CSRF 检查。

Caddy 对 localhost 自动使用内部 CA，对实际公共域名使用其标准自动 HTTPS。skip_install_trust 保持显式客户端信任。私有入口可复制公共 root.crt，再以 server profile add --ca-file PUBLIC_PEM 配置客户端。CA 文件只接受公共证书块，证书链及主机名仍由标准 TLS 验证。

    docker compose cp proxy:/data/caddy/pki/authorities/local/root.crt /private/path/root-ca.crt
    ael server profile add --name NAME --url ACTUAL_HTTPS_ORIGIN --token-stdin --ca-file /private/path/root-ca.crt

## 维护与一致备份

管理员 CLI 复用既有登录/session/CSRF。密码从 stdin 读取，维护请求使用稳定 request-key，重放相同键需相同输入。deadline 为实际 RFC3339 UTC 时间。

    ael server maintenance --url ORIGIN --username ADMIN --password-stdin --action drain --deadline DEADLINE_UTC --reason REASON --request-key KEY
    ael server maintenance --url ORIGIN --username ADMIN --password-stdin --action quiesce --reason REASON --request-key NEXT_KEY

私有 CA 入口增加 --ca-file。drain 阻止新责任领取，现有执行者收到 server_maintenance 状态与 deadline，按授权保存现场或释放责任。截止后受保护写入拒绝。quiesce 等待在途授权请求结束，分项目撤销旧 API 责任，保留 interrupted 历史、unknown workspace hold 和原回执。执行者宿主状态继续为 unknown，恢复需重新读取并明确工作区处理。

quiescing 是持久中间状态。部分项目失败后，用原请求键及输入继续，已完成项目按 epoch 重放。它不能直接 resume。quiesced 后可在授权独占栅栏下备份，所有中央库和文件在同一维护点冻结。

    docker compose run --rm --no-deps -T operator server backup --data-dir /data --out /backup/EXPORT --json
    docker compose run --rm --no-deps -T operator server verify-backup --from /backup/EXPORT --json

根库与每个已有项目库使用 SQLite VACUUM INTO 一致副本，复制配套 workspace、全部 uploads/objects 和其幂等/清理状态。manifest 写版本、数据格式、维护代次和每个文件的大小/SHA-256。备份目录包含身份及授权数据，应按私有数据保管。导出目标必须位于服务数据目录之外且为新目录，完整备份失败保留原服务数据。

现有附件 stats/quota/prune 沿用原管理员接口及活动任务、待裁定意见、规范快照和关联任务保护。备份不删除附件。

## 格式升级与旧二进制回退

格式 1 的旧 Server 缺少当前维护协议，应先停止该 Server 服务，保存一致备份，再明确迁移。--ack-stopped 仅确认旧 AEL Server 已停止。

    ael server backup --data-dir OLD_DATA --out NEW_BACKUP --ack-stopped --json
    ael server migrate --data-dir OLD_DATA --ack-stopped --json

迁移先持久化恢复意图，再切换格式 marker，最后逐项目处理旧责任并记录格式 2。中断后原目录仍受恢复门禁保护，原 migrate 命令可续跑。格式 2 marker 使旧 0.1.19 程序在打开库前拒绝启动，未来格式也由当前程序拒绝。回退需要恢复匹配的完整数据备份与对应版本，不能只替换可执行文件或镜像。

新建项目的默认策略使用完整私有阶段文件与独占发布。既有空文件或改写后的策略返回 SERVER_POLICY_CHANGED，先保留失败现场，再从已验证的升级前备份恢复到空卷并核对数据。恢复完成后重新登录和签发项目令牌。

当前格式 2 服务切换已验证的新镜像前，先 drain/quiesce 和备份，再停止 Server、设置已核验的固定新镜像、按需要迁移，启动后检查 health、实际版本、任务与图片。最后 resume，客户端重新核对协议、责任和回执。

## 空服务恢复

恢复目标是新目录或新空 named volume，可以继续使用历史容器内路径 /data。导出目录与目标须实际分离。恢复先验证 manifest 和所有文件，阶段文件保存在目标私有目录，原有非空数据目标拒绝覆盖。

    docker compose run --rm --no-deps -T -v ORIGINAL_BACKUP_VOLUME:/backup:ro operator server restore --data-dir /data --from /backup/EXPORT --json

restore 的阶段记录绑定原备份 hash。进程中断后用相同备份和目标继续，已有文件必须符合预期字节、私有权限、单一硬链接和 ACL 约束。所有文件完成后才提交可服务 marker，部分恢复不能启动。

恢复完整中央数据，重定位已知服务器 outbox 路径，客户端诊断路径保持原事实。旧运行责任失效，unknown workspace 保护继续。恢复会清除旧浏览器 session 并撤销快照中的项目令牌，管理员重新登录后检查恢复代次、resume，再签发新项目令牌。密码摘要、账号、成员关系、任务/候选/规范/发现/人工结果、历史回执及附件保留。新身份仍可查原幂等回执，旧责任不能据此重新写入。

本机演练入口为 scripts/server_compose_acceptance.py 和 scripts/server_storage_acceptance.py。前者覆盖真实 TLS Compose、任务/图片重启与空卷恢复，后者使用真实旧/新程序并实际杀死迁移和恢复进程。实际平台、镜像 ID、版本和限制写入本项回执。Oracle 与实际业务部署由对应后续授权处理。

## 已有 Lucky 反向代理

已有 Lucky 时，可使用独立 AEL Server 服务，将容器 7332 发布到主机回环地址。V2-23 授权部署实际使用 `127.0.0.1:17332:7332`，Lucky 的上游为 `http://127.0.0.1:17332`，转发根路径并保留访问域名的原始 Host，HTTPS 由已有代理处理。AEL 的 `--origin` 配置为实际完整 HTTPS 地址，Lucky 传递 `X-Forwarded-Proto: https`。

`--trusted-proxy` 只包含实际反代到达 AEL 时的直接来源地址。本次 Lucky 使用 host 网络，经 Docker 回环端口到达容器时来源为该专用网桥网关 `172.31.240.1`，配置为 `/32`。其他部署应先核对实际网络和对端地址。身份、项目授权、Host/Origin、CSRF、私有健康端点和版本保护继续生效。

实际单客户端部署配置与验证结果保存于 `docs/v2-execution/evidence/V2-23/`。本轮用户选择现有 Lucky，AEL Server 使用独立数据卷和备份卷，运维沿用上文的一致备份与空卷恢复入口。

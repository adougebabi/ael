# 发行模块设计

发行 manifest 的内容完整性与发布身份分别表示。SHA-256 与大小验证只证明内容与清单一致，在线可用资产还必须经过用户配置的成熟签名验证器核验。

清单使用版本化 JSON。平台、kind、路径、重复项和协议在读取时校验。资产路径限定为同目录单个文件名，本地读取复用 store.SafePath 与 HashFile。版本选择使用 Go 官方 x/mod/semver。

网络源采用显式 URL 与来源允许列表。HTTPS 用于非 loopback，HTTP 仅用于本机测试源。重定向仍逐次校验允许来源。manifest/signature/catalog 有响应大小限制，资产流式读取并以清单大小限制。最终输出使用新文件链接，现有文件受到保护。

OpenSSH 验证绑定 allowed_signers、身份和 ael-release namespace。Cosign 验证绑定本地信任配置中的固定证书身份/issuer 或公钥。源码与下载资产不会向 AEL 提供签名私钥。本地真实 OpenSSH 签名与下载验收通过。CI keyless 配置经核对，尚未在 GitHub 实际触发。

客户端、Server 和 Skill 分开打包，包内文件另存 package.json 校验信息。兼容离线打包仍输出原四平台锁和安装接口。V2-19 的机器级升级通过单平台包读取和有界解包消费这些资源，旧四平台 Local 契约保留。

2026-11-12 V2-18 新增发行模块。外部成熟签名验证器的路径与固定身份属于显式本地信任配置。Check 返回的认证选择在内存中绑定相同策略和完整选中数据，序列化后的对象不能直接恢复成可信下载授权。下载不执行安装或用户项目命令。OpenSSH 真实签名、HTTP 下载及负例由 release_test.go 覆盖，CI 使用 Cosign keyless，实际云端 workflow 尚未 dispatch。

2026-11-13 V2-19 增加精确版本筛选和客户端包读取。client tar.gz 仅接受 ael、notice、README 和 package.json 四个普通平面文件，按内包文件记录验证全部字节。外层签名选择的版本、build、commit 与内包及可执行探针在安装层再次绑定。解包字节总量受限，落点由调用者创建为空临时目录，实际安装复用旧事务。

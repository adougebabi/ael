# V2-19 升级与轻入口验收

基线 eb63016f5daedd94b33546a7425d00fb99000208。正式版本 0.1.19，build eb63016-eeef983757c3，检查点前同一代码快照。manifest 如实记录 dirty=true、local_unpublished。真实旧版为 0.1.18 / b81aa06-45035685c455，分发保存在 /private/tmp/ael-v219-previous-eb63016。

## 正式结果

- upgrade-summary.json 与 client-upgrade.log 保存外部驱动正式 CLI 的 43 个通过事件。使用真实新旧二进制、独立 Server、两个项目范围令牌、真实 OpenSSH 签名 HTTP 发行源，覆盖精确版本、目标 prefix、check/dry-run、签名升级、入口 hash 保留、文本/JSON、真实 SIGKILL updater、停止身份恢复、权限失败、篡改和用户内容冲突。
- 真实 0.1.18 Local 项目在机器升级后分别通过新版机器 CLI 和旧固定入口的默认 update，旧固定版本继续运行。Bundle 登记和完整缓存保留，单平台缓存单独记录为 client_bundle。
- 客户端 PATH 中没有 Docker，Remote 项目没有 .ael/bin 或 Local task 数据库。活动外部宿主是明确的进程夹具，原协议/入口快照和进程在升级后保持。真实模型会话和实际多机仍按后续任务验证。
- package-result.json、manifest.json、binary-sha256.json 保存独立包、旧四平台离线实际安装、十资产和包内字节校验。source-result.json 为十一场景签名发行 CLI 回归，image-result.json 为本机 OrbStack arm64 Server 登录、401/403 和重启持久化。
- go-full.log、race.log、vet.log 均对应最终代码。remote_*_acceptance.log、standalone_server_acceptance.log、ui_usability_acceptance.log 对应同一分发。原 Context、协作、附件、工作台、发现/复验、八项人工比较、独立 Server 浏览器以及 Local 59 场景通过。

完整隔离现场为 /private/tmp/ael-v219-verified。浏览器截图留在各 fixture 的 browser 目录。此目录只复制脱敏日志、摘要、版本和 hash，没有复制令牌、租约、数据库、签名私钥或浏览器 trace。

## 失败与复核

- client-repeat-red.log 记录原始来源与持久缓存比较引起的重复安装，已修正。
- entry-recovery-red.log 记录 AGENTS 已替换、manifest 未提交的部分现场，恢复核对原意图与前后 hash。
- independent-audit-red.log 记录 executable 协议不符、大 AGENTS journal 和不兼容 GET。指定 prefix 检查另有单元及实际签名源回归。
- check-flag-context-red.log、check-flag-unit-red.log 保留检查 ID 被新布尔 flag 吞掉的失败。当前只在维护命令中使用布尔语义，旧值参数和等号赋值均通过。
- local-default-source-red.log 保留 Local 默认完整来源丢失及额外 package.json 误分类，实际新旧 Local 默认入口均已复验。
- get-handshake-fixtures-red.log 与 review-notes.md 说明补 meta 及两次幂等查询掉包的原因，原提交/上传次数和字节断言保持。

独立复核未发现最后两处兼容修复的新问题。remote 静态扫描唯一 high 指向原有公开合成测试 token，原始结果与人工说明均保留。AGENTS 上限 12 MiB，entry journal 上限 16 MiB，项目替换前检查可读容量。其他架构客户端实机与真实 GitHub/Cosign keyless workflow 未运行，产物未发布。

## 复现

    GOMODCACHE=/private/tmp/ael-v218-modules GOCACHE=/private/tmp/ael-v212-go-build GOTOOLCHAIN=local sh scripts/package.sh
    python3 scripts/client_upgrade_acceptance.py --release dist/release --previous-bundle OLD_REAL_FULL_DISTRIBUTION --out NEW_PRIVATE_DIR
    python3 scripts/remote_context_acceptance.py --bundle dist --out NEW_PRIVATE_DIR
    python3 scripts/ui_usability_acceptance.py --bundle dist --out NEW_PRIVATE_DIR

Go 全量、相关 race 的具体目标和结果保存在 go-full.log、race.log。当前没有 push/tag/真实 Release/Oracle/业务工程操作，也没有修改真实宿主 Agent 配置或模型凭据。

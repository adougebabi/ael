# V2-18 发行验收证据

基线 b81aa06496a06112bb2aa7f96adb1c936140ba78。最终发行 build 为 b81aa06-45035685c455，版本 0.1.18。本地验证来自检查点前同一代码快照，manifest 如实记录 dirty=true、local_unpublished。此目录只保存脱敏日志、公开版本/hash、夹具摘要和独立复核记录，不包含测试数据库、令牌、租约、签名私钥或浏览器 trace。

## 主要入口

- package.log、manifest.json 和 binary-sha256.json 对应四平台、独立客户端/Server/Skill、兼容 Unix 包及双架构镜像的实际内容。
- package-result.json 对应外部驱动校验所有包内文件、运行原生客户端、空 PATH 的版本/发行查询、实际四平台离线安装和篡改拒绝。
- source-result.json 对应外部 Python 驱动正式 CLI 的十一场景，包含真实 OpenSSH 签名与实际 loopback HTTP、稳定/预发布、平台、下载、覆盖保护、错误身份、坏签名、错误协议及缺失资产。清单的 released/clean 在隔离发行源中构造，签发者为本次临时 fixture 身份，official_release_access=false。
- publish-result.json 对应六场景离线 gh executable fixture，覆盖草稿、重签名后公开、预发布标记、已公开保护、错误 tag commit、上传失败保留草稿和资产篡改。真实 Release 未访问。
- image-arm64.log、image-amd64.log、image-load-*.log、loaded-image-result.json 与 image-version.json 对应独立镜像实际导出、导入、非特权运行、401/403、登录、创建项目及重建持久化。运行环境为本机 OrbStack linux/arm64。amd64 镜像已构建、导出及导入，实际 amd64 宿主运行未验证。
- native-build.log 对应无需 Docker 的当前平台构建入口。
- go-full.log、race.log、vet.log 保存最终相关测试结果。module/security/quality 静态检查与 review-notes.md 保存代码核验边界。
- remote_*_acceptance.log、standalone_server_acceptance.log 与 ui_usability_acceptance.log 保存同一最终分发二进制的旧协议和真实浏览器回归，Local 59 场景、远程人工比较八场景和发现修复四场景通过。

完整隔离现场位于 /private/tmp/ael-v218-final，旧客户端/浏览器截图仍在对应 fixture 目录。产品交付文件位于仓库 dist/release，本地镜像标签 ael-server:v218-final 和 ael-server:v218-final-amd64。它们均为未发布本地产物。

## 失败与恢复

disk-full-go-failure.log 与 disk-full-download-failure.log 保留最终复跑中宿主磁盘耗尽的实际失败。主线程只删除本次 AEL 可重建编译缓存，恢复空间后在新的独立目录重新执行全量 Go 和所有分发回归。没有放宽断言或把失败计为 PASS。初始编译缓存路径为 /private/tmp/ael-v212-go-build，模块下载使用 /private/tmp/ael-v218-modules。

## 复现

```sh
GOMODCACHE=/private/tmp/ael-v218-modules GOCACHE=/private/tmp/ael-v212-go-build GOTOOLCHAIN=local sh scripts/package.sh
python3 scripts/release_acceptance.py --bundle dist --out NEW_RESULT_JSON
python3 scripts/release_source_acceptance.py --bundle dist --out NEW_SOURCE_JSON
python3 scripts/release_publish_acceptance.py --out NEW_PUBLISH_JSON
python3 scripts/server_image.py --manifest dist/release/manifest.json --platform linux-arm64 --tag ael-server:local
python3 scripts/server_image_acceptance.py --image ael-server:local --out NEW_IMAGE_JSON
GOMODCACHE=/private/tmp/ael-v218-modules GOCACHE=/private/tmp/ael-v212-go-build GOTOOLCHAIN=local go test ./... -count=1
```

当前没有产品默认发行 URL，Cosign keyless workflow 已核对官方语法并由只读审查复核，尚未在 GitHub 实际触发。没有 push/tag/真实 Release/Oracle/业务项目操作。

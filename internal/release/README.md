# AEL 发行读取与校验

本模块处理独立客户端、Server、Skill 和兼容离线包的发行 manifest，选择显式可信源中的真实资产，并校验下载内容。

`Manifest` 保存版本、构建、完整 commit、源码 dirty 状态、发布状态、API/Skill/CLI 协议及资产大小/SHA-256。`Verify` 只校验本地内容，返回独立的 publisher_identity 状态。`Source.Check` 验证固定发行身份后选择平台资产，`Source.Download` 流式下载并拒绝大小/hash 不符或覆盖现有文件。

Source 为用户显式选择的本地 JSON，包含 releases_url、manifest_asset、signature_asset、allowed_origins 和 trust。当前签名策略支持 OpenSSH allowed_signers，以及 Cosign 的固定 keyless 身份/issuer 或固定公钥。验证器使用系统工具，下载逻辑不会安装验证器或管理签名私钥。

CLI 入口为 `release verify --manifest FILE`、`release check --source FILE` 和 `release download --source FILE --out NEW_FILE`。这些操作在项目模式分流前处理，连接远程的项目也可使用发行命令。

原四平台 `bootstrap.Bundle` 与安装器继续使用原接口。新模块的下载与完整性校验不改项目配置、数据库或运行任务。

依赖 Go 标准库、官方 golang.org/x/mod/semver，以及已有 core 错误与 store 安全路径/hash。主调用方为 cmd/ael 的发行命令。完整操作示例和签名配置见 docs/distribution.md，测试以 `go test ./internal/release` 运行。

V2-19 增加 CheckVersion 的精确版本选择，以及 ReadClientPackage/ExtractClientPackage 的单平台四文件格式校验。解包拒绝链接、目录、重复项、越界和超量字节。bootstrap 复用这些函数进入已有安装事务，实际可执行文件的版本/build/协议还需经版本探针核对。

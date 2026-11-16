# Server、客户端与协议发行

## 本地构建

需要本机 Go（版本取 go.mod）、Node/npm、Python 3.11+。构建脚本从仓库根目录执行。下载和使用客户端只需要对应平台可执行文件。在线签名验证按所选策略调用已安装的 OpenSSH `ssh-keygen` 或 Cosign 3.1.4。

```sh
sh scripts/release-build.sh native dist/native-release
sh scripts/package.sh
```

第一条构建当前平台客户端、同架构 Linux Server 与 Skill 包。第二条保留原四平台离线工具链、锁、Unix 包和安装接口，同时输出 `dist/release/manifest.json` 及独立包。`RELEASE_PUBLICATION` 默认为 `local_unpublished`。构建会记录完整源 commit、dirty、build、平台、协议、大小和 SHA-256。本地带改动产物如实显示 dirty。`released` 要求干净 checkout。

独立客户端包包含 `ael`、notice、说明和文件清单。Server 包包含同一 AEL 程序和嵌入的 Dashboard，启动使用 `server` 子命令。Skill 包保存分发 Skill 及结果/评估/计划协议示例。兼容离线包仍使用原 `upgrade --from` 和项目 `update`，单平台包以 `upgrade --from CLIENT_ARCHIVE` 安装到机器级缓存，Remote 项目复用该 CLI，具体版本与恢复语义见 updating.md。

```sh
dist/ael-darwin-arm64 release verify --manifest dist/release/manifest.json --json
dist/ael-darwin-arm64 release check --json
```

按实际宿主选择二进制。第一条校验目录内所有声明资产。它分别返回 `content_integrity=verified` 和 `publisher_identity=unverified`。第二条在没有显式来源时返回 `no_configured_source`、available=false。清单校验失败、文件缺失、symlink、越界名、重复资产或篡改会失败。

## 独立 Server 镜像

先完成发行包构建，再按实际架构执行。

```sh
python3 scripts/server_image.py --manifest dist/release/manifest.json --platform linux-arm64 --tag ael-server:local
```

脚本核对 Server 包哈希，使用 `packaging/server.Dockerfile` 从 scratch 构建本地镜像并 `docker image save` 导出压缩包，然后把镜像包加入同一 manifest。镜像没有 shell、Git、Node 或构建工具，默认 UID/GID 为 65532，专用空 `/data` 为 0700。Dashboard 编译资产嵌入可执行文件。只需 Server 维护端提供 Docker。镜像构建与导出会写本地镜像存储，不执行 registry push。

```sh
docker volume create ael-server-data
# 输入首个管理员密码后结束 stdin。密码不作为命令参数传入。
docker run --rm -i -v ael-server-data:/data ael-server:local server bootstrap --data-dir /data --username owner --password-stdin
docker run -d --name ael-server -p 127.0.0.1:7332:7332 -v ael-server-data:/data -e AEL_SERVER_ORIGIN=http://127.0.0.1:7332 ael-server:local
```

这个示例是本机访问入口。非 loopback 监听必须显式指定实际 Origin。正式 TLS 使用已有受信代理及精确 `--trusted-proxy`，配置见 standalone-server.md。数据卷独立于镜像，切换镜像不会删除数据。备份、维护升级和 Compose 的完整流程由后续维护任务交付。

本机镜像验收使用独立随机命名容器/卷，启动、登录、创建项目、停止并重建容器，再验证相同项目，随后清理自身容器/卷。

```sh
python3 scripts/server_image_acceptance.py --image ael-server:local --out /tmp/NEW-image-result.json
```

## 可信在线来源

使用者显式准备 Source JSON。`releases_url` 必须是实际可访问、返回 GitHub Release 风格数组的发行目录 URL。资产取目录中真实 `browser_download_url`，不会按版本拼 URL。`manifest_asset` 和 `signature_asset` 是目录内真实资产名。跨来源下载或重定向需要在 `allowed_origins` 写实际 HTTPS Origin。HTTP 只允许 loopback fixture。目录、清单与签名响应有大小限制，下载按签名清单大小受限，资产上限 16 GiB。

Source JSON 的字段示意如下，尖括号内容须替换成用户已核实的真实配置。

```json
{
  "releases_url": "<实际发行目录 HTTPS URL>",
  "manifest_asset": "manifest.json",
  "signature_asset": "manifest.sigstore.json",
  "allowed_origins": ["<实际资产 HTTPS Origin>"],
  "trust": {
    "type": "cosign-keyless",
    "identity": "<受信固定工作流证书身份>",
    "issuer": "https://token.actions.githubusercontent.com"
  }
}
```

`cosign-keyless` 绑定确切 certificate identity 和 issuer。GitHub 工作流通常使用 `https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/BRANCH` 形式，必须由发布者真实仓库和受信 ref 确定。`cosign-key` 使用 `public_key` 指向已核实的本地公钥。`openssh` 使用 `identity` 和 `allowed_signers` 指向本地 allowed_signers 文件，并固定 `ael-release` namespace。相对公钥路径以 Source 文件目录解析。可用 `trust.verifier` 显式选择成熟验证器路径。Source 文件由使用者控制，其策略和验证器路径决定信任边界。

```sh
ael release check --source /absolute/path/source.json --kind client --platform darwin-arm64 --channel stable
ael release download --source /absolute/path/source.json --kind client --platform darwin-arm64 --out /absolute/path/NEW-client.tar.gz --json
```

`stable` 排除 draft 及预发布。`prerelease` 允许预发布并按 SemVer 选最新版本。空目录返回 `no_available_version`。选中版本缺清单、签名、目标平台或资产会给出明确失败。签名必须先通过，再解析兼容清单及选平台。清单要求 clean、released，且版本与 tag 一致。下载到临时文件，完成大小和哈希检查后通过新文件链接发布，已存在输出受到保护。CLI 不解包或执行下载内容。

## CI 与公开发布

`.github/workflows/release.yml` 在推送 main 时自动测试、构建并上传 workflow artifact。推送 `v*` 标签时，标签运行使用仓库工作流令牌调度 main 上的发行运行，选择 `released` 并传入该标签。发行运行从标签 checkout 源码，验证标签版本、精确提交及 main 祖先关系，再构建、签名并公开。签名工作流身份固定在 main，与客户端信任配置一致。普通构建、各标签调度和各版本发行分别使用并发组。

保留 `workflow_dispatch` 手动入口，默认 `build_artifact`，选择 `draft` 创建草稿，选择 `released` 创建公开 Release。可通过 `release_tag` 指定已有标签，发行构建与发布脚本使用实际 checkout 的同一提交。发布步骤需要对应 tag 已存在且准确指向本轮源 commit，使用 `--verify-tag`，不会创建 tag。工作流根据 SemVer 预发布后缀自动设置 prerelease，且预发布不成为 latest。已存在的同 tag 草稿可上传本次重新签名产物，再显式公开。已公开版本受到覆盖保护。草稿替换上传失败会保留草稿状态，重跑可继续。

构建 job 仅有 contents read 及签名所需 id-token write，只有选择发行操作的 publish job 有 contents write。CI 执行 Go 测试、相同本地打包脚本、双架构 Server 镜像导出，再以 Cosign keyless 签署最终 manifest 并验证当前 workflow identity。构建、草稿、公开状态写入清单。草稿的 manifest 不被在线正式下载入口接受。公开发布前须以 `released` 状态重新构建和签名，由有权人员审阅具体 tag、commit、工作流身份及产物。

本轮只验证本地等价构建和工作流配置，未 dispatch 工作流、改真实 Release、push、tag 或部署 Oracle。当前没有产品默认在线发行 URL，必须使用真实已核实的显式来源。

签名和 Actions 的配置依据来自官方文档。[Cosign](https://github.com/sigstore/cosign/blob/v3.1.4/doc/cosign_verify-blob.md) · [GitHub workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)

0.1.19 可用 `upgrade --source TRUSTED_JSON --version EXACT --prefix DIR --check` 核对实际目标安装和签名发行，正式升级对下载与内包执行完整校验。`release download` 保持只下载的独立入口。

# V2-18 初始发行入口核对

基线 b81aa06496a06112bb2aa7f96adb1c936140ba78。当前仅开始 V2-18，V2-19—23 PENDING。

现有 scripts/package.sh 构建四平台运行时、ael-toolchain.lock、完整 Unix 离线包和 SHA256SUMS。internal/bootstrap/portable.go 校验现有锁与内容，update_source.go 的安装/更新要求完整四平台分发。尚无 .github 发行工作流、独立客户端发行清单或在线发行端点。

Docker 当前上下文 orbstack，端点为本机 Unix socket。只读引擎探测返回 Docker 29.8.2、linux/aarch64，可用于后续本地 Server 镜像构建验收。尚未创建、运行或发布本项镜像。

通过沙箱外 Context7 CLI 查询 /sigstore/cosign/v3.1.4，官方文档确认以下成熟 keyless blob 流程。

```sh
cosign sign-blob artifact --bundle artifact.sigstore.json --yes
cosign verify-blob artifact --bundle artifact.sigstore.json \
  --certificate-identity EXPECTED_WORKFLOW_IDENTITY \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

工作流证书身份必须由显式可信发行来源确定，内容 SHA-256 与发布身份分别核验。当前尚未实现签名/下载入口，未产生签名，也未改真实 Release。

文档依据为 https://github.com/sigstore/cosign/blob/v3.1.4/README.md 和 https://github.com/sigstore/cosign/blob/v3.1.4/doc/cosign_verify-blob.md 。Context7 CLI 在临时 npm cache 运行，未修改宿主 Node/npm 安装。cosign 和 gh 当前未在 PATH 中发现。

下一步保留旧离线安装接口，接独立 CLI/Skill/Server 交付与 manifest，随后以发行 fixture 核验真实 asset 选择、版本、完整性与身份。

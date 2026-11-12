# V2-18 独立复核与修正

只读发行代码复核未发现具体可复现缺陷。另一次只读打包复核指出以下三项，主线程已修正。

1. 原工作流每次 create 同 tag Release，草稿不能经重新签名转公开。现在 publication helper 先分页读取真实发行目录，拒绝修改已公开版本，现有草稿先上传本轮产物再显式 edit。上传失败保留草稿状态。离线 gh 六场景验证命令及失败路径，未接触真实 Release。
2. 原公开入口遗漏预发布标记。现在从 SemVer 的 prerelease 部分生成 --prerelease=true/false，预发布同时 --latest=false。新建和草稿转公开共用该事实。
3. 原镜像临时文件位于系统临时目录，跨文件系统 os.replace 可能 EXDEV。现在临时目录就在 manifest 所在目录，导出后同文件系统原子替换。最终本机双架构镜像构建和导出通过。

最后一次只读复核检查修正后的 workflow、publish helper、离线 fixture 和镜像导出，未发现明确剩余缺陷。

主线程补齐 golang.org/x/mod v0.29.0 的原始 BSD 许可证，独立包和镜像均随附 THIRD_PARTY_NOTICES.txt。静态 module/security 检查通过。quality 检查通过并给出若干行长信息，保留可读的字段和验证条件。静态扫描不能证明没有安全缺陷。

在线信任配置由使用者显式选定。验证器为用户已有的成熟工具。OpenSSH 真实生成临时 fixture 密钥，签名与验证在测试结束清理。未读取真实签名私钥。Cosign keyless workflow 核对官方文档，未在 GitHub 实际 dispatch。

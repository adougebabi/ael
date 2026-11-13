# 本机升级与项目更新

## 0.1.8 发布与看板重启核查（2026-11-04）

页面整改结束时，本机缓存已存在 `0.1.7 / 58efd8c-93214cbf1336`，而后续页面分发仍标为 `0.1.7 / 58efd8c-e2b98db1d3dd`。实际执行 `ael upgrade --dry-run` 会返回 `DISTRIBUTION_VERSION_CONFLICT`；这是同版本不可变分发保护，不是成功升级或静默无操作。本轮修正为 **0.1.8 / 58efd8c-f08a725fe655**，同步 Go、前端包与锁文件版本并重建四平台分发。

已用本机原有 0.1.7 缓存，在隔离安装目录实际验证旧客户端预览、升级到 0.1.8、再次返回 `up_to_date`；随后本机 `~/.local/bin/ael` 实际升级成功，旧程序及安装元数据备份保留。版本号须在发布新内容时递增；`package.sh` 按源码声明版本构建，不会自动增加版本号，也不通过删除旧缓存绕过保护。

同次只读核查发现 7331 看板仍运行旧内存程序 `0.1.5 / 58efd8c-8662f5e7bf9c`。经用户明确授权，确认没有运行执行后重启该看板；现在真实服务已验证为 0.1.8，仍使用原项目和运行数据目录。没有执行业务项目的 `update`、迁移或业务配置修改。若进程启动后有其他执行者继续任务，任务分组计数会正常变化，不能把这些变化误判为重启丢失数据。

本次升级证据：`/tmp/ael-upgrade-0.1.8-proof.json`；隔离升级命令与结果：`/private/var/folders/gv/hlfcpzxj63b3813yxlrkqv6r0000gn/T/ael-upgrade-0.1.8-0o801ja5/results.json`；升级测试：`/tmp/ael-upgrade-0.1.8-tests.log`。此前页面 A01—A14 全量验收仍以 `ui-usability-acceptance.md` 中记录的 0.1.7 构建为准，不将版本号递增后的只读核查冒充重新执行全部用例。

`ael upgrade` 升级本机 AEL；`ael update` 更新当前已接入项目。0.1.5 起不再提供旧刷新命令或别名。初始化新项目仍使用 `init`。

## Local 常用操作

```sh
ael upgrade --dry-run
ael upgrade

cd /path/to/initialized-project
ael update --dry-run
ael update
# 或使用项目启动器
./ael update

# 从其他目录指定目标
ael update --project /path/to/initialized-project
```

第一次安装新命令时，旧版程序无法调用不存在的接口。使用新完整分发中的本机二进制引导一次，例如本机 macOS ARM64：

```sh
/path/to/distribution/ael-darwin-arm64 upgrade
```

从默认输出为 JSON 的旧客户端升级到默认文本的新版本时，旧客户端的版本探针无法解析新版本的默认文本；请直接使用新分发中的二进制引导 `upgrade`，再由新二进制执行所需项目的 `update`。不要把旧客户端探测失败当作升级成功。脚本需显式使用 `--json`；新程序探测旧版时兼容旧版默认 JSON。

从分发目录运行会自动识别相邻的 `ael-toolchain.lock`。也可显式用 `--from DIR`，自定义安装用 `--prefix DIR`。临时目录测试不会改默认安装或业务工程。

## Local 分发来源

本机升级会把完整四平台包保存在 `<prefix>/share/ael/distribution/<version>/`，并登记已验证的本地来源。后续 `upgrade` 读取登记来源；`update` 默认读取已保存的分发。若没有登记来源，可以显式 `--from DIR`，或先安装本机版本。`AEL_BUNDLE_DIR` 可指定完整包，`AEL_INSTALL_PREFIX` 可指定安装位置。

默认没有内置在线发行来源。显式可信来源的签名升级按下方当前客户端流程处理。源码开发者重新构建登记的 dist 目录后，可 `ael upgrade` 再 `ael update`。同版本不同 payload 会被本机不可变分发缓存拒绝，应发布新的版本号。

Local 项目有自己的锁定运行时，使用明确的项目 update。兼容 Remote 项目共用机器 CLI。`update` 只检查当前目录或显式 `--project`，不扫描其他项目、不自动初始化或切换控制器。新包与当前程序不同，项目更新会委派给新包自身的程序执行，并校验实际安装结果。

## Local 预览、保护与恢复

两条命令都返回 operation、目标、来源、原版本、目标版本、文件及备份路径。`--dry-run` 完成可机械核查的来源/受管资产/运行状态检查，不替换文件，不初始化运行库。它不是外部环境已经验证或未来写入一定成功的证明。

项目更新拒绝以下情况：

- 没有有效 `.ael/project.yaml`，或项目 ID/Schema 无效。
- 已知运行/中断 Attempt、旧工作区 fence、待修复 outbox 或其他更新锁。
- 用户修改的受管启动器、AGENTS 受管块、Skill 或其他冲突资产。
- 分发损坏、缺平台、版本/二进制不一致、降级或更新中来源变化。

不会自动停止 Agent 或看板，也不会自动恢复未知旧 writer。结束/安全交接执行后再更新，旧服务须重新启动才使用新二进制。新内核支持维护锁；维护记录在与领取相同的 SQLite 写事务中建立，任务表和工作区锁的数据库保护会阻止旧内核在更新窗口写入；新内核还检查代次，拒绝使用更新前的输入完成迟到领取。已有服务仍须重启，不能据此声称会停止或热替换外部进程。

更新完成后，新程序会校验锁文件、启动器、内嵌 Skill/引用、notice、资产清单和四个平台运行时，并返回绑定来源包的完整校验回执；旧客户端委派时会再次对实际文件核验该回执。

项目原 ID、模式、未知自定义配置字段、业务源码、任务定义/记录保持。工具更新改变受检输入，之后继续任务仍需按新内核规则核对候选与证据，不把旧检查自动当新版本通过。没有自动改业务 hooks、迁移控制器、发布或降低门禁。

项目资产备份位于报告的 `backup_dir/project/`，清单记录原文件 hash；安装前已存在的全部受管文件会保留。正式业务更新还应自行保存与项目匹配的 `storage backup`，尤其在更新后继续执行过任务的情况下。部分失败会保留备份、数据库维护记录与原有 outbox/bundle-install 恢复信息。确认旧 updater 已停止后，`ael update --from 原分发目录 --ack-stopped` 只允许用同一份包继续，存活所有者、错误来源和用户内容冲突仍会拒绝；不能删资产清单或直接改数据库强制覆盖。

本机升级备份旧可执行文件、install.sha256 与安装元数据，旧版本分发保留。中断状态记录在 `<prefix>/share/ael/upgrade-intent.json`；确认旧 updater 已停止、检查 lock owner 后，使用同一份 `--from` 包重试完成记录。未知存活进程或包不一致时拒绝恢复，不盲目抢锁。需要回退时恢复相匹配的旧可执行文件/安装标记，项目则恢复受管资产及匹配运行数据备份；命令没有隐式自动降级或通用 rollback。

如原运行服务使用 `AEL_DATA_DIR`，须沿用原值。运行库按 canonical checkout 绑定，不会从另一进程的环境自动猜测数据位置。


## 当前单平台 CLI 与 Remote 入口

0.1.19 的 `upgrade` 复用既有备份、缓存、意图和原子可执行文件替换，支持独立客户端目录或 tar.gz。CLI 包的内层文件 hash、声明 version/build/协议与实际 `version --json` 探测匹配。0.1.18 独立包的 API/Skill 缺省字段沿用已支持的协议 1。旧四平台包继续供 Local init/update 使用。

```sh
ael upgrade --from /absolute/client.tar.gz --prefix /absolute/install --version 0.1.19 --check
ael upgrade --from /absolute/client.tar.gz --prefix /absolute/install --version 0.1.19 --dry-run
ael upgrade --from /absolute/client.tar.gz --prefix /absolute/install --version 0.1.19
```

单平台缓存位于 `<prefix>/share/ael/clients/<version>`。安装登记指向已校验的持久缓存，原压缩包删除后仍可查询和重试。完整离线缓存保留在 `distribution/<version>`，登记中的旧 Bundle 来源继续供新旧 Local update 使用，单平台来源另记 client_bundle。同版本不同内容、用户改过的已安装程序及降级继续拒绝。`upgrade --check` 只读指定 prefix 的实际安装，支持 up_to_date、update_available、install_available 等明确结果。

```sh
ael upgrade --source /absolute/trusted-source.json --prefix /absolute/install --check
ael upgrade --source /absolute/trusted-source.json --prefix /absolute/install --version 0.1.19 --dry-run
ael upgrade --source /absolute/trusted-source.json --prefix /absolute/install --version 0.1.19
```

Source 配置与成熟签名验证器沿用 distribution.md。`--version` 是精确 SemVer，预发布还需 `--channel prerelease`。在线 check/dry-run 核对签名清单及目标安装版本，返回 asset_downloaded=false。正式升级继续下载、检查大小/hash、验证内包并探测 executable。签名外层与内包 version/build/commit 及协议必须匹配。显式本地来源的 publisher_identity 为 unverified，签名线上来源为 verified，内容完整性单独记录。

真实 updater 中断后，`upgrade --from 原包 --prefix 原安装 --ack-stopped` 检查其 PID/启动身份。活跃或未知 owner 继续受拒绝，dry-run 不清理旧锁。通过核对的已停止 updater 才能清理自身锁并用准确意图继续。旧可执行文件和安装元数据备份仍可运行/恢复。该流程只处理 AEL 安装事务，外部 Agent 继续使用自己的宿主生命周期。

Remote 项目可选 `project connect --install-entry`，或连接后显式 `update --project PATH`。新建轻入口包含 launcher、AGENTS 受管块、ael-work 及三个引用文档，没有项目内四平台二进制或 Local task 数据库。launcher 使用 AEL_CLI、AEL_INSTALL_PREFIX/bin/ael、默认用户安装或 PATH 中的机器 CLI，并拒绝递归调用自身。

已连接 Remote 且遗留受管离线入口时，先 `update --project PATH --dry-run --migrate-entry`，再明确执行 `--migrate-entry`。旧 launcher/受管资产先备份，`.ael/bin` 和 toolchain.lock 保留。已有 Local project.yaml 继续需要独立的数据权威切换，轻入口更新保持该边界。

Remote update 保留 AGENTS 受管块外的用户原文，受管块/Skill/launcher 被用户改过时报告 ASSET_CONFLICT。项目外备份复用原资产备份逻辑。中断文件意图保存到 `.ael/remote-entry-update.json`，逐个 hash 核对，全部目标完成后提交受管 manifest。恢复需相同 CLI 资产及同一已校验绑定，用户并发编辑保留。恢复日志限制 16 MiB，在任何项目文件替换前校验其可读性，AGENTS 输入以 12 MiB 为读取上限。

`version --check --project PATH` 报告当前 CLI、项目入口及 Server 元数据。旧 Server 没有上报版本时明确标为 unreported。Remote 读写均先核对服务协议，离线历史缓存仍标为非权威。会话可用 AEL_API_PROTOCOL/AEL_SKILL_PROTOCOL 明确声明已取得的协议，当前支持 1，旧绑定缺省按 1 读取。机器升级保留项目 Skill 和活动会话规则，显式更新项目入口为后续会话生效，不停止 Agent、修改模型配置或接管构建环境。

正式隔离验收入口为 `python3 scripts/client_upgrade_acceptance.py --release RELEASE_DIR --previous-bundle OLD_FULL_BUNDLE --out NEW_PRIVATE_DIR`。其旧版来源须为真实已构建分发。实际外部宿主由测试夹具驱动，真实 updater kill 与权限错误在本机发生。完整多机 Agent 验收仍按后续授权任务执行。

# AEL V2 实施账本

> 本账本初始状态不代表任何实施已开始。只有实际执行者才能根据代码和验收结果更新状态。
> 状态仅允许 PENDING、ACTIVE、VERIFYING、DONE、BLOCKED。

当前任务: V2-12

| ID | 任务 | 状态 | 回执 |
|---|---|---|---|
| V2-00 | 基线核对与串行实施账本 | DONE | receipts/V2-00.md |
| V2-01 | CLI 默认可读，JSON 显式选择 | DONE | receipts/V2-01.md |
| V2-02 | 父任务与叶子任务：最小可用任务树 | DONE | receipts/V2-02.md |
| V2-03 | 拆分计划：复用用户分项与 Agent 辅助补全 | DONE | receipts/V2-03.md |
| V2-04 | 顺序推进与后继领取 | DONE | receipts/V2-04.md |
| V2-05 | 大任务进度与工作台接通 | DONE | receipts/V2-05.md |
| V2-06 | 停滞检测、重复失败与可恢复停止 | DONE | receipts/V2-06.md |
| V2-07 | 父级集成验收与整体验收门禁 | DONE | receipts/V2-07.md |
| V2-08 | 独立 Server 与远程身份边界 | DONE | receipts/V2-08.md |
| V2-09 | 远程任务与 CLI：唯一权威的第一条路径 | DONE | receipts/V2-09.md |
| V2-10 | 中心证据存储与授权上传下载 | DONE | receipts/V2-10.md |
| V2-11 | 跨设备候选、规范快照与执行上下文 | DONE | receipts/V2-11.md |
| V2-12 | 远程 Runner 的真实单任务闭环 | BLOCKED | receipts/V2-12-BLOCKED.md |
| V2-13 | 双执行者协作、断网与恢复 | PENDING | — |
| V2-14 | 统一远程 Dashboard 与节点可观测性 | PENDING | — |
| V2-15 | 结构化验收发现与问题归并 | PENDING | — |
| V2-16 | 截图比较、人工结论与版本保护 | PENDING | — |
| V2-17 | 发现 → 修复任务 → 复验闭环 | PENDING | — |
| V2-18 | 发行包与可核验的版本清单 | PENDING | — |
| V2-19 | CLI 与项目资产的轻量升级 | PENDING | — |
| V2-20 | Server 部署包、备份与安全升级 | PENDING | — |
| V2-21 | 旧本地状态导入与单权威切换 | PENDING | — |
| V2-22 | 打包后的全链路验收与交付核对 | PENDING | — |
| V2-23 | 授权后的 Oracle 部署与实际双机验收 | PENDING | — |

## 当前现场

- 本地工作目录：/Users/vinson/Documents/project/个人/ael
- 当前分支 / 基线提交：main / c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4；开工工作区干净。
- 当前任务未完成的验收项：满足原契约的本机执行树隔离、Runner 崩溃/失权后的旧写者控制和同用户凭据隔离；解除安全门禁后须重跑真实单任务、父叶、权限、Codex 条件及完整回归。
- 最近实际进展：此前的隔离 fixture 和单步真实 Codex smoke 及后续发现的脱组、崩溃后旧写者和 lease 泄漏见 `receipts/V2-12-BLOCKED.md`。继续工作后完成控制区与执行器 I/O 布局、主进程身份及“后代未知”记录，修复新旧 claim/提交恢复；复核发现的旧 attempt 重建、同组后台子进程漏检、日志脱敏/诊断回归均有保留的失败用例及通过记录。最新 CLI 与旧执行器完整竞态回归通过。进程身份和目录分离不证明宿主隔离。
- 阻塞：旧执行树在失权/控制者崩溃后对授权工作区的写入及执行者对同用户控制凭据的访问尚无符合原设定且经完整验证的隔离方案；`runner run` 和 Server 写入仍 fail-closed，正向验收测试保留。容器与本机 APFS/sandbox 探针均只证明有限场景，不能作为完整方案。全量 Go 回归的两个远程正向测试仍因该门禁失败，其余包通过；时间顺序及证据见阻塞回执。
- 最近限定复核：APFS 强制卸载返回后，两个脱组写者的旧文件/目录句柄操作失败；但组合崩溃实验中，重新挂载的时间线仍有两条写入起始时间戳晚于监督器收到 EOF，卸载返回后没有记录新的起始时间戳。只读重挂载命令失败，`UF_IMMUTABLE` 未阻止已打开文件的若干写调用。另一个合成 fixture 中，Codex 的直接 `:workspace` 命令沙箱可读取 `0600` 假租约；该探针不是实际 `codex exec` 全链路。探针的范围和清理限制见 `evidence/V2-12-native/README.md`。
- 下一具体动作：找到并验证符合原契约且不强制引入新服务的可撤销执行边界，覆盖失权、崩溃、脱组、工作区交付、凭据、普通检查和已登录 Codex；只有完整证明后才解除门禁并重跑本项验收。当前任务保持 BLOCKED，不得启动 V2-13。任务外非阻断问题记于 `NON-BLOCKING-ISSUES.md`。

## 修订记录

初始任务包创建；尚未执行仓库修改或产品测试。

- V2-00 完成：基线、隔离构建和账本检查结果见 receipts/V2-00.md。
- V2-01 完成：输出协议、实际验证与旧版本升级限制见 receipts/V2-01.md。
- V2-02 完成：任务树、兼容性和验收结果见 receipts/V2-02.md。
- V2-03 完成：计划预览/应用、恢复验证及宿主限制见 receipts/V2-03.md。
- V2-04 完成：顺序领取、失败修复和 checkpoint 验证见 receipts/V2-04.md。
- V2-05 完成：大任务汇总、工作台、范围变化及打包浏览器验收见 receipts/V2-05.md。
- V2-06 完成：停滞判断、限定复核、安全恢复及打包验收见 receipts/V2-06.md。
- V2-07 完成：父级候选、集成与交付门禁、三叶及八叶打包验收见 receipts/V2-07.md。
- V2-08 完成：独立 Server、账户与项目授权、浏览器登录和打包重启验收见 receipts/V2-08.md。
- V2-09 完成：远程单权威任务、计划和双客户端验收见 receipts/V2-09.md。
- V2-10 完成：中心附件、受权预览、清理恢复与双客户端验收见 receipts/V2-10.md。
- V2-11 完成：可移植候选、规范快照、检查报告与跨端验收见 receipts/V2-11.md。

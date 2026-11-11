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
- 最近实际进展：隔离 fixture 曾跑通失败→修复→通过、场景/人工、父叶顺序及回执恢复；真实 Codex 仅完成一个隔离 define smoke。随后独立复现脱组子进程、父进程崩溃后旧写者和 lease 泄漏；已修复服务端检查制品绑定、失败证据/日志计数、方法歧义和确认恢复后的占用同步，但系统级隔离未解决。fail-closed 测试和 Local 回归通过；完整 Go 检查中两项远程 Runner 正向测试因安全门禁而失败，未删除或跳过。完整证据见 `receipts/V2-12-BLOCKED.md`。
- 阻塞：当前进程组及同用户文件权限不能强制约束脱组后代或隔离 Runner 凭据；本机 `sandbox-exec` 禁止 fork 会破坏一般检查及 Codex，允许 fork 时 Runner 崩溃不会撤销工作区写权限。没有已验证且不改变原任务设定的替代隔离；远程执行入口已 fail-closed，正向验收测试保留并因 `RUNNER_ISOLATION_REQUIRED` 失败。
- 下一具体动作：在不修改真实业务、hooks 或运行库的前提下，建立并验证支持一般检查/Codex 的进程树生命周期、隔离工作区和凭据边界；通过后移除 fail-closed，重跑 `receipts/V2-12-BLOCKED.md` 中列出的验收。不得启动 V2-13。任务外非阻断问题记于 `NON-BLOCKING-ISSUES.md`。

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

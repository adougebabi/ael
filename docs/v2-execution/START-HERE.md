# START HERE · AEL V2 逐项闭环实施任务包

## 用法

把本目录放到**现有 AEL 仓库**的 `docs/v2-execution/` 或其他普通资料位置。它不是 AEL 初始化资料，不需要安装 Skill。

先读取 `00-EXECUTION-CONTRACT.md`、`01-TASK-ORDER.md`、`WORKLOG.md`，随后只读取当前编号任务。

初始只有 V2-00 可以开工。完成一项后自动进入下一项，不等用户反复说继续；遇到真实阻塞保留唯一当前任务，不能跳出去铺其他功能。

## 给本地 Codex 的启动 Prompt

```text
在当前 AEL 仓库实施 docs/v2-execution/ 任务包。

先完整读取 00-EXECUTION-CONTRACT.md、01-TASK-ORDER.md、WORKLOG.md，随后读取当前任务文件。

本次开发不得使用 AEL 调度 AEL 自己：不得在本仓库 init、安装活动工作 Skill、用 claim/submit/loop 记录本次实施。AEL 命令只能在明确隔离的测试工程运行。产品内 assets/skills 是待修改/测试的源码，不是当前会话工作入口。

全局 WIP=1。当前任务未实际通过全部必需验收、保存回执和检查点之前，不得启动下一编号，不得并行铺开后端、前端和其他模块。不要先搭所有模块骨架，也不要把联调放到最后。

只读审查或测试子 Agent 只能服务当前任务；不同任务不能分发给多个写入 Agent 并行开发。

按“实现本项→验证→修复→回执→检查点→DONE→下一项”持续执行。普通授权内不逐项问是否继续。相同失败反复出现须限定根因复核，仍无新证据时停在当前任务并记录阻塞，不无限修改测试，也不换任务制造进度。

每完成一项输出真实交付、实际验证、回执/提交位置和下一项。新会话先恢复 WORKLOG 当前未完成项，不重新规划全部任务。

现有已完成能力优先复用；本地 HEAD 和未提交改动为准，不回退到参考提交。未经授权，不 push/tag/发布，不改摇光等业务工程，不部署真实 Oracle、不迁移真实运行库。

现在从 WORKLOG 当前任务开始。
```

## 中断后的续接 Prompt

```text
继续 docs/v2-execution/。先读执行契约、WORKLOG 和当前任务回执，核对 Git 和真实产物。
只恢复唯一未完成任务，不重做已完成项，不启动后面的任务。
恢复前运行 tools/check_worklog.py。上一会话的完成声明不能代替现有代码和测试。
本项验收通过后按顺序自动继续；缺外部条件准确停下，不伪造后台执行。
```

## 账本检查

```sh
python3 docs/v2-execution/tools/check_worklog.py docs/v2-execution/WORKLOG.md
```

它仅检查顺序、WIP 和 PASS 回执的存在性，不运行 AEL、不调模型、不证明测试已经通过。回执模板在 `receipts/RECEIPT-TEMPLATE.md`。

## 交付分界

- V2-00—07：大任务本地闭环。
- V2-08—17：中心协作、证据与 UI/UX 闭环。
- V2-18—22：发行升级、部署/迁移演练与打包验收。
- V2-23：拿到明确授权后，真实 Oracle + Mac + 云电脑验收；缺条件就保持 BLOCKED。

所有任务最初均为 PENDING。本包是实施规范，不是已经实现或已经部署的 AEL V2。

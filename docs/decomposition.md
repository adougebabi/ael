# 拆分计划

由用户或 Agent 判断需求语义，AEL 负责提供已有任务和严格校验。程序不根据关键词猜测拆分、产品决定或 UI 范围。每份计划使用本项目中的开放 Epic 和唯一计划 ID；执行成功是应用规划，不代表任何叶子或父任务已验收。

准备原始输入，例如：

```json
{"request":"实现指定的离线结果","items":[{"id":"user-1","description":"保留用户提供的交付分项","existing_task_id":""}]}
```

```sh
./ael plan prepare --task EPIC --file input.json --json
# Agent 或用户据返回的 schema、父验收及已有叶子撰写 proposal.json
./ael plan preview --file proposal.json --json
# 核对 changes、字段差异、coverage 和结构化校验的局限；使用预览返回的 digest
./ael plan apply --file proposal.json --digest DIGEST --json
```

`plan prepare` 返回完整 JSON schema、父任务、用户原文和当前已关联或用户显式指定的已有叶子；它只在本地运行库登记不可变输入快照，不启动模型或修改任务。提议必须原样携带返回的 `input_hash`、原始 request 和全部 user_items；输入变更需重新 prepare。提议中 `user_items` 原样保留用户分项，每个分项的 ID 必须恰好映射到一个叶子的 `source_ids`；指定 `existing_task_id` 时必须复用该任务。没有现成分项时可使用空数组，由 Agent 或用户给出有交付物、非目标、验收、依据和能力要求的叶子。`depends_on` 使用同一提议里的叶子 key，已有项目任务 ID 可在定义的 `dependencies` 中引用；前置顺序必须在后继之前。

每条父验收必须映射到必需叶子或明确的父级集成检查。预览拒绝缺失覆盖、重复映射/职责、循环或缺失依赖、无法独立验收的叶子，以及未声明的 UI/UX 检查。`inspection_only: true`、`qa` 或 `verify` 类型以只验收意图创建，目标必须是 `report` 或 `analysis`，运行时仍拒绝改变业务文件。以上为结构与权限校验，不替代 Agent 或用户核实语义。

重规划必须列出父任务所有已关联叶子，并以 `existing_task_id` 复用，避免遗漏既有范围。复用现有叶子时填写 `existing_task_id`；省略或置零 `expected_revision` 表示只读复用、保留定义及历史。要修改开放任务时填写当前 `expected_revision` 和完整新定义；预览列出变化前后字段和范围变化所需确认。已完成或已取消任务不能被重新定义；需要新工作时建立新叶子。相同计划 ID 和内容可以重复应用，返回同一结果；相同 ID 不同内容会被拒绝。

若应用在中途失败，已完成的创建/关联不会被删除。先处理错误，再用**同一提议文件和原 digest**重试；本地运行库记录进度，重启后继续。若确定不能继续，可显式执行 `./ael plan abandon --task EPIC --plan-id ID --reason 理由 --json`；已产生的任务保留，下份计划须显式复用它们。从完整运行库备份恢复未完计划时，先按既有流程核对并解除受影响任务和父任务的恢复确认与暂停，再执行 `./ael plan recover --task EPIC --plan-id ID --reason 理由 --json`，然后用原提议和 digest 重试；也可保留部分工作并显式放弃。已应用计划及父验收映射保存在父任务可移植记录中；跨工作区恢复的历史验收不会自动成为当前通过。

可在新建的隔离目录运行确定性演示（演示脚本预先编写提议，不能代表模型规划能力）：

```sh
python3 scripts/plan_proposal_demo.py --bundle /path/to/unpacked-distribution --out /tmp/ael-plan-fixture
```

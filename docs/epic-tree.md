# 本地任务树

Epic 是类型为 `epic` 的父任务；普通任务仍是叶子，已有任务无需转换。Epic 保留范围、目标与验收要求，当前只汇总子任务，不领取普通实现步骤，也不会因为子任务完成而自动完成整体验收。

创建叶子时设置 `parent` 为当前项目中已存在的 Epic ID，会建立必需成员关系。也可创建普通叶子后显式关联：

```sh
./ael task link --parent EPIC_ID --task LEAF_ID --milestone 阶段一 --json
./ael task link --parent EPIC_ID --task OPTIONAL_ID --required=false --milestone 阶段二 --json
./ael task tree --task EPIC_ID --json
./ael task progress --task EPIC_ID
./ael task list --parent EPIC_ID --json
```

关联是项目本地的；一个叶子只能有一个显式 Epic 关系，Epic 不能作为叶子。重复关联、自关联、缺失目标以及链接正在执行的叶子会被拒绝。取消必需叶子会保留该成员及取消状态，不能满足父范围；已关联的叶子不能直接删除。关联已完成叶子保留其定义、结果和完成时间。

树结果包含父 `root`、`children`、`required`、`required_completed` 与 `summary`；每个子项携带 `required`、`milestone`、实际 `task` 状态和只读 `summary`。`task progress` 默认显示简要汇总和最近事实，`--json` 使用与树和本机看板相同的投影；单独的 `progress` 命令仍用于记录某个已领取工作单元的事实。汇总分别统计已实施、必要检查通过（已验收）、真实交付回执、未开始、部分检查通过、失败、阻塞、等待接手和取消的叶子；这些状态可以重叠，不是项目完成百分比。取消必要叶子不会减少必需分母；范围变化、取消原因以及实际进展从已有事件读取。完成状态是叶子自身的结果，并不代表父级已经集成或发布。跨工作区导入的历史完成记录保留原时间，但不作为当前工作区已复验的必需完成数。

HTTP 接口使用现有本地 session 鉴权：`GET /api/task-tree?id=EPIC_ID` 与 CLI 使用同一树投影；`POST /api/task-link` 接收 `{ "parent": "EPIC_ID", "task": "LEAF_ID", "required": true, "milestone": "阶段一" }`，省略 `required` 时为必需。任务列表支持 `GET /api/tasks?compact=1&parent=EPIC_ID`。旧版宽松 `parent` 字符串不会在打开旧库时被自动提升为新的显式关联；仍可读取原记录。显式关联旧叶子时保留原定义中的历史父引用，界面以新 Epic 关联为准。较旧的记录或完整备份若会覆盖现有 Epic 关联，将以 `TASK_MEMBERSHIP_CONFLICT` 拒绝，需先核对两份数据。

可在全新隔离目录运行以下演示。它只使用确定性的测试执行器，不运行真实 Agent：

```sh
python3 scripts/epic_tree_demo.py --bundle /path/to/unpacked-distribution --out /tmp/ael-epic-fixture
```

演示创建父任务与三个独立验收的叶子，对照 CLI 与本机 HTTP 树，并停止自己启动的 Dashboard。`--out` 必须为新目录或空目录。

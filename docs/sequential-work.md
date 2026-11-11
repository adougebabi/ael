# 父任务内顺序推进

本地 `work next` 查询已授权 Epic 内的下一叶子，`work claim` 在同一 SQLite 写事务中重新检查顺序、依赖、能力、父范围授权、Runner 的在制任务和现有租约，然后领取当前单元。计划中的 `order`、`capabilities` 生效；手工关联的叶子按稳定 ID 排序，同时优先处理其父范围内的依赖。缺少可用执行者或能力时返回等待，不自动空转。

```sh
./ael work next --parent EPIC --runner RUNNER --capabilities 开发,验证 --json
./ael work claim --parent EPIC --runner RUNNER --capabilities 开发,验证 --json
# claimed 时使用 next.attempt.id/token；先 context，再执行并 submit 当前单元。
./ael context --task LEAF --json
./ael submit --file submission.json --json
# 若领取的单元是命令检查，使用同一 attempt，不能再发起第二次领取：
./ael verify --task LEAF --check CHECK --executor RUNNER --attempt ATTEMPT --token TOKEN --json
# 模型/场景检查用同一已领取 attempt 提交结构化评估：
./ael assess --file assessment.json --attempt ATTEMPT --token TOKEN --json
./ael work next --parent EPIC --runner RUNNER --capabilities 开发,验证 --json
```

默认一个 Runner 在一个本地项目内只保留一个未完成叶子，同一父任务也默认只由一个 Runner 推进。一个步骤提交后仍先处理该叶子；验证失败返回同一叶子的修复步骤，不跳过它领取后继。完成所有必要检查后才可推进下一叶子。当前 attempt 已提交且无未恢复旧写入者时，后继执行者可以自行领取同一个未完成叶子，无需前一角色安排其他 Bot；也可显式执行 `work release --parent EPIC --runner RUNNER --reason 理由 --json` 记录交接。需要 Attached 独立续租时可在 `work claim` 提供可靠常驻的 `--owner-pid PID`，否则按原有租约规则 heartbeat。

不同 Runner 只有双方都明确指定 `--parallel`、提供互不相同且完整的 `--resources A,B` 声明，且任务范围可判定不重叠时才可并行；未知或通配范围按冲突处理。资源声明由执行者负责真实反映共享环境，AEL 不推断外部资源的全部副作用。

Managed 父运行器可以使用：

```sh
./ael loop run --parent EPIC --runner RUNNER --capabilities 开发,验证 --config executor.json --json
./ael work checkpoint --parent EPIC --runner RUNNER --json
```

它保留单任务 `loop run --task ID`，每个子进程仍只完成当前 attempt，不可递归启动 loop。两种有界循环都返回 `stop_reason` 和 checkpoint；`max_steps` 表示达到上限并停止，不表示任务已完成。`wait_human`、`wait_capability`、`wait_runner`、`wait_dependency`、`runner_offline`、`budget_stopped`、`wait_wip`、`wait_retry`、`wait_environment`、`wait_children`、`wait_integration` 分别说明停止原因；不能凭退出码 0 判断交付完成。

同一运行库中，Runner assignment 和 checkpoint 在进程退出后保留。旧 attempt 未确认停止前不能被接管；核实后按原流程 `resume --ack-stopped` 再继续。跨运行库恢复不自动复活旧租约或进程；这不代表远程 Runner 或跨设备交接已经完成。

可在新临时目录运行确定性演示：`python3 scripts/sequential_work_demo.py --bundle /path/to/unpacked-distribution --out /tmp/ael-sequential-fixture`。演示不是实际 Agent 运行。

本地 HTTP 使用现有 session 鉴权：`GET /api/work/next?parent=EPIC&runner=RUNNER`，`POST /api/work/claim` 接收 WorkRequest，`POST /api/work/release` 接收 parent/runner/reason，`GET /api/work/checkpoint?parent=EPIC&runner=RUNNER` 读取检查点。已领取的检查可在 `/api/action` 的 verify/assess 请求中传入 attempt、token 和对应执行者；不能重新领取同一检查。

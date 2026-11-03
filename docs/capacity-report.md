# SQLite P0 实测

2026-11-03，darwin/arm64，Go1.26.3，16 logical CPU。命令：`go build -o /tmp/aelbench ./cmd/aelbench && /tmp/aelbench /tmp/ael-p0-capacity`。原始数据见 capacity-results.json，数据库在仓库外 /tmp/ael-p0-capacity。每项含3个 Attempt 元数据与20条关键事件。无截图或外部执行的基准样本中日志/产物均为0，不能据此推算真实执行日志容量。

| 工作项 | 事件 | backup bytes | index bytes | 写入 P50/P99 ms | 100组分页/计数/历史查询 ms |
|---|---|---|---|---|---|
| 1,000 | 20,000 | 3,477,504 | 401,408 | .006/.044 | 9 |
| 10,000 | 200,000 | 34,992,128 | 4,468,736 | .006/.056 | 43 |

四个独立进程各竞争同100条记录，总获胜100、冲突300；整批耗时约4.8–20.9ms。kill 未提交写事务后，错误状态记录数0。VACUUM INTO 生成一致备份；显式删去半数事件并 VACUUM 后10k主库19,640,320 bytes。WAL仍可能占用约19.8MB：必须独立统计并 checkpoint，不能只报主库。

结论：锁定 modernc.org/sqlite v1.44.3 为唯一运行后端，WAL+FULL+busy_timeout(5000)、短 BEGIN IMMEDIATE；外部命令不持事务。此基准测量元数据能力，并非 Coding Agent 吞吐量或生产 SLA。只支持同机本地文件系统，不支持网络共享数据库。P7补充实际日志与产物统计。

## P7 完整模型补测

命令：`GOCACHE=/tmp/ael-go-cache go build -o /tmp/aelbench ./cmd/aelbench && /tmp/aelbench /tmp/ael-p7-capacity`。完整 Task/Definition/Plan 与3个全字段 Attempt，每项20事件，另每项生成1104 bytes真实日志与600 bytes产物文件。原始JSON：capacity-results-full-model.json。硬件可见16逻辑CPU、darwin/arm64；sandbox下sysctl硬件型号读取被拒，未编造具体芯片或内存。

| 工作项 | 备份主库 bytes | 索引 bytes | 日志 bytes | 产物 bytes | 写P50/P99 ms | 100组看板SQL ms |
|---|---|---|---|---|---|---|
| 1000 | 7,266,304 | 401,408 | 1,104,000 | 600,000 | .008/.074 | 14 |
| 10000 | 72,646,656 | 4,468,736 | 11,040,000 | 6,000,000 | .009/.079 | 45 |

10k的checkpoint前主库73,072,640 bytes、WAL73,562,632 bytes、SHM163,840 bytes，不能只统计主库。删去半数事件并VACUUM后主库57,294,848 bytes；日志与产物没有被暗中删除。四进程仍总获胜100、冲突300；kill未提交事务后脏状态0。实测SQL分页能力不等同HTTP完整看板全量响应延迟；目前UI按刷新周期读取任务集合，容量上限需结合实际任务详情大小另评估。

补充真实领域协议的进程回归：cmd/ael/main_test.go 的 TestMultiProcessProtocol，6个进程竞争一次claim仅1成功，6次相同提交全部返回同一状态，仅1条Attempt。真实命令日志与产物的产品级stats/backup/restore/prune通过TestBackupRestoreAndPruneSummary验证。

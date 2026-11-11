# 参考基线与本轮边界

本任务包编制于 2026-11-11。参考公开仓库提交：

```text
adougebabi/ael
c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4
```

这是**计划编制时的只读核对**，不是对你的本地代码、AEL 实际数据、大任务进度或 Oracle 部署的验收。开工仍以本地 HEAD、未提交改动和当前配置为准。

## 已核对且影响实施顺序的事实

1. 该提交根目录未列出已提交的 `.ael/`；`assets/skills/ael-work/` 是产品分发资源。本轮不把 AEL 仓库接入 AEL。
2. `internal/core/model.go` 已有 Parent、Dependencies、Attempt、Evidence 等；应补齐行为而不是再设计一套无关数据模型。
3. `internal/execution/runner.go` 现有 Run 围绕单个任务 id 推进，有 max_steps、递归保护、按检查能力暂停等逻辑。不能仅把 max_steps 改成无限，就声称大任务调度已完成。
4. `cmd/ael/main.go` 默认对输出和错误做 JSON 编码。增加默认文本时，必须把机器消费入口改成显式 JSON，尤其内部版本 probe。
5. `internal/core/verify.go` 已提供 ReviewResultExact / ReviewResultWithNoteExact，支持 pass/fail/blocked、准确版本及幂等。旧讨论中“人工仅能通过”的判断已不足以描述此提交；本轮复用新实现。
6. README 描述本地运行目录与项目工具分发、upgrade/update 分离，以及现有会话 token 不构成远程用户认证。上云必须增加认证与权威边界，不能直接把原端口暴露公网。
7. 没有取得“MeroMero-first、拟人交流、提示词治理与能力插件化”实际任务全量数据。不能以标题推断原 T00—T12 的具体含义或宣称其完成情况。

## 可复核入口

以下均是参考，不要求执行者重复长时间调研；涉及具体接口时以安装版本与原文为准。

```text
https://github.com/adougebabi/ael/tree/c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4
https://github.com/adougebabi/ael/blob/c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4/cmd/ael/main.go
https://github.com/adougebabi/ael/blob/c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4/internal/core/model.go
https://github.com/adougebabi/ael/blob/c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4/internal/core/verify.go
https://github.com/adougebabi/ael/blob/c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4/internal/execution/runner.go
https://github.com/adougebabi/ael/blob/c3f3da2e264c3a0cb99d28c3fa6c04e9cd7d8bf4/README.md
https://www.sqlite.org/useovernet.html
https://sqlite.org/wal.html
https://docs.github.com/en/rest/releases/releases
https://docs.github.com/en/rest/releases/assets
```

SQLite 仍是服务端同机嵌入存储，客户端通过应用 API 访问；不把 WAL 放到网络盘多机并写。GitHub Releases 仅作为可信发行来源的候选，当前是否存在可用 Release 必须实际查询，不能编造下载成功。

本任务包没有执行仓库变更、代码构建、V2 功能测试、发布、业务迁移或 Oracle 部署。附带账本检查器的测试只验证实施清单格式与顺序。

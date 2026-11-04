# 独立工程完整演练记录

此记录只描述通用测试工程；它不是业务项目模板，也不把确定性脚本当作 Agent。

1. 源码基线 `58efd8c`，创建 `codex/workflow-protocol-v1`，保留现有实现；差距见 workflow-gap-record.md。完整阅读用户提供的两份文档。
2. 最终四平台分发版本 `0.1.4 / 58efd8c-60020764eaf8`。在 `/tmp/ael-workflow-delivery-checks` 全新工程用 scripts/workflow_acceptance.py 实跑 12 个 CLI 分支。来源原文、初始 hash、定义、每条命令和输出保留在该目录。
3. 主项目 `/private/tmp/ael-workflow-final/project`，运行目录 `/tmp/ael-workflow-final/runtime/bf06bfcf6857631d5c85561c`，所有自动命令只用本地 Python 标准库或本地目录复制，不连接外部账号、数据库或生产环境。
4. 接入既有 AGENTS、current 算术规范和 rejected 决策。索引保存来源与 hash；任务 context 提供同正文和适用理由；看板可反查。普通 feature 实际新增 scale、测试和规范，按最终候选验收。旧任务仍能展开更新前规范正文。
5. 当前真实 Codex Attached 宿主领取 attached-normalize，实际读取 normalize.py、test_normalize.py 和 text-contract。初始 strip 只有边缘裁剪，内部空格及 Unicode 换行两例失败，空输入通过。AEL verify 返回 repair，失败记录未清空成不存在。
6. 同一真实宿主重新 claim/context 后将实现改为 `" ".join(text.split())`。提交真实源码/测试引用及无需改 Spec/Decision 的理由。真实 unittest 三例通过，AEL 达到 verified；执行者与失败修复过程在同一看板可见。
7. attached-semantic 为实际模型判断：读取三个明确输入、输出 runtime 报告、记录 hash、发现、理由和非形式证明限制；以 method=model 提交，未改源码。它没有代替命令测试。
8. 最后时间线与 UI/UX 覆盖修正后工具链输入改变，原记录保留；在最终包创建 feedback_of=attached-normalize 的只验收轮次 attached-release-recheck，当前宿主复核并重新运行真实 3 例回归，交付 report。没有降低原目标或修改旧完成历史。
9. actual-dashboard-release 为真实 Chromium 的只验收轮次：实际打开最终本机页面，检查目标、依据、执行者、失败/通过、交付及事实时间线，保存真实 PNG 和 HTTP 响应；method=scenario。缺环境的 T09-scenario 则继续保留 blocked，没有改成人工 pass。
10. 关键契约使用隔离 fixture-owner 记录准确版本决定；旧否决保留。并行通过真实 CLI 进程与两个绑定 checkout 验证；同写目录拒绝。T04 验收失败交付报告、源码 hash 不变；T10 只向独立本地目录复制并核对 hash，回执真实，未接生产。
11. 空项目、无索引、损坏、读取失败及缺关键资料分别演练；用户改 Skill 时 refresh 拒绝覆盖；临时用户级卸载保留项目。新 Codex CLI 会话尝试因用户配置 features 类型错误真实阻塞，记录 process.log，没有更改宿主配置或伪造通过。
12. 内核 race/vet、类型检查与构建、最终包安装 CLI、真实浏览器 8 项全部按实际记录。完整新 CLI 会话仍未验证；其他厂商、非原生平台和生产环境未实跑。

复查入口：<http://127.0.0.1:7339>。服务的版本、构建、实际路径、数据目录可在初始化与迁移页核对。

原始证据索引：

- 最终安装演练：`/tmp/ael-workflow-delivery-checks/results.json`、`commands.json`。
- 真实修复：`/tmp/ael-workflow-final/actual-failure.json`、`actual-pass.json`，及其 runtime 日志。
- 模型审查：`actual-semantic-pass.json`、runtime 的 semantic-review.md。
- 最终包复验：`final-recheck-pass.json`、runtime 的 final-recheck.md。
- 实际场景：`dashboard-final-pass.json`、runtime 的 dashboard-final-*.png / requests.json / review.md。
- 看板回归：`/tmp/ael-browser-results/report.json`、workflow-spec-chain.png、attached-real-loop.png、migration-applied.png、disconnected.png。
- 新宿主阻塞：`/tmp/ael-workflow-final-codex/host-result-final.json` 及所引用 process.log。

本轮没有提供或使用既有业务项目路径。所有写入、升级、迁移、发布模拟授权和本地交付均限上述测试工程；业务工程正式升级需要另行授权及相匹配备份/回退计划。

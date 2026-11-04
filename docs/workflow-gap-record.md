# 通用工程协议整改：差距与决策

核查基线：2026-11-04；源码 `58efd8c`，干净 master；整改分支 `codex/workflow-protocol-v1`。
已完整阅读用户提供的 Protocol v1 与 Remediation Prompt；二者是本次实现/验收输入，不是业务工程模板。

| 协议 | 当前源码与实际表现 | 保留 / 修复 | 验证 |
|---|---|---|---|
| INIT / R1 | bootstrap.Init 只检测目录；knowledge.ReadAll 只认 .ael/knowledge；现有迁移把未分类 Spec 全设 conflict | 保留迁移快照/回滚。增加原位来源索引及逐项对账，明确继承，不复制另一套可写正文 | T01、T11、T12 |
| W1/W2 | Definition.Knowledge 只有 ID；缺失引用可静默消失；短 Skill 缺按节点详细责任 | 复用任务/知识模型；固定来源/hash/适用原因，执行前加载；按需协议及真实命令入口 | T01—T03、T08 |
| W3 / R2 | claim/submit 互斥、fencing、幂等、候选已有；失败 repair 只在提交响应存在 | 保留安全内核，持久下一动作，准备故障保存现场，进展与定义分离 | T02、T06、T07 |
| 修订 | syncDefinition 对标题/排版也全量失效并审批；policy 直接 hash bytes | 区分展示、检查配置、工作范围及授权边界；必要时保守重验并说明原因 | T05、T08 |
| W4 / R3 | Verify 真命令有日志；Review 一律 human/pass；qa/verify 没有只验收保护 | 扩展明确方式及输入证据，失败报告交付独立于候选通过；缺能力准确阻塞 | T04、T09 |
| W5 / FEEDBACK | 交付有候选审批/回执；反馈双向关联；缺显式知识收敛责任 | 保留交付/反馈，记录知识变更或无需变更原因，完成前核查最终候选 | T03、T10 |
| 看板 / R4 | 五板块已有；正文有 hash；工作/交付埋 JSON，知识页不轮询，显示 asset_version 冒充服务版本 | 保留视觉；补任务依据/执行/覆盖/卡点/交付及知识反查，实例诊断与刷新 | T01—T12 浏览器验证 |
| 运行 / R5 | PATH ael=~/.local/bin/ael，实报 0.1.3/build ada13c4；仓库 HEAD=58efd8c；dist 同为旧包；当前监听未见 AEL 7331 | 不动全局旧安装。新包只安装到 /tmp 独立项目/独立运行目录，核对服务二进制和数据 | 安装后演练、版本/API/浏览器 |

本机：Go 1.26.3 / Node 24.12.0 / npm 9.4.0；Codex 位于 /opt/homebrew/bin/codex，实际宿主运行尚未验证。
未提供既有业务工程路径，本轮不扫描其他工程；不修改其 AGENTS、hooks、数据库、服务或发布配置。
完整 ps 被沙箱拒绝；监听清单只能证明当时可见端口，不能推定不存在其他运行实例。

后续实测与限制统一记在 workflow-acceptance-report.md；此表不把静态代码能力记为实测通过。

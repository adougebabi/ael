# V2-20 复核记录

中央存储独立复核发现恢复目标误拒绝历史 /data 路径，以及恢复续跑只检查相同字节、未拒绝硬链接或宽权限的问题。修正后保留 restore-audit-red.log 和 restore-audit-green.log，新增历史路径、硬链接、权限与空 Docker 卷回归。最终存储静态复核未指出剩余必修缺陷。维护/TLS 独立复核未指出必修缺陷。

真实 Compose 验收先后暴露网络 IPAM 空值、持久通信遗漏显式 CA、心跳回执查询入口错误、一次性 server 容器固定 IP 冲突、空 named volume 权限和 Git 候选无 HEAD。分别修正探测、统一 TLS transport、使用 portable-operations 回执、无网络 operator 服务、镜像目录权限和真实 fixture 提交。开发失败日志保留在 /private/tmp/ael-v220-*，最终摘要位于本目录。

change-scan.json 通过，README、架构、服务说明、运维说明、Compose README/DESIGN、Skill 和 CLI help 已同步。quality-scan.json 通过，零错误及 39 条仓库级警告。新增存储验收脚本的 CLI helper 参数数量提示已复核，其参数分别对应实际调用身份和错误预期。

security-scan.json 为全仓文本启发式扫描，原始报告保留全部 94 条发现。四条 critical 分别是敏感内容拒绝器的 PRIVATE KEY 字面量、TLS 测试中的 fake 私钥块、固定 artifactColumns 的 SQL 列名拼接和固定两个表名的 PRAGMA 测试。生产 SQL 的 task ID 使用参数占位符，测试不接收用户表名。五条硬编码凭据发现位于合成测试或旧安全探针。79 条路径告警主要命中离线工具/测试的 Path.resolve，并未证明服务接受任意路径。三条 SSRF 告警来自测试 HTTP 请求。生成前端的 innerHTML 与两条调试提示为既有代码。新增 Compose/存储脚本仅操作明确隔离目录，TLS 测试只包含 ZmFrZQ== 合成内容。扫描结果不能自动视为通过，逐项分类复核后没有发现本次提交包含真实秘密或新增可复现漏洞。

恢复采用清除旧 session、撤销旧项目令牌的产品实现决定。账号、密码摘要、成员关系和历史数据保留，管理员须重新登录并签发令牌。旧责任和未知宿主状态继续受到版本与恢复约束。

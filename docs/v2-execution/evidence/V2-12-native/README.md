# V2-12 原生隔离探针记录

这些文件保存一次性本机合成 fixture 的输入与原始结果，不是 V2-12 验收通过的证明。`*.py` 是当时实际运行的脚本原样留存；它们的异常清理路径尚未完整加固，**不要直接作为产品组件或自动化测试复用**。

- `volume-observation.json`：一次 APFS 强制卸载**返回后**，两个持有旧文件句柄、目录句柄和工作目录的脱组进程尝试写入失败。实验没有让写入与卸载并发；原挂载路径返回 `EACCES` 还受到下层目录权限 `0500` 的影响。原脚本的 30 秒等待超时短于卸载命令的 45 秒超时，未来重做须使用明确的成功/取消屏障。
- `sandbox-probe-v1-result.json` 和 `sandbox-probe-v2-result.json`：修正信号与 `/dev/null` 规则前后的结果。第二次在最小离线 fixture 中通过 Go 测试和 Git 状态检查，拒绝特定控制文件/映像读取、原 fixture 工作区写入及向自有监督进程发信号；刻意继承的合成凭据文件描述符仍可读。profile 使用 `allow default`，不证明其他宿主路径、IPC、网络或真实项目均被保护。
- `crash-result.json`：一次模拟 Runner 崩溃中，重挂载后保留的 3 条写入**起始时间戳**晚于发送 kill 的起始时间，其中 2 条晚于监督器观察到 EOF，0 条晚于卸载返回。脚本先取时间戳再调用 `write`/`fsync`，没有逐次记录调用完成时间，不能据此推算通用上限、确认所有旧写法都被拦截或证明进程已退出。该脚本在就绪文件尚未完整写入或启动异常时可能遗漏已创建的工作进程；重做前须原子发布就绪状态、独立登记进程身份，并在所有异常路径确认清理。
- `freeze-result.json`：`mount -u -r -f` 在此 APFS 系统因不支持 `force` 而失败，卷未变为只读，故该子项未验证只读重挂载后的行为。对已打开文件设置 `UF_IMMUTABLE` 后，`pwrite`、`ftruncate` 和已有共享 mmap 写调用均未报错；根目录标志未阻止未设置标志的子目录新建文件，沙箱进程也能清除文件标志。各操作并发作用于同一文件，没有逐项重新挂载核对最终字节，不声称每项都已持久化。
- `codex-sandbox-v1-result.json`、`v2-result.json` 保存参数错误；`v4-result.json` 在临时 `0600` 合成租约和回环正向对照下，记录 `codex sandbox --permission-profile :workspace` 的一次行为：命令能读取并编码已知路径的假租约，创建临时 Git 标签被 `.git` 写限制拒绝，假回环连接返回 `EPERM`。这是直接命令沙箱探针，不等同于 AEL 实际的 `codex exec --sandbox workspace-write --add-dir ...`，没有使用真实认证材料或证明所有发布方式被阻断。

三个磁盘映像的测试脚本均报告自身卸载成功；随后对本轮已知进程 PID 的查询无匹配，对本轮三个映像路径的附加状态查询也无匹配。合成临时文件及映像仍保留在各结果记录的 `/private/tmp` 路径，以便排查，可能被系统清理。

OpenAI 的 [Sandbox security](https://developers.openai.com/api/docs/guides/agents-api/environments/security) 文档也说明，环境中的 Agent 代码可能访问该环境可用的文件、凭据和网络；它不替代上述具体测试。

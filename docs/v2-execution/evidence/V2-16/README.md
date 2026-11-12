# V2-16 验收证据

本目录保存最终正式二进制的脱敏结果、实际图片和失败修复记录。完整请求、会话、令牌、数据库和浏览器 trace 留在 `/private/tmp/ael-v216-*` 私有夹具中。二进制内容校验见 binary-sha256.json。

正式入口为 `python3 scripts/remote_review_acceptance.py --bundle dist --out /private/tmp/ael-v216-NEW`。外部驱动调用正式 CLI，两个独立配置与 checkout 上传真实 Chromium 生成的基准/实际图。三种结论、意见与正式检查、原请求恢复、候选变化和提交后定义变化均经真实 HTTP。人工结论为受控测试输入，未宣称真实人类审阅。

formal.log 与 summary.json 对应 `/private/tmp/ael-v216-review-complete`。三个 PNG 展示叠加、保留旧版本表单和不可用证据。缺失、清理状态只在该测试 Server 的独立附件数据中注入，上传未完成使用正式上传入口。原件清理及进行中任务保护另由附件回归验证。

Local 浏览器的旧 A12a 在从未领取或接手已结束且没有旧责任的任务上硬要停工复选框，local-ui-red.log 与 local-ui-second-red.log 保存两处超时。基线 b7c0d7f 已仅对存在旧责任显示该项，本次改为断言该空责任场景没有复选框，继续真实恢复/领取/检查/删除闭环。真实旧责任的安全拒绝和确认测试仍执行。

initial-fixture-01/02 保留夹具未按实际 Fast 计划先执行 define 的失败。initial-browser 保留选择控件嵌套标签的精确匹配失败。expanded-browser-red 保留新增叠加测试未切回并排及重复登录触发真实限流的失败，后续显式切换显示方式并复用已建立会话。post-review-revision-red 保留远程检查修订误用宿主路径的真实缺陷，修复后用中央不可变候选及观察顺序校验复用，旧版本表单继续拒绝。

安全扫描覆盖 Go 与 TypeScript，扫描器未解析 Vue 单文件组件。Vue 路径另经独立只读审查、人工源码核对及真实浏览器验收。security-server.json 的固定 artifactColumns SQL 拼接仍使用绑定参数，生成前端的 innerHTML 命中来自既有关闭原始 HTML 的 Markdown 渲染路径。对应命中未新增动态 SQL 或原始 HTML 输入。

最终新界面八项场景与 Local 59 项全套界面通过。额外覆盖超过轮询周期的真实读取延迟、项目身份读取失败后的草稿命名空间保护，以及提交后版本变化提示。Go 全量、相关 race、vet 和实施账本测试日志一并保存。

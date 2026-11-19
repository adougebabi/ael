# AEL 可用性整改工作记录

当前任务：UC-01 现场复发修复——已批准存量任务的技术恢复与下一步指引
当前状态：DONE（UC-01现场技术恢复与指引补修完成；按用户要求本地提交，未发布）
代码基线及本轮改动：main / 7acfabf；线上Server已是7acfabf-f5d256631c72，Mac共享CLI仍cbb29fb-97b6f1d1a497，业务工程Skill未包含新接管指引。本次新增只读recovery_action=resume与Skill分支，不改审批/恢复写入规则。
本项已经完成：核对原导入active、真实owner准确版本恢复批准、当前无责任/阻塞/后续暂停；4项由executor执行CAS技术resume后continue，PrimitiveString原本可继续未改，最后已由既有业务执行者领取；本线程未新增租约。补只读下一步指引，避免再要求人工重复确认。
本项尚未完成：本次现场与源码补修验证无剩余项；补修代码本地提交，尚未发布/部署。业务实现继续由该工程Agent负责；本线程未领取业务执行责任。
已执行验证与结果：本次先红后绿的core回归通过；旧归档已批准→executor恢复的HTTP/仪表盘与幂等测试通过；相关core/server race通过（1.441s/56.022s），最后失败复核保护补测core race通过（2.187s）；显示契约、web类型/构建、相关go vet、原生构建和diff检查通过。上轮全仓回归记录仍保留。
具体阻塞及影响范围：现场恢复门禁已解除。发现是已批准后仍缺技术resume、旧CLI/Skill与新Server不同步；不再要求用户整批网页恢复。来源未知且没有当前授权的其他任务仍按原规则保护。
下一条可以直接执行的动作：业务Agent重新读取各项next：4项continue可按原协议领取；PrimitiveString当前wait_executor，已有执行者处理，不抢占。补充提示代码如需上线，沿正常版本交付同步Server/CLI/Skill，不原地改写冻结会话。

| 任务 | 状态 |
|---|---|
| UC-01 历史任务自主核对与接管 | DONE |
| UC-02 普通人工验收直接完成 | DONE |
| UC-03 工作台与大任务进度有效化 | DONE |
| UC-04 公共交互组件统一视觉 | DONE |
| UC-05 附件占用归因与针对性优化 | DONE |

## 开工检查点与边界

已完整读取根目录提示词、README、docs/architecture.md 与 .impeccable.md。以当前HEAD/工作区实施，未回退历史版本。已有修改是 cli-onboarding 工作记录及未跟踪提示词/截图，无既有产品源码修改。始终WIP=1，子代理只做当前UC的只读定位/审查。保留Server＋Dashboard＋共享CLI、Local模式、原状态机与SQLite；未新增常驻Runner，未以AEL管理本轮开发。

## UC-01 — DONE

问题：导入暂停与通用人工confirm/resume分离，Agent无法在既有执行权限内独立核对接管。

变更：新增受限 `task reconcile-import --task ID --context CONTEXT --reason 核对结果 --start 继续起点 --expected-revision REV --operation-id OP --idempotency-key KEY`。服务端在原事务/CAS/回执链中检查active导入账本、可信来源或当前准确恢复授权、当前身份context、暂停/业务决定/失败/执行责任；只解除导入hold并记录真实Agent事件。正常claim/submit继续消费next。新归档保留最小暂停/未决决定来源；旧归档仍兼容，缺失事实明确解释，不由旧PASS或Agent自述推断授权。通用confirm仍只允许人工会话，不生成假审批或当前验收。已解除hold的新key不会撤销后续计划批准。同步CLI/help与产品Skill。

验证：真实context发布/规范字节→reconcile→claim→上传实际文件→submit推进验收；同键重放；未知来源、原暂停/决定、后来人工暂停、有效责任、context/角色/过期版本保护。核心/Server/CLI相关及宽回归通过，并纳入最终全仓race。

证据：`internal/server/import_reconcile_test.go`、`remote_runner_test.go`；早期相关日志 `/tmp/ael-uc01-targeted-final.log`、`/tmp/ael-uc01-cli.log`。限制：未改变生产历史任务；缺少可验证来源或当前恢复授权的个别旧任务仍需负责人明确范围。

## UC-02 — DONE

问题：普通人工验收被通用理由、附件、UI截图和重复确认束缚。

变更：任务首屏/具体检查直接提供验收通过、发现问题、暂无法验证。普通通过一次点击，无必填理由/附件/同义勾选；fail/blocked只收集一段问题/原因。身份来自真实登录会话，候选/定义/计划/范围按原正式review_result绑定；证据方式记录为direct_observation或observation_with_attachments。其他门禁仍按原协议推进。明确约定截图/专项报告/Check.Artifacts仍核验，单纯kind=ui不强制截图；“实现截图功能/logo.png”等产品文字不误判为截图证据要求。没有自动生成通用Check.Artifacts的源码依据，因此不凭文件名清除显式约定。

准确性：原请求/键保留以恢复丢响应；不清待确认请求来换版本。草稿合并保留完整表单内容；新旧版本分别展示，版本变化拒绝旧结论并保留输入。普通附件收起，完整表单取消第二份恢复说明和重复勾选。

验证：相关core/server测试、权限/命令方法/过期候选/专项截图；最终完整分发15项真实浏览器全部通过（含UC-04两尺寸），以及人工通过后外部命令检查完成剩余门禁。`/tmp/ael-usability-review-final-2/summary.json` PASS。

证据：`evidence/UC-02/direct-review-before.png`、`direct-review-after.png`、`final-review-summary.json`。限制：不猜测用户实际看过的部署版本；页面明确展示被确认候选，无法关联时记录无法验证。未执行生产验收。

## UC-03 — DONE

问题：列表只显示时间，方案决定混入工程成果，父任务缺少子项结果与整体缺口，报告原文难读。

变更：列表、待办和详情共用workPresentation，从权威summary/WorkHealth派生主要结论、原因、处理角色和next。列表显示成果正文；没有成果明确缺口。Attempt仅保存真实progress上报的当前工作/next/时间，心跳不算成果；Skill要求工作单元、失败、阻塞变化、恢复/交接/交付及时回报。EvidenceProgress绑定候选和定义/计划版本，旧结果或缺版本的记录明确待复验/核对；方案批准、无新证据提交不计工程成果。

父任务：原定义关联子项显示真实成果、验收、阻塞、下一步及直达入口，不自动等于必需交付；无整体计划说明缺口。Epic沿完整数据计算分母后分页，保留历史/当前/失败/未执行区别，子项成果按真实source task读取。

证据：JSON先显示结论/观察/范围/候选/来源，原文展开；可读文件名、原件下载、图片打开与缩放、复用标记。现有JSON上传实际使用text/plain，未擅自扩展格式。只有报告引用时不冒充原图。

验证：当前上报在列表与详情一致、无成果、暂缓/恢复、历史边界、混合验收、JSON阅读等7项浏览器通过；1/8/30子项、完整分母、分页、失败证据、实时汇总等5项Epic浏览器通过。最后适用版本修复补跑相关core/server与7项浏览器均通过，最终全仓race覆盖领域回归。

证据：`evidence/UC-03/current-work.png`、`readable-report.png`；`/tmp/ael-uc03-browser-final.log`、`/tmp/ael-uc03-epic-2.log`。限制：没有前端猜测成果、百分比或新后台进程。

## UC-04 — DONE

变更：沿.impeccable.md与既有Demo，使用polish/frontend-design技能局部检查。owner原生details改为按钮＋向上浮层，去除全局折叠污染和无效--ink；触发项、链接/按钮尺寸对齐，支持外部点击/Esc/Tab/焦点返回；项目切换Esc焦点返回和窄屏44px操作保留。未更改全站主题或其他折叠内容。

验证：1280×800与390×844实际鼠标/键盘操作，trigger不移位、浮层不越界、项目切换和证据图片缩放通过；最终完整分发15项浏览器中包含这两项。

证据：`evidence/UC-04/UC04-account-closed-1280.png`、`UC04-account-open-1280.png`、`UC04-account-closed-390.png`、`UC04-account-open-390.png`、`UC04-evidence-390.png`，已替换为最终build截图。未部署。

## UC-05 — DONE（真实统计已补齐）

问题与变更：确认原实现只按请求键幂等，相同字节不同键会重复保存。新增本项目artifact_content最小映射复用一份不可变、单链接原件；逻辑附件的任务/候选/来源/时间独立保留，不跨项目。begin验证既有字节后回执received=size，无额外完整预留或临时文件；原CLI已支持。重叠上传在finalize会合，映射提交后再清stage及中断遗留目标，保留文件已落盘/DB未提交的恢复能力。旧无映射附件仍按原ID读取。没有旧数据批量迁移或图片转码。

统计与回收：实际目录字节＋剩余预留，inventory按稳定记录仅计一次物理占用，并显示逻辑附件、类型/来源、哈希物理重复量和保留依据。prune预览计算实际唯一可释放字节，删除时复核finalized/purging/open持有者，最后引用才删除共享原件；取消上传不影响其他对象。补齐未完成上传证据元数据列的读取回归。单连接SQLite内不再另开DB读取；物理key随同一SELECT投影。

| 统计项目 | 正式服务本轮结果 |
|---|---|
| 范围与时间 | `https://ael.adoumi.site` / `local-ai-companion`；2026-11-18 13:38:28–13:40:53（Asia/Shanghai）；线上0.1.31 / cbb29fb-97b6f1d1a497 |
| 完整范围 | files以limit=100分页取得3897项（40页），uploads 0项；99个归属任务逐个取得附件元数据，3897/3897匹配，全部finalized |
| 配额/实际 | 配额1,073,741,824B（1GiB）；原件/used 125,051,096B（119.26MiB）；temporary=0、reserved=0、untracked=0；取元数据后stats未变化 |
| 文件类型 | PNG 1051份/78,277,407B；JPEG 23份/823,863B；text/plain 2823份/45,949,826B。JSON后缀3份/2517B、Markdown后缀819份/3,924,895B；子类型仅按可辨文件名，未展开历史txt正文 |
| 来源 | runner执行证据1487份/81,515,796B；历史导入1587份/39,607,116B；knowledge snapshot 809份/3,799,406B；其他14份/128,778B |
| 主要任务 | “固定管理中心各功能页的顶部区域”7,233,830B/130项；“诊断与任务：统计卡全是—…”6,684,547B/123项；“衣柜等辅助读取失败…”5,709,219B/106项；完整前8项见field-analysis.json |
| 主要大文件 | 最大单份历史文本2,029,263B，在两个任务下哈希相同；较大的PNG约195KB。增长来自大量保存及重复内容，未证明需要有损图片转码 |
| 重复内容 | 1285个不同SHA-256，517个重复组；相同内容额外副本88,559,202B（84.46MiB，约70.82%）；若保留全部逻辑身份并共享物理内容，理论内容量36,491,894B（34.80MiB）。这是复用潜力，不是当前可prune量 |
| 明确重复样例 | 同一AGENTS.md 3610B保存237份、涉及39个任务；部分截图同哈希12–24份，部分历史文本跨25个任务重复。没有根据相同文件名推算 |
| 保留与回收 | 当前上下文、未完成任务、未决验收和有效显式引用沿现有保护闭包；不能把88,559,202B视为可直接删除量。旧重复副本的复用迁移需保留每份逻辑身份与引用，未执行迁移或清理预览/删除 |
| 未取得范围 | 数据库/WAL/备份/其他服务器目录不在附件统计内，本轮未访问，仍未知；未完整枚举每个旧context的引用闭包，因此不宣布逐对象可删除 |
| 生产处理/释放 | 仅正常只读CLI/HTTP；没有部署、迁移、清理、取消上传、调额或修改Cloudflare；实际释放0B |

依据：`evidence/UC-05/field-analysis.json`。完整脱敏inventory与元数据保留在`/tmp/ael-uc05-go-field-inventory.json`、`/tmp/ael-uc05-go-field-metadata.json`，不复制原件或把数千行明细重复写进工作记录。

隔离收益：同一54B报告不同键再次上传，产生独立逻辑ID，项目物理used/原件从128B保持128B，新增0B，temporary/reserved均0；见`evidence/UC-05/sharing-evidence.json`。32B跨任务样例两份逻辑证据共占32B，先删一份释放0B且另一份仍可读，最后删除释放32B。重叠上传及文件落盘/DB未提交（stage已移除/仍成对链接）恢复后均只留一份。见`sharing-regression.log`。

验证：既有附件/导入及新共享链路通过；最后相关Server race通过（180.015s），见`storage-race.log`。最终共享优化分发的双客户端CLI＋存储浏览器2项通过，含分类/共享节省、配额、上传取消、分页、证据预览/引用清理；见`storage-attribution.png`。最后元数据修复后最终完整分发15项review浏览器全部通过。实现中发现的事务读与中断残留均修复，不能用现场状态掩盖代码缺口。

现场统计已完成，403不再是阻塞。前次只记录状态码、未展开响应正文就交接，是排查遗漏；正常客户端及现有授权始终可用。旧生产附件保持原样；实施成果和真实归因均完成，因此UC-05记DONE。上线后的新上传复用效果与旧数据迁移不在本轮自动生产操作范围，未声称已节省线上空间。

## 最终构建与回归

- HEAD cbb29fb + 本轮工作区；最终完整分发0.1.31 / cbb29fb-f5d256631c72。
- `GOCACHE=/tmp/ael-go-cache go test -race ./...` 全部通过（Server491.699s，core94.269s，CLI132.296s）；最后两处元数据/无候选读取修复后补跑受影响Server race（180.015s）与最终15项浏览器通过。
- `go vet ./...` 最终通过；`git diff --check`通过。
- `./scripts/package.sh` 完成web类型检查/构建、四平台运行时、client/server/Skill/兼容包共8资产；dist/release/manifest.json内容完整性verified，local_unpublished。`evidence/UC-05/release-verify.json`保存核验。
- 代码/构建完成；本轮按用户要求本地提交，未推送、发布或部署。未操作任何生产任务/附件/配额；原有cli-onboarding工作记录与截图改动不纳入本轮提交。
- 真实统计403现已诊断为Python客户端签名被Cloudflare拦截；使用正常AEL CLI/Go客户端取数成功。UC-01至UC-05全部DONE，不开启下一轮。

## 2026-11-18 13:33 恢复检查点

用户要求“继续”后重新读取实施提示词、当前记录，核对main/HEAD、工作区与源码摘要。HEAD仍为cbb29fb，源码摘要仍为f5d256631c72，与已验证完整构建一致；全部本轮和原有改动保留，无新增产品代码变更。

按记录的下一动作，使用相同oracle-local-ai-companion profile，对正式服务相同项目仅GET stats一次，仍HTTP403（application/json，经Cloudflare；没有可识别的应用错误码）。未能取得stats，因此没有继续inventory请求，没有切换账号或尝试其他登录方式。具体入口/权限原因尚未获证实。脱敏记录见 `evidence/UC-05/resumed-access.json`。

UC-01至UC-04保持DONE，UC-05保持IMPLEMENTED_PENDING_FIELD。本次无须重复已通过的代码回归；没有提交、推送、部署或生产数据处理。下一动作仍是恢复该现有profile对stats/inventory的正常只读访问，再按完整分页获取真实统计。按提示词的外部条件交接规则停止，不能把未知线上归因标DONE。

## 2026-11-18 13:36–13:41 403根因与UC-05收口

公开meta与携带原profile的stats均返回Cloudflare结构化错误1010 / browser_signature_banned，说明此次Python客户端请求在AEL业务鉴权之前被拦截。随后项目原AEL CLI doctor使用同一profile成功，online=true、authoritative=true、role=executor。未改变User-Agent、登录身份或Cloudflare规则；标准Go HTTP客户端使用同一token读取stats与完整inventory成功。无需用户重新登录或额外授权。

统计3897项全部匹配99个任务元数据，前后stats一致，真实组成及重复空间已记入UC-05结果。原有IMPLEMENTED_PENDING_FIELD是历史交接状态，现在已解除。本轮产品源码未变，摘要仍f5d256631c72；原代码回归、构建和隔离验证继续适用，无需重跑以刷新报告。全部五项DONE，生产只读，线上释放0B。

## 本地提交

用户明确要求提交。本次提交包含UC-01至UC-05的源码、产品Skill、测试、已生成的内嵌前端资源、根目录本轮提示词与本目录结果/代表证据。此前已有cli-onboarding WORKLOG和历史截图修改保留在工作区，不混入。提交前git diff --check通过；产品源码未再修改，不重复已通过回归。提交标识以git log为准，未推送或部署。

## 2026-11-19 UC-01 现场复发：已批准恢复后仍要求网页操作

用户报告业务Agent要求在工作台恢复第一批5项。只读核对发现：线上已经是7acfabf新代码；Mac共享CLI及业务工程Skill仍是cbb29fb旧版。该批旧导入的Recovery来源字段缺失，但当前owner已逐项留下准确版本restore approve；confirm只清confirmation而不会清Paused。PrimitiveString已continue，其余4项只差技术resume。原回复把这两步混在一起，并把整批视作仍需要人处理，是流程指引缺口。

针对用户明确指定的5项，逐项重读当前状态与活动，检查真实当前批准、无执行/中断责任、无失败/阻塞/后续人工暂停。使用现有executor身份与expected_revision/原请求键，对Actor、Provider、LoRA、提交歧义执行技术resume；4项返回continue/paused=false/attempt_count=0；PrimitiveString已可继续，跳过。没有生成审批，没有claim，没有修改业务源码、候选或验收。证据evidence/UC-01/approved-restores-20261119.json。

补充源码：Next与TaskSummary只读投影recovery_action=resume，限当前准确恢复批准、仅导入暂停、空候选/无责任且无其他保护项；不改变原写接口。人工暂停、未决决定、旧版本批准、失败、责任与其他Server约束不推荐自动恢复。列表/详情明确执行者下一动作；产品Skill主入口与协议写明已continue直接claim、已批准由executor resume并刷新context，不能再要求用户网页重复确认或回复已恢复。客户端/冻结Skill与Server的实际build应分别核对，不改写冻结会话。

反馈环：`go test ./internal/core -run '^TestApprovedImportRecovery'`先复现失败（recovery_action为空）；修改后相关core测试通过。Server集成覆盖旧无Recovery归档→真实owner准确批准→executor读取指引→resume→幂等重放→continue，保留1条owner审批、0租约、0旧证据。

本次补修完成：core/server相关race分别通过（1.441s/56.022s），最后新增待失败复核保护的core race通过（2.187s）。Go vet、显示契约、web类型/构建、原生构建及git diff --check通过。原生验证产物 `/tmp/ael-approved-import-bundle/ael-darwin-arm64`，build=`7acfabf-approved-import-resume`，是未发布验证构建，不冒充线上版本。新增core/server回归覆盖当前批准、人工暂停、未决决定、失效版本、有效/中断责任、失败复核、已有候选、幂等与不复活历史验收。

2026-11-19 09:23只读复核：Actor、Provider、LoRA、提交歧义仍为continue；PrimitiveString已经wait_executor，由既有业务执行者领取。4次resume之外未创建审批/租约、未改业务源码/候选/验收。证据 `evidence/UC-01/final-status-20261119.json`。CLI/Skill仍旧版是已记录的交付不同步问题；已批准导入的技术resume在旧CLI也可执行，因此本次不强行升级活动会话或要求用户重复登录/确认。

2026-11-19 用户要求提交本次补修：将恢复下一步投影、显示/Skill、回归测试、更新后的内嵌资源与本次现场记录形成本地提交。原有cli-onboarding记录与截图不纳入，提交标识以git log为准；未推送或部署。产品源码未再次修改，沿用上述验证结果。

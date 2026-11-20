import { test, expect, type Browser, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

// Restoration of the reference workbench layout over the real Server. Every
// state assertion reads the Server API, not only the page.
const fixture = JSON.parse(fs.readFileSync(process.env.AEL_WB_FIXTURE!, "utf8"));
const base: string = process.env.AEL_WB_URL || fixture.url;
const shots = path.join(fixture.out, "browser-shots");
fs.mkdirSync(shots, { recursive: true });
const cookies: Record<string, any[]> = {};
test.describe.configure({ mode: "serial" });

async function open(browser: Browser, user = "owner", width = 1440): Promise<Page> {
  const context = await browser.newContext({ viewport: { width, height: 900 }, timezoneId: "Asia/Shanghai" });
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: base });
  const page = await context.newPage();
  if (cookies[user]) { await context.addCookies(cookies[user]); await page.goto(base + "/#/tasks?project=shared"); }
  else {
    await page.goto(base + "/#/tasks?project=shared");
    await page.getByLabel("用户名", { exact: true }).fill(user);
    await page.getByLabel("密码", { exact: true }).fill(fixture.accounts[user]);
    await page.getByRole("button", { name: "登录", exact: true }).click();
  }
  await expect(page.locator(".wb-sidebar")).toBeVisible();
  cookies[user] = await context.cookies();
  return page;
}
const go = async (page: Page, route: string) => { await page.goto(base + "/#/" + route + (route.includes("?") ? "&" : "?") + "project=shared"); await page.waitForTimeout(1200); };
const task = async (page: Page, id: string) => (await (await page.request.get(base + "/api/v1/projects/shared/tasks/" + id)).json()).task;
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(shots, name + ".png"), fullPage: true });
const noOverflow = async (page: Page) => expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);

test("切片1 外壳：角色可见性、项目隔离、别名路由、owner 菜单与主题", async ({ browser }) => {
  let page = await open(browser, "reviewer");
  await expect(page.locator(".wb-sidebar").getByText("服务管理")).toHaveCount(0);
  await expect(page.locator(".wb-owner-button small")).toHaveText("验收者");
  await go(page, "service");
  await expect(page.getByText("当前账号没有服务管理权限")).toBeVisible();
  await page.context().close();

  page = await open(browser);
  await shot(page, "s1-workbench-1440");
  await page.getByRole("button", { name: "切换项目" }).click();
  await page.locator("#remote-project-options").getByRole("button", { name: /另一个项目/ }).click();
  await expect(page.locator("main")).toContainText("另一个项目的唯一任务");
  await expect(page.locator("main")).not.toContainText("账户菜单与个人设置入口");
  await expect(page.locator(".wb-breadcrumb")).toContainText("另一个项目");
  await go(page, "workbench");
  await expect(page.locator("main")).toContainText("账户菜单与个人设置入口");
  await go(page, "task/child-model");
  await expect(page.locator(".wb-detail-title")).toHaveText("按任务选择模型");
  await go(page, "tasks/account-menu");
  await page.locator(".wb-nav-item", { hasText: "规范与决策" }).click();
  await expect(page).toHaveURL(/knowledge/);
  await page.goBack(); await expect(page).toHaveURL(/tasks\/account-menu/);
  await page.goForward(); await expect(page).toHaveURL(/knowledge/);
  await go(page, "settings?tab=storage");
  await expect(page.locator(".wb-breadcrumb")).toContainText("项目设置");
  await page.locator(".wb-owner-button").focus(); await page.keyboard.press("Enter");
  await expect(page.locator("#account-options")).toBeVisible();
  await expect(page.locator("#account-options [role=menuitem]").first()).toBeFocused();
  await shot(page, "s1-owner-menu");
  await page.keyboard.press("Escape");
  await expect(page.locator("#account-options")).toHaveCount(0);
  await expect(page.locator(".wb-owner-button")).toBeFocused();
  await page.locator(".wb-owner-button").click(); await page.getByRole("menuitem", { name: /石墨/ }).click();
  expect(await page.evaluate(() => getComputedStyle(document.querySelector(".wb-brand-mark")!).backgroundColor)).toBe("rgb(59, 69, 89)");
  await page.locator(".wb-owner-button").click(); await page.getByRole("menuitem", { name: /靛蓝/ }).click();
  await page.keyboard.press("Control+k");
  await page.getByRole("searchbox", { name: "全局搜索" }).fill("日程");
  await expect(page.locator(".wb-search-results")).toContainText("日程视图与生活状态验收");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.context().close();
});

test("切片2 工作台与详情：父子进度、状态区分、筛选空态与真实动作", async ({ browser }) => {
  let page = await open(browser);
  await go(page, "tasks");
  const parent = page.locator('.wb-task-row[data-task="plugin-parent"]');
  await expect(parent).toContainText("3 / 6");
  await expect(parent.locator(".tag")).toHaveText("推进中");
  await parent.getByRole("button", { name: /展开子任务/ }).click();
  await expect(page.locator(".wb-row-children .wb-task-row")).toHaveCount(3);
  await expect(page.locator(".wb-row-children")).toContainText("查看全部 6 个子任务");
  await expect(page.locator('.wb-task-row[data-task="media-retry"] .tag')).toHaveText("待实施");
  await expect(page.locator('.wb-row-children .wb-task-row[data-task="child-memory"] .tag')).toHaveText("待接手");
  await expect(page.locator('.wb-task-row[data-task="legacy-trellis"] .tag')).toHaveText("历史待核对");
  await shot(page, "s2-workbench-expanded");
  await page.locator(".wb-status-tab", { hasText: "已取消" }).click();
  await expect(page.locator(".wb-task-list")).toContainText("旧版日程导出");
  await go(page, "tasks?q=" + encodeURIComponent("记忆回想"));
  await expect(page.locator(".wb-row-children")).toContainText("记忆回想接入");
  await go(page, "tasks?q=zzzz-no-match");
  await expect(page.locator(".wb-task-list")).toContainText("这里暂时没有匹配的事项");

  await go(page, "tasks/plugin-parent");
  await expect(page.locator(".wb-detail-meta .tag")).toHaveText("推进中");
  await expect(page.locator("#remaining-title").locator("..")).toContainText("已完成 3 / 6");
  await page.getByRole("button", { name: "展开 3 项已完成工作" }).click();
  await expect(page.locator(".wb-subtask-item")).toHaveCount(6);
  await expect(page.locator(".wb-delivery-note")).toContainText("不会自动让父事项完成");
  await shot(page, "s2-parent-detail");
  await go(page, "tasks/schedule-fix");
  await expect(page.locator(".wb-situation")).toContainText("测试环境缺少时区配置");
  await go(page, "tasks/legacy-trellis");
  await expect(page.getByRole("button", { name: "确认核对并准备恢复" })).toBeVisible();

  const before = await task(page, "child-memory");
  await go(page, "tasks/child-memory");
  await page.getByRole("button", { name: "复制给 Agent" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain("child-memory");
  await expect(page.locator(".wb-toast")).toContainText("不会启动");
  const after = await task(page, "child-memory");
  expect(after.status).toBe(before.status);
  expect(JSON.stringify(after.attempts || [])).toBe(JSON.stringify(before.attempts || []));
  await page.locator(".wb-task-more summary").click();
  await page.getByRole("button", { name: "暂缓任务" }).click();
  let dialog = page.getByRole("dialog");
  await dialog.locator("textarea").first().fill("等待接口约定");
  await dialog.locator("button.primary").last().click();
  await expect.poll(async () => (await task(page, "child-memory")).paused).toBe(true);
  await expect(page.locator(".wb-detail-meta .tag")).toHaveText("受阻");
  await page.getByRole("button", { name: "恢复任务" }).first().click();
  dialog = page.getByRole("dialog");
  await dialog.locator("button.primary").last().click();
  await expect.poll(async () => !!(await task(page, "child-memory")).paused).toBe(false);
  await page.context().close();

  page = await open(browser, "observer");
  await go(page, "tasks/child-memory");
  await page.locator(".wb-task-more summary").click();
  await expect(page.getByRole("button", { name: "暂缓任务" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "取消任务" })).toHaveCount(0);
  await go(page, "tasks");
  await expect(page.locator('.wb-task-row[data-task="account-menu"] .tag')).toHaveText("待验收");
  await page.context().close();

  for (const width of [1280, 390]) {
    page = await open(browser, "owner", width);
    for (const route of ["tasks", "tasks/plugin-parent", "tasks/schedule-fix"]) { await go(page, route); await noOverflow(page); await shot(page, `s2-${route.replace(/\W/g, "_")}-${width}`); }
    await page.context().close();
  }
});

test("切片3 验收：同处查看、只提交当前检查、反馈回到修复、回执未知与证据要求", async ({ browser }) => {
  let page = await open(browser);
  await go(page, "tasks");
  await expect(page.locator(".wb-attention-card").first()).toContainText("人工观察");
  // Cards show a thumbnail only when the task has a current-candidate image.
  for (const card of await page.locator(".wb-attention-card").all()) if (await card.locator(".wb-attention-image").count()) await expect(card.locator("img")).toBeVisible();
  await shot(page, "s3-workbench-attention");
  await go(page, "tasks?review=account-menu");
  const review = page.getByRole("dialog", { name: "查看并验收" });
  await expect(review).toContainText("2 项人工检查");
  await review.getByRole("button", { name: "visual", exact: true }).click();
  await shot(page, "s3-review-dialog");
  await review.locator(".wb-review-image").click();
  const preview = page.getByRole("dialog", { name: "account-after.png" });
  await expect(preview.locator("img.wb-preview-image")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(preview).toHaveCount(0);
  await expect(review.locator(".wb-review-image")).toBeFocused();
  await review.getByRole("button", { name: "对比改动前后" }).click();
  const compare = page.getByRole("dialog", { name: /改动对比/ });
  await expect(compare).toContainText("调整前 · 历史候选");
  await expect(compare).toContainText("调整后 · 当前候选");
  await shot(page, "s3-compare");
  await compare.getByRole("button", { name: "返回验收" }).click();
  await review.getByRole("button", { name: "确认通过" }).click();
  await expect(review).toContainText("其他必要条件继续保留");
  let state = await task(page, "account-menu");
  const visual = state.evidence.filter((e: any) => e.check === "visual");
  expect(visual).toHaveLength(1);
  // Quick confirmation keeps the existing direct-observation contract.
  expect(visual[0]).toMatchObject({ result: "pass", source: "human:owner", evidence_mode: "direct_observation" });
  expect(visual[0].artifacts || []).toHaveLength(0);
  expect(state.evidence.some((e: any) => e.check === "keyboard")).toBe(false);
  expect(state.status).toBe("open");
  await review.getByRole("button", { name: "keyboard", exact: true }).click();
  await review.getByRole("button", { name: "需要调整" }).click();
  await review.getByLabel("具体问题").fill("菜单关闭后焦点没有回到 owner 按钮");
  await review.getByRole("button", { name: "提交反馈" }).click();
  await expect(review).toContainText("反馈已关联");
  state = await task(page, "account-menu");
  const keyboard = state.evidence.filter((e: any) => e.check === "keyboard");
  expect(keyboard).toHaveLength(1);
  expect(keyboard[0].result).toBe("fail");
  await review.getByRole("button", { name: "稍后处理" }).click();
  await expect(page.locator('.wb-task-row[data-task="account-menu"] .tag')).toHaveText("待修复");

  await go(page, "tasks/child-schedule");
  await page.route("**/api/v1/projects/shared/reviews", async (route) => { await route.fetch(); await route.abort(); }, { times: 1 });
  const inline = page.locator("#overview-review");
  await inline.getByRole("button", { name: "确认通过" }).click();
  await expect(inline).toContainText(/提交结果未知|原请求已生效/);
  if (await inline.getByRole("button", { name: "原样重试提交" }).count()) await inline.getByRole("button", { name: "原样重试提交" }).click();
  await expect.poll(async () => (await task(page, "child-schedule")).evidence.filter((e: any) => e.check === "visual").length).toBe(1);
  expect((await task(page, "child-schedule")).status).toBe("open");
  await go(page, "tasks/plugin-parent");
  await expect(page.locator("#remaining-title").locator("..")).toContainText("已完成 3 / 6");

  await go(page, "tasks/evidence-review");
  await expect(page.locator("#overview-review")).toContainText("本项要求专项证据");
  await expect(page.locator("#overview-review").getByRole("button", { name: "确认通过" })).toBeDisabled();
  await shot(page, "s3-evidence-required");
  await go(page, "tasks/copy-review");
  await expect(page.locator("#overview-review")).toContainText("不要求额外上传附件");
  await page.locator("#overview-review").getByRole("button", { name: "确认通过" }).click();
  await expect.poll(async () => (await task(page, "copy-review")).status).toBe("completed");
  await expect(page.locator(".wb-toast")).toContainText("任务已按原有交付规则完成");
  await page.context().close();

  page = await open(browser, "observer");
  await go(page, "tasks/evidence-review");
  await expect(page.getByRole("button", { name: "确认通过" })).toHaveCount(0);
  await page.context().close();
  page = await open(browser, "owner", 390);
  await go(page, "tasks/account-menu?section=checks"); await noOverflow(page);
  await shot(page, "s3-checks-390");
  await page.context().close();
});

test("切片4 资料、规范与演进：任务↔资料往返、输入与产物区分、损坏原件、空数据不编造", async ({ browser }) => {
  let page = await open(browser);
  await go(page, "tasks/account-menu");
  await page.locator(".wb-detail-aside").getByRole("link", { name: "查看任务全部资料" }).click();
  await expect(page).toHaveURL(/materials\?.*task=account-menu/);
  const card = page.locator('.wb-library-item[data-material="account-after.png"]');
  await expect(card.locator("img")).toBeVisible();
  await shot(page, "s4-materials-task");
  await card.locator(".wb-result-card").click();
  const preview = page.getByRole("dialog", { name: "account-after.png" });
  await expect(preview.locator("img.wb-preview-image")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(card.locator(".wb-result-card")).toBeFocused();
  await card.getByRole("link", { name: "账户菜单与个人设置入口" }).click();
  await expect(page.locator(".wb-detail-title")).toHaveText("账户菜单与个人设置入口");

  // Inputs are one choice away; same-name AGENTS.md as input and as output stay distinct.
  await go(page, "materials?task=context-guide");
  await expect(page.locator('.wb-library-item[data-material="AGENTS.md"]')).toHaveCount(1);
  await expect(page.locator('.wb-library-item[data-material="AGENTS.md"] .tag')).toHaveText("工作成果");
  await go(page, "materials?task=context-guide&source=all");
  const agents = page.locator('.wb-library-item[data-material="AGENTS.md"] .tag');
  await expect(agents.filter({ hasText: "输入资料" }).first()).toBeVisible();
  await expect(agents.filter({ hasText: "工作成果" })).toHaveCount(1);
  await page.getByRole("button", { name: "列表视图" }).click();
  await expect(page.locator(".wb-material-table")).toContainText("执行时读取的规范快照");
  await page.getByRole("button", { name: "网格视图" }).click();

  // A damaged original reports itself instead of rendering as a result.
  await go(page, "materials?task=evidence-review");
  const broken = page.locator('.wb-library-item[data-material="account-before.png"]');
  await expect(broken).toContainText(/图片无法显示|原件不可用/);
  await broken.locator(".wb-result-card").click();
  await expect(page.getByRole("dialog", { name: "account-before.png" })).toContainText(/原件|无法显示/);
  await page.keyboard.press("Escape");
  await go(page, "tasks/evidence-review");
  // The damaged upload never stands in for the required screenshot.
  await expect(page.locator("#overview-review")).toContainText("当前尚未关联所需材料");
  await expect(page.locator("#overview-review").getByRole("button", { name: "确认通过" })).toBeDisabled();

  await go(page, "knowledge");
  const row = page.locator(".wb-doc-row", { hasText: "示例规范" });
  await row.getByRole("button", { name: /收藏 示例规范/ }).click();
  await page.locator(".wb-page-tabs").getByRole("button", { name: /我的收藏/ }).click();
  await expect(page.locator(".wb-doc-row")).toHaveCount(1);
  await shot(page, "s4-knowledge-bookmarks");
  await page.locator(".wb-doc-row a.record-title").click();
  await expect(page.locator(".wb-detail-title")).toHaveText("示例规范");
  await expect(page.locator(".knowledge-reader")).toContainText("界面日期使用同一时区");
  await go(page, "evolution");
  await expect(page.locator(".wb-change").first()).toBeVisible();
  await shot(page, "s4-evolution");
  for (const route of ["materials", "knowledge", "evolution"]) { await page.setViewportSize({ width: 390, height: 844 }); await go(page, route); await noOverflow(page); }
  await page.setViewportSize({ width: 1440, height: 900 });

  // Another project: real empty states, nothing carried over or invented.
  await page.goto(base + "/#/materials?project=other"); await page.waitForTimeout(1500);
  await expect(page.locator("main")).toContainText("这个项目还没有资料");
  await page.goto(base + "/#/knowledge?project=other"); await page.waitForTimeout(1500);
  await expect(page.locator("main")).toContainText(/当前分类暂无资料/);
  await page.goto(base + "/#/evolution?project=other"); await page.waitForTimeout(1500);
  await expect(page.locator("main")).toContainText("暂无可追溯的项目变化");
  await page.context().close();
});

test("切片5 设置、接入与服务：上报不称在线、令牌只显示一次、角色权限、配额与清理预览、发行平台", async ({ browser }) => {
  let page = await open(browser);
  await go(page, "settings");
  await expect(page.locator("main")).toContainText("不代表 Agent 或本机进程当前在线");
  await page.getByRole("button", { name: "复制接入信息" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain("--remote-project shared");
  await shot(page, "s5-connection");
  await page.getByRole("link", { name: "查看接入指引" }).click();
  await expect(page.locator(".wb-steps")).toContainText("--remote-project shared");
  await expect(page.locator("main")).toContainText("网页不会连接、扫描或控制本机");

  // Account → membership → token shown once → revoke, all read back from the API.
  await go(page, "service");
  const platform = page.getByLabel("目标平台");
  expect(await platform.locator("option").allTextContents()).toEqual(["macOS · Apple Silicon", "macOS · Intel", "Linux · ARM64", "Linux · x86_64"]);
  await expect(page.getByRole("button", { name: "复制更新说明" })).toBeDisabled();
  await expect(page.locator("main")).toContainText("尚无可验证的推荐版本");
  await shot(page, "s5-service");
  await page.getByRole("button", { name: "创建账户" }).click();
  let dialog = page.getByRole("dialog", { name: "创建账户" });
  await dialog.getByLabel("用户名").fill("tester");
  await dialog.getByLabel("初始密码").fill("fixture-tester-password");
  await dialog.getByRole("button", { name: "创建账户" }).click();
  await expect(page.locator("main")).toContainText("账户 tester 已创建");
  await go(page, "settings?section=members");
  await page.getByRole("button", { name: "添加成员" }).click();
  dialog = page.getByRole("dialog", { name: "添加项目成员" });
  await dialog.getByLabel("用户名").fill("tester");
  await dialog.getByLabel("项目角色").selectOption("reviewer");
  await dialog.getByRole("button", { name: "保存成员" }).click();
  await expect(page.locator(".wb-setting-row", { hasText: "tester" })).toContainText("验收者");
  const members = (await (await page.request.get(base + "/api/v1/projects/shared/members")).json()).members;
  expect(members).toContainEqual({ username: "tester", role: "reviewer" });
  await page.getByRole("button", { name: "创建令牌" }).click();
  dialog = page.getByRole("dialog", { name: "创建 Agent 令牌" });
  await dialog.getByLabel("所属成员").selectOption("tester");
  await dialog.getByLabel("用途").fill("验收环境");
  await dialog.getByRole("button", { name: "创建令牌" }).click();
  const secret = await dialog.getByLabel("新令牌").inputValue();
  expect(secret.length).toBeGreaterThan(20);
  await shot(page, "s5-token-once");
  await dialog.getByRole("button", { name: "我已保存" }).click();
  expect(await page.locator("main").innerText()).not.toContain(secret);
  await page.getByRole("button", { name: "创建令牌" }).click();
  await expect(page.getByRole("dialog", { name: "创建 Agent 令牌" }).getByLabel("新令牌")).toHaveCount(0);
  await page.keyboard.press("Escape");
  const tokenRow = page.locator(".wb-setting-row", { hasText: "验收环境" });
  await expect(tokenRow).toContainText("tester · 继承验收者");
  await tokenRow.getByRole("button", { name: "撤销" }).click();
  await page.getByRole("dialog", { name: "撤销令牌？" }).getByRole("button", { name: "确认撤销" }).click();
  await expect.poll(async () => (await (await page.request.get(base + "/api/v1/tokens")).json()).tokens.find((t: any) => t.label === "验收环境")?.revoked).toBe(true);

  await go(page, "settings?section=storage");
  await page.getByLabel("容量上限（MiB）").fill("200");
  await page.getByRole("button", { name: "保存配额" }).click();
  await expect.poll(async () => (await (await page.request.get(base + "/api/v1/projects/shared/artifacts/inventory?limit=1")).json()).stats.quota_bytes).toBe(200 * 1048576);
  await page.getByLabel("创建日期早于").fill("2099-01-01");
  await page.getByRole("button", { name: "预览清理清单" }).click();
  const prune = page.getByRole("dialog");
  await expect(prune).toContainText("本次可清理");
  const before = (await (await page.request.get(base + "/api/v1/projects/shared/artifacts/inventory?limit=1")).json()).total;
  await prune.getByRole("button", { name: /取消|返回|关闭/ }).first().click();
  expect((await (await page.request.get(base + "/api/v1/projects/shared/artifacts/inventory?limit=1")).json()).total).toBe(before);
  await shot(page, "s5-storage");
  for (const route of ["settings", "settings?section=members", "settings?section=storage", "service", "local-setup", "account"]) { await page.setViewportSize({ width: 390, height: 844 }); await go(page, route); await noOverflow(page); }
  await page.context().close();

  page = await open(browser, "reviewer");
  await go(page, "settings?section=members");
  await expect(page.locator("main")).toContainText("成员与令牌由服务管理员管理");
  await go(page, "settings?section=storage");
  await expect(page.getByRole("button", { name: "保存配额" })).toHaveCount(0);
  expect((await page.request.get(base + "/api/v1/projects/shared/members")).status()).toBe(403);
  await page.context().close();
});

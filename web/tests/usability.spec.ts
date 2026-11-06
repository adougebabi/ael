import {
  test,
  expect,
  type Page,
  type APIRequestContext,
} from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const manifestPath = process.env.AEL_UI_FIXTURE;
if (!manifestPath)
  throw new Error(
    "AEL_UI_FIXTURE must point to a marked disposable fixture manifest",
  );
const fixture = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
if (
  !fixture.isolated ||
  !fs.existsSync(path.join(fixture.out, "ISOLATED-UI-FIXTURE"))
)
  throw new Error(
    "Refusing to test writes outside an explicitly isolated fixture",
  );
const projects = fixture.projects;
const expectedVersion =
  fixture.version || process.env.AEL_EXPECT_VERSION || "0.1.6";
const output = process.env.AEL_UI_OUT || "/tmp/ael-ui-browser";
fs.mkdirSync(output, { recursive: true });
function ensureIsolatedOutput() {
  const dir = path.join(projects.single.root, "app");
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, "ui-output.txt");
  if (!fs.existsSync(file)) fs.writeFileSync(file, "isolated UI test output\n");
}
const shot = async (page: Page, name: string) => {
  await expect(page.locator(".sidebar footer")).toContainText(
    "v" + expectedVersion,
    {
      timeout: 10000,
    },
  );
  await expect(page.locator(".loading")).toHaveCount(0, { timeout: 15000 });
  return page.screenshot({
    path: path.join(output, name + ".png"),
    fullPage: true,
  });
};
async function connect(request: APIRequestContext, name = "many") {
  const url = projects[name].url;
  const token = (await (await request.get(url + "/api/session")).json()).token;
  const headers = { Authorization: "Bearer " + token };
  return {
    async get(endpoint: string) {
      const r = await request.get(url + endpoint, { headers });
      expect(r.ok(), await r.text()).toBeTruthy();
      return r.json();
    },
    async post(endpoint: string, data: any, ok = true) {
      const r = await request.post(url + endpoint, { headers, data });
      if (ok) expect(r.ok(), await r.text()).toBeTruthy();
      return { response: r, value: await r.json() };
    },
  };
}
async function goto(page: Page, name: string, route = "/tasks") {
  await page.goto(projects[name].url + "/#" + route);
}
async function confirmDialog(page: Page, reason?: string) {
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  if (reason) await dialog.getByLabel("操作理由").fill(reason);
  await dialog.getByRole("button", { name: /^确认/ }).click();
  await expect(dialog).toBeHidden();
}
async function step(
  client: Awaited<ReturnType<typeof connect>>,
  id: string,
  artifacts: string[] = [],
  work?: () => void,
) {
  const { value: n } = await client.post("/api/action", {
    action: "claim",
    id,
    executor: "browser-fixture-driver",
  });
  await client.get("/api/context?id=" + id);
  work?.();
  return client.post("/api/action", {
    action: "submit",
    submission: {
      task: id,
      attempt: n.attempt.id,
      token: n.attempt.token,
      key: crypto.randomUUID(),
      result: {
        status: "pass",
        summary: "隔离浏览器协议步骤完成",
        artifacts,
      },
    },
  });
}

test("A01 单任务：紧凑五分组与真实等待接手", async ({ page }) => {
  await goto(page, "single");
  await expect(page.locator(".task-row")).toHaveCount(1);
  await expect(page.getByRole("navigation", { name: "任务分组定位" }).getByRole("link")).toHaveCount(6);
  await expect(page.locator("[data-board]")).toHaveCount(1);
  await expect(page.locator(".task-table-head")).toContainText("必要验收");
  await expect(page.locator(".dashboard-bottom")).toContainText("最近交付");
  await expect(page.locator(".dashboard-bottom")).toContainText("项目依据");
  const row = page.locator(".task-row");
  await expect(row).toContainText("等待接手");
  await expect(row).toContainText("当前无人执行");
  await expect(row.getByRole("link", { name: /只有一个任务/ })).toBeVisible();
  const rect = await row.boundingBox();
  expect(rect!.width).toBeGreaterThan(750);
  expect(rect!.height).toBeLessThan(190);
  expect(
    await page.locator(".board").evaluate((el) => getComputedStyle(el).display),
  ).toBe("block");
  await expect(row).not.toContainText("wait_executor");
  await shot(page, "A01-single-task");
});

test("A02 多任务：并行执行、等待接手、环境、确认分组同源", async ({
  page,
  request,
}) => {
  const client = await connect(request);
  const data = await client.get("/api/tasks?compact=1&days=0&limit=100");
  const parallel = data.items.find((x: any) => x.id === "parallel-checks");
  expect(parallel.board).toBe("active");
  expect(parallel.running).toBe(2);
  expect(parallel.checks.running).toBe(2);
  expect(parallel.checks.not_run).toBe(1);
  expect(parallel.waiting_checks).toBe(1);
  expect(parallel.executors).toEqual([
    "fixture-checker-A",
    "fixture-checker-B",
  ]);
  expect(data.items.find((x: any) => x.id === "environment").next_action).toBe(
    "wait_environment",
  );
  expect(data.items.find((x: any) => x.id === "decision-task").board).toBe(
    "confirmation",
  );
  await goto(page, "many");
  const deliveries = page.locator(".dashboard-panel").filter({ hasText: "最近交付" });
  await expect(deliveries.locator(".dashboard-item")).toHaveCount(2);
  await expect(deliveries).toContainText("已验证");
  await expect(deliveries).not.toContainText("已实现 · 尚无发布依据");
  await page.getByRole("navigation", { name: "任务分组定位" }).getByRole("link", { name: /^推进中/ }).click();
  await expect(page.locator("[data-board]")).toHaveCount(1);
  await page.getByRole("navigation", { name: "任务分组定位" }).getByRole("link", { name: /^当前工作/ }).click();
  await expect(page.locator("[data-board=active] .task-row")).toContainText(
    "2 项执行中 · 1 项检查待接手",
  );
  await expect(page.locator("[data-board=confirmation]")).toContainText(
    "需要准确修订确认的方案",
  );
  await goto(page, "many", "/tasks/parallel-checks?section=checks");
  await expect(
    page.locator(".check").filter({ hasText: "check-a ·" }),
  ).toContainText("执行中");
  await expect(page.locator(".check").filter({ hasText: "check-a ·" }).locator(".check-state-icon")).toHaveClass(/running/);
  await expect(
    page.locator(".check").filter({ hasText: "check-a ·" }),
  ).toContainText("fixture-checker-A");
  await expect(
    page.locator(".check").filter({ hasText: "check-c ·" }),
  ).toContainText("未执行");
  await goto(page, "many");
  await page.getByLabel("搜索任务").fill("等待环境标记");
  await expect(page.locator(".task-row")).toHaveCount(1);
  await expect(page.locator(".task-row")).toContainText("环境标记尚未准备好");
  await expect(page.locator(".task-row .task-state .status")).not.toContainText("执行中");
  await page.getByRole("button", { name: "查看全部记录 →" }).click();
  await page.getByLabel("完成历史").selectOption("7");
  await page.getByRole("navigation", { name: "任务分组定位" }).getByRole("link", { name: /^当前工作/ }).click();
  await expect(page.locator(".task-row")).toHaveCount(1);
  await shot(page, "A02-environment");
  await goto(page, "many", "/tasks/current-block");
  await expect(page.locator(".conclusion")).toContainText(
    "核实并解决适用规范冲突",
  );
  await page
    .getByRole("button", { name: "查看待核实规范", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "批准当前版本", exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "阅读待核实规范", exact: true })
    .click();
  await expect(page.locator(".markdown")).toContainText("TODO 待确认");
  await expect(page.locator("main")).toContainText("影响当前任务的待核实规范");
  await shot(page, "A02-scoped-conflict-basis");
});

test("A03 分组分页、详情直达、返回保留筛选位置", async ({ page }) => {
  await goto(page, "many");
  await page.getByLabel("搜索任务").fill("分页任务");
  await expect(page.locator("[data-board=pending] .task-row")).toHaveCount(20);
  await page
    .getByRole("navigation", { name: "待处理分页" })
    .getByRole("button", { name: "下一页" })
    .click();
  await expect(page).toHaveURL(/p_pending=20/);
  await expect(
    page.getByRole("navigation", { name: "待处理分页" }),
  ).toContainText("第 2 / 3 页");
  const link = page.locator(".task-row").nth(8);
  await link.scrollIntoViewIfNeeded();
  const name = await link.locator("h3").innerText();
  const y = await page.evaluate(() => window.scrollY);
  await link.locator(".task-title-link").click();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  await expect(
    page.getByRole("navigation", { name: "任务详情分区" }).getByRole("button"),
  ).toHaveCount(4);
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  await page.goBack();
  await expect(page.getByLabel("搜索任务")).toHaveValue("分页任务");
  await expect(page).toHaveURL(/p_pending=20/);
  await expect(page.locator(".task-row")).toHaveCount(20);
  await expect
    .poll(() => page.evaluate(() => window.scrollY))
    .toBeGreaterThan(Math.max(0, y - 100));
  await shot(page, "A03-returned-page-two");
  await page.goForward();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();

  await goto(page, "many", "/tasks?days=all");
  await page
    .getByRole("navigation", { name: "任务分组定位" })
    .getByRole("link", { name: /^已完成/ })
    .click();
  await expect(page.locator("[data-board=completed] .task-row")).toHaveCount(3);
  await page.locator("[data-board=completed] .task-title-link").first().click();
  await expect(
    page.getByRole("navigation", { name: "任务详情分区" }),
  ).toBeVisible();
  await page.goBack();
  await expect(page.locator("[data-board=completed] .task-row")).toHaveCount(3);
});

test("A04 单项失败与未执行分开，按需查看真实证据", async ({ page }) => {
  await goto(page, "many", "/tasks/failed-check?section=checks");
  await expect(page.locator(".check")).toHaveCount(2);
  await expect(page.locator(".detail")).toContainText("失败 1");
  await expect(
    page.locator(".check").filter({ hasText: "not-run" }),
  ).toContainText("未执行");
  const failed = page.locator(".check").filter({ hasText: "failed ·" });
  await failed.getByRole("button", { name: "查看检查依据与证据" }).click();
  await expect(failed.locator(".evidence")).toContainText("失败");
  await failed
    .getByRole("button", { name: /查看证据/ })
    .first()
    .click();
  await expect(page.locator(".artifact-panel pre")).toBeVisible();
  await shot(page, "A04-failed-evidence");
});

test("A05 千条资料：分类搜索分页与正文懒加载", async ({ page, request }) => {
  const client = await connect(request);
  const p = await client.get("/api/knowledge-page?view=history&limit=25");
  expect(p.total).toBeGreaterThanOrEqual(1000);
  expect(p.items).toHaveLength(25);
  expect(p.items.every((x: any) => !("body" in x))).toBeTruthy();
  let bodyRequests = 0;
  page.on("request", (r) => {
    if (r.url().includes("/api/knowledge-record?")) bodyRequests++;
  });
  await goto(page, "many", "/knowledge?view=history");
  await expect(page.locator(".knowledge-scopes")).toContainText("按适用范围");
  await page.getByText("更多范围", { exact: true }).click();
  await page.getByLabel("适用范围").selectOption("archive");
  await expect(page.locator(".knowledge")).toHaveCount(25);
  expect(bodyRequests).toBe(0);
  await expect(page.locator(".markdown")).toHaveCount(0);
  await page
    .getByRole("navigation", { name: "资料分页" })
    .getByRole("button", { name: "下一页" })
    .click();
  await expect(page).toHaveURL(/offset=25/);
  await expect(page.locator(".knowledge")).toHaveCount(25);
  expect(bodyRequests).toBe(0);
  await page.getByLabel("搜索全部资料").fill("needle-0999");
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(page.locator(".knowledge")).toHaveCount(1);
  await expect(page.locator(".knowledge")).toContainText("历史资料");
  await page.locator(".knowledge a").click();
  await expect(page.locator(".markdown")).toContainText("needle-0999");
  expect(bodyRequests).toBe(1);
  await shot(page, "A05-knowledge-lazy-reader");
});

test("A06 状态不改写、Markdown、真实链接、来源与替代关系", async ({
  page,
  request,
}) => {
  const client = await connect(request);
  expect(
    (await client.get("/api/knowledge-record?id=current-rule")).status,
  ).toBe("current");
  expect(
    (await client.get("/api/knowledge-record?id=uncertain-rule")).status,
  ).toBe("conflict");
  await goto(page, "many", "/knowledge/current-rule");
  await expect(page.locator(".markdown table")).toBeVisible();
  await expect(page.locator(".markdown pre code")).toBeVisible();
  await expect(
    page.getByRole("navigation", { name: "正文目录" }),
  ).toBeVisible();
  await expect(page.locator(".reader-layout-right .reader-sidebar")).toContainText("关联任务与后继");
  await expect(page.locator(".markdown script")).toHaveCount(0);
  await expect(page.locator(".markdown")).toContainText("<script>");
  await expect(page.locator('.markdown a[href^="javascript:"]')).toHaveCount(0);
  await page.getByText("来源与版本", { exact: true }).click();
  await expect(page.locator(".source-details")).toContainText(
    ".ael/knowledge/current.md",
  );
  await shot(page, "A06-markdown-source");
  await page.getByRole("button", { name: "正文", exact: true }).click();
  await page
    .locator(".markdown")
    .getByRole("link", { name: "读取决策" })
    .click();
  await expect(page).toHaveURL(/knowledge\/local-choice/);
  await expect(page.locator(".markdown")).toContainText("已决定不等于已发布");
  await page.goBack();
  await page
    .locator(".markdown")
    .getByRole("link", { name: "未接入资料" })
    .click();
  await expect(page.getByRole("alert")).toContainText("目标文档未接入");
  await page
    .locator(".markdown")
    .getByRole("link", { name: "越界链接" })
    .click();
  await expect(page).toHaveURL(/knowledge\/current-rule/);
  await goto(page, "many", "/knowledge/old-rule");
  await page.getByRole("link", { name: "查看后继资料" }).click();
  await expect(page).toHaveURL(/knowledge\/current-rule/);
});

test("A07 大量观察事件仍可诊断，真实变化不会被淹没", async ({
  page,
  request,
}) => {
  const client = await connect(request);
  const events = await client.get("/api/timeline?limit=200&offset=0");
  expect(events.length).toBe(200);
  const observed = await client.get("/api/timeline?limit=200&offset=600");
  expect(
    observed.some((e: any) => e.kind === "knowledge_observed"),
  ).toBeTruthy();
  const changes = await client.get("/api/evolution?limit=100");
  expect(
    changes.items.some((c: any) => c.id === "task:verified-change"),
  ).toBeTruthy();
  expect(
    changes.items.every(
      (c: any) => !c.id.includes("observed") && !c.id.includes("migration:"),
    ),
  ).toBeTruthy();
  expect(changes.total).toBeLessThan(20);
  await goto(page, "many", "/evolution");
  await expect(page.locator(".change-row")).toHaveCount(changes.total);
  await expect(page.locator(".evolution-aside")).toContainText("让变化留下理由");
  await expect(page.locator("main")).not.toContainText("knowledge_observed");
  await shot(page, "A07-project-evolution");
  await page.getByRole("link", { name: "查看原始项目事件诊断" }).click();
  await expect(
    page.getByRole("heading", { name: "原始项目事件", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".settings-diagnostics .record-row")).toHaveCount(
    25,
  );
});

test("A08 显式多任务、规范和决策关系汇总且可往返", async ({
  page,
  request,
}) => {
  const client = await connect(request);
  const p = await client.get("/api/evolution?limit=100");
  expect(
    p.items.filter((c: any) => c.id === "knowledge:local-choice"),
  ).toHaveLength(1);
  expect(
    p.items.some(
      (c: any) => c.id === "task:change-a" || c.id === "task:change-b",
    ),
  ).toBeFalsy();
  await goto(page, "many", "/evolution/knowledge%3Alocal-choice");
  await expect(page.locator(".change-comparison")).toContainText("未单独记录");
  await expect(
    page.getByRole("link", {
      name: "隔离夹具：真实隔离交付 change-a",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "隔离夹具：当前交付规范", exact: true }),
  ).toBeVisible();
  await shot(page, "A08-explicit-relations");
  await page
    .getByRole("link", { name: "隔离夹具：真实隔离交付 change-a", exact: true })
    .click();
  await expect(page.getByRole("heading", { level: 1 })).toContainText(
    "change-a",
  );
  await page.getByRole("button", { name: "检查与验收", exact: true }).click();
  await expect(page.locator(".check .status")).toHaveText("通过");
  await expect(page.locator(".stage-track li").filter({ hasText: "执行验收" })).toHaveClass(/done/);
  await page.goBack();
  await expect(page).toHaveURL(/evolution\/knowledge%3Alocal-choice/);
});

test("A09 日期未知、实现未发布、决定与否决均保持原事实", async ({
  page,
  request,
}) => {
  const client = await connect(request);
  const unknown = await client.get(
    "/api/evolution?id=knowledge%3Arejected-choice",
  );
  expect(unknown.occurred_at).toBeNull();
  expect(unknown.result).toBe("rejected");
  const realized = await client.get(
    "/api/evolution?id=task%3Aimplemented-only",
  );
  expect(realized.result).toBe("implemented");
  await goto(page, "many", "/evolution");
  await expect(
    page.getByRole("heading", { name: "日期未确认", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".change-row").filter({ hasText: "已实现但尚未发布" }),
  ).toContainText("已实现 · 尚无发布依据");
  await expect(
    page.locator(".change-row").filter({ hasText: "本地结果与回归交付" }),
  ).toContainText("已决定 · 未据此认定实现");
  await page
    .getByRole("link", { name: "隔离夹具：否决远程索引方案", exact: true })
    .click();
  await expect(page.locator("main")).toContainText("离线验收不能依赖远程服务");
  await expect(page.locator("main")).toContainText("日期未确认");
  await shot(page, "A09-rejected-undated");
  await goto(page, "many", "/evolution");
  await page.getByRole("navigation", { name: "项目变化分类" }).getByRole("button", { name: "决策与取舍" }).click();
  await page.getByText("更多筛选", { exact: true }).click();
  await page.getByLabel("发生于 / 从").fill("2026-10-20");
  await page.getByLabel("至", { exact: true }).fill("2026-10-20");
  await expect(page.locator(".change-row")).toHaveCount(1);
  await expect(page.locator(".change-row")).toContainText("本地结果与回归交付");
  await expect(page.locator(".change-row")).toContainText("已验证");
});

test("A10 大量迁移告警先汇总，当前阻断与历史可分页定位", async ({
  page,
  request,
}) => {
  const batch = projects.many.warning_batch;
  const client = await connect(request);
  const v = await client.get("/api/migration-view?id=" + batch);
  expect(
    v.groups.find((g: any) => g.category === "history").count,
  ).toBeGreaterThanOrEqual(180);
  await goto(page, "many", "/settings?section=migrations&batch=" + batch);
  await expect(
    page.getByRole("heading", { name: /阻断当前工作/ }),
  ).toBeVisible();
  await expect(page.locator("main")).toContainText("当前规范冲突阻断的工作");
  await expect(page.locator("main")).not.toContainText("unresolved link:");
  await shot(page, "A10-migration-summary");
  const history = page
    .locator(".warning-group")
    .filter({ hasText: "历史资料告警" });
  await history.getByRole("button", { name: "查看此类记录" }).click();
  await expect(page.locator("main>.record-row")).toHaveCount(0);
  await expect(
    page.getByRole("navigation", { name: "迁移告警分页" }),
  ).toContainText("共 180 项");
  await page
    .getByRole("navigation", { name: "迁移告警分页" })
    .getByRole("button", { name: "下一页" })
    .click();
  await expect(page).toHaveURL(/offset=25/);
  const raw = page.getByText("来源与原始告警", { exact: true }).first();
  await raw.click();
  await expect(page.locator("details[open] pre").first()).toContainText(
    "unresolved link:",
  );
  await shot(page, "A10-history-warning-detail");
});

test("A11 断线恢复、真实空、后端错误与快速搜索无旧响应覆盖", async ({
  page,
}) => {
  await goto(page, "many");
  await expect(page.locator(".task-row").first()).toBeVisible();
  await page.route("**/api/tasks?**", (r) => r.abort());
  await expect(page.getByRole("alert")).toBeVisible({ timeout: 12000 });
  await expect(page.getByRole("alert")).toContainText("最后成功数据");
  await shot(page, "A11-disconnected");
  await page.unroute("**/api/tasks?**");
  await page.getByRole("button", { name: "重试", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.route("**/api/tasks?**", async (route) => {
    if (new URL(route.request().url()).searchParams.get("q") === "分页任务") {
      const response = await route.fetch();
      await new Promise((r) => setTimeout(r, 1200));
      await route.fulfill({ response });
    } else await route.continue();
  });
  const first = page.waitForRequest(
    (r) =>
      r.url().includes("/api/tasks?") &&
      new URL(r.url()).searchParams.get("q") === "分页任务",
  );
  await page.getByLabel("搜索任务").fill("分页任务");
  await first;
  await page.getByLabel("搜索任务").fill("等待环境标记");
  await expect(page.locator(".task-row")).toHaveCount(1);
  await page.waitForTimeout(1500);
  await expect(page.locator(".task-row")).toHaveCount(1);
  await expect(page.locator(".task-row")).toContainText("等待环境标记");
  await page.unroute("**/api/tasks?**");
  await goto(page, "empty");
  await expect(page.locator("[data-board]")).toHaveCount(0);
  await expect(page.locator(".task-row")).toHaveCount(0);
  await expect(page.locator("main")).toContainText("当前分组暂无工作");
  await goto(page, "empty", "/evolution");
  await expect(
    page.getByRole("heading", { name: "暂无可追溯的项目变化" }),
  ).toBeVisible();
  await shot(page, "A11-empty-evolution");
  await goto(page, "many", "/knowledge/nonexistent");
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("资料不存在");
});

test("A12a 创建、接手、暂缓、恢复、检查、取消与删除均写入隔离库", async ({
  page,
  request,
}) => {
  await goto(page, "single");
  await page.getByRole("link", { name: "＋ 创建任务" }).click();
  const title =
    "浏览器隔离闭环 " + Date.now() + " <img src=x onerror=alert(1)>";
  await page.getByLabel("标题", { exact: true }).fill(title);
  await page.getByLabel("影响路径（逗号分隔）").fill("app");
  await page.getByLabel("验收条件（每行一条）").fill("输出文件真实存在");
  await page.getByText("配置检查与验收映射", { exact: true }).click();
  await page.getByLabel("检查定义 JSON").fill(
    JSON.stringify([
      {
        id: "file",
        kind: "functional",
        method: "command",
        command: ["sh", "-c", "test -f app/ui-output.txt"],
        covers: ["输出文件真实存在"],
        required: true,
      },
    ]),
  );
  await page.getByRole("button", { name: "创建并生成计划" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: title }),
  ).toBeVisible();
  await expect(page.locator("h1 img")).toHaveCount(0);
  await page.locator(".task-more > summary").click();
  await page.getByRole("button", { name: "暂缓", exact: true }).click();
  await confirmDialog(page, "隔离浏览器暂停验证");
  await expect(page.locator(".conclusion")).toContainText("隔离浏览器暂停验证");
  await page.locator(".next-panel").getByRole("button", { name: "恢复任务", exact: true }).click();
  await page.getByRole("dialog").getByRole("checkbox").check();
  await confirmDialog(page);
  await expect(
    page.getByRole("button", { name: "接手当前工作", exact: true }),
  ).toBeVisible();
  for (const phase of ["define", "execute"]) {
    await page
      .getByRole("button", { name: "接手当前工作", exact: true })
      .click();
    await confirmDialog(page);
    await page.getByLabel("实际工作与结果").fill("浏览器真实操作 " + phase);
    if (phase === "execute") {
      fs.mkdirSync(path.join(projects.single.root, "app"), { recursive: true });
      fs.writeFileSync(
        path.join(projects.single.root, "app/ui-output.txt"),
        "isolated browser output\n",
      );
      await page.screenshot({
        path: path.join(projects.single.root, "app/ui-evidence.png"),
      });
      await page
        .getByLabel("本次产物（逗号分隔）")
        .fill("app/ui-output.txt,app/ui-evidence.png");
      await page
        .getByLabel("规范处理依据 / 阻塞解除条件")
        .fill("无适用规范变化；真实输出仅存在于隔离目录");
    }
    await page.getByRole("button", { name: "提交并查看下一步" }).click();
    await expect(
      page.getByRole("heading", { name: "提交本次工作结果" }),
    ).toHaveCount(0);
  }
  await page.getByRole("button", { name: "查看验收与运行检查" }).click();
  await page.getByRole("button", { name: "运行真实检查" }).click();
  await confirmDialog(page);
  await expect(page.locator(".check .status")).toHaveText("通过");
  await page.getByRole("button", { name: "执行过程", exact: true }).click();
  await page
    .getByRole("button", { name: "查看产物 app/ui-evidence.png", exact: true })
    .click();
  await expect(
    page.getByRole("img", { name: "任务验收证据原件" }),
  ).toBeVisible();
  await shot(page, "A12-real-check-passed");
  const client = await connect(request, "single");
  const id = "cancel-ui-" + Date.now();
  await client.post("/api/tasks", {
    id,
    title: "隔离取消与删除",
    type: "docs",
    goal: "analysis",
    acceptance: ["保留取消理由"],
  });
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".task-more > summary").click();
  await page.getByRole("button", { name: "取消任务", exact: true }).click();
  await confirmDialog(page, "浏览器隔离取消记录");
  await expect(page.locator(".conclusion")).toContainText("浏览器隔离取消记录");
  await page.getByRole("button", { name: "执行过程", exact: true }).click();
  await page.getByText("技术记录", { exact: true }).click();
  await page.getByRole("button", { name: "永久删除任务", exact: true }).click();
  await confirmDialog(page, "只删除此次可丢弃夹具");
  await expect(page).toHaveURL(/#\/tasks$/);
  await page.reload();
  await expect(
    page.locator(".task-row").filter({ hasText: "隔离取消与删除" }),
  ).toHaveCount(0);
  const recoveryId = "return-ui-" + Date.now();
  await client.post("/api/tasks", {
    id: recoveryId,
    title: "隔离返回实施复验",
    type: "bug",
    goal: "verified",
    scope: ["app"],
    acceptance: ["输出可复验"],
    checks: [
      {
        id: "file",
        kind: "functional",
        method: "command",
        command: ["sh", "-c", "test -f app/ui-output.txt"],
        covers: ["输出可复验"],
        required: true,
      },
    ],
  });
  await step(client, recoveryId);
  await step(client, recoveryId, ["app/ui-output.txt"]);
  await client.post("/api/action", {
    action: "pause",
    id: recoveryId,
    reason: "隔离复验需要回到实施",
  });
  await goto(page, "single", "/tasks/" + recoveryId);
  await page.locator(".next-panel").getByRole("button", { name: "恢复任务", exact: true }).click();
  await page.getByRole("dialog").getByLabel("恢复位置").selectOption("execute");
  await page.getByRole("dialog").getByRole("checkbox").check();
  await confirmDialog(page);
  expect((await client.get("/api/task?id=" + recoveryId)).current_step).toBe(
    "execute",
  );
  const longId = "long-check-" + Date.now();
  await client.post("/api/tasks", {
    id: longId,
    title: "隔离长检查等待真实回执",
    type: "bug",
    goal: "verified",
    scope: ["app"],
    acceptance: ["超过二十秒后仍以真实文件检查为准"],
    checks: [
      {
        id: "long",
        kind: "functional",
        method: "command",
        command: ["sh", "-c", "sleep 21; test -f app/ui-output.txt"],
        covers: ["超过二十秒后仍以真实文件检查为准"],
        required: true,
      },
    ],
  });
  await step(client, longId);
  await step(client, longId, ["app/ui-output.txt"]);
  await goto(page, "single", "/tasks/" + longId + "?section=checks");
  await page.getByRole("button", { name: "运行真实检查", exact: true }).click();
  const longStarted = Date.now();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "确认运行检查" })
    .click();
  await expect
    .poll(async () => (await client.get("/api/task?id=" + longId)).board)
    .toBe("active");
  await expect(page.getByRole("dialog")).toBeHidden({ timeout: 30000 });
  expect(Date.now() - longStarted).toBeGreaterThan(20000);
  await expect(page.locator(".check .status")).toHaveText("通过");
  await shot(page, "A12-long-check-receipt");
});

test("A12b 精确版本确认、过期失败保留输入与重新审阅", async ({
  page,
  request,
}) => {
  const client = await connect(request, "single");
  const id = "confirm-ui-" + Date.now();
  await client.post("/api/tasks", {
    id,
    title: "隔离准确版本确认",
    type: "feature",
    goal: "verified",
    preset: "strict",
    acceptance: ["准确修订的决定"],
  });
  await step(client, id);
  await step(client, id);
  await goto(page, "single", "/tasks/" + id + "?section=basis");
  await page.getByRole("button", { name: "批准当前版本", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("操作理由").fill("保留这份已经输入的准确审阅结论");
  const n = await client.get("/api/task?id=" + id),
    c = n.task.confirmation;
  await client.post("/api/action", {
    action: "confirm",
    id,
    kind: c.kind,
    decision: "adjust",
    executor: "independent-fixture-reviewer",
    reason: "独立夹具要求新修订",
    candidate: c.candidate,
    revision: c.definition_revision,
    plan_revision: c.plan_revision,
  });
  await dialog.getByRole("button", { name: "确认批准当前版本" }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  await expect(dialog.getByRole("alert")).toContainText("确认版本已变化");
  await expect(dialog.getByLabel("操作理由")).toHaveValue(
    "保留这份已经输入的准确审阅结论",
  );
  await shot(page, "A12-stale-confirmation-error");
  await dialog.getByRole("button", { name: "返回", exact: true }).click();
  await step(client, id);
  await page.reload();
  await page.getByRole("button", { name: "批准当前版本", exact: true }).click();
  await confirmDialog(page, "审阅新修订后批准");
  await expect(
    page.getByRole("heading", { name: "需要你的决定", exact: true }),
  ).toHaveCount(0);
  const after = await client.get("/api/task?id=" + id);
  expect(after.task.approvals.at(-1).decision).toBe("approve");
  expect(after.task.approvals.at(-1).plan_revision).toBe(2);
});

test("A12c 迁移真实预览、错误摘要、应用防重复、核验修复回滚", async ({
  page,
  request,
}) => {
  await goto(page, "migration", "/settings?section=migrations");
  await page.getByRole("button", { name: "扫描与预览", exact: true }).click();
  await confirmDialog(page);
  await expect(page.getByRole("heading", { name: /待审阅计划/ })).toBeVisible();
  await expect(
    page.getByRole("button", { name: "批准此计划并导入" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "审阅来源原文" }).first().click();
  await expect(page.locator(".preview .markdown")).toBeVisible();
  const client = await connect(request, "migration"),
    preview = await client.get("/api/migration-view?id=preview");
  const bad = await client.post(
    "/api/migrations",
    { action: "apply", approved_digest: "incorrect" },
    false,
  );
  expect(bad.response.status()).toBe(409);
  await page.getByLabel("审阅后填写完整确认摘要").fill(preview.digest);
  await page.getByRole("button", { name: "批准此计划并导入" }).click();
  let applies = 0;
  page.on("request", (r) => {
    if (r.method() === "POST" && r.postDataJSON()?.action === "apply")
      applies++;
  });
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "确认批准并导入当前计划" })
    .dblclick();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(applies).toBe(1);
  await expect(
    page.getByRole("heading", { name: /迁移批次 · 导入已应用/ }),
  ).toBeVisible();
  await page.getByRole("button", { name: "核验迁移", exact: true }).click();
  await confirmDialog(page);
  await expect(
    page.getByRole("heading", { name: "本次核验通过", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "修复中断", exact: true }).click();
  await confirmDialog(page);
  await shot(page, "A12-migration-verified");
  await page.getByRole("button", { name: "回滚本次导入", exact: true }).click();
  await confirmDialog(page);
  await expect(
    page.getByRole("heading", { name: /迁移批次 · 已回滚/ }),
  ).toBeVisible();
});

test("A13 深路由刷新、键盘焦点和多尺寸无全页溢出", async ({
  page,
  context,
}) => {
  await goto(page, "many", "/tasks/failed-check");
  await page.locator(".task-more > summary").click();
  await page.getByRole("button", { name: "暂缓", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(
    page.locator(".task-more > summary"),
  ).toBeFocused();
  for (const viewport of [
    { width: 1480, height: 1000 },
    { width: 1280, height: 900 },
    { width: 768, height: 900 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    for (const route of [
      "/tasks?q=较长标题",
      "/knowledge/current-rule",
      "/evolution/knowledge%3Alocal-choice",
      "/settings?section=sources",
      "/settings",
      "/settings?section=diagnostics",
    ]) {
      await goto(page, "many", route);
      await expect(page.locator("main h1").first()).toBeVisible();
      await page.reload();
      await expect(page.locator("main h1").first()).toBeVisible();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBeTruthy();
    }
    await shot(page, "A13-diagnostics-" + viewport.width);
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await goto(page, "many");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "跳到主要内容" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("main")).toBeFocused();
  await expect(page).not.toHaveURL(/main-content/);
  await goto(page, "many", "/settings?section=sources");
  await page.getByText("来源路径与读取诊断", { exact: true }).first().click();
  await expect(page.locator("details[open]")).toContainText("来源：");
  await goto(page, "many", "/settings");
  await page.getByRole("button", { name: "探测受管执行器接口" }).click();
  await expect(page.getByText("探测详情", { exact: true })).toBeVisible({
    timeout: 15000,
  });
  await page.getByText("探测详情", { exact: true }).click();
  await expect(page.locator("details[open] pre")).toContainText("state");
  await shot(page, "A13-project-capabilities");
  await goto(page, "many", "/settings?section=diagnostics");
  await page.getByRole("button", { name: "复制", exact: true }).first().click();
  await expect(page.getByLabel("可手动复制的内容")).toBeVisible();
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.getByRole("button", { name: "复制", exact: true }).first().click();
  await expect(page.getByRole("status")).toContainText("已复制");
});

test("A11b 阅读重试与异步对象隔离，未提供依据不冒充已使用版本", async ({
  page,
  request,
}) => {
  await goto(page, "many", "/tasks/verified-change?section=execution");
  const outputButton = page.getByRole("button", {
    name: "查看产物 app/verified-change.txt",
    exact: true,
  });
  await page.route("**/api/artifact?**", (r) => r.abort());
  await outputButton.click();
  await expect(
    page.locator(".artifact-panel").getByRole("alert"),
  ).toBeVisible();
  await page.unroute("**/api/artifact?**");
  await page
    .locator(".artifact-panel")
    .getByRole("button", { name: "重试", exact: true })
    .click();
  await expect(page.locator(".artifact-panel pre")).toContainText(
    "real isolated implementation output",
  );
  await page.route("**/api/artifact?**", async (r) => {
    const response = await r.fetch();
    if (
      new URL(r.request().url()).searchParams.get("ref") ===
      "app/verified-change.txt"
    )
      await new Promise((resolve) => setTimeout(resolve, 700));
    await r.fulfill({ response });
  });
  const started = page.waitForRequest(
    (r) =>
      r.url().includes("/api/artifact?") &&
      r.url().includes("verified-change.txt"),
  );
  await outputButton.click();
  await started;
  await page
    .getByRole("button", { name: /^查看产物 runtime:/ })
    .first()
    .click();
  await expect(page.locator(".artifact-panel>p")).toContainText("runtime:");
  await page.waitForTimeout(850);
  await expect(page.locator(".artifact-panel>p")).toContainText("runtime:");
  await page.unroute("**/api/artifact?**");
  await goto(page, "many", "/tasks/verified-change?section=basis");
  await page.route("**/api/knowledge-record?**", async (r) => {
    const response = await r.fetch();
    await new Promise((resolve) => setTimeout(resolve, 700));
    await r.fulfill({ response });
  });
  const reading = page.waitForRequest((r) =>
    r.url().includes("/api/knowledge-record?"),
  );
  await page.getByRole("button", { name: "阅读实际提供版本" }).first().click();
  await reading;
  await goto(page, "many", "/tasks/pending-00?section=basis");
  await page.waitForTimeout(850);
  await expect(page.locator(".markdown")).toHaveCount(0);
  await page.unroute("**/api/knowledge-record?**");
  const client = await connect(request);
  const id = "explicit-unprovided-" + Date.now();
  await client.post("/api/tasks", {
    id,
    title: "隔离显式引用尚未提供",
    type: "docs",
    goal: "analysis",
    scope: ["app"],
    knowledge: ["current-rule"],
    acceptance: ["保持依据版本真实性"],
  });
  await goto(page, "many", "/tasks/" + id + "?section=basis");
  await page.getByRole("button", { name: "阅读当前显式引用" }).click();
  await expect(page.locator("main")).toContainText(
    "尚未向本任务执行者提供，不能当作已使用的依据",
  );
  await expect(
    page.getByText("此处是任务实际提供版本", { exact: false }),
  ).toHaveCount(0);
  await shot(page, "A11-unprovided-basis");
});

test("A12d 领取回执成功而上下文失败时只重试读取，不重复领取", async ({
  page,
  request,
}) => {
  const client = await connect(request, "single"),
    id = "context-retry-" + Date.now();
  await client.post("/api/tasks", {
    id,
    title: "隔离上下文读取重试",
    type: "bug",
    goal: "verified",
    acceptance: ["领取与读取失败分别呈现"],
  });
  await goto(page, "single", "/tasks/" + id);
  let claims = 0;
  page.on("request", (r) => {
    if (
      r.method() === "POST" &&
      r.url().includes("/api/action") &&
      r.postDataJSON()?.action === "claim"
    )
      claims++;
  });
  await page.route("**/api/context?**", (r) => r.abort());
  await page.getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  await expect(page.locator(".work-form").getByRole("alert")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "提交并查看下一步" }),
  ).toBeDisabled();
  expect(claims).toBe(1);
  await page.unroute("**/api/context?**");
  await page
    .locator(".work-form")
    .getByRole("button", { name: "重试", exact: true })
    .click();
  await expect(page.locator(".work-form").getByRole("alert")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "提交并查看下一步" }),
  ).toBeEnabled();
  expect(claims).toBe(1);
  await client.post("/api/action", { action: "resume", id, ack_stopped: true });
});

test("A12e 原有交付节点保留准确授权与真实回执，发布事实可追溯", async ({
  page,
  request,
}) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"),
    id = "delivery-ui-" + Date.now();
  await client.post("/api/tasks", {
    id,
    title: "隔离本地交付回执",
    type: "bug",
    goal: "released",
    environment: "fixture-local-ui",
    scope: ["app"],
    acceptance: ["本地输出真实存在"],
    checks: [
      {
        id: "file",
        kind: "functional",
        method: "command",
        command: ["sh", "-c", "test -f app/ui-output.txt"],
        covers: ["本地输出真实存在"],
        required: true,
      },
    ],
  });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  await client.post("/api/action", { action: "verify", id, unit: "file" });
  await goto(page, "single", "/tasks/" + id + "?section=basis");
  await page.getByRole("button", { name: "批准当前版本", exact: true }).click();
  await confirmDialog(page, "只授权隔离目录内的实际复制，不授权生产发布");
  await page.getByRole("button", { name: "概览", exact: true }).click();
  await page.getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  const n = await client.get("/api/task?id=" + id),
    destination = path.join(fixture.out, "local-delivery-ui");
  fs.mkdirSync(destination, { recursive: true });
  fs.copyFileSync(
    path.join(projects.single.root, "app/ui-output.txt"),
    path.join(destination, "ui-output.txt"),
  );
  expect(fs.readFileSync(path.join(destination, "ui-output.txt"), "utf8")).toBe(
    fs.readFileSync(
      path.join(projects.single.root, "app/ui-output.txt"),
      "utf8",
    ),
  );
  const receipt = {
    run_id: "isolated-ui-copy",
    idempotency_key: id,
    candidate: n.task.candidate,
    artifact: "runtime:ui-delivery-receipt.json",
    environment: "fixture-local-ui",
    target: "",
    status: "success",
  };
  fs.writeFileSync(
    path.join(projects.single.data_dir, "ui-delivery-receipt.json"),
    JSON.stringify(receipt),
  );
  await page.getByLabel("真实交付回执引用").fill(receipt.artifact);
  await page.getByRole("button", { name: "读取交付回执" }).click();
  await expect(page.locator(".receipt-form")).toContainText("isolated-ui-copy");
  await page
    .getByLabel("实际工作与结果")
    .fill("已实际复制到隔离本地交付目录并比较字节内容");
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  await expect(page.locator(".conclusion")).toContainText(
    "已发布（有交付回执）",
  );
  const after = await client.get("/api/task?id=" + id);
  expect(after.task.result).toBe("released");
  expect(after.task.delivery.environment).toBe("fixture-local-ui");
  await shot(page, "A12-local-delivery-receipt");
});

test("A12f 人工通过明确绑定所审阅候选，过期提交不能批准新产物", async ({
  page,
  request,
}) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"),
    id = "human-exact-" + Date.now();
  await client.post("/api/tasks", {
    id,
    title: "隔离人工精确验收",
    type: "bug",
    goal: "verified",
    scope: ["app"],
    acceptance: ["人工明确审阅当前产物"],
    checks: [
      {
        id: "owner",
        kind: "review",
        method: "human",
        covers: ["人工明确审阅当前产物"],
        required: true,
      },
    ],
  });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  fs.writeFileSync(
    path.join(projects.single.data_dir, "human-review.md"),
    "Isolated UI human-review fixture, never a real business acceptance.",
  );
  await goto(page, "single", "/tasks/" + id + "?section=checks");
  await page
    .getByRole("button", { name: "记录人工验收", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog
    .getByLabel("实际观察与理由")
    .fill("明确通过已审阅的隔离产物");
  await dialog
    .getByLabel("补充已有证据引用")
    .fill("runtime:human-review.md");
  expect((await dialog.getByLabel("补充已有证据引用").boundingBox())!.width).toBeGreaterThan(300);
  await dialog.getByLabel("我确认以上结论针对所示候选和修订。").check();
  await shot(page, "A12-manual-review-form");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBeTruthy();
  await shot(page, "A12-manual-review-form-390");
  await page.setViewportSize({ width: 1440, height: 900 });
  await client.post("/api/action", {
    action: "resume",
    id,
    unit: "execute",
    ack_stopped: true,
  });
  await step(client, id, ["app/ui-output.txt"], () =>
    fs.writeFileSync(
      path.join(projects.single.root, "app/ui-output.txt"),
      "new isolated candidate for explicit human review " + id + "\n",
    ),
  );
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog.getByRole("alert")).toContainText("验收候选或修订已变化");
  expect((await client.get("/api/task?id=" + id)).task.evidence||[]).toHaveLength(
    0,
  );
  await shot(page, "A12-stale-human-review");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await page.reload();
  await page
    .getByRole("button", { name: "记录人工验收", exact: true })
    .click();
  await page.getByRole("dialog").getByRole("radio", { name: "通过", exact: true }).check();
  await page.getByRole("dialog").getByLabel("记录人").fill("isolated reviewer");
  await page
    .getByRole("dialog")
    .getByLabel("补充已有证据引用")
    .fill("runtime:human-review.md");
  await page
    .getByRole("dialog")
    .getByLabel("实际观察与理由")
    .fill("明确审阅新隔离产物并通过");
  await page.getByRole("dialog").getByLabel("我确认以上结论针对所示候选和修订。").check();
  await page.getByRole("dialog").getByRole("button", { name: "提交验收结果" }).click();
  await expect(page.getByRole("dialog")).toContainText("任务已完成");
  await shot(page, "A12-human-review-receipt");
  await page.getByRole("dialog").getByRole("button", { name: "关闭", exact: true }).click();
  await expect(page.locator(".check .status")).toHaveText("通过");
});

test("A12g 工作台旧方法入口、失败与受阻保留真实结果", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single");
  async function makeTask(suffix: string, method?: string) {
    const id = `human-${suffix}-${Date.now()}`;
    await client.post("/api/tasks", {
      id, title: `隔离人工验收 ${suffix}`, type: "bug", goal: "verified", scope: ["app"],
      acceptance: ["人工审阅实际产物"],
      checks: [{ id: "owner", kind: "review", ...(method ? { method } : {}), covers: ["人工审阅实际产物"], required: true }],
    });
    await step(client, id);
    await step(client, id, ["app/ui-output.txt"]);
    return id;
  }
  const failed = await makeTask("failure");
  await goto(page, "single", "/tasks?q=" + failed);
  const attention = page.locator(".attention-card").filter({ hasText: "需要你的验收" });
  await expect(attention).toBeVisible();
  await attention.getByRole("button", { name: "开始验收" }).click();
  let dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("radio", { name: "通过", exact: true })).not.toBeChecked();
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog.getByRole("alert")).toContainText("请选择验收结论");
  await dialog.getByRole("radio", { name: "不通过", exact: true }).check();
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("实际观察与理由").fill("真实隔离产物与检查标准不一致");
  await dialog.getByLabel("修复方向").fill("修正隔离产物后重新检查");
  await dialog.getByLabel("app/ui-output.txt").check();
  await dialog.getByLabel("我确认以上结论针对所示候选和修订。").check();
  await page.route("**/api/action", async (route) => {
    if (route.request().postDataJSON()?.action === "review_result") {
      const persisted = await route.fetch();
      expect(persisted.ok()).toBeTruthy();
      await route.abort();
    }
    else await route.continue();
  });
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog.getByRole("alert")).toContainText("无法取得最新数据");
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("真实隔离产物与检查标准不一致");
  await page.unroute("**/api/action");
  await expect(dialog.getByLabel("实际观察与理由")).toBeDisabled();
  await dialog.getByRole("button", { name: "原样重试提交" }).click();
  await expect(dialog).toContainText("任务仍有后续工作");
  await shot(page, "A12-human-failed-receipt");
  let current = await client.get("/api/task?id=" + failed);
  expect(current.next_action).toBe("repair");
  expect(current.task.evidence[0].result).toBe("fail");
  expect(current.task.evidence).toHaveLength(1);
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();

  const blocked = await makeTask("blocked", "human");
  await goto(page, "single", `/tasks/${blocked}?section=checks`);
  await page.getByRole("button", { name: "记录人工验收", exact: true }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: "暂无法验证", exact: true }).check();
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("实际观察与理由").fill("隔离环境尚未准备");
  await dialog.getByLabel("继续验证的条件").fill("准备隔离环境后重试");
  await dialog.getByLabel("我确认以上结论针对所示候选和修订。").check();
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog).toContainText("任务仍有后续工作");
  await shot(page, "A12-human-blocked-receipt");
  current = await client.get("/api/task?id=" + blocked);
  expect(current.next_action).toBe("wait_environment");
  expect(current.current_checks.owner.result).toBe("blocked");
  await client.post("/api/action", { action: "resume", id: blocked, ack_stopped: true });
  current = await client.get("/api/task?id=" + blocked);
  expect(current.current_checks.owner.result).toBe("blocked");
  expect(current.task.status).not.toBe("completed");
});

test("A15 各主页面和检查详情的四种视口", async ({ page }) => {
  const pages = [
    ["workbench", "/tasks"],
    ["create-task", "/tasks/new"],
    ["task-overview", "/tasks/failed-check"],
    ["checks", "/tasks/failed-check?section=checks"],
    ["execution", "/tasks/verified-change?section=execution"],
    ["knowledge-list", "/knowledge"],
    ["knowledge", "/knowledge/current-rule"],
    ["evolution-list", "/evolution"],
    ["evolution", "/evolution/knowledge%3Alocal-choice"],
    ["settings", "/settings"],
    ["settings-sources", "/settings?section=sources"],
    ["migration", "/settings?section=migrations"],
  ];
  for (const viewport of [{ width: 1480, height: 1000 }, { width: 1280, height: 900 }, { width: 768, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    for (const [name, route] of pages) {
      await goto(page, "many", route);
      await expect(page.locator("main h1").first()).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBeTruthy();
      await shot(page, `A15-${name}-${viewport.width}`);
    }
  }
});

test("A16 外观切换持久化且不改变任务状态", async ({ page }) => {
  await goto(page, "many");
  await page.getByRole("button", { name: "切换到石墨外观" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "graphite");
  await expect(page.locator(".page-header .primary")).toHaveCSS("background-color", "rgb(54, 66, 87)");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "graphite");
  await page.getByRole("button", { name: "切换到靛蓝外观" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "indigo");
  await goto(page, "many", "/settings");
  await page.getByRole("button", { name: "石墨 · 更克制" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "graphite");
  await expect(page.getByRole("button", { name: "切换到靛蓝外观" })).toBeVisible();
  await page.getByRole("button", { name: "靛蓝 · 默认" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "indigo");
});

test("A17 全局查找连接真实资料并可跳转", async ({ page }) => {
  await goto(page, "many");
  await page.keyboard.press("Control+k");
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("搜索任务和资料").fill("当前交付规范");
  await dialog.getByRole("button", { name: "查找", exact: true }).click();
  await dialog.getByRole("link", { name: /隔离夹具：当前交付规范/ }).click();
  await expect(page).toHaveURL(/knowledge\/current-rule/);
  await expect(page.getByRole("heading", { name: "隔离夹具：当前交付规范", level: 1 })).toBeVisible();
});

test("A19 顶栏刷新重新读取当前页面数据", async ({ page }) => {
  await goto(page, "many", "/knowledge");
  await expect(page.locator(".knowledge").first()).toBeVisible();
  const reload = page.waitForRequest((r) => r.url().includes("/api/knowledge-page?"));
  await page.getByRole("button", { name: "刷新项目状态", exact: true }).click();
  await reload;
  await expect(page.locator(".knowledge").first()).toBeVisible();
});

test("A20 本机回环服务可通过 localhost 访问", async ({ page }) => {
  const url = new URL(projects.many.url);
  url.hostname = "localhost";
  await page.goto(url.toString() + "/#/tasks");
  await expect(page.getByRole("heading", { name: "工作台", level: 1 })).toBeVisible();
  await expect(page.locator(".sidebar footer")).toContainText("v" + expectedVersion);
});

test("A18 方案调整和否决均记录决定而不取消任务", async ({ page, request }) => {
  const client = await connect(request, "single");
  for (const [decision, button] of [["adjust", "要求调整"], ["reject", "否决方案"]]) {
    const id = `decision-${decision}-${Date.now()}`;
    await client.post("/api/tasks", { id, title: `隔离方案 ${decision}`, type: "feature", goal: "verified", preset: "strict", acceptance: ["审阅准确方案"] });
    await step(client, id);
    await step(client, id);
    await goto(page, "single", `/tasks/${id}?section=basis`);
    await page.getByRole("button", { name: button, exact: true }).click();
    await confirmDialog(page, "隔离工程中明确记录本次方案决定");
    const current = await client.get("/api/task?id=" + id);
    expect(current.task.status).toBe("open");
    expect(current.task.approvals.at(-1).decision).toBe(decision);
    expect(current.task.plan.revision).toBe(2);
    expect(current.current_step).toBe("analysis");
    if (decision === "reject") await shot(page, "A18-rejected-plan");
  }
});

test("A14 实际项目入口使用内嵌离线资源及版本可追溯", async ({
  page,
  request,
}) => {
  const client = await connect(request);
  const info = await client.get("/api/info");
  expect(info.executable).toContain("/.ael/bin/");
  expect(info.service_build).toBeTruthy();
  expect(info.service_version).toBe(expectedVersion);
  if (fixture.build) expect(info.service_build).toBe(fixture.build);
  const requests: string[] = [];
  page.on("request", (r) => requests.push(r.url()));
  await page.route("**/*", (route) =>
    new URL(route.request().url()).hostname === "127.0.0.1"
      ? route.continue()
      : route.abort(),
  );
  await goto(page, "many");
  await expect(page.locator(".task-row").first()).toBeVisible();
  expect(
    requests.filter((u) => new URL(u).hostname !== "127.0.0.1"),
  ).toHaveLength(0);
  expect(requests.some((u) => /\/assets\/index-.*\.js/.test(u))).toBeTruthy();
  const cssURL = requests.find((u) => /\/assets\/index-.*\.css/.test(u));
  expect(cssURL).toBeTruthy();
  expect((await request.get(cssURL!)).headers()["cache-control"]).toContain("no-store");
  await expect(page.locator(".page-header .primary")).toHaveCSS("background-color", "rgb(86, 84, 217)");
  expect(requests.some((u) => u.includes("/@vite/"))).toBeFalsy();
  fs.writeFileSync(
    path.join(output, "embedded-version.json"),
    JSON.stringify(info, null, 2),
  );
  await shot(page, "A14-embedded-workbench");
});

test("A21 接手后刷新及返回仍可用原领取提交，其他标签页不会自动接手", async ({ page, request, context }) => {
  const client = await connect(request, "single"), id = "claim-reload-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离领取刷新恢复", type: "bug", goal: "verified", acceptance: ["领取后可继续提交"] });
  await goto(page, "single", "/tasks/" + id);
  let claims = 0;
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/action") && r.postDataJSON()?.action === "claim") claims++;
  });
  await page.getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  const work = page.getByLabel("实际工作与结果");
  await work.fill("刷新后仍保留的真实工作结果");
  const initial = await client.get("/api/task?id=" + id);
  const attemptId = initial.task.attempts.find((a: any) => a.status === "running").id;
  await page.reload();
  await expect(work).toHaveValue("刷新后仍保留的真实工作结果");
  await goto(page, "single");
  await goto(page, "single", "/tasks/" + id);
  await expect(work).toHaveValue("刷新后仍保留的真实工作结果");
  const other = await context.newPage();
  await goto(other, "single", "/tasks/" + id);
  await expect(other.getByRole("heading", { name: "提交本次工作结果" })).toHaveCount(0);
  await other.close();
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  await expect(page.getByRole("heading", { name: "提交本次工作结果" })).toHaveCount(0);
  const saved = await client.get("/api/task?id=" + id);
  expect(saved.task.attempts.filter((a: any) => a.id === attemptId && a.status === "pass")).toHaveLength(1);
  expect(claims).toBe(1);
});

test("A22 任务四个分区按参考结构呈现真实内容", async ({ page }) => {
  await page.setViewportSize({ width: 940, height: 1000 });
  await goto(page, "many", "/tasks/failed-check");
  await expect(page.getByRole("heading", { name: "目标与范围" })).toBeVisible();
  const order = await page.locator(".task-detail-main .overview").evaluate((el) => {
    const positions = [".result-banner", ".content-section", ".overview-card:first-child", ".overview-card:last-child"].map((selector) => el.querySelector(selector)!.getBoundingClientRect().top);
    return positions.every((value, index) => index === 0 || positions[index - 1] < value);
  });
  expect(order).toBeTruthy();
  await shot(page, "A22-overview-940");
  await page.getByRole("button", { name: "检查与验收", exact: true }).click();
  await expect(page.locator(".info-strip")).toContainText("定义修订");
  await expect(page.locator(".check-list-header")).toContainText("通过");
  await expect(page.locator(".check")).toHaveCount(2);
  expect(await page.locator(".next-panel").evaluate((el) => el.getBoundingClientRect().top)).toBeGreaterThan(await page.locator(".check-list").evaluate((el) => el.getBoundingClientRect().top));
  await shot(page, "A22-checks-940");
  await page.getByRole("button", { name: "执行过程", exact: true }).click();
  await expect(page.getByRole("heading", { name: "工作进展", exact: true })).toBeVisible();
  await expect(page.locator(".source-detail")).not.toHaveAttribute("open");
  await shot(page, "A22-execution-940");
  await goto(page, "many", "/tasks/verified-change?section=basis");
  await expect(page.locator(".info-strip")).toContainText("实际提供过的准确版本");
  await expect(page.locator(".task-basis-list .record-row").first()).toBeVisible();
  await shot(page, "A22-basis-940");
  await goto(page, "many", "/tasks/verified-change?section=technical");
  await expect(page.locator(".source-detail")).toHaveAttribute("open", "");
  for (const width of [1480, 1280, 768, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBeTruthy();
  }
});

test("A23 提交已保存但回执丢失时刷新后使用原请求重试", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "claim-retry-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离提交回执丢失", type: "bug", goal: "verified", acceptance: ["重复提交保持幂等"] });
  await goto(page, "single", "/tasks/" + id);
  await page.getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  await page.getByLabel("实际工作与结果").fill("结果已提交但回执被隔离测试丢弃");
  let first: any;
  await page.route("**/api/action", async (route) => {
    const body = route.request().postDataJSON();
    if (body?.action === "submit") { first = body; const response = await route.fetch(); expect(response.ok()).toBeTruthy(); await route.abort(); }
    else await route.continue();
  });
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  await expect(page.getByRole("button", { name: "原样重试提交" })).toBeVisible();
  await page.unroute("**/api/action");
  await page.reload();
  await expect(page.getByLabel("实际工作与结果")).toBeDisabled();
  let retry: any;
  page.on("request", (r) => { if (r.method() === "POST" && r.url().includes("/api/action") && r.postDataJSON()?.action === "submit") retry = r.postDataJSON(); });
  await page.getByRole("button", { name: "原样重试提交" }).click();
  await expect(page.getByRole("heading", { name: "提交本次工作结果" })).toHaveCount(0);
  expect(retry).toEqual(first);
  const saved = await client.get("/api/task?id=" + id);
  expect(saved.task.attempts.filter((a: any) => a.id === first.submission.attempt && a.status === "pass")).toHaveLength(1);
});

test("A24 凭据丢失的接手只有确认所有旧执行停止后才能恢复", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "claim-lost-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离丢失接手恢复", type: "bug", goal: "verified", acceptance: ["需要确认后恢复"] });
  await client.post("/api/action", { action: "claim", id, executor: "other-fixture-executor" });
  await goto(page, "single", "/tasks/" + id);
  await page.getByRole("button", { name: "恢复无法继续的接手" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("所有正在执行或中断的旧接手");
  await dialog.getByRole("button", { name: "确认恢复无法继续的接手" }).click();
  expect((await client.get("/api/task?id=" + id)).task.attempts[0].status).toBe("running");
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "确认恢复无法继续的接手" }).click();
  await expect(dialog).toBeHidden();
  expect((await client.get("/api/task?id=" + id)).task.attempts[0].status).toBe("abandoned");
  await expect(page.getByRole("button", { name: "接手当前工作", exact: true })).toBeVisible();
});

test("A25 产物不存在的明确拒绝允许修正后提交", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "claim-invalid-artifact-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离产物校验重试", type: "bug", goal: "verified", scope: ["app"], acceptance: ["使用真实产物"] });
  await step(client, id);
  await goto(page, "single", "/tasks/" + id);
  await page.getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  await page.getByLabel("实际工作与结果").fill("先校验缺失产物，再提交真实隔离产物");
  await page.getByLabel("本次产物（逗号分隔）").fill("app/no-such-output.txt");
  await page.getByLabel("规范处理依据 / 阻塞解除条件").fill("没有适用规范变化");
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  await expect(page.getByRole("alert").first()).toBeVisible();
  await expect(page.getByLabel("本次产物（逗号分隔）")).toBeEnabled();
  await page.getByLabel("本次产物（逗号分隔）").fill("app/ui-output.txt");
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  await expect(page.getByRole("heading", { name: "提交本次工作结果" })).toHaveCount(0);
  expect((await client.get("/api/task?id=" + id)).current_step).toBe("verify");
});

test("A26 工作台任务行可直接进入符合条件的人工验收", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "human-row-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离工作台验收入口", type: "bug", goal: "verified", scope: ["app"], acceptance: ["人工核验产物"], checks: [{ id: "owner", kind: "review", method: "human", covers: ["人工核验产物"], required: true }] });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  await goto(page, "single", "/tasks?q=" + id);
  const row = page.locator(".task-row").filter({ hasText: id });
  await expect(row.getByRole("button", { name: "验收", exact: true })).toBeVisible();
  await row.getByRole("button", { name: "验收", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "记录人工验收" })).toBeVisible();
  await expect(page.getByRole("dialog")).toContainText("人工核验产物");
  await page.getByRole("dialog").getByRole("button", { name: "取消" }).click();
});

test("A27 提交响应返回前切换任务不会误清另一任务状态", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "submit-navigation-" + Date.now(), other = id + "-other";
  await client.post("/api/tasks", { id, title: "隔离提交切换来源", type: "bug", goal: "verified", acceptance: ["提交回执保留原任务归属"] });
  await client.post("/api/tasks", { id: other, title: "隔离提交切换目标", type: "bug", goal: "verified", acceptance: ["另一任务不受影响"] });
  await goto(page, "single", "/tasks/" + id);
  await page.getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  await page.getByLabel("实际工作与结果").fill("隔离任务响应期间切换页面");
  let release!: () => void, markAccepted!: () => void;
  const gate = new Promise<void>((resolve) => release = resolve), accepted = new Promise<void>((resolve) => markAccepted = resolve);
  await page.route("**/api/action", async (route) => {
    if (route.request().postDataJSON()?.action === "submit") {
      const response = await route.fetch();
      expect(response.ok()).toBeTruthy();
      markAccepted();
      await gate;
      await route.fulfill({ response });
    } else await route.continue();
  });
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  await accepted;
  await page.evaluate((nextId) => { location.hash = "#/tasks/" + nextId; }, other);
  await expect(page.getByRole("heading", { level: 1, name: "隔离提交切换目标" })).toBeVisible();
  release();
  await expect(page.getByText("工作结果已提交，请在对应任务查看下一步。", { exact: false })).toBeVisible();
  await page.unroute("**/api/action");
  await goto(page, "single", "/tasks/" + id);
  await expect(page.getByRole("heading", { name: "提交本次工作结果" })).toHaveCount(0);
  expect((await client.get("/api/task?id=" + id)).task.attempts.filter((a: any) => a.status === "pass")).toHaveLength(1);
  expect((await client.get("/api/task?id=" + other)).task.attempts || []).toHaveLength(0);
});

test("A28 详情长范围可完整展开，侧栏列出可阅读的真实依据", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "long-scope-" + Date.now();
  const scope = Array.from({ length: 9 }, (_, i) => `app/a-very-long-isolated-subdirectory-${i}/actual-source-file.ts`);
  await client.post("/api/tasks", { id, title: "隔离详情长范围", type: "bug", goal: "verified", scope, acceptance: ["范围完整可查"] });
  await page.setViewportSize({ width: 940, height: 1000 });
  await goto(page, "single", "/tasks/" + id);
  const goal = page.locator(".overview > .content-section").filter({ hasText: "目标与范围" });
  await expect(goal.locator(".scope-preview")).toHaveCount(2);
  await goal.getByText("查看完整范围（9 项）", { exact: true }).click();
  await expect(goal.locator(".scope-details li")).toHaveCount(9);
  await expect(goal.locator(".scope-details li").last()).toHaveText(scope[8]);
  await expect(page.locator(".task-more > summary")).toBeVisible();
  await goto(page, "many", "/tasks/verified-change");
  const sidebar = page.locator(".sidebar-basis");
  await expect(sidebar.locator(".sidebar-basis-link")).toHaveCount(2);
  await expect(sidebar).toContainText("隔离夹具：当前交付规范");
  await sidebar.getByRole("button", { name: "隔离夹具：当前交付规范" }).click();
  await expect(page).toHaveURL(/section=basis/);
  await expect(page.locator("#basis-reading")).toContainText("此处是任务实际提供版本");
  await shot(page, "A28-detail-basis");
});

test("A29 人工验收草稿重开保留输入，未知提交可安全重放", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "human-draft-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离验收草稿", type: "bug", goal: "verified", scope: ["app"], acceptance: ["人工核对实际产物"], checks: [{ id: "owner", kind: "review", method: "human", covers: ["人工核对实际产物"], required: true }] });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  await goto(page, "single", `/tasks/${id}?section=checks`);
  await page.getByRole("button", { name: "记录人工验收", exact: true }).click();
  let dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("实际观察与理由").fill("关闭并重开后保留真实隔离观察");
  await dialog.getByLabel("app/ui-output.txt").check();
  await dialog.getByLabel("我确认以上结论针对所示候选和修订。").check();
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await page.getByRole("button", { name: "记录人工验收", exact: true }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("radio", { name: "通过", exact: true })).toBeChecked();
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("关闭并重开后保留真实隔离观察");
  await expect(dialog.getByLabel("app/ui-output.txt")).toBeChecked();
  await expect(dialog.getByLabel("我确认以上结论针对所示候选和修订。")).not.toBeChecked();
  await dialog.getByLabel("我确认以上结论针对所示候选和修订。").check();
  await page.route("**/api/action", async (route) => {
    if (route.request().postDataJSON()?.action === "review_result") { const response = await route.fetch(); expect(response.ok()).toBeTruthy(); await route.abort(); }
    else await route.continue();
  });
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog.getByRole("button", { name: "原样重试提交" })).toBeVisible();
  await page.unroute("**/api/action");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await page.reload();
  await page.locator(".next-panel").getByRole("button", { name: "查看待确认的验收" }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("关闭并重开后保留真实隔离观察");
  await expect(dialog.getByLabel("实际观察与理由")).toBeDisabled();
  await dialog.getByRole("button", { name: "原样重试提交" }).click();
  await expect(dialog).toContainText("任务已完成");
  expect((await client.get("/api/task?id=" + id)).task.evidence).toHaveLength(1);
});

test("A30 四项检查通过且待人工验收时对齐工作台与详情入口", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "reference-review-" + Date.now();
  const checks = Array.from({ length: 4 }, (_, index) => ({ id: `command-${index + 1}`, kind: "functional", method: "command", command: ["sh", "-c", "test -f app/ui-output.txt"], covers: ["实际产物可核验"], required: true }));
  checks.push({ id: "human-review", kind: "review", method: "human", command: [], covers: ["实际产物可核验"], required: true });
  await client.post("/api/tasks", { id, title: "隔离参考：等待人工验收", type: "bug", goal: "verified", scope: ["app/ui-output.txt"], acceptance: ["实际产物可核验"], checks });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  for (const check of checks.slice(0, 4)) await client.post("/api/action", { action: "verify", id, unit: check.id });
  const current = await client.get("/api/task?id=" + id);
  expect(current.summary.checks.required_passed).toBe(4);
  expect(current.check_access["human-review"].can_review).toBeTruthy();
  await page.setViewportSize({ width: 940, height: 1000 });
  await goto(page, "single", "/tasks?q=" + id);
  const row = page.locator(".task-row").filter({ hasText: id });
  await expect(page.locator(".attention-card").filter({ hasText: "隔离参考：等待人工验收" })).toContainText("4 / 5 项必要检查通过");
  await expect(row).toContainText("等待人工验收");
  await expect(row).toContainText("4 / 5 通过");
  await expect(row.getByRole("button", { name: "验收", exact: true })).toBeVisible();
  await shot(page, "A30-workbench-human-940");
  await row.locator(".task-title-link").click();
  await expect(page.locator(".result-banner")).toContainText("还有人工验收需要你处理");
  await expect(page.locator(".result-banner").getByRole("button", { name: "开始人工验收" })).toBeVisible();
  await expect(page.locator(".acceptance-summary")).toContainText("4 / 5 项必要检查通过");
  await expect(page.locator(".next-panel").getByRole("button", { name: "开始人工验收" })).toBeVisible();
  await shot(page, "A30-overview-human-940");
  await page.screenshot({ path: path.join(output, "A30-overview-human-940-viewport.png") });
});

test("A31 资料分区、筛选清除和诊断摘要连接真实数据", async ({ page }) => {
  await goto(page, "many", "/knowledge/current-rule");
  await expect(page.locator(".markdown")).toBeVisible();
  await page.getByRole("navigation", { name: "资料详情分区" }).getByRole("button", { name: "来源与版本" }).click();
  await expect(page.locator(".source-details")).toContainText(".ael/knowledge/current.md");
  await page.getByRole("navigation", { name: "资料详情分区" }).getByRole("button", { name: "关联工作" }).click();
  await expect(page.getByRole("link", { name: "真实隔离交付 verified-change" })).toBeVisible();
  await goto(page, "many", "/knowledge?view=all&q=not-present-in-fixture-2026");
  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(page).not.toHaveURL(/q=/);
  await expect(page.locator(".knowledge").first()).toBeVisible();
  await goto(page, "many", "/evolution?q=not-present-in-fixture-2026");
  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(page).not.toHaveURL(/q=/);
  await expect(page.locator(".change-row").first()).toBeVisible();
  await goto(page, "many", "/settings?section=diagnostics");
  await expect(page.getByRole("button", { name: "复制诊断摘要" })).toBeVisible();
});

test("A32 工作台可恢复已提交但响应丢失的人工验收", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "human-workbench-pending-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离工作台验收回执恢复", type: "bug", goal: "verified", scope: ["app"], acceptance: ["人工核验隔离产物"], checks: [{ id: "owner", kind: "review", method: "human", covers: ["人工核验隔离产物"], required: true }] });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  await goto(page, "single", "/tasks?q=" + id);
  await page.locator(".task-row").filter({ hasText: id }).getByRole("button", { name: "验收", exact: true }).click();
  let dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("实际观察与理由").fill("工作台验收已持久化但响应丢失");
  await dialog.getByLabel("app/ui-output.txt").check();
  await dialog.getByLabel("我确认以上结论针对所示候选和修订。").check();
  await page.route("**/api/action", async (route) => {
    if (route.request().postDataJSON()?.action === "review_result") { const response = await route.fetch(); expect(response.ok()).toBeTruthy(); await route.abort(); }
    else await route.continue();
  });
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog.getByRole("button", { name: "原样重试提交" })).toBeVisible();
  await page.unroute("**/api/action");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await page.reload();
  const pending = page.locator(".attention-card").filter({ hasText: "验收提交待确认" });
  await expect(pending).toContainText("隔离工作台验收回执恢复");
  await pending.getByRole("button", { name: "查看并重试" }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "原样重试提交" }).click();
  await expect(dialog).toContainText("任务已完成");
  expect((await client.get("/api/task?id=" + id)).task.evidence).toHaveLength(1);
});

test("A33 不会把同端口其他项目的人工验收草稿当成本项目输入", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "human-project-scope-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离验收项目边界", type: "bug", goal: "verified", scope: ["app"], acceptance: ["人工核验"], checks: [{ id: "owner", kind: "review", method: "human", covers: ["人工核验"], required: true }] });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  await goto(page, "single", `/tasks/${id}?section=checks`);
  await page.evaluate((taskId) => {
    const key = "ael-manual-review-v2:" + encodeURIComponent("/another/project") + ":" + encodeURIComponent(taskId) + ":owner";
    sessionStorage.setItem(key, JSON.stringify({ checkId: "owner", outcome: "pass", reason: "其他项目的观察", selected: ["app/ui-output.txt"] }));
  }, id);
  await page.getByRole("button", { name: "记录人工验收", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("radio", { name: "通过", exact: true })).not.toBeChecked();
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
});

test("A34 实施中可直接记录人工意见，候选生成后由用户用于正式验收", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "manual-before-candidate-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离人工验收先记录", type: "bug", goal: "verified", scope: ["app"], acceptance: ["检查真实页面"], checks: [
    { id: "owner", kind: "review", method: "human", covers: ["检查真实页面"], required: true },
    { id: "automated", kind: "functional", method: "command", command: ["sh", "-c", "test -f app/ui-output.txt"], covers: ["检查真实页面"], required: true },
  ] });
  await step(client, id);
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".next-panel").getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  const before = await client.get("/api/task?id=" + id);
  const attempt = before.task.attempts.find((a: any) => a.status === "running");
  await page.locator(".result-banner").getByRole("button", { name: "开始人工验收" }).click();
  let dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("尚未生成验收候选");
  await expect(dialog.getByRole("radio", { name: "通过", exact: true })).not.toBeChecked();
  await expect(dialog.getByLabel("本次产物（逗号分隔）")).toHaveCount(0);
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由").fill("我检查了当前页面，候选生成后再确认。");
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("我确认以上是当前人工观察，候选生成后需要重新确认。").check();
  await shot(page, "A34-pending-human-review-form");
  await page.route("**/api/action", async (route) => {
    if (route.request().postDataJSON()?.action === "review_pending") { const response = await route.fetch(); expect(response.ok()).toBeTruthy(); await route.abort(); }
    else await route.continue();
  });
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog.getByRole("button", { name: "原样重试提交" })).toBeVisible();
  await page.unroute("**/api/action");
  await dialog.getByRole("button", { name: "原样重试提交" }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("人工验收意见已记录");
  await expect(dialog).toContainText("待候选确认");
  await shot(page, "A34-pending-human-review-receipt");
  let current = await client.get("/api/task?id=" + id);
  expect(current.current_step).toBe("execute");
  expect(current.task.candidate).toBe("");
  expect(current.task.evidence || []).toHaveLength(0);
  expect(current.task.human_review_notes).toHaveLength(1);
  expect(current.task.attempts.find((a: any) => a.id === attempt.id).status).toBe("running");
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await page.locator(".next-panel").getByRole("button", { name: "填写实施结果" }).click();
  await page.getByLabel("实际工作与结果").fill("隔离实施产物已准备");
  await page.getByLabel("本次产物（逗号分隔）").fill("app/ui-output.txt");
  await page.getByLabel("规范处理依据 / 阻塞解除条件").fill("没有适用规范变化");
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  current = await client.get("/api/task?id=" + id);
  expect(current.current_step).toBe("verify");
  expect(current.task.candidate).toBeTruthy();
  await page.locator(".result-banner").getByRole("button", { name: "查看验收记录" }).click();
  dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("我检查了当前页面，候选生成后再确认。");
  await dialog.getByRole("button", { name: "引用这条意见" }).click();
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("我检查了当前页面，候选生成后再确认。");
  await dialog.getByLabel("app/ui-output.txt").check();
  await dialog.getByLabel("我确认以上结论针对所示候选和修订。").check();
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("任务仍有后续工作");
  current = await client.get("/api/task?id=" + id);
  expect(current.task.human_review_notes[0].status).toBe("confirmed");
  expect(current.task.human_review_notes[0].confirmed_candidate).toBe(current.task.candidate);
  expect(current.current_checks.owner.result).toBe("pass");
  expect(current.current_checks.automated).toBeNull();
  expect(current.task.status).toBe("open");
});

test("A35 未配置人工检查的任务也可记录并确认任务级意见，但不替代模型检查", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "manual-task-note-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离任务级人工意见", type: "bug", goal: "verified", scope: ["app"], acceptance: ["模型检查独立保留"], checks: [{ id: "model", kind: "review", method: "model", covers: ["模型检查独立保留"], required: true }] });
  await step(client, id);
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".result-banner").getByRole("button", { name: "开始人工验收" }).click();
  let dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由").fill("记录当前人工体验意见");
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("我确认以上是当前人工观察，候选生成后需要重新确认。").check();
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("待候选确认");
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await step(client, id, ["app/ui-output.txt"]);
  await page.reload();
  await page.locator(".result-banner").getByRole("button", { name: "查看验收记录" }).click();
  dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await dialog.getByRole("button", { name: "确认这条意见适用于当前候选" }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("人工验收意见已确认");
  const current = await client.get("/api/task?id=" + id);
  expect(current.task.human_review_notes[0].status).toBe("confirmed");
  expect(current.task.evidence || []).toHaveLength(0);
  expect(current.current_checks.model).toBeNull();
  expect(current.task.status).toBe("open");
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await page.reload();
  await expect(page.locator(".result-banner")).toContainText("人工意见已确认当前候选");
});

test("A36 待候选人工意见的响应丢失后可关闭重开并安全重试", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "manual-pending-retry-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离待候选意见重试", type: "bug", goal: "verified", acceptance: ["人工观察真实结果"] });
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".result-banner").getByRole("button", { name: "开始人工验收" }).click();
  let dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await dialog.getByRole("radio", { name: "不通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由").fill("当前页面仍有需要调整的地方");
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("我确认以上是当前人工观察，候选生成后需要重新确认。").check();
  await page.route("**/api/action", async (route) => {
    if (route.request().postDataJSON()?.action === "review_pending") { const response = await route.fetch(); expect(response.ok()).toBeTruthy(); await route.abort(); }
    else await route.continue();
  });
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(dialog.getByRole("button", { name: "原样重试提交" })).toBeVisible();
  await page.unroute("**/api/action");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await page.reload();
  await page.locator(".next-panel").getByRole("button", { name: "查看待确认的验收" }).click();
  dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("当前页面仍有需要调整的地方");
  await dialog.getByRole("button", { name: "原样重试提交" }).click();
  await expect(page.getByRole("dialog")).toContainText("待候选确认");
  const current = await client.get("/api/task?id=" + id);
  expect(current.task.human_review_notes).toHaveLength(1);
  expect(current.task.human_review_notes[0].result).toBe("fail");
  expect(current.task.evidence || []).toHaveLength(0);
});

test("A37 旧接手释放后保留未提交文字，可由用户带入人工观察", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "manual-prior-work-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离旧表单内容保留", type: "bug", goal: "verified", acceptance: ["人工观察可复核"] });
  await step(client, id);
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".next-panel").getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  await page.getByLabel("实际工作与结果").fill("先前在实施表单填写的人工观察");
  await page.getByLabel("规范处理依据 / 阻塞解除条件").fill("另一条尚未提交的说明");
  await client.post("/api/action", { action: "resume", id, ack_stopped: true });
  await page.reload();
  await expect(page.getByRole("heading", { name: "提交本次工作结果" })).toHaveCount(0);
  await page.locator(".result-banner").getByRole("button", { name: "开始人工验收" }).click();
  const dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("发现本标签页上次未提交的实施表单");
  await dialog.getByRole("button", { name: "带入上次填写的工作说明" }).click();
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("先前在实施表单填写的人工观察");
  await expect(dialog.getByRole("radio", { name: "通过", exact: true })).not.toBeChecked();
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("我确认以上是当前人工观察，候选生成后需要重新确认。").check();
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(page.getByRole("dialog")).toContainText("人工验收意见已记录");
  const current = await client.get("/api/task?id=" + id);
  expect(current.task.human_review_notes[0].reason).toBe("先前在实施表单填写的人工观察");
  expect(current.task.evidence || []).toHaveLength(0);
});

test("A38 未知的实施提交在旧接手释放后可核对并保留原文字", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "manual-old-submit-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离旧提交状态核对", type: "bug", goal: "verified", acceptance: ["保留人工观察"] });
  await step(client, id);
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".next-panel").getByRole("button", { name: "接手当前工作", exact: true }).click();
  await confirmDialog(page);
  await page.getByLabel("实际工作与结果").fill("尚未发送成功的人工观察");
  await page.getByLabel("规范处理依据 / 阻塞解除条件").fill("此前填写的说明");
  await page.route("**/api/action", (route) => route.request().postDataJSON()?.action === "submit" ? route.abort() : route.continue());
  await page.getByRole("button", { name: "提交并查看下一步" }).click();
  await expect(page.getByRole("button", { name: "原样重试提交" })).toBeVisible();
  await page.unroute("**/api/action");
  await client.post("/api/action", { action: "resume", id, ack_stopped: true });
  await page.reload();
  await page.getByRole("button", { name: "原样重试提交" }).click();
  await expect(page.getByRole("heading", { name: "提交本次工作结果" })).toHaveCount(0);
  await page.locator(".result-banner").getByRole("button", { name: "开始人工验收" }).click();
  const dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await dialog.getByRole("button", { name: "带入上次填写的工作说明" }).click();
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("尚未发送成功的人工观察");
  expect((await client.get("/api/task?id=" + id)).task.evidence || []).toHaveLength(0);
});

test("A39 人工意见提交后工作台和详情清楚显示待候选确认", async ({ page, request, context }) => {
  const client = await connect(request, "single"), id = "review-status-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离人工验收状态", type: "bug", goal: "verified", acceptance: ["保留用户的验收意见"] });
  await goto(page, "single", "/tasks?q=" + id);
  let row = page.locator(".task-row").filter({ hasText: id });
  await row.getByRole("button", { name: "验收", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由").fill("已核对本次隔离界面的实际表现");
  await dialog.getByLabel("记录人").fill("isolated reviewer");
  await dialog.getByLabel("我确认以上是当前人工观察，候选生成后需要重新确认。").check();
  await dialog.getByRole("button", { name: "提交验收结果" }).click();
  await expect(page.getByRole("dialog")).toContainText("待候选确认");
  await page.getByRole("dialog").getByRole("button", { name: "回工作台" }).click();
  row = page.locator(".task-row").filter({ hasText: id });
  await expect(row.locator(".review-note-status")).toContainText("已记录人工验收 · 待候选确认");
  await expect(row).toContainText("等待接手");
  await shot(page, "A39-workbench-review-status");
  const other = await context.newPage();
  await goto(other, "single", "/tasks?q=" + id);
  await expect(other.locator(".task-row").filter({ hasText: id }).locator(".review-note-status")).toContainText("待候选确认");
  await other.close();
  await row.getByRole("button", { name: "查看验收记录" }).click();
  dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("已核对本次隔离界面的实际表现");
  await expect(dialog.getByRole("button", { name: "提交验收结果" })).toHaveCount(0);
  await shot(page, "A39-saved-review-record");
  await dialog.getByRole("button", { name: "记录新的验收意见" }).click();
  await expect(dialog.getByRole("button", { name: "提交验收结果" })).toBeVisible();
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await goto(page, "single", "/tasks/" + id);
  await expect(page.locator(".result-banner")).toContainText("已记录人工验收 · 待候选确认");
  await expect(page.locator(".result-banner")).toContainText("等待接手");
  await expect(page.locator(".result-banner").getByRole("button", { name: "查看验收记录" })).toBeVisible();
  await shot(page, "A39-detail-review-status");
  const current = await client.get("/api/task?id=" + id);
  expect(current.task.human_review_notes).toHaveLength(1);
  expect(current.task.status).toBe("open");
  expect(current.task.evidence || []).toHaveLength(0);
});

test("A40 两条已保存的人工意见都可查看，不自动合并或重复提交", async ({ page, request }) => {
  const client = await connect(request, "single"), id = "review-two-records-" + Date.now();
  const created = (await client.post("/api/tasks", { id, title: "隔离两次人工验收", type: "bug", goal: "verified", acceptance: ["保留真实记录"] })).value;
  const common = { action: "review_pending", id, unit: "", executor: "reviewer", result: "pass", reason: "已核对", recovery: "", artifacts: [], candidate: "", revision: created.definition_revision, plan_revision: created.plan_revision };
  await client.post("/api/action", { ...common, request_key: id + "-one" });
  await client.post("/api/action", { ...common, request_key: id + "-two" });
  await goto(page, "single", "/tasks/" + id);
  await expect(page.locator(".result-banner")).toContainText("待候选确认");
  await page.locator(".result-banner").getByRole("button", { name: "查看验收记录" }).click();
  const dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog.locator(".review-note-list .review-note")).toHaveCount(2);
  await expect(dialog.getByRole("button", { name: "提交验收结果" })).toHaveCount(0);
  const current = await client.get("/api/task?id=" + id);
  expect(current.task.human_review_notes).toHaveLength(2);
  expect(current.task.evidence || []).toHaveLength(0);
});

test("A41 已完成任务仍可查看已确认的人工验收记录", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "review-completed-record-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离完成后的验收记录", type: "bug", goal: "verified", scope: ["app"], acceptance: ["人工确认结果"], checks: [{ id: "owner", kind: "review", method: "human", covers: ["人工确认结果"], required: true }] });
  await step(client, id);
  let current = await client.get("/api/task?id=" + id);
  const note = (await client.post("/api/action", { action: "review_pending", id, unit: "", executor: "reviewer", result: "pass", reason: "此前已查看", recovery: "", artifacts: [], request_key: id + "-note", candidate: "", revision: current.definition_revision, plan_revision: current.plan_revision })).value;
  await step(client, id, ["app/ui-output.txt"]);
  current = await client.get("/api/task?id=" + id);
  await client.post("/api/action", { action: "review_result", id, unit: "owner", executor: "reviewer", result: "pass", reason: "重新核对当前候选", recovery: "", artifacts: ["app/ui-output.txt"], request_key: id + "-formal", review_note_id: note.id, candidate: current.task.candidate, revision: current.definition_revision, plan_revision: current.plan_revision });
  await goto(page, "single", "/tasks/" + id);
  await expect(page.locator(".result-banner")).toContainText("人工意见已确认当前候选");
  await page.locator(".task-heading-actions").getByRole("button", { name: "查看验收记录" }).click();
  const dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("此前已查看");
  await expect(dialog.getByRole("button", { name: "提交验收结果" })).toHaveCount(0);
  expect((await client.get("/api/task?id=" + id)).task.status).toBe("completed");
});

test("A42 多个人工检查时两个入口都能查看其他检查的意见", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "review-multiple-checks-" + Date.now();
  await client.post("/api/tasks", { id, title: "隔离多个人工检查", type: "bug", goal: "verified", scope: ["app"], acceptance: ["核对实际产物"], checks: [
    { id: "first-review", kind: "review", method: "human", covers: ["核对实际产物"], required: true },
    { id: "second-review", kind: "review", method: "human", covers: ["核对实际产物"], required: true },
  ] });
  await step(client, id);
  let current = await client.get("/api/task?id=" + id);
  await client.post("/api/action", { action: "review_pending", id, unit: "second-review", executor: "reviewer", result: "pass", reason: "第二项检查的真实意见", recovery: "", artifacts: [], request_key: id + "-note", candidate: "", revision: current.definition_revision, plan_revision: current.plan_revision });
  await step(client, id, ["app/ui-output.txt"]);
  current = await client.get("/api/task?id=" + id);
  expect(current.check_access["first-review"].can_review).toBeTruthy();
  expect(current.check_access["second-review"].can_review).toBeTruthy();
  await goto(page, "single", "/tasks?q=" + id);
  await page.locator(".task-row").filter({ hasText: id }).getByRole("button", { name: "查看验收记录" }).click();
  let dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("检查 second-review");
  await expect(dialog).toContainText("第二项检查的真实意见");
  await expect(dialog.getByRole("button", { name: "引用这条意见" })).toHaveCount(0);
  await dialog.getByRole("button", { name: "返回", exact: true }).click();
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".result-banner").getByRole("button", { name: "查看验收记录" }).click();
  dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("第二项检查的真实意见");
  await expect(dialog.getByRole("button", { name: "引用这条意见" })).toHaveCount(0);
  await dialog.getByRole("button", { name: "返回", exact: true }).click();
  await page.getByRole("button", { name: "检查与验收" }).click();
  await page.locator(".check").filter({ hasText: "second-review ·" }).getByRole("button", { name: "记录人工验收" }).click();
  dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await dialog.getByRole("button", { name: "引用这条意见" }).click();
  await expect(dialog.getByLabel("实际观察与理由")).toHaveValue("第二项检查的真实意见");
  current = await client.get("/api/task?id=" + id);
  expect(current.task.human_review_notes).toHaveLength(1);
  expect(current.task.evidence || []).toHaveLength(0);
});

test("A43 人工意见不遮挡阻塞，终态记录不能再次确认", async ({ page, request }) => {
  ensureIsolatedOutput();
  const client = await connect(request, "single"), id = "review-blocked-" + Date.now();
  const created = (await client.post("/api/tasks", { id, title: "隔离人工意见与阻塞", type: "bug", goal: "verified", scope: ["app"], acceptance: ["核对结果"], checks: [{ id: "command", kind: "functional", method: "command", command: ["sh", "-c", "test -f app/ui-output.txt"], covers: ["核对结果"], required: true }] })).value;
  await client.post("/api/action", { action: "review_pending", id, unit: "", executor: "reviewer", result: "pass", reason: "预先记录的意见", recovery: "", artifacts: [], request_key: id + "-note", candidate: "", revision: created.definition_revision, plan_revision: created.plan_revision });
  await step(client, id);
  await step(client, id, ["app/ui-output.txt"]);
  await client.post("/api/action", { action: "pause", id, reason: "等待隔离环境恢复" });
  await goto(page, "single", "/tasks?q=" + id);
  const row = page.locator(".task-row").filter({ hasText: id });
  await expect(row.locator(".review-note-status")).toContainText("待确认当前候选");
  await expect(row.locator(".blocked-reason")).toContainText("等待隔离环境恢复");
  await client.post("/api/action", { action: "cancel", id, reason: "隔离测试结束" });
  await goto(page, "single", "/tasks/" + id);
  await page.locator(".task-heading-actions").getByRole("button", { name: "查看验收记录" }).click();
  const dialog = page.getByRole("dialog", { name: "记录人工验收" });
  await expect(dialog).toContainText("预先记录的意见");
  await expect(dialog.getByRole("button", { name: "确认这条意见适用于当前候选" })).toHaveCount(0);
  expect((await client.get("/api/task?id=" + id)).task.status).toBe("cancelled");
});

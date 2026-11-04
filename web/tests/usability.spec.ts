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
  await expect(page.locator("[data-board]")).toHaveCount(5);
  const row = page.locator(".task-row");
  await expect(row).toContainText("等待接手");
  await expect(row).toContainText("当前无人执行");
  await expect(row).toContainText("下一步：领取");
  const rect = await row.boundingBox();
  expect(rect!.width).toBeGreaterThan(850);
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
  await page.getByLabel("状态分组").selectOption("active");
  await expect(page.locator("[data-board]")).toHaveCount(1);
  await page.getByLabel("状态分组").selectOption("");
  await expect(page.locator("[data-board=active] .task-row")).toContainText(
    "2 项执行中 · 1 项检查待接手",
  );
  await expect(page.locator("[data-board=confirmation]")).toContainText(
    "需要准确修订确认的方案",
  );
  await goto(page, "many", "/tasks/parallel-checks?section=execution");
  await expect(
    page.locator(".check").filter({ hasText: "check-a ·" }),
  ).toContainText("执行中");
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
  await expect(page.locator(".task-row .task-title .status")).not.toContainText("执行中");
  await page.getByLabel("完成历史").selectOption("7");
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
  await link.click();
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
    .getByRole("link", { name: /^最近完成/ })
    .click();
  await expect(page.locator("[data-board=completed] .task-row")).toHaveCount(3);
  await page.locator("[data-board=completed] .task-row").first().click();
  await expect(
    page.getByRole("navigation", { name: "任务详情分区" }),
  ).toBeVisible();
  await page.goBack();
  await expect(page.locator("[data-board=completed] .task-row")).toHaveCount(3);
});

test("A04 单项失败与未执行分开，按需查看真实证据", async ({ page }) => {
  await goto(page, "many", "/tasks/failed-check?section=execution");
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
  await expect(page.locator(".markdown script")).toHaveCount(0);
  await expect(page.locator(".markdown")).toContainText("<script>");
  await expect(page.locator('.markdown a[href^="javascript:"]')).toHaveCount(0);
  await page.getByText("来源与版本", { exact: true }).click();
  await expect(page.locator(".source-details")).toContainText(
    ".ael/knowledge/current.md",
  );
  await shot(page, "A06-markdown-source");
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
  await page.getByRole("button", { name: "执行与验收", exact: true }).click();
  await expect(page.locator(".check .status")).toHaveText("通过");
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
  await page.getByLabel("变化类型").selectOption("decision");
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
  await expect(page.locator("[data-board]")).toHaveCount(5);
  await expect(page.locator(".task-row")).toHaveCount(0);
  await expect(page.locator("main")).toContainText("暂无待处理");
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
  await page.getByRole("button", { name: "暂缓", exact: true }).click();
  await confirmDialog(page, "隔离浏览器暂停验证");
  await expect(page.locator(".conclusion")).toContainText("隔离浏览器暂停验证");
  await page.getByRole("button", { name: "恢复任务", exact: true }).click();
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
  await page.getByRole("button", { name: "取消任务", exact: true }).click();
  await confirmDialog(page, "浏览器隔离取消记录");
  await expect(page.locator(".conclusion")).toContainText("浏览器隔离取消记录");
  await page.getByRole("button", { name: "技术记录", exact: true }).click();
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
  await page.getByRole("button", { name: "恢复任务", exact: true }).click();
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
  await goto(page, "single", "/tasks/" + longId + "?section=execution");
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
  await page.getByRole("button", { name: "暂缓", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "暂缓", exact: true }),
  ).toBeFocused();
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1280, height: 800 },
    { width: 760, height: 900 },
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
  await goto(page, "single", "/tasks/" + id + "?section=execution");
  await page
    .getByRole("button", { name: "记录人工验收通过", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("人工验收结论与依据")
    .fill("明确通过已审阅的隔离产物");
  await dialog
    .getByLabel("已有报告与证据（逗号分隔）")
    .fill("runtime:human-review.md");
  await client.post("/api/action", {
    action: "resume",
    id,
    unit: "execute",
    ack_stopped: true,
  });
  await step(client, id, ["app/ui-output.txt"], () =>
    fs.writeFileSync(
      path.join(projects.single.root, "app/ui-output.txt"),
      "new isolated candidate for explicit human review\n",
    ),
  );
  await dialog.getByRole("button", { name: "确认记录人工验收通过" }).click();
  await expect(dialog.getByRole("alert")).toContainText("验收候选或修订已变化");
  expect((await client.get("/api/task?id=" + id)).task.evidence||[]).toHaveLength(
    0,
  );
  await shot(page, "A12-stale-human-review");
  await dialog.getByRole("button", { name: "返回", exact: true }).click();
  await page.reload();
  await page
    .getByRole("button", { name: "记录人工验收通过", exact: true })
    .click();
  await page
    .getByRole("dialog")
    .getByLabel("已有报告与证据（逗号分隔）")
    .fill("runtime:human-review.md");
  await page
    .getByRole("dialog")
    .getByLabel("人工验收结论与依据")
    .fill("明确审阅新隔离产物并通过");
  await confirmDialog(page);
  await expect(page.locator(".check .status")).toHaveText("通过");
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
  expect(requests.some((u) => u.includes("/@vite/"))).toBeFalsy();
  fs.writeFileSync(
    path.join(output, "embedded-version.json"),
    JSON.stringify(info, null, 2),
  );
  await shot(page, "A14-embedded-workbench");
});

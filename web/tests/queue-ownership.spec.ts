import { test, expect, type Browser, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

// Queue ownership over a real Server seeded by queue_ownership_acceptance.py.
// Every state assertion is cross-checked against the Server API.
const fixture = JSON.parse(fs.readFileSync(process.env.AEL_QUEUE_FIXTURE!, "utf8"));
const base: string = fixture.url;
const shots = path.join(fixture.out, "browser-shots");
fs.mkdirSync(shots, { recursive: true });
test.describe.configure({ mode: "serial" });

async function open(browser: Browser, width = 1440): Promise<Page> {
  const context = await browser.newContext({ viewport: { width, height: 900 }, timezoneId: "Asia/Shanghai" });
  const page = await context.newPage();
  await page.goto(base + "/#/tasks?project=shared");
  await page.getByLabel("用户名", { exact: true }).fill(fixture.admin.username);
  await page.getByLabel("密码", { exact: true }).fill(fixture.admin.password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.locator(".wb-sidebar")).toBeVisible();
  return page;
}
const go = async (page: Page, route: string) => { await page.goto(base + "/#/" + route + (route.includes("?") ? "&" : "?") + "project=shared"); await page.waitForTimeout(1200); };
const queueOf = async (page: Page, id: string) => (await (await page.request.get(base + "/api/v1/projects/shared/queue?task=" + id)).json()).items[0];
const row = (page: Page, id: string) => page.locator(`.wb-task-row[data-task="${id}"]`);
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(shots, name + ".png"), fullPage: true });

test("列表：排队标签、未安排、筛选与中性色", async ({ browser }) => {
  const page = await open(browser);
  await go(page, "tasks");
  await expect(row(page, "q5").locator(".wb-row-owner")).toContainText("Bot A · 排队中");
  await expect(row(page, "free").locator(".wb-row-owner")).toContainText("未安排");
  // Status keeps the real step/blocker; queue never borrows the running tone.
  await expect(row(page, "q5").locator(".tag")).not.toHaveClass(/active|done/);
  await expect(row(page, "paused").locator(".tag")).toHaveText("受阻");
  await expect(row(page, "waits").locator(".tag")).toHaveText("等待前置");
  await shot(page, "list-1440");
  const filter = page.getByLabel("排队安排");
  await filter.selectOption("queued");
  await expect(page.locator(".wb-task-row")).toHaveCount(2);
  await expect(row(page, "q5")).toBeVisible(); await expect(row(page, "q6")).toBeVisible();
  await filter.selectOption("claimable");
  await expect(row(page, "free")).toBeVisible(); await expect(row(page, "q3")).toBeVisible();
  await expect(row(page, "q5")).toHaveCount(0); await expect(row(page, "waits")).toHaveCount(0); await expect(row(page, "paused")).toHaveCount(0);
  await filter.selectOption({ label: "执行者：Bot A" });
  await expect(row(page, "q5")).toBeVisible(); await expect(row(page, "free")).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await go(page, "tasks?assign=queued");
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
  await shot(page, "list-queued-390");
});

test("详情：显示等待时长，转交给 Codex 并与服务端一致", async ({ browser }) => {
  const page = await open(browser);
  await go(page, "tasks/q5");
  const panel = page.getByTestId("queue-ownership");
  await expect(panel).toContainText("Bot A · 排队中");
  await expect(panel).toContainText("已等待");
  await shot(page, "detail-queued");
  await panel.getByRole("button", { name: "转交" }).click();
  await panel.getByLabel("接收者").selectOption({ label: "Codex 本地（codex-1）" });
  await panel.getByLabel("原因").fill("本地有空闲");
  await panel.getByRole("button", { name: "确认转交" }).click();
  await expect(panel).toContainText("Codex 本地 · 排队中");
  await expect(panel).toContainText("由 bot-1 转交");
  const q = await queueOf(page, "q5");
  expect(q.queue.runner).toBe("codex-1");
  expect(q.assignment).toBe("queued");
});

test("详情：版本冲突时说明原因、保留输入并刷新归属", async ({ browser }) => {
  const page = await open(browser);
  await go(page, "tasks/q6");
  const panel = page.getByTestId("queue-ownership");
  await expect(panel).toContainText("Bot A · 排队中");
  await panel.getByRole("button", { name: "释放安排" }).click();
  await panel.getByLabel("原因").fill("改由本地处理");
  // Someone else transfers it first, through the same server.
  const before = await queueOf(page, "q6");
  const csrf = (await page.context().cookies()).find((c) => c.name === "ael_csrf")!.value;
  const moved = await page.request.post(base + "/api/v1/projects/shared/runners/bot-1/queue-transfer", {
    headers: { "X-AEL-CSRF": csrf, "X-AEL-Protocol": "1", Origin: base },
    data: { task_id: "q6", expected_version: before.queue.version, to_runner: "bot-2", reason: "Bot 内部调度", operation_id: "ui-race", idempotency_key: "ui-race" },
  });
  expect(moved.ok()).toBeTruthy();
  await panel.getByRole("button", { name: "确认释放" }).click();
  await expect(panel.getByRole("alert")).toContainText("排队安排已被他人更新或已失效");
  await expect(panel.getByLabel("原因")).toHaveValue("改由本地处理");
  await expect(panel).toContainText("由 bot-1 转交");
  expect((await queueOf(page, "q6")).queue.runner).toBe("bot-2");
  await shot(page, "detail-conflict");
});

test("已开始的任务沿用现有执行信息，不显示排队", async ({ browser }) => {
  const page = await open(browser);
  await go(page, "tasks/q1");
  await expect(page.getByTestId("queue-ownership")).toHaveCount(0);
  expect((await queueOf(page, "q1")).assignment).toBe("started");
  await go(page, "tasks/free");
  await expect(page.getByTestId("queue-ownership")).toContainText("未安排");
  await expect(page.getByTestId("queue-ownership")).toContainText("其他系统是否另有安排未知");
});

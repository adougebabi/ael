import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const manifestPath = process.env.AEL_EPIC_FIXTURE;
if (!manifestPath) throw new Error("AEL_EPIC_FIXTURE is required");
const fixture = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const root = path.resolve(fixture.out);
if (!fixture.isolated || !fs.existsSync(path.join(root, "ISOLATED-EPIC-PROGRESS")) ||
    !path.resolve(fixture.root).startsWith(root + path.sep) || !path.resolve(fixture.runtime).startsWith(root + path.sep)) {
  throw new Error("refusing a non-isolated Epic fixture");
}
const output = process.env.AEL_UI_OUT || "/tmp/ael-epic-browser";
fs.mkdirSync(output, { recursive: true });

async function client(request: APIRequestContext) {
  const session = await request.get(fixture.url + "/api/session");
  expect(session.ok()).toBeTruthy();
  const token = (await session.json()).token;
  const headers = { Authorization: "Bearer " + token };
  return {
    async get(endpoint: string) {
      const r = await request.get(fixture.url + endpoint, { headers });
      expect(r.ok(), await r.text()).toBeTruthy();
      return r.json();
    },
    async post(endpoint: string, data: unknown) {
      const r = await request.post(fixture.url + endpoint, { headers, data });
      expect(r.ok(), await r.text()).toBeTruthy();
      return r.json();
    },
  };
}
async function open(page: Page, route: string) {
  await page.goto(fixture.url + "/#" + route);
}
async function shot(page: Page, name: string) {
  await expect(page.locator(".epic-overview .loading")).toHaveCount(0);
  await page.screenshot({ path: path.join(output, name + ".png"), fullPage: true });
}

test("打包页面显示 1、8、30 个子任务与不同验证状态", async ({ page, request }) => {
  const c = await client(request);
  const info = await c.get("/api/info");
  expect(info.project).toBe(fixture.root);
  expect(path.resolve(info.data_dir).startsWith(path.resolve(fixture.runtime) + path.sep)).toBeTruthy();
  expect(info.service_version).toBe(fixture.version);
  expect(info.service_build).toBe(fixture.build);
  for (const count of [1, 8, 30]) {
    await open(page, `/tasks/epic-${count}?section=overview`);
    const overview = page.getByLabel("大任务进展");
    await expect(overview).toContainText(`${count} 个叶子`);
    await expect(overview).toContainText(`必需 ${count}`);
    await expect(page.locator(".result-banner")).toContainText("等待必需子任务");
    await expect(page.locator("main pre")).toHaveCount(0);
    await expect(page.locator("main")).not.toContainText('"definition_revision"');
    if (count === 1) await expect(overview).toContainText("当前无人执行");
    if (count === 8) {
      await expect(overview).toContainText("实施完成 · 尚未验收");
      await expect(overview).toContainText("实施完成 · 部分检查通过");
      await expect(overview).toContainText("当前无人执行");
      await expect(overview).toContainText("失败检查与证据");
      await expect(overview.locator(".epic-leaves li").filter({ hasText: "8 子任务 waiting-4" })).toContainText("等待人工验收");
      await expect(page.getByRole("button", { name: "开始人工验收", exact: true })).toHaveCount(0);
    }
    if (count === 30) {
      await expect(overview.locator(".epic-leaves li")).toHaveCount(8);
      const pagination = overview.getByRole("navigation", { name: "子任务分页" });
      await pagination.getByRole("button", { name: "下一页" }).click();
      await expect(overview.locator(".epic-leaves li").first()).toContainText("leaf-08");
      await pagination.getByRole("button", { name: "下一页" }).click();
      await pagination.getByRole("button", { name: "下一页" }).click();
      await expect(overview.locator(".epic-leaves li")).toHaveCount(6);
      await expect(overview).toContainText("0 / 30 项必需子任务满足父级条件");
    }
    await shot(page, `epic-${count}`);
  }
});

test("父卡到失败检查、证据及返回时保留筛选与位置", async ({ page }) => {
  await open(page, "/tasks?board=all&q=隔离大任务%208");
  const row = page.locator(".task-row").filter({ hasText: "隔离大任务 8" });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("叶子 8");
  await row.getByRole("link", { name: "隔离大任务 8" }).click();
  const failure = page.getByLabel("大任务进展").getByRole("link", { name: "查看失败检查与证据 →" });
  await failure.scrollIntoViewIfNeeded();
  const y = await page.evaluate(() => window.scrollY);
  await failure.click();
  await expect(page).toHaveURL(/section=checks.*check=proof/);
  const check = page.locator("#task-check-proof");
  await expect(check.locator(".evidence")).toContainText("失败");
  await check.getByRole("button", { name: /读取现存引用/ }).first().click();
  await expect(page.locator(".artifact-panel pre")).toContainText("isolated failure evidence");
  await page.screenshot({ path: path.join(output, "epic-failed-evidence.png"), fullPage: true });
  await page.goBack();
  await expect(page.getByLabel("大任务进展")).toContainText("8 个叶子");
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(Math.max(0, y - 120));
  await page.goBack();
  await expect(page.getByLabel("搜索任务")).toHaveValue("隔离大任务 8");
  await expect(row).toHaveCount(1);
  await expect(page).toHaveURL(/board=all/);
});

test("30 项分页保留位置，末页必需取消仍计入父级范围", async ({ page, request }) => {
  const c = await client(request);
  await open(page, "/tasks/epic-30?section=overview");
  const overview = page.getByLabel("大任务进展");
  await c.post("/api/action", { action: "cancel", id: "e30-leaf-29", reason: "隔离末页取消" });
  await expect(overview).toContainText("取消");
  await expect(overview).toContainText("0 / 30 项必需子任务满足父级条件");
  const pagination = overview.getByRole("navigation", { name: "子任务分页" });
  await pagination.getByRole("button", { name: "下一页" }).click();
  await pagination.getByRole("button", { name: "下一页" }).click();
  const leaf = overview.getByRole("link", { name: "30 子任务 leaf-20", exact: true });
  await leaf.scrollIntoViewIfNeeded();
  const y = await page.evaluate(() => window.scrollY);
  await leaf.click();
  await expect(page.getByRole("heading", { level: 1, name: "30 子任务 leaf-20" })).toBeVisible();
  await page.goBack();
  await expect(overview.locator(".epic-leaves li")).toHaveCount(8);
  await expect(page).toHaveURL(/leaf_offset=16/);
  await expect(leaf).toBeVisible();
  await pagination.getByRole("button", { name: "下一页" }).click();
  await expect(overview.locator(".epic-leaves li").last()).toContainText("已取消 · 必需范围仍保留");
  await expect(overview).toContainText("0 / 30 项必需子任务满足父级条件");
});

test("长进展默认折叠且可查看完整事实", async ({ page }) => {
  await open(page, "/tasks/epic-8?section=overview");
  const overview = page.getByLabel("大任务进展");
  await overview.getByRole("button", { name: /查看最近 .* 条进展/ }).click();
  const fact = overview.locator(".epic-events li").filter({ hasText: "长事实前缀" });
  await expect(fact).toHaveCount(1);
  expect((await fact.locator(".fact-preview").innerText()).length).toBeLessThanOrEqual(180);
  await expect(fact.locator(".fact-full")).toBeHidden();
  await fact.locator("details summary").click();
  await expect(fact.locator(".fact-full")).toBeVisible();
  await expect(fact.locator(".fact-full")).toContainText("实际结尾");
});

test("子任务完成及时更新汇总，取消与新增保留范围记录", async ({ page, request }) => {
  const c = await client(request);
  await open(page, "/tasks/epic-8?section=overview");
  const overview = page.getByLabel("大任务进展");
  const accepted = overview.locator(".epic-counts div").filter({ hasText: "已验收" });
  await expect(accepted.locator("dd")).toHaveText("1");
  await c.post("/api/action", { action: "verify", id: "e8-implemented", unit: "proof" });
  await expect(accepted.locator("dd")).toHaveText("2", { timeout: 15000 });

  await open(page, "/tasks/epic-1?section=overview");
  const one = page.getByLabel("大任务进展");
  await expect(one).toContainText("必需 1");
  await c.post("/api/action", { action: "cancel", id: "e1-leaf-00", reason: "隔离范围调整" });
  await expect(one).toContainText("已取消 · 必需范围仍保留", { timeout: 15000 });
  await expect(one).toContainText("必需 1");
  await c.post("/api/tasks", { id: "e1-new", parent: "epic-1", title: "新增必要子项", type: "bug", goal: "verified", acceptance: ["实际结果存在"] });
  await expect(one).toContainText("必需 2", { timeout: 15000 });
  await one.locator("details").filter({ hasText: "查看范围变化" }).locator("summary").click();
  await expect(one).toContainText("必需分母不变");
  await expect(one).toContainText("隔离范围调整");
  await expect(one).toContainText("必需分母 +1");
  await shot(page, "epic-scope-changes");
});

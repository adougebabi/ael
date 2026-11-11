import { test, expect, type APIRequestContext } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const manifestPath = process.env.AEL_STAGNATION_FIXTURE;
if (!manifestPath) throw new Error("AEL_STAGNATION_FIXTURE is required");
const fixture = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const out = path.resolve(fixture.out);
if (!fixture.isolated || !fs.existsSync(path.join(out, "ISOLATED-STAGNATION")) ||
    !path.resolve(fixture.root).startsWith(out + path.sep) || !path.resolve(fixture.runtime).startsWith(out + path.sep)) {
  throw new Error("refusing a non-isolated fixture");
}
const output = process.env.AEL_UI_OUT || "/tmp/ael-stagnation-browser";
fs.mkdirSync(output, { recursive: true });

async function client(request: APIRequestContext) {
  const session = await request.get(fixture.url + "/api/session");
  expect(session.ok()).toBeTruthy();
  const headers = { Authorization: "Bearer " + (await session.json()).token };
  return {
    async get(endpoint: string) {
      const r = await request.get(fixture.url + endpoint, { headers });
      expect(r.ok(), await r.text()).toBeTruthy();
      return r.json();
    },
    async post(data: unknown) {
      const r = await request.post(fixture.url + "/api/action", { headers, data });
      expect(r.ok(), await r.text()).toBeTruthy();
      return r.json();
    },
  };
}

test("区分无人接手与声明中的长阶段", async ({ page, request }) => {
  const c = await client(request);
  const info = await c.get("/api/info");
  expect(info.project).toBe(fixture.root);
  expect(info.service_version).toBe(fixture.version);
  expect(info.service_build).toBe(fixture.build);
  await page.goto(fixture.url + "/#/tasks/epic?section=overview");
  const overview = page.getByLabel("大任务进展");
  await expect(overview).toContainText("隔离无人接手");
  await expect(overview).toContainText("超过设定时间未记录新的证据进展");
  await expect(overview).toContainText("当前无人执行");
  const reported = overview.locator("details").filter({ hasText: "查看未标记为成果的记录" });
  await reported.locator("summary").click();
  await expect(reported).toContainText("保留的旧式描述");
  await expect(reported).toContainText("未计为有证据的进展");
  await page.goto(fixture.url + "/#/tasks/long?section=overview");
  await expect(page.locator(".result-banner")).toContainText("长阶段仍在期限内，未自动停止");
  await expect(page.locator(".result-banner")).toContainText("integration-test");
  const task = await c.get("/api/task?id=long");
  expect(task.health.no_progress).toBe(true);
  expect(task.health.healthy_long).toBe(true);
  expect(task.task.paused).toBe(false);
  await page.screenshot({ path: path.join(output, "declared-long-stage.png"), fullPage: true });
});

test("父子页面完成限定复核、再次失败停止和新证据恢复", async ({ page, request }) => {
  const c = await client(request);
  await page.goto(fixture.url + "/#/tasks/epic?section=overview");
  await page.getByLabel("大任务进展").locator(".epic-leaves").getByRole("link", { name: "隔离重复失败", exact: true }).click();
  await expect(page.locator(".result-banner")).toContainText("同单元失败 3 次");
  await expect(page.locator(".result-banner")).toContainText("失败输入：");
  await page.screenshot({ path: path.join(output, "failure-stop.png"), fullPage: true });

  async function fillReview(ref: string) {
    await page.getByRole("button", { name: "记录根因复核", exact: true }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("执行者 / 决策者").fill("fixture-reviewer");
    await dialog.getByLabel("已尝试").fill("核对三次失败输入");
    await dialog.getByLabel("已排除").fill("排除文件缺失");
    await dialog.getByLabel("根因假设").fill("隔离配置不符");
    await dialog.getByLabel("下一动作").fill("核对配置后复测");
    await dialog.getByLabel("解除条件").fill("配置已核对");
    await dialog.getByLabel("复核证据（逗号分隔）").fill(ref);
    await dialog.getByRole("button", { name: "确认记录根因复核" }).click();
    return dialog;
  }
  let dialog = await fillReview("runtime:review.txt");
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "恢复任务", exact: true }).first().click();
  dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "确认恢复任务" }).click();
  await expect(dialog).toBeHidden();
  let current = await c.get("/api/task?id=stuck");
  expect(current.task.paused).toBe(false);
  expect(current.task.failure_cycles.define.count).toBe(3);
  const claimed = await c.post({ action: "claim", id: "stuck", executor: "fixture-after-review" });
  await c.post({ action: "submit", submission: { task: "stuck", attempt: claimed.attempt.id, token: claimed.attempt.token,
    key: crypto.randomUUID(), result: { status: "fail", summary: "复核后仍失败" } } });
  await expect(page.locator(".result-banner")).toContainText("复核后仍未收敛，已停止", { timeout: 15000 });
  await page.screenshot({ path: path.join(output, "failure-blocked.png"), fullPage: true });
  dialog = await fillReview("runtime:review.txt");
  await expect(dialog.getByRole("alert")).toContainText("需要保留新的证据");
  await dialog.getByLabel("复核证据（逗号分隔）").fill("runtime:review-2.txt");
  await dialog.getByRole("button", { name: "确认记录根因复核" }).click();
  await expect(dialog).toBeHidden();
  current = await c.get("/api/task?id=stuck");
  expect(current.task.failure_cycles.define.count).toBe(4);
  expect(current.task.failure_cycles.define.state).toBe("reviewed");
});

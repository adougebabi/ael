import { test, expect, type APIRequestContext } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const manifestPath = process.env.AEL_EPIC_INTEGRATION_FIXTURE;
if (!manifestPath) throw new Error("AEL_EPIC_INTEGRATION_FIXTURE is required");
const fixture = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const out = path.resolve(fixture.out);
if (!fixture.isolated || !fs.existsSync(path.join(out, "ISOLATED-EPIC-INTEGRATION"))) throw new Error("refusing a non-isolated fixture");
for (const project of Object.values(fixture.projects) as any[]) {
  if (!path.resolve(project.root).startsWith(out + path.sep) || !path.resolve(project.runtime).startsWith(out + path.sep)) throw new Error("fixture project escaped its isolated directory");
}
const output = process.env.AEL_UI_OUT || "/tmp/ael-epic-integration-browser";
fs.mkdirSync(output, { recursive: true });

async function client(request: APIRequestContext, name: string) {
  const project = fixture.projects[name];
  const session = await request.get(project.url + "/api/session");
  expect(session.ok()).toBeTruthy();
  const headers = { Authorization: "Bearer " + (await session.json()).token };
  return {
    async get(endpoint: string) {
      const r = await request.get(project.url + endpoint, { headers });
      expect(r.ok(), await r.text()).toBeTruthy();
      return r.json();
    },
    async post(endpoint: string, data: unknown) {
      const r = await request.post(project.url + endpoint, { headers, data });
      expect(r.ok(), await r.text()).toBeTruthy();
      return r.json();
    },
  };
}

test("三叶单测通过但集成失败，定点补齐并在父候选复验", async ({ page, request }) => {
  const project = fixture.projects.three, c = await client(request, "three");
  const info = await c.get("/api/info");
  expect(info.project).toBe(project.root);
  expect(info.service_build).toBe(fixture.build);
  const before: Record<string, any> = {};
  for (const id of ["tri-1", "tri-2", "tri-3"]) before[id] = (await c.get("/api/task?id=" + id)).task;
  await page.goto(project.url + "/#/tasks/tri-epic?section=overview");
  const overview = page.getByLabel("大任务进展");
  await expect(overview).toContainText("3 个叶子");
  await expect(overview.locator(".epic-counts div").filter({ hasText: "已集成" }).locator("dd")).toHaveText("0");
  await overview.getByRole("link", { name: /隔离三叶集成.*检查 integration/ }).click();
  await expect(page).toHaveURL(/section=checks.*check=integration/);
  const failed = page.locator("#task-check-integration");
  await expect(failed).toContainText("关联排查任务");
  await expect(failed).toContainText("tri-2");
  await expect(failed.locator(".evidence")).toContainText("失败");
  await failed.getByRole("button", { name: /查看证据/ }).first().click();
  await expect(page.locator(".artifact-panel pre")).toContainText("isolated integration check");
  await page.screenshot({ path: path.join(output, "tri-integration-failure.png"), fullPage: true });
  await page.locator(".artifact-panel").getByRole("button", { name: "关闭证据" }).click();

  await c.post("/api/tasks", { id: "bridge", parent: "tri-epic", title: "隔离桥接修复", type: "bug", goal: "verified", scope: ["bridge.txt"], acceptance: ["桥接存在"],
    checks: [{ id: "unit", kind: "functional", method: "command", command: ["sh", "-c", "test -s bridge.txt"], covers: ["桥接存在"], required: true }] });
  for (const stage of ["define", "execute"]) {
    const claim = await c.post("/api/action", { action: "claim", id: "bridge", executor: "fixture-bridge" });
    await c.get("/api/context?id=bridge");
    const artifacts: string[] = [];
    if (stage === "execute") {
      fs.writeFileSync(path.join(project.root, "bridge.txt"), "isolated bridge\n");
      artifacts.push("bridge.txt");
    }
    await c.post("/api/action", { action: "submit", submission: { task: "bridge", attempt: claim.attempt.id, token: claim.attempt.token,
      key: crypto.randomUUID(), result: { status: "pass", summary: "隔离桥接 " + stage, artifacts,
        knowledge: { status: "unchanged", reason: "隔离夹具", artifacts: [] } } } });
  }
  await c.post("/api/action", { action: "verify", id: "bridge", unit: "unit" });
  await page.getByRole("button", { name: "概览", exact: true }).click();
  await expect(page.getByRole("button", { name: "准备整体验收", exact: true }).first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "准备整体验收", exact: true }).first().click();
  let dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "确认准备整体验收" }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "验收与问题", exact: true }).click();
  async function runCheck(id: string) {
    const check = page.locator("#task-check-" + id);
    await check.getByRole("button", { name: "运行真实检查" }).click();
    dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "确认运行检查" }).click();
    await expect(dialog).toBeHidden();
  }
  await runCheck("integration");
  expect((await c.get("/api/task?id=tri-epic")).task.status).toBe("open");
  await runCheck("recheck-tri-2");
  const done = (await c.get("/api/task?id=tri-epic")).task;
  expect(done.status).toBe("completed");
  expect(done.result).toBe("verified");
  for (const id of ["tri-1", "tri-2", "tri-3"]) {
    const after = (await c.get("/api/task?id=" + id)).task;
    expect(after.completed).toBe(before[id].completed);
    expect(after.candidate).toBe(before[id].candidate);
    expect(after.attempts.length).toBe(before[id].attempts.length);
    expect(after.evidence).toEqual(before[id].evidence);
  }
  await page.screenshot({ path: path.join(output, "tri-integration-complete.png"), fullPage: true });
});

test("八叶从计划到 Git 候选、整体验收及反馈", async ({ page, request }) => {
  const project = fixture.projects.eight, c = await client(request, "eight");
  const info = await c.get("/api/info");
  expect(info.project).toBe(project.root);
  expect(info.service_build).toBe(fixture.build);
  const tree = await c.get("/api/task-tree?id=eight-epic");
  expect(tree.required_completed).toBe(8);
  expect(tree.summary.integrated).toBe(8);
  expect(tree.root.task.status).toBe("completed");
  expect(tree.root.task.result).toBe("verified");
  expect(tree.root.task.integration.git_head).toBeTruthy();
  expect(tree.root.task.integration.project_id).toBeTruthy();
  const completed = tree.root.task.completed;
  await page.goto(project.url + "/#/tasks/eight-epic?section=overview");
  const overview = page.getByLabel("大任务进展");
  await expect(overview).toContainText("8 个叶子");
  await expect(overview.locator(".epic-counts div").filter({ hasText: "已集成" }).locator("dd")).toHaveText("8");
  await expect(overview).toContainText("Git 提交");
  await overview.getByRole("button", { name: "查看依据" }).click();
  await expect(page.locator(".artifact-panel pre")).toContainText("各模块独立");
  await page.locator(".artifact-panel").getByRole("button", { name: "关闭证据" }).click();
  const related = page.getByRole("region", { name: "任务关系" });
  await expect(related).toContainText("后续反馈");
  await related.getByRole("link", { name: "隔离后续反馈" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "隔离后续反馈" })).toBeVisible();
  await page.goBack();
  expect((await c.get("/api/task?id=eight-epic")).task.completed).toBe(completed);
  await page.screenshot({ path: path.join(output, "eight-complete.png"), fullPage: true });
});

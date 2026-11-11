import { test, expect } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const manifestPath = process.env.AEL_STANDALONE_FIXTURE;
if (!manifestPath) throw new Error("AEL_STANDALONE_FIXTURE is required");
const fixture = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const output = path.resolve(fixture.out);
if (!fixture.isolated || !fs.existsSync(path.join(output, "ISOLATED-STANDALONE"))) throw new Error("refusing non-isolated fixture");
const browserOutput = process.env.AEL_UI_OUT || "/tmp/ael-standalone-browser";
fs.mkdirSync(browserOutput, { recursive: true });

async function login(page: import("@playwright/test").Page, username: string, password: string, navigate = true) {
  if (navigate) await page.goto(fixture.url);
  await expect(page.getByRole("heading", { name: "登录工作台" })).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "项目", exact: true })).toBeVisible();
}

if (fixture.phase === "initial") {
  test("浏览器登录、项目授权与令牌撤销", async ({ page, request }) => {
    expect((await request.get(fixture.url + "/api/v1/projects")).status()).toBe(401);
    await login(page, "owner", "fixture-admin-password");
    const project = page.locator("section").filter({ has: page.getByRole("heading", { name: "注册项目" }) });
    for (const [id, name] of [["private", "Private Project"], ["shared", "Shared Project"]]) {
      await project.getByLabel("项目 ID").fill(id);
      await project.getByLabel("项目名称").fill(name);
      await project.getByRole("button", { name: "注册项目" }).click();
      await expect(page.getByLabel("项目列表")).toContainText(name);
    }
    const user = page.locator("section").filter({ has: page.getByRole("heading", { name: "创建账号" }) });
    await user.getByLabel("用户名").fill("worker");
    await user.getByLabel("初始密码").fill("fixture-worker-password");
    await user.getByRole("button", { name: "创建账号" }).click();
    await expect(page.getByRole("status")).toContainText("账号已创建");
    const member = page.locator("section").filter({ has: page.getByRole("heading", { name: "设置项目成员" }) });
	await member.getByRole("combobox", { name: "项目", exact: true }).selectOption("shared");
    await member.getByLabel("用户名").fill("worker");
	await member.getByRole("combobox", { name: "角色", exact: true }).selectOption("executor");
    await member.getByRole("button", { name: "保存成员" }).click();
    await expect(page.getByRole("status")).toContainText("成员已更新");
    const issue = page.locator("section").filter({ has: page.getByRole("heading", { name: "创建 Agent 令牌" }) });
	await issue.getByRole("combobox", { name: "项目", exact: true }).selectOption("shared");
    await issue.getByLabel("成员用户名").fill("worker");
    await issue.getByLabel("用途").fill("fixture runner");
    await issue.getByRole("button", { name: "创建令牌" }).click();
    const token = await issue.getByLabel("新令牌").inputValue();
    expect(token).toMatch(/^ael_/);
    const headers = { Authorization: `Bearer ${token}` };
    expect((await request.get(fixture.url + "/api/v1/projects/shared", { headers })).status()).toBe(200);
    expect((await request.get(fixture.url + "/api/v1/projects/private", { headers })).status()).toBe(404);
    expect((await request.post(fixture.url + "/api/v1/projects", { headers: { ...headers, "X-AEL-Protocol": "1" }, data: { id: "forged", name: "forged", executor: "owner" } })).status()).toBe(403);
    const tokenList = page.locator("section").filter({ has: page.getByRole("heading", { name: "Agent 令牌", exact: true }) });
    await tokenList.getByRole("button", { name: "撤销令牌" }).click();
    await expect(tokenList).toContainText("已撤销");
    expect((await request.get(fixture.url + "/api/v1/projects/shared", { headers })).status()).toBe(401);
    await page.screenshot({ path: path.join(browserOutput, "admin-projects.png"), fullPage: true });
    await page.getByRole("button", { name: "退出登录" }).click();
    await expect(page.getByRole("heading", { name: "登录工作台" })).toBeVisible();
    await login(page, "owner", "fixture-admin-password");
    const privateRow = page.getByLabel("项目列表").getByRole("listitem").filter({ hasText: "Private Project" });
    await privateRow.getByRole("button", { name: "查看项目" }).click();
    await expect(page.locator(".standalone-selection")).toContainText("Private Project");
    await page.evaluate(async () => {
      const current = await (await fetch("/api/v1/auth/session")).json();
      await fetch("/api/v1/auth/logout", { method: "POST", headers: { "Content-Type": "application/json", "X-AEL-Protocol": "1", "X-AEL-CSRF": current.csrf }, body: "{}" });
    });
    await page.getByRole("button", { name: "刷新" }).click();
    await expect(page.getByRole("heading", { name: "登录工作台" })).toBeVisible();
    await login(page, "worker", "fixture-worker-password", false);
    await expect(page.getByLabel("项目列表")).toContainText("Shared Project");
    await expect(page.getByLabel("项目列表")).not.toContainText("Private Project");
    await expect(page.locator("main")).not.toContainText("Private Project");
    await expect(page.getByRole("heading", { name: "注册项目" })).toHaveCount(0);
    await page.screenshot({ path: path.join(browserOutput, "worker-projects.png"), fullPage: true });
    await page.getByRole("button", { name: "退出登录" }).click();
    await login(page, "owner", "fixture-admin-password", false);
    await page.getByLabel("项目列表").getByRole("listitem").filter({ hasText: "Private Project" }).getByRole("button", { name: "查看项目" }).click();
    await expect(page.locator(".standalone-selection")).toContainText("Private Project");
    const switched = await page.evaluate(async () => {
      return (await fetch("/api/v1/auth/login", { method: "POST", headers: { "Content-Type": "application/json", "X-AEL-Protocol": "1" }, body: JSON.stringify({ username: "worker", password: "fixture-worker-password" }) })).status;
    });
    expect(switched).toBe(200);
    await page.getByRole("button", { name: "刷新" }).click();
    await expect(page.getByRole("heading", { name: "登录工作台" })).toBeVisible();
    await page.reload();
    await expect(page.getByLabel("项目列表")).toContainText("Shared Project");
    await expect(page.locator("main")).not.toContainText("Private Project");
  });
} else if (fixture.phase === "restart") {
  test("Server 重启后保留数据并同步跨标签会话", async ({ page, request, context }) => {
    expect((await request.get(fixture.url + "/api/v1/projects/private")).status()).toBe(401);
    await login(page, "owner", "fixture-admin-password");
    await expect(page.getByLabel("项目列表")).toContainText("Private Project");
    await expect(page.getByLabel("项目列表")).toContainText("Shared Project");
    await expect(page.locator("section").filter({ has: page.getByRole("heading", { name: "Agent 令牌", exact: true }) })).toContainText("已撤销");
    await page.screenshot({ path: path.join(browserOutput, "after-restart.png"), fullPage: true });
    await page.getByLabel("项目列表").getByRole("listitem").filter({ hasText: "Private Project" }).getByRole("button", { name: "查看项目" }).click();
    await expect(page.locator(".standalone-selection")).toContainText("Private Project");
    const otherTab = await context.newPage();
    await otherTab.goto(fixture.url);
    await expect(otherTab.getByRole("heading", { name: "项目", exact: true })).toBeVisible();
    await otherTab.getByRole("button", { name: "退出登录" }).click();
    await login(otherTab, "worker", "fixture-worker-password", false);
    await expect(page.getByRole("heading", { name: "登录工作台" })).toBeVisible();
    await expect(page.locator("main")).not.toContainText("Private Project");
    await page.reload();
    await expect(page.getByLabel("项目列表")).toContainText("Shared Project");
    await expect(page.locator("main")).not.toContainText("Private Project");
    await otherTab.close();
  });
} else {
  throw new Error("invalid fixture phase");
}

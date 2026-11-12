import { test, expect, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
const fixture = JSON.parse(fs.readFileSync(process.env.AEL_REVIEW_FIXTURE!, "utf8"));
let ownerCookies: any[] = [];
async function login(page: Page, user = "owner", password = "fixture-admin-password") {
  if (user === "owner" && ownerCookies.length) {
    await page.context().addCookies(ownerCookies);
    await page.goto(fixture.url + "/#/tasks?project=shared");
    await expect(page.getByRole("heading", { name: "工作台", exact: true })).toBeVisible();
    return;
  }
  await page.goto(fixture.url + "/#/tasks?project=shared");
  await page.getByLabel("用户名", { exact: true }).fill(user);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByLabel("项目列表").getByRole("listitem").filter({ hasText: "Review shared" }).getByRole("button", { name: "查看项目", exact: true }).click();
  if (user === "owner") ownerCookies = await page.context().cookies();
}
async function open(page: Page, task: string) {
  await page.goto(fixture.url + "/#/tasks/" + task + "?project=shared&section=checks");
  await page.getByRole("button", { name: "记录人工验收", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("正在读取当前候选和检查…", { exact: true })).toHaveCount(0);
  const newOpinion = dialog.getByRole("button", { name: "记录新的验收意见", exact: true });
  if (await newOpinion.isVisible()) await newOpinion.click();
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toBeVisible();
  return dialog;
}

test("明确基准、键盘比较、丢失响应和幂等人工回执", async ({ page }) => {
  await login(page);
  const dialog = await open(page, "browser");
  const compare = dialog.getByRole("region", { name: "证据图片比较" });
  await expect(compare.getByLabel("基准图片", { exact: true })).toHaveValue("");
  await expect(dialog.getByRole("radio", { name: "通过", exact: true })).not.toBeChecked();
  await compare.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.browser.actual);
  await compare.getByLabel("叠加比较", { exact: true }).check();
  await expect(compare.getByRole("img", { name: "实际图片", exact: true })).toBeVisible();
  await expect(compare.getByLabel("基准图片", { exact: true })).toHaveValue("");
  await compare.getByLabel("基准图片", { exact: true }).selectOption(fixture.tasks.browser.baseline);
  await compare.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.browser.actual);
  await compare.getByLabel("叠加比较", { exact: true }).uncheck();
  await expect(compare.getByRole("img", { name: "基准图片", exact: true })).toBeVisible();
  await expect(compare.getByRole("img", { name: "实际图片", exact: true })).toBeVisible();
  await expect(compare).toContainText("client-b");
  await expect(compare).toContainText("explicit-design-reference");
  const zoom = compare.getByRole("slider", { name: "缩放" });
  await zoom.focus(); await page.keyboard.press("ArrowRight");
  await expect(zoom).toHaveValue("125");
  await compare.getByLabel("叠加比较", { exact: true }).check();
  await expect(compare.getByRole("img", { name: "叠加实际图片" })).toBeVisible();
  await compare.getByRole("button", { name: "添加问题定位" }).click();
  await compare.getByLabel("定位横坐标 %").fill("25");
  await expect(compare.getByLabel("问题定位")).toBeVisible();
  const download = await page.request.get(fixture.url + "/api/v1/projects/shared/artifacts/" + fixture.tasks.browser.baseline + "/content");
  expect(download.ok()).toBeTruthy();
  expect(crypto.createHash("sha256").update(await download.body()).digest("hex")).toBe(crypto.createHash("sha256").update(fs.readFileSync(path.join(fixture.out, "baseline.png"))).digest("hex"));
  await page.screenshot({ path: path.join(fixture.out, "comparison-overlay.png"), fullPage: true });
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由", { exact: false }).fill("RETAINED_REVIEW_OBSERVATION");
  await dialog.getByRole("checkbox", { name: "我确认以上结论针对所示候选和修订。" }).check();
  let accepted = false;
  await page.route("**/api/v1/projects/shared/reviews", async route => {
    const response = await route.fetch(); expect(response.ok()).toBeTruthy(); accepted = true;
    await route.abort();
  }, { times: 1 });
  await dialog.getByRole("button", { name: "提交验收结果", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "原样重试提交" })).toBeVisible();
  expect(accepted).toBeTruthy();
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveValue("RETAINED_REVIEW_OBSERVATION");
  await dialog.getByRole("button", { name: "原样重试提交" }).evaluate((button: HTMLButtonElement) => { button.click(); button.click(); });
  await expect(dialog.getByRole("heading", { name: "任务仍有后续工作" })).toBeVisible();
  await expect(dialog).toContainText("1 / 2 项通过");
  const state = await (await page.request.get(fixture.url + "/api/v1/projects/shared/tasks/browser")).json();
  expect(state.task.evidence.filter((item: any) => item.check === "visual")).toHaveLength(1);
  expect(state.task.evidence[0].comparison.baseline.artifact_id).toBe(fixture.tasks.browser.baseline);
  await dialog.getByRole("button", { name: "查看任务结果", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Review browser", exact: true })).toBeVisible();
});

test("新候选拒绝旧表单且保留输入及图片选择", async ({ page, request }) => {
  await login(page);
  let dialog = await open(page, "stale");
  await dialog.getByRole("button", { name: "引用这条意见", exact: true }).click();
  await dialog.getByLabel("基准图片", { exact: true }).selectOption(fixture.tasks.stale.baseline);
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.stale.actual);
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由", { exact: false }).fill("KEEP_OLD_VERSION_INPUT");
  await dialog.getByRole("checkbox", { name: "我确认以上结论针对所示候选和修订。" }).check();
  const changed = await request.post(fixture.control, { data: { action: "change-candidate", nonce: fixture.nonce }, timeout: 60000 });
  expect(changed.ok(), await changed.text()).toBeTruthy();
  await dialog.getByRole("button", { name: "提交验收结果", exact: true }).click();
  await expect(dialog.locator("pre")).toContainText("REVIEW_VERSION_MISMATCH");
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveValue("KEEP_OLD_VERSION_INPUT");
  await dialog.getByRole("button", { name: "重新读取版本并保留输入" }).click();
  await expect(dialog.getByRole("checkbox", { name: "我确认以上结论针对所示候选和修订。" })).not.toBeChecked();
  await expect(dialog.getByLabel("实际图片", { exact: true })).toHaveValue(fixture.tasks.stale.actual);
  await expect(dialog).toContainText("历史候选");
  await expect(dialog).toContainText("候选、定义或计划已变化，请重新记录意见。");
  await expect(dialog.getByRole("button", { name: "引用这条意见", exact: true })).toHaveCount(0);
  await page.keyboard.press("Escape");
  dialog = await open(page, "stale");
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveValue("KEEP_OLD_VERSION_INPUT");
  await expect(dialog.getByLabel("基准图片", { exact: true })).toHaveValue(fixture.tasks.stale.baseline);
  await page.screenshot({ path: path.join(fixture.out, "stale-review-retained.png"), fullPage: true });
});

test("缺失、清理及未完成上传分别显示并阻止错误验收", async ({ page }) => {
  await login(page);
  const dialog = await open(page, "missing");
  await dialog.getByLabel("基准图片", { exact: true }).selectOption(fixture.tasks.missing.baseline);
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.missing.actual);
  await expect(dialog.getByRole("region", { name: "证据图片比较" })).toContainText("原件已清理");
  await expect(dialog.getByRole("region", { name: "证据图片比较" })).toContainText("原件缺失");
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.missing.unfinished);
  await expect(dialog.getByRole("region", { name: "证据图片比较" })).toContainText("上传未完成");
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由", { exact: false }).fill("Evidence unavailable");
  await dialog.getByRole("checkbox", { name: "我确认以上结论针对所示候选和修订。" }).check();
  await dialog.getByRole("button", { name: "提交验收结果", exact: true }).click();
  await expect(dialog.locator("pre")).toContainText("REVIEW_EVIDENCE_CHANGED");
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveValue("Evidence unavailable");
  await page.screenshot({ path: path.join(fixture.out, "unavailable-evidence.png"), fullPage: true });
});

test("只读会话可读图片并受到人工裁定权限保护", async ({ page }) => {
  await login(page, "observer", "fixture-observer-password");
  await page.goto(fixture.url + "/#/tasks/browser?project=shared&section=checks");
  await expect(page.getByRole("button", { name: "记录人工验收", exact: true })).toHaveCount(0);
  const session = await (await page.request.get(fixture.url + "/api/v1/auth/session")).json();
  const response = await page.request.post(fixture.url + "/api/v1/projects/shared/reviews", { headers: { Origin: fixture.url, "X-AEL-CSRF": session.csrf, "X-AEL-Protocol": "1" }, data: {} });
  expect(response.status()).toBe(403);
});

test("人工 blocked 后可精确恢复且保留原阻塞证据", async ({ page }) => {
  await login(page);
  await page.goto(fixture.url + "/#/tasks/api-blocked?project=shared&section=checks");
  await page.getByRole("button", { name: "记录人工验收", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "恢复并继续人工验收", exact: true }).click();
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toBeVisible();
  const state = await (await page.request.get(fixture.url + "/api/v1/projects/shared/tasks/api-blocked")).json();
  expect(state.task.paused).toBeFalsy();
  expect(state.task.evidence.filter((item: any) => item.result === "blocked")).toHaveLength(1);
  expect(state.task.status).toBe("open");
});

test("已提交回执之后版本再次变化时提示重新审阅", async ({ page, request }) => {
  await login(page);
  const dialog = await open(page, "postchange");
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.postchange.actual);
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由", { exact: false }).fill("Exact original candidate review");
  await dialog.getByRole("checkbox", { name: "我确认以上结论针对所示候选和修订。" }).check();
  await page.route("**/api/v1/projects/shared/reviews", async route => {
    const response = await route.fetch(); expect(response.ok()).toBeTruthy();
    const changed = await request.post(fixture.control, { data: { action: "change-definition", nonce: fixture.nonce }, timeout: 60000 });
    expect(changed.ok(), await changed.text()).toBeTruthy();
    await route.fulfill({ response });
  }, { times: 1 });
  await dialog.getByRole("button", { name: "提交验收结果", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("当前候选或修订在本次记录后发生变化");
  await expect(dialog.getByRole("heading", { name: "任务仍有后续工作" })).toBeVisible();
});

test("慢于轮询周期的原件读取仍能显示比较图片", async ({ page }) => {
  await login(page);
  const dialog = await open(page, "slow");
  await page.route("**/dashboard/artifact?**", async route => {
    const response = await route.fetch();
    await new Promise(resolve => setTimeout(resolve, 6000));
    await route.fulfill({ response });
  });
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.slow.actual);
  await expect(dialog.getByRole("img", { name: "实际图片", exact: true })).toBeVisible({ timeout: 30000 });
  await page.unroute("**/dashboard/artifact?**");
});

test("读取项目身份失败时不使用空命名空间保存草稿", async ({ page }) => {
  await login(page);
  await page.goto(fixture.url + "/#/tasks/slow?project=shared&section=checks");
  const entry = page.getByRole("button", { name: "记录人工验收", exact: true });
  await expect(entry).toBeVisible();
  await page.route("**/dashboard/info", route => route.abort(), { times: 1 });
  await entry.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("无法核对当前候选");
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveCount(0);
  await dialog.getByRole("button", { name: "重新读取", exact: true }).click();
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toBeVisible();
});

import { test, expect, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
const fixture = JSON.parse(fs.readFileSync(process.env.AEL_FINDING_LIFECYCLE_FIXTURE!, "utf8"));
let ownerCookies: any[] = [];
async function login(page: Page, user = "owner", password = "fixture-admin-password") {
  if (user === "owner" && ownerCookies.length) {
    await page.context().addCookies(ownerCookies);
    await page.goto(fixture.url + "/#/tasks?project=shared");
    await expect(page.locator(".wb-sidebar")).toBeVisible();
    return;
  }
  await page.goto(fixture.url + "/#/tasks?project=shared");
  await page.getByLabel("用户名", { exact: true }).fill(user);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  // The project in the link opens directly in the workbench shell.
  await expect(page.locator(".wb-sidebar")).toBeVisible();
  if (user === "owner") ownerCookies = await page.context().cookies();
}
async function openFinding(page: Page, task: string, finding: string) {
  await page.goto(fixture.url + "/#/tasks/" + task + "?project=shared&section=findings&finding=" + finding);
  const actions = page.getByRole("region", { name: "发现裁定与复验" });
  await expect(actions).toBeVisible();
  return actions;
}
async function confirm(actions: ReturnType<Page["getByRole"]>, reason: string) {
  await actions.getByLabel("裁定或操作理由", { exact: true }).fill(reason);
  await actions.getByRole("checkbox", { name: "我已核对所示发现、来源及版本。" }).check();
}
async function sessionHeaders(page: Page) {
  const session = await (await page.request.get(fixture.url + "/api/v1/auth/session")).json();
  return { Origin: fixture.url, "X-AEL-CSRF": session.csrf, "X-AEL-Protocol": "1" };
}

test("发现修复经过真实外部实施和复验，旧失败与复发可追溯", async ({ page, request }) => {
  const control = async (action: string) => {
    const response = await request.post(fixture.control, { data: { action, nonce: fixture.nonce }, timeout: 90000 });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  await login(page);
  let actions = await openFinding(page, fixture.scan, fixture.finding);
  await expect(actions).toContainText("待实施修复");
  await control("implement-b");
  await expect(actions).toContainText("待有效复验", { timeout: 15000 });
  await actions.getByRole("button", { name: "记录新候选复验", exact: true }).click();
  await expect(actions).toContainText("需要关联修复任务在新候选上提供当前有效的通过证据");
  await confirm(actions, "WAIT_FOR_ACTUAL_RETEST");
  await actions.getByRole("button", { name: "确认有效复验", exact: true }).click();
  await expect(actions.getByRole("alert")).toContainText("需要关联修复任务");
  await control("verify-b");
  await actions.getByRole("button", { name: "重新读取版本并保留输入", exact: true }).click();
  await expect(actions.getByLabel("裁定或操作理由", { exact: true })).toHaveValue("WAIT_FOR_ACTUAL_RETEST");
  await confirm(actions, "Actual scenario evidence verifies candidate B");
  await actions.getByRole("button", { name: "确认有效复验", exact: true }).click();
  await expect(actions).toContainText("操作已记录 · 已复验通过");
  await actions.getByRole("button", { name: "返回发现记录", exact: true }).click();
  const stateB = await (await page.request.get(fixture.url + "/api/v1/projects/shared/tasks/" + fixture.repair)).json();
  expect(stateB.task.status).toBe("open");
  await page.screenshot({ path: path.join(fixture.out, "revalidated-b-with-remaining-gate.png"), fullPage: true });

  await control("fail-ready");
  await control("implement-c");
  await control("verify-c");
  await expect(actions).toContainText("历史版本复验", { timeout: 15000 });
  await actions.getByRole("button", { name: "记录新候选复验", exact: true }).click();
  await actions.getByRole("button", { name: "重新读取版本并保留输入", exact: true }).click();
  await expect(actions).toContainText("关联检查已在所示新候选上提供有效通过证据。");
  await confirm(actions, "Actual scenario evidence verifies candidate C");
  await actions.getByRole("button", { name: "确认有效复验", exact: true }).click();
  await expect(actions).toContainText("操作已记录 · 已复验通过");
  await actions.getByRole("button", { name: "返回发现记录", exact: true }).click();
  await control("complete-repair");
  await control("group-evolution");

  const evolution = await (await page.request.get(fixture.url + "/api/v1/projects/shared/dashboard/evolution")).json();
  const grouped = evolution.items.find((item: any) => item.id === "knowledge:lifecycle-decision");
  expect(grouped.references.some((item: any) => item.kind === "finding" && item.id === fixture.finding)).toBeTruthy();
  const tests = grouped.delivery_facts.filter((item: any) => item.finding_id === fixture.finding);
  expect(tests).toHaveLength(2);
  expect(new Set(tests.map((item: any) => item.candidate_id)).size).toBe(2);
  await page.goto(fixture.url + "/#/evolution/" + encodeURIComponent(grouped.id) + "?project=shared");
  await expect(page.getByRole("heading", { name: "Lifecycle repair decision", exact: true })).toBeVisible();
  await expect(page.getByText(/^复验候选 /)).toHaveCount(2);
  await page.getByRole("link", { name: "Clipped save label", exact: true }).click();
  await expect(page.getByRole("region", { name: "结构化发现" })).toContainText("Measured scroll width exceeds client width");
  await page.screenshot({ path: path.join(fixture.out, "repair-and-revalidation-history.png"), fullPage: true });

  const recurrence = await control("recurrence");
  actions = await openFinding(page, fixture.repair, recurrence.finding);
  await expect(actions).toContainText(recurrence.repair);
  const related = page.getByRole("region", { name: "关联版本发现" });
  await expect(related).toContainText("resolved");
  await related.getByRole("link", { name: "Clipped save label", exact: true }).click();
  await expect(page).toHaveURL(new RegExp("finding=" + fixture.finding));
  const original = await (await page.request.get(fixture.url + "/api/v1/projects/shared/findings/" + fixture.finding)).json();
  expect(original.candidate_id).toBe(fixture.original_candidate);
  expect(original.status).toBe("resolved");
  expect(original.occurrences[0].input.actual).toBe("Measured scroll width exceeds client width");
  expect(original.history.filter((item: any) => item.kind === "finding_revalidated")).toHaveLength(2);
  await page.screenshot({ path: path.join(fixture.out, "recurrence-linked-to-old-evidence.png"), fullPage: true });
});

test("裁定保留未知请求、理由与精确版本，权衡继续保留任务门禁", async ({ page }) => {
  await login(page);
  let actions = await openFinding(page, fixture.scan, fixture.demo);
  await actions.getByRole("button", { name: "裁定发现", exact: true }).click();
  await expect(actions.getByLabel("裁定", { exact: true })).toHaveValue("");
  await actions.getByLabel("裁定", { exact: true }).selectOption("more_evidence");
  await confirm(actions, "RETAINED_UNKNOWN_REQUEST");
  await page.route("**/findings/" + fixture.demo + "/decisions", async route => {
    const response = await route.fetch(); expect(response.ok()).toBeTruthy();
    await route.abort();
  }, { times: 1 });
  await actions.getByRole("button", { name: "保存裁定", exact: true }).click();
  await expect(actions.getByRole("button", { name: "原样重试操作", exact: true })).toBeVisible();
  await page.reload();
  actions = page.getByRole("region", { name: "发现裁定与复验" });
  await expect(actions.getByLabel("裁定或操作理由", { exact: true })).toHaveValue("RETAINED_UNKNOWN_REQUEST");
  await actions.getByRole("button", { name: "原样重试操作", exact: true }).click();
  await expect(actions).toContainText("操作已记录 · 待补证");
  await actions.getByRole("button", { name: "返回发现记录", exact: true }).click();
  await actions.getByRole("button", { name: "裁定发现", exact: true }).click();
  await actions.getByLabel("裁定", { exact: true }).selectOption("accepted_tradeoff");
  await confirm(actions, "Explicit fixture tradeoff keeps mandatory task checks");
  await actions.getByRole("button", { name: "保存裁定", exact: true }).click();
  await expect(actions).toContainText("操作已记录 · 已记录权衡");
  await actions.getByRole("button", { name: "返回发现记录", exact: true }).click();
  const source = await (await page.request.get(fixture.url + "/api/v1/projects/shared/tasks/" + fixture.scan)).json();
  expect(source.task.result).toBe("report_delivered_with_findings");
  expect(source.task.evidence.every((item: any) => item.result === "fail")).toBeTruthy();

  await actions.getByRole("button", { name: "裁定发现", exact: true }).click();
  await actions.getByLabel("裁定", { exact: true }).selectOption("fix");
  await confirm(actions, "STALE_FINDING_REASON");
  const current = await (await page.request.get(fixture.url + "/api/v1/projects/shared/findings/" + fixture.demo)).json();
  const changed = await page.request.post(fixture.url + "/api/v1/projects/shared/findings/" + fixture.demo + "/decisions", { headers: await sessionHeaders(page), data: { finding_id: fixture.demo, expected_revision: current.revision, expected_task_revision: current.source_task_revision, action: "more_evidence", reason: "Concurrent actual decision", operation_id: "concurrent-decision", idempotency_key: "concurrent-decision" } });
  expect(changed.ok()).toBeTruthy();
  await actions.getByRole("button", { name: "保存裁定", exact: true }).click();
  await expect(actions.getByRole("alert")).toContainText("FINDING_REVISION_CONFLICT");
  await expect(actions.getByLabel("裁定或操作理由", { exact: true })).toHaveValue("STALE_FINDING_REASON");
  await actions.getByRole("button", { name: "重新读取版本并保留输入", exact: true }).click();
  await expect(actions.getByRole("checkbox", { name: "我已核对所示发现、来源及版本。" })).not.toBeChecked();
  await actions.getByRole("button", { name: "取消操作", exact: true }).click();
});

test("关联既有授权任务保留原定义并提供来源上下文", async ({ page, request }) => {
  const setup = await request.post(fixture.control, { data: { action: "prepare-existing", nonce: fixture.nonce }, timeout: 90000 });
  expect(setup.ok(), await setup.text()).toBeTruthy();
  await login(page);
  const actions = await openFinding(page, fixture.scan, fixture.link_demo);
  await actions.getByRole("button", { name: "关联已有修复任务", exact: true }).click();
  await actions.getByLabel("修复任务 ID", { exact: true }).fill("existing-repair");
  await actions.getByRole("button", { name: "读取关联任务", exact: true }).click();
  await expect(actions).toContainText("Existing authorized repair");
  await actions.getByLabel("对应检查", { exact: true }).selectOption("visual");
  await confirm(actions, "Reuse existing authorized repair scope and exact criterion");
  await actions.getByRole("button", { name: "确认关联修复任务", exact: true }).click();
  await expect(actions).toContainText("操作已记录");
  const context = await request.post(fixture.control, { data: { action: "linked-context", nonce: fixture.nonce }, timeout: 90000 });
  expect(context.ok(), await context.text()).toBeTruthy();
  const task = await (await page.request.get(fixture.url + "/api/v1/projects/shared/tasks/existing-repair")).json();
  expect(task.task.definition.source).toBe("existing-authorized-work");
  expect(task.task.definition.original_request || "").toBe("");
});

test("只读会话不能裁定，非 UUID 浏览器与草稿存储失败仍可操作授权裁定", async ({ page, browser }) => {
  await login(page, "observer", "fixture-observer-password");
  let actions = await openFinding(page, fixture.scan, fixture.demo);
  await expect(actions.getByRole("button", { name: "裁定发现", exact: true })).toHaveCount(0);
  const denied = await page.request.post(fixture.url + "/api/v1/projects/shared/findings/" + fixture.demo + "/decisions", { headers: await sessionHeaders(page), data: {} });
  expect(denied.status()).toBe(403);
  const context = await browser.newContext();
  await context.addCookies(ownerCookies);
  const operator = await context.newPage();
  await operator.addInitScript(() => {
    Object.defineProperty(globalThis.crypto, "randomUUID", { value: undefined });
    const get = Storage.prototype.getItem, set = Storage.prototype.setItem;
    Storage.prototype.getItem = function(key: string) { if (key.startsWith("ael-finding-action-v1:")) throw new DOMException("fixture storage blocked"); return get.call(this, key); };
    Storage.prototype.setItem = function(key: string, value: string) { if (key.startsWith("ael-finding-action-v1:")) throw new DOMException("fixture storage blocked"); return set.call(this, key, value); };
  });
  actions = await openFinding(operator, fixture.scan, fixture.demo);
  await actions.getByRole("button", { name: "裁定发现", exact: true }).click();
  await actions.getByLabel("裁定", { exact: true }).selectOption("fix");
  await confirm(actions, "Memory-only authorized decision");
  await actions.getByRole("button", { name: "保存裁定", exact: true }).click();
  await expect(actions).toContainText("操作已记录 · 待处理");
  await context.close();
});

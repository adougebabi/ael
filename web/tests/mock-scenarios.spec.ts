import { test, expect, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

// Task detail and diagnostics in mock-scenario mode. Data is captured from a
// real local Server (scripts/web_mock_capture.py); these tests check what the
// page makes of it and how its actions behave, not the Server itself.
const shots = process.env.AEL_MOCK_SHOTS || "";
if (shots) fs.mkdirSync(shots, { recursive: true });
const shot = async (page: Page, name: string) => { if (shots) await page.screenshot({ path: path.join(shots, name + ".png"), fullPage: true }); };
async function scenario(page: Page, id: string) {
  await page.goto("/?scenario=" + id);
  await expect(page.locator(".mock-bar-toggle")).toContainText("模拟数据");
}
const noOverflow = async (page: Page) => expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);

test("失败任务：概览直接给出卡点，日志在共享预览弹窗中打开并归还焦点", async ({ page }) => {
  await scenario(page, "failed");
  const stuck = page.locator(".wb-situation-stuck");
  await expect(stuck).toContainText("最近失败于 验收检查 · report-file");
  const trigger = stuck.getByRole("button", { name: "查看失败日志" });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: /AT-.*\.log/ });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("13,020.00");
  await shot(page, "failed-log-preview");
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await stuck.getByRole("link", { name: "在执行诊断中查看" }).click();
  await expect(page.locator(".diag-row.open")).toContainText("验收检查 · report-file");
});

test("执行诊断：任务状态与单次执行结果分开，失败执行默认展开，原始内容按需读取", async ({ page }) => {
  await scenario(page, "mixed-calls");
  await expect(page.locator(".diag-task-state")).toContainText("任务：待修复");
  const rows = page.locator(".diag-row");
  await expect(rows).toHaveCount(5);
  await expect(page.locator(".diag-row.tone-fail")).toHaveCount(2);
  await expect(page.locator(".diag-row.open")).toHaveCount(1);
  await expect(page.locator(".diag-row.open")).toContainText("执行失败");
  // The lease token is a credential and never appears in the raw record.
  await page.locator(".diag-row.open").getByText("完整原始记录").click();
  await expect(page.locator(".diag-row.open .wb-raw")).toContainText('"unit": "check:report-file"');
  await expect(page.locator(".diag-row.open .wb-raw")).not.toContainText("lease-");
  await page.getByText("原始事件").click();
  await expect(page.locator(".diag-events li")).toHaveCount(22);
  await shot(page, "diagnostics-mixed");
  // Keyboard: rows are buttons with aria-expanded.
  const second = rows.nth(1).locator(".diag-row-head");
  await second.focus();
  await page.keyboard.press("Enter");
  await expect(second).toHaveAttribute("aria-expanded", "true");
  await expect(page).toHaveURL(/attempt=AT-/);
});

test("执行成功但任务未完成：运行检查有进行中与成功反馈，任务状态随之变化", async ({ page }) => {
  await scenario(page, "step-passed-open");
  await expect(page.locator(".wb-detail-meta .tag").first()).not.toHaveText("已完成");
  await page.getByRole("button", { name: /^验收与问题/ }).click();
  await page.getByRole("button", { name: "运行真实检查" }).click();
  const dialog = page.getByRole("dialog", { name: "运行检查" });
  await dialog.getByRole("button", { name: "确认运行检查" }).click();
  await expect(dialog.getByRole("button", { name: "正在提交…" })).toBeDisabled();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("status").filter({ hasText: "运行检查已记录" })).toBeVisible();
  await expect(page.locator(".wb-detail-meta .tag").first()).toHaveText(/完成|交付/);
});

test("操作失败：错误留在弹窗内，可返回", async ({ page }) => {
  await scenario(page, "action-error");
  await page.getByRole("button", { name: /^验收与问题/ }).click();
  const run = page.getByRole("button", { name: "运行真实检查" });
  await run.click();
  const dialog = page.getByRole("dialog", { name: "运行检查" });
  await dialog.getByRole("button", { name: "确认运行检查" }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  await shot(page, "action-error");
  await dialog.getByRole("button", { name: "返回" }).click();
  await expect(dialog).toHaveCount(0);
});

test("执行中：后台推进后轮询更新，手动刷新给出结果反馈", async ({ page }) => {
  await scenario(page, "running");
  await expect(page.locator(".wb-situation")).toContainText("正在推进");
  await page.locator(".mock-bar-toggle").click();
  await page.getByRole("button", { name: "执行者提交实施结果" }).click();
  await page.getByRole("button", { name: "刷新项目状态" }).click();
  await expect(page.locator(".wb-toast")).toHaveText("已重新读取任务状态");
  await expect(page.locator(".wb-situation")).not.toContainText("正在推进");
});

test("加载与失败：慢请求有加载提示，读取失败可重试，刷新失败保留旧数据", async ({ page }) => {
  await scenario(page, "slow");
  await expect(page.getByRole("status").filter({ hasText: "正在读取" })).toBeVisible();
  await expect(page.locator(".wb-detail-title")).toHaveText("修复周报金额汇总", { timeout: 10000 });
  await scenario(page, "load-error");
  await expect(page.getByRole("alert")).toContainText("重试");
  await shot(page, "load-error");
  await scenario(page, "refresh-error");
  await expect(page.locator(".wb-detail-title")).toHaveText("修复周报金额汇总");
  await page.getByRole("button", { name: "刷新项目状态" }).click();
  await expect(page.locator(".wb-toast")).toContainText("刷新失败");
  await expect(page.locator(".wb-detail-title")).toHaveText("修复周报金额汇总");
});

test("长内容与窄视口：诊断可读、操作可达、无横向溢出", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await scenario(page, "long");
  await expect(page.locator(".diag-row.open")).toContainText("验收检查");
  await page.locator(".diag-row.tone-fail").filter({ hasText: "实施工作" }).locator(".diag-row-head").click();
  const open = page.locator(".diag-row.open");
  await expect(open).toContainText("Traceback");
  await open.getByRole("button", { name: /展开全部/ }).click();
  await expect(open).toContainText("ValueError");
  await noOverflow(page);
  await shot(page, "long-narrow");
  await page.locator(".diag-row.tone-fail").filter({ hasText: "验收检查" }).locator(".diag-row-head").click();
  await page.locator(".diag-row.open").getByRole("button", { name: /查看日志/ }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "完成" })).toBeInViewport();
  await noOverflow(page);
  await shot(page, "long-log-narrow");
});

test("没有执行记录与直接打开检查详情", async ({ page }) => {
  await scenario(page, "no-calls");
  await expect(page.locator(".diag-empty")).toContainText("还没有执行记录");
  await scenario(page, "check-detail");
  const dialog = page.getByRole("dialog", { name: /report-file/ });
  await expect(dialog).toBeVisible();
  await expect(page).not.toHaveURL(/detail=/);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

test("切换场景清理上一场景的页面状态与未模拟请求", async ({ page }) => {
  await scenario(page, "delivered");
  await expect(page.locator(".wb-detail-title")).toHaveText("导出月度客户名单");
  await page.locator(".mock-bar-toggle").click();
  await page.locator(".mock-bar-body select").selectOption("queued");
  await expect(page).toHaveURL(/scenario=queued#\/tasks\/mock-queued/);
  await expect(page.locator(".wb-detail-title")).toHaveText("整理本周销售周报");
  expect(await page.evaluate(() => Object.keys(sessionStorage).filter((k) => k !== "ael-mock-scenario" && k !== "ael-navigation-positions"))).toEqual([]);
});

test("任务更多操作菜单：键盘、整行点击、禁用项与焦点返回", async ({ page }) => {
  await scenario(page, "running");
  const trigger = page.getByRole("button", { name: "更多任务操作" });
  await trigger.click();
  const menu = page.getByRole("menu", { name: "更多任务操作" });
  await expect(menu).toBeVisible();
  const items = menu.getByRole("menuitem");
  await expect(items.first()).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press("End");
  // Deleting is disabled while an execution is running, and skipped by the keyboard.
  await expect(menu.getByRole("menuitem", { name: "永久删除任务" })).toBeDisabled();
  await expect(menu.getByRole("menuitem", { name: "取消任务" })).toBeFocused();
  await shot(page, "task-menu");
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await expect(trigger).toBeFocused();
  // Outside click closes.
  await trigger.click();
  await page.locator(".wb-detail-title").click();
  await expect(menu).toHaveCount(0);
  // The whole row is the target: click near its right edge.
  await trigger.click();
  const pause = menu.getByRole("menuitem", { name: "暂缓任务" });
  const box = (await pause.boundingBox())!;
  await page.mouse.click(box.x + box.width - 6, box.y + box.height / 2);
  const dialog = page.getByRole("dialog", { name: "暂缓任务" });
  await expect(dialog).toBeVisible();
  await expect(menu).toHaveCount(0);
  await dialog.getByRole("button", { name: "返回" }).click();
  await expect(trigger).toBeFocused();
});

test("本机菜单与任务菜单行为一致：选择后关闭并归还焦点", async ({ page }) => {
  await scenario(page, "delivered");
  const trigger = page.getByRole("button", { name: "本机菜单" });
  await trigger.click();
  const menu = page.getByRole("menu", { name: "本机菜单" });
  await expect(menu.getByRole("menuitem").first()).toBeFocused();
  await menu.getByRole("menuitem", { name: /切换为/ }).click();
  await expect(menu).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "graphite");
  await shot(page, "local-graphite");
  await trigger.click();
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await trigger.click();
  await menu.getByRole("menuitem", { name: "项目设置" }).click();
  await expect(page).toHaveURL(/#\/settings/);
  await expect(menu).toHaveCount(0);
});

test("复制诊断信息在当前位置给出反馈", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await scenario(page, "mixed-calls");
  await page.getByRole("button", { name: "复制诊断摘要" }).click();
  await expect(page.locator(".wb-toast")).toHaveText("已复制：诊断摘要");
  expect(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).executions).toHaveLength(5);
});

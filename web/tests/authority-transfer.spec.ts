import { test, expect } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

const fixture = JSON.parse(fs.readFileSync(process.env.AEL_AUTHORITY_TRANSFER_FIXTURE!, "utf8"));
test("旧来源历史、真实图片、缺失状态和任务切换保持一致", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(fixture.url + "/#/tasks?project=shared");
  await page.getByLabel("用户名", { exact: true }).fill("owner");
  await page.getByLabel("密码", { exact: true }).fill("fixture-owner-password");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("listitem").filter({ hasText: "Imported histories" }).getByRole("button", { name: "查看项目", exact: true }).click();
  await expect(page.getByRole("heading", { name: "工作台", exact: true })).toBeVisible();
  const history = page.getByRole("region", { name: "导入历史", exact: true });
  for (const name of ["mac-old", "cloud-old"]) {
    const id = fixture.imports[name].task;
    await page.evaluate(value => { location.hash = value; }, `/tasks/${id}?project=shared&section=execution`);
    await expect(history).toContainText(name);
    await expect(history).toContainText("日期未确认");
    await expect(history).toContainText("原件缺失或不可用");
    await history.getByText("旧执行记录", { exact: false }).click();
    await expect(history).toContainText("historical-attempt");
    await history.getByText("原检查与人工记录", { exact: true }).click();
    await expect(history).toContainText("Historical reviewer");
    await history.getByText("原规范引用与版本", { exact: true }).click();
    await expect(history).toContainText("legacy-spec");
    await expect(history).not.toContainText("fixture-old-private");
    const picture = history.getByRole("listitem").filter({ hasText: "runtime:actual.png" });
    await picture.getByRole("button", { name: "查看原件", exact: true }).click();
    await expect(page.locator(".artifact-panel img")).toBeVisible();
    await expect.poll(() => page.locator(".artifact-panel img").evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(1);
    await page.evaluate(() => scrollTo(0, 0));
    await page.screenshot({ path: path.join(fixture.out, "history-" + name + ".png"), fullPage: true });
    await page.getByRole("button", { name: "关闭证据", exact: true }).click();
  }
  const first = fixture.imports["mac-old"];
  let release: () => void = () => {};
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/imports/" + first.id + "?**", async route => {
    const response = await route.fetch();
    await held;
    await route.fulfill({ response });
  });
  const requested = page.waitForRequest(request => request.url().includes("/imports/" + first.id));
  await page.evaluate(value => { location.hash = value; }, `/tasks/${first.task}?project=shared&section=execution`);
  await requested;
  await page.evaluate(value => { location.hash = value; }, `/tasks/${fixture.imports["cloud-old"].task}?project=shared&section=execution`);
  await expect(history).toContainText("cloud-old");
  release();
  await expect(history).not.toContainText("mac-old");
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".sidebar")).toBeHidden();
  await page.evaluate(() => scrollTo(0, 0));
  await expect(history).toContainText("cloud-old");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.screenshot({ path: path.join(fixture.out, "history-mobile.png"), fullPage: true });
  expect(errors).toEqual([]);
});

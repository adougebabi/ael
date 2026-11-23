import fs from "node:fs";

const [action, input] = process.argv.slice(2);
const config = JSON.parse(fs.readFileSync(input, "utf8"));
const { chromium, expect } = await import(config.playwright);
const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({
    viewport: action === "render" ? { width: 640, height: 480 } : { width: 1440, height: 1000 },
  });
  if (action === "render") {
    await page.goto(config.input);
    await page.locator("#calculate").click();
    const total = await page.locator("#total").innerText();
    const clipped = await page.locator("#calculate").evaluate(element => element.scrollWidth > element.clientWidth);
    await page.screenshot({ path: config.image });
    console.log(JSON.stringify({ total, clipped, viewport: { width: 640, height: 480 } }));
  } else if (action === "review" || action === "text-result") {
    await page.goto(config.url + "/#/tasks?project=shared");
    await page.getByLabel("用户名", { exact: true }).fill("human-reviewer");
    await page.getByLabel("密码", { exact: true }).fill("fixture-reviewer-password");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByRole("button", { name: "切换项目", exact: true })).toContainText("Eight-module full chain");
    if (action === "text-result") {
      await page.goto(config.url + "/#/tasks/" + config.task + "?project=shared");
      await expect(page.getByRole("region", { name: "实施结果" })).toContainText("Module 4 now returns 4");
      await expect(page.getByRole("region", { name: "检查结果" })).toContainText("command read the integrated value");
      await expect(page.getByRole("region", { name: "附件", exact: true })).toHaveCount(0);
      await page.goto(config.url + "/#/tasks/" + config.legacy + "?project=shared");
      const gallery = page.getByRole("region", { name: "附件", exact: true });
      await expect(gallery).toBeVisible();
      await gallery.locator(".result-entry button").first().click();
      await expect(page.getByRole("dialog", { name: "成果预览" }).getByRole("link", { name: "下载原件" })).toBeVisible();
    } else {
      await page.goto(config.url + "/#/tasks/" + config.task + "?project=shared&section=checks&review=layout");
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toBeVisible();
      await dialog.getByText("关联附件（可选）", { exact: true }).click();
      await dialog.getByRole("checkbox", { name: config.artifact, exact: true }).check();
      await dialog.getByRole("radio", { name: config.result === "pass" ? "通过" : "不通过", exact: true }).check();
      await dialog.getByLabel("实际观察与理由", { exact: false }).fill(config.reason);
      await page.screenshot({ path: config.screen, fullPage: true });
      let dropped = false;
      if (config.result === "fail") {
        await page.route("**/api/v1/projects/shared/reviews", async route => {
          const response = await route.fetch();
          if (!response.ok()) throw new Error(await response.text());
          dropped = true;
          await route.abort();
        }, { times: 1 });
        await dialog.getByRole("button", { name: "提交验收结果", exact: true }).click();
        await expect(dialog.getByRole("button", { name: "原样重试提交", exact: true })).toBeVisible();
      }
      const response = page.waitForResponse(value => value.url().endsWith("/reviews") && value.request().method() === "POST");
      await dialog.getByRole("button", { name: dropped ? "原样重试提交" : "提交验收结果", exact: true }).click();
      const accepted = await response;
      if (!accepted.ok()) throw new Error(await accepted.text());
      console.log(JSON.stringify({ result: config.result, accepted: true, acceptedResponseDroppedAndReplayed: dropped }));
    }
  } else {
    throw new Error("unknown fixture browser action");
  }
} finally {
  await browser.close();
}

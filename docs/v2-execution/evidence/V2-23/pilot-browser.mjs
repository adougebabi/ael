import fs from 'node:fs';
const [action, path] = process.argv.slice(2);
const config = JSON.parse(fs.readFileSync(path, 'utf8'));
const { chromium, expect } = await import(config.playwright);
const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ viewport: action === 'render' ? { width: 640, height: 480 } : { width: 1440, height: 1000 } });
  if (action === 'render') {
    await page.goto(config.input);
    await page.locator('#calculate').click();
    const total = await page.locator('#total').innerText();
    const clipped = await page.locator('#calculate').evaluate(element => element.scrollWidth > element.clientWidth);
    await page.screenshot({ path: config.image });
    console.log(JSON.stringify({ total, clipped, viewport: { width: 640, height: 480 } }));
  } else {
    await page.goto(config.url + '/#/tasks?project=v223-pilot');
    await page.getByLabel('用户名', { exact: true }).fill('mac-reviewer');
    await page.getByLabel('密码', { exact: true }).fill(fs.readFileSync(config.password_file, 'utf8').trim());
    await page.getByRole('button', { name: '登录', exact: true }).click();
    await page.getByLabel('项目列表').getByRole('button', { name: '查看项目', exact: true }).click();
    await page.goto(config.url + '/#/tasks/' + config.task + '?project=v223-pilot&section=checks');
    await page.getByRole('button', { name: '记录人工验收', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('实际图片', { exact: true }).selectOption(config.artifact);
    await expect(dialog.getByRole('img', { name: '实际图片', exact: true })).toBeVisible();
    await dialog.getByRole('radio', { name: config.result === 'pass' ? '通过' : '不通过', exact: true }).check();
    await dialog.getByLabel('实际观察与理由', { exact: false }).fill(config.reason);
    if (config.result === 'fail') await dialog.getByLabel('修复方向', { exact: false }).fill('Correct the measured button clipping in the source and rerun the same candidate-bound checks.');
    await dialog.getByRole('checkbox', { name: '我确认以上结论针对所示候选和修订。' }).check();
    await page.screenshot({ path: config.screen, fullPage: true });
    const accepted = page.waitForResponse(response => response.url().endsWith('/reviews') && response.request().method() === 'POST');
    await dialog.getByRole('button', { name: '提交验收结果', exact: true }).click();
    const response = await accepted;
    if (!response.ok()) throw new Error(await response.text());
    console.log(JSON.stringify({ accepted: true, task: config.task, result: config.result }));
  }
} finally {
  await browser.close();
}

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
    await expect(page.locator(".wb-sidebar")).toBeVisible();
    return;
  }
  await page.goto(fixture.url + "/#/tasks?project=shared");
  await page.getByLabel("用户名", { exact: true }).fill(user);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.locator(".wb-sidebar")).toBeVisible();
  if (user === "owner") ownerCookies = await page.context().cookies();
}
async function open(page: Page, task: string) {
  await page.goto(fixture.url + "/#/tasks/" + task + "?project=shared&section=checks&check=visual");
  await page.getByRole("button", { name: "进行人工验收", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("正在读取当前候选和检查…", { exact: true })).toHaveCount(0);
  const newOpinion = dialog.getByRole("button", { name: "记录新的验收意见", exact: true });
  if (await newOpinion.isVisible()) await newOpinion.click();
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toBeVisible();
  return dialog;
}

test("WB05 Remote 首页验收弹窗与真实验收回执",async({page})=>{
 await page.setViewportSize({width:1440,height:900});await login(page);
 // The workbench review frame opens the same check; images are viewable context.
 await page.goto(fixture.url+'/#/tasks?project=shared&review=direct-home');
 const dialog=page.getByRole('dialog',{name:'查看并验收'});
 const review=dialog.getByRole('region',{name:'人工检查 visual'});
 await expect(review).toContainText('需要你的确认');
 await review.locator('.wb-review-image').click();
 const preview=page.getByRole('dialog',{name:'actual.png'});await expect(preview.locator('img')).toBeVisible();await page.keyboard.press('Escape');
 await expect(preview).toHaveCount(0);await expect(dialog).toBeVisible();
 await page.screenshot({path:path.join(fixture.out,'WB05-remote-home.png'),fullPage:false,animations:'disabled'});
 await page.route('**/api/v1/projects/shared/reviews',async route=>{const response=await route.fetch();expect(response.ok()).toBeTruthy();await route.abort();},{times:1});
 await review.getByRole('button',{name:'确认通过',exact:true}).click();
 await expect.poll(async()=>{const n=await(await page.request.get(fixture.url+'/api/v1/projects/shared/tasks/direct-home')).json();return (n.task.evidence || []).filter((e:any)=>e.check==='visual'&&e.source==='human:owner').length;}).toBe(1);
 await page.reload();const pending=page.locator('.wb-attention-card').filter({hasText:'验收提交结果待确认'});await expect(pending).toContainText('Review direct-home');await pending.getByRole('button',{name:'核对原请求回执',exact:true}).click();const retryDialog=page.getByRole('dialog');await retryDialog.getByRole('button',{name:'原样重试提交',exact:true}).click();await expect(retryDialog).toContainText('验收结果已记录');
 const after=await(await page.request.get(fixture.url+'/api/v1/projects/shared/tasks/direct-home')).json();expect(after.task.evidence.filter((e:any)=>e.check==='visual').length).toBe(1);
});

test("明确基准、键盘比较、丢失响应和幂等人工回执", async ({ page }) => {
  await login(page);
  const dialog = await open(page, "browser");
  const compare = dialog.getByRole("region", { name: "证据图片比较" });
  await expect(compare).toHaveCount(0);
  await expect(dialog.getByText("actual.png", { exact: true })).toBeVisible();
  await dialog.getByText("对照基准与实际截图（可选）", { exact: true }).click();
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
  expect(state.task.evidence?.filter((item: any) => item.check === "visual")).toHaveLength(1);
  expect(state.task.evidence[0].comparison.baseline.artifact_id).toBe(fixture.tasks.browser.baseline);
  await dialog.getByRole("button", { name: "查看任务结果", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Review browser", exact: true })).toBeVisible();
});

test("新候选拒绝旧表单且保留输入及图片选择", async ({ page, request }) => {
  await login(page);
  let dialog = await open(page, "stale");
  await dialog.getByRole("button", { name: "引用这条意见", exact: true }).click();
  await dialog.getByText("对照基准与实际截图（可选）", { exact: true }).click();
  await dialog.getByLabel("基准图片", { exact: true }).selectOption(fixture.tasks.stale.baseline);
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.stale.actual);
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由", { exact: false }).fill("KEEP_OLD_VERSION_INPUT");
  const changed = await request.post(fixture.control, { data: { action: "change-candidate", nonce: fixture.nonce }, timeout: 60000 });
  expect(changed.ok(), await changed.text()).toBeTruthy();
  await dialog.getByRole("button", { name: "提交验收结果", exact: true }).click();
  await expect(dialog.locator("pre")).toContainText("REVIEW_VERSION_MISMATCH");
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveValue("KEEP_OLD_VERSION_INPUT");
  await dialog.getByRole("button", { name: "重新读取版本并保留输入" }).click();
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
  await dialog.getByText("对照基准与实际截图（可选）", { exact: true }).click();
  await dialog.getByLabel("基准图片", { exact: true }).selectOption(fixture.tasks.missing.baseline);
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.missing.actual);
  await expect(dialog.getByRole("region", { name: "证据图片比较" })).toContainText("原件已清理");
  await expect(dialog.getByRole("region", { name: "证据图片比较" })).toContainText("原件缺失");
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.missing.unfinished);
  await expect(dialog.getByRole("region", { name: "证据图片比较" })).toContainText("上传未完成");
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由", { exact: false }).fill("Evidence unavailable");
  await dialog.getByRole("button", { name: "提交验收结果", exact: true }).click();
  await expect(dialog.locator("pre")).toContainText("REVIEW_EVIDENCE_CHANGED");
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveValue("Evidence unavailable");
  await page.screenshot({ path: path.join(fixture.out, "unavailable-evidence.png"), fullPage: true });
});

test("只读会话可读图片并受到人工裁定权限保护", async ({ page }) => {
  await login(page, "observer", "fixture-observer-password");
  await page.goto(fixture.url + "/#/tasks/browser?project=shared&section=checks");
  await expect(page.getByRole("button", { name: "进行人工验收", exact: true })).toHaveCount(0);
  const session = await (await page.request.get(fixture.url + "/api/v1/auth/session")).json();
  const response = await page.request.post(fixture.url + "/api/v1/projects/shared/reviews", { headers: { Origin: fixture.url, "X-AEL-CSRF": session.csrf, "X-AEL-Protocol": "1" }, data: {} });
  expect(response.status()).toBe(403);
});

test("人工 blocked 后可精确恢复且保留原阻塞证据", async ({ page }) => {
  await login(page);
  await page.goto(fixture.url + "/#/tasks/api-blocked?project=shared");
  await page.getByRole("button", { name: "打开受阻人工检查", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "恢复并继续人工验收", exact: true }).click();
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toBeVisible();
  const state = await (await page.request.get(fixture.url + "/api/v1/projects/shared/tasks/api-blocked")).json();
  expect(state.task.paused).toBeFalsy();
  expect(state.task.evidence?.filter((item: any) => item.result === "blocked")).toHaveLength(1);
  expect(state.task.status).toBe("open");
});

test("已提交回执之后版本再次变化时提示重新审阅", async ({ page, request }) => {
  await login(page);
  const dialog = await open(page, "postchange");
  await dialog.getByText("对照基准与实际截图（可选）", { exact: true }).click();
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.postchange.actual);
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByLabel("实际观察与理由", { exact: false }).fill("Exact original candidate review");
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
  await dialog.getByText("对照基准与实际截图（可选）", { exact: true }).click();
  await dialog.getByLabel("实际图片", { exact: true }).selectOption(fixture.tasks.slow.actual);
  await expect(dialog.getByRole("img", { name: "实际图片", exact: true })).toBeVisible({ timeout: 30000 });
  await page.unroute("**/dashboard/artifact?**");
});

test("读取项目身份失败时不使用空命名空间保存草稿", async ({ page }) => {
  await login(page);
  await page.goto(fixture.url + "/#/tasks/slow?project=shared&section=checks&check=visual");
  const entry = page.getByRole("button", { name: "进行人工验收", exact: true });
  await expect(entry).toBeVisible();
  await page.route("**/dashboard/info", route => route.abort(), { times: 1 });
  await entry.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("无法核对当前候选");
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toHaveCount(0);
  await dialog.getByRole("button", { name: "重新读取", exact: true }).click();
  await expect(dialog.getByLabel("实际观察与理由", { exact: false })).toBeVisible();
});

test("无候选的任务级意见不用选附件或比较图片", async ({ page }) => {
  await login(page);
  await page.goto(fixture.url + "/#/tasks/before-candidate?project=shared");
  let listedEvidence = false;
  page.on("request", request => {
    if (request.url().includes("dashboard/review-evidence")) listedEvidence = true;
  });
  await page.getByRole("button", { name: "更多任务操作" }).click();
  await page.getByRole("menuitem", { name: "记录人工意见", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("尚未生成验收候选");
  await expect(dialog.getByText("对照基准与实际截图（可选）")).toHaveCount(0);
  await expect(dialog.getByText("关联证据（可选）", { exact: true })).toBeVisible();
  await expect(dialog.getByLabel("补充已有证据引用")).toBeHidden();
  await dialog.getByRole("radio", { name: "通过", exact: true }).check();
  await dialog.getByRole("button", { name: "保存人工意见" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("请填写问题或无法验证的原因。");
  await dialog.getByLabel("实际观察与理由").fill("隔离样本的人工观察");
  await dialog.getByRole("button", { name: "保存人工意见" }).click();
  await expect(dialog).toContainText("人工验收意见已记录");
  // Task material browsing may read metadata independently. The opinion form
  // still has no required attachment or comparison step.
  await expect(dialog.getByText("对照基准与实际截图（可选）")).toHaveCount(0);
  const state = await (await page.request.get(fixture.url + "/api/v1/projects/shared/tasks/before-candidate")).json();
  expect(state.task.human_review_notes).toHaveLength(1);
  expect(state.task.evidence || []).toHaveLength(0);
});

test("存量普通 UI 检查在任务首屏一次通过，仍显示剩余门禁", async ({page}) => {
 await login(page);
 await page.goto(fixture.url+"/#/tasks/direct?project=shared");
 const review=page.getByRole("region",{name:"人工检查 visual"});
 await expect(review.getByRole("button",{name:"确认通过",exact:true})).toBeVisible();
 await expect(review.getByRole("checkbox")).toHaveCount(0);
 await page.screenshot({path:path.join(fixture.out,"direct-review-before.png"),fullPage:true});
 await review.getByRole("button",{name:"确认通过",exact:true}).click();
 await expect.poll(async()=> (await (await page.request.get(fixture.url+"/api/v1/projects/shared/tasks/direct")).json()).task.evidence?.filter((e:any)=>e.check==="visual").length || 0).toBe(1);
 const state=await (await page.request.get(fixture.url+"/api/v1/projects/shared/tasks/direct")).json();
 expect(state.task.evidence[0]).toMatchObject({source:"human:owner",evidence_mode:"direct_observation",result:"pass",candidate:fixture.tasks.direct.candidate});
 expect(state.task.evidence[0].artifacts).toHaveLength(0);expect(state.task.status).toBe("open");
 await page.screenshot({path:path.join(fixture.out,"direct-review-after.png"),fullPage:true});
});
test("直接人工「需要调整」只收集一段具体问题",async({page})=>{
 await login(page);await page.goto(fixture.url+"/#/tasks/direct-fail?project=shared");
 const review=page.getByRole("region",{name:"人工检查 visual"});
 await review.getByRole("button",{name:"需要调整",exact:true}).click();
 await review.getByLabel("具体问题").fill("实际布局有遮挡");
 await review.getByRole("button",{name:"提交反馈",exact:true}).click();
 await expect.poll(async()=> (await (await page.request.get(fixture.url+"/api/v1/projects/shared/tasks/direct-fail")).json()).task.evidence?.filter((e:any)=>e.result==="fail").length || 0).toBe(1);
});
test("暂无法验证经完整验收记录提交",async({page})=>{
 await login(page);await page.goto(fixture.url+"/#/tasks/direct-blocked?project=shared");
 await page.getByRole("region",{name:"人工检查 visual"}).getByRole("button",{name:"更多选项",exact:true}).click();
 const dialog=page.getByRole("dialog");
 const opinion=dialog.getByRole("button",{name:"记录新的验收意见",exact:true});if(await opinion.isVisible())await opinion.click();
 await dialog.getByRole("radio",{name:"暂无法验证",exact:true}).check();
 await dialog.getByLabel("实际观察与理由",{exact:false}).fill("当前环境无法访问");
 await dialog.getByRole("button",{name:"提交验收结果",exact:true}).click();
 await expect.poll(async()=> (await (await page.request.get(fixture.url+"/api/v1/projects/shared/tasks/direct-blocked")).json()).task.evidence?.filter((e:any)=>e.result==="blocked").length || 0).toBe(1);
});
test("直接通过丢失响应后用原请求重试",async({page})=>{
 await login(page);await page.goto(fixture.url+"/#/tasks/direct-retry?project=shared");
 const review=page.getByRole("region",{name:"人工检查 visual"});
 await page.route("**/api/v1/projects/shared/reviews",async route=>{const r=await route.fetch();expect(r.ok()).toBeTruthy();await route.abort();},{times:1});
 await review.getByRole("button",{name:"确认通过",exact:true}).click();
 // The committed check may disappear on polling; the original request remains
 // in the existing draft store and the task's pending-review entry can reopen it.
 await expect.poll(async()=> (await (await page.request.get(fixture.url+"/api/v1/projects/shared/tasks/direct-retry")).json()).task.evidence?.length || 0).toBe(1);
 const retry=page.getByRole("button",{name:"原样重试提交",exact:true});
 if(await retry.isVisible()) await retry.click();
 else {await page.getByRole("button",{name:"查看人工意见与记录",exact:true}).click();await page.getByRole("button",{name:"原样重试提交",exact:true}).click();}
 await expect.poll(async()=> (await (await page.request.get(fixture.url+"/api/v1/projects/shared/tasks/direct-retry")).json()).task.evidence?.length || 0).toBe(1);
});

for(const viewport of [{width:1280,height:800},{width:390,height:844}]) {
 test(`UC04 账户菜单与公共组件 ${viewport.width}`,async({page})=>{
  await page.setViewportSize(viewport);await login(page);
  if(viewport.width<720) await page.getByRole('button',{name:'打开导航',exact:true}).click();
  const account=page.getByRole('button',{name:'账户菜单',exact:true});
  const before=await account.boundingBox();expect(before).not.toBeNull();
  await page.screenshot({path:path.join(fixture.out,`UC04-account-closed-${viewport.width}.png`),fullPage:false});
  await account.click();
  const options=page.getByRole('menu',{name:'账户操作'});await expect(options).toBeVisible();
  const after=await account.boundingBox();expect(after!.y).toBe(before!.y);
  const rect=await options.boundingBox();expect(rect!.x).toBeGreaterThanOrEqual(0);expect(rect!.y).toBeGreaterThanOrEqual(0);expect(rect!.x+rect!.width).toBeLessThanOrEqual(viewport.width);
  await expect(options.getByRole('menuitem',{name:'个人设置'})).toBeFocused();
  await page.screenshot({path:path.join(fixture.out,`UC04-account-open-${viewport.width}.png`),fullPage:false});
  await page.keyboard.press('Escape');await expect(options).toHaveCount(0);await expect(account).toBeFocused();
  await account.click();await page.locator('.wb-nav-title').click();await expect(options).toHaveCount(0);
  await account.focus();await page.keyboard.press('Enter');await expect(options).toBeVisible();await page.keyboard.press('Tab');await expect(options.getByRole('menuitem',{name:'复制项目连接信息'})).toBeFocused();await page.keyboard.press('Escape');await expect(account).toBeFocused();
  await page.getByRole('button',{name:'切换项目',exact:true}).click();await expect(page.getByRole('region',{name:'选择项目'})).toBeVisible();await page.keyboard.press('Escape');await expect(page.getByRole('button',{name:'切换项目',exact:true})).toBeFocused();
  if(viewport.width<720) await page.getByRole('button',{name:'关闭导航',exact:true}).click({position:{x:viewport.width-10,y:100}});
  await page.goto(fixture.url+'/#/tasks/browser?project=shared&section=checks&check=visual');
  await page.getByRole('button',{name:'查看证据 actual.png',exact:true}).click();
  await expect(page.getByRole('img',{name:'任务验收证据原件'})).toBeVisible();const zoom=page.getByRole('slider',{name:/图片缩放/});await zoom.focus();await page.keyboard.press('ArrowRight');await page.keyboard.press('ArrowRight');await expect(zoom).toHaveValue('150');
  await page.screenshot({path:path.join(fixture.out,`UC04-evidence-${viewport.width}.png`),fullPage:false});await page.getByRole('button',{name:'关闭证据',exact:true}).click();
 });
}

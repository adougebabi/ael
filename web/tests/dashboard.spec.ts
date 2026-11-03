import {test,expect} from '@playwright/test';
import fs from 'node:fs';
const root=process.env.AEL_TEST_PROJECT||'/tmp/ael-p1-workspace';
test('真实表单、取消流转与 HTML 文本安全',async({page})=>{
 await page.goto('/');await expect(page.getByRole('heading',{name:'让每一步都有依据'})).toBeVisible();await expect(page.locator('[data-board]')).toHaveCount(5);
 await page.getByRole('button',{name:'＋ 创建任务'}).click();const title='UI真实任务 '+Date.now()+' <img src=x onerror=alert(1)>';
 await page.getByLabel('标题',{exact:true}).fill(title);await page.getByLabel('验收条件（每行一条）').fill('任务及取消记录可追溯');await page.getByRole('button',{name:'创建并生成计划'}).click();
 await expect(page.locator('[data-board=pending] .task').filter({hasText:title})).toBeVisible();await expect(page.locator('.task img')).toHaveCount(0);
 await page.getByLabel('理由 / 人工验收结论').fill('本轮浏览器测试完成，取消测试工作项');await page.getByRole('button',{name:'取消任务',exact:true}).click();await expect(page.locator('[data-board=cancelled] .task').filter({hasText:title})).toBeVisible();await page.getByRole('button',{name:'关闭 ×'}).click();await page.screenshot({path:'/tmp/ael-browser-results/p1-board.png',fullPage:true});
});
test('真实检查失败、回开发修复、再验证完成，在看板观察',async({page,request})=>{
 const token=(await(await request.get('/api/session')).json()).token;const headers={Authorization:'Bearer '+token};
 const title='真实纵向闭环 '+Date.now();const created=await request.post('/api/tasks',{headers,data:{title,type:'bug',goal:'verified',acceptance:['交付物内容正确'],checks:[{id:'functional',kind:'functional',command:['sh','-c','test "$(cat deliverable.txt)" = fixed'],covers:['交付物内容正确'],required:true}]}});expect(created.ok()).toBeTruthy();const n=await created.json(),id=n.task.definition.id;
 async function step(refs:string[]=[]){const r=await(await request.post('/api/action',{headers,data:{action:'claim',id,executor:'browser-bot'}})).json();expect(r.attempt).toBeTruthy();const submitted=await request.post('/api/action',{headers,data:{action:'submit',submission:{task:id,attempt:r.attempt.id,token:r.attempt.token,key:crypto.randomUUID(),result:{status:'pass',summary:'真实执行阶段交付',artifacts:refs}}}});expect(submitted.ok()).toBeTruthy()}
 await step();fs.writeFileSync(root+'/deliverable.txt','broken');await step(['deliverable.txt']);await page.goto('/');await page.locator('.task').filter({hasText:title}).click();await page.getByRole('button',{name:'运行真实检查',exact:true}).click();await expect(page.locator('.detail .tag').filter({hasText:/^execute$/})).toBeVisible();await expect(page.locator('.detail')).toContainText('fail');
 fs.writeFileSync(root+'/deliverable.txt','fixed');await step(['deliverable.txt']);await page.getByRole('button',{name:'运行真实检查',exact:true}).click();await expect(page.locator('[data-board=completed] .task').filter({hasText:title})).toBeVisible();await page.screenshot({path:'/tmp/ael-browser-results/p1-completed-detail.png',fullPage:true});
});
test('服务断开显示 stale 错误，不填充假数据',async({page})=>{await page.goto('/');await expect(page.getByText('本机服务已连接')).toBeVisible();await page.route('**/api/tasks',r=>r.abort());await expect(page.getByRole('alert')).toBeVisible({timeout:10000});await expect(page.getByRole('alert')).toContainText('最后成功数据');await page.screenshot({path:'/tmp/ael-browser-results/disconnected.png',fullPage:true})});
test('准确修订确认、五板块同时存在、离线本地资源',async({page,request})=>{
 const token=(await(await request.get('/api/session')).json()).token,headers={Authorization:'Bearer '+token};
 async function make(title:string,more:any={}){const r=await request.post('/api/tasks',{headers,data:{title,type:'feature',goal:'verified',acceptance:['有可复核交付物'],...more}});expect(r.ok()).toBeTruthy();return (await r.json()).task.definition.id}
 async function step(id:string){const n=await(await request.post('/api/action',{headers,data:{action:'claim',id,executor:'handoff-bot'}})).json();expect(n.attempt).toBeTruthy();expect((await request.post('/api/action',{headers,data:{action:'submit',submission:{task:id,attempt:n.attempt.id,token:n.attempt.token,key:crypto.randomUUID(),result:{status:'pass',summary:'确认计划输入',artifacts:[]}}}})).ok()).toBeTruthy()}
 const title='严格修订确认 '+Date.now(),id=await make(title,{preset:'strict'});await step(id);await step(id);await page.goto('/');await page.locator('[data-board=confirmation] .task').filter({hasText:title}).click();await page.getByLabel('理由 / 人工验收结论').fill('已审阅当前定义与计划，要求补足约束');await page.getByRole('button',{name:'要求调整',exact:true}).click();await expect(page.locator('.detail')).toContainText('计划 · 修订 2');await page.getByRole('button',{name:'关闭 ×'}).click();
 const confirmId=await make('待确认的关键契约',{preset:'strict'});await step(confirmId);await step(confirmId);await make('等待下一执行者',{type:'docs'});const active=await make('真实已领取的进行任务',{type:'bug'});expect((await request.post('/api/action',{headers,data:{action:'claim',id:active,executor:'held-browser-attempt'}})).ok()).toBeTruthy();await page.waitForTimeout(3000);
 for(const board of ['active','pending','confirmation','completed','cancelled'])await expect(page.locator(`[data-board=${board}] .task`).first()).toBeVisible();
 await page.route('**/*',route=>{const u=new URL(route.request().url());if(u.hostname!=='127.0.0.1')return route.abort();return route.continue()});await page.reload();await expect(page.locator('[data-board]')).toHaveCount(5);await page.screenshot({path:'/tmp/ael-browser-results/five-boards.png',fullPage:true});
 // Leave fixture unlocked after screenshot for repeatable CLI/UI tests.
 expect((await request.post('/api/action',{headers,data:{action:'resume',id:active,ack_stopped:true}})).ok()).toBeTruthy();
});

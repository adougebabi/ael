import {test,expect} from '@playwright/test';
import {execFileSync} from 'node:child_process';
import fs from 'node:fs';

const workflow=process.env.AEL_WORKFLOW_URL||'http://127.0.0.1:7339';
const attached=process.env.AEL_ATTACHED_URL||'http://127.0.0.1:7336';
const fixture=process.env.AEL_WORKFLOW_PROJECT||'/tmp/ael-workflow-acceptance-v1/project';
const runtime=process.env.AEL_WORKFLOW_RUNTIME||'/tmp/ael-workflow-acceptance-v1/runtime';

test('安装版本、同源规范、旧版本正文、交付反馈均可复核',async({page,request})=>{
 const token=(await(await request.get(workflow+'/api/session')).json()).token;
 const headers={Authorization:'Bearer '+token};
 const info=await(await request.get(workflow+'/api/info',{headers})).json();expect(info.service_version).toBe(process.env.AEL_EXPECT_VERSION||'0.1.5');expect(info.executable).toContain('/.ael/bin/');expect(info.data_dir).toContain('/runtime/');
 const task=await(await request.get(workflow+'/api/task?id=T02-bug',{headers})).json();
 const provided=task.task.applicable_knowledge.find((r:any)=>r.id==='arithmetic');
 const history=await(await request.get(workflow+'/api/task-knowledge?id=T02-bug',{headers})).json();
 const old=history.records.find((r:any)=>r.id==='arithmetic');expect(old.hash).toBe(provided.version);expect(old.body).toContain('a+b');expect(old.body).not.toContain('scale(');
 await page.goto(workflow);await page.locator('.task').filter({hasText:'T03-feature'}).click();
 await expect(page.locator('.detail')).toContainText('适用规范与参考依据');await expect(page.locator('.detail')).toContainText('实际工作进展');await expect(page.locator('.detail')).toContainText('验收覆盖与证据');await expect(page.locator('.detail')).toContainText('交付、知识回写与反馈');
 await page.locator('.detail summary').filter({hasText:'Addition contract · spec / current'}).click();await expect(page.locator('.detail')).toContainText('scale(value,factor)');
 await page.screenshot({path:'/tmp/ael-browser-results/workflow-spec-chain.png',fullPage:true});
 await page.getByRole('button',{name:'关闭 ×'}).click();await page.getByRole('button',{name:'02 工程知识'}).click();await expect(page.locator('article.knowledge').filter({hasText:'Addition contract'})).toContainText('T03-feature');
 await page.getByRole('button',{name:'04 初始化与迁移'}).click();await expect(page.locator('.summary')).toContainText(info.service_build);await expect(page.locator('.summary')).toContainText(info.data_dir);
});

test('真实 Attached 失败修复事实与 CLI 刷新和稳定详情',async({page})=>{
 await page.goto(attached);await page.locator('.task').filter({hasText:'真实 Attached 宿主：修复文本规范化'}).click();
 await expect(page.locator('.detail')).toContainText('codex-desktop-attached');await expect(page.locator('.detail')).toContainText('strip');await expect(page.locator('.detail')).toContainText('fail');await expect(page.locator('.detail')).toContainText('pass');await expect(page.locator('.detail')).toContainText('text-contract');
 await expect(page.locator('.detail .events')).toContainText('strip');
 await page.screenshot({path:'/tmp/ael-browser-results/attached-real-loop.png',fullPage:true});
 await page.locator('.search input').fill('no matching task');await expect(page.locator('.detail')).toContainText('真实 Attached 宿主：修复文本规范化');
 await page.goto(workflow);
 const name='external-cli-'+Date.now();const definition='/tmp/'+name+'.json';fs.writeFileSync(definition,JSON.stringify({id:name,title:name,type:'docs',goal:'analysis',scope:['calculator.py'],acceptance:['inspect existing file'],checks:[]}));
 execFileSync(fixture+'/ael',['task','create','--file',definition,'--project',fixture],{env:{...process.env,AEL_DATA_DIR:runtime}});
 await expect(page.locator('.task').filter({hasText:name})).toBeVisible({timeout:10000});
 await page.locator('.task').filter({hasText:name}).click();await page.getByRole('button',{name:'02 工程知识'}).click();await page.getByLabel('中文、英文、路径或符号').fill('never matches');await page.getByRole('button',{name:'搜索',exact:true}).click();await expect(page.locator('.large-empty')).toContainText('ready');
});

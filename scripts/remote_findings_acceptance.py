#!/usr/bin/env python3
"""External browser observations and CLI reports exercise structured findings."""
import argparse
import http.cookiejar
import itertools
import json
import os
from pathlib import Path
import platform
import select
import subprocess
import urllib.parse
import urllib.request

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle',required=True)
parser.add_argument('--out',required=True)
args=parser.parse_args()
repo=Path(__file__).resolve().parent.parent
out=Path(args.out).resolve()
if out.exists() and any(out.iterdir()):raise SystemExit('fixture directory must be empty')
out.mkdir(parents=True,mode=0o700)
os.chmod(out,0o700)
system=platform.system().lower()
arch={'aarch64':'arm64','arm64':'arm64','x86_64':'amd64','amd64':'amd64'}[platform.machine().lower()]
binary=Path(args.bundle).resolve()/f'ael-{system}-{arch}'
env={k:v for k,v in os.environ.items() if not k.startswith('GIT_')}
env.update(GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL=os.devnull,GIT_TERMINAL_PROMPT='0',AEL_DATA_DIR=str(out/'unused-runtime'))
serial=itertools.count(1)
process=None
log=(out/'server.log').open('w')

def run(argv,cwd=out,input_text=None):
    result=subprocess.run(list(map(str,argv)),cwd=cwd,env=env,input=input_text,capture_output=True,text=True,timeout=90)
    if result.returncode:raise RuntimeError(f'{argv[:3]} failed: {result.stderr}')
    return json.loads(result.stdout) if str(argv[0])==str(binary) else result.stdout

def private(name,value):
    path=out/(name+'.json');path.write_text(json.dumps(value));path.chmod(0o600);return path

def start(address):
    global process
    process=subprocess.Popen([str(binary),'server','start','--data-dir',str(out/'server-data'),'--addr',address,'--json'],cwd=out,env=env,stdout=subprocess.PIPE,stderr=log,text=True)
    if not select.select([process.stdout],[],[],30)[0]:raise RuntimeError('Server startup timeout')
    return json.loads(process.stdout.readline())['url']

try:
    run([binary,'server','bootstrap','--data-dir',out/'server-data','--username','owner','--password-stdin','--json'],input_text='fixture-admin-password\n')
    base=start('127.0.0.1:0')
    opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    csrf=''
    def api(method,path,value=None):
        data=json.dumps(value).encode() if value is not None else None
        headers={'Origin':base,'X-AEL-Protocol':'1','X-AEL-CSRF':csrf}
        if data is not None:headers['Content-Type']='application/json'
        with opener.open(urllib.request.Request(base+path,data=data,method=method,headers=headers),timeout=20) as response:return json.load(response)
    csrf=api('POST','/api/v1/auth/login',{'username':'owner','password':'fixture-admin-password'})['csrf']
    for name in ('shared','other'):api('POST','/api/v1/projects',{'id':name,'name':'Findings '+name})
    projects={}
    configs={}
    for name in ('a','b'):
        path=out/('checkout-'+name);path.mkdir();projects[name]=path;configs[name]=out/('config-'+name)
        for command in (['init','-b','main'],['config','user.name','Fixture'],['config','user.email','fixture@example.invalid']):run(['git',*command],path)
        (path/'spec').mkdir();(path/'spec/rules.md').write_text('# Shared rules\nREMOTE_RULES_SHARED\nUse the fixture input.\n')
        (path/'app.txt').write_text('original\n');(path/'screen.html').write_text('<!doctype html><html><body><button id=save style="width:24px;overflow:hidden;white-space:nowrap">Save a long label</button><button id=cancel style="width:24px;overflow:hidden;white-space:nowrap">Cancel a long label</button><p id=status>Ready</p></body></html>');(path/'.gitignore').write_text('.ael/\n')
        run(['git','add','.'],path);run(['git','commit','-m','fixture'],path)
        # Stable candidate commits on both clients derive from the same origin.
        if name=='b':
            run(['git','fetch',str(projects['a']),'main'],path)
            run(['git','reset','--soft','FETCH_HEAD'],path)
        api('POST','/api/v1/users',{'username':'client-'+name,'password':'fixture-client-'+name+'-password'})
        api('POST','/api/v1/projects/shared/members',{'username':'client-'+name,'role':'executor'})
        token=api('POST','/api/v1/tokens',{'username':'client-'+name,'project_id':'shared','label':'fixture'})['token']
        run([binary,'server','profile','add','--name','central','--url',base,'--token-stdin','--project',path,'--config-dir',configs[name],'--json'],input_text=token+'\n')
        run([binary,'project','connect','--profile','central','--remote-project','shared','--project',path,'--config-dir',configs[name],'--json'])
    api('POST','/api/v1/users',{'username':'observer','password':'fixture-observer-password'})
    api('POST','/api/v1/projects/shared/members',{'username':'observer','role':'reader'})
    def cli(name,*argv):return run([binary,*argv,'--project',projects[name],'--config-dir',configs[name],'--json'],projects[name])
    def operation(name,action,body,**flags):
        key='op-'+str(next(serial));file=private(key,dict(body,operation_id=key,idempotency_key=key))
        argv=['runner',action,'--runner','dashboard-'+name,'--file',file]
        for flag,value in flags.items():argv.extend(['--'+flag,str(value)])
        return cli(name,*argv)
    cli('a','repository','register','--repository','fixture-repo','--operation-id','repo','--idempotency-key','repo')
    for name in ('a','b'):
        cli(name,'knowledge','index')
        file=private('runner-'+name,{'id':'dashboard-'+name,'workspace_id':'workspace-'+name,'checkout_id':'checkout-'+name,'repository':'fixture-repo','version':'fixture','alias':'External client '+name,'machine':'fixture-host-'+name,'capabilities':['development'],'expected_revision':'absent'})
        cli(name,'runner','register','--file',file,'--operation-id','runner-'+name,'--idempotency-key','runner-'+name)
    definition=private('task',{'id':'scan','title':'External findings scan','type':'qa','intent':'inspect','goal':'report','scope':['screen.html'],'acceptance':['visual result','interaction result'],'knowledge':['source:spec/rules.md'],'checks':[{'id':'visual','kind':'ui','method':'model','covers':['visual result'],'required':True},{'id':'flow','kind':'ux','method':'scenario','covers':['interaction result'],'required':True}]})
    created=cli('a','task','create','--file',definition,'--operation-id','create-scan','--idempotency-key','create-scan')
    revision=created['revision']
    observed=cli('a','candidate','capture','--task','scan','--repository','fixture-repo','--checkout','checkout-a','--workspace','workspace-a','--runner','dashboard-a','--expected-revision',revision,'--operation-id','observe','--idempotency-key','observe')
    context=cli('a','context','publish','--task','scan','--observation',observed['id'],'--expected-revision',revision,'--operation-id','context','--idempotency-key','context')
    candidate=observed['candidate_id']
    # The external test host renders the candidate input and observes clipping.
    script="""import {chromium} from MODULE;
import fs from 'node:fs';
const browser=await chromium.launch({headless:true});
try {const page=await browser.newPage({viewport:{width:640,height:480}});await page.setContent(fs.readFileSync(INPUT,'utf8'));
const clipped=await page.locator('#save').evaluate(el=>el.scrollWidth>el.clientWidth);
const before=await page.locator('#status').innerText();await page.locator('#save').click();const after=await page.locator('#status').innerText();
await page.screenshot({path:IMAGE});const trace={steps:[{action:'click #save',before,after}],clipped,viewport:{width:640,height:480}};fs.writeFileSync(TRACE,JSON.stringify(trace));console.log(JSON.stringify(trace));
} finally {await browser.close();}
"""
    for key,value in {'MODULE':(repo/'web/node_modules/playwright/index.mjs').as_uri(),'INPUT':str(projects['a']/'screen.html'),'IMAGE':str(out/'actual.png'),'TRACE':str(out/'trace.json')}.items():script=script.replace(key,json.dumps(value))
    observation=json.loads(run(['node','--input-type=module','-e',script]))
    assert observation['clipped'] is True
    raw_report=out/'report.json';raw_report.write_text(json.dumps({'fixture':'deterministic source classification, not a real model run','observations':observation}))
    def upload(file,source):
        key='upload-'+str(next(serial))
        return cli('a','artifact','upload','--task','scan','--candidate',candidate,'--context',context['id'],'--file',file,'--source',source,'--expected-revision',revision,'--operation-id',key,'--idempotency-key',key)['id']
    image=upload(out/'actual.png','external-fixture-screenshot')
    trace=upload(out/'trace.json','external-fixture-interaction')
    visual_report=upload(raw_report,'context-check:'+context['id']+':visual')
    flow_report=upload(raw_report,'context-check:'+context['id']+':flow')
    visual={'rule':'ui.clipped-label','title':'Clipped action label','kind':'ui','severity':'high','route':'/fixture','location':{'path':'screen.html','selector':'#save'},'expected':'The whole action label is visible','actual':'The measured label exceeds the button client width','reproduction':['Open the fixture at 640x480','Observe the Save action'],'impact':'Action text cannot be read','viewport':{'width':640,'height':480},'environment':'external Chromium','artifacts':[]}
    def report(finding,check='visual',raw=visual_report,method='model',ok=True):
        key='finding-'+str(next(serial))
        body={'context_id':context['id'],'task_id':'scan','candidate_id':candidate,'check_id':check,'expected_revision':revision,'artifact_id':raw,'source':{'method':method,'tool':'deterministic-external-fixture','run_id':key},'findings':[finding],'operation_id':key,'idempotency_key':key}
        file=private(key,body)
        if ok:return cli('a','finding','report','--file',file),file
        result=subprocess.run([str(binary),'finding','report','--file',str(file),'--project',str(projects['a']),'--config-dir',str(configs['a']),'--json'],cwd=projects['a'],env=env,capture_output=True,text=True)
        assert result.returncode!=0,result.stdout
        return result.stderr,file
    before=cli('a','task','show','--task','scan')
    first,_=report(visual)
    assert first['findings'][0]['completeness']=='incomplete' and 'screenshot' in first['findings'][0]['missing_evidence']
    visual['artifacts']=[image]
    second,file=report(visual)
    finding_id=first['findings'][0]['id']
    assert second['findings'][0]['id']==finding_id and second['findings'][0]['occurrence_count']==2
    assert second['findings'][0]['completeness']=='complete'
    replay=cli('a','finding','report','--file',file)
    assert replay['findings'][0]['occurrence_id']==second['findings'][0]['occurrence_id']
    other,_=report(dict(visual,location={'path':'screen.html','selector':'#cancel'}))
    assert other['findings'][0]['id']!=finding_id
    ux={'rule':'ux.missing-feedback','title':'No action feedback','kind':'ux','severity':'medium','route':'/fixture','location':{'selector':'#save'},'expected':'Clicking Save reports a result','actual':'The visible status is unchanged after clicking','reproduction':['Open the fixture','Click Save','Read the status'],'impact':'User cannot determine the action outcome','artifacts':[image],'trace_artifacts':[]}
    flow,_=report(ux,check='flow',raw=flow_report,method='scenario')
    assert 'interaction_trace' in flow['findings'][0]['missing_evidence']
    ux['trace_artifacts']=[trace]
    fixed_flow,_=report(ux,check='flow',raw=flow_report,method='scenario')
    assert fixed_flow['findings'][0]['id']==flow['findings'][0]['id'] and fixed_flow['findings'][0]['completeness']=='complete'
    missing,_=report(dict(visual,rule='ui.missing-report'),raw='')
    assert 'report' in missing['findings'][0]['missing_evidence']
    denied,_=report(visual,method='human',ok=False)
    assert 'FINDING_SOURCE_MISMATCH' in denied
    state=cli('a','task','show','--task','scan')
    assert state['revision']==before['revision'] and state['task']['status']==before['task']['status'] and state['task']['evidence']==before['task']['evidence']
    detail=cli('b','finding','show','--finding',finding_id)
    assert detail['occurrence_count']==2 and detail['occurrences'][0]['source']['actor']=='client-a' and detail['gate_effect']=='none'
    assert cli('a','finding','list','--task','scan')['total']==4
    assert cli('a','finding','list','--task','scan','--completeness','incomplete')['total']==1
    # A reader has the same project visibility, but cannot submit a finding.
    browser_manifest=private('manifest',{'url':base,'out':str(out),'finding':finding_id,'candidate':candidate,'task':'scan'})
    result=subprocess.run([str(repo/'web/node_modules/.bin/playwright'),'test','--config=playwright.findings.config.ts'],cwd=repo/'web',env=dict(env,AEL_FINDINGS_FIXTURE=str(browser_manifest),AEL_UI_OUT=str(out/'browser')),timeout=180)
    if result.returncode:raise RuntimeError('packaged findings browser acceptance failed')
    # A later portable observation changes finding validity without changing
    # the task's submitted candidate or acceptance state.
    (projects['a']/'app.txt').write_text('candidate-two\n')
    observed2=cli('a','candidate','capture','--task','scan','--repository','fixture-repo','--checkout','checkout-a','--workspace','workspace-a','--runner','dashboard-a','--expected-revision',revision,'--operation-id','observe-two','--idempotency-key','observe-two')
    candidate=observed2['candidate_id']
    context=cli('a','context','publish','--task','scan','--observation',observed2['id'],'--expected-revision',revision,'--operation-id','context-two','--idempotency-key','context-two')
    assert cli('a','finding','show','--finding',finding_id)['validity']=='historical'
    observation2=json.loads(run(['node','--input-type=module','-e',script]))
    raw_report.write_text(json.dumps({'fixture':'second candidate external observation','observations':observation2}))
    image2=upload(out/'actual.png','external-fixture-screenshot-two')
    report2=upload(raw_report,'context-check:'+context['id']+':visual')
    bad,_=report(visual,raw=report2,ok=False)
    assert 'FINDING_EVIDENCE_INVALID' in bad
    visual['artifacts']=[image2]
    new_result,new_file=report(visual,raw=report2)
    new_id=new_result['findings'][0]['id']
    assert new_id!=finding_id and cli('a','finding','show','--finding',new_id)['validity']=='current'
    duplicate=json.loads(new_file.read_text());duplicate['operation_id']='duplicate-batch';duplicate['idempotency_key']='duplicate-batch';duplicate['findings']*=2
    duplicate_file=private('duplicate-batch',duplicate)
    failed=subprocess.run([str(binary),'finding','report','--file',str(duplicate_file),'--project',str(projects['a']),'--config-dir',str(configs['a']),'--json'],cwd=projects['a'],env=env,capture_output=True,text=True)
    assert failed.returncode!=0 and 'FINDING_DUPLICATE' in failed.stderr
    assert cli('a','finding','show','--finding',new_id)['occurrence_count']==1
    human=dict(visual,rule='human.observation',title='Human advice fixture')
    response=api('POST','/api/v1/projects/shared/findings',{'context_id':context['id'],'task_id':'scan','candidate_id':candidate,'check_id':'visual','expected_revision':revision,'source':{'method':'human','tool':'authenticated-session-fixture'},'findings':[human],'operation_id':'human-advice','idempotency_key':'human-advice'})
    assert response['source']['actor']=='owner' and response['source']['method']=='human' and response['gate_effect']=='none'
    final=cli('a','task','show','--task','scan')
    assert final['revision']==before['revision'] and final['task']['status']==before['task']['status'] and final['task']['evidence']==before['task']['evidence']
    final_total=cli('a','finding','list','--task','scan')['total']
    assert final_total==6
    (out/'summary.json').write_text(json.dumps({'result':'PASS','mode':'external deterministic protocol fixture with actual Chromium observations','task_unchanged':True,'visual_finding':finding_id,'occurrences':2,'total_findings':final_total},indent=2))
    print(json.dumps({'result':'PASS','out':str(out),'finding':finding_id}))
finally:
    if process is not None:
        process.terminate()
        try:process.wait(timeout=10)
        except subprocess.TimeoutExpired:process.kill();process.wait()
    log.close()

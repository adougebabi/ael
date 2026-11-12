#!/usr/bin/env python3
"""External CLI clients and browser exercise the packaged central Dashboard."""
import argparse
import hashlib
import datetime
import sqlite3
import http.cookiejar
import http.server
import itertools
import json
import os
from pathlib import Path
import platform
import select
import subprocess
import threading
import urllib.error
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
control=None
log=(out/'server.log').open('w')
reports=[]

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
    for name in ('shared','other'):api('POST','/api/v1/projects',{'id':name,'name':'Dashboard '+name})
    projects={}
    configs={}
    for name in ('a','b'):
        path=out/('checkout-'+name);path.mkdir();projects[name]=path;configs[name]=out/('config-'+name)
        for command in (['init','-b','main'],['config','user.name','Fixture'],['config','user.email','fixture@example.invalid']):run(['git',*command],path)
        (path/'spec').mkdir();(path/'spec/rules.md').write_text('# Shared rules\nREMOTE_RULES_SHARED\nUse the fixture input.\n')
        (path/'app.txt').write_text('original\n');(path/'.gitignore').write_text('.ael/\n')
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
    command=['python3','-c',"from pathlib import Path; assert Path('app.txt').read_text() == 'fixed\\n'; print('external fixture passed')"]
    def create(task,title,parent=''):
        definition={'id':task,'title':title,'type':'bug','goal':'verified','scope':['app.txt'],'acceptance':['fixed'],'knowledge':['source:spec/rules.md'],'checks':[{'id':'unit','kind':'functional','method':'command','command':command,'covers':['fixed'],'required':True}]}
        if parent:definition['parent']=parent
        file=private('definition-'+task,definition)
        return cli('a','task','create','--file',file,'--operation-id','create-'+task,'--idempotency-key','create-'+task)
    parent=private('epic',{'id':'dashboard-epic','title':'Dashboard parent','type':'epic','goal':'verified','acceptance':['fixed']})
    cli('a','task','create','--file',parent,'--operation-id','epic','--idempotency-key','epic')
    create('dashboard-child','Dashboard child','dashboard-epic');create('dashboard-peer','Dashboard peer')
    for index in range(18):create('dashboard-filler-'+str(index),'Dashboard filler '+str(index))
    api('POST','/api/v1/projects/other/tasks',{'definition':{'id':'dashboard-child','title':'OTHER SECRET','type':'bug','goal':'verified','scope':['app.txt'],'acceptance':['fixed']},'expected_revision':'absent','operation_id':'other-create','idempotency_key':'other-create'})
    def claim(name,task,parent=''):
        state=cli(name,'task','show','--task',task);revision=state['revision'];key='context-'+str(next(serial))
        observed=cli(name,'candidate','capture','--task',task,'--repository','fixture-repo','--checkout','checkout-'+name,'--workspace','workspace-'+name,'--runner','dashboard-'+name,'--expected-revision',revision,'--operation-id',key,'--idempotency-key',key)
        context=cli(name,'context','publish','--task',task,'--observation',observed['id'],'--expected-revision',revision,'--operation-id',key+'-pub','--idempotency-key',key+'-pub')
        file=out/('lease-'+str(next(serial))+'.json')
        body={'task_id':task,'context_id':context['id'],'expected_revision':revision}
        if parent:body['parent_id']=parent
        operation(name,'claim',body,out=file)
        attempt=json.loads(file.read_text())['attempt']
        return {'name':name,'task':task,'revision':revision,'state':state,'attempt':attempt,'lease':{'task_id':task,'attempt_id':attempt['id'],'token':attempt['token'],'fence':attempt['fence']}}
    child=claim('a','dashboard-child','dashboard-epic');peer=claim('b','dashboard-peer')
    def submit(work,refs=None):
        observed=operation(work['name'],'observe',dict(work['lease'],expected_revision=work['revision']),repository='fixture-repo')
        candidate=observed['candidate_id'];bindings=[]
        for file,ref in refs or []:
            key='upload-'+str(next(serial))
            lease=private(key+'-lease',dict(work['lease'],runner_id='dashboard-'+work['name'],expected_revision=work['revision'],lease_until=work['attempt']['lease_until']))
            artifact=cli(work['name'],'artifact','upload','--task',work['task'],'--attempt',work['attempt']['id'],'--candidate',candidate,'--file',file,'--lease-file',lease,'--expected-revision',work['revision'],'--operation-id',key,'--idempotency-key',key)
            bindings.append({'path':ref,'artifact_id':artifact['id']})
        result={'status':'pass','summary':'external fixture completed responsibility','artifacts':[b['artifact_id'] for b in bindings]}
        extra={}
        if work['state']['current_step']=='execute':result['knowledge']={'status':'unchanged','reason':'rules unchanged'}
        if work['state']['current_step']=='verify':extra={'method':'command','exit_code':0,'inputs':['app.txt']}
        return operation(work['name'],'submit',dict(work['lease'],expected_revision=work['revision'],candidate_id=candidate,result=result,bindings=bindings,**extra))
    def progress():
        for work,label in ((child,'A14_CLIENT_A_REPORTED'),(peer,'A14_CLIENT_B_REPORTED')):operation(work['name'],'progress',dict(work['lease'],summary='external fixture observation',next='continue responsibility',stage=label))
        reports.append('two-client-progress')
    def finish_child():
        submit(child)
        work=claim('a','dashboard-child','dashboard-epic');(projects['a']/'app.txt').write_text('fixed\n');submit(work,[(projects['a']/'app.txt','app.txt')])
        work=claim('a','dashboard-child','dashboard-epic')
        run(command,projects['a'])
        observed=operation('a','observe',dict(work['lease'],expected_revision=work['revision']),repository='fixture-repo')
        report=private('report',{'candidate':observed['candidate_id'],'check':'unit','definition_revision':work['state']['definition_revision'],'plan_revision':work['state']['plan_revision'],'method':'command','source':'external-command:dashboard-a','covers':['fixed'],'observations':'External host asserted fixed file and command passed'})
        result=submit(work,[(report,'runtime:report.json')]);assert result['next']['next_action']=='completed';reports.append('child-completed')
    def expire_fixture_responsibility():
        create('dashboard-lost','Dashboard lost')
        work=claim('a','dashboard-lost')
        operation('a','progress',dict(work['lease'],summary='last external report',next='await transport',stage='LAST_REPORTED_BEFORE_LOSS'))
        # Fault injection changes only this test Server's persisted deadline.
        # Both product clients continue to use the normal HTTP protocol.
        database=out/'server-data/projects'/hashlib.sha256(b'shared').hexdigest()[:32]/'runtime/runtime.db'
        with sqlite3.connect(database) as connection:
            task=json.loads(connection.execute("SELECT body FROM tasks WHERE id=?",('dashboard-lost',)).fetchone()[0])
            task['attempts'][-1]['lease_until']=(datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(seconds=1)).isoformat().replace('+00:00','Z')
            connection.execute("UPDATE tasks SET body=? WHERE id=?",(json.dumps(task),'dashboard-lost'))
        reports.append('server-fixture-lease-expired')
    nonce=os.urandom(20).hex()
    class Control(http.server.BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def do_POST(self):
            try:
                value=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))))
                if value.get('nonce')!=nonce:self.send_error(403);return
                if value['action']=='progress':progress()
                elif value['action']=='expire':expire_fixture_responsibility()
                elif value['action']=='finish':finish_child()
                elif value['action']=='restart':
                    process.terminate();process.wait(timeout=10);assert start(urllib.parse.urlparse(base).netloc)==base;reports.append('server-restarted')
                else:self.send_error(400);return
                self.send_response(200);self.end_headers();self.wfile.write(b'{"ok":true}')
            except Exception as error:
                self.send_response(500);self.end_headers();self.wfile.write(str(error).encode())
    control=http.server.ThreadingHTTPServer(('127.0.0.1',0),Control)
    threading.Thread(target=control.serve_forever,daemon=True).start()
    manifest=private('manifest',{'url':base,'control':'http://127.0.0.1:'+str(control.server_address[1]),'nonce':nonce,'out':str(out)})
    browser_env=dict(env,AEL_REMOTE_DASHBOARD_FIXTURE=str(manifest),AEL_UI_OUT=str(out/'browser'))
    result=subprocess.run([str(repo/'web/node_modules/.bin/playwright'),'test','--config=playwright.remote-dashboard.config.ts'],cwd=repo/'web',env=browser_env,timeout=240)
    if result.returncode:raise RuntimeError('packaged Dashboard browser acceptance failed')
    (out/'summary.json').write_text(json.dumps({'result':'PASS','mode':'packaged Server + external CLI clients + real browser','actions':reports},indent=2))
    print(json.dumps({'result':'PASS','out':str(out),'actions':reports}))
finally:
    if process is not None:
        process.terminate()
        try:process.wait(timeout=10)
        except subprocess.TimeoutExpired:process.kill();process.wait()
    if control is not None:control.shutdown();control.server_close()
    log.close()

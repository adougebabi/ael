import json,subprocess,uuid
from pathlib import Path
root=Path('/private/tmp/ael-v223')
project=root/'agent-pilot/project'
private=root/'review-private'
private.mkdir(mode=0o700,exist_ok=True)
binary='/Users/vinson/.local/share/ael-oracle-v2/bin/ael'
config='/private/tmp/ael-v223/pilot-config'
def cli(*values):
 r=subprocess.run([binary,*map(str,values),'--project',str(project),'--config-dir',config,'--json'],capture_output=True,text=True,timeout=60)
 if r.returncode: raise RuntimeError(r.stderr)
 return json.loads(r.stdout)
def save(value):
 p=private/(uuid.uuid4().hex+'.json');p.write_text(json.dumps(value));p.chmod(0o600);return p
def flags():
 key=uuid.uuid4().hex;return ['--operation-id',key,'--idempotency-key',key]
choice=cli('runner','next','--runner','mac-pilot','--parent','oracle-parent')
if choice['action']!='wait_human':
 print(json.dumps({'review_needed':False,'choice':choice}));raise SystemExit(0)
task=choice['checkpoint']['task']
if not task: raise RuntimeError('missing exact human task')
state=cli('task','show','--task',task)
key=uuid.uuid4().hex
image=root/(key+'.png')
playwright=Path('/Users/vinson/Documents/project/个人/ael/web/node_modules/@playwright/test/index.mjs').as_uri()
render={'playwright':playwright,'input':(project/'screen.html').as_uri(),'image':str(image)}
r=subprocess.run(['node',str(root/'pilot-browser.mjs'),'render',str(save(render))],capture_output=True,text=True,timeout=60)
if r.returncode: raise RuntimeError(r.stderr)
observed=json.loads(r.stdout)
assert observed['total']=='8'
seen=cli('candidate','capture','--task',task,'--repository','oracle-pilot','--checkout','mac-checkout','--workspace','mac-workspace','--runner','mac-pilot','--expected-revision',state['revision'],*flags())
context=cli('context','publish','--task',task,'--observation',seen['id'],'--expected-revision',state['revision'],*flags())
assert context['candidate_id']==state['task']['candidate']
artifact=cli('artifact','upload','--task',task,'--context',context['id'],'--candidate',state['task']['candidate'],'--file',image,'--source','external-host Chromium through actual Lucky Dashboard','--expected-revision',state['revision'],*flags())
result='fail' if observed['clipped'] else 'pass'
review={'playwright':playwright,'url':'https://ael.adoumi.site','task':task,'artifact':artifact['id'],'result':result,'reason':'Actual external Chromium observation '+json.dumps(observed),'screen':str(root/(key+'-dashboard.png')),'password_file':'/Users/vinson/.config/ael-oracle-v2/mac-reviewer-password'}
r=subprocess.run(['node',str(root/'pilot-browser.mjs'),'review',str(save(review))],capture_output=True,text=True,timeout=90)
if r.returncode: raise RuntimeError(r.stderr)
entry={'task':task,'candidate':state['task']['candidate'],'artifact':artifact['id'],'result':result,'observed':observed,'image':str(image),'dashboard':review['screen'],'browser':json.loads(r.stdout),'reviewer':'authenticated mac-reviewer account driven by external browser automation'}
with (root/'human-reviews.jsonl').open('a') as log: log.write(json.dumps(entry)+'\n')
print(json.dumps(entry))

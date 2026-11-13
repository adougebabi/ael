"""External host communication helper. Source edits and check commands belong to the Agent."""
import argparse,json,os,subprocess,uuid
from pathlib import Path

ROOT=Path('/private/tmp/ael-v223/agent-pilot')
PROJECT=ROOT/'project'
PRIVATE=ROOT/'private'
BINARY='/Users/vinson/.local/share/ael-oracle-v2/bin/ael'
CONFIG='/private/tmp/ael-v223/pilot-config'
PARENT='oracle-parent'
RUNNER='mac-pilot'

def command(values,cwd=None,input=None):
 r=subprocess.run(list(map(str,values)),cwd=cwd or PROJECT,input=input,capture_output=True,text=True,timeout=90)
 if r.returncode: raise RuntimeError(r.stderr or r.stdout)
 return r.stdout

def cli(*values):
 return json.loads(command([BINARY,*values,'--project',PROJECT,'--config-dir',CONFIG,'--json']))

def private(value):
 PRIVATE.mkdir(mode=0o700,parents=True,exist_ok=True)
 path=PRIVATE/(uuid.uuid4().hex+'.json')
 path.write_text(json.dumps(value,ensure_ascii=False));path.chmod(0o600)
 return path

def flags():
 key=uuid.uuid4().hex
 return ['--operation-id',key,'--idempotency-key',key]

def mutation(action,body):
 key=uuid.uuid4().hex
 return cli('runner',action,'--runner',RUNNER,'--file',private(dict(body,operation_id=key,idempotency_key=key)))

def context(task):
 state=cli('task','show','--task',task)
 observed=cli('candidate','capture','--task',task,'--repository','oracle-pilot','--checkout','mac-checkout','--workspace','mac-workspace','--runner',RUNNER,'--expected-revision',state['revision'],*flags())
 published=cli('context','publish','--task',task,'--observation',observed['id'],'--expected-revision',state['revision'],*flags())
 return state,published

def unit_check(name,parent=False):
 check={'id':name,'kind':'functional','method':'command','command':['python3','checks.py',name],'covers':['module '+name],'required':True,'artifacts':['runtime:report.json']}
 if parent: check['affected_tasks']=['oracle-'+name]
 return check

def visual_check(parent=False):
 check={'id':'layout','kind':'ui','method':'human','covers':['layout readable'],'required':True}
 if parent: check['affected_tasks']=['oracle-two']
 return check

def prepare():
 if PROJECT.exists(): raise RuntimeError('pilot project already exists; use current task state')
 PROJECT.mkdir(parents=True)
 (PROJECT/'spec').mkdir()
 (PROJECT/'spec/rules.md').write_text('# Oracle pilot\nmodule_one.value() returns 3; module_two.value() returns 5. Build both Python modules. screen.html loads build/data.js, lists the two values, and Calculate total must show 8 without clipping. A human-role reviewer will first reject a deliberately clipped 50px baseline, then inspect the corrected candidate. Publishing is outside this fixture.\n')
 (PROJECT/'.gitignore').write_text('__pycache__/\nbuild/\n')
 (PROJECT/'checks.py').write_text("import importlib,sys\nname=sys.argv[1]\nmodule=importlib.import_module('module_'+name)\nassert module.value()=={'one':3,'two':5}[name]\nprint('checked '+name)\n")
 (PROJECT/'build.py').write_text("import importlib,json,pathlib,py_compile\nvalues=[]\nfor name in ['one','two']:\n path=pathlib.Path('module_'+name+'.py')\n if not path.exists():continue\n py_compile.compile(str(path),doraise=True)\n values.append(importlib.import_module('module_'+name).value())\npathlib.Path('build').mkdir(exist_ok=True)\npathlib.Path('build/data.js').write_text('window.fixtureValues='+json.dumps(values)+';')\nprint(json.dumps({'compiled':len(values),'sum':sum(values)}))\n")
 command(['git','init','-b','main'])
 command(['git','config','user.name','Oracle Pilot'])
 command(['git','config','user.email','oracle-pilot@example.invalid'])
 command(['git','add','.'])
 command(['git','commit','-m','isolated Oracle pilot requirements and host checks'])
 cli('project','connect','--profile','oracle-public','--remote-project','v223-pilot')
 cli('knowledge','index')
 cli('repository','register','--repository','oracle-pilot',*flags())
 registration={'id':RUNNER,'workspace_id':'mac-workspace','checkout_id':'mac-checkout','repository':'oracle-pilot','version':'external-existing-codex','capabilities':['development'],'expected_revision':'absent'}
 cli('runner','register','--file',private(registration),*flags())
 parent={'id':PARENT,'title':'Oracle real Agent pilot','type':'epic','goal':'verified','scope':['module_one.py','module_two.py','screen.html'],'acceptance':['module one','module two','layout readable'],'knowledge':['source:spec/rules.md'],'checks':[unit_check('one',True),unit_check('two',True),visual_check(True)]}
 created=cli('task','create','--file',private(parent),*flags())
 items=[{'id':name,'description':'Implement module '+name} for name in ['one','two']]
 request={'request':'Implement two modules and readable sum UI, then check final integrated candidate.','items':items}
 prepared=cli('plan','prepare','--task',PARENT,'--file',private(request),'--expected-revision',created['revision'],*flags())
 leaves=[]
 for name in ['one','two']:
  definition={'id':'oracle-'+name,'title':'Oracle module '+name,'type':'bug','goal':'verified','non_goals':['publishing','business project changes'],'scope':['module_'+name+'.py'],'acceptance':['module '+name],'knowledge':['source:spec/rules.md'],'checks':[unit_check(name)]}
  if name=='two':
   definition.update(ui=True,dependencies=['oracle-one'])
   definition['scope'].append('screen.html');definition['acceptance'].append('layout readable');definition['checks'].append(visual_check())
  leaves.append({'key':name,'source_ids':[name],'definition':definition,'deliverables':definition['scope'],'basis':['spec/rules.md'],'capabilities':['development'],'order':len(leaves)+1,'required':True})
 proposal={'version':1,'id':'oracle-plan','parent':PARENT,'parent_revision':1,'input_hash':prepared['input_hash'],'request':request['request'],'user_items':items,'leaves':leaves,'coverage':[{'acceptance':'module '+name,'leaves':[name],'integration':'Rerun exact command on final parent'} for name in ['one','two']]+[{'acceptance':'layout readable','leaves':['two'],'integration':'Review final candidate image'}]}
 path=private(proposal)
 preview=cli('plan','preview','--file',path)
 return cli('plan','apply','--file',path,'--digest',preview['digest'],'--expected-revision',created['revision'],'--operation-id','oracle-plan','--idempotency-key','oracle-plan')

def claim(task):
 state,published=context(task)
 path=PRIVATE/'active.json'
 key=uuid.uuid4().hex
 destination=PRIVATE/(key+'-claim.json')
 cli('runner','claim','--runner',RUNNER,'--file',private({'task_id':task,'parent_id':PARENT,'context_id':published['id'],'expected_revision':state['revision'],'operation_id':key,'idempotency_key':key}),'--out',destination)
 attempt=json.loads(destination.read_text())['attempt']
 active={'state':state,'attempt':attempt,'lease':{'task_id':task,'attempt_id':attempt['id'],'token':attempt['token'],'fence':attempt['fence']}}
 path.write_text(json.dumps(active));path.chmod(0o600)
 mutation('heartbeat',active['lease'])
 return {'task':task,'step':state['current_step'],'unit':attempt['unit'],'checks':state['task']['definition'].get('checks',[])}

def finish(outputs,report,exit_code):
 path=PRIVATE/'active.json'
 active=json.loads(path.read_text());state,attempt,lease=active['state'],active['attempt'],active['lease']
 task=lease['task_id']
 observed=cli('runner','observe','--runner',RUNNER,'--file',private(dict(lease,expected_revision=state['revision'],operation_id=uuid.uuid4().hex,idempotency_key=uuid.uuid4().hex)),'--repository','oracle-pilot')
 candidate=observed['candidate_id']
 files=[]
 if state['current_step']=='verify':
  if report is None or exit_code is None: raise RuntimeError('verify requires actual command report and exit code')
  check=next(check for check in state['task']['definition']['checks'] if attempt['unit']=='check:'+check['id'])
  report_data={'candidate':candidate,'check':check['id'],'definition_revision':state['definition_revision'],'plan_revision':state['plan_revision'],'method':'command','source':'external-command:'+RUNNER,'covers':check['covers'],'observations':Path(report).read_text()}
  files.append((private(report_data),'runtime:report.json'))
 else:
  for name in outputs:
   source=(PROJECT/name).resolve()
   source.relative_to(PROJECT)
   opaque=PRIVATE/(uuid.uuid4().hex+'.bin');opaque.write_bytes(source.read_bytes());opaque.chmod(0o600)
   files.append((opaque,name))
 bindings=[]
 for source,ref in files:
  uploaded=cli('artifact','upload','--task',task,'--attempt',attempt['id'],'--candidate',candidate,'--file',source,'--lease-file',private(dict(lease,runner_id=RUNNER,expected_revision=state['revision'],lease_until=attempt['lease_until'])),'--expected-revision',state['revision'],*flags(),*(['--mime','application/octet-stream'] if source.suffix=='.bin' else []))
  bindings.append({'path':ref,'artifact_id':uploaded['id']})
 result={'status':'fail' if exit_code else 'pass','summary':'Existing external Agent completed '+attempt['unit'],'artifacts':[item['artifact_id'] for item in bindings]}
 if state['current_step']=='execute': result['knowledge']={'status':'unchanged','reason':'Fixture specification read and retained'}
 body=dict(lease,expected_revision=state['revision'],candidate_id=candidate,result=result,bindings=bindings)
 if state['current_step']=='verify': body.update(method='command',inputs=['checks.py'],exit_code=exit_code)
 response=mutation('submit',body)
 path.unlink()
 return response

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('action',choices=['prepare','next','claim','finish','integrate'])
parser.add_argument('--task')
parser.add_argument('--outputs',nargs='*',default=[])
parser.add_argument('--report')
parser.add_argument('--exit-code',type=int)
a=parser.parse_args()
if a.action=='prepare': result=prepare()
elif a.action=='next': result=cli('runner','next','--runner',RUNNER,'--parent',PARENT)
elif a.action=='claim': result=claim(a.task)
elif a.action=='finish': result=finish(a.outputs,a.report,a.exit_code)
else:
 state,published=context(PARENT)
 result=cli('task','integrate','--task',PARENT,'--context',published['id'],'--expected-revision',state['revision'],*flags())
print(json.dumps(result,ensure_ascii=False))

#!/usr/bin/env python3
"""Installed-CLI contract exercises. File edits are test fixtures, never a simulated Agent.
Run actual-host exploration separately; these checks never claim that scripts are AI.
"""
import argparse, hashlib, json, os, pathlib, shutil, subprocess, tempfile, time, urllib.request
P=pathlib.Path
ap=argparse.ArgumentParser();ap.add_argument('--bundle',default='dist');ap.add_argument('--out');args=ap.parse_args()
bundle=P(args.bundle).resolve();out=P(args.out or tempfile.mkdtemp(prefix='ael-workflow-20261104-')).resolve();out.mkdir(parents=True,exist_ok=True)
root=out/'project';root.mkdir(exist_ok=True);runtime=out/'runtime';runtime.mkdir(exist_ok=True)
env=dict(os.environ,AEL_DATA_DIR=str(runtime));log=[];results=[]
def run(argv,ok=True,timeout=60,cwd=None):
 p=subprocess.run([str(x) for x in argv],env=env,cwd=cwd,text=True,capture_output=True,timeout=timeout)
 log.append({'command':[str(x) for x in argv],'code':p.returncode,'stdout':p.stdout,'stderr':p.stderr})
 if ok and p.returncode:raise AssertionError(p.stderr or p.stdout)
 try:v=json.loads(p.stdout)
 except json.JSONDecodeError:v={'stdout':p.stdout,'stderr':p.stderr,'code':p.returncode}
 return v
binary=bundle/'ael-darwin-arm64'
def cli(*argv,project=None,ok=True):return run([root/'ael',*argv,'--project',project or root,'--json'],ok)
def put(name,data):
 path=out/name;path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(data,ensure_ascii=False,indent=2));return path

def case(id,fn):
 try:
  detail=fn();results.append({'id':id,'status':'passed_installed_cli','detail':detail})
 except Exception as e:
  results.append({'id':id,'status':'failed','detail':str(e)});raise
 finally:
  (out/'results.json').write_text(json.dumps(results,ensure_ascii=False,indent=2));(out/'commands.json').write_text(json.dumps(log,ensure_ascii=False,indent=2))

def create(name,**extra):
 d={'id':name,'title':name,'type':'bug','original_request':'isolated engineering acceptance: '+name,'scope':['calculator.py','test_calculator.py'],'non_goals':['no production publish'],'goal':'verified','acceptance':['sums are correct'],'checks':[{'id':'regression','kind':'unit','method':'command','command':['python3','-B','-m','unittest','-v','test_calculator'],'covers':['sums are correct'],'required':True}],**extra}
 return cli('task','create','--file',put(name+'-definition.json',d))

def work(id,refs=(),summary='read task and provided scoped specification',knowledge=None,actor='protocol-test-driver',status='pass',blocker=None):
 n=cli('claim','--task',id,'--executor',actor);a=n['attempt'];ctx=cli('context','--task',id)
 result={'status':status,'summary':summary,'artifacts':list(refs),'knowledge':knowledge or {'status':'unchanged','reason':'restores existing addition contract; no interface or decision changes','artifacts':[]}}
 if blocker:result['blocker']=blocker
 return cli('submit','--file',put(id+'-'+a['id']+'.json',{'task':id,'attempt':a['id'],'token':a['token'],'key':a['id'],'result':result}))
def runtime_report(name,text):
 d=cli('doctor');p=P(d['data_dir'])/name;p.write_text(text);return 'runtime:'+name

def t01():
 (root/'docs/spec').mkdir(parents=True,exist_ok=True);(root/'docs/adr').mkdir(parents=True,exist_ok=True)
 (root/'AGENTS.md').write_text('# Test engineering project\nRead project rules before editing. Never touch external services.\n')
 (root/'docs/spec/arithmetic.md').write_text('---\nid: arithmetic\ntitle: Addition contract\nkind: spec\nstatus: current\npaths: [calculator.py, test_calculator.py]\n---\nThe add(a,b) function returns a+b, including zero and negative values.\n')
 (root/'docs/adr/old.md').write_text('---\nid: old-decision\nkind: decision\nstatus: rejected\n---\nRejected remote math service: local arithmetic needs no network.\n')
 (root/'calculator.py').write_text('def add(a, b):\n    return a - b\n')
 (root/'test_calculator.py').write_text('import unittest\nfrom calculator import add\nclass Addition(unittest.TestCase):\n    def test_positive(self): self.assertEqual(add(2,3),5)\n    def test_zero(self): self.assertEqual(add(2,0),2)\n    def test_negative(self): self.assertEqual(add(-2,-3),-5)\n')
 run(['git','init','-q',root]);before={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in root.rglob('*') if p.is_file() and '.git' not in p.parts}
 put('baseline-before-init.json',before)
 run([binary,'init','--project',root,'--bundle-dir',bundle,'--json']);v=cli('version');assert v['version']==run([binary,'version','--json'])['version']
 health=cli('knowledge','status');assert health['state']=='ready';assert sum(health['counts'].values())==len(health['sources'])
 k=cli('knowledge','read','--id','arithmetic');assert k['status']=='current';assert k['path']=='docs/spec/arithmetic.md';assert 'a+b' in k['body']
 assert cli('knowledge','read','--id','old-decision')['status']=='rejected'
 for rel,h in before.items():
  if rel!='AGENTS.md':assert hashlib.sha256((root/rel).read_bytes()).hexdigest()==h
 return {'version':v,'health':health,'data_dir':cli('doctor')['data_dir'],'note':'index references original sources; no legacy activation'}

def t02():
 create('T02-bug');work('T02-bug')
 n=work('T02-bug',['calculator.py','test_calculator.py'],'existing code inspected; regression exposes subtraction bug')
 n=cli('verify','--task','T02-bug','--check','regression');assert n['next_action']=='repair';assert n['task']['evidence'][0]['result']=='fail'
 assert cli('next','--task','T02-bug')['next_action']=='repair'
 # This script is a deterministic protocol driver. The separate attached-host run must make its own actual fix.
 a=cli('claim','--task','T02-bug','--executor','protocol-test-driver')['attempt'];cli('context','--task','T02-bug')
 (root/'calculator.py').write_text('def add(a, b):\n    return a + b\n')
 n=cli('submit','--file',put('T02-repair.json',{'task':'T02-bug','attempt':a['id'],'token':a['token'],'key':a['id'],'result':{'status':'pass','summary':'deterministic test driver repaired subtraction and kept zero/negative regressions','artifacts':['calculator.py','test_calculator.py'],'knowledge':{'status':'unchanged','reason':'implements existing addition rule','artifacts':[]}}}))
 n=cli('verify','--task','T02-bug','--check','regression');assert n['next_action']=='completed';assert n['task']['result']=='verified'
 assert any(e['result']=='fail' for e in n['task']['evidence_history']);return {'candidate':n['task']['candidate'],'attempts':len(n['task']['attempts']),'agent_claim':'deterministic driver, NOT a real Agent run'}

def t03():
 create('T03-feature',type='feature',scope=['calculator.py','test_calculator.py','docs/spec/arithmetic.md'])
 work('T03-feature');work('T03-feature',summary='add scale(x,factor) and cover it with a separate regression; update existing arithmetic specification')
 a=cli('claim','--task','T03-feature','--executor','protocol-test-driver')['attempt'];cli('context','--task','T03-feature')
 with (root/'calculator.py').open('a') as f:f.write('\ndef scale(value, factor):\n    return value * factor\n')
 with (root/'test_calculator.py').open('a') as f:f.write('\n    def test_scale(self):\n        from calculator import scale\n        self.assertEqual(scale(3,4),12)\n')
 with (root/'docs/spec/arithmetic.md').open('a') as f:f.write('\nscale(value,factor) returns their product.\n')
 cli('knowledge','index');cli('context','--task','T03-feature')
 n=cli('submit','--file',put('T03-work.json',{'task':'T03-feature','attempt':a['id'],'token':a['token'],'key':a['id'],'result':{'status':'pass','summary':'implemented scale and synchronized its approved local contract','artifacts':['calculator.py','test_calculator.py','docs/spec/arithmetic.md'],'knowledge':{'status':'updated','reason':'new public scale behavior requires contract addition','artifacts':['docs/spec/arithmetic.md']}}}))
 n=cli('verify','--task','T03-feature','--check','regression');assert n['next_action']=='completed';assert n['task']['knowledge_outcome']['status']=='updated';return n['task']['applicable_knowledge']

def t04():
 create('T04-inspection',type='verify',intent='inspect',goal='report',checks=[{'id':'finding','kind':'functional','method':'command','command':['python3','-B','-c','from calculator import add; assert add(1,1)==3, "requested equality is false"'],'covers':['sums are correct'],'required':True}])
 before=hashlib.sha256((root/'calculator.py').read_bytes()).hexdigest()
 work('T04-inspection');report=runtime_report('inspection.md','Actual inspection checks an intentionally false equality. Code modification is not authorized.\n')
 work('T04-inspection',[report],'executed inspection preparations; evidence report in isolated runtime')
 n=cli('verify','--task','T04-inspection','--check','finding');assert n['task']['result']=='report_delivered_with_findings';assert n['task']['evidence'][0]['result']=='fail'
 create('T04-followup',feedback_of='T04-inspection',goal='analysis',type='research')
 assert hashlib.sha256((root/'calculator.py').read_bytes()).hexdigest()==before
 return {'result':n['task']['result'],'source_hash':before,'linked_defect':'T04-followup','note':'intentionally false criterion, not a real addition defect'}

def t05():
 n=create('T05-contract',risk=['public API contract']);work('T05-contract');n=work('T05-contract',summary='proposed contract change requires exact decision; existing contract retained')
 c=n['task']['confirmation'];assert c and n['board']=='confirmation'
 bad=cli('confirm','--task','T05-contract','--kind','plan','--decision','approve','--revision','0','--plan-revision','0','--reason','stale decision','--executor','fixture-project-owner',ok=False);assert bad.get('code')==1
 n=cli('confirm','--task','T05-contract','--kind',c['kind'],'--decision','adjust','--revision',str(c['definition_revision']),'--plan-revision',str(c['plan_revision']),'--candidate',c['candidate'],'--reason','keep add behavior; reject subtracting alternative','--executor','fixture-project-owner')
 n=work('T05-contract',summary='revised plan preserves addition and only improves documentation');c=n['task']['confirmation'];assert c
 n=cli('confirm','--task','T05-contract','--kind',c['kind'],'--decision','approve','--revision',str(c['definition_revision']),'--plan-revision',str(c['plan_revision']),'--candidate',c['candidate'],'--reason','approve revised fixture plan for this candidate','--executor','fixture-project-owner')
 n=work('T05-contract',['calculator.py'],'deliver unchanged valid implementation under accepted fixture decision');n=cli('verify','--task','T05-contract','--check','regression');assert n['next_action']=='completed';assert len(n['task']['approvals'])==2
 return {'decisions':n['task']['approvals'],'authorization':'isolated fixture owner decisions, not user business approvals'}

def t06():
 create('T06-handoff');n=work('T06-handoff',actor='first-executor');assert n['next_action']=='continue';assert n['board']=='pending'
 n=work('T06-handoff',['calculator.py'],actor='second-executor');n=cli('verify','--task','T06-handoff','--check','regression');assert n['next_action']=='completed'
 create('T06-lock-a');create('T06-lock-b');cli('claim','--task','T06-lock-a','--executor','first-writer')
 cli('claim','--task','T06-lock-b','--executor','second-writer')
 for id in ['T06-lock-a','T06-lock-b']:
  cli('resume','--task',id,'--ack-stopped');cli('task','cancel','--task',id,'--reason','fixture concurrent claim exercise done')
 create('T06-parallel-a');create('T06-parallel-b')
 processes=[subprocess.Popen([str(root/'ael'),'claim','--task',id,'--workspace',str(root),'--executor',actor,'--project',str(root),'--json'],env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE) for id,actor in [('T06-parallel-a','parallel-a'),('T06-parallel-b','parallel-b')]]
 for proc,id in zip(processes,['T06-parallel-a','T06-parallel-b']):
  stdout,stderr=proc.communicate(timeout=30);assert proc.returncode==0,stderr;claim=json.loads(stdout);assert claim['task']['definition']['id']==id
  a=claim['attempt'];cli('submit','--file',put(id+'-parallel.json',{'task':id,'attempt':a['id'],'token':a['token'],'key':a['id'],'result':{'status':'pass','summary':'independent parallel task definition inspected; no shared writes','artifacts':[]}}))
  cli('task','cancel','--task',id,'--reason','isolated concurrent claim proof complete')
 return {'handoff':'first executor ended after submit; second claimed next unit','parallel':'two independent CLI processes claimed tasks in the same workspace; no task/version mixing; deterministic protocol driver, not an Agent run'}

def t07():
 create('T07-environment',checks=[{'id':'missing','kind':'unit','method':'command','command':['ael-intentionally-missing-checker'],'covers':['sums are correct'],'required':True}])
 work('T07-environment');work('T07-environment',['calculator.py']);n=cli('verify','--task','T07-environment','--check','missing');assert n['next_action']=='wait_environment';assert n['task']['evidence'][0]['result']=='blocked';assert n['task']['blocker']
 # A blocked result retains previous completed work. Resume acknowledges stopped checker; no execution repeated.
 n=cli('resume','--task','T07-environment','--ack-stopped');assert n['current_step']=='verify';assert len(n['task']['attempts'])==3
 cli('task','pause','--task','T07-environment','--reason','missing checker intentionally remains unavailable; restore checker then resume verify')
 return {'actual_environment_missing':True,'recovery_node':'verify','result':'blocked, never pass'}

def t08():
 create('T08-revision');n=cli('claim','--task','T08-revision','--executor','progress-writer');a=n['attempt'];cli('context','--task','T08-revision')
 n=cli('progress','--task','T08-revision','--attempt',a['id'],'--token',a['token'],'--file',put('progress.json',{'summary':'read existing addition rule and regression file','artifacts':['test_calculator.py'],'next':'submit defined work'}));assert n['definition_revision']==1
 n=cli('submit','--file',put('T08-define.json',{'task':'T08-revision','attempt':a['id'],'token':a['token'],'key':a['id'],'result':{'status':'pass','summary':'goal and scope confirmed from existing input','artifacts':[]}}))
 d=n['task']['definition'];d['title']='T08 presentation rename';n=cli('task','edit','--task','T08-revision','--file',put('T08-display.json',d),'--reason','display only');assert n['definition_revision']==1 and not n['task']['confirmation'];assert n['current_step']=='execute'
 work('T08-revision',['calculator.py']);n=cli('verify','--task','T08-revision','--check','regression');assert n['next_action']=='completed';return {'revision':1,'progress_and_title':'did not invalidate definition; check-specific revision reuse covered by Go regression'}

def t09():
 create('T09-scenario',checks=[{'id':'scenario','kind':'ux','method':'scenario','covers':['sums are correct'],'required':True}]);work('T09-scenario');work('T09-scenario',['calculator.py']);n=cli('next','--task','T09-scenario')
 r={'task':'T09-scenario','check':'scenario','candidate':n['task']['candidate'],'method':'scenario','executor':'protocol-test-driver','result':'blocked','reason':'No scenario browser configured for this test executor. Install/configure capability then resume verify.','inputs':[],'artifacts':[],'return_to':''}
 n=cli('assess','--file',put('scenario-blocked.json',r));assert n['next_action']=='wait_environment';return {'blocker':n['task']['blocker'],'scenario_path':'actual browser scenario is exercised separately; this missing-capability case remains blocked'}

def t10():
 n=cli('task','show','--task','T02-bug');assert 'delivery' not in n['task']['plan']['steps'];original=n['task']['completed'];create('T10-feedback',feedback_of='T02-bug');n=cli('task','show','--task','T02-bug');assert n['task']['completed']==original and 'T10-feedback' in n['task']['feedback']
 cli('task','cancel','--task','T10-feedback','--reason','feedback registered, development not automatically authorized')
 create('T10-local-release',goal='released',environment='fixture-local')
 work('T10-local-release');work('T10-local-release',['calculator.py']);n=cli('verify','--task','T10-local-release','--check','regression');c=n['task']['confirmation'];assert c['kind']=='delivery'
 cli('confirm','--task','T10-local-release','--kind','delivery','--decision','approve','--revision',str(c['definition_revision']),'--plan-revision',str(c['plan_revision']),'--candidate',c['candidate'],'--reason','fixture owner authorizes only the local isolated directory copy','--executor','fixture-project-owner')
 n=cli('claim','--task','T10-local-release','--executor','local-delivery-driver');a=n['attempt'];local=out/'local-environment';local.mkdir()
 # Actual local delivery process, no remote endpoint or production credentials.
 run(['cp',root/'calculator.py',local/'calculator.py'])
 assert hashlib.sha256((local/'calculator.py').read_bytes()).hexdigest()==hashlib.sha256((root/'calculator.py').read_bytes()).hexdigest()
 receipt={'run_id':'isolated-copy-1','idempotency_key':'local-copy-1','candidate':n['task']['candidate'],'artifact':'runtime:delivery-receipt.json','environment':'fixture-local','target':'','status':'success'}
 data=P(cli('doctor')['data_dir']);(data/'delivery-receipt.json').write_text(json.dumps(receipt))
 n=cli('submit','--file',put('T10-delivery.json',{'task':'T10-local-release','attempt':a['id'],'token':a['token'],'key':a['id'],'result':{'status':'pass','summary':'copied exact fixture file to isolated local environment and compared content hash','artifacts':['runtime:delivery-receipt.json'],'delivery':receipt}}))
 assert n['task']['result']=='released';assert n['task']['delivery']['run_id']=='isolated-copy-1'
 return {'non_publish':'verified task requires no deployment','feedback':'linked new task; original outcome retained','local_delivery_receipt':receipt,'target':str(local),'production':'never touched'}

def t11():
 empty=out/'empty';empty.mkdir(exist_ok=True);run([binary,'init','--project',empty,'--bundle-dir',bundle,'--json']);assert cli('knowledge','status',project=empty)['state']=='empty'
 idx=empty/'.ael/knowledge-sources.json';idx.unlink();assert cli('knowledge','status',project=empty)['state']=='not_indexed'
 idx.write_text('corrupt');assert cli('knowledge','status',project=empty)['state']=='index_error'
 cli('knowledge','index',project=empty);(empty/'docs/spec').mkdir(parents=True);(empty/'docs/spec/missing.md').symlink_to(out/'missing-target');cli('knowledge','index',project=empty,ok=False)
 assert cli('knowledge','status',project=empty)['state']=='read_failed'
 return {'distinguished':['empty','not_indexed','index_error','read_failed'],'no_inferred_specs':True}

def t12():
 prefix=out/'user-install';run(['sh',P(__file__).resolve().parent/'install.sh',prefix,binary,bundle]);run([prefix/'bin/ael','version','--json']);run(['sh',P(__file__).resolve().parent/'uninstall.sh',prefix]);assert not (prefix/'bin/ael').exists();assert cli('version')['version']==run([binary,'version','--json'])['version']
 skill=root/'.agents/skills/ael-work/SKILL.md';saved=skill.read_bytes();skill.write_text('user customization preserved\n')
 assert cli('update','--from',bundle,ok=False).get('code')==1;assert skill.read_text()=='user customization preserved\n';skill.write_bytes(saved)
 entry=(root/'AGENTS.md').read_text();assert '$ael-work' in entry;assert 'Read project rules before editing.' in entry
 return {'uninstall':'only isolated global executable removed; project runtime and original rules retained','fresh_host':'requires separate real-host discovery/run; not claimed by this script','entry':str(skill)}

for id,fn in [('T01',t01),('T02',t02),('T03',t03),('T04',t04),('T05',t05),('T06',t06),('T07',t07),('T08',t08),('T09',t09),('T10',t10),('T11',t11),('T12',t12)]:case(id,fn)
print(json.dumps({'out':str(out),'root':str(root),'runtime_base':str(runtime),'results':results},ensure_ascii=False,indent=2))

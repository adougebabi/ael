#!/usr/bin/env python3
"""Real installed-binary checks for local upgrade and project update. No business paths."""
import argparse, hashlib, json, os, pathlib, shutil, subprocess, tarfile, tempfile
P=pathlib.Path
ap=argparse.ArgumentParser();ap.add_argument('--bundle',default='dist');ap.add_argument('--old-archive',default='dist/ael-0.1.3-unix.tar.gz');ap.add_argument('--out');args=ap.parse_args()
out=P(args.out or tempfile.mkdtemp(prefix='ael-updating-')).resolve();out.mkdir(parents=True,exist_ok=True)
bundle=P(args.bundle).resolve();prefix=out/'installation';project=out/'project with spaces';project.mkdir(exist_ok=True);old=out/'old-release';old.mkdir(exist_ok=True)
env=dict(os.environ,AEL_DATA_DIR=str(out/'runtime'),AEL_INSTALL_PREFIX=str(prefix),AEL_BUNDLE_DIR='');commands=[]
def run(argv,cwd=None,ok=True):
 p=subprocess.run([str(x) for x in argv],cwd=cwd,env=env,text=True,capture_output=True,timeout=90)
 commands.append({'command':[str(x) for x in argv],'cwd':str(cwd) if cwd else None,'code':p.returncode,'stdout':p.stdout,'stderr':p.stderr})
 (out/'commands.json').write_text(json.dumps(commands,ensure_ascii=False,indent=2))
 if ok and p.returncode:raise AssertionError(p.stderr or p.stdout)
 try:return json.loads(p.stdout)
 except json.JSONDecodeError:return {'code':p.returncode,'stdout':p.stdout,'stderr':p.stderr}
def digest(path):return hashlib.sha256(P(path).read_bytes()).hexdigest()
def owned_snapshot(root):return {str(p.relative_to(root)):digest(p) for p in root.rglob('*') if p.is_file() and '.git' not in p.parts}
# Extract only the known legacy runtime payloads and original installer.
allowed={'ael-darwin-arm64','ael-darwin-amd64','ael-linux-amd64','ael-linux-arm64','ael-toolchain.lock','THIRD_PARTY_NOTICES.txt','scripts/install.sh'}
with tarfile.open(args.old_archive) as archive:
 for name in allowed:
  m=archive.getmember(name);assert m.isfile();dest=old/name;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(archive.extractfile(m).read());dest.chmod(m.mode)
platform='darwin-arm64' if os.uname().sysname=='Darwin' and os.uname().machine=='arm64' else ('darwin-amd64' if os.uname().sysname=='Darwin' else 'linux-'+('arm64' if os.uname().machine in ['aarch64','arm64'] else 'amd64'))
newbin=bundle/('ael-'+platform);oldbin=old/('ael-'+platform)
newversion=run([newbin,'version']);assert run([oldbin,'version'])['version']=='0.1.3'
run(['sh',old/'scripts/install.sh',prefix,oldbin,old]);assert run([prefix/'bin/ael','version'])['version']=='0.1.3'
(project/'AGENTS.md').write_text('# Existing engineering project\nKeep this user-owned rule and all business code.\n');(project/'app.py').write_text('print("business fixture unchanged")\n')
run([oldbin,'init','--project',project,'--bundle-dir',old]);original_config=json.loads((project/'.ael/project.yaml').read_text());original_id=original_config['project_id'];original_config['custom']={'preserve':True};(project/'.ael/project.yaml').write_text(json.dumps(original_config))
application=digest(project/'app.py');oldtool=digest(project/'.ael/toolchain.lock');oldlocal=digest(prefix/'bin/ael');before=owned_snapshot(project)
# Preview cannot replace either installation or project assets.
preview=run([newbin,'upgrade','--prefix',prefix,'--dry-run']);assert preview['status']=='preview';assert digest(prefix/'bin/ael')==oldlocal;assert owned_snapshot(project)==before
upgraded=run([newbin,'upgrade','--prefix',prefix]);assert upgraded['operation']=='upgrade' and upgraded['available']['version']==newversion['version'];assert run([prefix/'bin/ael','version'])==newversion;assert run([project/'ael','version'])['version']=='0.1.3';assert owned_snapshot(project)==before
assert digest(P(upgraded['backup_dir'])/'ael')==oldlocal
# A real legacy claim, with no tool process started, must block project replacement.
definition=out/'task.json';definition.write_text(json.dumps({'id':'update-busy-test','title':'isolated writer fence','type':'bug','goal':'verified','acceptance':['keep fixture'],'scope':['app.py'],'checks':[{'id':'check','kind':'functional','command':['sh','-c','test -f app.py'],'covers':['keep fixture'],'required':True}]}))
run([project/'ael','task','create','--file',definition]);run([project/'ael','claim','--task','update-busy-test','--executor','isolated-claim-no-process'])
busy=run([prefix/'bin/ael','update'],cwd=project,ok=False);assert 'PROJECT_BUSY' in busy['stderr'];assert digest(project/'.ael/toolchain.lock')==oldtool
run([project/'ael','resume','--task','update-busy-test','--ack-stopped'])
# Preserve definitions/checkpoints; update must not rewrite a historical task to get green.
records={str(p.relative_to(project)):digest(p) for pattern in ['.ael/work-items/*','.ael/records/*','.ael/outcomes/*'] for p in project.glob(pattern) if p.is_file()}
preview=run([prefix/'bin/ael','update','--dry-run'],cwd=project);assert preview['operation']=='update' and preview['status']=='preview';assert digest(project/'.ael/toolchain.lock')==oldtool
updated=run([prefix/'bin/ael','update'],cwd=project);assert updated['status']=='updated' and updated['available']['version']==newversion['version'];assert updated['target']==str(project)
assert run([project/'ael','version'])==newversion
config=json.loads((project/'.ael/project.yaml').read_text());assert config['project_id']==original_id and config['mode']==original_config['mode'] and config['custom']=={'preserve':True}
assert digest(project/'app.py')==application and 'Keep this user-owned rule' in (project/'AGENTS.md').read_text()
for rel,hash in records.items():assert digest(project/rel)==hash,rel
assert digest(P(updated['backup_dir'])/'project/.ael/toolchain.lock')==oldtool
# Project launcher detects its project; upgrade is not injected with --project.
assert run([project/'ael','update','--dry-run'])['operation']=='update'
assert run([project/'ael','--dry-run','upgrade'])['operation']=='upgrade'
empty=out/'uninitialized';empty.mkdir();err=run([prefix/'bin/ael','update'],cwd=empty,ok=False);assert 'NOT_INITIALIZED' in err['stderr'] and not (empty/'.ael').exists()
assert run([prefix/'bin/ael','update','--project',project,'--dry-run'],cwd=empty)['target']==str(project)
removed=run([prefix/'bin/ael','refresh'],cwd=empty,ok=False);assert 'UNKNOWN_COMMAND' in removed['stderr']
# Full preview must detect user edits, with no file replacement.
skill=project/'.agents/skills/ael-work/SKILL.md';saved=skill.read_bytes();skill.write_text('user-edited skill\n');before=owned_snapshot(project)
conflict=run([prefix/'bin/ael','update','--dry-run'],cwd=project,ok=False);assert 'ASSET_CONFLICT' in conflict['stderr'] and owned_snapshot(project)==before;skill.write_bytes(saved)
# Repeat normal commands; no-op local upgrade must not rewrite its registry.
registry=prefix/'share/ael/installation.json';prior=registry.read_bytes();assert run([prefix/'bin/ael','upgrade'])['status']=='up_to_date';assert registry.read_bytes()==prior
assert run([project/'ael','update'])['status']=='up_to_date'
# New installer is a wrapper over the same native upgrade implementation.
other=out/'script-install';run(['sh',P(__file__).resolve().parent/'install.sh',other,newbin,bundle]);assert run([other/'bin/ael','version'])==newversion
result={'status':'passed','version':newversion,'installation':str(prefix),'project':str(project),'project_id':original_id,'scope':'isolated real 0.1.3 to current release; no business project or global installation changed','checks':['upgrade preview no writes','local upgrade with executable backup','local upgrade leaves project pinned','busy/unknown old writer rejected','update detects current initialized directory','full preview conflicts','project source/runtime/Skill/lock update','project identity/mode/custom config/code/task history preserved','project launcher update/upgrade scopes','outside-project rejection','explicit target from elsewhere','removed refresh rejects','local/project no-op','installer wrapper']}
(out/'results.json').write_text(json.dumps(result,ensure_ascii=False,indent=2));print(json.dumps(result,ensure_ascii=False,indent=2))

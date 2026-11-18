#!/usr/bin/env python3
"""Run browser acceptance against packaged, embedded resources in disposable projects."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.request

ap=argparse.ArgumentParser()
ap.add_argument('--bundle',default='dist')
ap.add_argument('--out')
ap.add_argument('--manifest')
ap.add_argument('--binary')
ap.add_argument('--grep')
args=ap.parse_args()
repo=Path(__file__).resolve().parent.parent
if not args.manifest:
    if not args.out: ap.error('--out is required when creating new fixtures')
    subprocess.run(['python3',str(repo/'scripts/ui_usability_fixture.py'),'--bundle',args.bundle,'--out',args.out],check=True)
    manifest_path=Path(args.out)/'manifest.json'
else:
    manifest_path=Path(args.manifest).resolve()
manifest=json.loads(manifest_path.read_text())
out=Path(manifest['out'])
if not manifest.get('isolated') or not (out/'ISOLATED-UI-FIXTURE').is_file():
    raise SystemExit('Only an explicitly marked disposable fixture may be used')
processes=[]
handles=[]
try:
    for name,p in manifest['projects'].items():
        env=dict(os.environ,AEL_DATA_DIR=p['runtime'])
        binary=Path(args.binary).resolve() if args.binary else Path(p['root'])/'ael'
        log=(out/(name+'-server.log')).open('w');handles.append(log)
        proc=subprocess.Popen([str(binary),'dashboard','--project',p['root'],'--addr',f'127.0.0.1:{p["port"]}'],env=env,stdout=log,stderr=subprocess.STDOUT)
        processes.append(proc)
        for _ in range(100):
            if proc.poll() is not None: raise RuntimeError(f'{name} server exited; see {out}/{name}-server.log')
            try:
                with urllib.request.urlopen(p['url']+'/api/session',timeout=1) as r:
                    if r.status==200:break
            except OSError:time.sleep(.1)
        else:raise RuntimeError(f'{name} server did not become ready')
    env=dict(os.environ,AEL_UI_FIXTURE=str(manifest_path),AEL_UI_OUT=str(out/'browser'),AEL_UI_BINARY=str(Path(args.binary).resolve()) if args.binary else str(Path(manifest["projects"]["single"]["root"])/"ael"))
    if args.binary:
        actual=json.loads(subprocess.check_output([str(Path(args.binary).resolve()),'version','--json'],text=True))
        env.update(AEL_EXPECT_VERSION=actual['version'],AEL_EXPECT_BUILD=actual['build'])
    command=['npm','--prefix',str(repo/'web'),'run','test:usability','--']
    if args.grep:command+=['--grep',args.grep]
    p=subprocess.run(command,env=env,cwd=repo)
    raise SystemExit(p.returncode)
finally:
    for p in processes:
        if p.poll() is None:p.terminate()
    for p in processes:
        try:p.wait(timeout=5)
        except subprocess.TimeoutExpired:p.kill();p.wait()
    for log in handles:log.close()

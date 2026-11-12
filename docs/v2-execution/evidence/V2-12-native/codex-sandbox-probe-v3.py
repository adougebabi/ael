#!/usr/bin/env python3
"""Test the installed Codex command sandbox with synthetic local inputs only."""
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import threading

root=Path(tempfile.mkdtemp(prefix='ael-v212-codex-sandbox-',dir='/private/tmp'))
work, control, fakehome, codexhome=(root/name for name in ('work','control','home','codex-home'))
for path in (work,control,fakehome,codexhome): path.mkdir(mode=0o700)
(control/'lease.json').write_text('synthetic-fixture-token-only')
(work/'file.txt').write_text('fixture\n')
gitenv={'PATH':os.environ.get('PATH',''),'HOME':str(fakehome),'GIT_CONFIG_NOSYSTEM':'1','GIT_CONFIG_GLOBAL':'/dev/null','GIT_TERMINAL_PROMPT':'0'}
def git(*args):
    return subprocess.run(['git','-C',str(work),*args],env=gitenv,capture_output=True,text=True,timeout=10)
initial=[git('init','-q'),git('add','file.txt'),git('-c','user.name=Fixture','-c','user.email=fixture@example.invalid','commit','-qm','fixture baseline')]
if any(item.returncode for item in initial): raise RuntimeError('fixture git setup failed')
listener=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
listener.bind(('127.0.0.1',0))
listener.listen(1)
listener.settimeout(10)
received=[]
def serve():
    try:
        conn,_=listener.accept()
        with conn: received.append(conn.recv(128).decode('ascii','replace'))
    except socket.timeout: pass
thread=threading.Thread(target=serve,daemon=True)
thread.start()
worker=work/'probe.py'
worker.write_text(r'''import base64,json,pathlib,socket,subprocess,sys
work, control, port = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), int(sys.argv[3])
result = {}
try:
    secret = (control/'lease.json').read_bytes()
    result['control_read'] = 'allowed'
    result['can_encode_control'] = bool(base64.b64encode(secret))
except OSError as error:
    result['control_read'] = {'errno':error.errno,'error':error.strerror}
try:
    (work/'allowed.txt').write_text('fixture')
    result['workspace_write'] = 'allowed'
except OSError as error:
    result['workspace_write'] = {'errno':error.errno,'error':error.strerror}
p = subprocess.run(['git','tag','fixture-probe'],cwd=work,capture_output=True,text=True,timeout=10)
result['git_tag']={'exit':p.returncode,'stderr':p.stderr[-600:]}
try:
    with socket.create_connection(('127.0.0.1',port),timeout=3) as conn: conn.sendall(b'fixture-only')
    result['loopback_connect']='allowed'
except OSError as error:
    result['loopback_connect']={'errno':error.errno,'error':str(error)}
print(json.dumps(result),flush=True)
''')
env=dict(gitenv, CODEX_HOME=str(codexhome), TMPDIR=str(root), LANG='C')
args=['codex','sandbox','-C',str(work),'--permission-profile',':workspace','--',sys.executable,str(worker),str(work),str(control),str(listener.getsockname()[1])]
try:
    proc=subprocess.run(args,cwd=work,env=env,capture_output=True,text=True,timeout=30)
    thread.join(timeout=4)
    tag=git('tag','--list','fixture-probe')
    result={'fixture':str(root),'sandbox_exit':proc.returncode,'stderr':proc.stderr[-2500:],'stdout':proc.stdout[-2000:],'fixture_tag_created':tag.stdout.strip()=='fixture-probe','listener_received':received}
finally:
    listener.close()
(root/'result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result),flush=True)

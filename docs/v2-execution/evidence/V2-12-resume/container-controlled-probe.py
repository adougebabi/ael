import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import uuid

root = Path(tempfile.mkdtemp(prefix='ael-v212-container-controlled-'))
launch_script = '''
import os, subprocess, sys, time
folder, name, mode = sys.argv[1:]
worker = "setsid sh -c 'cat /proc/$$/stat > /work/writer.stat; printf armed > /work/armed; sleep 4; printf stale > /work/stale-write' </dev/null >/work/writer.out 2>/work/writer.err &"
waiter = "read -r ignored || exit 0" if mode == 'cooperative' else "while :; do sleep 60; done"
command = "cat /proc/self/stat > /work/init.stat; " + worker + " " + waiter
args = ['docker','run','--rm','-i','--pull=never','--name',name,'--network=none','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--pids-limit=32','--mount','type=bind,source='+folder+',target=/work','alpine:3.22','sh','-c',command]
with open(os.path.join(folder,'client.log'),'w') as log:
    proc = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=log, stderr=log)
    print(proc.pid, flush=True)
    time.sleep(60)
'''
results = []
scenarios = [('cooperative-client-kill', True)] if '--client-only' in sys.argv else [('noncooperative-control', False), ('noncooperative-crash', True), ('cooperative', True)]
for mode, kill_parent in scenarios:
    folder = root / mode
    folder.mkdir()
    name = 'ael-v212-probe-' + uuid.uuid4().hex[:12]
    launcher = None
    client_pid = None
    item = {'mode': mode, 'container': name}
    try:
        parent_mode = 'cooperative' if mode.startswith('cooperative') else 'noncooperative'
        launcher = subprocess.Popen([sys.executable, '-c', launch_script, str(folder), name, parent_mode], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        client_pid = int(launcher.stdout.readline().strip())
        deadline = time.monotonic() + 20
        while not (folder / 'armed').exists() and time.monotonic() < deadline and launcher.poll() is None:
            time.sleep(0.05)
        item['armed'] = (folder / 'armed').exists()
        if item['armed']:
            parts = (folder / 'writer.stat').read_text().split()
            item['writer_pid'], item['writer_ppid'], item['writer_pgrp'], item['writer_sid'] = map(int, (parts[0], parts[3], parts[4], parts[5]))
            item['writer_detached'] = item['writer_pid'] == item['writer_pgrp'] == item['writer_sid']
            if kill_parent:
                if mode == 'cooperative-client-kill':
                    os.kill(client_pid, signal.SIGKILL)
                else:
                    os.kill(launcher.pid, signal.SIGKILL)
                    launcher.wait(timeout=5)
            time.sleep(6)
            item['stale_write'] = (folder / 'stale-write').exists()
        else:
            item['startup_log'] = (folder / 'client.log').read_text() if (folder / 'client.log').exists() else ''
            item['writer_error'] = (folder / 'writer.err').read_text() if (folder / 'writer.err').exists() else ''
        inspect = subprocess.run(['docker','inspect','--format','{{.State.Running}}|{{.State.Pid}}',name], capture_output=True, text=True, timeout=10)
        daemon = subprocess.run(['docker','version','--format','{{.Server.Version}}'], capture_output=True, text=True, timeout=10)
        item['inspect'] = {'exit': inspect.returncode, 'stdout': inspect.stdout.strip(), 'stderr': inspect.stderr.strip()}
        item['daemon'] = {'exit': daemon.returncode, 'stdout': daemon.stdout.strip(), 'stderr': daemon.stderr.strip()}
        client = subprocess.run(['ps','-p',str(client_pid),'-o','command='], capture_output=True, text=True, timeout=5)
        item['client_present'] = name in client.stdout
    finally:
        if launcher and launcher.poll() is None:
            launcher.kill()
            launcher.wait(timeout=5)
        cleanup = subprocess.run(['docker','rm','-f',name], capture_output=True, text=True, timeout=10)
        item['cleanup'] = {'exit': cleanup.returncode, 'stdout': cleanup.stdout.strip(), 'stderr': cleanup.stderr.strip()}
        if client_pid:
            client = subprocess.run(['ps','-p',str(client_pid),'-o','command='], capture_output=True, text=True, timeout=5)
            if name in client.stdout:
                os.kill(client_pid, signal.SIGTERM)
        results.append(item)
        (root / 'result.json').write_text(json.dumps({'fixture': str(root), 'results': results}, indent=2) + '\n')
print(json.dumps({'fixture': str(root), 'results': results}))

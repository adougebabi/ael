#!/usr/bin/env python3
"""Bounded sandbox fixture; all processes, paths and credentials are synthetic."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

root = Path(tempfile.mkdtemp(prefix='ael-v212-sandbox-', dir='/private/tmp'))
work, scratch, control, original = (root / name for name in ('work', 'scratch', 'control', 'original'))
for path in (work, scratch, control, original):
    path.mkdir(mode=0o700)
(scratch / 'home').mkdir()
(scratch / 'cache').mkdir()
(scratch / 'tmp').mkdir()
secret = control / 'lease.json'
secret.write_text('fixture-synthetic-token-do-not-use')
secret.chmod(0o600)
image = root / 'fixture-image.dmg'
image.write_text('fixture disk image only')
(work / 'go.mod').write_text('module fixture.local/check\n\ngo 1.26\n')
(work / 'check_test.go').write_text('package fixture\nimport "testing"\nfunc TestFixture(t *testing.T) { if 2+2 != 4 { t.Fatal("arithmetic") } }\n')
init = subprocess.run(['git', 'init', '-q', str(work)], capture_output=True, text=True, timeout=10)
if init.returncode:
    raise SystemExit('fixture git init failed: ' + init.stderr)
q = lambda p: json.dumps(str(p))
profile = root / 'fixture.sb'
profile.write_text('(version 1)\n(allow default)\n(deny file-write*)\n' +
                   '(allow file-write* (subpath ' + q(work) + ') (subpath ' + q(scratch) + '))\n' +
                   '(deny file-write-mode (literal ' + q(work) + '))\n' +
                   '(deny file-read* (subpath ' + q(control) + ') (literal ' + q(image) + '))\n' +
                   '(deny network*)\n(deny signal (target others))\n(allow signal (target same-sandbox))\n')
monitor_code = r'''import pathlib, signal, sys, time
marker = pathlib.Path(sys.argv[1])
signal.signal(signal.SIGUSR1, lambda *_: marker.write_text('received'))
print('ready', flush=True)
while True: time.sleep(1)
'''
monitor = subprocess.Popen([sys.executable, '-c', monitor_code, str(root / 'monitor-signalled')], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
if monitor.stdout.readline().strip() != 'ready':
    raise RuntimeError('own monitor failed to start')
child_code = r'''import json, os, pathlib, signal, subprocess, sys
root, work, scratch, control, original, image, monitor, inherited = sys.argv[1:]
work, scratch, control, original, image = map(pathlib.Path, (work, scratch, control, original, image))
result = {}
def attempt(name, fn):
    try:
        fn()
        result[name] = 'allowed'
    except OSError as error:
        result[name] = {'errno': error.errno, 'error': error.strerror}
def touch(path):
    path.write_text('fixture')
attempt('work_write', lambda: touch(work / 'allowed'))
attempt('scratch_write', lambda: touch(scratch / 'allowed'))
attempt('original_write', lambda: touch(original / 'forbidden'))
attempt('control_read', lambda: (control / 'lease.json').read_text())
attempt('image_read', lambda: image.read_text())
attempt('mountpoint_chmod', lambda: os.chmod(work, 0o700))
attempt('signal_monitor', lambda: os.kill(int(monitor), signal.SIGUSR1))
if inherited != '-1':
    attempt('inherited_fd_read', lambda: os.read(int(inherited), 100))
env = {'PATH': os.environ.get('PATH', ''), 'HOME': str(scratch / 'home'), 'TMPDIR': str(scratch / 'tmp'), 'GOCACHE': str(scratch / 'cache'), 'GOPATH': str(scratch / 'gopath'), 'GOPROXY': 'off', 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_TERMINAL_PROMPT': '0'}
for label, args in [('go_test', ['go', 'test', './...']), ('git_status', ['git', 'status', '--porcelain'])]:
    try:
        proc = subprocess.run(args, cwd=work, env=env, capture_output=True, text=True, timeout=90)
        result[label] = {'exit': proc.returncode, 'stdout': proc.stdout[-1000:], 'stderr': proc.stderr[-1500:]}
    except (OSError, subprocess.TimeoutExpired) as error:
        result[label] = {'error': str(error)}
print(json.dumps(result), flush=True)
'''
env = {'PATH': os.environ.get('PATH', ''), 'HOME': str(scratch / 'home'), 'TMPDIR': str(scratch / 'tmp'), 'LANG': 'C'}
result = {'fixture': str(root), 'git_init': init.returncode}
try:
    fd = os.open(secret, os.O_RDONLY)
    try:
        proc = subprocess.run(['/usr/bin/sandbox-exec', '-f', str(profile), sys.executable, '-c', child_code, str(root), str(work), str(scratch), str(control), str(original), str(image), str(monitor.pid), str(fd)], env=env, pass_fds=(fd,), capture_output=True, text=True, timeout=120)
    finally:
        os.close(fd)
    result['sandbox_exit'] = proc.returncode
    result['sandbox_stderr'] = proc.stderr[-2000:]
    try:
        result['child'] = json.loads(proc.stdout)
    except json.JSONDecodeError:
        result['child_stdout'] = proc.stdout[-2000:]
    time.sleep(.2)
    result['monitor_received_signal'] = (root / 'monitor-signalled').exists()
finally:
    if monitor.poll() is None:
        monitor.terminate()
        monitor.wait(timeout=5)
    (root / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result), flush=True)

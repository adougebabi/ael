#!/usr/bin/env python3
"""Disposable macOS volume, watchdog and Runner-crash fixture."""
import json
import os
from pathlib import Path
import plistlib
import signal
import subprocess
import sys
import tempfile
import time
import uuid

root = Path(tempfile.mkdtemp(prefix='ael-v212-crash-', dir='/private/tmp'))
image, mount, remount, control, original = (root / name for name in ('fixture.dmg', 'mount', 'remount', 'control', 'original'))
mount.mkdir(mode=0o500)
for path in (remount, control, original):
    path.mkdir(mode=0o700)
(control / 'lease.json').write_text('fixture-only-secret')
result = {'fixture': str(root), 'status': 'incomplete'}
runner = watchdog = None
worker_pid = None
attached_target = None

def call(args, timeout=45):
    proc = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    return proc.returncode, proc.stdout, proc.stderr.decode('utf-8', 'replace')[-2000:]

def attach(path, label):
    global attached_target
    rc, out, err = call(['hdiutil', 'attach', str(image), '-mountpoint', str(path), '-nobrowse', '-noautoopen', '-plist'])
    result[label] = {'exit': rc, 'stderr': err}
    if rc:
        return False
    attached_target = path
    entries = plistlib.loads(out).get('system-entities', [])
    matches = [e for e in entries if e.get('mount-point') == str(path)]
    if len(matches) != 1 or not matches[0].get('dev-entry', '').startswith('/dev/disk'):
        raise RuntimeError('mount identity mismatch')
    result[label].update(mountpoint=str(path), device=matches[0]['dev-entry'])
    return True

watchdog_source = r'''import json, os, pathlib, subprocess, sys, time
root, mount = map(pathlib.Path, sys.argv[1:])
while os.read(0, 4096): pass
eof_ns = time.monotonic_ns()
start_ns = time.monotonic_ns()
try:
    p = subprocess.run(['hdiutil','detach',str(mount),'-force'], capture_output=True, text=True, timeout=40)
    state = {'eof_ns': eof_ns, 'start_ns': start_ns, 'end_ns': time.monotonic_ns(), 'exit': p.returncode, 'stdout': p.stdout[-1000:], 'stderr': p.stderr[-1000:]}
except subprocess.TimeoutExpired:
    state = {'eof_ns': eof_ns, 'start_ns': start_ns, 'end_ns': time.monotonic_ns(), 'timeout': True}
(root / 'watchdog.json').write_text(json.dumps(state))
'''
worker_source = r'''import json, os, pathlib, signal, sys, time
root, mount, control, original, image, watchdog = sys.argv[1:]
root, mount, control, original, image = map(pathlib.Path, (root, mount, control, original, image))
os.chdir(mount)
checks = {}
def attempt(label, fn):
    try:
        fn()
        checks[label] = 'allowed'
    except OSError as error:
        checks[label] = {'errno': error.errno, 'error': error.strerror}
attempt('control_read', lambda: (control / 'lease.json').read_text())
attempt('image_read', lambda: image.read_text())
attempt('original_write', lambda: (original / 'forbidden').write_text('bad'))
attempt('watchdog_kill', lambda: os.kill(int(watchdog), signal.SIGKILL))
fd = os.open('timeline', os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
(pathlib.Path('ready.json')).write_text(json.dumps({'pid': os.getpid(), 'pgrp': os.getpgrp(), 'sid': os.getsid(0), 'checks': checks}))
while True:
    try:
        os.write(fd, (str(time.monotonic_ns()) + '\n').encode())
        os.fsync(fd)
        time.sleep(.01)
    except OSError:
        break
'''
runner_source = r'''import os, pathlib, subprocess, sys, time
root, profile, worker, mount, control, original, image, watchdog, hold = sys.argv[1:]
cmd = ['/usr/bin/sandbox-exec','-f',profile,sys.executable,worker,root,mount,control,original,image,watchdog]
child = subprocess.Popen(cmd, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True, close_fds=True)
pathlib.Path(root,'worker-pid').write_text(str(child.pid))
while True: time.sleep(1)
'''
try:
    rc, out, err = call(['hdiutil', 'create', '-size', '64m', '-fs', 'APFS', '-volname', 'AELV212' + uuid.uuid4().hex[:6], str(image)])
    result['create'] = {'exit': rc, 'stderr': err}
    if rc or not attach(mount, 'attach'):
        result['status'] = 'setup_failed'
    else:
        q = lambda p: json.dumps(str(p))
        profile = root / 'fixture.sb'
        profile.write_text('(version 1)\n(allow default)\n(deny file-write*)\n' +
                           '(allow file-write* (subpath ' + q(mount) + ') (literal "/dev/null"))\n' +
                           '(deny file-write-mode (literal ' + q(mount) + '))\n' +
                           '(deny file-read* (subpath ' + q(control) + ') (literal ' + q(image) + '))\n' +
                           '(deny network*)\n(deny signal)\n(allow signal (target same-sandbox))\n')
        worker_file = root / 'worker.py'
        worker_file.write_text(worker_source)
        rfd, wfd = os.pipe()
        watchdog = subprocess.Popen([sys.executable, '-c', watchdog_source, str(root), str(mount)], stdin=rfd, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, close_fds=True)
        os.close(rfd)
        runner = subprocess.Popen([sys.executable, '-c', runner_source, str(root), str(profile), str(worker_file), str(mount), str(control), str(original), str(image), str(watchdog.pid), str(wfd)], pass_fds=(wfd,), stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, close_fds=True)
        os.close(wfd)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline and not (mount / 'ready.json').exists():
            if runner.poll() is not None or watchdog.poll() is not None:
                break
            time.sleep(.02)
        if not (mount / 'ready.json').exists():
            result['status'] = 'worker_not_ready'
            result['runner_exit'] = runner.poll()
            result['watchdog_exit'] = watchdog.poll()
        else:
            ready = json.loads((mount / 'ready.json').read_text())
            worker_pid = ready['pid']
            result['worker'] = ready
            time.sleep(.2)
            result['signal_before_ns'] = time.monotonic_ns()
            os.kill(runner.pid, signal.SIGKILL)
            result['signal_after_ns'] = time.monotonic_ns()
            runner.wait(timeout=5)
            try:
                _, wd_stderr = watchdog.communicate(timeout=45)
                result['watchdog_process_exit'] = watchdog.returncode
                result['watchdog_stderr'] = wd_stderr[-1000:]
            except subprocess.TimeoutExpired:
                result['watchdog_timed_out'] = True
            wd_path = root / 'watchdog.json'
            if wd_path.exists():
                result['watchdog'] = json.loads(wd_path.read_text())
            if result.get('watchdog', {}).get('exit') == 0:
                attached_target = None
                if attach(remount, 'reattach'):
                    lines = (remount / 'timeline').read_text(errors='replace').splitlines()
                    values = [int(line) for line in lines if line.isdigit()]
                    wd = result['watchdog']
                    result['timeline'] = {'samples': len(values), 'after_kill_request': sum(n > result['signal_before_ns'] for n in values), 'after_watchdog_eof': sum(n > wd['eof_ns'] for n in values), 'after_detach_return': sum(n > wd['end_ns'] for n in values)}
                    result['status'] = 'observed'
                else:
                    result['status'] = 'reattach_failed'
            else:
                result['status'] = 'watchdog_did_not_detach'
finally:
    if runner and runner.poll() is None:
        runner.kill()
        runner.wait(timeout=5)
    if watchdog and watchdog.poll() is None:
        try:
            watchdog.wait(timeout=5)
        except subprocess.TimeoutExpired:
            watchdog.kill()
            watchdog.wait(timeout=5)
    if worker_pid:
        ps = subprocess.run(['ps','-p',str(worker_pid),'-o','command='], capture_output=True, text=True, timeout=5)
        if str(root) in ps.stdout:
            os.killpg(worker_pid, signal.SIGKILL)
    if attached_target:
        rc, out, err = call(['hdiutil','detach',str(attached_target),'-force'])
        result['cleanup'] = {'target': str(attached_target), 'exit': rc, 'stderr': err}
    (root / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result), flush=True)

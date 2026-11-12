#!/usr/bin/env python3
"""Probe only disposable disk-image and writer processes created by this script."""
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

root = Path(tempfile.mkdtemp(prefix='ael-v212-volume-', dir='/private/tmp'))
image = root / 'fixture.dmg'
mount = root / 'mount'
remount = root / 'remount'
mount.mkdir(mode=0o500)
remount.mkdir(mode=0o700)
procs = []
attached = False
result = {'fixture': str(root), 'filesystem': 'APFS', 'status': 'incomplete'}

def command(args, timeout=45):
    p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    return p.returncode, p.stdout, p.stderr.decode('utf-8', 'replace')[-3000:]

def record_command(name, args):
    rc, out, err = command(args)
    result[name] = {'exit': rc, 'stderr': err, 'stdout': out.decode('utf-8', 'replace')[-3000:]}
    return rc

def attach(target, name):
    global attached
    rc, out, err = command(['hdiutil', 'attach', str(image), '-mountpoint', str(target), '-nobrowse', '-noautoopen', '-plist'])
    info = {'exit': rc, 'stderr': err}
    result[name] = info
    if rc:
        return False
    attached = True
    data = plistlib.loads(out)
    matches = [x for x in data.get('system-entities', []) if x.get('mount-point') == str(target)]
    if len(matches) != 1 or not matches[0].get('dev-entry', '').startswith('/dev/disk'):
        raise RuntimeError('fixture mount did not match the requested path')
    info['mountpoint'] = matches[0]['mount-point']
    info['device'] = matches[0]['dev-entry']
    return True

worker = r'''import json, os, pathlib, sys, time
root, mount, name = map(pathlib.Path, sys.argv[1:])
name = str(name)
os.chdir(mount)
fd = os.open(name + '-file', os.O_RDWR | os.O_CREAT, 0o600)
os.write(fd, b'before\n')
os.fsync(fd)
dfd = os.open('.', os.O_RDONLY)
(root / (name + '-ready.json')).write_text(json.dumps({'pid': os.getpid(), 'pgrp': os.getpgrp(), 'sid': os.getsid(0), 'cwd': os.getcwd()}))
deadline = time.monotonic() + 30
while not (root / 'proceed').exists() and time.monotonic() < deadline:
    time.sleep(.02)
results = {}
def attempt(label, fn):
    try:
        fn()
        results[label] = 'succeeded'
    except OSError as error:
        results[label] = {'errno': error.errno, 'error': error.strerror}
attempt('existing_fd', lambda: (os.write(fd, b'after\n'), os.fsync(fd)))
def create(path, **kw):
    f = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600, **kw)
    os.write(f, b'after\n')
    os.close(f)
attempt('relative_cwd', lambda: create(name + '-relative'))
attempt('relative_dirfd', lambda: create(name + '-dirfd', dir_fd=dfd))
attempt('absolute_mount_path', lambda: create(str(mount / (name + '-absolute'))))
(root / (name + '-result.json')).write_text(json.dumps(results))
'''
try:
    rc = record_command('create', ['hdiutil', 'create', '-size', '64m', '-fs', 'APFS', '-volname', 'AELV212' + uuid.uuid4().hex[:6], str(image)])
    if rc:
        result['status'] = 'create_failed'
    elif not attach(mount, 'attach'):
        result['status'] = 'attach_failed'
    else:
        for name in ['worker-a', 'worker-b']:
            p = subprocess.Popen([sys.executable, '-c', worker, str(root), str(mount), name], start_new_session=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            procs.append((name, p))
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline and not all((root / (name + '-ready.json')).exists() for name, _ in procs):
            if any(p.poll() is not None for _, p in procs):
                break
            time.sleep(.05)
        result['writers'] = {}
        for name, p in procs:
            path = root / (name + '-ready.json')
            result['writers'][name] = json.loads(path.read_text()) if path.exists() else {'ready': False, 'exit': p.poll()}
        if not all((root / (name + '-ready.json')).exists() for name, _ in procs):
            result['status'] = 'workers_not_ready'
        else:
            before = time.monotonic()
            rc = record_command('force_detach', ['hdiutil', 'detach', str(mount), '-force'])
            result['force_detach']['elapsed_seconds'] = round(time.monotonic() - before, 3)
            if rc == 0:
                attached = False
            (root / 'proceed').write_text('go')
            for name, p in procs:
                try:
                    _, err = p.communicate(timeout=12)
                except subprocess.TimeoutExpired:
                    os.killpg(p.pid, signal.SIGKILL)
                    _, err = p.communicate(timeout=5)
                    result['writers'][name]['timed_out'] = True
                result['writers'][name]['exit'] = p.returncode
                result['writers'][name]['stderr'] = err[-1000:]
                path = root / (name + '-result.json')
                if path.exists():
                    result['writers'][name]['operations'] = json.loads(path.read_text())
            if rc == 0 and attach(remount, 'reattach'):
                result['remounted_files'] = {}
                for f in sorted(remount.iterdir()):
                    if f.name.startswith('worker-') and f.is_file():
                        result['remounted_files'][f.name] = f.read_text(errors='replace')[:100]
                result['status'] = 'observed'
            else:
                result['status'] = 'detach_or_reattach_failed'
finally:
    (root / 'proceed').touch(exist_ok=True)
    for _, p in procs:
        if p.poll() is None:
            os.killpg(p.pid, signal.SIGKILL)
            p.wait(timeout=5)
    if attached:
        target = remount if result.get('reattach', {}).get('exit') == 0 else mount
        result['cleanup_exit'], out, err = command(['hdiutil', 'detach', str(target), '-force'])
        result['cleanup_stderr'] = err
    (root / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result), flush=True)

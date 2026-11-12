#!/usr/bin/env python3
"""Test filesystem write revocation on a uniquely created disposable APFS image."""
import json
import mmap
import os
from pathlib import Path
import plistlib
import stat
import subprocess
import sys
import tempfile
import time
import uuid

root = Path(tempfile.mkdtemp(prefix='ael-v212-freeze-', dir='/private/tmp'))
image, mount = root / 'fixture.dmg', root / 'mount'
mount.mkdir(mode=0o700)
result = {'fixture': str(root), 'status': 'incomplete'}
attached = False
children = []
def call(args, timeout=40):
    p = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    return {'exit': p.returncode, 'stdout': p.stdout[-1200:], 'stderr': p.stderr[-1200:]}
def attach():
    global attached
    p = subprocess.run(['hdiutil','attach',str(image),'-mountpoint',str(mount),'-nobrowse','-noautoopen','-plist'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=40)
    item = {'exit': p.returncode, 'stderr': p.stderr.decode('utf-8','replace')[-1200:]}
    if p.returncode == 0:
        attached = True
        entries = plistlib.loads(p.stdout).get('system-entities',[])
        match = [e for e in entries if e.get('mount-point') == str(mount)]
        if len(match) != 1 or not match[0].get('dev-entry','').startswith('/dev/disk'):
            raise RuntimeError('fixture mount identity mismatch')
        item['device'] = match[0]['dev-entry']
    return item
worker = r'''import json, mmap, os, pathlib, sys, time
root, base, mode = sys.argv[1:]
root, base = pathlib.Path(root), pathlib.Path(base)
fd = os.open(base / 'target', os.O_RDWR)
mapped = mmap.mmap(fd, 4096, flags=mmap.MAP_SHARED, prot=mmap.PROT_READ | mmap.PROT_WRITE) if mode == 'mmap' else None
(root / ('ready-' + mode)).write_text('ready')
while not (root / 'go').exists(): time.sleep(.01)
try:
    if mode == 'write': os.write(fd, b'write'); os.fsync(fd)
    elif mode == 'pwrite': os.pwrite(fd, b'pwrite', 32); os.fsync(fd)
    elif mode == 'truncate': os.ftruncate(fd, 2048)
    elif mode == 'mmap': mapped[64:68] = b'mmap'; mapped.flush()
    elif mode == 'create_root': (base / 'new-root').write_text('new')
    elif mode == 'create_nested': (base / 'nested' / 'new-nested').write_text('new')
    value = {'result':'allowed'}
except OSError as error:
    value = {'errno': error.errno, 'error': error.strerror}
(root / ('result-' + mode + '.json')).write_text(json.dumps(value))
'''
def spawn(mode):
    p = subprocess.Popen([sys.executable,'-c',worker,str(root),str(mount),mode], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    children.append((mode,p))
    deadline=time.monotonic()+10
    while not (root / ('ready-'+mode)).exists() and time.monotonic() < deadline and p.poll() is None:
        time.sleep(.02)
    if not (root / ('ready-'+mode)).exists(): raise RuntimeError(mode+' not ready')
    return p
def finish(mode,p):
    try: _, err=p.communicate(timeout=10)
    except subprocess.TimeoutExpired:
        p.kill(); _,err=p.communicate(timeout=5)
    path=root / ('result-'+mode+'.json')
    return {'exit':p.returncode,'stderr':err[-1000:],'operation':json.loads(path.read_text()) if path.exists() else None}
try:
    result['create'] = call(['hdiutil','create','-size','64m','-fs','APFS','-volname','AELV212'+uuid.uuid4().hex[:6],str(image)])
    if result['create']['exit'] != 0:
        result['status']='create_failed'
    else:
        result['attach'] = attach()
        if result['attach']['exit']:
            result['status']='attach_failed'
        else:
            (mount / 'target').write_bytes(b'0'*4096)
            p=spawn('write')
            result['remount_readonly'] = call(['mount','-u','-r','-f',str(mount)])
            result['readonly_flag'] = bool(os.statvfs(mount).f_flag & os.ST_RDONLY)
            (root / 'go').write_text('go')
            result['remount_existing_fd'] = finish('write',p)
            (root / 'go').unlink()
            result['detach_between'] = call(['hdiutil','detach',str(mount),'-force'])
            if result['detach_between']['exit'] == 0: attached=False
            if attached:
                result['status']='first_detach_failed'
            else:
                result['reattach'] = attach()
                if result['reattach']['exit']:
                    result['status']='reattach_failed'
                else:
                    target=mount/'target'
                    (mount/'nested').mkdir()
                    modes=['pwrite','truncate','mmap','create_root','create_nested']
                    entries=[(mode,spawn(mode)) for mode in modes]
                    try:
                        os.chflags(target, stat.UF_IMMUTABLE)
                        os.chflags(mount, stat.UF_IMMUTABLE)
                        result['flags_set']={'file':os.stat(target).st_flags,'root':os.stat(mount).st_flags}
                        (root/'go').write_text('go')
                        result['immutable_operations']={mode:finish(mode,p) for mode,p in entries}
                        profile=root/'flag.sb'
                        profile.write_text('(version 1)\n(allow default)\n(deny file-write*)\n(allow file-write* (subpath '+json.dumps(str(mount))+') (literal "/dev/null"))\n(deny file-write-mode (literal '+json.dumps(str(mount))+'))\n(deny network*)\n')
                        attempt='import os,sys; os.chflags(sys.argv[1],0)'
                        result['sandbox_clear_file_flag']=call(['/usr/bin/sandbox-exec','-f',str(profile),sys.executable,'-c',attempt,str(target)])
                        result['file_flags_after_child']=os.stat(target).st_flags
                        result['status']='observed'
                    finally:
                        try: os.chflags(mount,0)
                        except OSError as error: result['clear_root_error']=str(error)
                        try: os.chflags(target,0)
                        except OSError as error: result['clear_file_error']=str(error)
finally:
    (root/'go').touch(exist_ok=True)
    for _,p in children:
        if p.poll() is None:
            p.kill(); p.wait(timeout=5)
    if attached:
        result['cleanup'] = call(['hdiutil','detach',str(mount),'-force'])
    (root/'result.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result),flush=True)

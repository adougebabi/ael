#!/usr/bin/env python3
"""Build and inspect one Epic with three real leaves in a disposable project.

This is a deterministic fixture, not an Agent run or a business deployment.
The output directory must be new or empty; no existing project is opened.
"""
import argparse
import json
import os
from pathlib import Path
import platform
import select
import subprocess
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True, help='complete unpacked AEL distribution')
parser.add_argument('--out', required=True, help='new or empty isolated fixture directory')
args = parser.parse_args()
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('refusing a nonempty fixture directory')
out.mkdir(parents=True, exist_ok=True)
(out / 'ISOLATED-EPIC-FIXTURE').write_text('Disposable test project; not a business workspace.\n')
root = out / 'project'
root.mkdir()
runtime = out / 'runtime'
runtime.mkdir()
env = dict(os.environ, AEL_DATA_DIR=str(runtime))
system = {'darwin': 'darwin', 'linux': 'linux'}.get(platform.system().lower())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit('unsupported fixture platform')
binary = bundle / f'ael-{system}-{arch}'

def run(program, *argv):
    result = subprocess.run([str(program), *map(str, argv)], cwd=root, env=env, capture_output=True, text=True, timeout=60)
    if result.returncode:
        raise RuntimeError(result.stderr or result.stdout)
    return json.loads(result.stdout)

def cli(*argv):
    return run(root / 'ael', *argv, '--project', root, '--json')

def write_json(name, value):
    path = out / name
    path.write_text(json.dumps(value, ensure_ascii=False))
    return path

run(binary, 'init', '--project', root, '--bundle-dir', bundle, '--json')
parent = {'id': 'demo-epic', 'title': '隔离演示：整体交付', 'type': 'epic', 'goal': 'verified',
          'scope': ['fixture results'], 'acceptance': ['整体交付经集成验收']}
cli('task', 'create', '--file', write_json('parent.json', parent))
for index in range(1, 4):
    task_id = f'demo-leaf-{index}'
    leaf = {'id': task_id, 'title': f'隔离叶子 {index}', 'type': 'bug', 'goal': 'verified',
            'acceptance': ['实际结果存在'],
            'checks': [{'id': 'proof', 'kind': 'functional', 'method': 'command',
                        'command': ['sh', '-c', f'test -f result-{index}.txt'],
                        'covers': ['实际结果存在'], 'required': True}]}
    if index == 1:
        leaf['parent'] = 'demo-epic'
    cli('task', 'create', '--file', write_json(f'leaf-{index}.json', leaf))
    if index != 1:
        cli('task', 'link', '--parent', 'demo-epic', '--task', task_id,
            '--milestone', '阶段一' if index == 2 else '阶段二', f'--required={str(index == 2).lower()}')

for index in range(1, 4):
    task_id = f'demo-leaf-{index}'
    for stage in ('define', 'execute'):
        claim = cli('claim', '--task', task_id, '--executor', 'deterministic-fixture')
        cli('context', '--task', task_id)
        artifacts = []
        if stage == 'execute':
            artifact = f'result-{index}.txt'
            (root / artifact).write_text(f'isolated real result {index}\n')
            artifacts = [artifact]
        attempt = claim['attempt']
        submission = {'task': task_id, 'attempt': attempt['id'], 'token': attempt['token'], 'key': attempt['id'],
                      'result': {'status': 'pass', 'summary': f'{stage} fixture result {index}', 'artifacts': artifacts,
                                 'knowledge': {'status': 'unchanged', 'reason': 'isolated fixture output only', 'artifacts': []}}}
        cli('submit', '--file', write_json(f'submission-{index}-{stage}.json', submission))
    checked = cli('verify', '--task', task_id, '--check', 'proof')
    if checked['next_action'] != 'completed':
        raise RuntimeError(f'leaf did not complete: {task_id} / {checked["next_action"]}')

before = cli('task', 'tree', '--task', 'demo-epic')
assert len(before['children']) == 3 and before['required'] == before['required_completed'] == 2
assert before['root']['task']['status'] == 'open' and before['root']['next_action'] == 'wait_integration'

# Read the very same domain tree through a fresh local HTTP server.
proc = subprocess.Popen([str(root / 'ael'), 'dashboard', '--project', str(root), '--addr', '127.0.0.1:0', '--json'],
                        cwd=root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
try:
    ready, _, _ = select.select([proc.stdout], [], [], 15)
    if not ready:
        raise RuntimeError('dashboard did not announce its temporary port')
    line = proc.stdout.readline()
    if not line:
        raise RuntimeError(proc.stderr.read())
    url = json.loads(line)['url']
    with urllib.request.urlopen(url + '/api/session', timeout=10) as response:
        token = json.load(response)['token']
    request = urllib.request.Request(url + '/api/task-tree?id=demo-epic', headers={'Authorization': 'Bearer ' + token})
    with urllib.request.urlopen(request, timeout=10) as response:
        api_tree = json.load(response)
    if before != api_tree:
        raise RuntimeError('CLI and API returned different trees')
finally:
    proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=10)

after = cli('task', 'tree', '--task', 'demo-epic')
if after != before:
    raise RuntimeError('tree changed after server restart')
(out / 'tree.json').write_text(json.dumps(after, ensure_ascii=False, indent=2))
print(json.dumps({'status': 'passed', 'fixture': str(out), 'children': len(after['children']),
                  'required_completed': after['required_completed'], 'parent_status': after['root']['task']['status'],
                  'next_action': after['root']['next_action'], 'cli_api_equal': True,
                  'qualification': 'deterministic isolated fixture, not an Agent run'}, ensure_ascii=False, indent=2))

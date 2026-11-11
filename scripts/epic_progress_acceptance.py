#!/usr/bin/env python3
"""Exercise packaged Epic progress in a new, isolated, deterministic fixture."""
import argparse
import json
import os
from pathlib import Path
import platform
import select
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True, help='complete unpacked AEL distribution')
parser.add_argument('--out', required=True, help='new or empty isolated fixture directory')
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('refusing a nonempty fixture directory')
out.mkdir(parents=True, exist_ok=True)
(out / 'ISOLATED-EPIC-PROGRESS').write_text('Disposable fixture; no business project or real Agent run.\n')
root = out / 'project'
root.mkdir()
runtime = out / 'runtime'
runtime.mkdir()
protocol = out / 'protocol'
protocol.mkdir()
env = dict(os.environ, AEL_DATA_DIR=str(runtime))
system = {'darwin': 'darwin', 'linux': 'linux'}.get(platform.system().lower())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit('unsupported fixture platform')
binary = bundle / f'ael-{system}-{arch}'


def run(program, *argv):
    result = subprocess.run([str(program), *map(str, argv)], cwd=root, env=env,
                            capture_output=True, text=True, timeout=90)
    if result.returncode:
        raise RuntimeError(f'{argv[0]} failed: {result.stderr or result.stdout}')
    return json.loads(result.stdout)


def cli(*argv):
    return run(root / 'ael', *argv, '--project', root, '--json')


def put(name, value):
    file = protocol / (name + '.json')
    file.write_text(json.dumps(value, ensure_ascii=False))
    return file


def check(id, command='true'):
    return {'id': id, 'kind': 'functional', 'method': 'command',
            'command': ['sh', '-c', command], 'covers': ['实际结果存在'], 'required': True}


def create(id, title, type='bug', parent='', checks=None):
    definition = {'id': id, 'title': title, 'type': type, 'goal': 'verified',
                  'acceptance': ['整体结果'] if type == 'epic' else ['实际结果存在']}
    if parent:
        definition['parent'] = parent
    if checks is not None:
        definition['checks'] = checks
    cli('task', 'create', '--file', put(id, definition))


def implement(id):
    for stage in ('define', 'execute'):
        claim = cli('claim', '--task', id, '--executor', 'fixture-' + id)
        cli('context', '--task', id)
        attempt = claim['attempt']
        if id == 'e8-implemented' and stage == 'execute':
            fact = {'summary': json.dumps({'fact': '长事实前缀', 'detail': 'x' * 5000, 'tail': '实际结尾'}, ensure_ascii=False),
                    'artifacts': ['app/common.txt'], 'next': '验收这份隔离结果'}
            cli('progress', '--task', id, '--attempt', attempt['id'], '--token', attempt['token'],
                '--file', put(id + '-progress', fact))
        result = {'status': 'pass', 'summary': stage + ' 真实隔离产物',
                  'artifacts': ['app/common.txt'] if stage == 'execute' else [],
                  'knowledge': {'status': 'unchanged', 'reason': '只使用隔离夹具', 'artifacts': []}}
        submission = {'task': id, 'attempt': attempt['id'], 'token': attempt['token'],
                      'key': attempt['id'], 'result': result}
        cli('submit', '--file', put(id + '-' + stage, submission))


version = run(binary, 'version', '--json')
run(binary, 'init', '--project', root, '--bundle-dir', bundle, '--json')
(root / 'app').mkdir()
(root / 'app' / 'common.txt').write_text('isolated shared candidate\n')
for count in (1, 8, 30):
    create(f'epic-{count}', f'隔离大任务 {count}', type='epic')
    for number in range(count):
        if count == 8:
            name = ['implemented', 'partial', 'failed', 'completed'][number] if number < 4 else f'waiting-{number}'
        else:
            name = f'leaf-{number:02d}'
        id = f'e{count}-{name}'
        checks = [check('proof')]
        if name == 'partial':
            checks = [check('first'), check('second')]
        if name == 'failed':
            checks = [check('proof', "printf 'isolated failure evidence\\n'; exit 1")]
        if name == 'waiting-4':
            checks = [{'id': 'human', 'kind': 'review', 'method': 'human',
                       'covers': ['实际结果存在'], 'required': True}]
        create(id, f'{count} 子任务 {name}', parent=f'epic-{count}', checks=checks)

for name in ('implemented', 'partial', 'failed', 'completed', 'waiting-4'):
    implement('e8-' + name)
    cli('work', 'release', '--parent', 'epic-8', '--runner', 'fixture-e8-' + name,
        '--reason', '隔离夹具显式交接')
for name, unit in (('partial', 'first'), ('failed', 'proof'), ('completed', 'proof')):
    cli('verify', '--task', 'e8-' + name, '--check', unit, '--executor', 'checker-' + name)
    if name != 'completed':
        cli('work', 'release', '--parent', 'epic-8', '--runner', 'checker-' + name,
            '--reason', '隔离夹具保留未完成状态')

before = cli('task', 'progress', '--task', 'epic-8')
assert before['summary']['total'] == 8
assert before['summary']['implemented'] == 4  # the failed candidate is invalidated
assert before['summary']['accepted'] == 1 and before['summary']['partial_checks'] == 1
assert before['summary']['failed'] == 1

with (out / 'server-stderr.log').open('w') as stderr:
    proc = subprocess.Popen([str(root / 'ael'), 'dashboard', '--project', str(root),
                             '--addr', '127.0.0.1:0', '--json'], cwd=root, env=env,
                            stdout=subprocess.PIPE, stderr=stderr, text=True)
    try:
        ready, _, _ = select.select([proc.stdout], [], [], 20)
        if not ready:
            raise RuntimeError('fixture dashboard did not announce its temporary port')
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError('fixture dashboard exited; see server-stderr.log')
        url = json.loads(line)['url']
        manifest = {'isolated': True, 'out': str(out), 'root': str(root), 'runtime': str(runtime),
                    'url': url, 'version': version['version'], 'build': version['build']}
        manifest_path = out / 'manifest.json'
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2))
        test_env = dict(os.environ, AEL_EPIC_FIXTURE=str(manifest_path), AEL_UI_OUT=str(out / 'browser'))
        test = subprocess.run([str(repo / 'web/node_modules/.bin/playwright'), 'test',
                               '--config=playwright.epic.config.ts'],
                              cwd=repo / 'web', env=test_env)
        if test.returncode:
            raise SystemExit(test.returncode)
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=10)

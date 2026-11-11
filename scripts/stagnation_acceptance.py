#!/usr/bin/env python3
"""Exercise packaged no-progress and failure recovery in a disposable fixture."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import platform
import select
import sqlite3
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
bundle, out = Path(args.bundle).resolve(), Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('refusing a nonempty fixture directory')
out.mkdir(parents=True, exist_ok=True)
(out / 'ISOLATED-STAGNATION').write_text('Disposable synthetic fixture; no real business data or Agent.\n')
root, runtime, protocol = out / 'project', out / 'runtime', out / 'protocol'
root.mkdir(); runtime.mkdir(); protocol.mkdir()
env = dict(os.environ, AEL_DATA_DIR=str(runtime))
system = {'darwin': 'darwin', 'linux': 'linux'}.get(platform.system().lower())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit('unsupported fixture platform')
binary = bundle / f'ael-{system}-{arch}'


def run(program, *argv, machine=True):
    command = [str(program), *map(str, argv)] + (['--json'] if machine else [])
    result = subprocess.run(command, cwd=root, env=env, capture_output=True, text=True, timeout=90)
    if result.returncode:
        raise RuntimeError(f'{argv[0]} failed: {result.stderr or result.stdout}')
    return json.loads(result.stdout) if machine else result.stdout


def cli(*argv, machine=True):
    return run(root / 'ael', *argv, '--project', root, machine=machine)


def put(name, value):
    path = protocol / (name + '.json')
    path.write_text(json.dumps(value, ensure_ascii=False))
    return path


def create(id, title, type='bug', parent=''):
    definition = {'id': id, 'title': title, 'type': type, 'goal': 'verified', 'acceptance': ['隔离结果']}
    if parent:
        definition['parent'] = parent
    cli('task', 'create', '--file', put(id, definition))


def fail(id, number):
    claim = cli('claim', '--task', id, '--executor', 'fixture-' + id)
    attempt = claim['attempt']
    result = {'status': 'fail', 'summary': f'同单元隔离失败 {number}'}
    cli('submit', '--file', put(f'{id}-failure-{number}',
        {'task': id, 'attempt': attempt['id'], 'token': attempt['token'], 'key': attempt['id'], 'result': result}))


version = run(binary, 'version')
run(binary, 'init', '--project', root, '--bundle-dir', bundle)
policy_path = root / '.ael/policy.yaml'
policy = json.loads(policy_path.read_text())
policy['no_progress_seconds'] = 2
policy_path.write_text(json.dumps(policy))
data = Path(cli('doctor')['data_dir'])
(data / 'review.txt').write_text('first isolated root-cause evidence\n')
(data / 'review-2.txt').write_text('second isolated root-cause evidence\n')
create('epic', '隔离父任务', type='epic')
create('stuck', '隔离重复失败', parent='epic')
create('idle', '隔离无人接手', parent='epic')
create('cli-stuck', '隔离命令行复核')
create('long', '隔离长阶段')
for task in ('stuck', 'cli-stuck'):
    for number in range(1, 4):
        fail(task, number)
    state = cli('task', 'show', '--task', task)
    assert state['task']['failure_cycles']['define']['count'] == 3
    assert state['task']['failure_cycles']['define']['state'] == 'review_required'
cli('work', 'release', '--parent', 'epic', '--runner', 'fixture-stuck', '--reason', '隔离夹具保留失败现场')
idle = cli('claim', '--task', 'idle', '--executor', 'fixture-idle')
idle_attempt = idle['attempt']
cli('progress', '--task', 'idle', '--attempt', idle_attempt['id'], '--token', idle_attempt['token'],
    '--file', put('legacy-progress', {'summary': '保留的旧式描述', 'next': '等待后续接手'}))
cli('submit', '--file', put('idle-define', {'task': 'idle', 'attempt': idle_attempt['id'], 'token': idle_attempt['token'],
    'key': idle_attempt['id'], 'result': {'status': 'pass', 'summary': '隔离定义完成'}}))
cli('work', 'release', '--parent', 'epic', '--runner', 'fixture-idle', '--reason', '隔离夹具显式交接')

text = cli('task', 'show', '--task', 'cli-stuck', machine=False)
assert '同单元失败：3 次' in text and 'task review-failure' in text and '失败输入：' in text
(out / 'cli-show.txt').write_text(text)
review = {'actor': 'fixture-reviewer', 'tried': '核对输入', 'excluded': '排除路径缺失', 'hypothesis': '隔离配置不符',
          'next': '核对配置后复测', 'recovery': '配置已核对', 'evidence': ['runtime:review.txt']}
cli('task', 'review-failure', '--task', 'cli-stuck', '--file', put('cli-review', review))
cli('resume', '--task', 'cli-stuck')
fail('cli-stuck', 4)
state = cli('task', 'show', '--task', 'cli-stuck')
assert state['task']['failure_cycles']['define']['state'] == 'blocked'
assert state['task']['failure_cycles']['define']['count'] == 4

claim = cli('claim', '--task', 'long', '--executor', 'fixture-long')
deadline = (datetime.now(timezone.utc) + timedelta(minutes=10)).isoformat().replace('+00:00', 'Z')
stage = {'summary': '合成的长检查阶段', 'next': '等待阶段结束', 'stage': 'integration-test', 'expected_until': deadline}
cli('progress', '--task', 'long', '--attempt', claim['attempt']['id'], '--token', claim['attempt']['token'], '--file', put('long-stage', stage))

# Move only the fixture creation clocks, so the UI sees a stale interval without
# sleeping 30 minutes. The live lease and declared deadline remain real values.
with sqlite3.connect(data / 'runtime.db') as db:
    db.execute("UPDATE tasks SET body=json_set(body,'$.created','2000-01-01T00:00:00Z') WHERE id IN ('idle','long')")

with (out / 'server-stderr.log').open('w') as stderr:
    proc = subprocess.Popen([str(root / 'ael'), 'dashboard', '--project', str(root), '--addr', '127.0.0.1:0', '--json'],
                            cwd=root, env=env, stdout=subprocess.PIPE, stderr=stderr, text=True)
    try:
        ready, _, _ = select.select([proc.stdout], [], [], 20)
        if not ready:
            raise RuntimeError('fixture dashboard did not announce a temporary port')
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError('fixture dashboard exited; see server-stderr.log')
        manifest = {'isolated': True, 'out': str(out), 'root': str(root), 'runtime': str(runtime),
                    'url': json.loads(line)['url'], 'version': version['version'], 'build': version['build']}
        manifest_path = out / 'manifest.json'
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2))
        test_env = dict(os.environ, AEL_STAGNATION_FIXTURE=str(manifest_path), AEL_UI_OUT=str(out / 'browser'))
        test = subprocess.run([str(repo / 'web/node_modules/.bin/playwright'), 'test', '--config=playwright.stagnation.config.ts'],
                              cwd=repo / 'web', env=test_env)
        if test.returncode:
            raise SystemExit(test.returncode)
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait(timeout=10)

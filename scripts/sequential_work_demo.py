#!/usr/bin/env python3
"""Exercise ordered parent work in a new isolated project with a fake executor."""
import argparse
import json
import os
from pathlib import Path
import platform
import subprocess
import sys

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
bundle, out = Path(args.bundle).resolve(), Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('refusing a nonempty fixture directory')
out.mkdir(parents=True, exist_ok=True)
(out / 'ISOLATED-SEQUENTIAL-FIXTURE').write_text('Disposable test project; deterministic executor, not an Agent.\n')
root, runtime = out / 'project', out / 'runtime'
root.mkdir()
runtime.mkdir()
env = dict(os.environ, AEL_DATA_DIR=str(runtime))
system = {'darwin': 'darwin', 'linux': 'linux'}.get(platform.system().lower())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit('unsupported fixture platform')
binary = bundle / f'ael-{system}-{arch}'

def run(program, *argv):
    p = subprocess.run([str(program), *map(str, argv)], cwd=root, env=env, capture_output=True, text=True, timeout=90)
    if p.returncode:
        raise RuntimeError(p.stderr or p.stdout)
    return json.loads(p.stdout)

def cli(*argv):
    return run(root / 'ael', *argv, '--project', root, '--json')

def put(name, value):
    path = out / name
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2))
    return path

run(binary, 'init', '--project', root, '--bundle-dir', bundle, '--json')
cli('task', 'create', '--file', put('parent.json', {'id': 'epic', 'title': '顺序执行夹具', 'type': 'epic', 'goal': 'verified', 'acceptance': ['整体']}))
for index, task_id in enumerate(('01-first', '02-second', '03-third')):
    definition = {'id': task_id, 'parent': 'epic', 'title': task_id, 'type': 'bug', 'goal': 'verified',
                  'scope': [f'result-{task_id}.txt'], 'acceptance': ['fixed'],
                  'checks': [{'id': 'proof', 'kind': 'functional', 'method': 'command',
                              'command': ['sh', '-c', f'test "$(cat result-{task_id}.txt)" = fixed'],
                              'covers': ['fixed'], 'required': True}]}
    if index:
        definition['dependencies'] = [('01-first', '02-second')[index - 1]]
    cli('task', 'create', '--file', put(task_id + '.json', definition))

executor = out / 'executor.py'
executor.write_text('''import json, os
from pathlib import Path
ctx = json.loads(Path(os.environ["AEL_CONTEXT"]).read_text())
task = ctx["next"]["definition"]["id"]
artifacts = []
if os.environ["AEL_STEP"] == "execute":
    name = "result-" + task + ".txt"
    value = "broken" if task.endswith("second") and not Path(name).exists() else "fixed"
    Path(name).write_text(value)
    artifacts = [name]
result = {"status":"pass", "summary":"deterministic fixture", "artifacts":artifacts,
          "knowledge":{"status":"unchanged","reason":"isolated fixture only","artifacts":[]}}
Path(os.environ["AEL_RESULT"]).write_text(json.dumps(result))
''')
config = {'command': [sys.executable, str(executor)], 'runner': 'fixture-runner', 'max_steps': 6, 'timeout_seconds': 0}
config_path = put('config.json', config)
first = cli('loop', 'run', '--parent', 'epic', '--runner', 'fixture-runner', '--config', config_path)
assert first['stop_reason'] == 'max_steps' and first['checkpoint']['task'] == '02-second'
one = cli('task', 'show', '--task', '01-first')['task']
two = cli('task', 'show', '--task', '02-second')['task']
three = cli('task', 'show', '--task', '03-third')['task']
assert one['status'] == 'completed' and two['route'] == 'repair' and two['current_step'] == 'execute' and not three['attempts']
checkpoint = cli('work', 'checkpoint', '--parent', 'epic', '--runner', 'fixture-runner')
assert checkpoint['stop_reason'] == 'max_steps' and checkpoint['checkpoint']['task'] == '02-second'
# Every CLI invocation is a new process over the same isolated runtime.
config['max_steps'] = 1
put('config.json', config)
second = cli('loop', 'run', '--parent', 'epic', '--runner', 'fixture-runner', '--config', config_path)
assert second['stop_reason'] == 'max_steps' and not cli('task', 'show', '--task', '03-third')['task']['attempts']
config['max_steps'] = 3
put('config.json', config)
third = cli('loop', 'run', '--parent', 'epic', '--runner', 'fixture-runner', '--config', config_path)
assert third['stop_reason'] == 'max_steps'
two = cli('task', 'show', '--task', '02-second')['task']
three = cli('task', 'show', '--task', '03-third')['task']
assert two['status'] == 'completed' and len(three['attempts']) >= 1

# A second isolated parent demonstrates one uninterrupted invocation: the
# second leaf fails once, is repaired, and only then does the third start.
cli('task', 'create', '--file', put('continuous-parent.json', {'id': 'continuous-epic', 'title': '连续执行', 'type': 'epic', 'goal': 'verified', 'acceptance': ['整体']}))
continuous_ids = ('11-first', '12-second', '13-third')
for index, task_id in enumerate(continuous_ids):
    definition = {'id': task_id, 'parent': 'continuous-epic', 'title': task_id, 'type': 'bug', 'goal': 'verified',
                  'scope': [f'result-{task_id}.txt'], 'acceptance': ['fixed'],
                  'checks': [{'id': 'proof', 'kind': 'functional', 'method': 'command',
                              'command': ['sh', '-c', f'test "$(cat result-{task_id}.txt)" = fixed'],
                              'covers': ['fixed'], 'required': True}]}
    if index:
        definition['dependencies'] = [continuous_ids[index - 1]]
    cli('task', 'create', '--file', put(task_id + '.json', definition))
continuous_config = put('continuous-config.json', {'command': [sys.executable, str(executor)], 'runner': 'continuous-runner', 'max_steps': 9})
continuous = cli('loop', 'run', '--parent', 'continuous-epic', '--runner', 'continuous-runner', '--config', continuous_config)
assert continuous['stop_reason'] == 'max_steps' and continuous['checkpoint']['task'] == '13-third'
continuous_first = cli('task', 'show', '--task', '11-first')['task']
continuous_second = cli('task', 'show', '--task', '12-second')['task']
continuous_third = cli('task', 'show', '--task', '13-third')['task']
assert continuous_first['status'] == continuous_second['status'] == 'completed' and len(continuous_third['attempts']) == 1
assert any(e['result'] == 'fail' for e in continuous_second.get('evidence_history', []))

# An executor absent from PATH must wait without making an attempt.
cli('task', 'create', '--file', put('missing-parent.json', {'id': 'missing-epic', 'title': '能力等待', 'type': 'epic', 'goal': 'verified', 'acceptance': ['整体']}))
cli('task', 'create', '--file', put('missing-leaf.json', {'id': 'missing-leaf', 'parent': 'missing-epic', 'title': '待能力', 'type': 'bug', 'goal': 'verified', 'acceptance': ['结果']}))
missing_config = put('missing-config.json', {'runner': 'missing-runner', 'command': ['ael-intentionally-missing-v204-executor'], 'max_steps': 4})
missing = cli('loop', 'run', '--parent', 'missing-epic', '--runner', 'missing-runner', '--config', missing_config)
assert missing['stop_reason'] == 'wait_capability' and not cli('task', 'show', '--task', 'missing-leaf')['task']['attempts']

# Planned capability requirements also stop before creating an attempt.
cli('task', 'create', '--file', put('cap-parent.json', {'id': 'cap-epic', 'title': '能力匹配', 'type': 'epic', 'goal': 'verified', 'acceptance': ['数据库结果']}))
cap_input = {'request': '准备数据库结果', 'items': []}
prepared = cli('plan', 'prepare', '--task', 'cap-epic', '--file', put('cap-input.json', cap_input))
cap_plan = {'version': 1, 'id': 'cap-plan', 'parent': 'cap-epic', 'parent_revision': 1, 'input_hash': prepared['input_hash'],
            'request': cap_input['request'], 'user_items': [], 'leaves': [{'key': 'database', 'source_ids': [],
            'definition': {'id': 'needs-database', 'title': '数据库结果', 'type': 'bug', 'goal': 'verified',
                           'scope': ['db-result.txt'], 'non_goals': ['不发布'], 'acceptance': ['数据库结果'],
                           'checks': [{'id': 'proof', 'kind': 'functional', 'method': 'command', 'command': ['sh', '-c', 'test -f db-result.txt'], 'covers': ['数据库结果'], 'required': True}]},
            'deliverables': ['db-result.txt'], 'basis': ['合成需求'], 'capabilities': ['database'], 'depends_on': [], 'order': 1, 'required': True, 'milestone': ''}],
            'coverage': [{'acceptance': '数据库结果', 'leaves': ['database'], 'integration': ''}]}
cap_path = put('cap-plan.json', cap_plan)
cap_preview = cli('plan', 'preview', '--file', cap_path)
cli('plan', 'apply', '--file', cap_path, '--digest', cap_preview['digest'])
cap_config = put('cap-config.json', {'runner': 'incapable-runner', 'command': [sys.executable, str(executor)], 'capabilities': ['frontend'], 'max_steps': 4})
cap_wait = cli('loop', 'run', '--parent', 'cap-epic', '--runner', 'incapable-runner', '--config', cap_config)
assert cap_wait['stop_reason'] == 'wait_capability' and 'database' in cap_wait['detail']
assert not cli('task', 'show', '--task', 'needs-database')['task']['attempts']
summary = {'status': 'passed', 'fixture': str(out), 'first': one['status'], 'second_after_repair': two['status'],
           'third_attempts': len(three['attempts']), 'failed_second_before_third': True, 'restart_checkpoint': True,
           'uninterrupted_first_two_and_third_claim': True,
           'missing_capability': missing['stop_reason'], 'planned_capability_wait': cap_wait['stop_reason'],
           'qualification': 'deterministic fixture, not a real Agent run'}
(out / 'summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2))
print(json.dumps(summary, ensure_ascii=False, indent=2))

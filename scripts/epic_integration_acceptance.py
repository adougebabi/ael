#!/usr/bin/env python3
"""Run real local Epic integration fixtures using a packaged AEL binary."""
import argparse
import json
import os
from pathlib import Path
import platform
import select
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
(out / 'ISOLATED-EPIC-INTEGRATION').write_text('Disposable deterministic fixtures; no business data or real Agent.\n')
system = {'darwin': 'darwin', 'linux': 'linux'}.get(platform.system().lower())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit('unsupported fixture platform')
binary = bundle / f'ael-{system}-{arch}'
version = json.loads(subprocess.check_output([str(binary), 'version', '--json'], text=True))


def run(project, program, *argv):
    env = dict(os.environ, AEL_DATA_DIR=str(project['runtime']))
    result = subprocess.run([str(program), *map(str, argv)], cwd=project['root'], env=env,
                            capture_output=True, text=True, timeout=90)
    if result.returncode:
        raise RuntimeError(f'{argv[0]} failed: {result.stderr or result.stdout}')
    return json.loads(result.stdout)


def cli(project, *argv):
    return run(project, project['root'] / 'ael', *argv, '--project', project['root'], '--json')


def git(project, *argv):
    env = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM='1', GIT_TERMINAL_PROMPT='0')
    result = subprocess.run(['git', *argv], cwd=project['root'], env=env, capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise RuntimeError(f'git {argv[0]} failed: {result.stderr}')
    return result.stdout.strip()


def put(project, name, value):
    path = project['protocol'] / (name + '.json')
    path.write_text(json.dumps(value, ensure_ascii=False))
    return path


def create(project, definition):
    return cli(project, 'task', 'create', '--file', put(project, definition['id'], definition))


def init(name):
    directory = out / name
    root, runtime, protocol = directory / 'project', directory / 'runtime', directory / 'protocol'
    root.mkdir(parents=True); runtime.mkdir(); protocol.mkdir()
    project = {'root': root, 'runtime': runtime, 'protocol': protocol}
    git(project, 'init', '-q')
    git(project, 'config', 'user.name', 'isolated fixture')
    git(project, 'config', 'user.email', 'fixture@example.invalid')
    (root / '.gitignore').write_text('.ael/\n')
    git(project, 'add', '.gitignore')
    git(project, 'commit', '-qm', 'isolated base')
    run(project, binary, 'init', '--project', root, '--bundle-dir', bundle, '--json')
    evidence = root / '.ael/integration-evidence/impact.md'
    evidence.parent.mkdir(parents=True, exist_ok=True)
    evidence.write_text('隔离夹具：各模块独立写入不同文件；只声明的连接模块受整合影响。\n')
    return project


def fill_schema(value, schema):
    if 'anyOf' in schema:
        return value
    if schema.get('type') == 'object':
        value = dict(value or {})
        for key, rule in schema.get('properties', {}).items():
            if key not in value and key in schema.get('required', []):
                value[key] = {'string': '', 'integer': 0, 'boolean': False, 'array': [], 'object': {}}.get(rule.get('type'))
            if key in value:
                value[key] = fill_schema(value[key], rule)
        return value
    if schema.get('type') == 'array':
        return [fill_schema(item, schema['items']) for item in (value or [])]
    return value


def check(id, command, covers, affected=None):
    result = {'id': id, 'kind': 'functional', 'method': 'command', 'command': ['sh', '-c', command],
              'covers': [covers], 'required': True}
    if affected is not None:
        result['affected_tasks'] = affected
    return result


def execute_leaf(project, id, filename, commit):
    for stage in ('define', 'execute'):
        claim = cli(project, 'claim', '--task', id, '--executor', 'fixture-' + id)
        cli(project, 'context', '--task', id)
        artifacts = []
        if stage == 'execute':
            (project['root'] / filename).write_text('isolated output ' + id + '\n')
            artifacts = [filename]
            if commit:
                git(project, 'add', filename)
                git(project, 'commit', '-qm', 'isolated ' + id)
        attempt = claim['attempt']
        result = {'status': 'pass', 'summary': 'isolated ' + stage, 'artifacts': artifacts,
                  'knowledge': {'status': 'unchanged', 'reason': '隔离夹具', 'artifacts': []}}
        cli(project, 'submit', '--file', put(project, id + '-' + stage,
            {'task': id, 'attempt': attempt['id'], 'token': attempt['token'], 'key': attempt['id'], 'result': result}))
    finished = cli(project, 'verify', '--task', id, '--check', 'unit', '--executor', 'checker-' + id)
    assert finished['task']['status'] == 'completed'
    return finished['task']


three, eight = init('three'), init('eight')
impact = {'complete': True, 'evidence': '.ael/integration-evidence/impact.md'}
tri_checks = [check('integration', "printf 'isolated integration check\\n'; test -f bridge.txt", '模块协同', ['tri-2']),
              check('recheck-tri-2', 'test -s tri-2.txt', '模块协同', ['tri-2'])]
create(three, {'id': 'tri-epic', 'title': '隔离三叶集成', 'type': 'epic', 'goal': 'verified',
               'acceptance': ['模块协同'], 'integration_impact': impact, 'checks': tri_checks})
for i in range(1, 4):
    id = f'tri-{i}'
    create(three, {'id': id, 'parent': 'tri-epic', 'title': id, 'type': 'bug', 'goal': 'verified',
                   'scope': [id + '.txt'], 'acceptance': ['结果存在'], 'checks': [check('unit', 'test -s ' + id + '.txt', '结果存在')]})
    execute_leaf(three, id, id + '.txt', False)
prepared = cli(three, 'task', 'integrate', '--task', 'tri-epic')
assert prepared['task']['integration']['candidate'] and prepared['task']['status'] == 'open'
failed = cli(three, 'verify', '--task', 'tri-epic', '--check', 'integration', '--executor', 'integrator')
assert failed['task']['status'] == 'open' and failed['task']['evidence'][-1]['result'] == 'fail'

integration_command = ' && '.join(f'test -s module-{i:02d}.txt' for i in range(1, 9))
create(eight, {'id': 'eight-epic', 'title': '隔离八叶完整闭环', 'type': 'epic', 'goal': 'verified',
               'acceptance': ['八个模块协同'], 'integration_impact': impact,
               'checks': [check('integration', integration_command, '八个模块协同', ['eight-08'])]})
request = {'request': '合成交付八个相互独立的模块并执行整体检查。', 'items': []}
prepared = cli(eight, 'plan', 'prepare', '--task', 'eight-epic', '--file', put(eight, 'input', request))
leaves = []
for i in range(1, 9):
    id, filename = f'eight-{i:02d}', f'module-{i:02d}.txt'
    leaves.append({'key': f'key-{i:02d}', 'definition': {'id': id, 'title': id, 'type': 'bug', 'goal': 'verified',
                   'scope': [filename], 'non_goals': ['不发布'], 'acceptance': ['结果存在'], 'checks': [check('unit', 'test -s ' + filename, '结果存在')]},
                   'deliverables': [filename], 'basis': ['隔离需求'], 'capabilities': ['开发'], 'order': i, 'required': True})
proposal = {'version': 1, 'id': 'eight-plan', 'parent': 'eight-epic', 'parent_revision': 1,
            'input_hash': prepared['input_hash'], 'request': request['request'], 'user_items': [], 'leaves': leaves,
            'coverage': [{'acceptance': '八个模块协同', 'leaves': [x['key'] for x in leaves], 'integration': '执行整体检查'}]}
proposal = fill_schema(proposal, prepared['schema'])
proposal_path = put(eight, 'proposal', proposal)
preview = cli(eight, 'plan', 'preview', '--file', proposal_path)
applied = cli(eight, 'plan', 'apply', '--file', proposal_path, '--digest', preview['digest'])
assert len(applied['changes']) == 8
for i in range(1, 9):
    execute_leaf(eight, f'eight-{i:02d}', f'module-{i:02d}.txt', True)
tree = cli(eight, 'task', 'tree', '--task', 'eight-epic')
assert tree['required_completed'] == 8 and tree['root']['task']['status'] == 'open'
prepared = cli(eight, 'task', 'integrate', '--task', 'eight-epic')
assert prepared['task']['integration']['git_head'] == git(eight, 'rev-parse', 'HEAD')
assert len(prepared['task']['integration']['leaves']) == 8
done = cli(eight, 'verify', '--task', 'eight-epic', '--check', 'integration', '--executor', 'integrator')['task']
assert done['status'] == 'completed' and done['result'] == 'verified' and done['completed']
completed_at = done['completed']
create(eight, {'id': 'eight-feedback', 'title': '隔离后续反馈', 'type': 'bug', 'goal': 'verified',
               'acceptance': ['反馈已记录'], 'feedback_of': 'eight-epic'})
after = cli(eight, 'task', 'show', '--task', 'eight-epic')['task']
assert after['completed'] == completed_at and 'eight-feedback' in after['feedback']

processes, handles = [], []
try:
    manifest = {'isolated': True, 'out': str(out), 'version': version['version'], 'build': version['build'], 'projects': {}}
    for name, project in [('three', three), ('eight', eight)]:
        stderr = (out / (name + '-server-stderr.log')).open('w'); handles.append(stderr)
        env = dict(os.environ, AEL_DATA_DIR=str(project['runtime']))
        proc = subprocess.Popen([str(project['root'] / 'ael'), 'dashboard', '--project', str(project['root']),
                                 '--addr', '127.0.0.1:0', '--json'], cwd=project['root'], env=env,
                                stdout=subprocess.PIPE, stderr=stderr, text=True)
        processes.append(proc)
        ready, _, _ = select.select([proc.stdout], [], [], 20)
        if not ready:
            raise RuntimeError(name + ' dashboard did not announce a temporary port')
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError(name + ' dashboard exited; see server log')
        manifest['projects'][name] = {'root': str(project['root']), 'runtime': str(project['runtime']),
                                      'url': json.loads(line)['url']}
    manifest_path = out / 'manifest.json'
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2))
    test_env = dict(os.environ, AEL_EPIC_INTEGRATION_FIXTURE=str(manifest_path), AEL_UI_OUT=str(out / 'browser'))
    test = subprocess.run([str(repo / 'web/node_modules/.bin/playwright'), 'test', '--config=playwright.epic-integration.config.ts'],
                          cwd=repo / 'web', env=test_env)
    if test.returncode:
        raise SystemExit(test.returncode)
finally:
    for proc in processes:
        if proc.poll() is None:
            proc.terminate()
    for proc in processes:
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait(timeout=10)
    for handle in handles:
        handle.close()

#!/usr/bin/env python3
"""Exercise decomposition with deterministic data in a disposable project.

This is a fixture, not a model-generated plan or a claim about business work.
"""
import argparse
import copy
import json
import os
from pathlib import Path
import platform
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('refusing a nonempty fixture directory')
out.mkdir(parents=True, exist_ok=True)
(out / 'ISOLATED-PLAN-FIXTURE').write_text('Disposable test project; not a business workspace.\n')
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

def run(program, *args, success=True):
    p = subprocess.run([str(program), *map(str, args)], cwd=root, env=env, text=True, capture_output=True, timeout=60)
    if (p.returncode == 0) != success:
        raise RuntimeError(p.stderr or p.stdout)
    return json.loads(p.stdout if success else p.stderr)

def cli(*args, success=True):
    return run(root / 'ael', *args, '--project', root, '--json', success=success)

def put(name, value):
    path = out / name
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2))
    return path

def fill_schema(value, schema):
    if 'anyOf' in schema:
        return value
    kind = schema.get('type')
    if kind == 'object':
        value = dict(value or {})
        for key, rule in schema.get('properties', {}).items():
            if key not in value and key in schema.get('required', []):
                kind = rule.get('type')
                value[key] = {'string': '', 'integer': 0, 'boolean': False, 'array': [], 'object': {}}.get(kind)
            if key in value:
                value[key] = fill_schema(value[key], rule)
        return value
    if kind == 'array':
        return [fill_schema(item, schema['items']) for item in (value or [])]
    return value

def check_schema(value, schema):
    if 'anyOf' in schema:
        for option in schema['anyOf']:
            try:
                check_schema(value, option)
                return
            except AssertionError:
                pass
        raise AssertionError('no schema variant matches')
    kind = schema.get('type')
    if kind == 'null':
        assert value is None
    elif kind == 'object':
        assert isinstance(value, dict)
        properties = schema.get('properties', {})
        assert all(key in value for key in schema.get('required', []))
        if schema.get('additionalProperties') is False:
            assert set(value).issubset(properties)
        for key, item in value.items():
            if key in properties:
                check_schema(item, properties[key])
    elif kind == 'array':
        assert isinstance(value, list)
        for item in value:
            check_schema(item, schema['items'])
    elif kind == 'string':
        assert isinstance(value, str)
    elif kind == 'integer':
        assert isinstance(value, int) and not isinstance(value, bool)
    elif kind == 'boolean':
        assert isinstance(value, bool)

run(binary, 'init', '--project', root, '--bundle-dir', bundle, '--json')
parent = {'id': 'plan-epic', 'title': '规划隔离夹具', 'type': 'epic', 'goal': 'verified',
          'acceptance': ['已有结果可用', '空值可被拒绝']}
cli('task', 'create', '--file', put('parent.json', parent))
old = {'id': 'provided-leaf', 'title': '用户已有分项', 'type': 'bug', 'goal': 'verified', 'scope': ['existing.txt'],
       'acceptance': ['已有结果可用'], 'checks': [{'id': 'proof', 'kind': 'functional', 'method': 'command',
       'command': ['sh', '-c', 'test -f existing.txt'], 'covers': ['已有结果可用'], 'required': True}]}
cli('task', 'create', '--file', put('existing.json', old))
for stage in ('define', 'execute'):
    claim = cli('claim', '--task', 'provided-leaf', '--executor', 'deterministic-fixture')
    cli('context', '--task', 'provided-leaf')
    artifacts = []
    if stage == 'execute':
        (root / 'existing.txt').write_text('actual existing fixture result\n')
        artifacts = ['existing.txt']
    a = claim['attempt']
    result = {'status': 'pass', 'summary': stage + ' actual fixture', 'artifacts': artifacts,
              'knowledge': {'status': 'unchanged', 'reason': 'isolated fixture only', 'artifacts': []}}
    cli('submit', '--file', put(stage + '.json', {'task': 'provided-leaf', 'attempt': a['id'], 'token': a['token'], 'key': a['id'], 'result': result}))
before = cli('verify', '--task', 'provided-leaf', '--check', 'proof')['task']
assert before['status'] == 'completed' and before['completed']

request = {'request': '保留已有结果，增加明确的空值校验。',
           'items': [{'id': 'user-item-1', 'description': '已有结果可用', 'existing_task_id': 'provided-leaf'}]}
prepared = cli('plan', 'prepare', '--task', 'plan-epic', '--file', put('input.json', request))
assert prepared['schema']['type'] == 'object' and any(x['definition']['id'] == 'provided-leaf' for x in prepared['existing_leaves'])
proposal = {'version': 1, 'id': 'fixture-plan-1', 'parent': 'plan-epic', 'parent_revision': 1, 'input_hash': prepared['input_hash'], 'request': request['request'],
            'user_items': request['items'], 'leaves': [
    {'key': 'existing', 'source_ids': ['user-item-1'], 'existing_task_id': 'provided-leaf', 'definition': {},
     'deliverables': ['existing.txt'], 'basis': ['用户已有分项'], 'capabilities': ['验证'], 'depends_on': [], 'order': 1, 'required': True, 'milestone': '已有结果'},
    {'key': 'empty-value', 'source_ids': [], 'definition': {'title': '空值校验', 'type': 'bug', 'goal': 'verified',
     'scope': ['validation.txt'], 'non_goals': ['不发布'], 'acceptance': ['空值可被拒绝'],
     'checks': [{'id': 'proof', 'kind': 'functional', 'method': 'command', 'command': ['sh', '-c', 'test -f validation.txt'],
                 'covers': ['空值可被拒绝'], 'required': True}]},
     'deliverables': ['validation.txt'], 'basis': ['原始需求'], 'capabilities': ['开发'], 'depends_on': ['existing'],
     'order': 2, 'required': True, 'milestone': '新增结果'}],
    'coverage': [{'acceptance': '已有结果可用', 'leaves': ['existing'], 'integration': ''},
                 {'acceptance': '空值可被拒绝', 'leaves': ['empty-value'], 'integration': ''}]}
proposal = fill_schema(proposal, prepared['schema'])
check_schema(proposal, prepared['schema'])
omitted = copy.deepcopy(proposal)
omitted['user_items'] = []
omitted['leaves'][0]['source_ids'] = []
error = cli('plan', 'preview', '--file', put('omitted-user-item.json', omitted), success=False)
assert error['code'] == 'PLAN_INPUT_MISMATCH'
path = put('proposal.json', proposal)
preview = cli('plan', 'preview', '--file', path)
assert len(preview['changes']) == 2 and preview['digest']
result = cli('plan', 'apply', '--file', path, '--digest', preview['digest'])
assert result == cli('plan', 'apply', '--file', path, '--digest', preview['digest'])
after = cli('task', 'show', '--task', 'provided-leaf')['task']
assert after['completed'] == before['completed'] and after['result'] == before['result'] and after['definition_hash'] == before['definition_hash']
tree = cli('task', 'tree', '--task', 'plan-epic')
assert len(tree['children']) == 2 and tree['required_completed'] == 1
new_id = next(x['task_id'] for x in result['changes'] if x['key'] == 'empty-value')
new_task = cli('task', 'show', '--task', new_id)['task']
assert 'provided-leaf' in new_task['definition']['dependencies']
assert all(c['kind'] not in ('ui', 'ux') for c in new_task['definition']['checks'])
assert cli('plan', 'preview', '--file', path)['already_applied']

replan = copy.deepcopy(proposal)
replan['id'] = 'fixture-plan-2'
leaf = replan['leaves'][1]
leaf['existing_task_id'] = new_id
leaf['expected_revision'] = new_task['definition']['definition_revision']
leaf['definition'] = new_task['definition']
leaf['definition']['title'] = '空值校验（名称更新）'
replan = fill_schema(replan, prepared['schema'])
check_schema(replan, prepared['schema'])
replan_path = put('replan.json', replan)
repreview = cli('plan', 'preview', '--file', replan_path)
assert any(x['key'] == 'empty-value' and x['action'] == 'revise' and 'title' in x['fields'] for x in repreview['changes'])
cli('plan', 'apply', '--file', replan_path, '--digest', repreview['digest'])
assert cli('task', 'show', '--task', new_id)['task']['definition']['title'] == '空值校验（名称更新）'
assert cli('task', 'show', '--task', 'provided-leaf')['task']['completed'] == before['completed']

invalid = copy.deepcopy(replan)
invalid['id'] = 'invalid-coverage'
invalid['coverage'] = invalid['coverage'][:1]
error = cli('plan', 'preview', '--file', put('invalid.json', invalid), success=False)
assert error['code'] == 'PARENT_COVERAGE_MISSING'

# An undivided, synthetic request receives an explicitly authored fixture
# proposal; this does not claim that a model produced the decomposition.
unsplit_parent = {'id': 'unsplit-epic', 'title': '未拆分夹具', 'type': 'epic', 'goal': 'verified',
                  'acceptance': ['结果可读取', '错误有反馈']}
cli('task', 'create', '--file', put('unsplit-parent.json', unsplit_parent))
unsplit_input = {'request': '交付可读取结果并给出明确错误。', 'items': []}
unsplit_prepared = cli('plan', 'prepare', '--task', 'unsplit-epic', '--file', put('unsplit-input.json', unsplit_input))
unsplit = {'version': 1, 'id': 'unsplit-fixture-plan', 'parent': 'unsplit-epic', 'parent_revision': 1,
           'input_hash': unsplit_prepared['input_hash'], 'request': unsplit_input['request'], 'user_items': [],
           'leaves': [
    {'key': 'readable', 'definition': {'title': '可读取的结果', 'type': 'bug', 'goal': 'verified',
     'scope': ['readable.txt'], 'non_goals': ['不发布'], 'acceptance': ['结果可读取'],
     'checks': [{'id': 'read', 'kind': 'functional', 'method': 'command', 'command': ['sh', '-c', 'test -f readable.txt'], 'covers': ['结果可读取'], 'required': True}]},
     'deliverables': ['readable.txt'], 'basis': ['合成需求'], 'capabilities': ['开发'], 'order': 1, 'required': True},
    {'key': 'error', 'definition': {'title': '明确的错误反馈', 'type': 'bug', 'goal': 'verified',
     'scope': ['error.txt'], 'non_goals': ['不发布'], 'acceptance': ['错误有反馈'],
     'checks': [{'id': 'error', 'kind': 'functional', 'method': 'command', 'command': ['sh', '-c', 'test -f error.txt'], 'covers': ['错误有反馈'], 'required': True}]},
     'deliverables': ['error.txt'], 'basis': ['合成需求'], 'capabilities': ['开发'], 'depends_on': ['readable'], 'order': 2, 'required': True}],
           'coverage': [{'acceptance': '结果可读取', 'leaves': ['readable'], 'integration': ''},
                        {'acceptance': '错误有反馈', 'leaves': ['error'], 'integration': ''}]}
unsplit = fill_schema(unsplit, unsplit_prepared['schema'])
check_schema(unsplit, unsplit_prepared['schema'])
unsplit_path = put('unsplit-proposal.json', unsplit)
unsplit_preview = cli('plan', 'preview', '--file', unsplit_path)
cli('plan', 'apply', '--file', unsplit_path, '--digest', unsplit_preview['digest'])
assert len(cli('task', 'tree', '--task', 'unsplit-epic')['children']) == 2
summary = {'status': 'passed', 'fixture': str(out), 'reused_completed_task': 'provided-leaf', 'new_leaf': new_id,
           'children': 2, 'idempotent_replay': True, 'targeted_replan': True, 'missing_coverage_rejected': True,
           'original_item_omission_rejected': True, 'undivided_proposal': 'deterministic fixture',
           'qualification': 'deterministic fixture, not a model-generated plan'}
(out / 'summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2))
print(json.dumps(summary, ensure_ascii=False, indent=2))

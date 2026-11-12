#!/usr/bin/env python3
"""External CLI finding, repair and revalidation lifecycle acceptance."""
import argparse
import hashlib
import sqlite3
import http.cookiejar
import http.server
import itertools
import json
import os
from pathlib import Path
import platform
import select
import subprocess
import threading
import urllib.parse
import urllib.request
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('fixture directory must be empty')
out.mkdir(parents=True, mode=448)
os.chmod(out, 448)
system = platform.system().lower()
arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}[platform.machine().lower()]
binary = Path(args.bundle).resolve() / f'ael-{system}-{arch}'
env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0', AEL_DATA_DIR=str(out / 'unused-runtime'))
serial = itertools.count(1)
process = None
control = None
log = (out / 'server.log').open('w')
reports = []

def run(argv, cwd=out, input_text=None):
    result = subprocess.run(list(map(str, argv)), cwd=cwd, env=env, input=input_text, capture_output=True, text=True, timeout=90)
    if result.returncode:
        raise RuntimeError(f'{argv[:3]} failed: {result.stderr}')
    return json.loads(result.stdout) if str(argv[0]) == str(binary) else result.stdout

def private(name, value):
    path = out / (name + '.json')
    path.write_text(json.dumps(value))
    path.chmod(384)
    return path

def start(address):
    global process
    process = subprocess.Popen([str(binary), 'server', 'start', '--data-dir', str(out / 'server-data'), '--addr', address, '--json'], cwd=out, env=env, stdout=subprocess.PIPE, stderr=log, text=True)
    if not select.select([process.stdout], [], [], 30)[0]:
        raise RuntimeError('Server startup timeout')
    return json.loads(process.stdout.readline())['url']
try:
    run([binary, 'server', 'bootstrap', '--data-dir', out / 'server-data', '--username', 'owner', '--password-stdin', '--json'], input_text='fixture-admin-password\n')
    base = start('127.0.0.1:0')
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    csrf = ''

    def api(method, path, value=None):
        data = json.dumps(value).encode() if value is not None else None
        headers = {'Origin': base, 'X-AEL-Protocol': '1', 'X-AEL-CSRF': csrf}
        if data is not None:
            headers['Content-Type'] = 'application/json'
        with opener.open(urllib.request.Request(base + path, data=data, method=method, headers=headers), timeout=20) as response:
            return json.load(response)
    csrf = api('POST', '/api/v1/auth/login', {'username': 'owner', 'password': 'fixture-admin-password'})['csrf']
    for name in ('shared', 'other'):
        api('POST', '/api/v1/projects', {'id': name, 'name': 'Lifecycle ' + name})
    projects = {}
    configs = {}
    for name in ('a', 'b'):
        path = out / ('checkout-' + name)
        path.mkdir()
        projects[name] = path
        configs[name] = out / ('config-' + name)
        for command in (['init', '-b', 'main'], ['config', 'user.name', 'Fixture'], ['config', 'user.email', 'fixture@example.invalid']):
            run(['git', *command], path)
        (path / 'spec').mkdir()
        (path / 'spec/rules.md').write_text('# Shared rules\nREMOTE_RULES_SHARED\nUse the fixture input.\n')
        (path / 'app.txt').write_text('original\n')
        (path / 'screen.html').write_text('<!doctype html><html data-ready="0"><body><button id="save" style="width:24px;overflow:hidden;white-space:nowrap" onclick="document.getElementById(\'status\').textContent=\'Saved\'">Save all pending changes</button><p id="status">Ready</p></body></html>')
        (path / '.gitignore').write_text('.ael/\n')
        run(['git', 'add', '.'], path)
        run(['git', 'commit', '-m', 'fixture'], path)
        # Stable candidate commits on both clients derive from the same origin.
        if name == 'b':
            run(['git', 'fetch', str(projects['a']), 'main'], path)
            run(['git', 'reset', '--soft', 'FETCH_HEAD'], path)
        api('POST', '/api/v1/users', {'username': 'client-' + name, 'password': 'fixture-client-' + name + '-password'})
        api('POST', '/api/v1/projects/shared/members', {'username': 'client-' + name, 'role': 'executor' if name == 'a' else 'reviewer'})
        token = api('POST', '/api/v1/tokens', {'username': 'client-' + name, 'project_id': 'shared', 'label': 'fixture'})['token']
        run([binary, 'server', 'profile', 'add', '--name', 'central', '--url', base, '--token-stdin', '--project', path, '--config-dir', configs[name], '--json'], input_text=token + '\n')
        run([binary, 'project', 'connect', '--profile', 'central', '--remote-project', 'shared', '--project', path, '--config-dir', configs[name], '--json'])
    api('POST', '/api/v1/users', {'username': 'observer', 'password': 'fixture-observer-password'})
    api('POST', '/api/v1/projects/shared/members', {'username': 'observer', 'role': 'reader'})

    def cli(name, *argv):
        return run([binary, *argv, '--project', projects[name], '--config-dir', configs[name], '--json'], projects[name])

    def operation(name, action, body, **flags):
        key = 'op-' + str(next(serial))
        file = private(key, dict(body, operation_id=key, idempotency_key=key))
        argv = ['runner', action, '--runner', 'dashboard-' + name, '--file', file]
        for flag, value in flags.items():
            argv.extend(['--' + flag, str(value)])
        return cli(name, *argv)
    cli('a', 'repository', 'register', '--repository', 'fixture-repo', '--operation-id', 'repo', '--idempotency-key', 'repo')
    for name in ('a', 'b'):
        cli(name, 'knowledge', 'index')
        file = private('runner-' + name, {'id': 'dashboard-' + name, 'workspace_id': 'workspace-' + name, 'checkout_id': 'checkout-' + name, 'repository': 'fixture-repo', 'version': 'fixture', 'alias': 'External client ' + name, 'machine': 'fixture-host-' + name, 'capabilities': ['development', 'scenario'] if name == 'a' else ['scenario'], 'expected_revision': 'absent'})
        cli(name, 'runner', 'register', '--file', file, '--operation-id', 'runner-' + name, '--idempotency-key', 'runner-' + name)
    parent_id = 'lifecycle-epic'
    scan_id = 'lifecycle-scan'
    ready_command = ['python3', '-c', "from pathlib import Path; assert 'data-ready=\"1\"' in Path('screen.html').read_text(); print('ready marker passed')"]
    parent = {'id': parent_id, 'title': 'Lifecycle parent', 'type': 'epic', 'goal': 'verified', 'scope': ['screen.html'], 'acceptance': ['fix validated'], 'checks': [{'id': 'integration', 'kind': 'functional', 'method': 'scenario', 'covers': ['fix validated'], 'required': True}]}
    cli('a', 'task', 'create', '--file', private('parent', parent), '--operation-id', 'parent', '--idempotency-key', 'parent')
    definition = {'id': scan_id, 'title': 'Lifecycle scan', 'type': 'qa', 'intent': 'inspect', 'goal': 'report', 'parent': parent_id, 'scope': ['screen.html'], 'acceptance': ['label readable', 'ready marker'], 'knowledge': ['source:spec/rules.md'], 'checks': [{'id': 'visual', 'kind': 'ui', 'method': 'scenario', 'covers': ['label readable'], 'required': True}, {'id': 'ready', 'kind': 'functional', 'method': 'command', 'command': ready_command, 'covers': ['ready marker'], 'required': True}]}
    cli('a', 'task', 'create', '--file', private('scan', definition), '--operation-id', 'scan', '--idempotency-key', 'scan')

    def claim(task):
        state = cli('a', 'task', 'show', '--task', task)
        revision = state['revision']
        key = 'claim-context-' + str(next(serial))
        observed = cli('a', 'candidate', 'capture', '--task', task, '--repository', 'fixture-repo', '--checkout', 'checkout-a', '--workspace', 'workspace-a', '--runner', 'dashboard-a', '--expected-revision', revision, '--operation-id', key, '--idempotency-key', key)
        context = cli('a', 'context', 'publish', '--task', task, '--observation', observed['id'], '--expected-revision', revision, '--operation-id', key + '-pub', '--idempotency-key', key + '-pub')
        file = out / ('lease-' + str(next(serial)) + '.json')
        operation('a', 'claim', {'task_id': task, 'parent_id': parent_id, 'context_id': context['id'], 'expected_revision': revision}, out=file)
        attempt = json.loads(file.read_text())['attempt']
        current_context = cli('a', 'context', 'show', '--context', context['id'])
        return {'task': task, 'state': state, 'revision': revision, 'attempt': attempt, 'context': current_context, 'lease': {'task_id': task, 'attempt_id': attempt['id'], 'token': attempt['token'], 'fence': attempt['fence']}}

    def submit(work, refs=(), status='pass', summary='External fixture responsibility', method='', exit_code=None):
        observed = operation('a', 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
        bindings = []
        for file, ref in refs:
            key = 'artifact-' + str(next(serial))
            lease = private(key + '-lease', dict(work['lease'], runner_id='dashboard-a', expected_revision=work['revision'], lease_until=work['attempt']['lease_until']))
            upload_file = Path(file)
            mime_flags = []
            if not ref.startswith('runtime:') and upload_file.suffix.lower() not in ('.txt', '.md', '.log', '.json', '.csv', '.png', '.jpg', '.jpeg', '.mp4'):
                # The external host snapshots source bytes as an opaque leased
                # attachment. Its binding still names the original candidate path.
                upload_file = out / (key + '.bin')
                upload_file.write_bytes(Path(file).read_bytes())
                upload_file.chmod(0o600)
                mime_flags = ['--mime', 'application/octet-stream']
            item = cli('a', 'artifact', 'upload', '--task', work['task'], '--attempt', work['attempt']['id'], '--candidate', observed['candidate_id'], '--file', upload_file, '--lease-file', lease, '--expected-revision', work['revision'], '--operation-id', key, '--idempotency-key', key, *mime_flags)
            bindings.append({'path': ref, 'artifact_id': item['id']})
        result = {'status': status, 'summary': summary, 'artifacts': [item['artifact_id'] for item in bindings]}
        if work['state']['current_step'] == 'execute':
            result['knowledge'] = {'status': 'unchanged', 'reason': 'Published project rules remain unchanged'}
        extra = {}
        if method:
            extra['method'] = method
            extra['inputs'] = ['screen.html']
        if exit_code is not None:
            extra['exit_code'] = exit_code
        return operation('a', 'submit', dict(work['lease'], expected_revision=work['revision'], candidate_id=observed['candidate_id'], result=result, bindings=bindings, **extra))

    def browser_observation(name, label):
        image = out / (label + '.png')
        trace = out / (label + '-trace.json')
        script = """import {chromium} from MODULE;
import fs from 'node:fs';
const browser=await chromium.launch({headless:true});
try {const page=await browser.newPage({viewport:{width:640,height:480}});
await page.setContent(fs.readFileSync(INPUT,'utf8'));
const clipped=await page.locator('#save').evaluate(el=>el.scrollWidth>el.clientWidth);
const ready=await page.locator('html').getAttribute('data-ready');
const before=await page.locator('#status').innerText();await page.locator('#save').click();const after=await page.locator('#status').innerText();
await page.screenshot({path:IMAGE});const result={clipped,ready,before,after,viewport:{width:640,height:480},steps:[{action:'click #save',before,after}]};fs.writeFileSync(TRACE,JSON.stringify(result));console.log(JSON.stringify(result));
} finally {await browser.close();}
"""
        for key, value in {'MODULE': (repo / 'web/node_modules/playwright/index.mjs').as_uri(), 'INPUT': str(projects[name] / 'screen.html'), 'IMAGE': str(image), 'TRACE': str(trace)}.items():
            script = script.replace(key, json.dumps(value))
        observed = json.loads(run(['node', '--input-type=module', '-e', script]))
        assert observed['after'] == 'Saved'
        return observed, image, trace

    def verify_visual(task, label):
        work = claim(task)
        assert work['attempt']['unit'] == 'check:visual'
        observed, image, trace = browser_observation('a', label)
        candidate = operation('a', 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')['candidate_id']
        status = 'fail' if observed['clipped'] else 'pass'
        report = private(label + '-report', {'candidate': candidate, 'check': 'visual', 'definition_revision': work['state']['definition_revision'], 'plan_revision': work['state']['plan_revision'], 'method': 'scenario', 'source': 'external-scenario:dashboard-a', 'covers': ['label readable'], 'observations': json.dumps(observed)})
        result = submit(work, [(report, 'runtime:report.json'), (image, 'runtime:screen.png'), (trace, 'runtime:trace.json')], status, 'External Chromium measured actual label geometry', 'scenario')
        return result, candidate

    def verify_ready(task, expect_pass):
        work = claim(task)
        assert work['attempt']['unit'] == 'check:ready'
        process_result = subprocess.run(ready_command, cwd=projects['a'], env=env, capture_output=True, text=True)
        assert (process_result.returncode == 0) == expect_pass
        candidate = operation('a', 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')['candidate_id']
        label = 'ready-' + str(next(serial))
        log_file = out / (label + '.log')
        log_file.write_text(process_result.stdout + process_result.stderr)
        report = private(label, {'candidate': candidate, 'check': 'ready', 'definition_revision': work['state']['definition_revision'], 'plan_revision': work['state']['plan_revision'], 'method': 'command', 'source': 'external-command:dashboard-a', 'covers': ['ready marker'], 'observations': process_result.stdout + process_result.stderr})
        return submit(work, [(report, 'runtime:report.json'), (log_file, 'runtime:command.log')], 'pass' if expect_pass else 'fail', 'External host checked the actual ready marker', 'command', process_result.returncode)

    # An inspection responsibility keeps its input unchanged throughout delivery.
    work = claim(scan_id)
    while work['state']['current_step'] in ('define', 'analysis'):
        submit(work)
        work = claim(scan_id)
    assert work['state']['current_step'] == 'execute'
    inspection_report = out / 'inspection.md'
    inspection_report.write_text('External fixture will deliver real image and command observations.\n')
    submit(work, [(inspection_report, 'runtime:inspection.md')])

    def report_finding(task, label, rule='ui.clipped-label'):
        state = cli('b', 'task', 'show', '--task', task)
        key = 'scan-context-' + str(next(serial))
        observed = cli('b', 'candidate', 'capture', '--task', task, '--repository', 'fixture-repo', '--checkout', 'checkout-b', '--workspace', 'workspace-b', '--runner', 'dashboard-b', '--expected-revision', state['revision'], '--operation-id', key, '--idempotency-key', key)
        context = cli('b', 'context', 'publish', '--task', task, '--observation', observed['id'], '--expected-revision', state['revision'], '--operation-id', key + '-pub', '--idempotency-key', key + '-pub')
        observation, image_file, trace_file = browser_observation('b', label)
        assert observation['clipped']
        report_file = private(label + '-raw', {'observations': observation, 'method': 'external Chromium fixture'})
        def upload(file, source):
            key = 'scan-artifact-' + str(next(serial))
            return cli('b', 'artifact', 'upload', '--task', task, '--candidate', observed['candidate_id'], '--context', context['id'], '--file', file, '--source', source, '--expected-revision', state['revision'], '--operation-id', key, '--idempotency-key', key)['id']
        image = upload(image_file, 'external inspection screenshot')
        trace = upload(trace_file, 'external interaction trace')
        report = upload(report_file, 'context-check:' + context['id'] + ':visual')
        key = 'finding-' + str(next(serial))
        body = {'context_id': context['id'], 'task_id': task, 'candidate_id': observed['candidate_id'], 'check_id': 'visual', 'expected_revision': state['revision'], 'artifact_id': report, 'source': {'method': 'scenario', 'tool': 'external-Chromium-fixture', 'run_id': label}, 'findings': [{'rule': rule, 'title': 'Clipped save label', 'kind': 'ui', 'severity': 'high', 'route': '/fixture', 'location': {'path': 'screen.html', 'selector': '#save'}, 'expected': 'The complete Save label is readable', 'actual': 'Measured scroll width exceeds client width', 'reproduction': ['Open the fixture at 640x480', 'Observe Save and click it'], 'impact': 'The full action cannot be read', 'viewport': {'width': 640, 'height': 480}, 'environment': 'external Chromium fixture', 'artifacts': [image], 'trace_artifacts': [trace]}], 'operation_id': key, 'idempotency_key': key}
        result = cli('b', 'finding', 'report', '--file', private(key, body))
        return result['findings'][0]['id'], observed['candidate_id'], image

    finding_id, original_candidate, original_image = report_finding(scan_id, 'original-finding')
    demo_id, _, _ = report_finding(scan_id, 'decision-demo', 'ui.decision-demo')
    link_demo_id, _, _ = report_finding(scan_id, 'link-demo', 'ui.link-demo')
    failed, _ = verify_visual(scan_id, 'original-check')
    assert failed['next']['current_step'] == 'verify'
    source_result = verify_ready(scan_id, False)
    assert source_result['next']['next_action'] == 'completed'
    source_before = cli('a', 'task', 'show', '--task', scan_id)['task']
    assert source_before['result'] == 'report_delivered_with_findings'
    assert [item['result'] for item in source_before['evidence']] == ['fail', 'fail']

    def finding_request(id, key, **extra):
        view = cli('a', 'finding', 'show', '--finding', id)
        return dict(finding_id=id, expected_revision=view['revision'], expected_task_revision=view['source_task_revision'], reason='Explicit external fixture repair request', operation_id=key, idempotency_key=key, **extra)

    denied_request = private('scanner-repair', finding_request(finding_id, 'scanner-repair'))
    denied = subprocess.run([str(binary), 'finding', 'repair', '--finding', finding_id, '--file', str(denied_request), '--project', str(projects['b']), '--config-dir', str(configs['b']), '--json'], cwd=projects['b'], env=env, capture_output=True, text=True)
    assert denied.returncode != 0 and 'FORBIDDEN' in denied.stderr
    repair_request = private('create-repair', finding_request(finding_id, 'create-repair'))
    first = cli('a', 'finding', 'repair', '--finding', finding_id, '--file', repair_request)
    repair_id = first['repair']['task_id']
    repeated = cli('a', 'finding', 'repair', '--finding', finding_id, '--file', repair_request)
    assert repeated['repair']['task_id'] == repair_id
    alternate = json.loads(repair_request.read_text())
    alternate['operation_id'] = alternate['idempotency_key'] = 'same-finding-new-key'
    assert cli('a', 'finding', 'repair', '--finding', finding_id, '--file', private('alternate-repair', alternate))['repair']['task_id'] == repair_id
    # Reopen the persisted Server and recover the original operation receipt.
    process.terminate()
    process.wait(timeout=10)
    assert start(urllib.parse.urlparse(base).netloc) == base
    recovered = cli('a', 'runner', 'retry', '--pending', first['operation_file'])
    assert recovered['repair']['task_id'] == repair_id
    tree = cli('a', 'task', 'tree', '--task', parent_id)
    assert tree['required'] == 2 and tree['required_completed'] == 1
    assert tree['root']['task']['status'] == 'open'
    reports.extend(['scanner-role-denied-repair', 'one-repair-across-keys', 'server-restart-original-receipt', 'required-repair-blocks-parent'])

    phases = {}
    def implement(label, transform):
        work = claim(repair_id)
        while work['state']['current_step'] in ('define', 'analysis'):
            submit(work)
            work = claim(repair_id)
        assert work['state']['current_step'] == 'execute'
        assert work['context']['finding_sources'][0]['finding_id'] == finding_id
        source = projects['a'] / 'screen.html'
        source.write_text(transform(source.read_text()))
        run(['git', 'add', 'screen.html'], projects['a'])
        run(['git', 'commit', '-m', label], projects['a'])
        result = submit(work, [(source, 'screen.html')], summary='External host edited and committed ' + label)
        phases[label] = result['next']['task']['candidate']
        assert cli('a', 'finding', 'show', '--finding', finding_id)['status'] in ('open', 'resolved')
        return result

    def publish_grouping():
        document = projects['a'] / 'spec/change.md'
        metadata = {'id': 'lifecycle-decision', 'kind': 'decision', 'status': 'accepted', 'title': 'Lifecycle repair decision', 'summary': 'Actual repair and exact candidate revalidation', 'tasks': [scan_id, repair_id], 'paths': ['screen.html']}
        document.write_text('---\n' + json.dumps(metadata) + '\n---\n\nActual fixture repair evidence and revalidation.\n')
        run(['git', 'add', 'spec/change.md'], projects['a'])
        run(['git', 'commit', '-m', 'fixture evolution source'], projects['a'])
        cli('a', 'knowledge', 'index')
        definition = {'id': 'evolution-source', 'title': 'Evolution source snapshot', 'type': 'docs', 'goal': 'verified', 'scope': ['spec/change.md'], 'acceptance': ['source available'], 'knowledge': ['lifecycle-decision']}
        created = cli('a', 'task', 'create', '--file', private('group-definition', definition), '--operation-id', 'group-definition', '--idempotency-key', 'group-definition')
        observed = cli('a', 'candidate', 'capture', '--task', 'evolution-source', '--repository', 'fixture-repo', '--checkout', 'checkout-a', '--workspace', 'workspace-a', '--runner', 'dashboard-a', '--expected-revision', created['revision'], '--operation-id', 'group-observe', '--idempotency-key', 'group-observe')
        cli('a', 'context', 'publish', '--task', 'evolution-source', '--observation', observed['id'], '--expected-revision', created['revision'], '--operation-id', 'group-context', '--idempotency-key', 'group-context')

    def recurrence():
        source = projects['a'] / 'screen.html'
        source.write_text(source.read_text().replace('width:240px', 'width:24px'))
        run(['git', 'add', 'screen.html'], projects['a'])
        run(['git', 'commit', '-m', 'fixture recurrence snapshot'], projects['a'])
        run(['git', 'fetch', str(projects['a']), 'main'], projects['b'])
        run(['git', 'reset', '--hard', 'FETCH_HEAD'], projects['b'])
        cli('b', 'knowledge', 'index')
        new_id, candidate, _ = report_finding(repair_id, 'recurrence')
        view = cli('a', 'finding', 'show', '--finding', new_id)
        assert new_id != finding_id and view['recurrence_of'] == finding_id
        old = cli('a', 'finding', 'show', '--finding', finding_id)
        assert old['status'] == 'resolved' and old['candidate_id'] == original_candidate
        assert old['occurrences'][0]['input']['artifacts'][0] == original_image
        new_repair = cli('a', 'finding', 'repair', '--finding', new_id, '--file', private('recurrence-repair', finding_request(new_id, 'recurrence-repair')))
        assert new_repair['repair']['task_id'] != repair_id
        phases['recurrence'] = {'finding': new_id, 'candidate': candidate, 'repair': new_repair['repair']['task_id']}
        return phases['recurrence']

    nonce = os.urandom(20).hex()
    class Control(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_POST(self):
            try:
                value = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
                if value.get('nonce') != nonce:
                    self.send_error(403)
                    return
                action = value['action']
                result = {'ok': True}
                if action == 'implement-b':
                    implement('candidate-b', lambda source: source.replace('width:24px', 'width:240px'))
                    assert cli('a', 'finding', 'show', '--finding', finding_id)['status'] == 'open'
                elif action == 'verify-b':
                    _, phases['b'] = verify_visual(repair_id, 'candidate-b-visual')
                elif action == 'fail-ready':
                    verify_ready(repair_id, False)
                elif action == 'implement-c':
                    implement('candidate-c', lambda source: source.replace('data-ready="0"', 'data-ready="1"'))
                elif action == 'verify-c':
                    _, phases['c'] = verify_visual(repair_id, 'candidate-c-visual')
                elif action == 'complete-repair':
                    completed = verify_ready(repair_id, True)
                    assert completed['next']['next_action'] == 'completed'
                    current = cli('a', 'task', 'show', '--task', scan_id)['task']
                    assert current['result'] == source_before['result'] and current['evidence'] == source_before['evidence']
                elif action == 'prepare-existing':
                    existing = dict(definition, id='existing-repair', title='Existing authorized repair', type='bug', intent='change', goal='verified', source='existing-authorized-work')
                    cli('a', 'task', 'create', '--file', private('existing-definition', existing), '--operation-id', 'existing-task', '--idempotency-key', 'existing-task')
                elif action == 'linked-context':
                    state = cli('a', 'task', 'show', '--task', 'existing-repair')
                    observed = cli('a', 'candidate', 'capture', '--task', 'existing-repair', '--repository', 'fixture-repo', '--checkout', 'checkout-a', '--workspace', 'workspace-a', '--runner', 'dashboard-a', '--expected-revision', state['revision'], '--operation-id', 'existing-observe', '--idempotency-key', 'existing-observe')
                    context = cli('a', 'context', 'publish', '--task', 'existing-repair', '--observation', observed['id'], '--expected-revision', state['revision'], '--operation-id', 'existing-context', '--idempotency-key', 'existing-context')
                    sources = cli('a', 'context', 'show', '--context', context['id'])['finding_sources']
                    assert sources[0]['finding_id'] == link_demo_id
                elif action == 'group-evolution':
                    publish_grouping()
                elif action == 'recurrence':
                    result.update(recurrence())
                else:
                    self.send_error(400)
                    return
                reports.append(action)
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps(result).encode())
            except Exception as error:
                self.send_response(500)
                self.end_headers()
                self.wfile.write(str(error).encode())

    control = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Control)
    threading.Thread(target=control.serve_forever, daemon=True).start()
    manifest = private('manifest', {'url': base, 'control': 'http://127.0.0.1:' + str(control.server_address[1]), 'nonce': nonce, 'out': str(out), 'finding': finding_id, 'demo': demo_id, 'link_demo': link_demo_id, 'repair': repair_id, 'scan': scan_id, 'parent': parent_id, 'original_candidate': original_candidate})
    result = subprocess.run([str(repo / 'web/node_modules/.bin/playwright'), 'test', '--config=playwright.finding-lifecycle.config.ts'], cwd=repo / 'web', env=dict(env, AEL_FINDING_LIFECYCLE_FIXTURE=str(manifest), AEL_UI_OUT=str(out / 'browser')), timeout=300)
    if result.returncode:
        raise RuntimeError('packaged finding lifecycle browser acceptance failed')
    (out / 'summary.json').write_text(json.dumps({'result': 'PASS', 'mode': 'external CLI fixture with actual edits, Git commits, command tests and Chromium screenshots', 'actions': reports, 'phases': phases}, indent=2))
    print(json.dumps({'result': 'PASS', 'out': str(out), 'actions': reports}))
finally:
    if process is not None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
    if control is not None:
        control.shutdown()
        control.server_close()
    log.close()

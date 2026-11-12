#!/usr/bin/env python3
"""External CLI clients upload real images and exercise packaged human review."""
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
        api('POST', '/api/v1/projects', {'id': name, 'name': 'Review ' + name})
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
        (path / '.gitignore').write_text('.ael/\n')
        run(['git', 'add', '.'], path)
        run(['git', 'commit', '-m', 'fixture'], path)
        # Stable candidate commits on both clients derive from the same origin.
        if name == 'b':
            run(['git', 'fetch', str(projects['a']), 'main'], path)
            run(['git', 'reset', '--soft', 'FETCH_HEAD'], path)
        api('POST', '/api/v1/users', {'username': 'client-' + name, 'password': 'fixture-client-' + name + '-password'})
        api('POST', '/api/v1/projects/shared/members', {'username': 'client-' + name, 'role': 'executor'})
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
        file = private('runner-' + name, {'id': 'dashboard-' + name, 'workspace_id': 'workspace-' + name, 'checkout_id': 'checkout-' + name, 'repository': 'fixture-repo', 'version': 'fixture', 'alias': 'External client ' + name, 'machine': 'fixture-host-' + name, 'capabilities': ['development'], 'expected_revision': 'absent'})
        cli(name, 'runner', 'register', '--file', file, '--operation-id', 'runner-' + name, '--idempotency-key', 'runner-' + name)
    command = ['python3', '-c', "from pathlib import Path; assert Path('app.txt').read_text() == 'fixed\\n'; print('external fixture passed')"]

    # The protocol helpers below are the same calls used by collaboration tests.
    def claim(name, task):
        state = cli(name, 'task', 'show', '--task', task)
        revision = state['revision']
        key = 'context-' + str(next(serial))
        observed = cli(name, 'candidate', 'capture', '--task', task, '--repository', 'fixture-repo', '--checkout', 'checkout-' + name, '--workspace', 'workspace-' + name, '--runner', 'dashboard-' + name, '--expected-revision', revision, '--operation-id', key, '--idempotency-key', key)
        context = cli(name, 'context', 'publish', '--task', task, '--observation', observed['id'], '--expected-revision', revision, '--operation-id', key + '-pub', '--idempotency-key', key + '-pub')
        file = out / ('lease-' + str(next(serial)) + '.json')
        operation(name, 'claim', {'task_id': task, 'context_id': context['id'], 'expected_revision': revision}, out=file)
        attempt = json.loads(file.read_text())['attempt']
        return {'name': name, 'task': task, 'revision': revision, 'state': state, 'attempt': attempt, 'lease': {'task_id': task, 'attempt_id': attempt['id'], 'token': attempt['token'], 'fence': attempt['fence']}}

    def submit(work, refs=None):
        observed = operation(work['name'], 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
        candidate = observed['candidate_id']
        bindings = []
        for file, ref in refs or []:
            key = 'upload-' + str(next(serial))
            lease = private(key + '-lease', dict(work['lease'], runner_id='dashboard-' + work['name'], expected_revision=work['revision'], lease_until=work['attempt']['lease_until']))
            artifact = cli(work['name'], 'artifact', 'upload', '--task', work['task'], '--attempt', work['attempt']['id'], '--candidate', candidate, '--file', file, '--lease-file', lease, '--expected-revision', work['revision'], '--operation-id', key, '--idempotency-key', key)
            bindings.append({'path': ref, 'artifact_id': artifact['id']})
        result = {'status': 'pass', 'summary': 'external fixture completed responsibility', 'artifacts': [b['artifact_id'] for b in bindings]}
        extra = {}
        if work['state']['current_step'] == 'execute':
            result['knowledge'] = {'status': 'unchanged', 'reason': 'rules unchanged'}
        if work['state']['current_step'] == 'verify':
            extra = {'method': 'command', 'exit_code': 0, 'inputs': ['app.txt']}
        return operation(work['name'], 'submit', dict(work['lease'], expected_revision=work['revision'], candidate_id=candidate, result=result, bindings=bindings, **extra))
    render="""import {chromium} from MODULE;
const browser=await chromium.launch({headless:true});
try {const page=await browser.newPage({viewport:{width:640,height:480}});
for(const [file,padding] of [[BASELINE,24],[ACTUAL,12]]) {await page.setContent('<!doctype html><html><body><main style="padding:'+padding+'px"><h1>Review fixture</h1><button>Save changes</button><p>External host rendered this image.</p></main></body></html>');await page.screenshot({path:file});}
} finally {await browser.close();}
"""
    for key, value in {'MODULE': (repo / 'web/node_modules/playwright/index.mjs').as_uri(), 'BASELINE': str(out / 'baseline.png'), 'ACTUAL': str(out / 'actual.png')}.items():
        render = render.replace(key, json.dumps(value))
    run(['node', '--input-type=module', '-e', render])
    tasks = {}

    def upload_context(name, task, file, source):
        state = cli(name, 'task', 'show', '--task', task)
        revision = state['revision']
        key = 'image-context-' + str(next(serial))
        observed = cli(name, 'candidate', 'capture', '--task', task, '--repository', 'fixture-repo', '--checkout', 'checkout-' + name, '--workspace', 'workspace-' + name, '--runner', 'dashboard-' + name, '--expected-revision', revision, '--operation-id', key, '--idempotency-key', key)
        assert observed['candidate_id'] == state['task']['candidate']
        context = cli(name, 'context', 'publish', '--task', task, '--observation', observed['id'], '--expected-revision', revision, '--operation-id', key + '-pub', '--idempotency-key', key + '-pub')
        key = 'image-' + str(next(serial))
        return cli(name, 'artifact', 'upload', '--task', task, '--candidate', observed['candidate_id'], '--context', context['id'], '--file', file, '--source', source, '--expected-revision', revision, '--operation-id', key, '--idempotency-key', key)['id']

    def prepare(task):
        (projects['a'] / 'app.txt').write_text('original\n')
        definition = {'id': task, 'title': 'Review ' + task, 'type': 'bug', 'goal': 'verified', 'scope': ['app.txt'], 'acceptance': ['fixed'], 'knowledge': ['source:spec/rules.md'], 'checks': [{'id': 'visual', 'kind': 'ui', 'method': 'human', 'covers': ['fixed'], 'required': True}, {'id': 'unit', 'kind': 'functional', 'method': 'command', 'command': command, 'covers': ['fixed'], 'required': True}]}
        cli('a', 'task', 'create', '--file', private('definition-' + task, definition), '--operation-id', 'create-' + task, '--idempotency-key', 'create-' + task)
        work = claim('a', task)
        while work['state']['current_step'] in ('define', 'analysis'):
            submit(work)
            work = claim('a', task)
        assert work['state']['current_step'] == 'execute'
        (projects['a'] / 'app.txt').write_text('fixed\n')
        submit(work, [(projects['a'] / 'app.txt', 'app.txt')])
        # This check precedes new general observations and catches the old input /
        # accepted output ordering bug in the normal external execution path.
        status = api('GET', '/api/v1/projects/shared/dashboard/review-candidate?id=' + task)
        assert status['matches'], status
        (projects['b'] / 'app.txt').write_text('fixed\n')
        baseline = upload_context('b', task, out / 'baseline.png', 'explicit-design-reference; external Chromium; viewport=640x480; page=/fixture')
        actual = upload_context('a', task, out / 'actual.png', 'actual-host-render; external Chromium; viewport=640x480; page=/fixture')
        state = cli('a', 'task', 'show', '--task', task)
        tasks[task] = {'baseline': baseline, 'actual': actual, 'candidate': state['task']['candidate'], 'revision': state['definition_revision'], 'plan_revision': state['plan_revision']}
    for task in ('api-pass', 'api-fail', 'api-blocked', 'opinion', 'browser', 'stale', 'missing', 'postchange', 'slow'):
        prepare(task)

    def review(task, result, key):
        value = tasks[task]
        return api('POST', '/api/v1/projects/shared/reviews', {'action': 'review_result', 'id': task, 'unit': 'visual', 'executor': 'forged-executor', 'reason': 'Synthetic reviewer protocol fixture with real images', 'recovery': 'Review layout after correction', 'result': result, 'artifacts': [value['actual']], 'comparison': {'baseline': value['baseline'], 'actual': value['actual']}, 'candidate': value['candidate'], 'revision': value['revision'], 'plan_revision': value['plan_revision'], 'request_key': key})
    for result in ('pass', 'fail', 'blocked'):
        task = 'api-' + result
        first = review(task, result, 'formal-' + result)
        again = review(task, result, 'formal-' + result)
        assert first == again
        state = cli('a', 'task', 'show', '--task', task)
        evidence = state['task']['evidence'][-1]
        assert evidence['source'] == 'human:owner' and evidence['result'] == result and (evidence['comparison']['baseline']['uploader'] == 'client-b')
        assert state['task']['status'] == 'open'
        assert first['next_action'] and len([attempt for attempt in state['task']['attempts'] if attempt['unit'] == 'check:visual']) == 1
        reports.append('real-http-' + result + '-and-idempotent-receipt')
    value = tasks['opinion']
    opinion = {'action': 'review_pending', 'id': 'opinion', 'unit': 'visual', 'reason': 'Explicit provisional opinion', 'result': 'pass', 'artifacts': [value['actual']], 'comparison': {'baseline': value['baseline'], 'actual': value['actual']}, 'candidate': value['candidate'], 'revision': value['revision'], 'plan_revision': value['plan_revision'], 'request_key': 'opinion'}
    note = api('POST', '/api/v1/projects/shared/reviews', opinion)
    assert note['status'] == 'pending' and (not cli('a', 'task', 'show', '--task', 'opinion')['task']['evidence'])
    opinion.update(action='review_result', request_key='apply-opinion', review_note_id=note['id'])
    api('POST', '/api/v1/projects/shared/reviews', opinion)
    assert cli('a', 'task', 'show', '--task', 'opinion')['task']['human_review_notes'][0]['status'] == 'confirmed'
    reports.append('provisional-opinion-non-gating-and-explicit-application')
    stale = tasks['stale']
    api('POST', '/api/v1/projects/shared/reviews', {
        'action': 'review_pending', 'id': 'stale', 'unit': 'visual',
        'reason': 'Opinion for the original candidate', 'result': 'pass',
        'artifacts': [stale['actual']], 'candidate': stale['candidate'],
        'revision': stale['revision'], 'plan_revision': stale['plan_revision'],
        'request_key': 'stale-opinion',
    })
    # Unfinished upload remains visible by its reserved opaque artifact ID.
    missing = tasks['missing']
    state = cli('a', 'task', 'show', '--task', 'missing')
    pending = api('POST', '/api/v1/projects/shared/artifacts/uploads', {'operation_id': 'unfinished', 'idempotency_key': 'unfinished', 'expected_revision': state['revision'], 'task_id': 'missing', 'candidate': missing['candidate'], 'filename': 'unfinished.png', 'mime': 'image/png', 'size': (out / 'actual.png').stat().st_size, 'sha256': hashlib.sha256((out / 'actual.png').read_bytes()).hexdigest(), 'source': 'unfinished external fixture'})
    missing['unfinished'] = pending['artifact_id']
    # Fault injection is confined to this disposable Server's metadata and byte
    # store, representing externally lost/purged historical evidence.
    database = out / 'server-data/projects' / hashlib.sha256(b'shared').hexdigest()[:32] / 'runtime/runtime.db'
    with sqlite3.connect(database) as connection:
        connection.execute("UPDATE artifact_objects SET status='purged' WHERE id=?", (missing['baseline'],))
    object_file = out / 'server-data/attachments' / hashlib.sha256(b'shared').hexdigest()[:32] / 'objects' / missing['actual']
    object_file.unlink()
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
                if value['action'] == 'change-candidate':
                    review('stale', 'fail', 'invalidate-stale')
                    work = claim('a', 'stale')
                    (projects['a'] / 'app.txt').write_text('fixed again\n')
                    submit(work, [(projects['a'] / 'app.txt', 'app.txt')])
                    reports.append('candidate-updated-through-external-repair')
                elif value['action'] == 'change-definition':
                    state = cli('a', 'task', 'show', '--task', 'postchange')
                    definition = state['task']['definition']
                    definition['checks'].append({'id': 'additional', 'kind': 'review', 'method': 'human', 'covers': ['fixed'], 'required': True})
                    file = private('post-review-definition', definition)
                    cli('a', 'task', 'edit', '--task', 'postchange', '--file', file, '--reason', 'new required review', '--operation-id', 'post-definition', '--idempotency-key', 'post-definition', '--expected-revision', state['revision'])
                    reports.append('definition-changed-after-committed-review')
                else:
                    self.send_error(400)
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"ok":true}')
            except Exception as error:
                self.send_response(500)
                self.end_headers()
                self.wfile.write(str(error).encode())
    control = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Control)
    threading.Thread(target=control.serve_forever, daemon=True).start()
    manifest = private('manifest', {'url': base, 'control': 'http://127.0.0.1:' + str(control.server_address[1]), 'nonce': nonce, 'out': str(out), 'tasks': tasks})
    result = subprocess.run([str(repo / 'web/node_modules/.bin/playwright'), 'test', '--config=playwright.review.config.ts'], cwd=repo / 'web', env=dict(env, AEL_REVIEW_FIXTURE=str(manifest), AEL_UI_OUT=str(out / 'browser')), timeout=240)
    if result.returncode:
        raise RuntimeError('packaged human review browser acceptance failed')
    (projects['a'] / 'app.txt').write_text('fixed\n')
    work = claim('a', 'browser')
    assert work['state']['current_step'] == 'verify'
    run(command, projects['a'])
    observed = operation('a', 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
    report = private('final-command-report', {'candidate': observed['candidate_id'], 'check': 'unit', 'definition_revision': work['state']['definition_revision'], 'plan_revision': work['state']['plan_revision'], 'method': 'command', 'source': 'external-command:dashboard-a', 'covers': ['fixed'], 'observations': 'External host asserted the candidate file after the human check'})
    result = submit(work, [(report, 'runtime:report.json')])
    assert result['next']['next_action'] == 'completed'
    reports.append('external-command-after-human-completes-required-gates')
    (out / 'summary.json').write_text(json.dumps({'result': 'PASS', 'mode': 'external dual CLI clients, real rendered images and packaged browser; synthetic reviewer decisions', 'actions': reports}, indent=2))
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

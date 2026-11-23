#!/usr/bin/env python3
"""Disposable local Server for workbench UI restoration and browser checks.

Every task, user and image below is a deterministic fixture created through the
real CLI/HTTP protocol of a throwaway Server. Nothing is read from or written to
real business projects. Two relations are injected into this throwaway database
only: legacy `definition.parent` links under a non-epic `feature` parent, which
mirrors imported history (formal epic membership requires an `epic` parent).

The Server keeps running until the process receives SIGINT/SIGTERM. The manifest
(`manifest.json` in --out) lists URL, accounts and task IDs for browser tests.
"""
import argparse
import hashlib
import http.cookiejar
import itertools
import json
import os
import platform
import select
import signal
import sqlite3
import subprocess
import sys
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', required=True, help='ael binary built from this checkout')
parser.add_argument('--out', required=True, help='new empty directory for disposable data')
parser.add_argument('--addr', default='127.0.0.1:0')
parser.add_argument('--no-wait', action='store_true', help='stop the Server after seeding')
parser.add_argument('--browser-test', action='store_true', help='run web/tests/workbench-demo.spec.ts, then stop')
parser.add_argument('--ui-url', default='', help='optional dev UI URL proxying this Server (AEL_WB_URL)')
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
binary = Path(args.binary).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('fixture directory must be empty')
out.mkdir(parents=True, mode=0o700, exist_ok=True)
(out / 'ISOLATED-UI-FIXTURE').write_text('Disposable AEL workbench fixture. No real business data.\n')
env = {k: v for k, v in os.environ.items() if not k.startswith(('GIT_', 'AEL_'))}
env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0',
           AEL_DATA_DIR=str(out / 'unused-runtime'), AEL_CLIENT_CONFIG_DIR=str(out / 'machine-clients'),
           AEL_INSTALL_PREFIX=str(out / 'unused-install'))
serial = itertools.count(1)
log = (out / 'server.log').open('w')
process = None


def run(argv, cwd=out, input_text=None):
    result = subprocess.run(list(map(str, argv)), cwd=cwd, env=env, input=input_text, capture_output=True, text=True, timeout=120)
    if result.returncode:
        raise RuntimeError(f'{list(map(str, argv))[:4]} failed: {result.stderr or result.stdout}')
    return json.loads(result.stdout) if str(argv[0]) == str(binary) else result.stdout


def private(name, value):
    path = out / 'files' / (name + '.json')
    path.parent.mkdir(exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False))
    path.chmod(0o600)
    return path


def start(address):
    global process
    process = subprocess.Popen([str(binary), 'server', 'start', '--data-dir', str(out / 'server-data'), '--addr', address, '--json'],
                               cwd=out, env=env, stdout=subprocess.PIPE, stderr=log, text=True)
    if not select.select([process.stdout], [], [], 30)[0]:
        raise RuntimeError('Server startup timeout')
    return json.loads(process.stdout.readline())['url']


def stop():
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


def project_db(project):
    return out / 'server-data/projects' / hashlib.sha256(project.encode()).hexdigest()[:32] / 'runtime/runtime.db'


try:
    run([binary, 'server', 'bootstrap', '--data-dir', out / 'server-data', '--username', 'owner', '--password-stdin', '--json'],
        input_text='fixture-admin-password\n')
    base = start(args.addr)
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    csrf = ''

    def api(method, path, value=None):
        data = json.dumps(value).encode() if value is not None else None
        headers = {'Origin': base, 'X-AEL-Protocol': '1', 'X-AEL-CSRF': csrf}
        if data is not None:
            headers['Content-Type'] = 'application/json'
        with opener.open(urllib.request.Request(base + path, data=data, method=method, headers=headers), timeout=30) as response:
            return json.load(response)

    csrf = api('POST', '/api/v1/auth/login', {'username': 'owner', 'password': 'fixture-admin-password'})['csrf']
    api('POST', '/api/v1/projects', {'id': 'shared', 'name': '示例协作项目'})
    api('POST', '/api/v1/projects', {'id': 'other', 'name': '另一个项目'})
    projects, configs = {}, {}
    for name in ('a', 'b', 'c'):
        path = out / ('checkout-' + name)
        path.mkdir()
        projects[name], configs[name] = path, out / ('config-' + name)
        for command in (['init', '-b', 'main'], ['config', 'user.name', 'Fixture'], ['config', 'user.email', 'fixture@example.invalid']):
            run(['git', *command], path)
        (path / 'spec').mkdir()
        (path / 'spec/rules.md').write_text('# 示例规范\n界面日期使用同一时区。\n')
        (path / 'AGENTS.md').write_text('# 示例 Agent 指引（输入）\n先读当前任务和规范。\n')
        (path / 'app.txt').write_text('original\n')
        (path / '.gitignore').write_text('.ael/\n')
        run(['git', 'add', '.'], path)
        run(['git', 'commit', '-m', 'fixture'], path)
        if name != 'a':
            run(['git', 'fetch', str(projects['a']), 'main'], path)
            run(['git', 'reset', '--soft', 'FETCH_HEAD'], path)
        api('POST', '/api/v1/users', {'username': 'client-' + name, 'password': 'fixture-client-' + name + '-password'})
        api('POST', '/api/v1/projects/shared/members', {'username': 'client-' + name, 'role': 'executor'})
        token = api('POST', '/api/v1/tokens', {'username': 'client-' + name, 'project_id': 'shared', 'label': 'fixture ' + name})['token']
        run([binary, 'server', 'profile', 'add', '--name', 'central', '--url', base, '--token-stdin', '--project', path,
             '--config-dir', configs[name], '--json'], input_text=token + '\n')
        run([binary, 'project', 'connect', '--profile', 'central', '--remote-project', 'shared', '--project', path,
             '--config-dir', configs[name], '--json'])
    for user, role in (('reviewer', 'reviewer'), ('observer', 'reader')):
        api('POST', '/api/v1/users', {'username': user, 'password': 'fixture-' + user + '-password'})
        api('POST', '/api/v1/projects/shared/members', {'username': user, 'role': role})

    def cli(name, *argv):
        return run([binary, *argv, '--project', projects[name], '--config-dir', configs[name], '--json'], projects[name])

    def operation(name, action, body, **flags):
        key = 'op-' + str(next(serial))
        file = private(key, dict(body, operation_id=key, idempotency_key=key))
        argv = ['runner', action, '--runner', 'fixture-' + name, '--file', file]
        for flag, value in flags.items():
            argv.extend(['--' + flag, str(value)])
        return cli(name, *argv)

    cli('a', 'repository', 'register', '--repository', 'fixture-repo', '--operation-id', 'repo', '--idempotency-key', 'repo')
    for name in ('a', 'b', 'c'):
        cli(name, 'knowledge', 'index')
        file = private('runner-' + name, {'id': 'fixture-' + name, 'workspace_id': 'workspace-' + name, 'checkout_id': 'checkout-' + name,
                                          'repository': 'fixture-repo', 'version': 'fixture', 'alias': {'a': '开发 Agent', 'b': '开发 Agent B', 'c': '开发 Agent C'}[name],
                                          'machine': 'fixture-host-' + name, 'capabilities': ['development'], 'expected_revision': 'absent'})
        cli(name, 'runner', 'register', '--file', file, '--operation-id', 'runner-' + name, '--idempotency-key', 'runner-' + name)
    command = ['python3', '-c', "from pathlib import Path; assert Path('app.txt').read_text() == 'fixed\\n'; print('fixture passed')"]

    def create(task, title, kind='bug', checks=None, request='', acceptance=None, scope=None, goal='verified'):
        definition = {'id': task, 'title': title, 'type': kind, 'goal': goal, 'scope': scope or ['app.txt'],
                      'acceptance': acceptance or ['结果符合描述'], 'knowledge': ['source:spec/rules.md'],
                      'checks': checks if checks is not None else [{'id': 'unit', 'kind': 'functional', 'method': 'command', 'command': command, 'covers': (acceptance or ['结果符合描述'])[:1], 'required': True}]}
        if request:
            definition['original_request'] = request
        return cli('a', 'task', 'create', '--file', private('definition-' + task, definition), '--operation-id', 'create-' + task, '--idempotency-key', 'create-' + task)

    def claim(name, task, share_source=None):
        state = cli(name, 'task', 'show', '--task', task)
        revision = state['revision']
        key = 'context-' + str(next(serial))
        observed = cli(name, 'candidate', 'capture', '--task', task, '--repository', 'fixture-repo', '--checkout', 'checkout-' + name,
                       '--workspace', 'workspace-' + name, '--runner', 'fixture-' + name, '--expected-revision', revision,
                       '--operation-id', key, '--idempotency-key', key)
        context = cli(name, 'context', 'publish', '--task', task, '--observation', observed['id'], '--expected-revision', revision,
                      '--operation-id', key + '-pub', '--idempotency-key', key + '-pub',
                      *(['--share-source', share_source] if share_source else []))
        file = out / 'files' / ('lease-' + str(next(serial)) + '.json')
        operation(name, 'claim', {'task_id': task, 'context_id': context['id'], 'expected_revision': revision}, out=file)
        attempt = json.loads(file.read_text())['attempt']
        return {'name': name, 'task': task, 'revision': revision, 'state': state, 'attempt': attempt,
                'lease': {'task_id': task, 'attempt_id': attempt['id'], 'token': attempt['token'], 'fence': attempt['fence']}}

    def submit(work, refs=None, status='pass', summary='外部 Agent 完成当前责任', **extra):
        observed = operation(work['name'], 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
        candidate = observed['candidate_id']
        bindings = []
        for file, ref in refs or []:
            key = 'upload-' + str(next(serial))
            lease = private(key + '-lease', dict(work['lease'], runner_id='fixture-' + work['name'], expected_revision=work['revision'], lease_until=work['attempt']['lease_until']))
            artifact = cli(work['name'], 'artifact', 'upload', '--task', work['task'], '--attempt', work['attempt']['id'], '--candidate', candidate,
                           '--file', file, '--lease-file', lease, '--expected-revision', work['revision'], '--operation-id', key, '--idempotency-key', key)
            bindings.append({'path': ref, 'artifact_id': artifact['id']})
        result = {'status': status, 'summary': summary, 'artifacts': [b['artifact_id'] for b in bindings]}
        if work['state']['current_step'] == 'execute':
            result['knowledge'] = {'status': 'unchanged', 'reason': '规范未变化'}
        body = dict(work['lease'], expected_revision=work['revision'], candidate_id=candidate, result=result, bindings=bindings, **extra)
        if work['state']['current_step'] == 'verify' and 'method' not in extra:
            body.update(method='command', exit_code=0, inputs=['app.txt'])
        return operation(work['name'], 'submit', body)

    def to_execute(name, task, share_source=None):
        (projects[name] / 'app.txt').write_text('original\n')
        work = claim(name, task, share_source)
        while work['state']['current_step'] in ('define', 'analysis'):
            submit(work, summary='定义与分析已核对')
            work = claim(name, task)
        assert work['state']['current_step'] == 'execute', work['state']['current_step']
        return work

    def analyse_only(name, task, summary):
        # Define and analysis pass through the real protocol; implementation
        # is left unclaimed so the task waits at execute.
        work = claim(name, task)
        while True:
            submit(work, summary=summary)
            if cli(name, 'task', 'show', '--task', task)['current_step'] not in ('define', 'analysis'):
                return
            work = claim(name, task)

    def execute(name, task, summary, outputs=(), share_source=None):
        work = to_execute(name, task, share_source)
        (projects[name] / 'app.txt').write_text('fixed\n')
        for rel, body in outputs:
            (projects[name] / rel).write_text(body)
        return submit(work, [(projects[name] / 'app.txt', 'app.txt')] + [(projects[name] / rel, rel) for rel, _ in outputs], summary=summary)

    def finish(name, task, summary, report_text, outputs=(), share_source=None):
        execute(name, task, summary, outputs, share_source)
        if outputs:
            cli(name, 'knowledge', 'index')  # a changed indexed source must be re-indexed before new context
        work = claim(name, task)
        assert work['state']['current_step'] == 'verify', work['state']['current_step']
        run(command, projects[name])
        observed = operation(name, 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
        report = private('report-' + task, {'candidate': observed['candidate_id'], 'check': 'unit', 'definition_revision': work['state']['definition_revision'],
                                             'plan_revision': work['state']['plan_revision'], 'method': 'command', 'source': 'external-command:fixture-' + name,
                                             'covers': ['结果符合描述'], 'summary': report_text, 'observations': report_text})
        result = submit(work, [(report, 'runtime:report.json')], summary=summary)
        assert result['next']['next_action'] == 'completed', result

    render = """import {chromium} from MODULE;
const browser=await chromium.launch({headless:true,executablePath:CHROME||undefined});
try {const page=await browser.newPage({viewport:{width:960,height:600}});
for (const [file,title,accent] of SHOTS) {await page.setContent('<!doctype html><html><body style="margin:0;font:16px sans-serif;background:#f6f7fb"><main style="padding:32px"><h1 style="color:'+accent+'">'+title+'</h1><p>示例界面截图（夹具）</p><div style="display:grid;gap:12px;max-width:600px">'+[1,2,3].map(i=>'<div style="background:white;border:1px solid #e7e9f1;border-radius:10px;padding:16px">项目 '+i+'</div>').join('')+'</div></main></body></html>');await page.screenshot({path:file});}
} finally {await browser.close();}
"""
    shots = [[str(out / 'schedule-after.png'), '日程视图 · 调整后', '#5956dc'], [str(out / 'account-before.png'), '账户菜单 · 调整前', '#666'],
             [str(out / 'account-after.png'), '账户菜单 · 调整后', '#5956dc']]
    chrome = '/usr/bin/chromium' if Path('/usr/bin/chromium').exists() else ''
    for key, value in {'MODULE': (repo / 'web/node_modules/playwright/index.mjs').as_uri(), 'SHOTS': shots, 'CHROME': chrome}.items():
        render = render.replace(key, json.dumps(value, ensure_ascii=False))
    run(['node', '--input-type=module', '-e', render])

    def upload_context(name, task, file, source):
        state = cli(name, 'task', 'show', '--task', task)
        revision = state['revision']
        key = 'image-context-' + str(next(serial))
        observed = cli(name, 'candidate', 'capture', '--task', task, '--repository', 'fixture-repo', '--checkout', 'checkout-' + name,
                       '--workspace', 'workspace-' + name, '--runner', 'fixture-' + name, '--expected-revision', revision,
                       '--operation-id', key, '--idempotency-key', key)
        context = cli(name, 'context', 'publish', '--task', task, '--observation', observed['id'], '--expected-revision', revision,
                      '--operation-id', key + '-pub', '--idempotency-key', key + '-pub')
        key = 'image-' + str(next(serial))
        return cli(name, 'artifact', 'upload', '--task', task, '--candidate', observed['candidate_id'], '--context', context['id'], '--file', file,
                   '--source', source, '--expected-revision', revision, '--operation-id', key, '--idempotency-key', key)['id']

    manifest = {'url': base, 'out': str(out), 'accounts': {'owner': 'fixture-admin-password', 'reviewer': 'fixture-reviewer-password',
                'observer': 'fixture-observer-password', 'client-a': 'fixture-client-a-password'}, 'tasks': {}}
    unit = lambda covers: {'id': 'unit', 'kind': 'functional', 'method': 'command', 'command': command, 'covers': covers, 'required': True}

    # Feature parent with six legacy children: three completed, three open.
    create('plugin-parent', '能力插件化与对话主链迁移', 'feature', checks=[], request='把对话主链迁移到能力插件，并保留现有媒体与记忆能力。',
           acceptance=['六个子项全部交付', '整体回归与交付说明通过'])
    children = [('child-model', '按任务选择模型'), ('child-context', '上下文分层装配'), ('child-chat', '对话主链迁移'),
                ('child-media', '接入媒体能力'), ('child-schedule', '日程视图与生活状态验收'), ('child-memory', '记忆回想接入')]
    for task, title in children[:3]:
        create(task, title)
        finish('a', task, title + '已交付', title + '：外部命令验证通过')
    create('child-media', '接入媒体能力')
    work = to_execute('b', 'child-media')
    operation('b', 'progress', dict(work['lease'], summary='媒体调用已接入，正在补失败重试', next='补齐失败重试后提交', stage='execute'))
    create('child-schedule', '日程视图与生活状态验收', checks=[unit(['日程清楚、时间一致']), {'id': 'visual', 'kind': 'ui', 'method': 'human', 'covers': ['日程清楚、时间一致'], 'required': True}],
           acceptance=['日程清楚、时间一致'])
    execute('a', 'child-schedule', '日程视图已调整')
    schedule_image = upload_context('a', 'child-schedule', out / 'schedule-after.png', 'actual-host-render; viewport=960x600; page=/schedule')
    create('child-memory', '记忆回想接入')

    # A standalone task with two independent human checks and before/after images.
    create('account-menu', '账户菜单与个人设置入口', 'improvement',
           checks=[unit(['菜单清楚、好用']), {'id': 'visual', 'kind': 'ui', 'method': 'human', 'covers': ['菜单清楚、好用'], 'required': True},
                   {'id': 'keyboard', 'kind': 'review', 'method': 'human', 'covers': ['菜单清楚、好用'], 'required': True}],
           request='新版账户菜单要清楚、好用，键盘可以打开和退出。', acceptance=['菜单清楚、好用'])
    execute('a', 'account-menu', '菜单层级已整理，等待确认新版交互')
    before = upload_context('b', 'account-menu', out / 'account-before.png', 'explicit-design-reference; previous version')
    after = upload_context('a', 'account-menu', out / 'account-after.png', 'actual-host-render; viewport=960x600; page=/account')

    # Direct observation without attachments, and an explicit screenshot demand.
    create('copy-review', '调整空状态提示文案', 'improvement', checks=[{'id': 'wording', 'kind': 'review', 'method': 'human', 'covers': ['空状态提示读起来清楚'], 'required': True}],
           request='调整空状态提示文案，读起来清楚即可。', acceptance=['空状态提示读起来清楚'])
    execute('a', 'copy-review', '空状态提示文案已调整')
    create('evidence-review', '登录页错误提示样式', 'bug', checks=[{'id': 'visual', 'kind': 'ui', 'method': 'human', 'covers': ['错误提示与输入框对齐'], 'required': True}],
           request='修复登录页错误提示错位，必须提供截图作为验收证据。', acceptance=['错误提示与输入框对齐'])
    execute('a', 'evidence-review', '错误提示样式已修复，尚未上传截图')
    broken_image = upload_context('a', 'evidence-review', out / 'account-before.png', 'fixture upload whose stored bytes are corrupted below')

    # Environment-blocked verification.
    create('schedule-fix', '修复初始日程生成失败', request='首次打开时日程生成失败，需要修复并验证。')
    execute('a', 'schedule-fix', '修复已提交')
    work = claim('a', 'schedule-fix')
    submit(work, status='blocked', summary='测试环境缺少时区配置，场景验证暂未完成', method='command', exit_code=1, inputs=['app.txt'])

    # Active work, unclaimed work, completed and cancelled history.
    create('context-trim', '收敛运行时上下文长度', 'improvement', request='去除重复状态字段，保留记忆摘要中的关键事实。')
    work = to_execute('c', 'context-trim')
    operation('c', 'progress', dict(work['lease'], summary='已去除重复状态字段，正在核对记忆摘要', next='核对后提交', stage='execute'))
    create('media-retry', '补齐媒体调用失败重试', request='媒体调用失败时按分类重试。')
    analyse_only('a', 'media-retry', '失败分类已整理，等待接手实施恢复策略')
    create('legacy-trellis', '核对旧 Trellis 任务与当前实现', request='旧方案保留了记录，需要对应到当前版本后继续。')
    create('cli-onboarding', '完成轻量 CLI 项目接入', 'feature', request='新机器可绑定已有项目，并读取当前任务。')
    finish('a', 'cli-onboarding', '新机器可绑定已有项目，并读取当前任务', '项目接入验收结果：绑定与读取通过', share_source='spec/rules.md')
    # AGENTS.md is both an input snapshot and, here, this task's own output.
    create('context-guide', '更新 Agent 上下文恢复指引', 'docs', request='压缩后先核对进度与实际代码，再继续当前项。', scope=['app.txt', 'AGENTS.md'])
    original_agents = (projects['a'] / 'AGENTS.md').read_text()
    finish('a', 'context-guide', '压缩后先核对进度与实际代码，再继续当前项', '恢复指引已更新并通过检查',
           outputs=[('AGENTS.md', original_agents + '\n## 恢复\n压缩后先核对进度与实际代码，再继续当前项。\n')], share_source='AGENTS.md')
    create('old-cancel', '旧版日程导出', request='已由新的日程视图取代。')
    revision = cli('a', 'task', 'show', '--task', 'old-cancel')['revision']
    api('POST', '/api/v1/projects/shared/tasks/old-cancel/cancel', {'reason': '已由新的日程视图取代', 'expected_revision': revision, 'operation_id': 'cancel-old', 'idempotency_key': 'cancel-old'})

    # Delivery chain through the real repository-mode submit (the path agents
    # use): source, Markdown, logs and a check report, where two submissions
    # carry the same source and scope note (9 objects, 7 distinct contents).
    npc_accept = ['已保存的响应在另一进程继续处理，不重复请求', '恢复与重放不重复产生业务结果', '来源取消后拒绝继续或抑制结果']
    create('npc-recovery', 'NPC 持久响应的跨进程恢复验收', 'improvement', acceptance=npc_accept, request='补齐已保存 NPC 响应在进程重启后的恢复验收覆盖。',
           scope=['app.txt', 'internal/npc/recovery_test.go', 'docs/npc-scope.md'], checks=[{'id': 'scenario', 'kind': 'functional', 'method': 'command', 'command': command, 'covers': npc_accept, 'required': True}])
    work = to_execute('a', 'npc-recovery')
    root = projects['a']
    (root / 'app.txt').write_text('fixed\n')
    (root / 'internal/npc').mkdir(parents=True, exist_ok=True)
    (root / 'internal/npc/recovery_test.go').write_text('package npc\n\nimport "testing"\n\n// Fixture source: five recovery scenarios across two processes.\nfunc TestCheckpointAcrossProcesses(t *testing.T) {\n\tfor _, scenario := range []string{"opportunity_accept", "opportunity_decline", "opportunity_cancelled_source", "moment_comment", "moment_cancelled_source"} {\n\t\tt.Run(scenario, func(t *testing.T) {\n\t\t\tfirst := runChild(t, "checkpoint", scenario)\n\t\t\tresumed := runChild(t, "resume", scenario)\n\t\t\tif first.PID == resumed.PID {\n\t\t\t\tt.Fatal("same process")\n\t\t\t}\n\t\t})\n\t}\n}\n')
    (root / 'docs').mkdir(exist_ok=True)
    (root / 'docs/npc-scope.md').write_text('# 验收范围与限制\n\n## 覆盖\n\n- 五个受控恢复场景\n- 隔离数据库与受控 HTTP\n\n## 不覆盖\n\n- 真实模型的自然语言质量\n- 绕过清理逻辑的强制终止\n')
    runtime_dir = out / 'runtime-npc'
    runtime_dir.mkdir()
    operation('a', 'submit', dict(work['lease'], expected_revision=work['revision'], result={'status': 'pass', 'summary': '补齐跨进程恢复测试，并写明验收范围与限制', 'artifacts': ['internal/npc/recovery_test.go', 'docs/npc-scope.md'], 'knowledge': {'status': 'unchanged', 'reason': '规范未变化'}}),
              repository='fixture-repo', **{'artifacts-dir': runtime_dir})
    cli('a', 'knowledge', 'index')  # docs/ joined the indexed source inventory
    work = claim('a', 'npc-recovery')
    assert work['state']['current_step'] == 'verify', work['state']['current_step']
    observed = operation('a', 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
    scenarios = [('机会响应 · 接受', '恢复后继续原操作'), ('机会响应 · 拒绝', '不重复产生响应与业务效果'), ('机会响应 · 来源取消', '拒绝继续或抑制新结果'),
                 ('动态评论 · 正常恢复', '沿用已保存响应继续处理'), ('动态评论 · 来源取消', '保留响应记录，不产生新效果')]
    (runtime_dir / 'report.json').write_text(json.dumps({'candidate': observed['candidate_id'], 'check': 'scenario', 'definition_revision': work['state']['definition_revision'],
        'plan_revision': work['state']['plan_revision'], 'method': 'command', 'source': 'external-command:fixture-a', 'covers': npc_accept,
        'observations': '五个受控恢复场景的验收记录；每个场景在两个独立进程中执行并核对业务去重。',
        'scenarios': [{'scenario': a, 'focus': b, 'result': 'pass'} for a, b in scenarios],
        'limitations': ['使用隔离数据库和受控 HTTP，没有调用真实模型', '不覆盖绕过清理逻辑的强制终止']}, ensure_ascii=False, indent=2))
    log_lines = ['$ go test ./internal/npc -run TestCheckpointAcrossProcesses -count=1 -v']
    for index, (name, _) in enumerate(scenarios):
        log_lines.append('=== RUN   TestCheckpointAcrossProcesses/' + name)
        log_lines.extend('17:36:%02d  INFO  fixture.step  scenario=%d step=%d' % (step % 60, index, step) for step in range(24))
        log_lines.append('17:37:00  INFO  checkpoint.saved  scenario=%d' % index)
        log_lines.append('--- PASS: TestCheckpointAcrossProcesses/' + name)
    (runtime_dir / 'scenarios.log').write_text('\n'.join(log_lines) + '\nPASS\n')
    (runtime_dir / 'regression.log').write_text('ok  \tfixture/internal/npc\t0.412s\nok  \tfixture/internal/reply\t0.210s\n')
    (runtime_dir / 'baseline.txt').write_text('验收基线：fixture 仓库当前候选；隔离数据库；受控 HTTP。\n')
    (runtime_dir / 'vet.log').write_text('$ go vet ./internal/npc\n')
    run(command, root)
    operation('a', 'submit', dict(work['lease'], expected_revision=work['revision'], result={'status': 'pass', 'summary': '五个受控恢复场景通过，恢复与重放没有重复产生业务结果',
              'artifacts': ['runtime:report.json', 'internal/npc/recovery_test.go', 'docs/npc-scope.md', 'runtime:scenarios.log', 'runtime:regression.log', 'runtime:baseline.txt', 'runtime:vet.log']},
              method='command', exit_code=0, inputs=['app.txt']), repository='fixture-repo', **{'artifacts-dir': runtime_dir})
    assert cli('a', 'task', 'show', '--task', 'npc-recovery')['next_action'] == 'completed'

    # Report goal: the report is delivered while its check records a failure.
    create('menu-audit', '菜单焦点验收报告', 'improvement', goal='report', acceptance=['菜单关闭后焦点回到触发按钮'], request='检查菜单焦点行为并交付验收报告。',
           checks=[{'id': 'focus', 'kind': 'functional', 'method': 'command', 'command': command, 'covers': ['菜单关闭后焦点回到触发按钮'], 'required': True}])
    execute('a', 'menu-audit', '已按验收清单检查菜单焦点行为')
    work = claim('a', 'menu-audit')
    observed = operation('a', 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
    report = private('report-menu-audit', {'candidate': observed['candidate_id'], 'check': 'focus', 'definition_revision': work['state']['definition_revision'],
                                           'plan_revision': work['state']['plan_revision'], 'method': 'command', 'source': 'external-command:fixture-a', 'covers': ['菜单关闭后焦点回到触发按钮'],
                                           'observations': '按 Esc 关闭菜单后，焦点落在页面顶部而不是触发按钮。'})
    submit(work, [(report, 'runtime:report.json')], status='fail', summary='菜单关闭后焦点没有回到触发按钮', exit_code=1, method='command', inputs=['app.txt'])
    manifest['report_task_state'] = cli('a', 'task', 'show', '--task', 'menu-audit')['next_action']
    # Analysis goal: only define and analysis steps deliver a conclusion.
    create('retry-analysis', '媒体失败重试策略分析', 'research', goal='analysis', checks=[{'id': 'review', 'kind': 'review', 'method': 'command', 'command': command, 'covers': ['给出失败分类与建议'], 'required': True}], acceptance=['给出失败分类与建议'], request='分析媒体调用失败的分类，给出重试建议。', scope=['docs/retry-analysis.md'])
    work = to_execute('a', 'retry-analysis')
    (out / 'runtime-analysis').mkdir()
    (projects['a'] / 'docs').mkdir(exist_ok=True)
    (projects['a'] / 'docs/retry-analysis.md').write_text('# 媒体失败重试策略分析\n\n## 结论\n\n失败分为超时、限流、内容拒绝三类；仅对前两类按退避重试。\n\n## 待确认\n\n- 限流阈值需要服务方确认\n')
    operation('a', 'submit', dict(work['lease'], expected_revision=work['revision'], result={'status': 'pass', 'summary': '失败分为超时、限流、内容拒绝三类；建议仅对前两类按退避重试',
              'artifacts': ['docs/retry-analysis.md'], 'knowledge': {'status': 'unchanged', 'reason': '规范未变化'}}), repository='fixture-repo', **{'artifacts-dir': out / 'runtime-analysis'})
    cli('a', 'knowledge', 'index')
    work = claim('a', 'retry-analysis')
    observed = operation('a', 'observe', dict(work['lease'], expected_revision=work['revision']), repository='fixture-repo')
    report = private('report-retry-analysis', {'candidate': observed['candidate_id'], 'check': 'review', 'definition_revision': work['state']['definition_revision'],
                                               'plan_revision': work['state']['plan_revision'], 'method': 'command', 'source': 'external-command:fixture-a', 'covers': ['给出失败分类与建议'],
                                               'observations': '分析文档包含三类失败与对应建议。'})
    submit(work, [(report, 'runtime:report.json')], summary='分析结论已核对')
    manifest['analysis_task_state'] = cli('a', 'task', 'show', '--task', 'retry-analysis')['next_action']

    # Another project, so switching can be checked for leaks.
    api('POST', '/api/v1/projects/other/tasks', {'definition': {'id': 'other-only', 'title': '另一个项目的唯一任务', 'type': 'bug', 'goal': 'verified', 'acceptance': ['仅属于另一个项目']},
                                                 'expected_revision': 'absent', 'operation_id': 'other-create', 'idempotency_key': 'other-create'})
    api('POST', '/api/v1/projects/other/members', {'username': 'reviewer', 'role': 'reader'})

    # Legacy-only relation injection, confined to this disposable database.
    stop()
    with sqlite3.connect(project_db('shared')) as connection:
        for task, _ in children:
            connection.execute("UPDATE tasks SET body=json_set(body,'$.definition.parent','plugin-parent') WHERE id=?", (task,))
        # Same restore gate an authority history import writes (see
        # internal/core/authority_archive.go), without a real import record.
        body = json.loads(connection.execute("SELECT body FROM tasks WHERE id='legacy-trellis'").fetchone()[0])
        body['paused'] = True
        body['confirmation'] = {'kind': 'restore', 'reason': 'historical import requires current context and fresh execution',
                                'definition_revision': body['definition']['definition_revision'], 'plan_revision': body['plan']['revision']}
        connection.execute("UPDATE tasks SET body=? WHERE id='legacy-trellis'", (json.dumps(body, ensure_ascii=False),))
        # A damaged original: metadata stays, stored digest no longer matches.
        connection.execute("UPDATE artifact_objects SET sha256=? WHERE id=?", ('0' * 64, broken_image))
    base = start(base.split('//', 1)[1])
    manifest['url'] = base
    manifest['tasks'] = {'parent': 'plugin-parent', 'children': [c for c, _ in children], 'schedule_image': schedule_image,
                         'account_before': before, 'account_after': after, 'broken_image': broken_image}
    (out / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2))
    print(json.dumps({'ready': True, 'url': base, 'manifest': str(out / 'manifest.json')}, ensure_ascii=False), flush=True)
    if args.browser_test:
        browser_env = dict(os.environ, AEL_WB_FIXTURE=str(out / 'manifest.json'), AEL_UI_OUT=str(out / 'browser'))
        if args.ui_url:
            browser_env['AEL_WB_URL'] = args.ui_url
        result = subprocess.run([str(repo / 'web/node_modules/.bin/playwright'), 'test', '--config=playwright.workbench-demo.config.ts'], cwd=repo / 'web', env=browser_env)
        raise SystemExit(result.returncode)
    if not args.no_wait:
        signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
        process.wait()
finally:
    stop()
    log.close()

#!/usr/bin/env python3
"""Queue-ownership chain through the real CLI against a disposable Server.

Two clients use separate executor accounts, profiles and runners: a simulated
cloud Bot ("Bot A", runners bot-1/bot-2) and a local Codex (runner codex-1).
The Bot here is this script calling the real `ael runner queue*` / `runner claim`
commands; it is not the external Bot product. No real project data is touched.

Checks: queue registration is visible to the other client without creating an
Attempt; transfer makes the old Bot queue entry unclaimable; the holder's claim
converts the marker; release/transfer replays are idempotent; an unreachable
Server leaves a pending operation (never a confirmed queue) that `runner retry`
completes later. With --keep the Server stays up for browser checks.
"""
import argparse
import itertools
import re
import json
import os
import select
import subprocess
import sys
import urllib.request
import http.cookiejar
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', required=True)
parser.add_argument('--out', required=True, help='new empty directory')
parser.add_argument('--keep', action='store_true', help='keep the Server running after the checks')
args = parser.parse_args()
binary = Path(args.binary).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('output directory must be empty')
out.mkdir(parents=True, mode=0o700, exist_ok=True)
env = {k: v for k, v in os.environ.items() if not k.startswith(('GIT_', 'AEL_'))}
env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0',
           AEL_DATA_DIR=str(out / 'unused-runtime'), AEL_CLIENT_CONFIG_DIR=str(out / 'machine-clients'),
           AEL_INSTALL_PREFIX=str(out / 'unused-install'))
serial = itertools.count(1)
log = (out / 'server.log').open('w')
process = None
results = []


def check(name, condition, detail=''):
    results.append({'check': name, 'ok': bool(condition), 'detail': detail})
    print(('PASS ' if condition else 'FAIL ') + name + ((' · ' + str(detail)[:300]) if not condition else ''), flush=True)
    if not condition:
        raise SystemExit(1)


def raw(argv, cwd=out, input_text=None):
    return subprocess.run(list(map(str, argv)), cwd=cwd, env=env, input=input_text, capture_output=True, text=True, timeout=120)


def run(argv, cwd=out, input_text=None):
    result = raw(argv, cwd, input_text)
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


try:
    run([binary, 'server', 'bootstrap', '--data-dir', out / 'server-data', '--username', 'owner', '--password-stdin', '--json'],
        input_text='fixture-admin-password\n')
    base = start('127.0.0.1:0')
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
    api('POST', '/api/v1/projects', {'id': 'shared', 'name': '排队协作示例'})
    projects, configs = {}, {}
    for name in ('bot', 'codex'):
        path = out / ('checkout-' + name)
        path.mkdir()
        projects[name], configs[name] = path, out / ('config-' + name)
        for command in (['init', '-b', 'main'], ['config', 'user.name', 'Fixture'], ['config', 'user.email', 'fixture@example.invalid']):
            run(['git', *command], path)
        (path / 'app.txt').write_text('original\n')
        (path / '.gitignore').write_text('.ael/\n')
        run(['git', 'add', '.'], path)
        run(['git', 'commit', '-m', 'fixture'], path)
        if name != 'bot':
            run(['git', 'fetch', str(projects['bot']), 'main'], path)
            run(['git', 'reset', '--soft', 'FETCH_HEAD'], path)
        api('POST', '/api/v1/users', {'username': name, 'password': 'fixture-' + name + '-password'})
        api('POST', '/api/v1/projects/shared/members', {'username': name, 'role': 'executor'})
        token = api('POST', '/api/v1/tokens', {'username': name, 'project_id': 'shared', 'label': 'fixture ' + name})['token']
        run([binary, 'server', 'profile', 'add', '--name', 'central', '--url', base, '--token-stdin', '--project', path,
             '--config-dir', configs[name], '--json'], input_text=token + '\n')
        run([binary, 'project', 'connect', '--profile', 'central', '--remote-project', 'shared', '--project', path,
             '--config-dir', configs[name], '--json'])

    def cli(name, *argv):
        return run([binary, *argv, '--project', projects[name], '--config-dir', configs[name], '--json'], projects[name])

    def cli_fail(name, *argv):
        result = raw([binary, *argv, '--project', projects[name], '--config-dir', configs[name], '--json'], projects[name])
        return result.returncode, result.stdout + result.stderr

    cli('bot', 'repository', 'register', '--repository', 'fixture-repo', '--operation-id', 'repo', '--idempotency-key', 'repo')
    runners = {'bot-1': ('bot', 'Bot A'), 'bot-2': ('bot', 'Bot A'), 'codex-1': ('codex', 'Codex 本地')}
    for runner, (name, alias) in runners.items():
        file = private('runner-' + runner, {'id': runner, 'workspace_id': 'ws-' + runner, 'checkout_id': 'co-' + runner, 'repository': 'fixture-repo',
                                            'version': 'fixture', 'alias': alias, 'capabilities': ['development'], 'expected_revision': 'absent'})
        cli(name, 'runner', 'register', '--file', file, '--operation-id', 'reg-' + runner, '--idempotency-key', 'reg-' + runner)
    for name in ('bot', 'codex'):
        cli(name, 'knowledge', 'index')

    def create(task, title, deps=()):
        definition = {'id': task, 'title': title, 'type': 'bug', 'goal': 'verified', 'scope': ['app.txt'], 'acceptance': ['结果符合描述'], 'dependencies': list(deps)}
        return cli('bot', 'task', 'create', '--file', private('definition-' + task, definition), '--operation-id', 'create-' + task, '--idempotency-key', 'create-' + task)

    def claim(name, runner, task):
        state = cli(name, 'task', 'show', '--task', task)
        revision = state['revision']
        key = 'ctx-' + str(next(serial))
        observed = cli(name, 'candidate', 'capture', '--task', task, '--repository', 'fixture-repo', '--checkout', 'co-' + runner,
                       '--workspace', 'ws-' + runner, '--runner', runner, '--expected-revision', revision, '--operation-id', key, '--idempotency-key', key)
        context = cli(name, 'context', 'publish', '--task', task, '--observation', observed['id'], '--expected-revision', revision,
                      '--operation-id', key + '-pub', '--idempotency-key', key + '-pub')
        request = private('claim-' + key, {'task_id': task, 'context_id': context['id'], 'expected_revision': revision, 'operation_id': key + '-claim', 'idempotency_key': key + '-claim'})
        lease = out / 'files' / ('lease-' + key + '.json')
        return cli_fail(name, 'runner', 'claim', '--runner', runner, '--file', request, '--out', lease)

    def queue_list(name, *flags):
        return {item['task_id']: item for item in cli(name, 'runner', 'queue-list', *flags)['items']}

    for i in range(1, 7):
        create(f'q{i}', f'排队任务 {i}')
    create('free', '未安排的任务')
    create('waits', '依赖前置的任务', deps=['q1'])
    create('paused', '暂停中的任务')
    paused = cli('bot', 'task', 'show', '--task', 'paused')
    cli('bot', 'task', 'pause', '--task', 'paused', '--reason', '等待测试环境', '--expected-revision', paused['revision'], '--operation-id', 'pause', '--idempotency-key', 'pause')

    # 1. The Bot queues 5 tasks; Codex sees the same server record.
    before = {f'q{i}': cli('codex', 'task', 'show', '--task', f'q{i}')['revision'] for i in range(1, 6)}
    versions = {}
    for i in range(1, 6):
        receipt = cli('bot', 'runner', 'queue', '--runner', 'bot-1', '--task', f'q{i}', '--operation-id', f'queue-q{i}', '--idempotency-key', f'queue-q{i}')
        versions[f'q{i}'] = receipt['queue']['version']
    seen = queue_list('codex', '--runner', 'codex-1')
    check('Codex 看到 Bot A 的 5 项排队', all(seen[f'q{i}']['assignment'] == 'queued' and seen[f'q{i}']['queue']['runner_label'] == 'Bot A' for i in range(1, 6)), seen)
    shown = {k: cli('codex', 'task', 'show', '--task', k) for k in before}
    check('排队不创建 Attempt、不改变任务修订', all(not s['task'].get('attempts') and s['revision'] == before[k] for k, s in shown.items()))
    claimable = queue_list('codex', '--runner', 'codex-1', '--state', 'claimable')
    check('可接手只含未安排且满足条件的任务', set(claimable) == {'free', 'q6'}, sorted(claimable))
    code, output = cli_fail('bot', 'runner', 'queue', '--runner', 'bot-1', '--task', 'paused', '--operation-id', 'queue-paused', '--idempotency-key', 'queue-paused')
    check('暂停的任务不能登记排队', code != 0 and 'NOT_RUNNABLE' in output, output)

    # 2. Codex cannot bypass the queue; the holder's claim converts it.
    code, output = claim('codex', 'codex-1', 'q2')
    check('Codex 普通领取被 Bot 排队拒绝', code != 0 and 'QUEUE_RESERVED' in output, output)
    code, output = claim('bot', 'bot-1', 'q1')
    check('Bot 领取自己的排队任务', code == 0, output)
    after = queue_list('codex', '--task', 'q1')['q1']
    check('领取后排队标记转为实际责任', after['assignment'] == 'started' and after['queue'] is None, after)

    # 3. Transfer without the Bot online; the stale Bot entry is refused.
    flags = ['--holder', 'bot-1', '--task', 'q2', '--expected-version', versions['q2'], '--to', 'codex-1', '--reason', '本地有空闲', '--operation-id', 'move-q2', '--idempotency-key', 'move-q2']
    first = cli('bot', 'runner', 'queue-transfer', *flags)
    replay = cli('bot', 'runner', 'queue-transfer', *flags)
    check('转交重放返回同一回执', first['queue']['version'] == replay['queue']['version'] and first['queue']['runner'] == 'codex-1', replay)
    code, output = claim('bot', 'bot-2', 'q2')
    check('Bot 用旧队列信息领取被拒绝', code != 0 and 'QUEUE_RESERVED' in output, output)
    code, output = claim('codex', 'codex-1', 'q2')
    check('Codex 合法领取转交的任务', code == 0, output)
    code, output = cli_fail('codex', 'runner', 'queue-transfer', '--holder', 'codex-1', '--task', 'q2', '--expected-version', first['queue']['version'], '--to', 'bot-2', '--reason', '退回', '--operation-id', 'back-q2', '--idempotency-key', 'back-q2')
    check('已开始的任务不能经转交绕过交接', code != 0 and 'QUEUE_STARTED' in output, output)

    # 4. Release is idempotent and keeps the task.
    flags = ['--holder', 'bot-1', '--task', 'q3', '--expected-version', versions['q3'], '--reason', 'Bot 移出队列', '--operation-id', 'drop-q3', '--idempotency-key', 'drop-q3']
    cli('bot', 'runner', 'queue-release', *flags)
    cli('bot', 'runner', 'queue-release', *flags)
    released = queue_list('codex', '--task', 'q3')['q3']
    check('释放后任务保留且为未安排', released['assignment'] == 'unassigned' and released['claimable'], released)

    # 5. Cancel clears an old record.
    q4 = cli('bot', 'task', 'show', '--task', 'q4')
    cli('bot', 'task', 'cancel', '--task', 'q4', '--reason', '不再需要', '--expected-revision', q4['revision'], '--operation-id', 'cancel-q4', '--idempotency-key', 'cancel-q4')
    code, output = cli_fail('bot', 'runner', 'queue-release', '--holder', 'bot-1', '--task', 'q4', '--expected-version', versions['q4'], '--operation-id', 'drop-q4', '--idempotency-key', 'drop-q4')
    check('取消后旧安排不可再用', code != 0 and 'QUEUE_VERSION_CONFLICT' in output, output)

    # 6. Unreachable Server: the intent stays pending, never shown as queued.
    address = base.split('://', 1)[1]
    stop()
    code, output = cli_fail('bot', 'runner', 'queue', '--runner', 'bot-1', '--task', 'q6', '--operation-id', 'queue-q6', '--idempotency-key', 'queue-q6')
    check('服务不可达时返回待确认操作', code != 0 and 'AGENT_OPERATION_PENDING' in output, output)
    pending = re.search(r'runner retry --pending (/[^\s"]+?\.json)', output).group(1)
    base = start(address)
    check('服务恢复后未确认的意向不显示为已安排', queue_list('codex', '--task', 'q6')['q6']['assignment'] == 'unassigned')
    retried = cli('bot', 'runner', 'retry', '--pending', pending)
    check('runner retry 以原幂等键完成登记', retried['queue']['runner'] == 'bot-1')
    again = cli('bot', 'runner', 'retry', '--pending', pending)
    check('重复 retry 不生成第二份安排', again['queue']['version'] == retried['queue']['version'])
    check('另一客户端看到确认后的安排', queue_list('codex', '--task', 'q6')['q6']['assignment'] == 'queued')

    manifest = {'url': base, 'out': str(out), 'admin': {'username': 'owner', 'password': 'fixture-admin-password'}, 'project': 'shared',
                'queued': ['q5', 'q6'], 'started': ['q1', 'q2'], 'unassigned': ['free', 'q3'], 'waits': 'waits', 'paused': 'paused', 'checks': results}
    (out / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2))
    print(json.dumps({'passed': len(results), 'url': base}, ensure_ascii=False), flush=True)
    if args.keep:
        print('Server running; Ctrl-C to stop', flush=True)
        process.wait()
finally:
    stop()

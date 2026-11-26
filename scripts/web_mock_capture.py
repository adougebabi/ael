#!/usr/bin/env python3
"""Capture real local-mode API responses for the web mock-scenario mode.

Seeds a disposable local project through the real CLI (create / claim /
submit / verify), starts the real local dashboard and saves the JSON the
task-detail page reads into web/src/mock/fixtures/. The browser mock mode
(npm run dev:mock) serves only these captured responses, so its data keeps
the authority's actual shape instead of a hand-written imitation.

Nothing here touches a real project: every path lives under --out, which
must be new or empty. Re-run after changing task/snapshot APIs:

  python3 scripts/web_mock_capture.py --binary PATH_TO_DEV_AEL \
      --bundle-binary dist/ael-linux-arm64 --out ~/.cache/ael-mock/run1
"""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import time
import urllib.parse
import urllib.request

ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument('--binary', required=True, help='ael built from this checkout (go build ./cmd/ael)')
ap.add_argument('--bundle-binary', required=True, help='release binary matching dist/ for `init --bundle-dir`')
ap.add_argument('--bundle', default='dist')
ap.add_argument('--out', required=True, help='new empty directory for the disposable project')
ap.add_argument('--fixtures', default='web/src/mock/fixtures')
args = ap.parse_args()

repo = Path(__file__).resolve().parent.parent
binary = Path(args.binary).expanduser().resolve()
out = Path(args.out).expanduser().resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('Refusing to reuse a nonempty directory; choose a fresh --out')
root = out / 'project'
root.mkdir(parents=True)
env = dict(os.environ, AEL_DATA_DIR=str(out / 'runtime'))
fixtures = (repo / args.fixtures).resolve()
fixtures.mkdir(parents=True, exist_ok=True)

subprocess.run([str(Path(args.bundle_binary).resolve()), 'init', '--project', str(root),
                '--bundle-dir', str((repo / args.bundle).resolve()), '--json'],
               env=env, check=True, capture_output=True, timeout=120)


def cli(*argv, ok=True):
    p = subprocess.run([str(binary), *map(str, argv), '--project', str(root), '--json'],
                       env=env, text=True, capture_output=True, timeout=120)
    if ok and p.returncode:
        raise RuntimeError(' '.join(map(str, argv)) + ': ' + (p.stderr or p.stdout))
    return json.loads(p.stdout) if p.stdout.strip() else {}


def put(name, value):
    p = out / 'protocol' / (name + '.json')
    p.parent.mkdir(exist_ok=True)
    p.write_text(json.dumps(value, ensure_ascii=False))
    return p


def create(id, title, request, check_command, acceptance=None, **extra):
    acceptance = acceptance or ['生成的周报汇总文件存在且内容非空']
    d = dict(id=id, title=title, type='feature', goal='verified', scope=['reports'],
             original_request=request, acceptance=acceptance,
             checks=[dict(id='report-file', kind='functional', method='command', command=check_command,
                          covers=acceptance[:1], required=True)])
    d.update(extra)
    return cli('task', 'create', '--file', put(id + '-definition', d))


def step(id, summary, status='pass', outputs=(), write=None, failure_class=''):
    a = cli('claim', '--task', id, '--executor', 'mock-capture-agent')['attempt']
    cli('context', '--task', id)
    if write:
        for rel, body in write.items():
            (root / rel).parent.mkdir(parents=True, exist_ok=True)
            (root / rel).write_text(body)
    result = dict(status=status, summary=summary, artifacts=list(outputs))
    if a['unit'] == 'execute' and status == 'pass':
        result['knowledge'] = dict(status='unchanged', reason='沿用现有项目规范。', artifacts=[])
    if failure_class:
        result['failure_class'] = failure_class
    if status == 'blocked':
        result['blocker'] = dict(category='environment', reason=summary, scope=['reports'],
                                 tried='见本次工作结果', recovery='恢复报表数据源访问后继续', resume_at=a['unit'])
    return cli('submit', '--file', put(a['id'], dict(task=id, attempt=a['id'], token=a['token'],
                                                    key=a['id'], result=result)))


def prepare(id, notes):
    """Pass the define/analysis steps the plan puts before execute."""
    for note in notes:
        step(id, note)
    while cli('task', 'show', '--task', id)['current_step'] != 'execute':
        step(id, '方案确认：沿用既有周报生成流程。')


LONG = ('Traceback (most recent call last):\n' + ''.join(
    f'  File "/srv/reports/pipeline/stage_{i:02d}.py", line {40 + i}, in transform_{i}\n'
    f'    rows = normalise(rows, schema=SCHEMA_V{i % 4}, strict=True)  # 周报字段校验\n'
    for i in range(60)) + 'ValueError: column "region_code" contains 1,284 values outside the allowed set; '
        'first offending row: {"week":"2026-W47","region_code":"CN-ZZ-UNKNOWN","amount":"12,880.00"}\n')

# 1 queued: created, nobody has claimed it.
create('mock-queued', '整理本周销售周报', '把各区域本周销售数据汇总成一份周报，交给运营复核。',
       ['sh', '-c', 'test -s reports/weekly.md'])
# 2 running: define passed, execute claimed and still leased.
create('mock-running', '生成区域销售对比图', '在周报中加入区域销售对比图。', ['sh', '-c', 'test -s reports/chart.svg'])
prepare('mock-running', ['确认图表口径：按区域汇总本周销售额，与上周对比。'])
running_attempt = cli('claim', '--task', 'mock-running', '--executor', 'mock-capture-agent')['attempt']
cli('context', '--task', 'mock-running')
# 3 delivered: every step passed and the required check passed.
create('mock-delivered', '导出月度客户名单', '导出本月新增客户名单，供客服回访。', ['sh', '-c', 'test -s reports/customers.csv'])
prepare('mock-delivered', ['确认导出字段：客户名、联系人、开户日期。'])
step('mock-delivered', '导出 128 位新增客户到 reports/customers.csv。', outputs=['reports/customers.csv'],
     write={'reports/customers.csv': 'name,contact,opened\n华东贸易,王敏,2026-11-03\n'})
cli('verify', '--task', 'mock-delivered', '--check', 'report-file')
# 4/6 failed: execute failed once, then passed; the required check fails.
create('mock-failed', '修复周报金额汇总', '周报里的区域金额合计与财务系统对不上，需要修复。',
       ['sh', '-c', 'echo "合计 12,880.00 与财务系统 13,020.00 不一致（差额 140.00）" >&2; exit 1'])
prepare('mock-failed', ['定位到汇总脚本对退款记录重复扣减。'])
step('mock-failed', '首次修复未通过本地对账：退款记录仍被扣减两次。', status='fail', failure_class='reconcile:mismatch')
step('mock-failed', '调整退款扣减逻辑并重新生成周报。', outputs=['reports/weekly.md'],
     write={'reports/weekly.md': '# 周报\n合计 12,880.00\n'})
cli('verify', '--task', 'mock-failed', '--check', 'report-file', ok=False)
# 7 execute passed but the task is still open: verification not run yet.
create('mock-awaiting-check', '补充周报环比说明', '在周报末尾补充环比变化说明。', ['sh', '-c', 'test -s reports/notes.md'])
prepare('mock-awaiting-check', ['确认说明范围：只写环比变化最大的三个区域。'])
step('mock-awaiting-check', '已写入环比说明到 reports/notes.md。', outputs=['reports/notes.md'],
     write={'reports/notes.md': '华东 +12%，华南 -4%，西南 +9%\n'})
# 8 long content: long request, long failure output, long summaries.
create('mock-long', '排查周报数据管线校验错误', '运营反馈：' + '周报生成任务在数据校验阶段中断，需要排查并给出可复现的原因。' * 30,
       ['sh', '-c', 'printf "%s" "$0" >&2; exit 1', LONG])
prepare('mock-long', ['复现步骤与口径确认：' + '按 2026-W47 数据重跑管线并记录每一阶段的输入行数。' * 12])
step('mock-long', LONG, status='fail', failure_class='pipeline:schema')
step('mock-long', '放宽了地区编码的白名单读取方式，重新生成周报。', outputs=['reports/weekly.md'])
cli('verify', '--task', 'mock-long', '--check', 'report-file', ok=False)
# blocked: execution reports an environment blocker.
create('mock-blocked', '同步财务系统汇率', '每周同步一次财务系统汇率表。', ['sh', '-c', 'true'])
prepare('mock-blocked', ['确认汇率来源为财务系统导出接口。'])
step('mock-blocked', '财务系统导出接口返回 403，当前凭据无读取权限。', status='blocked')


def free_port():
    s = socket.socket()
    s.bind(('127.0.0.1', 0))
    port = s.getsockname()[1]
    s.close()
    return port


port = free_port()
server = subprocess.Popen([str(binary), 'dashboard', '--addr', f'127.0.0.1:{port}', '--project', str(root)],
                          env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
base = f'http://127.0.0.1:{port}'
try:
    for _ in range(100):
        try:
            urllib.request.urlopen(base + '/api/session', timeout=1)
            break
        except Exception:
            time.sleep(0.2)
    token = json.load(urllib.request.urlopen(base + '/api/session'))['token']

    def get(path):
        req = urllib.request.Request(base + path, headers={'Authorization': 'Bearer ' + token})
        with urllib.request.urlopen(req, timeout=30) as r:
            body = r.read().decode()
            return json.loads(body) if 'json' in r.headers.get('Content-Type', '') else body

    def scrub(value):
        # Disposable absolute paths are replaced so fixtures do not record the
        # capturing machine; the shape stays exactly what the Server returned.
        text = json.dumps(value, ensure_ascii=False).replace(str(out), '/mock').replace(str(binary), '/mock/bin/ael')
        return json.loads(text)

    def snapshot(id):
        return {'task': scrub(get('/api/task?id=' + id)), 'events': scrub(get('/api/events?id=' + id))}

    data = {
        'info': scrub(get('/api/info')),
        'attention': scrub(get('/api/attention?human=1')),
        'workbench': scrub(get('/api/tasks?view=workbench&limit=20&days=0')),
        'recent': scrub(get('/api/tasks?compact=1&board=completed&days=0&limit=3')),
        'children': scrub(get('/api/tasks?view=workbench&parent=mock-queued&limit=8')),
        'task': {},
    }
    ids = ['mock-queued', 'mock-running', 'mock-delivered', 'mock-failed', 'mock-awaiting-check', 'mock-long', 'mock-blocked']
    for id in ids:
        data['task'][id] = {
            'states': [snapshot(id)],
            'knowledge': scrub(get('/api/task-knowledge?id=' + id + '&compact=1')),
            'relations': scrub(get('/api/task-relations?id=' + id)),
        }
    # Second real states for the scenarios whose page must change after an
    # action: the running execution submits, and the awaiting task is checked.
    # The check runs first: both tasks share one workspace candidate.
    cli('verify', '--task', 'mock-awaiting-check', '--check', 'report-file')
    data['task']['mock-awaiting-check']['states'].append(snapshot('mock-awaiting-check'))
    (root / 'reports').mkdir(exist_ok=True)
    (root / 'reports/chart.svg').write_text('<svg xmlns="http://www.w3.org/2000/svg"/>')
    cli('submit', '--file', put('running-submit', dict(
        task='mock-running', attempt=running_attempt['id'], token=running_attempt['token'], key=running_attempt['id'],
        result=dict(status='pass', summary='已生成区域对比图 reports/chart.svg，并写入周报。', artifacts=['reports/chart.svg'],
                    knowledge=dict(status='unchanged', reason='沿用现有项目规范。', artifacts=[])))))
    data['task']['mock-running']['states'].append(snapshot('mock-running'))
    # Every output an attempt or check record references, read through the
    # same artifact endpoint the page uses.
    data['artifacts'] = {}
    for id, v in data['task'].items():
        t = v['states'][-1]['task']['task']
        refs = {ref for a in t.get('attempts') or [] for ref in a.get('outputs') or []}
        refs |= {ref for e in (t.get('evidence') or []) + (t.get('evidence_history') or []) for ref in e.get('artifacts') or []}
        for ref in sorted(refs):
            try:
                data['artifacts'][id + ':' + ref] = scrub(get('/api/artifact?task=' + id + '&ref=' + urllib.parse.quote(ref)))
            except Exception as e:  # A purged or unreadable output stays visible as missing.
                print('artifact unreadable', id, ref, e)
    data['captured_at'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    data['captured'] = {'binary_version': cli('version'), 'note': 'Captured by scripts/web_mock_capture.py'}
    (fixtures / 'captured.json').write_text(json.dumps(data, ensure_ascii=False, indent=1) + '\n')
    print('wrote', fixtures / 'captured.json')
finally:
    server.terminate()
    server.wait(10)

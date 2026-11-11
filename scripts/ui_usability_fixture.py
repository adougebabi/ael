#!/usr/bin/env python3
"""Disposable UI acceptance fixtures; never discovers or writes business projects.

Every source and outcome below is explicitly a deterministic test fixture, not
an Agent run or a claim about the user's engineering history.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess

ap = argparse.ArgumentParser()
ap.add_argument('--bundle', default='dist')
ap.add_argument('--out', required=True)
ap.add_argument('--seed-binary')
args = ap.parse_args()
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit('Refusing to reuse a nonempty fixture directory; choose a fresh --out')
out.mkdir(parents=True, exist_ok=True)
(out / 'ISOLATED-UI-FIXTURE').write_text('Disposable AEL usability test projects. No real business data.\n')
native = Path(args.seed_binary).resolve() if args.seed_binary else bundle / 'ael-darwin-arm64'
native_version = json.loads(subprocess.check_output([str(native), 'version', '--json'], text=True))
manifest = {'isolated': True, 'out': str(out), 'projects': {}}
manifest['version'] = native_version['version']
manifest['build'] = native_version['build']
commands = []


def write(root, rel, body):
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(body)
    return p


def run(project, *argv, ok=True):
    env = dict(os.environ, AEL_DATA_DIR=project['runtime'])
    binary = native if args.seed_binary else Path(project['root']) / 'ael'
    p = subprocess.run([str(binary), *map(str, argv), '--project', project['root'], '--json'],
                       text=True, capture_output=True, env=env, timeout=90)
    commands.append({'project': project['root'], 'args': list(map(str, argv)), 'code': p.returncode,
                     'error': p.stderr if p.returncode else ''})
    if ok and p.returncode:
        raise RuntimeError(p.stderr or p.stdout)
    return json.loads(p.stdout) if not p.returncode else {'error': p.stderr}


def init(name, port, legacy=False):
    root = out / name / 'project'
    root.mkdir(parents=True)
    project = {'root': str(root), 'runtime': str(out / name / 'runtime'), 'port': port,
               'url': f'http://127.0.0.1:{port}'}
    if legacy:
        write(root, '.trellis/spec/backend/rules.md', '# 隔离夹具规范\n写入必须支持幂等键。\n')
        write(root, '.trellis/research/old.md', '# 隔离夹具历史记录\n[未接入资料](not-present.md)\n')
    env = dict(os.environ, AEL_DATA_DIR=project['runtime'])
    p = subprocess.run([str(native), 'init', '--project', str(root), '--bundle-dir', str(bundle), '--json'],
                       env=env, capture_output=True, text=True, timeout=90)
    if p.returncode:
        raise RuntimeError(p.stderr)
    manifest['projects'][name] = project
    print('initialized', name, flush=True)
    return project, root


def put(project, name, value):
    p = out / 'protocol' / (name + '.json')
    p.parent.mkdir(exist_ok=True)
    p.write_text(json.dumps(value, ensure_ascii=False))
    return p


def create(project, id, title, **extra):
    d = dict(id=id, title='隔离夹具：' + title, type='bug', goal='verified', scope=['app'],
             original_request='隔离可用性验收；所有产物仅存在于临时目录，不涉及真实业务。',
             acceptance=['结果有可读取的真实证据'],
             checks=[dict(id='content', kind='functional', method='command', command=['sh', '-c', 'true'],
                          covers=['结果有可读取的真实证据'], required=True)])
    d.update(extra)
    return run(project, 'task', 'create', '--file', put(project, id+'-definition', d))


def work(project, id, *, change=None, status='pass', blocker=None, outputs=None):
    n = run(project, 'claim', '--task', id, '--executor', 'isolated-fixture-driver')
    a = n['attempt']
    run(project, 'context', '--task', id)
    if change:
        write(Path(project['root']), change, 'real isolated implementation output: '+id+'\n')
    result = dict(status=status, summary='隔离夹具实际工作：'+id,
                  artifacts=outputs or ([change] if change else []),
                  knowledge=dict(status='unchanged', reason='只使用现有隔离规范，不改真实业务资料。', artifacts=[]))
    if blocker:
        result['blocker'] = blocker
    return run(project, 'submit', '--file', put(project, a['id'],
               dict(task=id, attempt=a['id'], token=a['token'], key=a['id'], result=result)))


try:
    single, root = init('single', 7351)
    create(single, 'one-task', '只有一个任务也能读懂当前工作与下一步')
    empty, _ = init('empty', 7354)
    many, root = init('many', 7352)
    write(root, '.ael/knowledge/current.md', '''---
id: current-rule
title: 隔离夹具：当前交付规范
kind: spec
status: current
paths: [app]
tasks: [verified-change]
---
# 当前交付规范

## 当前约束
结果必须留下真实证据，不能把已决定当成已实现。

| 检查 | 要求 |
| --- | --- |
| 输出 | 可读取 |

```sh
printf 'evidence'
```

## 关联资料
[读取决策](choice.md) · [未接入资料](missing.md)
[越界链接](../../../../etc/passwd)

<script>window.fixtureXSS = true</script>
[恶意协议](javascript:alert(1))
''')
    write(root, '.ael/knowledge/choice.md', '''---
id: local-choice
title: 隔离夹具：本地结果与回归交付
kind: decision
status: accepted
summary: 增加两项本地结果输出及其回归验证，显式汇总两个交付任务。
rationale: 验收需要追溯每项实际产物与准确版本证据。
occurred_at: 2026-10-20T06:00:00Z
paths: [app]
tasks: [change-a, change-b]
---
# 本地交付决策

采用离线产物与真实命令检查。已决定不等于已发布。
''')
    write(root, '.ael/knowledge/rejected.md', '''---
id: rejected-choice
title: 隔离夹具：否决远程索引方案
kind: decision
status: rejected
rationale: 离线验收不能依赖远程服务。
---
# 已否决方案
原因是不可接受的远程依赖。原始发生日期没有记录。
''')
    write(root, '.ael/knowledge/conflict.md', '''---
id: uncertain-rule
title: 隔离夹具：待核实的局部规范
kind: spec
status: conflict
paths: [conflict-scope]
---
# 待核实规范
这里保留真实冲突，不能标记为现行有效。
''')
    write(root, '.ael/knowledge/old.md', '''---
id: old-rule
title: 隔离夹具：已被替代的输出规则
kind: spec
status: superseded
superseded_by: current-rule
---
# 历史输出规则
仅供追溯，已经有明确后继。
''')
    for i in range(1000):
        write(root, f'.ael/knowledge/archive/{i:04d}.md',
              f'---\nid: archive-{i:04d}\ntitle: 隔离夹具历史资料 {i:04d}\nkind: reference\nstatus: historical\npaths: [archive]\n---\n# 历史资料 {i:04d}\n\n这是分页负载夹具，不是项目成果。needle-{i:04d}\n'+('历史正文用于验证列表不传输全文。\n'*20))
    run(many, 'knowledge', 'index')
    for i in range(43):
        create(many, f'pending-{i:02d}', f'分页任务 {i:02d} · 保留标题、当前工作、执行者和下一动作')
    create(many, 'long-title', '较长标题验证：并行检查失败后保留原始证据、核对执行者交接并恢复下一步工作，确保在两个桌面尺寸与较窄屏幕中仍可完整阅读 '+('长标题内容 '*8))
    create(many, 'environment', '等待环境标记解除后恢复')
    work(many, 'environment', status='blocked', blocker=dict(category='environment',reason='隔离环境标记尚未准备好',scope=['app'],tried='检查临时环境中的标记文件，当前不存在',recovery='准备隔离环境标记并核实后恢复',resume_at='define'))
    create(many, 'decision-task', '需要准确修订确认的方案', preset='strict')
    work(many, 'decision-task'); work(many, 'decision-task')
    check = lambda id: dict(id=id,kind='functional',method='command',command=['sh','-c','true'],covers=['结果有可读取的真实证据'],required=True)
    create(many, 'parallel-checks', '两个检查执行中，另一个等待接手', checks=[check('check-a'),check('check-b'),check('check-c')])
    create(many, 'failed-check', '单项失败、另一项未运行', checks=[dict(check('failed'),command=['sh','-c','test -f allow-pass']),check('not-run')])
    work(many, 'failed-check');work(many, 'failed-check',change='app/failed.txt');run(many,'verify','--task','failed-check','--check','failed')
    for id in ['change-a','change-b','verified-change']:
        create(many,id,'真实隔离交付 '+id,knowledge=['current-rule'],checks=[dict(
            id='content',kind='functional',method='command',required=True,
            covers=['结果有可读取的真实证据'],
            command=['python3','-B','-c',f"from pathlib import Path; assert Path('app/{id}.txt').read_text().strip() == 'real isolated implementation output: {id}'"])])
        work(many,id);work(many,id,change='app/'+id+'.txt');run(many,'verify','--task',id,'--check','content')
    create(many,'implemented-only','已实现但尚未发布的本地输出')
    work(many,'implemented-only');work(many,'implemented-only',change='app/unreleased.txt')
    create(many,'cancelled-task','已取消的隔离工作');run(many,'task','cancel','--task','cancelled-task','--reason','隔离夹具取消记录，不伪造交付')
    print('seeded tasks and 1005 knowledge records', flush=True)
    # A real migration generates many observation/link warnings in an isolated
    # project; current blockers remain driven by Next, not guessed from paths.
    create(many,'current-block','当前规范冲突阻断的工作',scope=['current-block'])
    write(root,'.trellis/spec/current-block/rules.md','# 隔离夹具混合规范\nTODO 待确认：此局部规则尚未核实。\n')
    write(root,'.trellis/research/history.md','# 隔离夹具历史引用\n'+'\n'.join(f'[旧资料 {i}](missing-{i}.md)' for i in range(180)))
    report=out/'migration-input'
    run(many,'migrate','scan','--out',report)
    run(many,'migrate','plan','--inventory',report/'inventory.json','--out',report)
    plan=json.loads((report/'plan.json').read_text())
    migrated=run(many,'migrate','apply','--plan',report/'plan.json','--approved-digest',plan['digest'])
    many['warning_batch']=migrated['id']
    # Context/Sync above already created >1000 real knowledge_observed events.
    migration_project,_=init('migration',7353,legacy=True)
    work(many, 'parallel-checks');work(many, 'parallel-checks',change='app/parallel.txt')
    run(many, 'claim', '--task','parallel-checks','--unit','check-a','--executor','fixture-checker-A')
    run(many, 'claim', '--task','parallel-checks','--unit','check-b','--executor','fixture-checker-B')
    for project in manifest['projects'].values():
        project['data_dir']=run(project,'doctor')['data_dir']
    (out/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
    print(json.dumps(manifest,ensure_ascii=False,indent=2),flush=True)
finally:
    (out/'commands.json').write_text(json.dumps(commands,ensure_ascii=False,indent=2))

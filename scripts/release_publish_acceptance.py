#!/usr/bin/env python3
"""Exercise publication control flow with an offline gh executable fixture."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory(prefix='ael-publish-fixture-') as directory:
    root = Path(directory)
    fake = root / 'gh'
    fake.write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
path=Path(os.environ['GH_FIXTURE'])
state=json.loads(path.read_text())
args=sys.argv[1:]
state['calls'].append(args)
if args[0]=='api':
    if '/commits/' in args[1]: print(json.dumps({'sha':state['tag_commit']}))
    else: print(json.dumps(state['releases']))
else:
    state['mutations'].append(args)
    if args[1]=='upload' and state.get('fail_upload'):
        path.write_text(json.dumps(state));sys.exit(1)
    if args[1]=='create': state['releases']=[{'tag_name':args[2], 'draft':'--draft' in args}]
    if args[1]=='edit': state['releases'][0]['draft']='--draft=true' in args
path.write_text(json.dumps(state))
''')
    fake.chmod(0o755)
    commit = 'a' * 40
    version = '1.2.3-rc.1'
    content = b'fixture payload'
    (root / 'client.tar.gz').write_bytes(content)
    (root / 'manifest.sigstore.json').write_text('offline fixture signature, never trusted online')
    manifest = {'version': version, 'commit': commit, 'dirty': False, 'publication': 'draft', 'assets': [{'path': 'client.tar.gz', 'size': len(content), 'sha256': hashlib.sha256(content).hexdigest()}]}
    statefile = root / 'fixture.json'
    results = []
    def run(case, publication, releases, expected, **changes):
        manifest['publication'] = publication
        (root / 'manifest.json').write_text(json.dumps(manifest))
        state = {'tag_commit': commit, 'calls': [], 'mutations': [], 'releases': releases, **changes}
        statefile.write_text(json.dumps(state))
        env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'], GH_REPO='fixture/unused', GH_TOKEN='', SOURCE_COMMIT=commit, GH_FIXTURE=str(statefile))
        result = subprocess.run([sys.executable, str(repo / 'scripts/release_publish.py'), '--manifest', str(root / 'manifest.json'), '--publication', publication], env=env, text=True, capture_output=True)
        assert (result.returncode == 0) == expected, (case, result.stdout, result.stderr)
        state = json.loads(statefile.read_text())
        results.append({'case': case, 'passed': True, 'commands': state['calls']})
        return state
    state = run('create draft', 'draft', [], True)
    assert '--prerelease=true' in state['mutations'][0] and '--latest=false' in state['mutations'][0]
    draft = [{'tag_name': 'v' + version, 'draft': True}]
    state = run('promote rebuilt signed draft', 'released', draft, True)
    assert [call[1] for call in state['mutations']] == ['upload', 'edit'] and not state['releases'][0]['draft']
    state = run('published immutable', 'released', [{'tag_name': 'v' + version, 'draft': False}], False)
    assert state['mutations'] == []
    state = run('wrong target commit', 'released', [], False, tag_commit='b' * 40)
    assert state['mutations'] == []
    state = run('failed upload stays draft', 'released', draft, False, fail_upload=True)
    assert state['releases'][0]['draft'] and all(call[1] != 'edit' for call in state['mutations'])
    (root / 'client.tar.gz').write_bytes(b'changed')
    state = run('changed asset rejected', 'released', draft, False)
    assert state['calls'] == []
output = Path(args.out)
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps({'status': 'passed', 'offline_gh_fixture': True, 'real_release_access': False, 'cases': results}, indent=2) + '\n')
print(json.dumps({'status': 'passed', 'cases': len(results), 'real_release_access': False}))

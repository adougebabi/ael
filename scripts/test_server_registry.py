"""Exercise registry publication using explicitly offline Docker/Hub fixtures."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class ServerRegistryTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.state = self.root / 'state.json'
        self.state.write_text(json.dumps({'calls': []}))
        fake = self.root / 'fixture-tool'
        fake.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
path=Path(os.environ['REGISTRY_FIXTURE']);state=json.loads(path.read_text());name=Path(sys.argv[0]).name;args=sys.argv[1:]
state['calls'].append([name,*args]);path.write_text(json.dumps(state))
if name=='docker' and args[:2]==['image','inspect']:
 arch=args[-1].rsplit('-',1)[1]
 print(json.dumps([{'Architecture':arch,'Os':'linux','Config':{'Labels':{'org.opencontainers.image.version':'1.2.3','org.opencontainers.image.revision':'a'*40}}}]))
elif name=='docker' and args[:3]==['buildx','imagetools','inspect']:
 if '--raw' in args: print(json.dumps({'manifests':[{'platform':{'os':'linux','architecture':a}} for a in ['amd64','arm64']]}))
 else: print('Name: fixture\\nDigest: sha256:'+'c'*64)
''')
        fake.chmod(0o755)
        for name in ['docker']:
            (self.root / name).symlink_to(fake)
        assets = []
        for arch in ['amd64', 'arm64']:
            path = self.root / ('image-' + arch + '.tar.gz')
            data = ('offline image fixture ' + arch).encode()
            path.write_bytes(data)
            assets.append({'kind': 'server_image', 'platform': 'linux-' + arch, 'path': path.name,
                           'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
        self.manifest = self.root / 'manifest.json'
        self.manifest.write_text(json.dumps({'version': '1.2.3', 'commit': 'a' * 40, 'build': 'fixture',
                                             'dirty': False, 'publication': 'released', 'assets': assets}))
        self.environment = dict(os.environ, PATH=str(self.root) + os.pathsep + os.environ['PATH'],
                                GH_REPO='fixture/ael', REGISTRY_FIXTURE=str(self.state))
        self.script = Path(__file__).with_name('server_registry.py')

    def run_script(self, *arguments):
        return subprocess.run([sys.executable, str(self.script), '--image', 'docker.io/fixture/ael-server', *arguments],
                              env=self.environment, text=True, capture_output=True, timeout=30)

    def test_publishes_both_platforms_and_reports_exact_index(self):
        result = self.run_script('--manifest', str(self.manifest))
        self.assertEqual(result.returncode, 0, result.stderr)
        receipt = json.loads((self.root / 'server-registry.json').read_text())
        self.assertEqual(receipt['platforms'], ['linux/amd64', 'linux/arm64'])
        calls = json.loads(self.state.read_text())['calls']
        self.assertEqual(len([c for c in calls if c[:3] == ['docker', 'image', 'push']]), 2)
        self.assertEqual(receipt['reference'], 'docker.io/fixture/ael-server@sha256:' + 'c' * 64)

    def test_tampered_image_never_reaches_docker(self):
        (self.root / 'image-amd64.tar.gz').write_bytes(b'changed fixture bytes')
        result = self.run_script('--manifest', str(self.manifest))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(json.loads(self.state.read_text())['calls'], [])

    def test_main_build_pushes_main_without_replacing_version_tags(self):
        manifest = json.loads(self.manifest.read_text())
        manifest['publication'] = 'build_artifact'
        self.manifest.write_text(json.dumps(manifest))
        result = self.run_script('--manifest', str(self.manifest), '--main')
        self.assertEqual(result.returncode, 0, result.stderr)
        receipt = json.loads((self.root / 'server-registry.json').read_text())
        self.assertEqual(receipt['tag'], 'main')
        pushes = [c[-1] for c in json.loads(self.state.read_text())['calls'] if c[:3] == ['docker', 'image', 'push']]
        self.assertEqual(pushes, ['docker.io/fixture/ael-server:main-amd64', 'docker.io/fixture/ael-server:main-arm64'])


if __name__ == '__main__':
    unittest.main()

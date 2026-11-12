#!/usr/bin/env python3
"""Drive a packaged CLI against an actual signed loopback release source."""
import argparse
import copy
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import threading

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
bundle = Path(args.bundle).resolve()
release = bundle / 'release'
native = platform.system().lower() + '-' + {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine().lower()]
binary = bundle / ('ael-' + native)
verifier = shutil.which('ssh-keygen')
if not verifier:
    raise SystemExit('OpenSSH ssh-keygen is required for signed source acceptance')
base = json.loads((release / 'manifest.json').read_text())
base['dirty'], base['publication'] = False, 'released'
payloads = {asset['path']: (release / asset['path']).read_bytes() for asset in base['assets']}
catalog = []
content = {}
results = []

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *values):
        pass

    def do_GET(self):
        if self.path == '/releases':
            body = json.dumps(catalog).encode()
        else:
            body = content.get(self.path)
            if body is None:
                self.send_error(404)
                return
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

with tempfile.TemporaryDirectory(prefix='ael-release-source-') as directory:
    root = Path(directory)
    key = root / 'key'
    subprocess.run([verifier, '-q', '-t', 'ed25519', '-N', '', '-f', str(key)], check=True, capture_output=True)
    (root / 'allowed_signers').write_text('fixture-publisher ' + (root / 'key.pub').read_text())
    project = root / 'remote-project'
    (project / '.ael').mkdir(parents=True)
    (project / '.ael/remote.json').write_text('{"schema":1,"profile":"unavailable","project_id":"fixture"}')
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    origin = 'http://127.0.0.1:' + str(server.server_address[1])
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    source = {'releases_url': origin + '/releases', 'manifest_asset': 'manifest.json', 'signature_asset': 'manifest.sig', 'allowed_origins': [], 'trust': {'type': 'openssh', 'identity': 'fixture-publisher', 'allowed_signers': 'allowed_signers', 'verifier': verifier}}
    sourcefile = root / 'source.json'
    sourcefile.write_text(json.dumps(source))
    env = dict(os.environ, AEL_DATA_DIR=str(root / 'unused-runtime'), AEL_CONFIG_DIR=str(root / 'unused-config'))
    def run(case, *values, error=None):
        command = [str(binary), 'release', *values, '--source', str(sourcefile), '--project', str(project), '--json']
        result = subprocess.run(command, cwd=root, env=env, text=True, capture_output=True, timeout=90)
        if error:
            assert result.returncode != 0 and error in result.stderr, (case, result.stdout, result.stderr)
            value = {'rejected': error}
        else:
            assert result.returncode == 0, (case, result.stderr)
            value = json.loads(result.stdout)
        results.append({'case': case, 'result': value.get('status', value.get('rejected')), 'passed': True})
        return value
    def signed(manifest, namespace):
        path = root / (namespace + '.json')
        path.write_text(json.dumps(manifest))
        subprocess.run([verifier, '-Y', 'sign', '-f', str(key), '-n', 'ael-release', str(path)], check=True, capture_output=True)
        content['/' + namespace + '/manifest.json'] = path.read_bytes()
        content['/' + namespace + '/manifest.sig'] = Path(str(path) + '.sig').read_bytes()
        assets = [{'name': name, 'browser_download_url': origin + '/' + namespace + '/' + name, 'size': len(content['/' + namespace + '/' + name])} for name in ['manifest.json', 'manifest.sig']]
        for asset in manifest['assets']:
            content['/' + namespace + '/' + asset['path']] = payloads[asset['path']]
            assets.append({'name': asset['path'], 'browser_download_url': origin + '/' + namespace + '/' + asset['path'], 'size': asset['size']})
        return {'tag_name': 'v' + manifest['version'], 'draft': False, 'prerelease': '-' in manifest['version'].split('+')[0], 'assets': assets}
    try:
        value = run('empty catalog', 'check')
        assert not value['available'] and value['status'] == 'no_available_version'
        stable = signed(base, 'stable')
        pre = copy.deepcopy(base)
        pre['version'] = '99.0.0-rc.1'
        prerelease = signed(pre, 'prerelease')
        catalog[:] = [prerelease, stable]
        value = run('stable platform selection', 'check', '--platform', native)
        assert value['manifest']['version'] == base['version'] and value['asset']['platform'] == native
        value = run('explicit prerelease', 'check', '--channel', 'prerelease')
        assert value['manifest']['version'] == pre['version']
        output = root / 'client.tar.gz'
        value = run('signed real asset download', 'download', '--out', str(output))
        assert value['publisher_identity'] == 'verified' and value['content_integrity'] == 'verified'
        run('output collision', 'download', '--out', str(output), error='RELEASE_OUTPUT_UNAVAILABLE')
        run('missing platform', 'check', '--kind', 'server', '--platform', 'darwin-arm64', error='RELEASE_ASSET_MISSING')
        asset = value['asset']
        content['/stable/' + asset['path']] = b'x' * asset['size']
        run('tampered download', 'download', '--out', str(root / 'tampered'), error='RELEASE_CHECKSUM_MISMATCH')
        content['/stable/' + asset['path']] = payloads[asset['path']]
        source['trust']['identity'] = 'wrong-publisher'
        sourcefile.write_text(json.dumps(source))
        run('wrong publisher', 'check', error='RELEASE_IDENTITY_REJECTED')
        source['trust']['identity'] = 'fixture-publisher'
        sourcefile.write_text(json.dumps(source))
        original = content['/stable/manifest.json']
        content['/stable/manifest.json'] = original.replace(b'"schema": 1', b'"schema": 2')
        run('tampered signed manifest', 'check', error='RELEASE_IDENTITY_REJECTED')
        content['/stable/manifest.json'] = original
        bad = copy.deepcopy(base)
        bad['protocols']['api'] = 999
        catalog[:] = [signed(bad, 'bad-manifest')]
        run('incompatible signed manifest', 'check', error='RELEASE_PROTOCOL_UNSUPPORTED')
        catalog[:] = [copy.deepcopy(stable)]
        catalog[0]['assets'] = catalog[0]['assets'][:2]
        run('missing catalog asset', 'check', error='RELEASE_ASSET_MISSING')
        assert not (root / 'unused-runtime').exists()
        assert not (project / '.ael/runtime.db').exists()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
result = {'status': 'passed', 'build': base['build'], 'isolated_loopback_source': True, 'real_ssh_signature': True, 'official_release_access': False, 'remote_project_without_local_runtime': True, 'cases': results}
out = Path(args.out)
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))

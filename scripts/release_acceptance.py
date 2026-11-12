#!/usr/bin/env python3
"""Verify packaged AEL distributions, actual native client and legacy install."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve()
if out.exists():
    raise SystemExit('choose a new acceptance output file')
release = bundle / 'release'
manifest = json.loads((release / 'manifest.json').read_text())
native = platform.system().lower() + '-' + {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine().lower()]
binary = bundle / ('ael-' + native)
with tempfile.TemporaryDirectory(prefix='ael-release-acceptance-') as directory:
    root = Path(directory)
    env = dict(os.environ, AEL_DATA_DIR=str(root / 'unused-runtime'), AEL_INSTALL_PREFIX=str(root / 'unused-install'), HOME=str(root / 'home'))
    def run(executable, *values, expected=True, extra_env=None):
        result = subprocess.run([str(executable), *values], env=dict(env, **(extra_env or {})), cwd=root, text=True, capture_output=True, timeout=120)
        assert (result.returncode == 0) == expected, (values, result.stdout, result.stderr)
        return json.loads(result.stdout) if expected else result.stderr
    verification = run(binary, 'release', 'verify', '--manifest', str(release / 'manifest.json'), '--json')
    assert verification['content_integrity'] == 'verified' and verification['publisher_identity'] == 'unverified'
    packages = []
    for asset in manifest['assets']:
        if asset['kind'] not in ['client', 'server', 'skill']:
            continue
        with tarfile.open(release / asset['path'], 'r:gz') as archive:
            members = archive.getmembers()
            assert all(member.isfile() and not member.name.startswith('/') and '..' not in Path(member.name).parts for member in members)
            package = json.load(archive.extractfile('package.json'))
            assert package['commit'] == manifest['commit'] and package['build'] == manifest['build'] and package['protocols'] == manifest['protocols']
            for record in package['files']:
                data = archive.extractfile(record['path']).read()
                assert len(data) == record['size'] and hashlib.sha256(data).hexdigest() == record['sha256']
            assert set(member.name for member in members) == {record['path'] for record in package['files']} | {'package.json'}
            if asset['kind'] == 'client':
                assert {member.name for member in members} == {'ael', 'README.md', 'THIRD_PARTY_NOTICES.txt', 'package.json'}
            if asset['kind'] == 'client' and asset['platform'] == native:
                client = root / 'standalone-client'
                client.write_bytes(archive.extractfile('ael').read())
                client.chmod(0o755)
                version = run(client, 'version', '--json', extra_env={'PATH': '/no-host-tools'})
                assert version['build'] == manifest['build']
                check = run(client, 'release', 'check', '--json', extra_env={'PATH': '/no-host-tools'})
                assert check['status'] == 'no_configured_source' and not check['available']
        packages.append({'kind': asset['kind'], 'platform': asset['platform'], 'files_verified': len(package['files'])})
    bad = root / 'tampered'
    bad.mkdir()
    asset = next(item for item in manifest['assets'] if item['kind'] == 'client')
    (bad / 'manifest.json').write_text(json.dumps(manifest))
    (bad / asset['path']).write_bytes(b'tamper')
    error = run(binary, 'release', 'verify', '--manifest', str(bad / 'manifest.json'), '--json', expected=False)
    assert 'RELEASE_CHECKSUM_MISMATCH' in error
    prefix = root / 'legacy-install'
    preview = run(binary, 'upgrade', '--from', str(bundle), '--prefix', str(prefix), '--dry-run', '--json')
    installed = run(binary, 'upgrade', '--from', str(bundle), '--prefix', str(prefix), '--json')
    installed_version = run(prefix / 'bin/ael', 'version', '--json')
    assert installed_version['build'] == manifest['build']
result = {'status': 'passed', 'build': manifest['build'], 'commit': manifest['commit'], 'dirty': manifest['dirty'], 'publication': manifest['publication'], 'native': native, 'content_integrity': 'verified', 'publisher_identity': 'unverified', 'packages': packages, 'client_without_host_tools': 'passed', 'tamper': 'rejected', 'legacy_install': 'passed', 'physical_other_platform_execution': 'unverified'}
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))

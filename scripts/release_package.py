#!/usr/bin/env python3
"""Package independently consumable AEL clients, Server and protocol resources."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binaries', required=True)
parser.add_argument('--out', required=True)
parser.add_argument('--platform', default='all')
parser.add_argument('--publication', choices=['local_unpublished', 'build_artifact', 'draft', 'released'], default='local_unpublished')
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
binaries = Path(args.binaries).resolve()
out = Path(args.out).resolve()
out.mkdir(parents=True, exist_ok=True)
native = platform.system().lower() + '-' + {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}[platform.machine().lower()]
platforms = ['darwin-arm64', 'darwin-amd64', 'linux-amd64', 'linux-arm64'] if args.platform == 'all' else [native if args.platform == 'native' else args.platform]
if any(value not in ['darwin-arm64', 'darwin-amd64', 'linux-amd64', 'linux-arm64'] for value in platforms):
    raise SystemExit('unsupported platform')
version = json.loads(subprocess.check_output([str(binaries / ('ael-' + native)), 'version', '--json'], text=True))
commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
dirty = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo, text=True).strip())
manifest = {'schema': 1, 'version': version['version'], 'build': version['build'], 'commit': commit, 'dirty': dirty, 'publication': args.publication, 'protocols': {'api': 1, 'skill': 1, 'cli_output': 1}, 'assets': []}
if args.publication == 'released' and dirty:
    raise SystemExit('released artifacts require a clean source checkout')

def archive(kind, target, files, content):
    name = 'ael-' + kind + '-' + version['version'] + '-' + target + '.tar.gz'
    destination = out / name
    with tempfile.TemporaryDirectory(prefix='ael-release-stage-') as stage_name:
        stage = Path(stage_name)
        for source, relative in files:
            target_file = stage / relative
            target_file.parent.mkdir(parents=True, exist_ok=True)
            source_path = Path(source)
            if source_path.is_symlink() or any(file.is_symlink() for file in source_path.rglob("*")):
                raise SystemExit("distribution sources must not contain symlinks")
            if source_path.is_dir():
                shutil.copytree(source, target_file)
            else:
                shutil.copyfile(source, target_file)
                target_file.chmod(0o755 if relative == 'ael' else 0o644)
        for relative, value in content.items():
            (stage / relative).write_text(value)
        records = []
        for file in sorted(stage.rglob('*')):
            if file.is_file():
                records.append({'path': file.relative_to(stage).as_posix(), 'sha256': hashlib.sha256(file.read_bytes()).hexdigest(), 'size': file.stat().st_size})
        package = dict(version=version['version'], build=version['build'], commit=commit, kind=kind, platform=target, protocols=manifest['protocols'], files=records)
        (stage / 'package.json').write_text(json.dumps(package, indent=2) + '\n')
        temporary = out / ('.' + name + '.tmp')
        try:
            with tarfile.open(temporary, 'w:gz') as result:
                for file in sorted(stage.rglob('*')):
                    if file.is_file():
                        result.add(file, arcname=file.relative_to(stage), recursive=False)
            os.replace(temporary, destination)
        finally:
            if temporary.exists():
                temporary.unlink()
    manifest['assets'].append({'kind': kind, 'platform': target, 'path': name, 'sha256': hashlib.sha256(destination.read_bytes()).hexdigest(), 'size': destination.stat().st_size})

notice = (repo / 'THIRD_PARTY_NOTICES.txt', 'THIRD_PARTY_NOTICES.txt')
for target in platforms:
    binary = binaries / ('ael-' + target)
    if not binary.is_file():
        raise SystemExit('missing built runtime ' + target)
    archive('client', target, [(binary, 'ael'), notice], {'README.md': '# AEL client\n\nRun `./ael version`, configure a Server profile and connect the selected project. This package contains one platform executable. Communication requires the Server address and an authorized AEL token. Protocol and Skill resources are published separately.\n'})
server_platforms = sorted({'linux-' + target.rsplit('-', 1)[1] for target in platforms})
for target in server_platforms:
    binary = binaries / ('ael-' + target)
    if not binary.is_file():
        raise SystemExit('missing Server runtime ' + target)
    archive('server', target, [(binary, 'ael'), notice], {'README.md': '# AEL Server\n\nUse a dedicated persistent data directory. Initialize an administrator with `./ael server bootstrap --data-dir /data --username owner --password-stdin`, then run `./ael server start --data-dir /data`. Non-loopback listening requires an explicit Origin.\n'})
resources = [(repo / 'assets/skills', 'skills'), notice]
for name in ['step-result.json', 'assessment.json', 'plan-proposal.json']:
    resources.append((repo / 'assets' / name, name))
archive('skill', 'all', resources, {'README.md': '# AEL protocol resources\n\nAPI, Skill and CLI output protocols are recorded in package.json. Install the selected Skill through the authorized client setup or project asset update. Existing external Agents own actual execution.\n'})
legacy = binaries / ('ael-' + version['version'] + '-unix.tar.gz')
if legacy.is_file():
    target = out / legacy.name
    if target != legacy:
        shutil.copyfile(legacy, target)
    manifest['assets'].append({'kind': 'legacy', 'platform': 'all', 'path': target.name, 'sha256': hashlib.sha256(target.read_bytes()).hexdigest(), 'size': target.stat().st_size})
temporary = out / '.manifest.json.tmp'
temporary.write_text(json.dumps(manifest, indent=2) + '\n')
os.replace(temporary, out / 'manifest.json')
print(json.dumps({'status': 'packaged', 'publication': args.publication, 'manifest': str(out / 'manifest.json'), 'assets': len(manifest['assets'])}))

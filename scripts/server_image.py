#!/usr/bin/env python3
"""Build and export the independent Server image from a verified local package."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--manifest', required=True)
parser.add_argument('--platform', choices=['linux-arm64', 'linux-amd64'], required=True)
parser.add_argument('--tag', required=True, help='Explicit local image name. No registry push is performed.')
args = parser.parse_args()
path = Path(args.manifest).resolve()
manifest = json.loads(path.read_text())
asset = next(item for item in manifest['assets'] if item['kind'] == 'server' and item['platform'] == args.platform)
if Path(asset['path']).name != asset['path']:
    raise SystemExit('invalid Server asset path')
archive = path.parent / asset['path']
if archive.is_symlink() or archive.stat().st_size != asset['size'] or hashlib.file_digest(archive.open('rb'), 'sha256').hexdigest() != asset['sha256']:
    raise SystemExit('Server package integrity failure')
repo = Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory(prefix='.ael-server-image-', dir=path.parent) as directory:
    stage = Path(directory)
    with tarfile.open(archive, 'r:gz') as package:
        for name in ['ael', 'THIRD_PARTY_NOTICES.txt']:
            member = package.getmember(name)
            if not member.isfile():
                raise SystemExit('Server package entries must be regular files')
            with package.extractfile(member) as source, (stage / name).open('wb') as target:
                shutil.copyfileobj(source, target)
    (stage / 'ael').chmod(0o755)
    (stage / 'data').mkdir(mode=0o700)
    shutil.copyfile(repo / 'packaging/server.Dockerfile', stage / 'Dockerfile')
    subprocess.run(['docker', 'build', '--platform', args.platform.replace('-', '/'), '--build-arg', 'VERSION=' + manifest['version'], '--build-arg', 'COMMIT=' + manifest['commit'], '--build-arg', 'SOURCE=https://github.com/' + os.environ.get('GITHUB_REPOSITORY', 'adougebabi/ael'), '-t', args.tag, str(stage)], check=True)
    name = 'ael-server-image-' + manifest['version'] + '-' + args.platform + '.tar.gz'
    destination = path.parent / name
    temporary = stage / name
    with temporary.open('wb') as raw, gzip.GzipFile(fileobj=raw, mode='wb', mtime=0) as compressed:
        process = subprocess.Popen(['docker', 'image', 'save', args.tag], stdout=subprocess.PIPE)
        try:
            shutil.copyfileobj(process.stdout, compressed)
        finally:
            process.stdout.close()
        if process.wait():
            raise SystemExit('docker image save failed')
    os.replace(temporary, destination)
    record = {'kind': 'server_image', 'platform': args.platform, 'path': name, 'size': destination.stat().st_size}
    with destination.open('rb') as file:
        record['sha256'] = hashlib.file_digest(file, 'sha256').hexdigest()
    manifest['assets'] = [item for item in manifest['assets'] if (item['kind'], item['platform']) != ('server_image', args.platform)] + [record]
    pending = path.with_name('.manifest.image.tmp')
    pending.write_text(json.dumps(manifest, indent=2) + '\n')
    os.replace(pending, path)
print(json.dumps({'status': 'local_image_built', 'tag': args.tag, 'asset': record, 'publication': manifest['publication']}))

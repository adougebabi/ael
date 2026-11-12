#!/usr/bin/env python3
"""Explicit CI publication helper. Requires authorized gh credentials and a real tag."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--manifest', required=True)
parser.add_argument('--publication', choices=['draft', 'released'], required=True)
args = parser.parse_args()
path = Path(args.manifest).resolve()
manifest = json.loads(path.read_text())
repo = os.environ['GH_REPO']
commit = os.environ['SOURCE_COMMIT']
version = manifest['version']
if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?', version):
    raise SystemExit('invalid release version')
if manifest['dirty'] or manifest['commit'] != commit or manifest['publication'] != args.publication:
    raise SystemExit('source commit, clean state and explicit publication must match')
files = [path, path.parent / 'manifest.sigstore.json']
for asset in manifest['assets']:
    if Path(asset['path']).name != asset['path']:
        raise SystemExit('invalid asset name')
    file = path.parent / asset['path']
    with file.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    if file.is_symlink() or file.stat().st_size != asset['size'] or digest != asset['sha256']:
        raise SystemExit('asset changed since build')
    files.append(file)
if any(not file.is_file() or file.is_symlink() for file in files):
    raise SystemExit('missing manifest, signature or asset')

def gh(*values):
    return subprocess.check_output(['gh', *values], text=True)

tag = 'v' + version
remote_commit = json.loads(gh('api', 'repos/' + repo + '/commits/' + tag))['sha']
if remote_commit != commit:
    raise SystemExit('existing remote tag must point to the exact source commit')
existing = None
for page in range(1, 1001):
    releases = json.loads(gh('api', 'repos/' + repo + '/releases?per_page=100&page=' + str(page)))
    existing = next((release for release in releases if release['tag_name'] == tag), None)
    if existing or len(releases) < 100:
        break
else:
    raise SystemExit('release catalog exceeds bounded publication lookup')
if existing and not existing['draft']:
    raise SystemExit('refusing to modify an existing published release')
prerelease = '-' in version.split('+', 1)[0]
flags = ['--prerelease=' + str(prerelease).lower()]
if prerelease:
    flags.append('--latest=false')
if existing:
    gh('release', 'upload', tag, *map(str, files), '--clobber')
    gh('release', 'edit', tag, '--verify-tag', '--draft=' + str(args.publication == 'draft').lower(), *flags)
else:
    if args.publication == 'draft':
        flags.append('--draft')
    gh('release', 'create', tag, *map(str, files), '--verify-tag', '--title', 'AEL ' + version, '--notes', 'Independent clients, Server and protocol resources. Verify the signed manifest using your pinned publisher policy.', *flags)
print(json.dumps({'status': args.publication, 'tag': tag, 'commit': commit, 'prerelease': prerelease}))

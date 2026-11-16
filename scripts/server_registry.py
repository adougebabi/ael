#!/usr/bin/env python3
"""Publish verified Server image archives and maintain the signed stable alias."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def command(*arguments):
    result = subprocess.run(arguments, capture_output=True, text=True, timeout=300)
    if result.returncode:
        raise RuntimeError(arguments[0] + ' failed: ' + result.stderr[-4000:])
    return result.stdout


def image_digest(reference):
    description = command('docker', 'buildx', 'imagetools', 'inspect', reference)
    match = re.search(r'^Digest:\s+(sha256:[0-9a-f]{64})\s*$', description, re.MULTILINE)
    if not match:
        raise RuntimeError('registry did not return a bounded image digest')
    return match[1]


def verify_reference(image, version):
    identity = 'https://github.com/' + os.environ['GH_REPO'] + '/.github/workflows/release.yml@refs/heads/main'
    verified = json.loads(command('cosign', 'verify', '--certificate-identity', identity,
                                  '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com',
                                  '--output', 'json', image + ':' + version.replace('+', '_')))
    digests = {item['critical']['image']['docker-manifest-digest'] for item in verified}
    if len(digests) != 1 or not re.fullmatch(r'sha256:[0-9a-f]{64}', next(iter(digests))):
        raise RuntimeError('image signature returned conflicting digests')
    return image + '@' + next(iter(digests))


def promote(image):
    versions = []
    for page in range(1, 1001):
        releases = json.loads(command('gh', 'api', 'repos/' + os.environ['GH_REPO'] + '/releases?per_page=100&page=' + str(page)))
        for release in releases:
            match = re.fullmatch(r'v(\d+)\.(\d+)\.(\d+)(?:\+[0-9A-Za-z.-]+)?', release['tag_name'])
            if match and not release['draft'] and not release['prerelease']:
                versions.append((tuple(map(int, match.groups())), release['tag_name'][1:]))
        if len(releases) < 100:
            break
    else:
        raise RuntimeError('release catalog exceeds the bounded lookup')
    if not versions:
        raise RuntimeError('no published stable release exists')
    version = max(versions, key=lambda item: item[0])[1]
    reference = verify_reference(image, version)
    command('docker', 'buildx', 'imagetools', 'create', '--tag', image + ':latest', reference)
    if image_digest(image + ':latest') != reference.split('@', 1)[1]:
        raise RuntimeError('stable alias does not match the verified index')
    return {'status': 'stable_alias_updated', 'version': version, 'reference': reference, 'latest': image + ':latest'}


def publish(path, image):
    manifest = json.loads(path.read_text())
    if manifest['dirty'] or manifest['publication'] != 'released':
        raise RuntimeError('registry publication requires a clean released manifest')
    version = manifest['version']
    tag = version.replace('+', '_')
    if not re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}', tag):
        raise RuntimeError('version cannot be represented as an OCI tag')
    references = []
    for architecture in ['amd64', 'arm64']:
        asset = next(item for item in manifest['assets'] if item['kind'] == 'server_image' and item['platform'] == 'linux-' + architecture)
        if Path(asset['path']).name != asset['path']:
            raise RuntimeError('invalid Server image archive path')
        archive = path.parent / asset['path']
        if archive.is_symlink() or archive.stat().st_size != asset['size']:
            raise RuntimeError('Server image archive integrity failure')
        with archive.open('rb') as content:
            if hashlib.file_digest(content, 'sha256').hexdigest() != asset['sha256']:
                raise RuntimeError('Server image archive integrity failure')
        command('docker', 'image', 'load', '--input', str(archive))
        local = 'ael-server:ci-' + architecture
        metadata = json.loads(command('docker', 'image', 'inspect', local))[0]
        labels = metadata['Config']['Labels']
        if metadata['Architecture'] != architecture or metadata['Os'] != 'linux' or labels['org.opencontainers.image.version'] != version or labels['org.opencontainers.image.revision'] != manifest['commit']:
            raise RuntimeError('image platform or source identity differs from the manifest')
        reference = image + ':' + tag + '-' + architecture
        command('docker', 'image', 'tag', local, reference)
        command('docker', 'image', 'push', reference)
        references.append(reference)
        print('Published verified linux/' + architecture + ' image', flush=True)
    command('docker', 'buildx', 'imagetools', 'create', '--tag', image + ':' + tag, *references)
    index = json.loads(command('docker', 'buildx', 'imagetools', 'inspect', '--raw', image + ':' + tag))
    platforms = {entry.get('platform', {}).get('os', '') + '/' + entry.get('platform', {}).get('architecture', '') for entry in index.get('manifests', [])}
    if not {'linux/amd64', 'linux/arm64'}.issubset(platforms):
        raise RuntimeError('registry index is missing a required Server platform')
    digest = image_digest(image + ':' + tag)
    reference = image + '@' + digest
    command('cosign', 'sign', '--yes', reference)
    receipt = {'image': image, 'version': version, 'commit': manifest['commit'], 'build': manifest['build'],
               'digest': digest, 'reference': reference, 'platforms': ['linux/amd64', 'linux/arm64']}
    (path.parent / 'server-registry.json').write_text(json.dumps(receipt, indent=2) + '\n')
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write('image=' + image + '\n')
    return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', type=Path)
    parser.add_argument('--image', required=True)
    parser.add_argument('--promote-latest', action='store_true')
    args = parser.parse_args()
    if not re.fullmatch(r'ghcr\.io/[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*', args.image):
        parser.error('provide an explicit lowercase GHCR image repository')
    if not args.promote_latest and not args.manifest:
        parser.error('--manifest is required for image publication')
    result = promote(args.image) if args.promote_latest else publish(args.manifest.resolve(), args.image)
    print(json.dumps(result))


if __name__ == '__main__':
    main()

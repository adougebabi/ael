#!/usr/bin/env python3
"""Publish verified Server image archives to Docker Hub."""
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


def publish(path, image, main=False):
    manifest = json.loads(path.read_text())
    expected_publication = 'build_artifact' if main else 'released'
    if manifest['dirty'] or manifest['publication'] != expected_publication:
        raise RuntimeError('registry publication requires a clean matching manifest')
    version = manifest['version']
    tag = 'main' if main else version.replace('+', '_')
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
    if main:
        command('docker', 'buildx', 'imagetools', 'create', '--tag', image + ':latest', reference)
        if image_digest(image + ':latest') != digest:
            raise RuntimeError('latest does not match the verified main index')
    receipt = {'image': image, 'version': version, 'commit': manifest['commit'], 'build': manifest['build'],
               'tag': tag, 'digest': digest, 'reference': reference, 'platforms': ['linux/amd64', 'linux/arm64']}
    (path.parent / 'server-registry.json').write_text(json.dumps(receipt, indent=2) + '\n')
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write('image=' + image + '\n')
    return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', type=Path, required=True)
    parser.add_argument('--image', required=True)
    parser.add_argument('--main', action='store_true', help='publish the main and latest branch images')
    args = parser.parse_args()
    if not re.fullmatch(r'docker\.io/[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*', args.image):
        parser.error('provide an explicit lowercase Docker Hub image repository')
    result = publish(args.manifest.resolve(), args.image, args.main)
    print(json.dumps(result))


if __name__ == '__main__':
    main()

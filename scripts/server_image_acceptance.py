#!/usr/bin/env python3
"""Run an isolated local Server image and verify auth and persistent restart."""
import argparse
import http.cookiejar
import json
from pathlib import Path
import socket
import subprocess
import time
import urllib.error
import urllib.request
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--image', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
out = Path(args.out).resolve()
if out.exists():
    raise SystemExit('choose a new fixture output file')
name = 'ael-v218-' + uuid.uuid4().hex[:12]
volume = name + '-data'
password = 'fixture-image-password-218'

def docker(*values, input=None):
    result = subprocess.run(['docker', *values], input=input, text=True, capture_output=True, timeout=120)
    if result.returncode:
        raise RuntimeError('docker fixture operation failed: ' + ' '.join(values[:3]) + '\n' + result.stderr)
    return result.stdout

with socket.socket() as reservation:
    reservation.bind(('127.0.0.1', 0))
    port = reservation.getsockname()[1]
origin = 'http://127.0.0.1:' + str(port)
phases = []
try:
    docker('volume', 'create', volume)
    docker('run', '--rm', '-i', '-v', volume + ':/data', args.image, 'server', 'bootstrap', '--data-dir', '/data', '--username', 'owner', '--password-stdin', '--json', input=password + '\n')
    for phase in ['initial', 'restart']:
        docker('run', '-d', '--name', name, '-p', '127.0.0.1:' + str(port) + ':7332', '-v', volume + ':/data', '-e', 'AEL_SERVER_ORIGIN=' + origin, args.image)
        cookies = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
        def request(path, body=None, csrf=None, supplied_origin=None):
            headers = {'Origin': supplied_origin or origin, 'X-AEL-Protocol': '1'}
            if body is not None:
                headers['Content-Type'] = 'application/json'
            if csrf:
                headers['X-AEL-CSRF'] = csrf
            req = urllib.request.Request(origin + path, None if body is None else json.dumps(body).encode(), headers)
            with opener.open(req, timeout=5) as response:
                return response.read()
        for attempt in range(100):
            try:
                meta = json.loads(request('/api/v1/meta'))
                break
            except (OSError, urllib.error.URLError):
                time.sleep(0.1)
        else:
            raise RuntimeError('image did not become ready')
        assert meta['mode'] == 'server'
        assert b'<html' in request('/')
        for path, status, bad_origin in [('/api/v1/projects', 401, None), ('/api/v1/meta', 403, 'http://untrusted.invalid')]:
            try:
                request(path, supplied_origin=bad_origin)
                raise RuntimeError('request gate accepted invalid access')
            except urllib.error.HTTPError as error:
                assert error.code == status
        csrf = json.loads(request('/api/v1/auth/login', {'username': 'owner', 'password': password}))['csrf']
        if phase == 'initial':
            request('/api/v1/projects', {'id': 'image-fixture', 'name': 'Persistent image fixture'}, csrf)
        projects = json.loads(request('/api/v1/projects'))['projects']
        assert any(project['id'] == 'image-fixture' for project in projects)
        phases.append({'phase': phase, 'meta': meta, 'login': 'passed', 'project_persisted': True, 'unauthorized': 401, 'wrong_origin': 403, 'dashboard': 'served'})
        docker('stop', '-t', '3', name)
        docker('rm', name)
    image = json.loads(docker('image', 'inspect', args.image))[0]
    assert image['Config']['User'] == '65532:65532'
    result = {'status': 'passed', 'image': args.image, 'image_id': image['Id'], 'architecture': image['Architecture'], 'user': image['Config']['User'], 'phases': phases, 'isolated_local_fixture': True, 'production_access': False}
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))
finally:
    subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
    subprocess.run(['docker', 'volume', 'rm', volume], capture_output=True)

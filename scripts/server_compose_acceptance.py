#!/usr/bin/env python3
"""Verify an isolated Compose server, genuine HTTPS, task and image persistence."""
import argparse
import hashlib
from datetime import datetime, timedelta, timezone
import http.cookiejar
import ipaddress
import json
import os
from pathlib import Path
import platform
import shutil
import socket
import ssl
import struct
import subprocess
import time
import urllib.error
import urllib.request
import uuid
import zlib

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bundle', required=True)
parser.add_argument('--image', required=True)
parser.add_argument('--proxy-image', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve()
if out.exists():
    raise SystemExit('choose a new isolated fixture directory')
out.mkdir(parents=True, mode=0o700)
(out / 'ISOLATED-COMPOSE').write_text('Owned disposable local Compose deployment. No Oracle or business access.\n')
project_name = 'ael-v220-' + uuid.uuid4().hex[:10]
native = platform.system().lower() + '-' + {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine().lower()]
binary = bundle / ('ael-' + native)
events = []
preflight_disk = shutil.disk_usage(out)


def command(label, values, input_text=None, check=True, env=None):
    result = subprocess.run(list(map(str, values)), input=input_text, text=True, capture_output=True, env=env, timeout=180)
    if check and result.returncode:
        raise RuntimeError(label + '\n' + result.stderr)
    return result

published_lines = command('existing local services', ['docker', 'ps', '--format', 'json']).stdout.splitlines()
published_services = [json.loads(line) for line in published_lines if line.strip()]
existing_proxy_count = sum(any(name in service.get('Image', '').lower() for name in ['caddy', 'nginx', 'traefik']) for service in published_services)
context = command('local Docker context', ['docker', 'context', 'show']).stdout.strip()
context_info = json.loads(command('local context details', ['docker', 'context', 'inspect', context]).stdout)[0]
assert context_info['Endpoints']['docker']['Host'] == 'unix:///Users/vinson/.orbstack/run/docker.sock'
engine = json.loads(command('engine info', ['docker', 'info', '--format', '{{json .}}']).stdout)
image = json.loads(command('image inspect', ['docker', 'image', 'inspect', args.image]).stdout)[0]
proxy = json.loads(command('proxy inspect', ['docker', 'image', 'inspect', args.proxy_image]).stdout)[0]
assert image['Config']['User'] == '65532:65532'
network_lines = command('network inventory', ['docker', 'network', 'ls', '--format', 'json']).stdout.splitlines()
networks = [json.loads(line) for line in network_lines if line.strip()]
used = []
for network in networks:
    detail = json.loads(command('network detail', ['docker', 'network', 'inspect', network['ID']]).stdout)[0]
    for item in (detail.get('IPAM', {}).get('Config') or []):
        if item.get('Subnet'):
            used.append(ipaddress.ip_network(item['Subnet']))
subnet = None
for third in range(220, 250):
    candidate = ipaddress.ip_network('172.29.' + str(third) + '.0/29')
    if not any(candidate.overlaps(existing) for existing in used if existing.version == 4):
        subnet = candidate
        break
assert subnet is not None
with socket.socket() as reservation:
    reservation.bind(('127.0.0.1', 0))
    port = reservation.getsockname()[1]
origin = 'https://localhost:' + str(port)
deployment = out / 'deployment'
shutil.copytree(repo / 'packaging/compose', deployment)
env = dict(os.environ, COMPOSE_PROJECT_NAME=project_name, AEL_IMAGE=args.image, CADDY_IMAGE=args.proxy_image, AEL_PUBLIC_ORIGIN=origin, AEL_SITE_HOST='localhost', AEL_HTTPS_PORT=str(port), AEL_BIND_IP='127.0.0.1', AEL_NETWORK_CIDR=str(subnet), AEL_PROXY_IP=str(subnet.network_address + 2), AEL_SERVER_IP=str(subnet.network_address + 3))
compose_base = ['docker', 'compose', '--project-directory', str(deployment), '-f', str(deployment / 'compose.yaml'), '-p', project_name]
def compose(label, *values, input_text=None, check=True):
    return command(label, compose_base + list(values), input_text, check, env)

config = json.loads(compose('validated Compose config', 'config', '--format', 'json').stdout)
assert 'ports' not in config['services']['server']
assert config['services']['server']['volumes'][0]['target'] == '/data'
assert all('docker.sock' not in json.dumps(service) for service in config['services'].values())
project = out / 'external-project'
project.mkdir()
client_env = dict(os.environ, AEL_REMOTE_CONFIG_DIR=str(out / 'client-config'), AEL_DATA_DIR=str(out / 'unused-runtime'), PATH='/usr/bin:/bin:/usr/sbin:/sbin', AEL_API_PROTOCOL='1', AEL_SKILL_PROTOCOL='1')
assert shutil.which('docker', path=client_env['PATH']) is None
def cli(label, *values, input_text=None):
    result = command(label, [binary, *values, '--project', project, '--json'], input_text, env=client_env)
    value = json.loads(result.stdout)
    events.append({'case': label, 'passed': True})
    return value

def png():
    def chunk(kind, data):
        return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', zlib.crc32(kind + data) & 0xffffffff)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!2I5B', 2, 2, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(b'\x00\x20\x70\xa0\x20\x70\xa0' * 2)) + chunk(b'IEND', b'')

restored_compose = None
backup_volume = project_name + '_backup_data'
try:
    compose('bootstrap over stdin', 'run', '--rm', '--no-deps', '-T', 'server', 'server', 'bootstrap', '--data-dir', '/data', '--username', 'owner', '--password-stdin', '--json', input_text='fixture-compose-password-220\n')
    compose('start isolated Compose', 'up', '-d', '--wait')
    root_ca = out / 'root-ca.crt'
    for attempt in range(120):
        copied = compose('export public local root', 'cp', 'proxy:/data/caddy/pki/authorities/local/root.crt', str(root_ca), check=False)
        if copied.returncode == 0 and root_ca.is_file():
            break
        time.sleep(0.25)
    else:
        raise RuntimeError('proxy did not create its local CA')
    tls = ssl.create_default_context(cafile=str(root_ca))
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=tls), urllib.request.HTTPCookieProcessor(jar))
    csrf = None
    def api(method, path, body=None, expected=200, bearer=None):
        headers = {'Origin': origin, 'X-AEL-Protocol': '1'}
        if body is not None:
            headers['Content-Type'] = 'application/json'
            if csrf:
                headers['X-AEL-CSRF'] = csrf
        if bearer:
            headers['Authorization'] = 'Bearer ' + bearer
        request = urllib.request.Request(origin + path, None if body is None else json.dumps(body).encode(), headers, method=method)
        try:
            with opener.open(request, timeout=15) as response:
                status, data = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, data = error.code, error.read()
        if status != expected:
            raise RuntimeError('isolated HTTPS API returned ' + str(status) + ' for ' + path)
        return json.loads(data) if data else {}
    api('GET', '/api/v1/projects', expected=401)
    api('GET', '/healthz', expected=403)
    csrf = api('POST', '/api/v1/auth/login', {'username': 'owner', 'password': 'fixture-compose-password-220'})['csrf']
    assert all(cookie.secure for cookie in jar)
    api('POST', '/api/v1/projects', {'id': 'compose-fixture', 'name': 'Compose fixture'}, expected=201)
    api('POST', '/api/v1/users', {'username': 'agent', 'password': 'fixture-compose-agent-password'}, expected=201)
    api('POST', '/api/v1/projects/compose-fixture/members', {'username': 'agent', 'role': 'executor'}, expected=201)
    token = api('POST', '/api/v1/tokens', {'username': 'agent', 'project_id': 'compose-fixture', 'label': 'external fixture'}, expected=201)['token']
    cli('save explicit public CA with private profile', 'server', 'profile', 'add', '--name', 'compose', '--url', origin, '--ca-file', root_ca, '--token-stdin', input_text=token + '\n')
    cli('connect external CLI through genuine TLS', 'project', 'connect', '--profile', 'compose', '--remote-project', 'compose-fixture')
    definition = out / 'task.json'
    definition.write_text(json.dumps({'id': 'persistent-task', 'title': 'Persistent task and image', 'type': 'bug', 'goal': 'verified', 'acceptance': ['report']}))
    task = cli('create actual task', 'task', 'create', '--file', definition, '--operation-id', 'create', '--idempotency-key', 'create')
    picture = project / 'fixture.png'
    picture.write_bytes(png())
    artifact = cli('upload actual image bytes', 'artifact', 'upload', '--task', 'persistent-task', '--file', picture, '--expected-revision', task['revision'], '--operation-id', 'image', '--idempotency-key', 'image')
    expected_hash = hashlib.sha256(picture.read_bytes()).hexdigest()
    assert artifact['sha256'] == expected_hash
    command('initialize host-owned Git workspace', ['git', '-C', project, 'init', '-q'])
    command('stage selected fixture files', ['git', '-C', project, 'add', '.'])
    command('commit actual fixture baseline', ['git', '-C', project, '-c', 'user.name=AEL fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'fixture baseline'])

    cli('register host repository', 'repository', 'register', '--repository', 'compose-repo', '--operation-id', 'repo', '--idempotency-key', 'repo')
    cli('index selected context', 'knowledge', 'index')
    register = out / 'runner.json'
    register.write_text(json.dumps({'id': 'compose-agent', 'workspace_id': 'compose-workspace', 'checkout_id': 'compose-checkout', 'repository': 'compose-repo', 'version': 'external-maintenance-driver', 'capabilities': ['development'], 'expected_revision': 'absent'}))
    register.chmod(0o600)
    cli('register external client', 'runner', 'register', '--file', register, '--operation-id', 'runner', '--idempotency-key', 'runner')
    current_task = cli('read actual current responsibility version', 'task', 'show', '--task', 'persistent-task')
    observed = cli('capture actual host candidate', 'candidate', 'capture', '--task', 'persistent-task', '--repository', 'compose-repo', '--checkout', 'compose-checkout', '--workspace', 'compose-workspace', '--runner', 'compose-agent', '--expected-revision', current_task['revision'], '--operation-id', 'candidate', '--idempotency-key', 'candidate')
    context = cli('publish actual host context', 'context', 'publish', '--task', 'persistent-task', '--observation', observed['id'], '--expected-revision', current_task['revision'], '--operation-id', 'context', '--idempotency-key', 'context')
    claim_request = out / 'claim-request.json'
    claim_request.write_text(json.dumps({'task_id': 'persistent-task', 'context_id': context['id'], 'expected_revision': current_task['revision'], 'operation_id': 'claim', 'idempotency_key': 'claim'}))
    claim_request.chmod(0o600)
    claim_file = out / 'claim.json'
    cli('external client claims actual responsibility', 'runner', 'claim', '--runner', 'compose-agent', '--file', claim_request, '--out', claim_file)
    claimed = json.loads(claim_file.read_text())['attempt']
    lease_body = {'task_id': 'persistent-task', 'attempt_id': claimed['id'], 'token': claimed['token'], 'fence': claimed['fence']}
    def heartbeat(label, key, expected_error=None):
        request = out / (key + '.json')
        request.write_text(json.dumps(dict(lease_body, operation_id=key, idempotency_key=key)))
        request.chmod(0o600)
        values = [binary, 'runner', 'heartbeat', '--runner', 'compose-agent', '--file', request, '--project', project, '--json']
        response = command(label, values, check=expected_error is None, env=client_env)
        if expected_error:
            assert response.returncode != 0 and expected_error in response.stderr
            value = None
        else:
            value = json.loads(response.stdout)
        events.append({'case': label, 'passed': True})
        return value
    def admin_cli(label, action, key, deadline=None):
        values = [binary, 'server', 'maintenance', '--url', origin, '--username', 'owner', '--password-stdin', '--ca-file', root_ca, '--action', action, '--reason', 'isolated maintenance fixture', '--request-key', key, '--json']
        if deadline:
            values.extend(['--deadline', deadline])
        response = command(label, values, 'fixture-compose-password-220\n', env=client_env)
        events.append({'case': label, 'passed': True})
        return json.loads(response.stdout)
    deadline = (datetime.now(timezone.utc) + timedelta(seconds=2)).isoformat()
    drained = admin_cli('formal admin CLI drain', 'drain', 'compose-drain', deadline)
    assert drained['current']['mode'] == 'draining'
    assert heartbeat('live responsibility receives drain notice', 'beat-before')['server_maintenance']['mode'] == 'draining'
    api('POST', '/api/v1/projects/compose-fixture/runners/missing/claim', {}, expected=503)
    time.sleep(2.1)
    api('POST', '/api/v1/projects', {'id': 'during-maintenance', 'name': 'must reject'}, expected=503)
    quiesced = admin_cli('formal admin CLI quiesce', 'quiesce', 'compose-quiesce')
    assert quiesced['current']['mode'] == 'quiesced'
    replayed = admin_cli('formal admin CLI exact replay', 'quiesce', 'compose-quiesce')
    assert replayed['replayed']
    backup = compose('full central backup at quiesced fence', 'run', '--rm', '--no-deps', '-T', 'operator', 'server', 'backup', '--data-dir', '/data', '--out', '/backup/export', '--json')
    backup_manifest = json.loads(backup.stdout)
    assert backup_manifest['data_format'] == 2
    compose('verify complete central backup', 'run', '--rm', '--no-deps', '-T', 'operator', 'server', 'verify-backup', '--from', '/backup/export', '--json')
    resumed = admin_cli('formal admin CLI resume', 'resume', 'compose-resume')
    assert resumed['current']['mode'] == 'active'
    heartbeat('old responsibility stays rejected after resume', 'beat-after', 'STALE_TOKEN')
    receipt = api('GET', '/api/v1/projects/compose-fixture/portable-operations/beat-before', bearer=token)
    events.append({'case': 'read original heartbeat receipt', 'passed': True})
    assert receipt['found']
    health = compose('private health from own server container', 'exec', '-T', 'server', '/ael', 'server', 'health', '--json')
    assert json.loads(health.stdout)['status'] == 'healthy'
    compose('restart only own server', 'restart', 'server')
    for attempt in range(100):
        try:
            readable = cli('read persisted task after restart', 'task', 'show', '--task', 'persistent-task')
            break
        except RuntimeError:
            time.sleep(0.2)
    else:
        raise RuntimeError('restarted service never became readable')
    target = project / 'downloaded.png'
    downloaded = cli('download persisted image after restart', 'artifact', 'download', '--artifact', artifact['id'], '--out', target)
    assert hashlib.sha256(target.read_bytes()).hexdigest() == expected_hash == downloaded['sha256']
    stats = api('GET', '/api/v1/projects/compose-fixture/artifacts/stats')
    assert stats['artifact_bytes'] >= len(picture.read_bytes())
    assert target.is_file() and not (project / '.ael/bin').exists()
    original_origin = origin
    restored_name = project_name + '-restored'
    with socket.socket() as reservation:
        reservation.bind(('127.0.0.1', 0))
        restored_port = reservation.getsockname()[1]
    origin = 'https://localhost:' + str(restored_port)
    restored_env = dict(env, COMPOSE_PROJECT_NAME=restored_name, AEL_PUBLIC_ORIGIN=origin, AEL_HTTPS_PORT=str(restored_port))
    restored_base = ['docker', 'compose', '--project-directory', str(deployment), '-f', str(deployment / 'compose.yaml'), '-p', restored_name]
    def restored_compose(label, *values, check=True):
        return command(label, restored_base + list(values), check=check, env=restored_env)
    restored = restored_compose('restore into fresh named volume at same /data path', 'run', '--rm', '--no-deps', '-T', '-v', backup_volume + ':/backup:ro', 'operator', 'server', 'restore', '--data-dir', '/data', '--from', '/backup/export', '--json')
    assert json.loads(restored.stdout)['status'] == 'restored_quiesced'
    # The source deployment is finished. Release its owned subnet before the
    # restored deployment uses the same validated proxy IP configuration.
    compose('stop original deployment before authority switch', 'down')
    restored_compose('start restored isolated service', 'up', '-d', '--wait')
    restored_ca = out / 'restored-root-ca.crt'
    for attempt in range(120):
        copied = restored_compose('export restored public CA', 'cp', 'proxy:/data/caddy/pki/authorities/local/root.crt', str(restored_ca), check=False)
        if copied.returncode == 0 and restored_ca.is_file():
            break
        time.sleep(0.25)
    else:
        raise RuntimeError('restored proxy did not create its CA')
    root_ca = restored_ca
    tls = ssl.create_default_context(cafile=str(root_ca))
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=tls), urllib.request.HTTPCookieProcessor(jar))
    csrf = None
    api('GET', '/api/v1/projects', expected=401, bearer=token)
    csrf = api('POST', '/api/v1/auth/login', {'username': 'owner', 'password': 'fixture-compose-password-220'})['csrf']
    maintenance = api('GET', '/api/v1/admin/maintenance')
    assert maintenance['mode'] == 'quiesced' and maintenance['epoch'] > backup_manifest['maintenance']['epoch']
    api('POST', '/api/v1/admin/maintenance', {'action': 'resume', 'reason': 'verified restored fixture', 'request_key': 'restored-resume'})
    renewed = api('POST', '/api/v1/tokens', {'username': 'agent', 'project_id': 'compose-fixture', 'label': 'after restore'}, expected=201)['token']
    project = out / 'restored-project'
    project.mkdir()
    cli('new identity connection after restore', 'server', 'profile', 'add', '--name', 'restored', '--url', origin, '--ca-file', root_ca, '--token-stdin', input_text=renewed + '\n')
    cli('bind to preserved project after restore', 'project', 'connect', '--profile', 'restored', '--remote-project', 'compose-fixture')
    cli('read preserved task after full restore', 'task', 'show', '--task', 'persistent-task')
    heartbeat('old responsibility stays rejected after full restore', 'beat-restored', 'STALE_TOKEN')
    restored_picture = project / 'restored.png'
    cli('download and reconcile image after full restore', 'artifact', 'download', '--artifact', artifact['id'], '--out', restored_picture)
    assert hashlib.sha256(restored_picture.read_bytes()).hexdigest() == expected_hash
    result = {'result': 'PASS', 'mode': 'same-machine local Compose with genuine verified HTTPS', 'engine': {'os': engine['OSType'], 'architecture': engine['Architecture']}, 'preflight': {'host_os': platform.platform(), 'free_bytes': preflight_disk.free, 'existing_proxy_count': existing_proxy_count, 'bind_ip': '127.0.0.1', 'network': str(subnet)}, 'image_id': image['Id'], 'proxy_image_id': proxy['Id'], 'origin': original_origin, 'restored_origin': origin, 'full_backup_restore': True, 'actual_old_responsibility_rejected': True, 'original_receipt_preserved': True, 'restored_old_token_rejected': True, 'private_health': 'passed', 'public_health_denied': True, 'secure_login_cookie': True, 'task': 'persistent-task', 'artifact': artifact['id'], 'image_sha256': expected_hash, 'restart_persistence': True, 'admin_cli_maintenance': True, 'deadline_rejects_writes': True, 'maintenance_replay': True, 'client_without_docker': True, 'events': events, 'oracle_access': False}
    (out / 'summary.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))
finally:
    logs = compose('owned deployment logs', 'logs', '--no-color', check=False)
    (out / 'compose.log').write_text(logs.stdout + logs.stderr)
    if restored_compose is not None:
        restored_logs = restored_compose('restored deployment logs', 'logs', '--no-color', check=False)
        (out / 'restored-compose.log').write_text(restored_logs.stdout + restored_logs.stderr)
        restored_compose('remove own restored deployment', 'down', '--volumes', '--remove-orphans', check=False)
    compose('remove only owned containers and volumes', 'down', '--volumes', '--remove-orphans', check=False)
    command('remove own backup volume', ['docker', 'volume', 'rm', backup_volume], check=False)

#!/usr/bin/env python3
"""Drive actual old/new clients, signed upgrades and two isolated Remote projects."""
import argparse
import copy
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import http.cookiejar
import json
import os
from pathlib import Path
import platform
import select
import shutil
import signal
import subprocess
import sys
import tarfile
import threading
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--release', required=True)
parser.add_argument('--previous-bundle', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
release = Path(args.release).resolve()
previous = Path(args.previous_bundle).resolve()
out = Path(args.out).resolve()
if out.exists():
    raise SystemExit('choose a new isolated fixture directory')
out.mkdir(parents=True, mode=0o700)
(out / 'ISOLATED-UPGRADE').write_text('Disposable client/server/version fixture. No production access.\n')
native = platform.system().lower() + '-' + {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine().lower()]
manifest = json.loads((release / 'manifest.json').read_text())
asset = next(item for item in manifest['assets'] if item['kind'] == 'client' and item['platform'] == native)
archive_path = release / asset['path']
assert hashlib.sha256(archive_path.read_bytes()).hexdigest() == asset['sha256']
with tarfile.open(archive_path, 'r:gz') as archive:
    member = archive.getmember('ael')
    assert member.isfile()
    binary = out / 'new-ael'
    binary.write_bytes(archive.extractfile(member).read())
    binary.chmod(0o755)
old_binary = previous / ('ael-' + native)
old_version = json.loads(subprocess.check_output([str(old_binary), 'version', '--json'], text=True))
assert old_version['version'] != manifest['version']
config = out / 'client-config'
cli_env = dict(os.environ, AEL_DATA_DIR=str(out / 'unused-runtime'), AEL_REMOTE_CONFIG_DIR=str(config), AEL_BUNDLE_DIR='', PATH='/usr/bin:/bin:/usr/sbin:/sbin', AEL_API_PROTOCOL='1', AEL_SKILL_PROTOCOL='1')
assert shutil.which('docker', path=cli_env['PATH']) is None
agent_config = out / 'host-config/agent-settings.toml'
agent_config.parent.mkdir(parents=True)
agent_config.write_text('# Fixture agent configuration remains owned by the host.\n')
agent_config_hash = hashlib.sha256(agent_config.read_bytes()).hexdigest()
events = []
secrets = ['fixture-admin-password-219', 'fixture-agent-password-219']

def sha(path):
    with Path(path).open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def cli(label, executable, *values, input_text=None, error=None, env=None, text=False):
    command = [str(executable), *map(str, values)]
    if not text:
        command.append('--json')
    result = subprocess.run(command, input=input_text, env=env or cli_env, cwd=out, text=True, capture_output=True, timeout=120)
    if error:
        assert result.returncode != 0 and error in result.stderr, (label, result.returncode, result.stderr)
        value = {'status': error}
    else:
        if result.returncode:
            message = result.stderr
            for secret in secrets:
                message = message.replace(secret, '<redacted>')
            raise RuntimeError(label + '\n' + message)
        value = result.stdout if text else json.loads(result.stdout)
    events.append({'case': label, 'passed': True, 'status': value.get('status') if isinstance(value, dict) else 'text'})
    return value


catalog = []
content = {}
source_counts = {'downloads': 0, 'incompatible_project_reads': 0}

class SourceHandler(BaseHTTPRequestHandler):
    def log_message(self, *values):
        pass

    def do_GET(self):
        if self.path == '/releases':
            body = json.dumps(catalog).encode()
        elif self.path == '/api/v1/meta':
            body = json.dumps({'mode': 'server', 'protocol': 2, 'skill_protocol': 2, 'capabilities': ['remote_tasks']}).encode()
        elif self.path.startswith('/api/v1/projects/'):
            source_counts['incompatible_project_reads'] += 1
            body = b'{"tasks":[]}'
        else:
            body = content.get(self.path)
            if body is None:
                self.send_error(404)
                return
            if self.path.endswith('.tar.gz'):
                source_counts['downloads'] += 1
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

source_server = ThreadingHTTPServer(('127.0.0.1', 0), SourceHandler)
source_origin = 'http://127.0.0.1:' + str(source_server.server_address[1])
source_thread = threading.Thread(target=source_server.serve_forever, daemon=True)
source_thread.start()
key = out / 'fixture-signing-key'
verifier = shutil.which('ssh-keygen')
subprocess.run([verifier, '-q', '-t', 'ed25519', '-N', '', '-f', str(key)], check=True, capture_output=True)
(out / 'allowed_signers').write_text('upgrade-fixture ' + Path(str(key) + '.pub').read_text())
source_file = out / 'source.json'
source_file.write_text(json.dumps({'releases_url': source_origin + '/releases', 'manifest_asset': 'manifest.json', 'signature_asset': 'manifest.sig', 'allowed_origins': [], 'trust': {'type': 'openssh', 'identity': 'upgrade-fixture', 'allowed_signers': 'allowed_signers', 'verifier': verifier}}))


def signed_catalog(source_manifest, directory, namespace):
    selected = copy.deepcopy(source_manifest)
    selected['dirty'], selected['publication'] = False, 'released'
    path = out / (namespace + '-manifest.json')
    path.write_text(json.dumps(selected))
    subprocess.run([verifier, '-Y', 'sign', '-f', str(key), '-n', 'ael-release', str(path)], check=True, capture_output=True)
    files = {'manifest.json': path.read_bytes(), 'manifest.sig': Path(str(path) + '.sig').read_bytes()}
    for item in selected['assets']:
        files[item['path']] = (directory / item['path']).read_bytes()
    assets = []
    for name, body in files.items():
        content['/' + namespace + '/' + name] = body
        assets.append({'name': name, 'browser_download_url': source_origin + '/' + namespace + '/' + name, 'size': len(body)})
    return {'tag_name': 'v' + selected['version'], 'draft': False, 'prerelease': False, 'assets': assets}

current_catalog = signed_catalog(manifest, release, 'current')
old_manifest = json.loads((previous / 'release/manifest.json').read_text())
old_catalog = signed_catalog(old_manifest, previous / 'release', 'previous')
catalog[:] = [current_catalog]
server = None
host = None
server_log = (out / 'server.log').open('w')
try:
    cli('bootstrap actual isolated server', binary, 'server', 'bootstrap', '--data-dir', out / 'server-data', '--username', 'owner', '--password-stdin', input_text=secrets[0] + '\n')
    server = subprocess.Popen([str(binary), 'server', 'start', '--data-dir', str(out / 'server-data'), '--addr', '127.0.0.1:0', '--json'], cwd=out, env=cli_env, text=True, stdout=subprocess.PIPE, stderr=server_log)
    ready, _, _ = select.select([server.stdout], [], [], 30)
    assert ready
    origin = json.loads(server.stdout.readline())['url']
    cookies = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
    csrf = None

    def api(path, body=None):
        headers = {'Origin': origin, 'X-AEL-Protocol': '1', 'Content-Type': 'application/json'}
        if csrf:
            headers['X-AEL-CSRF'] = csrf
        request = urllib.request.Request(origin + path, None if body is None else json.dumps(body).encode(), headers)
        try:
            with opener.open(request, timeout=15) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError('isolated API fixture rejected ' + path + ' with ' + str(error.code)) from None

    csrf = api('/api/v1/auth/login', {'username': 'owner', 'password': secrets[0]})['csrf']
    api('/api/v1/users', {'username': 'agent', 'password': secrets[1]})
    prefix = out / 'shared-install'
    cli('install actual old offline distribution', old_binary, 'upgrade', '--from', previous, '--prefix', prefix)
    installed = prefix / 'bin/ael'
    assert cli('old executable version', installed, 'version')['version'] == old_version['version']
    local_project = out / 'local-project'
    local_project.mkdir()
    local_env = dict(cli_env, AEL_DATA_DIR=str(out / 'local-runtime'), AEL_INSTALL_PREFIX=str(prefix))
    cli('initialize actual old Local project', installed, 'init', '--project', local_project, '--bundle-dir', previous, env=local_env)

    projects = []
    for suffix in ['a', 'b']:
        project_id = 'upgrade-' + suffix
        api('/api/v1/projects', {'id': project_id, 'name': 'Upgrade fixture ' + suffix})
        api('/api/v1/projects/' + project_id + '/members', {'username': 'agent', 'role': 'executor'})
        token = api('/api/v1/tokens', {'username': 'agent', 'project_id': project_id, 'label': 'upgrade fixture'})['token']
        secrets.append(token)
        project = out / ('project-' + suffix)
        project.mkdir()
        (project / 'AGENTS.md').write_text('User project instructions ' + suffix + '\n')
        cli('save private profile ' + suffix, binary, 'server', 'profile', 'add', '--name', suffix, '--url', origin, '--project', project, '--token-stdin', input_text=token + '\n')
        cli('connect with old shared CLI ' + suffix, installed, 'project', 'connect', '--profile', suffix, '--remote-project', project_id, '--project', project)
        cli('install explicit light entry ' + suffix, binary, 'update', '--project', project)
        project_env = dict(cli_env, AEL_INSTALL_PREFIX=str(prefix))
        result = cli('old shared CLI reads project ' + suffix, project / 'ael', 'task', 'list', env=project_env)
        assert result['online'] and result['authoritative']
        assert not (project / '.ael/bin').exists()
        projects.append((project, project_env))

    def project_hashes(project):
        paths = [project / 'ael', project / 'AGENTS.md', project / '.ael/remote.json', project / '.ael/remote-assets.json']
        paths.extend((project / '.agents/skills/ael-work').rglob('*.md'))
        return {str(path.relative_to(project)): sha(path) for path in paths}

    before_projects = [project_hashes(project) for project, _ in projects]
    snapshot = out / 'active-host-context.json'
    snapshot.write_text(json.dumps({'api_protocol': 1, 'skill_protocol': 1, 'entry_hashes': before_projects[0]}))
    snapshot_hash = sha(snapshot)
    host = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(300)'], cwd=out, env=cli_env)
    before_binary = sha(installed)
    check = cli('online check inspects selected old prefix', binary, 'upgrade', '--source', source_file, '--prefix', prefix, '--check')
    assert check['status'] == 'update_available'
    assert check['installation']['previous']['version'] == old_version['version']
    preview = cli('online dry-run preserves old tool', binary, 'upgrade', '--source', source_file, '--prefix', prefix, '--dry-run')
    assert preview['status'] == 'preview' and sha(installed) == before_binary and source_counts['downloads'] == 0
    missing = cli('exact unavailable version', binary, 'upgrade', '--source', source_file, '--version', '98.0.0', '--prefix', prefix, '--check')
    assert missing['status'] == 'no_available_version'
    result = cli('signed exact-version upgrade', binary, 'upgrade', '--source', source_file, '--version', manifest['version'], '--prefix', prefix)
    assert result['publisher_identity'] == 'verified' and result['content_integrity'] == 'verified'
    assert cli('new installed version', installed, 'version')['version'] == manifest['version']
    assert sha(installed) != before_binary and host.poll() is None and sha(snapshot) == snapshot_hash
    local_update = cli('new machine CLI retains default old Local source', installed, 'update', '--project', local_project, env=local_env)
    assert local_update['available']['version'] == old_version['version']
    old_entry_update = cli('old pinned Local entry retains its default source', local_project / 'ael', 'update', env=local_env)
    assert old_entry_update['available']['version'] == old_version['version']
    assert cli('old pinned Local executable remains unchanged', local_project / 'ael', 'version', env=local_env)['version'] == old_version['version']

    assert cli('old backup remains executable', Path(result['backup_dir']) / 'ael', 'version')['version'] == old_version['version']
    check = cli('online same version is up to date', binary, 'upgrade', '--source', source_file, '--prefix', prefix, '--check')
    assert check['status'] == 'up_to_date' and check['installation']['previous']['version'] == manifest['version']
    catalog[:] = [old_catalog]
    cli('online check refuses downgrade', binary, 'upgrade', '--source', source_file, '--prefix', prefix, '--check', error='DOWNGRADE_REFUSED')
    catalog[:] = [current_catalog]
    for index, (project, project_env) in enumerate(projects):
        assert project_hashes(project) == before_projects[index]
        result = cli('new shared CLI reads unchanged project ' + str(index), project / 'ael', 'task', 'list', env=project_env)
        assert result['online'] and result['authoritative']
        version = cli('CLI project and Server versions ' + str(index), project / 'ael', 'version', '--check', env=project_env)
        assert version['version'] == manifest['version'] and version['project_entry']['version'] == manifest['version'] and version['server']['version'] == manifest['version'] and version['compatible']
        readable = cli('human readable version ' + str(index), project / 'ael', 'version', '--check', env=project_env, text=True)
        assert manifest['version'] in readable and 'remote' in readable

    bad_env = dict(projects[0][1], AEL_SKILL_PROTOCOL='2')
    cli('active unsupported session stays explicit', projects[0][0] / 'ael', 'task', 'list', env=bad_env, error='CLIENT_SESSION_PROTOCOL_UNSUPPORTED')
    profile_file = config / 'profiles/a.json'
    original_profile = profile_file.read_text()
    bad_profile = json.loads(original_profile)
    bad_profile['url'] = source_origin
    profile_file.write_text(json.dumps(bad_profile))
    cli('incompatible Server GET is rejected', projects[0][0] / 'ael', 'task', 'list', env=projects[0][1], error='REMOTE_PROTOCOL_UNSUPPORTED')
    assert source_counts['incompatible_project_reads'] == 0
    profile_file.write_text(original_profile)

    interrupted_prefix = out / 'interrupted-install'
    cli('prepare real interruption old tool', old_binary, 'upgrade', '--from', previous, '--prefix', interrupted_prefix)
    log = (out / 'interrupted-upgrade.log').open('w')
    updater = subprocess.Popen([str(binary), 'upgrade', '--from', str(archive_path), '--prefix', str(interrupted_prefix), '--json'], cwd=out, env=cli_env, stdout=log, stderr=log)
    owner_file = interrupted_prefix / 'share/ael/upgrade.lock/owner.json'
    caught = False
    try:
        for attempt in range(10000):
            if updater.poll() is not None:
                break
            if owner_file.exists():
                try:
                    owner = json.loads(owner_file.read_text())
                except (OSError, json.JSONDecodeError):
                    time.sleep(0.001)
                    continue
                if owner['pid'] == updater.pid:
                    os.kill(updater.pid, signal.SIGSTOP)
                    caught = True
                    break
            time.sleep(0.001)
        assert caught, 'real updater completed before interruption could be observed'
        updater.kill()
        updater.wait(timeout=10)
    finally:
        if updater.poll() is None:
            updater.kill()
            updater.wait(timeout=10)
        log.close()
    assert owner_file.exists()
    interrupted_version = cli('killed updater target stays executable', interrupted_prefix / 'bin/ael', 'version')['version']
    assert interrupted_version in [old_version['version'], manifest['version']]
    old_cached_binary = interrupted_prefix / 'share/ael/distribution' / old_version['version'] / ('ael-' + native)
    assert cli('killed updater keeps old cached tool usable', old_cached_binary, 'version')['version'] == old_version['version']
    cli('unacknowledged stale updater is refused', binary, 'upgrade', '--from', archive_path, '--prefix', interrupted_prefix, error='UPDATE_BUSY')
    cli('actual killed updater recovers exact source', binary, 'upgrade', '--from', archive_path, '--prefix', interrupted_prefix, '--ack-stopped')
    assert cli('interrupted target is usable', interrupted_prefix / 'bin/ael', 'version')['version'] == manifest['version']

    permission_prefix = out / 'permission-install'
    cli('prepare permission fixture old tool', old_binary, 'upgrade', '--from', previous, '--prefix', permission_prefix)
    permission_binary = permission_prefix / 'bin/ael'
    old_hash = sha(permission_binary)
    (permission_prefix / 'bin').chmod(0o500)
    try:
        cli('real permission failure', binary, 'upgrade', '--from', archive_path, '--prefix', permission_prefix, error='permission denied')
        assert sha(permission_binary) == old_hash
        assert cli('permission failure retains old executable', permission_binary, 'version')['version'] == old_version['version']
    finally:
        (permission_prefix / 'bin').chmod(0o700)
    cli('permission restored exact-source retry', binary, 'upgrade', '--from', archive_path, '--prefix', permission_prefix)
    malformed = out / 'tampered-client.tar.gz'
    malformed.write_bytes(b'tampered archive')
    current_hash = sha(installed)
    cli('tampered package rejected', binary, 'upgrade', '--from', malformed, '--prefix', prefix, error='CLIENT_PACKAGE_INVALID')
    assert sha(installed) == current_hash
    entry = projects[0][0] / 'ael'
    entry.write_text('user edited entry\n')
    cli('user entry conflict preserved', binary, 'update', '--project', projects[0][0], error='ASSET_CONFLICT')
    assert entry.read_text() == 'user edited entry\n'
    assert agent_config_hash == sha(agent_config)
    assert (prefix / 'share/ael/distribution' / old_version['version']).is_dir()
    assert not list((out / 'unused-runtime').rglob('runtime.db'))
    result = {'result': 'PASS', 'mode': 'same-machine isolated real HTTP and signed source', 'previous': old_version, 'current_version': manifest['version'], 'current_build': manifest['build'], 'client_without_docker': True, 'two_projects_share_machine_cli': True, 'project_entries_unchanged_by_machine_upgrade': True, 'active_external_host_preserved': True, 'external_host_is_fixture': True, 'real_updater_killed_and_recovered': True, 'real_permission_failure_kept_old_tool': True, 'old_offline_payload_retained': True, 'old_local_default_update_retained': True, 'real_release_or_production_access': False, 'events': events}
    (out / 'summary.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))
finally:
    if host is not None and host.poll() is None:
        host.terminate()
        host.wait(timeout=10)
    if server is not None and server.poll() is None:
        server.terminate()
        server.wait(timeout=10)
    server_log.close()
    source_server.shutdown()
    source_server.server_close()
    source_thread.join(timeout=5)
    for path in [key, Path(str(key) + '.pub')]:
        if path.exists():
            path.unlink()

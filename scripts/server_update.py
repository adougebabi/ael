#!/usr/bin/env python3
"""Run one signed, idle-only Server image update with recoverable maintenance."""
import argparse
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import subprocess
import uuid
import http.cookiejar
import urllib.error
import urllib.parse
import urllib.request


def atomic_file(path, content):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = path.with_name('.' + path.name + '-' + uuid.uuid4().hex)
    try:
        with temporary.open('xb') as file:
            temporary.chmod(0o600)
            file.write(content)
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, path)
        descriptor = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        temporary.unlink(missing_ok=True)


def image_environment(content, image):
    text = content.decode()
    text, count = re.subn(r'^AEL_IMAGE=.*$', 'AEL_IMAGE=' + image, text, flags=re.MULTILINE)
    if count != 1:
        raise RuntimeError('deployment must contain exactly one AEL_IMAGE entry')
    return text.encode()


def version_tuple(version):
    match = re.fullmatch(r'(\d+)\.(\d+)\.(\d+)(?:[-+].*)?', version)
    if not match:
        raise RuntimeError('Server version is not valid SemVer')
    return tuple(map(int, match.groups()))


class AdminRequestError(RuntimeError):
    def __init__(self, status, code):
        self.status = status
        super().__init__('maintenance HTTP ' + str(status) + ' ' + code)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, url):
        return None


class ServerUpdater:
    def __init__(self, configuration):
        self.config = configuration
        origin = urllib.parse.urlsplit(configuration['origin'])
        if origin.scheme != 'https' or not origin.hostname or origin.username or origin.password or origin.path or origin.query or origin.fragment:
            raise ValueError('provide the exact HTTPS Server origin')
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies), NoRedirect())
        self.csrf = ''
        if configuration.get('schema') != 1:
            raise ValueError('unsupported updater configuration schema')
        for key in ['work_dir', 'env_file', 'compose_file', 'data_dir', 'cli', 'source', 'verifier', 'password_file']:
            if not Path(configuration[key]).is_absolute():
                raise ValueError(key + ' must be an absolute path')
        if not re.fullmatch(r'ghcr\.io/[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*', configuration['image']):
            raise ValueError('an explicit GHCR image repository is required')
        password = Path(configuration['password_file'])
        information = password.lstat()
        if not stat.S_ISREG(information.st_mode) or information.st_mode & 0o077:
            raise ValueError('administrator password file must be private and regular')
        self.work = Path(configuration['work_dir'])
        self.work.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.journal = self.work / 'pending.json'
        self.state = self.work / 'status.json'
        self.environment = Path(configuration['env_file'])
        self.compose = ['docker', 'compose', '--project-directory', str(self.environment.parent),
                        '--env-file', str(self.environment), '-f', configuration['compose_file']]

    def command(self, arguments, input_text=None, timeout=180):
        result = subprocess.run(arguments, input=input_text, capture_output=True, text=True, timeout=timeout)
        if result.returncode:
            raise RuntimeError(Path(arguments[0]).name + ' failed: ' + result.stderr[-2000:])
        return result.stdout

    def json_command(self, arguments, input_text=None, timeout=180):
        return json.loads(self.command(arguments, input_text, timeout))

    def record(self, value):
        value = dict(value, checked_at=datetime.now(timezone.utc).isoformat())
        atomic_file(self.state, (json.dumps(value, indent=2) + '\n').encode())
        return value

    def phase(self, journal, phase):
        journal['phase'] = phase
        atomic_file(self.journal, (json.dumps(journal, indent=2) + '\n').encode())

    def api_json(self, method, path, value=None):
        headers = {'Origin': self.config['origin'], 'X-AEL-Protocol': '1',
                   'User-Agent': 'Go-http-client/1.1', 'Accept': 'application/json'}
        if self.csrf:
            headers['X-AEL-CSRF'] = self.csrf
        data = None
        if value is not None:
            headers['Content-Type'] = 'application/json'
            data = json.dumps(value).encode()
        request = urllib.request.Request(self.config['origin'] + path, data=data, method=method, headers=headers)
        try:
            with self.opener.open(request, timeout=30) as response:
                raw = response.read((1 << 20) + 1)
            if len(raw) > 1 << 20:
                raise RuntimeError('maintenance response exceeds its limit')
            return json.loads(raw)
        except urllib.error.HTTPError as error:
            try:
                code = json.loads(error.read(4096)).get('code', '')
            except ValueError:
                code = ''
            if not re.fullmatch(r'[A-Z][A-Z0-9_]{0,63}', code):
                code = 'SERVER_ADMIN_REJECTED'
            raise AdminRequestError(error.code, code) from None
        except urllib.error.URLError as error:
            raise RuntimeError('maintenance connection unavailable') from error

    def login(self):
        self.csrf = self.api_json('POST', '/api/v1/auth/login', {
            'username': self.config['username'], 'password': Path(self.config['password_file']).read_text().rstrip('\r\n')})['csrf']
        if not self.csrf:
            raise RuntimeError('maintenance login omitted its CSRF binding')

    def close_session(self):
        if self.csrf:
            try:
                self.api_json('POST', '/api/v1/auth/logout', {})
            except (RuntimeError, OSError, ValueError):
                pass
        self.csrf = ''
        self.cookies.clear()

    def maintenance(self, action, journal=None, expected=None):
        body = None
        if journal:
            operation = journal.get('recovery_operation', journal['operation'])
            key = operation + '-' + action
            requests = journal.setdefault('request_states', {})
            if expected is not None and key not in requests:
                revision = expected.get('revision', '')
                if re.fullmatch(r'[0-9a-f]{64}', revision):
                    requests[key] = revision
                    self.phase(journal, journal['phase'])
                elif not journal.get('legacy_bootstrap'):
                    raise RuntimeError('conditional maintenance state is unavailable')
            body = {'action': action, 'reason': journal['reason'], 'request_key': key}
            if key in requests:
                body['expected_state'] = requests[key]
            if action == 'drain':
                body['deadline'] = journal.get('recovery_deadline', journal['deadline'])
        if not self.csrf:
            self.login()
        method = 'GET' if action == 'status' else 'POST'
        try:
            result = self.api_json(method, '/api/v1/admin/maintenance', body)
        except AdminRequestError as error:
            if error.status != 401:
                raise
            self.csrf = ''
            self.cookies.clear()
            self.login()
            result = self.api_json(method, '/api/v1/admin/maintenance', body)
        state = result.get('current', result)
        if result.get('current_revision'):
            state['revision'] = result['current_revision']
        return state

    def durable_maintenance(self):
        path = Path(self.config['data_dir']) / 'runtime.db'
        try:
            with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True) as database:
                row = database.execute("SELECT body FROM metadata WHERE key='server:maintenance'").fetchone()
            if row:
                return json.loads(row[0])
        except (sqlite3.Error, OSError, ValueError) as error:
            raise RuntimeError('authoritative maintenance state is unavailable') from error
        raise RuntimeError('authoritative maintenance state is unavailable')

    def owned_maintenance(self, journal, allow_durable=False):
        try:
            state = self.maintenance('status')
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError):
            if not allow_durable:
                raise
            state = self.durable_maintenance()
            if state.get('mode') != 'quiesced':
                raise RuntimeError('maintenance API is unavailable and no quiesced barrier is proven')
        if state['mode'] != 'active' and state.get('reason') != journal['reason']:
            raise RuntimeError('another administrator owns the maintenance state')
        return state

    def running_version(self):
        return self.json_command(['docker', 'exec', self.config['container'], '/ael', 'version', '--json'])

    def health(self, version, build, public=True):
        running = self.running_version()
        if running['version'] != version or running['build'] != build:
            raise RuntimeError('running Server does not match the selected version and build')
        health = self.json_command(['docker', 'exec', self.config['container'], '/ael', 'server', 'health',
                                    '--url', 'http://127.0.0.1:7332', '--json'])
        if health.get('status') != 'healthy':
            raise RuntimeError('private Server health failed')
        if public:
            metadata = self.json_command(['curl', '-fsS', '--max-time', '20', self.config['origin'] + '/api/v1/meta'])
            if metadata['version'] != version or metadata['build'] != build:
                raise RuntimeError('public origin does not match the running Server')

    def active_responsibilities(self):
        now = datetime.now(timezone.utc)
        active = 0
        for path in Path(self.config['data_dir']).glob('projects/*/runtime/runtime.db'):
            with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True) as database:
                rows = database.execute('SELECT body FROM tasks').fetchall()
            for (body,) in rows:
                task = json.loads(body)
                if task['status'] != 'open' or task.get('paused'):
                    continue
                for attempt in task.get('attempts', []):
                    if attempt['status'] != 'running':
                        continue
                    lease = datetime.fromisoformat(attempt['lease_until'].replace('Z', '+00:00'))
                    if lease > now and attempt.get('definition_revision') == task['definition'].get('definition_revision') and attempt.get('plan_revision') == task['plan'].get('revision'):
                        active += 1
        return active

    def restart(self):
        self.command(self.compose + ['up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--wait', 'server'], timeout=180)

    def saved_environment(self, journal):
        original = Path(journal['env_backup']).read_bytes()
        if hashlib.sha256(original).hexdigest() != journal['old_env_sha256']:
            raise RuntimeError('saved deployment configuration changed')
        return original

    def replace_environment(self, journal, image):
        original = self.saved_environment(journal)
        replacement = image_environment(original, image)
        rollback = image_environment(original, journal['old_image'])
        allowed = {journal['old_env_sha256'], journal['new_env_sha256'], hashlib.sha256(rollback).hexdigest()}
        if hashlib.sha256(self.environment.read_bytes()).hexdigest() not in allowed:
            raise RuntimeError('deployment configuration changed outside this update')
        atomic_file(self.environment, replacement)

    def recover(self, journal):
        state = self.owned_maintenance(journal, allow_durable=True)
        try:
            running = self.running_version()
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError):
            running = {}
        environment_hash = hashlib.sha256(self.environment.read_bytes()).hexdigest()
        rollback_hash = hashlib.sha256(image_environment(self.saved_environment(journal), journal['old_image'])).hexdigest()
        original_environment = environment_hash in {journal['old_env_sha256'], rollback_hash}
        if state['mode'] == 'active' and environment_hash == journal['new_env_sha256'] and running.get('version') == journal['version'] and running.get('build') == journal['build']:
            try:
                self.health(journal['version'], journal['build'])
            except (RuntimeError, OSError, ValueError, subprocess.SubprocessError):
                pass
            else:
                self.journal.unlink()
                return self.record({'status': 'recovered_updated', 'version': journal['version'], 'image': journal['image']})
        old_running = running.get('version') == journal['old_version'] and running.get('build') == journal['old_build']
        if state['mode'] == 'active' and old_running and original_environment:
            self.health(journal['old_version'], journal['old_build'], public=False)
            self.journal.unlink()
            return self.record({'status': 'recovered_original', 'version': journal['old_version'],
                                'error': journal.get('error', 'interrupted before deployment switch')})
        if state['mode'] == 'active':
            journal['recovery_operation'] = journal['operation'] + '-r-' + uuid.uuid4().hex[:6]
            journal['recovery_deadline'] = (datetime.now(timezone.utc) + timedelta(minutes=2)).isoformat()
            self.phase(journal, 'recovery_drain_requested')
            state = self.maintenance('drain', journal, expected=state)
        if state['mode'] == 'draining':
            if self.active_responsibilities():
                if self.maintenance('resume', journal, expected=state)['mode'] != 'active':
                    raise RuntimeError('recovery could not preserve active responsibilities')
                return self.record({'status': 'deferred_recovery_active', 'version': running.get('version')})
            state = self.maintenance('quiesce', journal, expected=state)
        elif state['mode'] == 'quiescing':
            state = self.maintenance('quiesce', journal)
        if state['mode'] != 'quiesced' or state.get('reason') != journal['reason']:
            raise RuntimeError('recovery requires its own quiesced maintenance barrier')
        barrier = self.owned_maintenance(journal, allow_durable=True)
        if barrier['mode'] != 'quiesced':
            raise RuntimeError('maintenance barrier changed before rollback')
        if not old_running or not original_environment:
            self.replace_environment(journal, journal['old_image'])
            self.restart()
        self.health(journal['old_version'], journal['old_build'], public=False)
        state = self.owned_maintenance(journal)
        if state['mode'] != 'quiesced':
            raise RuntimeError('maintenance barrier changed during rollback')
        if self.maintenance('resume', journal, expected=state)['mode'] != 'active':
            raise RuntimeError('old Server maintenance recovery is incomplete')
        self.journal.unlink()
        return self.record({'status': 'rolled_back', 'version': journal['old_version'],
                            'image': journal['old_image'], 'backup': journal.get('backup'),
                            'error': journal.get('error', 'recovered an interrupted update')})

    def update(self):
        if self.journal.exists():
            return self.recover(json.loads(self.journal.read_text()))
        current = self.running_version()
        selected = self.json_command([self.config['cli'], 'release', 'check', '--source', self.config['source'],
                                      '--kind', 'server_image', '--platform', self.config['platform'], '--json'], timeout=90)
        if not selected['available']:
            return self.record({'status': selected['status'], 'version': current['version']})
        manifest = selected['manifest']
        if version_tuple(current['version']) > version_tuple(manifest['version']):
            return self.record({'status': 'ahead_of_stable', 'version': current['version']})
        if current['version'] == manifest['version']:
            if current['build'] != manifest['build']:
                raise RuntimeError('same-version Server bytes differ from the signed release')
            if re.search(r'^AEL_IMAGE=' + re.escape(self.config['image']) + '@sha256:', self.environment.read_text(), re.MULTILINE):
                return self.record({'status': 'up_to_date', 'version': current['version']})
        state = self.maintenance('status')
        if not re.fullmatch(r'[0-9a-f]{64}', state.get('revision', '')) and not self.config.get('legacy_bootstrap'):
            return self.record({'status': 'deferred_initial_setup', 'version': current['version']})
        if state['mode'] != 'active':
            return self.record({'status': 'deferred_existing_maintenance', 'version': current['version']})
        active = self.active_responsibilities()
        if active:
            return self.record({'status': 'deferred_active_responsibilities', 'active': active, 'version': current['version']})
        target = self.config['image'] + ':' + manifest['version'].replace('+', '_')
        signatures = self.json_command([self.config['verifier'], 'verify', '--certificate-identity', self.config['identity'],
                                        '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com', '--output', 'json', target], timeout=120)
        digests = {signature['critical']['image']['docker-manifest-digest'] for signature in signatures}
        if len(digests) != 1 or not re.fullmatch(r'sha256:[0-9a-f]{64}', next(iter(digests))):
            raise RuntimeError('registry image has conflicting signature digests')
        image = self.config['image'] + '@' + next(iter(digests))
        self.command(['docker', 'pull', image], timeout=300)
        candidate = self.json_command(['docker', 'run', '--rm', '--network', 'none', '--read-only', '--entrypoint', '/ael', image, 'version', '--json'])
        marker = (Path(self.config['data_dir']) / 'ael-server.marker').read_text().strip()
        match = re.fullmatch(r'AEL-STANDALONE-(\d+)', marker)
        if not match or candidate.get('server_data_format') != int(match[1]):
            return self.record({'status': 'deferred_storage_migration', 'version': current['version'], 'target': manifest['version']})
        if candidate['version'] != manifest['version'] or candidate['build'] != manifest['build']:
            raise RuntimeError('signed registry image differs from the signed release')
        old_image = self.command(['docker', 'inspect', '--format', '{{.Image}}', self.config['container']]).strip()
        if not re.fullmatch(r'sha256:[0-9a-f]{64}', old_image):
            raise RuntimeError('running image identity is unavailable')
        state = self.maintenance('status')
        if state['mode'] != 'active':
            return self.record({'status': 'deferred_existing_maintenance', 'version': current['version']})
        operation = 'auto-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + uuid.uuid4().hex[:8]
        folder = self.work / operation
        folder.mkdir(mode=0o700)
        original = self.environment.read_bytes()
        atomic_file(folder / 'env.before', original)
        journal = {'operation': operation, 'legacy_bootstrap': bool(self.config.get('legacy_bootstrap')), 'reason': 'scheduled registry update ' + operation,
                   'deadline': (datetime.now(timezone.utc) + timedelta(minutes=2)).isoformat(),
                   'old_version': current['version'], 'old_build': current['build'], 'old_image': old_image,
                   'version': manifest['version'], 'build': manifest['build'], 'image': image,
                   'env_backup': str(folder / 'env.before'), 'old_env_sha256': hashlib.sha256(original).hexdigest(),
                   'new_env_sha256': hashlib.sha256(image_environment(original, image)).hexdigest(),
                   'backup': '/backup/' + operation}
        self.phase(journal, 'drain_requested')
        try:
            state = self.maintenance('drain', journal, expected=state)
            if state['mode'] != 'draining' or state.get('reason') != journal['reason']:
                raise RuntimeError('conditional drain did not establish update ownership')
            active = self.active_responsibilities()
            if active:
                if self.maintenance('resume', journal, expected=state)['mode'] != 'active':
                    raise RuntimeError('could not resume after responsibility race')
                self.journal.unlink()
                return self.record({'status': 'deferred_active_responsibilities', 'active': active, 'version': current['version']})
            self.phase(journal, 'quiesce_requested')
            state = self.maintenance('quiesce', journal, expected=state)
            if state['mode'] != 'quiesced' or state.get('reason') != journal['reason']:
                raise RuntimeError('maintenance did not reach its consistent backup point')
            self.phase(journal, 'backup_requested')
            self.json_command(self.compose + ['run', '--rm', '--no-deps', '-T', 'operator', 'server', 'backup',
                                             '--data-dir', '/data', '--out', journal['backup'], '--json'], timeout=300)
            self.json_command(self.compose + ['run', '--rm', '--no-deps', '-T', 'operator', 'server', 'verify-backup',
                                             '--from', journal['backup'], '--json'], timeout=300)
            state = self.owned_maintenance(journal)
            if state['mode'] != 'quiesced':
                raise RuntimeError('maintenance changed before image switch')
            self.phase(journal, 'switch_requested')
            self.replace_environment(journal, image)
            self.restart()
            self.health(journal['version'], journal['build'])
            self.phase(journal, 'validated')
            state = self.owned_maintenance(journal)
            if state['mode'] != 'quiesced':
                raise RuntimeError('maintenance changed before resume')
            if self.maintenance('resume', journal, expected=state)['mode'] != 'active':
                raise RuntimeError('new Server did not resume accepting work')
            self.journal.unlink()
            return self.record({'status': 'updated', 'previous': current['version'], 'version': journal['version'],
                                'build': journal['build'], 'image': image, 'backup': journal['backup']})
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
            journal['error'] = str(error)
            self.phase(journal, journal['phase'])
            return self.recover(journal)

    def run(self):
        with (self.work / 'update.lock').open('a') as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                return {'status': 'another_check_running'}
            try:
                return self.update()
            finally:
                self.close_session()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, type=Path)
    parser.add_argument('--bootstrap-legacy', action='store_true', help='one coordinated initial update from a pre-conditional Server')
    args = parser.parse_args()
    configuration = json.loads(args.config.read_text())
    configuration['legacy_bootstrap'] = args.bootstrap_legacy
    updater = ServerUpdater(configuration)
    try:
        print(json.dumps(updater.run()))
    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(json.dumps(updater.record({'status': 'failed', 'error': str(error)})))
        raise SystemExit(1)


if __name__ == '__main__':
    main()

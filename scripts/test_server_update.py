"""Deterministic checks for the finite deployment controller and crash recovery."""
import importlib.util
from datetime import datetime, timedelta, timezone
import sqlite3
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('server_update', Path(__file__).with_name('server_update.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class FixtureUpdater(module.ServerUpdater):
    def __init__(self, root):
        config = {'schema': 1, 'work_dir': str(root / 'updates'), 'env_file': str(root / '.env'),
                  'compose_file': str(root / 'compose.yaml'), 'data_dir': str(root / 'data'),
                  'cli': str(root / 'ael'), 'source': str(root / 'source.json'),
                  'verifier': str(root / 'cosign'), 'password_file': str(root / 'password'),
                  'origin': 'https://server.test', 'username': 'owner', 'container': 'server',
                  'image': 'ghcr.io/fixture/ael-server', 'platform': 'linux-arm64',
                  'identity': 'https://github.com/fixture/ael/.github/workflows/release.yml@refs/heads/main'}
        Path(config['env_file']).write_text('AEL_IMAGE=fixture:old\nAEL_PUBLIC_ORIGIN=https://server.test\n')
        Path(config['password_file']).write_text('fixture-password\n')
        Path(config['password_file']).chmod(0o600)
        Path(config['data_dir']).mkdir()
        (Path(config['data_dir']) / 'ael-server.marker').write_text('AEL-STANDALONE-2\n')
        super().__init__(config)
        self.old = {'version': '0.1.28', 'build': 'old'}
        self.new = {'version': '0.1.29', 'build': 'new', 'server_data_format': 2}
        self.current = self.old
        self.mode = 'active'
        self.reason = ''
        self.actions = []
        self.commands = []
        self.active_checks = iter([0, 0])
        self.fail_new_health = False
        self.signature_bad = False
        self.restarter_crashes = False
        self.quiesce_crashes = False
        self.resume_crashes = False
        self.status_unavailable = False
        self.external_drain_on_pull = False
        self.race_on_drain = False
        self.legacy_server = False

    def active_responsibilities(self):
        return next(self.active_checks, 0)

    def command(self, arguments, input_text=None, timeout=180):
        self.commands.append(arguments)
        if arguments[:2] == ['docker', 'pull'] and self.external_drain_on_pull:
            self.mode, self.reason = 'draining', 'external administrator'
        if arguments[:2] == ['docker', 'inspect']:
            return 'sha256:' + 'a' * 64
        return ''

    def json_command(self, arguments, input_text=None, timeout=180):
        self.commands.append(arguments)
        if arguments[0] == self.config['cli']:
            if arguments[1:3] == ['release', 'check']:
                return {'available': True, 'manifest': self.new}
        if arguments[0] == self.config['verifier']:
            if self.signature_bad:
                raise RuntimeError('fixture invalid signature')
            return [{'critical': {'image': {'docker-manifest-digest': 'sha256:' + 'b' * 64}}}]
        if arguments[:2] == ['docker', 'run']:
            return self.new
        if arguments[:2] == ['docker', 'exec']:
            if 'health' in arguments:
                if self.current is self.new and self.fail_new_health:
                    raise RuntimeError('fixture unhealthy new Server')
                return {'status': 'healthy'}
            return self.current
        if arguments[0] == 'curl':
            return self.current
        return {'status': 'verified'}

    def api_json(self, method, path, value=None):
        if path.endswith('/auth/login'):
            self.assert_password(value['password'] + '\n')
            return {'csrf': 'fixture-csrf'}
        if path.endswith('/auth/logout'):
            return {}
        action = 'status' if method == 'GET' else value['action']
        arguments = [self.config['cli'], 'server', 'maintenance', '--action', action]
        if value:
            arguments.extend(['--request-key', value['request_key'], '--reason', value['reason']])
            if value.get('expected_state'):
                arguments.extend(['--expected-state', value['expected_state']])
        self.commands.append(arguments)
        legacy = self.legacy_server and self.current is self.old
        if legacy and value and value.get('expected_state'):
            raise RuntimeError('legacy Server rejected conditional input')
        if action == 'status' and self.status_unavailable:
            raise RuntimeError('status API unavailable fixture')
        if action == 'drain' and self.race_on_drain:
            self.mode, self.reason = 'draining', 'external administrator'
        revision = self.state_revision()
        if action != 'status':
            if value.get('expected_state') and value['expected_state'] != revision and not (action == 'quiesce' and self.mode == 'quiescing'):
                raise RuntimeError('conditional state conflict fixture')
            self.actions.append((action, value['request_key']))
            self.reason = value['reason']
            self.mode = {'drain': 'draining', 'quiesce': 'quiesced', 'resume': 'active'}[action]
            if action == 'quiesce' and self.quiesce_crashes:
                self.mode = 'quiescing'
                raise KeyboardInterrupt('partial quiesce fixture')
            if action == 'resume' and self.resume_crashes:
                raise KeyboardInterrupt('resume completed before caller saved result')
        result = {'current': {'mode': self.mode, 'reason': self.reason}}
        if not legacy:
            result['current_revision'] = self.state_revision()
        return result

    def state_revision(self):
        return hashlib.sha256((self.mode + self.reason + str(len(self.actions))).encode()).hexdigest()

    @staticmethod
    def assert_password(value):
        if value != 'fixture-password\n':
            raise AssertionError('password did not use stdin')

    def restart(self):
        if self.restarter_crashes:
            raise KeyboardInterrupt('simulated process termination')
        self.commands.append(['restart'])
        self.current = self.new if 'ghcr.io/' in self.environment.read_text() else self.old


class ServerUpdateTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.updater = FixtureUpdater(Path(self.directory.name))

    def test_update_verifies_backups_pins_digest_and_resumes(self):
        result = self.updater.run()
        self.assertEqual(result['status'], 'updated')
        self.assertEqual(self.updater.mode, 'active')
        self.assertEqual([a for a, _ in self.updater.actions], ['drain', 'quiesce', 'resume'])
        self.assertIn('@sha256:' + 'b' * 64, self.updater.environment.read_text())
        self.assertFalse(self.updater.journal.exists())
        self.assertTrue(any('backup' in command for command in self.updater.commands))
        self.assertTrue(any('verify-backup' in command for command in self.updater.commands))

    def test_active_responsibility_defers_without_maintenance(self):
        self.updater.active_checks = iter([1])
        self.assertEqual(self.updater.run()['status'], 'deferred_active_responsibilities')
        self.assertEqual(self.updater.actions, [])

    def test_claim_race_after_drain_resumes_without_switch(self):
        self.updater.active_checks = iter([0, 1])
        self.assertEqual(self.updater.run()['status'], 'deferred_active_responsibilities')
        self.assertEqual([a for a, _ in self.updater.actions], ['drain', 'resume'])
        self.assertIn('fixture:old', self.updater.environment.read_text())

    def test_bad_signature_leaves_server_active(self):
        self.updater.signature_bad = True
        with self.assertRaises(RuntimeError):
            self.updater.run()
        self.assertEqual(self.updater.actions, [])
        self.assertIn('fixture:old', self.updater.environment.read_text())

    def test_format_change_defers_before_maintenance(self):
        self.updater.new['server_data_format'] = 3
        self.assertEqual(self.updater.run()['status'], 'deferred_storage_migration')
        self.assertEqual(self.updater.actions, [])

    def test_unhealthy_candidate_rolls_back_exact_old_image(self):
        self.updater.fail_new_health = True
        result = self.updater.run()
        self.assertEqual(result['status'], 'rolled_back')
        self.assertEqual(self.updater.mode, 'active')
        self.assertIn('AEL_IMAGE=sha256:' + 'a' * 64, self.updater.environment.read_text())
        self.assertFalse(self.updater.journal.exists())

    def test_interrupted_switch_recovers_old_server(self):
        self.updater.restarter_crashes = True
        with self.assertRaises(KeyboardInterrupt):
            self.updater.run()
        self.assertTrue(self.updater.journal.exists())
        self.updater.restarter_crashes = False
        self.assertEqual(self.updater.run()['status'], 'rolled_back')
        self.assertEqual(self.updater.mode, 'active')

    def test_completed_version_is_noop(self):
        self.updater.run()
        actions = len(self.updater.actions)
        self.assertEqual(self.updater.run()['status'], 'up_to_date')
        self.assertEqual(len(self.updater.actions), actions)

    def test_partial_quiesce_replays_original_request_before_resume(self):
        self.updater.quiesce_crashes = True
        with self.assertRaises(KeyboardInterrupt):
            self.updater.run()
        original = next(key for action, key in self.updater.actions if action == 'quiesce')
        self.updater.quiesce_crashes = False
        self.assertEqual(self.updater.run()['status'], 'rolled_back')
        replay = [key for action, key in self.updater.actions if action == 'quiesce']
        self.assertEqual(replay, [original, original])
        self.assertNotIn(['restart'], self.updater.commands)

    def test_completed_resume_does_not_restart_new_active_server(self):
        self.updater.resume_crashes = True
        with self.assertRaises(KeyboardInterrupt):
            self.updater.run()
        restarts = self.updater.commands.count(['restart'])
        self.updater.resume_crashes = False
        self.assertEqual(self.updater.run()['status'], 'recovered_updated')
        self.assertEqual(self.updater.commands.count(['restart']), restarts)
        self.assertEqual(self.updater.mode, 'active')

    def test_external_configuration_change_is_preserved(self):
        self.updater.restarter_crashes = True
        with self.assertRaises(KeyboardInterrupt):
            self.updater.run()
        changed = b'AEL_IMAGE=operator:manual\n'
        self.updater.environment.write_bytes(changed)
        self.updater.restarter_crashes = False
        with self.assertRaisesRegex(RuntimeError, 'configuration changed'):
            self.updater.run()
        self.assertEqual(self.updater.environment.read_bytes(), changed)

    def test_expired_or_stale_attempts_are_not_effective_responsibilities(self):
        directory = Path(self.updater.config['data_dir']) / 'projects/fixture/runtime'
        directory.mkdir(parents=True)
        database = sqlite3.connect(directory / 'runtime.db')
        self.addCleanup(database.close)
        database.execute('CREATE TABLE tasks(body TEXT)')
        task = {'status': 'open', 'paused': False, 'definition': {'definition_revision': 1},
                'plan': {'revision': 1}, 'attempts': [{'status': 'running', 'definition_revision': 1,
                'plan_revision': 1, 'lease_until': (datetime.now(timezone.utc) - timedelta(hours=1)).isoformat()}]}
        database.execute('INSERT INTO tasks VALUES(?)', (json.dumps(task),))
        database.commit()
        self.assertEqual(module.ServerUpdater.active_responsibilities(self.updater), 0)
        task['attempts'][0]['lease_until'] = (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()
        database.execute('UPDATE tasks SET body=?', (json.dumps(task),))
        database.commit()
        self.assertEqual(module.ServerUpdater.active_responsibilities(self.updater), 1)
        task['attempts'][0]['definition_revision'] = 0
        database.execute('UPDATE tasks SET body=?', (json.dumps(task),))
        database.commit()
        self.assertEqual(module.ServerUpdater.active_responsibilities(self.updater), 0)

    def interrupted_switch(self):
        self.updater.restarter_crashes = True
        with self.assertRaises(KeyboardInterrupt):
            self.updater.run()
        self.updater.restarter_crashes = False

    def test_unknown_maintenance_state_does_not_reconfigure_or_restart(self):
        self.interrupted_switch()
        original = self.updater.environment.read_bytes()
        self.updater.status_unavailable = True
        with self.assertRaisesRegex(RuntimeError, 'maintenance state is unavailable'):
            self.updater.run()
        self.assertEqual(self.updater.environment.read_bytes(), original)
        self.assertNotIn(['restart'], self.updater.commands)

    def test_external_maintenance_is_checked_before_recovery_mutation(self):
        self.interrupted_switch()
        original = self.updater.environment.read_bytes()
        self.updater.reason = 'external administrator'
        with self.assertRaisesRegex(RuntimeError, 'another administrator'):
            self.updater.run()
        self.assertEqual(self.updater.environment.read_bytes(), original)
        self.assertNotIn(['restart'], self.updater.commands)

    def test_external_drain_during_pull_is_not_overwritten(self):
        self.updater.external_drain_on_pull = True
        self.assertEqual(self.updater.run()['status'], 'deferred_existing_maintenance')
        self.assertEqual(self.updater.actions, [])

    def test_conditional_drain_rejects_operator_race(self):
        self.updater.race_on_drain = True
        with self.assertRaisesRegex(RuntimeError, 'another administrator'):
            self.updater.run()
        self.assertEqual(self.updater.actions, [])
        self.assertIn('fixture:old', self.updater.environment.read_text())

    def test_active_recovery_reestablishes_drain_and_checks_new_claim(self):
        self.interrupted_switch()
        self.updater.mode, self.updater.reason = 'active', 'manual resume'
        self.updater.active_checks = iter([1])
        self.assertEqual(self.updater.run()['status'], 'deferred_recovery_active')
        self.assertEqual(self.updater.mode, 'active')
        self.assertNotIn(['restart'], self.updater.commands)
        self.assertTrue(self.updater.journal.exists())

    def test_scheduled_check_requires_conditional_server_support(self):
        self.updater.legacy_server = True
        self.assertEqual(self.updater.run()['status'], 'deferred_initial_setup')
        self.assertEqual(self.updater.actions, [])

    def test_explicit_legacy_bootstrap_transitions_to_conditional_server(self):
        self.updater.legacy_server = True
        self.updater.config['legacy_bootstrap'] = True
        self.assertEqual(self.updater.run()['status'], 'updated')
        writes = [c for c in self.updater.commands if '--action' in c and c[c.index('--action') + 1] != 'status']
        self.assertNotIn('--expected-state', writes[0])
        self.assertNotIn('--expected-state', writes[1])
        self.assertIn('--expected-state', writes[-1])

    def test_resume_between_recovery_reads_does_not_restart(self):
        self.interrupted_switch()
        original = self.updater.environment.read_bytes()
        read = self.updater.owned_maintenance
        calls = 0
        def changed_state(journal, allow_durable=False):
            nonlocal calls
            calls += 1
            if calls == 2:
                self.updater.mode, self.updater.reason = 'active', 'operator resumed'
            return read(journal, allow_durable)
        self.updater.owned_maintenance = changed_state
        with self.assertRaisesRegex(RuntimeError, 'barrier changed'):
            self.updater.run()
        self.assertEqual(self.updater.environment.read_bytes(), original)
        self.assertNotIn(['restart'], self.updater.commands)


if __name__ == '__main__':
    unittest.main()

#!/usr/bin/env python3
"""Exercise actual legacy-format migration and killed restore processes."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', required=True)
parser.add_argument('--previous-binary', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
binary, previous = Path(args.binary).resolve(), Path(args.previous_binary).resolve()
out = Path(args.out).resolve()
if out.exists():
    raise SystemExit('choose a new isolated fixture directory')
out.mkdir(parents=True, mode=0o700)
(out / 'ISOLATED-STORAGE').write_text('Disposable legacy server storage fixture. No production data.\n')
data = out / 'data'
events = []

def cli(label, executable, *values, input_text=None, expected=True, error=None):
    result = subprocess.run([str(executable), *map(str, values), '--json'], input=input_text, text=True, capture_output=True, cwd=out, timeout=180)
    if expected:
        assert result.returncode == 0, (label, result.stderr)
        value = json.loads(result.stdout)
    else:
        assert result.returncode != 0 and (error is None or error in result.stderr), (label, result.stdout, result.stderr)
        value = None
    events.append({'case': label, 'passed': True})
    return value

def sha(path):
    with Path(path).open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()

def kill_when(label, values, predicate):
    log = (out / (label + '.log')).open('w')
    process = subprocess.Popen([str(binary), *map(str, values), '--json'], cwd=out, text=True, stdout=log, stderr=log)
    caught = False
    try:
        for attempt in range(30000):
            if process.poll() is not None:
                break
            if predicate():
                os.kill(process.pid, signal.SIGSTOP)
                caught = True
                break
            time.sleep(0.001)
        assert caught, label + ' completed before its durable partial state could be observed'
        process.kill()
        process.wait(timeout=10)
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=10)
        log.close()
    events.append({'case': label + ' actual SIGKILL', 'passed': True})

old = cli('real old version', previous, 'version')
current = cli('real current version', binary, 'version')
assert old['version'] == '0.1.19' and current['version'] == '0.1.20'
cli('bootstrap legacy format through old CLI', previous, 'server', 'bootstrap', '--data-dir', data, '--username', 'owner', '--password-stdin', input_text='fixture-storage-password-220\n')
assert (data / 'ael-server.marker').read_text() == 'AEL-STANDALONE-1\n'
# Registered but unopened projects are a supported old layout. Their quantity
# creates an observable migration window without a product test hook.
with sqlite3.connect(data / 'runtime.db') as database:
    database.executemany('INSERT INTO server_projects(id,name) VALUES(?,?)', [('fixture-' + str(index), 'Legacy fixture ' + str(index)) for index in range(120)])
cli('current server requires explicit legacy migration', binary, 'server', 'start', '--data-dir', data, expected=False, error='SERVER_MIGRATION_REQUIRED')
pre_backup = out / 'before-upgrade'
snapshot = cli('consistent legacy backup before migration', binary, 'server', 'backup', '--data-dir', data, '--out', pre_backup, '--ack-stopped')
assert snapshot['data_format'] == 1
cli('migration requires stopped legacy service', binary, 'server', 'migrate', '--data-dir', data, expected=False, error='SERVER_STOP_CONFIRMATION_REQUIRED')
intent = data / 'server-migration.json'
kill_when('migration', ['server', 'migrate', '--data-dir', data, '--ack-stopped'], lambda: intent.is_file() and (data / 'ael-server.marker').read_text() == 'AEL-STANDALONE-2\n')
before_old_start = sha(data / 'runtime.db')
cli('old binary rollback is blocked after format switch', previous, 'server', 'start', '--data-dir', data, expected=False)
assert sha(data / 'runtime.db') == before_old_start
cli('partial migrated service cannot serve', binary, 'server', 'start', '--data-dir', data, expected=False, error='SERVER_RECOVERY_REQUIRED')
resumed = cli('resume same migration', binary, 'server', 'migrate', '--data-dir', data, '--ack-stopped')
assert resumed['status'] == 'migrated_quiesced'
cli('completed migration idempotence', binary, 'server', 'migrate', '--data-dir', data, '--ack-stopped')
post_backup = out / 'after-upgrade'
snapshot = cli('whole central backup after migration', binary, 'server', 'backup', '--data-dir', data, '--out', post_backup)
assert snapshot['data_format'] == 2
cli('verify backup through formal CLI', binary, 'server', 'verify-backup', '--from', post_backup)
target = out / 'restored'
plan = target / 'server-restore.json'
kill_when('restore', ['server', 'restore', '--data-dir', target, '--from', post_backup], lambda: plan.is_file())
cli('partial restored service cannot serve', binary, 'server', 'start', '--data-dir', target, expected=False, error='SERVER_RECOVERY_REQUIRED')
restored = cli('resume exact restore into empty volume', binary, 'server', 'restore', '--data-dir', target, '--from', post_backup)
assert restored['status'] == 'restored_quiesced'
with sqlite3.connect(target / 'runtime.db') as database:
    count = database.execute('SELECT count(*) FROM server_projects').fetchone()[0]
    users = database.execute('SELECT count(*) FROM server_users').fetchone()[0]
assert count == 120 and users == 1
marker = target / 'ael-server.marker'
assert marker.read_text() == 'AEL-STANDALONE-2\n'
cli('restore refuses to overwrite a populated service', binary, 'server', 'restore', '--data-dir', target, '--from', post_backup, expected=False, error='SERVER_RESTORE_TARGET_NOT_EMPTY')
result = {'result': 'PASS', 'isolated_same_machine': True, 'old': old, 'current': current, 'registered_projects_preserved': count, 'actual_migration_killed_and_recovered': True, 'old_binary_rollback_rejected': True, 'actual_restore_killed_and_recovered': True, 'events': events, 'oracle_access': False}
(out / 'summary.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))

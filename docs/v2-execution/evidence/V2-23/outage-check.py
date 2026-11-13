import json,subprocess
from pathlib import Path
root=Path('/private/tmp/ael-v223')
binary='/Users/vinson/.local/share/ael-oracle-v2/bin/ael'
project=str(root/'agent-pilot/project')
config=str(root/'pilot-config')
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=12','-o','StrictHostKeyChecking=yes','-i','/Users/vinson/.ssh/oracle.key','ubuntu@129.154.48.38']
compose='sudo -n docker compose --project-directory /opt/ael-v2 --env-file /opt/ael-v2/.env -f /opt/ael-v2/compose.yaml '
def remote(action):
 r=subprocess.run(ssh+[compose+action],capture_output=True,text=True,timeout=120)
 (root/('outage-'+action.split()[0]+'.log')).write_text(r.stdout+r.stderr)
 r.check_returncode()
def cli(*args):
 return subprocess.run([binary,*args,'--project',project,'--config-dir',config,'--json'],capture_output=True,text=True,timeout=60)
before=cli('task','show','--task','oracle-parent');before.check_returncode();initial=json.loads(before.stdout)
try:
 remote('stop server')
 offline=cli('task','show','--task','oracle-parent');offline.check_returncode();cached=json.loads(offline.stdout)
 assert cached['online'] is False and cached['authoritative'] is False and cached['revision']==initial['revision']
 (root/'actual-offline-cache.json').write_text(json.dumps(cached,indent=2))
 rejected=cli('runner','next','--runner','mac-pilot','--parent','oracle-parent')
 assert rejected.returncode!=0 and 'REMOTE_UNAVAILABLE' in rejected.stderr
 (root/'actual-offline-next-error.json').write_text(rejected.stderr)
finally:
 remote('up -d --wait --wait-timeout 90 server')
after=cli('task','show','--task','oracle-parent');after.check_returncode();current=json.loads(after.stdout)
assert current['online'] is True and current['authoritative'] is True and current['revision']==initial['revision']
assert current['task']['status']=='completed' and current['task']['candidate']==initial['task']['candidate']
(root/'outage-summary.json').write_text(json.dumps({'result':'PASS','actual_remote_service_stopped':True,'offline_cache_non_authoritative':True,'offline_next_rejected':True,'same_revision_after_restart':True,'candidate':current['task']['candidate'],'restored_service':'healthy'},indent=2))
print((root/'outage-summary.json').read_text())

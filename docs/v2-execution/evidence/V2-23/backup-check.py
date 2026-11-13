import json,subprocess
from datetime import datetime,timedelta,timezone
from pathlib import Path
root=Path('/private/tmp/ael-v223')
binary='/Users/vinson/.local/share/ael-oracle-v2/bin/ael'
password=Path('/Users/vinson/.config/ael-oracle-v2/admin-password').read_text()
def maintenance(action,key,extra=()):
 command=[binary,'server','maintenance','--url','https://ael.adoumi.site','--username','owner','--password-stdin','--action',action,'--reason','authorized V2-23 isolated pilot backup','--request-key',key,'--json',*extra]
 r=subprocess.run(command,input=password,capture_output=True,text=True,timeout=60)
 (root/('maintenance-'+action+'.json')).write_text(r.stdout)
 if r.returncode: raise RuntimeError(r.stderr)
 return json.loads(r.stdout)
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=12','-o','StrictHostKeyChecking=yes','-i','/Users/vinson/.ssh/oracle.key','ubuntu@129.154.48.38']
compose='sudo -n docker compose --project-directory /opt/ael-v2 --env-file /opt/ael-v2/.env -f /opt/ael-v2/compose.yaml '
try:
 deadline=(datetime.now(timezone.utc)+timedelta(seconds=30)).isoformat().replace('+00:00','Z')
 maintenance('drain','v223-real-backup-drain',('--deadline',deadline))
 quiesced=maintenance('quiesce','v223-real-backup-quiesce')
 assert quiesced['current']['mode']=='quiesced'
 for name,args in [('backup','run --rm --no-deps -T operator server backup --data-dir /data --out /backup/v223-20261113 --json'),('verify-backup','run --rm --no-deps -T operator server verify-backup --from /backup/v223-20261113 --json')]:
  r=subprocess.run(ssh+[compose+args],capture_output=True,text=True,timeout=180)
  (root/(name+'.json')).write_text(r.stdout)
  (root/(name+'-stderr.log')).write_text(r.stderr)
  if r.returncode: raise RuntimeError(r.stderr)
finally:
 resumed=maintenance('resume','v223-real-backup-resume')
 assert resumed['current']['mode']=='active'
print('real Oracle backup verified; primary service active')

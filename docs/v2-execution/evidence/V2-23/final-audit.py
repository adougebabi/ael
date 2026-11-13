import hashlib,json,subprocess
from pathlib import Path
root=Path('/private/tmp/ael-v223')
binary='/Users/vinson/.local/share/ael-oracle-v2/bin/ael'
base=['--project',str(root/'agent-pilot/project'),'--config-dir',str(root/'pilot-config'),'--json']
states={}
for task in ['oracle-one','oracle-two','oracle-parent']:
 r=subprocess.run([binary,'task','show','--task',task,*base],capture_output=True,text=True,timeout=60)
 if r.returncode: raise RuntimeError(r.stderr)
 state=json.loads(r.stdout)
 assert state['online'] and state['authoritative'] and state['task']['status']=='completed'
 (root/(task+'-final.json')).write_text(json.dumps(state,indent=2))
 states[task]=state
parent=states['oracle-parent']['task']
assert len(parent['integration']['leaves'])==2 and len(parent['evidence'])==3
assert all(item['method']=='parent_rechecked' for item in parent['integration']['leaves'])
assert all(item['result']=='pass' for item in parent['evidence'])
reviews=[json.loads(line) for line in (root/'human-reviews.jsonl').read_text().splitlines()]
assert [item['result'] for item in reviews]==['fail','pass','pass']
assert reviews[0]['candidate']!=reviews[1]['candidate']==reviews[2]['candidate']==parent['candidate']
leaf=states['oracle-two']['task']
assert any(item['result']=='fail' and item['candidate']==reviews[0]['candidate'] and item['source']=='human:mac-reviewer' for item in leaf.get('evidence_history',[]))
context=json.loads((root/'final-context.json').read_text())
spec_sha=hashlib.sha256((root/'agent-pilot/project/spec/rules.md').read_bytes()).hexdigest()
assert context['candidate_id']==parent['candidate'] and context['workspace_matches'] is True
assert context['usable_for_current_work'] is False and 'task is terminal, paused or awaiting a decision' in context['validity_reasons']
assert context['sources'][0]['sha256']==spec_sha
assert any(item['version']==spec_sha for item in parent['applicable_knowledge'])
assert hashlib.sha256((root/'downloaded-spec.md').read_bytes()).hexdigest()==spec_sha
sha=hashlib.sha256(Path(reviews[-1]['image']).read_bytes()).hexdigest()
assert hashlib.sha256((root/'downloaded-parent.png').read_bytes()).hexdigest()==sha
restore=json.loads((root/'restore-summary.json').read_text())
assert restore['result']=='PASS' and restore['candidate']==parent['candidate'] and restore['artifact_sha256']==sha and restore['restored_old_token_status']==401
outage=json.loads((root/'outage-summary.json').read_text())
assert outage['result']=='PASS' and outage['same_revision_after_restart']
old=json.loads((root/'old-client-final-parent.json').read_text())
assert old['online'] and old['authoritative'] and old['task']['candidate']==parent['candidate'] and old['task']['status']=='completed'
version=json.loads((root/'current-version-check.json').read_text())
assert version['compatible'] and version['version']=='0.1.22' and version['server']['version']=='0.1.22'
assert json.loads((root/'maintenance-resume.json').read_text())['current']['mode']=='active'
assert not (root/'agent-pilot/project/.ael/work-items').exists()
summary={'result':'PASS','origin':'https://ael.adoumi.site','mode':'one authorized Mac Codex session using real Oracle Server through user Lucky HTTPS','client_version':'0.1.22','server_build':'f838a94-e5074a4d4148','parent':{'id':'oracle-parent','status':parent['status'],'candidate':parent['candidate'],'fresh_checks':3,'required_leaves':2,'impact_unknown':parent['integration'].get('impact_unknown',False)},'leaves':list(states)[:2],'actual_agent':'current authorized Codex desktop session performed source edits, builds, Git and declared checks','additional_codex_exec':'existing read-only configuration prevented writes; failed attempt retained and not counted as coding pass','reviewer':'independent authenticated account driven by external Playwright','human_role_results':['fail','pass','pass'],'spec_sha256':spec_sha,'image_sha256':sha,'context_terminal_work_rejected':True,'actual_remote_service_outage_recovered':True,'old_0_1_21_client_compatible':True,'actual_oracle_empty_volume_restore':True,'restored_old_token_status':401,'client_task_containers':False,'business_scope':'isolated synthetic pilot only; actual cloud client requirement removed by explicit user decision'}
(root/'summary.json').write_text(json.dumps(summary,indent=2))
print(json.dumps(summary))

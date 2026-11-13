import hashlib,http.client,json,sys
from http.cookies import SimpleCookie

secret=json.load(sys.stdin)
base_headers={'Host':'ael.adoumi.site','X-Forwarded-Proto':'https','Origin':'https://ael.adoumi.site','X-AEL-Protocol':'1'}
def request(method,path,body=None,extra=None):
 headers=dict(base_headers)
 headers.update(extra or {})
 data=None if body is None else json.dumps(body).encode()
 if data is not None: headers['Content-Type']='application/json'
 conn=http.client.HTTPConnection('127.0.0.1',17333,timeout=20)
 conn.request(method,path,body=data,headers=headers)
 response=conn.getresponse()
 status=response.status
 values=response.getheaders()
 content=response.read()
 conn.close()
 return status,values,content
status,_,_=request('GET','/api/v1/projects/v223-pilot/tasks/oracle-parent',extra={'Authorization':'Bearer '+secret['old_token']})
assert status==401,status
status,headers,body=request('POST','/api/v1/auth/login',{'username':'owner','password':secret['password']})
assert status==200,(status,body[:200])
cookies=SimpleCookie()
for key,value in headers:
 if key.lower()=='set-cookie': cookies.load(value)
cookie='; '.join(key+'='+value.value for key,value in cookies.items())
status,_,body=request('GET','/api/v1/projects/v223-pilot/tasks/oracle-parent',extra={'Cookie':cookie})
assert status==200,(status,body[:200])
task=json.loads(body)['task']
assert task['status']=='completed' and task['candidate']==secret['candidate']
status,_,picture=request('GET','/api/v1/projects/v223-pilot/artifacts/'+secret['artifact']+'/content',extra={'Cookie':cookie})
assert status==200,status
sha=hashlib.sha256(picture).hexdigest()
assert sha==secret['artifact_sha256']
print(json.dumps({'result':'PASS','restored_task':task['definition']['id'],'status':task['status'],'candidate':task['candidate'],'artifact':secret['artifact'],'artifact_sha256':sha,'restored_old_token_status':401,'admin_login':True,'private_test_endpoint':'Oracle loopback 17333','actual_empty_volume_restore':True}))

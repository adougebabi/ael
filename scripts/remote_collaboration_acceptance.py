#!/usr/bin/env python3
"""V2-13 external dual-identity HTTP collaboration and recovery acceptance."""
import argparse
import concurrent.futures
import hashlib
import itertools
import time
import datetime
import http.client
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import platform
import select
import shutil
import socket
import subprocess
import sys
import threading
import urllib.parse
import urllib.error
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
source = parser.add_mutually_exclusive_group(required=True)
source.add_argument("--binary")
source.add_argument("--bundle")
parser.add_argument("--out", required=True)
args = parser.parse_args()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("refusing a nonempty fixture directory")
out.mkdir(parents=True, exist_ok=True)
(out / "ISOLATED-REMOTE-RUNNER").write_text("Disposable project, Server and processes only.\n")
if args.binary:
    binary = Path(args.binary).resolve()
else:
    system = {"darwin": "darwin", "linux": "linux"}.get(platform.system().lower())
    arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(platform.machine().lower())
    if not system or not arch:
        raise SystemExit("unsupported fixture platform")
    binary = Path(args.bundle).resolve() / f"ael-{system}-{arch}"
env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
env.update(AEL_DATA_DIR=str(out / "unused-runtime"), AEL_INSTALL_PREFIX=str(out / "unused-install"),
           GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT="0",
           RUNNER_SENTINEL_SECRET="must-not-reach-child")
commands = []


def run(argv, cwd, run_env=env, input_text=None, expect_ok=True):
    process = subprocess.run([str(value) for value in argv], cwd=cwd, env=run_env, input=input_text,
                             capture_output=True, text=True, timeout=90)
    commands.append({"argv": [str(value) for value in argv], "exit": process.returncode,
                     "stdout": process.stdout, "stderr": process.stderr})
    if (process.returncode == 0) != expect_ok:
        raise RuntimeError(f"unexpected command result: {argv}; {process.stderr}")
    return json.loads(process.stdout) if expect_ok and str(argv[0]) == str(binary) else process.stdout


def git(cwd, *argv):
    return run(["git", *argv], cwd)


origin = out / "origin"
origin.mkdir()
git(origin, "init", "-b", "main")
git(origin, "config", "user.name", "Fixture")
git(origin, "config", "user.email", "fixture@example.invalid")
(origin / "spec").mkdir()
(origin / "spec" / "rules.md").write_text("# Fixture rules\nDo not publish.\n")
(origin / "app.txt").write_text("original\n")
(origin / ".gitignore").write_text(".ael/\n")
git(origin, "add", "--", ".gitignore", "app.txt", "spec/rules.md")
git(origin, "commit", "-m", "fixture baseline")
projects = {name: out / f"checkout-{name}" for name in ("a", "b")}
for directory in projects.values():
    git(out, "clone", "--no-local", str(origin), str(directory))


def client(name, *argv, input_text=None, expect_ok=True):
    client_env = dict(env, AEL_REMOTE_CONFIG_DIR=str(out / f"config-{name}"), AEL_DATA_DIR=str(out / f"runtime-{name}"))
    return run([binary, *argv, "--project", projects[name], "--json"], projects[name], client_env, input_text, expect_ok)


check_command = [sys.executable, "-c", "from pathlib import Path; assert Path('app.txt').read_text() == 'fixed\\n'; print('fixture check passed')"]
proc, log, proxy = None, None, None
try:
    run([binary, "server", "bootstrap", "--data-dir", out / "server-data", "--username", "owner", "--password-stdin", "--json"], out, env, "fixture-admin-password\n")
    log = (out / "server.log").open("w")
    proc = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(out / "server-data"), "--addr", "127.0.0.1:0", "--json"],
                            cwd=out, env=env, stdout=subprocess.PIPE, stderr=log, text=True)
    ready, _, _ = select.select([proc.stdout], [], [], 30)
    if not ready:
        raise RuntimeError("server did not announce its address")
    line = proc.stdout.readline()
    if not line:
        raise RuntimeError("server exited; see server.log")
    base = json.loads(line)["url"]
    target = urllib.parse.urlparse(base)

    class Proxy(http.server.BaseHTTPRequestHandler):
        def log_message(self, *unused):
            pass

        def forward(self):
            data = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            if self.server.offline:
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
            if self.command == "GET" and "/operations/" in self.path and self.server.drop_next_lookup:
                self.server.drop_next_lookup = False
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
            headers = {key: value for key, value in self.headers.items() if key.lower() not in ("host", "connection", "content-length")}
            headers["Host"] = target.netloc
            if data:
                headers["Content-Length"] = str(len(data))
            conn = http.client.HTTPConnection(target.hostname, target.port, timeout=20)
            conn.request(self.command, self.path, body=data or None, headers=headers)
            response = conn.getresponse()
            body = response.read()
            if self.command == "POST" and self.path.endswith("/submit") and response.status == 200 and self.server.drop_next_submit:
                self.server.drop_next_submit = False
                if self.server.offline_after_submit: self.server.offline = True
                self.server.drop_count += 1
                conn.close()
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
            self.send_response(response.status)
            for key, value in response.getheaders():
                if key.lower() not in ("connection", "transfer-encoding", "content-length", "server", "date"):
                    self.send_header(key, value)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            conn.close()

        do_GET = forward
        do_POST = forward

    proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Proxy)
    proxy.offline = False
    proxy.offline_after_submit = False
    proxy.drop_next_submit = False
    proxy.drop_next_lookup = False
    proxy.drop_count = 0
    proxyThread = threading.Thread(target=proxy.serve_forever, daemon=True)
    proxyThread.start()
    proxyBase = "http://127.0.0.1:" + str(proxy.server_address[1])
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    csrf = ""

    def api(method, path, payload=None, bearer=None, expected=200):
        body = json.dumps(payload).encode() if payload is not None else None
        headers = {}
        if body is not None:
            headers.update({"Content-Type": "application/json", "X-AEL-Protocol": "1", "Origin": base})
            if csrf:
                headers["X-AEL-CSRF"] = csrf
        if bearer:
            headers["Authorization"] = "Bearer " + bearer
        request = urllib.request.Request(base + path, data=body, headers=headers, method=method)
        try:
            with opener.open(request, timeout=15) as response:
                status, data = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, data = error.code, error.read()
        if status != expected:
            raise RuntimeError(f"{method} {path}: HTTP {status}, expected {expected}: {data[:300]!r}")
        return json.loads(data) if data else {}

    csrf = api("POST", "/api/v1/auth/login", {"username": "owner", "password": "fixture-admin-password"})["csrf"]
    api("POST", "/api/v1/projects", {"id": "shared", "name": "Runner Fixture"}, expected=201)
    tokens = {}
    for name, username, role in (("a", "writer", "executor"), ("b", "reader", "executor")):
        api("POST", "/api/v1/users", {"username": username, "password": f"fixture-{username}-password"}, expected=201)
        api("POST", "/api/v1/projects/shared/members", {"username": username, "role": role}, expected=201)
        tokens[name] = api("POST", "/api/v1/tokens", {"username": username, "project_id": "shared", "label": name}, expected=201)["token"]
        client(name, "server", "profile", "add", "--name", "central", "--url", proxyBase if name == "a" else base, "--token-stdin", input_text=tokens[name] + "\n")
        client(name, "project", "connect", "--profile", "central", "--remote-project", "shared")
    client("a", "repository", "register", "--repository", "fixture-repo", "--operation-id", "repo", "--idempotency-key", "repo")
    client("a", "knowledge", "index")
    serial = itertools.count(1)
    summary = []
    def restart_server():
        global proc
        proc.terminate();proc.wait(timeout=10)
        proc=subprocess.Popen([str(binary),"server","start","--data-dir",str(out/"server-data"),"--addr",target.netloc,"--json"],cwd=out,env=env,stdout=subprocess.PIPE,stderr=log,text=True)
        assert select.select([proc.stdout],[],[],30)[0]
        assert json.loads(proc.stdout.readline())["url"]==base

    def private(label, value):
        path = out / (label+".json")
        path.write_text(json.dumps(value)); path.chmod(0o600)
        return path

    def mutation(name, action, body, ok=True, runner=None, **flags):
        key = "op-"+str(next(serial))
        path = private(key, dict(body, operation_id=key, idempotency_key=key))
        argv = ["runner", action, "--runner", runner or "agent-"+name, "--file", path]
        for key, value in flags.items(): argv.extend(["--"+key, str(value)])
        result = client(name, *argv, expect_ok=ok)
        return result, path

    def current(task):
        return client("b", "task", "show", "--task", task)

    def create(task, checks=None):
        check = {"id":"unit", "kind":"functional", "method":"command", "command":[sys.executable,"-c","print('external check')"], "covers":["result"], "required":True}
        definition = {"id":task,"title":task,"type":"bug","goal":"verified","scope":["app.txt"],"acceptance":["result"],"knowledge":["source:spec/rules.md"],"checks":checks or [check]}
        path = private("definition-"+task,definition)
        return client("a", "task", "create", "--file", path, "--operation-id", "create-"+task, "--idempotency-key", "create-"+task)

    def prepare(name, task, runner=None, workspace=None):
        state = current(task)
        key = "context-"+str(next(serial))
        observed = client(name, "candidate", "capture", "--task", task, "--repository", "fixture-repo", "--checkout", "checkout-"+name, "--workspace", workspace or "workspace-"+name, "--runner", runner or "agent-"+name, "--expected-revision", state["revision"], "--operation-id", key, "--idempotency-key", key)
        context = client(name, "context", "publish", "--task", task, "--observation", observed["id"], "--expected-revision", state["revision"], "--operation-id", key+"-publish", "--idempotency-key", key+"-publish")
        return state, context

    def claim(name, task):
        state, context = prepare(name, task)
        destination = out / ("claim-"+str(next(serial))+".json")
        result, request = mutation(name,"claim",{"task_id":task,"context_id":context["id"],"expected_revision":state["revision"]},out=destination)
        assert "token" not in result["attempt"]
        attempt = json.loads(destination.read_text())["attempt"]
        lease = {"task_id":task,"attempt_id":attempt["id"],"token":attempt["token"],"fence":attempt["fence"]}
        return {"name":name,"task":task,"state":state,"revision":state["revision"],"lease":lease,"attempt":attempt}

    def observe(work):
        result, _ = mutation(work["name"],"observe",dict(work["lease"],expected_revision=work["revision"]),repository="fixture-repo")
        work["candidate"] = result["candidate_id"]
        return result

    def upload(work, file, ref, ok=True):
        lease_file = private("upload-lease-"+str(next(serial)),dict(work["lease"],runner_id="agent-"+work["name"],expected_revision=work["revision"],lease_until=work["attempt"]["lease_until"]))
        key="upload-"+str(next(serial))
        result=client(work["name"],"artifact","upload","--task",work["task"],"--attempt",work["attempt"]["id"],"--candidate",work["candidate"],"--file",file,"--lease-file",lease_file,"--expected-revision",work["revision"],"--operation-id",key,"--idempotency-key",key,expect_ok=ok)
        return result, {"path":ref,"artifact_id":result["id"]} if ok else None

    def body(work, bindings=None, method=None):
        result = {"status":"pass","summary":"external host completed responsibility","artifacts":[b["artifact_id"] for b in bindings or []]}
        if work["state"]["current_step"]=="execute": result["knowledge"]={"status":"unchanged","reason":"fixture rules unchanged"}
        payload=dict(work["lease"],expected_revision=work["revision"],candidate_id=work["candidate"],result=result,bindings=bindings or [])
        if method: payload.update(method=method,inputs=["app.txt"])
        if method=="command": payload["exit_code"]=0
        return payload

    def finish(work, bindings=None, method=None):
        return mutation(work["name"],"submit",body(work,bindings,method))[0]

    def pending(name, action, key):
        paths=[]
        for path in (out / ("config-"+name) / "agent-operations").glob("*.json"):
            record=json.loads(path.read_text())
            if record["action"]==action and record["payload"]["idempotency_key"]==key: paths.append(path)
        assert len(paths)==1, (action,key)
        assert paths[0].stat().st_mode & 0o077 == 0
        return paths[0]

    for name in ("a","b"):
        client(name,"knowledge","index")
        path=private("register-"+name,{"id":"agent-"+name,"workspace_id":"workspace-"+name,"checkout_id":"checkout-"+name,"repository":"fixture-repo","version":"external-dual-fixture","capabilities":["development","scenario"],"expected_revision":"absent"})
        client(name,"runner","register","--file",path,"--operation-id","register-"+name,"--idempotency-key","register-"+name)

    # Two independent identities race through actual CLI processes and HTTP.
    create("race")
    states = {name:prepare(name,"race") for name in ("a","b")}
    requests={name:private("race-"+name,{"task_id":"race","context_id":states[name][1]["id"],"expected_revision":states[name][0]["revision"],"operation_id":"race-"+name,"idempotency_key":"race-"+name}) for name in ("a","b")}
    def contender(name):
        process=subprocess.run([str(binary),"runner","claim","--runner","agent-"+name,"--file",str(requests[name]),"--out",str(out/("race-lease-"+name+".json")),"--project",str(projects[name]),"--config-dir",str(out/("config-"+name)),"--json"],cwd=projects[name],env=env,capture_output=True,text=True,timeout=45)
        return name,process.returncode
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool: winners=list(pool.map(contender,("a","b")))
    assert sum(code==0 for _,code in winners)==1,winners
    winner=next(name for name,code in winners if code==0)
    a=json.loads((out/("race-lease-"+winner+".json")).read_text())["attempt"]
    work={"name":winner,"task":"race","state":states[winner][0],"revision":states[winner][0]["revision"],"attempt":a,"lease":{"task_id":"race","attempt_id":a["id"],"token":a["token"],"fence":a["fence"]}}
    observe(work);finish(work)
    assert len(current("race")["task"]["attempts"])==1
    summary.append({"case":"A13-01 same-unit race","result":"PASS","winner":winner})
    create("parallel-a");create("parallel-b")
    first=claim("a","parallel-a");second=claim("b","parallel-b")
    assert current("parallel-a")["task"]["attempts"][-1]["status"]=="running"
    assert current("parallel-b")["task"]["attempts"][-1]["status"]=="running"
    for work in (first,second): observe(work);finish(work)
    summary.append({"case":"A13-01 independent units active concurrently","result":"PASS"})

    # Freeze upload bytes across a CLI exit and a local source edit.
    create("loss");work=claim("a","loss");observe(work)
    original=out/"retained.txt";original.write_text("fixed original upload\n")
    proxy.offline=True
    started=time.monotonic();upload(work,original,"runtime:retained.txt",ok=False)
    assert time.monotonic()-started<35
    upload_error=commands[-1]["stderr"];assert "AGENT_OPERATION_PENDING" in upload_error,upload_error
    upload_args=commands[-1]["argv"];key=upload_args[upload_args.index("--idempotency-key")+1]
    upload_pending=pending("a","artifact-upload",key)
    original.write_text("changed while offline\n")
    proxy.offline=False
    received=client("a","runner","retry","--pending",upload_pending)
    downloaded=out/"retained-download.txt"
    client("b","artifact","download","--artifact",received["id"],"--out",downloaded)
    assert downloaded.read_text()=="fixed original upload\n"
    binding={"path":"runtime:retained.txt","artifact_id":received["id"]}
    # Lose the accepted response, and make subsequent lookups unreachable.
    proxy.drop_next_submit=True;proxy.offline_after_submit=True
    _,request=mutation("a","submit",body(work,[binding]),ok=False)
    assert "AGENT_OPERATION_PENDING" in commands[-1]["stderr"]
    key=json.loads(request.read_text())["idempotency_key"]
    operation=pending("a","submit",key)
    assert current("loss")["current_step"]=="execute"
    restart_server()
    proxy.offline=False;proxy.offline_after_submit=False
    restored=client("a","runner","retry","--pending",operation)
    assert restored["next"]["current_step"]=="execute"
    assert len(current("loss")["task"]["attempts"])==1
    assert client("a","runner","retry","--pending",operation)["operation_id"]==restored["operation_id"]
    client("b","runner","retry","--pending",operation,expect_ok=False)
    assert "AGENT_OPERATION_SCOPE" in commands[-1]["stderr"]
    summary.append({"case":"A13-02 bounded offline upload and lost accepted response, Server restart","result":"PASS"})

    # The same authenticated user may have another checkout. Server operation
    # keys are shared by that identity, so changed payloads must still conflict.
    def same_identity_on_b(*argv, ok=True):
        return run([binary,*argv,"--project",projects["b"],"--config-dir",out/"config-a","--json"],projects["b"],env,expect_ok=ok)
    foreign=private("same-key-different-submission",dict(json.loads(request.read_text()),result={"status":"pass","summary":"different request on another checkout","artifacts":[]}))
    same_identity_on_b("runner","submit","--runner","agent-a","--file",foreign,ok=False)
    assert "IDEMPOTENCY_CONFLICT" in commands[-1]["stderr"]
    foreign_records=[p for p in (out/"config-a/agent-operations").glob("*.json") if json.loads(p.read_text())["checkout"]==str(projects["b"].resolve()) and json.loads(p.read_text())["action"]=="submit"]
    assert len(foreign_records)==1
    same_identity_on_b("runner","retry","--pending",foreign_records[0],ok=False)
    assert "IDEMPOTENCY_CONFLICT" in commands[-1]["stderr"]
    retained_upload=json.loads(upload_pending.read_text())
    flags=retained_upload["upload"]["flags"]
    lease_file=private("collision-upload-lease",dict(work["lease"],runner_id="agent-a",expected_revision=work["revision"],lease_until=work["attempt"]["lease_until"]))
    argv=["artifact","upload","--file",downloaded,"--lease-file",lease_file]
    for flag,value in dict(flags,source="different-source").items(): argv.extend(["--"+flag,value])
    same_identity_on_b(*argv,ok=False)
    assert "IDEMPOTENCY_CONFLICT" in commands[-1]["stderr"]
    foreign_uploads=[p for p in (out/"config-a/agent-operations").glob("*.json") if json.loads(p.read_text())["checkout"]==str(projects["b"].resolve()) and json.loads(p.read_text())["action"]=="artifact-upload"]
    assert len(foreign_uploads)==1
    same_identity_on_b("runner","retry","--pending",foreign_uploads[0],ok=False)
    assert "IDEMPOTENCY_CONFLICT" in commands[-1]["stderr"]
    summary.append({"case":"cross-checkout same-identity key conflicts remain conflicts during recovery","result":"PASS"})

    # The old request remains attached to the old fence after explicit handoff.
    create("handoff");old=claim("a","handoff");observe(old)
    proxy.offline=True
    _,request=mutation("a","submit",body(old),ok=False)
    key=json.loads(request.read_text())["idempotency_key"];old_pending=pending("a","submit",key)
    proxy.offline=False
    state,context=prepare("b","handoff")
    same=private("same-workspace",{"id":"agent-same","workspace_id":"workspace-a","checkout_id":"checkout-b","repository":"fixture-repo","version":"fixture","capabilities":["development"],"expected_revision":"absent"})
    client("b","runner","register","--file",same,"--operation-id","same-register","--idempotency-key","same-register")
    mutation("b","recover",{"task_id":"handoff","attempt_id":old["attempt"]["id"],"context_id":context["id"],"expected_revision":state["revision"],"mode":"independent-workspace","reason":"old host disconnected"},runner="agent-same",ok=False)
    assert "WORKSPACE_CONFIRMATION_REQUIRED" in commands[-1]["stderr"]
    recovered,_=mutation("b","recover",{"task_id":"handoff","attempt_id":old["attempt"]["id"],"context_id":context["id"],"expected_revision":state["revision"],"mode":"independent-workspace","reason":"receiver declares an independent checkout"})
    assert recovered["workspace_decision"]=="independent-workspace" and recovered["host_execution_state"]=="unknown"
    replacement=private("replacement-register",{"id":"replacement","workspace_id":"workspace-replacement","checkout_id":"checkout-b","repository":"fixture-repo","version":"fixture","capabilities":["development"],"expected_revision":"absent"})
    client("b","runner","register","--file",replacement,"--operation-id","replacement-register","--idempotency-key","replacement-register")
    state,context=prepare("b","handoff",runner="replacement",workspace="workspace-replacement")
    mutation("b","recover",{"task_id":"handoff","attempt_id":old["attempt"]["id"],"context_id":context["id"],"expected_revision":state["revision"],"mode":"independent-workspace","reason":"original receiver became unavailable before claim"},runner="replacement")
    state,context=prepare("b","handoff")
    mutation("b","claim",{"task_id":"handoff","context_id":context["id"],"expected_revision":state["revision"]},out=out/"superseded-receiver.json",ok=False)
    assert "REMOTE_HANDOFF_TARGET" in commands[-1]["stderr"]
    mutation("b","recover",{"task_id":"handoff","attempt_id":old["attempt"]["id"],"context_id":context["id"],"expected_revision":state["revision"],"mode":"independent-workspace","reason":"explicitly select the available receiver again"})
    restart_server()
    receiving=claim("b","handoff")
    client("a","runner","retry","--pending",old_pending,expect_ok=False)
    assert any(code in commands[-1]["stderr"] for code in ("STALE_TOKEN","STALE_EXECUTION_UNIT"))
    create("quarantine")
    state,context=prepare("a","quarantine")
    mutation("a","claim",{"task_id":"quarantine","context_id":context["id"],"expected_revision":state["revision"]},out=out/"quarantined-lease.json",ok=False)
    assert "WORKSPACE_CONFIRMATION_REQUIRED" in commands[-1]["stderr"]
    state=current("handoff")
    mutation("a","recover",{"task_id":"handoff","attempt_id":old["attempt"]["id"],"expected_revision":state["revision"],"mode":"confirmed-stopped","reason":"old executor explicitly reports stopped"})
    assert current("handoff")["task"]["attempts"][-1]["id"]==receiving["attempt"]["id"]
    observe(receiving);finish(receiving)
    released=claim("a","quarantine");observe(released);finish(released)
    summary.append({"case":"A13-05 explicit workspace handoff, quarantine and old pending rejection","result":"PASS"})

    create("observation")
    work=claim("a","observation")
    before=(projects["a"]/"app.txt").read_bytes()
    proxy.offline=True
    _,request=mutation("a","observe",dict(work["lease"],expected_revision=work["revision"]),repository="fixture-repo",ok=False)
    key=json.loads(request.read_text())["idempotency_key"]
    retained=pending("a","observe",key)
    fixed_candidate=json.loads(retained.read_text())["payload"]["candidate"]["id"]
    (projects["a"]/"app.txt").write_text("changed after offline observation\n")
    proxy.offline=False
    restored=client("a","runner","retry","--pending",retained)
    assert restored["candidate_id"]==fixed_candidate
    (projects["a"]/"app.txt").write_bytes(before)
    observe(work);finish(work)
    summary.append({"case":"offline observe retains its exact candidate despite later host edits","result":"PASS"})

    # Actual browser screenshots are produced by the external fixture host.
    scenario = [{"id":"visual","kind":"ui","method":"scenario","covers":["result"],"required":True,"artifacts":["runtime:report.json","runtime:screenshot.png"]}]
    for name in ("a","b"):
        git(projects[name],"checkout","-b","branch-"+name)
        create("branch-"+name,scenario)
        work=claim(name,"branch-"+name);observe(work);finish(work)
        work=claim(name,"branch-"+name)
        (projects[name]/"app.txt").write_text("branch "+name+"\n")
        observe(work)
        _,binding=upload(work,projects[name]/"app.txt","app.txt")
        finish(work,[binding])
    browser_module = (Path(__file__).resolve().parent.parent/"web/node_modules/playwright/index.mjs").as_uri()
    screenshot_script = """import {chromium} from MODULE;
const browser=await chromium.launch({headless:true});
try {const page=await browser.newPage(); for (const name of ['a','b']) {await page.setContent('<h1>Branch '+name+'</h1>'); await page.screenshot({path:ROOT+'/branch-'+name+'.png'});}} finally {await browser.close();}
""".replace("MODULE",json.dumps(browser_module)).replace("ROOT",json.dumps(str(out)))
    run(["node","--input-type=module","-e",screenshot_script],out)
    verifications={}
    for name in ("a","b"):
        work=claim(name,"branch-"+name);observe(work)
        assert (projects[name]/"app.txt").read_text()=="branch "+name+"\n"
        report={"candidate":work["candidate"],"check":"visual","definition_revision":work["state"]["definition_revision"],"plan_revision":work["state"]["plan_revision"],"method":"scenario","source":"external-scenario:agent-"+name,"covers":["result"],"observations":"External browser rendered branch "+name+"; host verified its own app.txt"}
        report_path=private("branch-report-"+name,report)
        _,report_binding=upload(work,report_path,"runtime:report.json")
        _,image_binding=upload(work,out/("branch-"+name+".png"),"runtime:screenshot.png")
        verifications[name]=(work,[report_binding,image_binding])
    takeover=private("takeover-register",{"id":"takeover","workspace_id":"workspace-c","checkout_id":"checkout-b","repository":"fixture-repo","version":"fixture","capabilities":["development","scenario"],"expected_revision":"absent"})
    client("b","runner","register","--file",takeover,"--operation-id","takeover-register","--idempotency-key","takeover-register")
    state,context=prepare("b","branch-a",runner="takeover",workspace="workspace-c")
    historical=next(a for a in state["task"]["attempts"] if a["unit"]=="execute" and a["status"]=="pass")
    mutation("b","recover",{"task_id":"branch-a","attempt_id":historical["id"],"context_id":context["id"],"expected_revision":state["revision"],"mode":"independent-workspace","reason":"must not overwrite active verifier using old implementation"},runner="takeover",ok=False)
    assert "CLAIM_CONFLICT" in commands[-1]["stderr"]
    assert current("branch-a")["task"]["context_attempt"]==verifications["a"][0]["attempt"]["id"]
    a,bindings_a=verifications["a"];b,bindings_b=verifications["b"]
    assert a["candidate"]!=b["candidate"]
    mutation("a","submit",body(a,[bindings_a[0],bindings_b[1]],"scenario"),ok=False)
    assert "ARTIFACT_REQUIRED" in commands[-1]["stderr"]
    # A report from another branch fails its candidate binding even when the
    # bytes are uploaded under the current identity and current Attempt.
    foreign=private("foreign-report",json.loads((out/"branch-report-b.json").read_text()))
    _,foreign_binding=upload(a,foreign,"runtime:report.json")
    mutation("a","submit",body(a,[foreign_binding,bindings_a[1]],"scenario"),ok=False)
    assert "ASSESSMENT_EVIDENCE_REQUIRED" in commands[-1]["stderr"]
    for name,(work,bindings) in verifications.items():
        assert finish(work,bindings,"scenario")["next"]["next_action"]=="completed"
    summary.append({"case":"A13-04 independent branches, actual browser screenshots and cross-evidence rejection","result":"PASS"})

    # Development and verification use different authenticated clients.
    create("cross-verification")
    work=claim("a","cross-verification");observe(work);finish(work)
    work=claim("a","cross-verification");observe(work)
    _,deliverable=upload(work,projects["a"]/"app.txt","app.txt")
    finish(work,[deliverable])
    implemented_id=work["attempt"]["id"]
    # Source synchronization is an explicit action of this external test host.
    (projects["b"]/"app.txt").write_bytes((projects["a"]/"app.txt").read_bytes())
    state,context=prepare("b","cross-verification")
    mutation("b","recover",{"task_id":"cross-verification","attempt_id":implemented_id,"context_id":context["id"],"expected_revision":state["revision"],"mode":"independent-workspace","reason":"external host supplied the exact implementation candidate to verification"})
    verify=claim("b","cross-verification");observe(verify)
    run(verify["state"]["task"]["definition"]["checks"][0]["command"],projects["b"])
    assert (projects["b"]/"app.txt").read_bytes()==(projects["a"]/"app.txt").read_bytes()
    report=private("cross-verification-report",{"candidate":verify["candidate"],"check":"unit","definition_revision":verify["state"]["definition_revision"],"plan_revision":verify["state"]["plan_revision"],"method":"command","source":"external-command:agent-b","covers":["result"],"observations":"Separate authenticated verifier ran the declared command on its own checkout and compared fixture content"})
    _,binding=upload(verify,report,"runtime:report.json")
    assert finish(verify,[binding],"command")["next"]["next_action"]=="completed"
    implementation=next(a for a in current("cross-verification")["task"]["attempts"] if a["id"]==implemented_id)
    assert implementation["status"]=="pass"
    summary.append({"case":"separate developer and verifier handoff preserves completed implementation history","result":"PASS"})

    # Cancel and revision changes invalidate still-retained requests.
    for case in ("cancel","revision"):
        create(case);work=claim("a",case);observe(work)
        proxy.offline=True
        _,request=mutation("a","submit",body(work),ok=False)
        key=json.loads(request.read_text())["idempotency_key"];retained=pending("a","submit",key)
        proxy.offline=False
        state=current(case)
        if case=="cancel":
            client("b","task","cancel","--task",case,"--reason","external cancellation","--expected-revision",state["revision"],"--operation-id","cancel-live","--idempotency-key","cancel-live")
        else:
            definition=dict(state["task"]["definition"],acceptance=["changed result"],checks=[dict(state["task"]["definition"]["checks"][0],covers=["changed result"])])
            client("b","task","edit","--task",case,"--file",private("changed-definition",definition),"--reason","acceptance changed","--expected-revision",state["revision"],"--operation-id","revise-live","--idempotency-key","revise-live")
        client("a","runner","retry","--pending",retained,expect_ok=False)
        assert any(code in commands[-1]["stderr"] for code in ("STALE_TOKEN","STALE_EXECUTION_UNIT")),commands[-1]["stderr"]
        assert retained.exists()
        view=client("a","runner","show","--runner","agent-a")
        assert view["host_execution_state"]=="unknown"
        summary.append({"case":"A13-03 "+case+" rejects retained result","result":"PASS"})
        if case=="cancel":
            state=current(case)
            mutation("a","recover",{"task_id":case,"attempt_id":work["attempt"]["id"],"expected_revision":state["revision"],"mode":"confirmed-stopped","reason":"test executor confirms stopped after cancellation"})
        if case=="revision":
            # Release this fixture's responsibility without authorizing new work.
            mutation("a","interrupt",dict(work["lease"],reason="stale request"),ok=False)
    assert not (out/"runtime-a").exists() and not (out/"runtime-b").exists()
    assert not any((project/".ael/work-items").exists() for project in projects.values())
    (out/"summary.json").write_text(json.dumps({"mode":"external dual-identity real HTTP fixture","result":"PASS","cases":summary},indent=2))
    print(json.dumps({"result":"PASS","cases":summary,"out":str(out)}))
finally:
    if proc is not None:
        proc.terminate()
        try: proc.wait(timeout=10)
        except subprocess.TimeoutExpired: proc.kill(); proc.wait()
    if log is not None: log.close()
    if proxy is not None: proxy.shutdown();proxy.server_close()
    for path in out.glob("*.json"): path.chmod(0o600)

#!/usr/bin/env python3
"""Exercise one remote Runner on disposable local Git workspaces."""
import argparse
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
parser.add_argument("--verify-recovery", action="store_true")
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
    for name, username, role in (("a", "writer", "executor"), ("b", "reader", "reader")):
        api("POST", "/api/v1/users", {"username": username, "password": f"fixture-{username}-password"}, expected=201)
        api("POST", "/api/v1/projects/shared/members", {"username": username, "role": role}, expected=201)
        tokens[name] = api("POST", "/api/v1/tokens", {"username": username, "project_id": "shared", "label": name}, expected=201)["token"]
        client(name, "server", "profile", "add", "--name", "central", "--url", proxyBase if name == "a" else base, "--token-stdin", input_text=tokens[name] + "\n")
        client(name, "project", "connect", "--profile", "central", "--remote-project", "shared")
    client("a", "repository", "register", "--repository", "fixture-repo", "--operation-id", "repo", "--idempotency-key", "repo")
    client("a", "knowledge", "index")
    definition = {"id": "task", "parent":"epic", "title": "Repair fixture", "type": "bug", "goal": "verified", "scope": ["app.txt"], "acceptance": ["fixed"],
                  "knowledge": ["source:spec/rules.md"], "checks": [{"id": "unit", "kind": "functional", "method": "command", "command": check_command, "artifacts": ["runtime:report.json"], "covers": ["fixed"], "required": True}]}
    parent_file = out / "epic.json"
    parent_file.write_text(json.dumps({"id":"epic", "title":"External sequence", "type":"epic", "goal":"verified", "acceptance":["both children and integration"]}))
    client("a", "task", "create", "--file", parent_file, "--operation-id", "epic", "--idempotency-key", "epic")
    taskFile = out / "task.json"
    taskFile.write_text(json.dumps(definition))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "task", "--idempotency-key", "task")
    second_file = out / "second.json"
    second_file.write_text(json.dumps(dict(definition, id="second", dependencies=["task"])))
    client("a", "task", "create", "--file", second_file, "--operation-id", "second", "--idempotency-key", "second")
    serial = 0
    def request(action, body, ok=True, runner="agent-a", **flags):
        global serial
        serial += 1
        key = f"agent-{serial}"
        body = dict(body, operation_id=key, idempotency_key=key)
        path = out / f"request-{serial}.json"
        path.write_text(json.dumps(body)); path.chmod(0o600)
        argv = ["runner", action, "--runner", runner, "--file", path]
        for name, value in flags.items(): argv.extend(["--"+name, str(value)])
        return client("a", *argv, expect_ok=ok)

    register = out / "agent.json"
    register.write_text(json.dumps({"id":"agent-a", "workspace_id":"workspace-a", "checkout_id":"checkout-a", "repository":"fixture-repo", "version":"external-driver", "capabilities":["development"], "expected_revision":"absent"}))
    register.chmod(0o600)
    client("a", "runner", "register", "--file", register, "--operation-id", "register", "--idempotency-key", "register")
    for action in ("run", "resume-result", "recover-claim"):
        client("a", "runner", action, expect_ok=False)
    did_break = False
    did_fail = False
    records = []
    task_id = "task"
    for iteration in range(12):
        choice = client("a", "runner", "next", "--runner", "agent-a", "--parent", "epic")
        if choice["action"] != "ready": break
        task_id = choice["checkpoint"]["task"]
        current = client("a", "task", "show", "--task", task_id)
        task, revision = current["task"], current["revision"]
        next_value = client("a", "next", "--task", task_id)
        if task["status"] == "completed": break
        assert next_value["next_action"] in ("continue", "repair"), next_value
        step = task["current_step"]
        serial += 1
        key = f"context-{serial}"
        observed = client("a", "candidate", "capture", "--task", task_id, "--repository", "fixture-repo", "--checkout", "checkout-a", "--workspace", "workspace-a", "--runner", "agent-a", "--expected-revision", revision, "--operation-id", key, "--idempotency-key", key)
        context = client("a", "context", "publish", "--task", task_id, "--observation", observed["id"], "--expected-revision", revision, "--operation-id", key+"-pub", "--idempotency-key", key+"-pub")
        client("a", "context", "show", "--context", context["id"])
        claim_path = out / f"claim-{iteration}.json"
        claimed = request("claim", {"task_id":task_id, "parent_id":"epic", "context_id":context["id"], "expected_revision":revision}, out=claim_path)
        assert "token" not in claimed["attempt"]
        private = json.loads(claim_path.read_text())
        attempt = private["attempt"]
        lease = {"task_id":task_id, "attempt_id":attempt["id"], "token":attempt["token"], "fence":attempt["fence"]}
        # Read-only principals cannot borrow another Agent's responsibility.
        client("b", "runner", "claim", "--runner", "agent-a", "--file", out / f"request-{serial}.json", "--out", out / f"forbidden-claim-{iteration}.json", expect_ok=False)
        request("heartbeat", lease)
        request("progress", dict(lease, summary="external driver working", next="submit actual observations", stage=step))
        if args.verify_recovery:
            saved = client("a", "recovery", "save", "--session", "deterministic-a", "--runner", "agent-a")
            assert saved["online_verified"] and saved["summary"]["task"] == task_id, saved
            checked = client("a", "recovery", "check", "--session", "deterministic-a")
            assert checked["summary"]["attempt"] == attempt["id"], checked
            assert client("a", "recovery", "check", "--session", "independent-session")["status"] == "no_checkpoint"
            raw_checkpoint = Path(saved["checkpoint"]).read_text()
            assert attempt["token"] not in raw_checkpoint and tokens["a"] not in raw_checkpoint
            if task_id == "second":
                assert "task" in checked["summary"]["completed_children"], checked
            if iteration == 0:
                for host in ("codex", "claude"):
                    installed = client("a", "recovery", "install", "--host", host)
                    assert installed["status"] in ("pending_host_trust", "unsupported_or_unavailable"), installed
                    repeated = client("a", "recovery", "install", "--host", host)
                    assert not repeated.get("changed", False), repeated
        if step == "execute":
            (projects["a"] / "app.txt").write_text("fixed\n" if did_break else "broken\n")
            did_break = True
        output = request("observe", dict(lease, expected_revision=revision), repository="fixture-repo")
        candidate = client("a", "candidate", "show", "--candidate", output["candidate_id"])
        lease_path = out / f"upload-lease-{iteration}.json"
        lease_path.write_text(json.dumps(dict(lease, runner_id="agent-a", expected_revision=revision, lease_until=attempt["lease_until"])))
        lease_path.chmod(0o600)
        def upload(path, ref):
            global serial
            serial += 1
            key = f"upload-{serial}"
            receipt = client("a", "artifact", "upload", "--task", task_id, "--attempt", attempt["id"], "--candidate", candidate["id"], "--file", path, "--lease-file", lease_path, "--expected-revision", revision, "--operation-id", key, "--idempotency-key", key)
            client("b", "artifact", "upload", "--task", task_id, "--attempt", attempt["id"], "--candidate", candidate["id"], "--file", path, "--lease-file", lease_path, "--expected-revision", revision, "--operation-id", key+"-forbidden", "--idempotency-key", key+"-forbidden", expect_ok=False)
            return {"path":ref, "artifact_id":receipt["id"]}
        result = {"status":"pass", "summary":"External host completed "+step}
        bindings = []
        extra = {}
        if step == "execute":
            bindings.append(upload(projects["a"] / "app.txt", "app.txt"))
            result["knowledge"] = {"status":"unchanged", "reason":"fixture specification unchanged"}
        if step == "verify":
            process = subprocess.run(check_command, cwd=projects["a"], capture_output=True, text=True)
            code = process.returncode
            did_fail = did_fail or code != 0
            result.update(status="pass" if code == 0 else "fail", summary="External command reported "+str(code))
            report = {"candidate":candidate["id"], "check":"unit", "definition_revision":task["definition"]["definition_revision"], "plan_revision":task["plan"]["revision"], "method":"command", "source":"external-command:agent-a", "covers":["fixed"], "observations":process.stdout+process.stderr or "command completed"}
            report_path = out / f"report-{iteration}.json"
            report_path.write_text(json.dumps(report))
            bindings.append(upload(report_path, "runtime:report.json"))
            # Local edits after upload cannot alter the finalized original.
            report_path.write_text("changed after upload\n")
            download = out / f"received-{iteration}.json"
            client("b", "artifact", "download", "--artifact", bindings[-1]["artifact_id"], "--out", download)
            assert json.loads(download.read_text()) == report
            extra = {"method":"command", "exit_code":code, "inputs":["app.txt"]}
        result["artifacts"] = [b["artifact_id"] for b in bindings]
        body = dict(lease, expected_revision=revision, candidate_id=candidate["id"], result=result, bindings=bindings, **extra)
        if step == "verify":
            if iteration == 2:
                for label, changes in (("source", {"source":"human:owner"}), ("version", {"definition_revision":report["definition_revision"]+1}), ("coverage", {"covers":[]}), ("candidate", {"candidate":"0"*64})):
                    bad_path = out / ("bad-report-"+label+".json")
                    bad_path.write_text(json.dumps(dict(report, **changes)))
                    bad_binding = upload(bad_path, "runtime:report.json")
                    request("submit", dict(body, result=dict(result, artifacts=[bad_binding["artifact_id"]]), bindings=[bad_binding]), ok=False)
            request("submit", dict(body, result=dict(result, artifacts=[]), bindings=[]), ok=False)
            request("submit", dict(body, expected_revision="stale"), ok=False)
            request("submit", dict(body, fence=999), ok=False)
        probe = out / f"forbidden-submit-{iteration}.json"
        probe.write_text(json.dumps(dict(body, operation_id="forbidden", idempotency_key="forbidden"))); probe.chmod(0o600)
        client("b", "runner", "submit", "--runner", "agent-a", "--file", probe, expect_ok=False)
        # An active responsibility loses effective write permission immediately.
        if iteration == 1:
            api("POST", "/api/v1/projects/shared/members", {"username":"writer", "role":"reader"}, expected=201)
            if args.verify_recovery:
                revoked = client("a", "recovery", "check", "--session", "deterministic-a")
                assert revoked["status"] == "permission_revoked", revoked
            request("submit", body, ok=False)
            api("POST", "/api/v1/projects/shared/members", {"username":"writer", "role":"executor"}, expected=201)
        if iteration == 0:
            proxy.drop_next_submit = True
        submitted = request("submit", body)
        if args.verify_recovery:
            refreshed = client("a", "recovery", "save", "--session", "deterministic-a", "--runner", "agent-a")
            assert refreshed["online_verified"], refreshed
            if submitted["next"]["next_action"] == "completed":
                assert not refreshed["summary"].get("task"), refreshed
        if iteration == 0:
            assert proxy.drop_count == 1
        # Replaying the exact accepted submission returns the same immutable receipt.
        replay_file = out / f"request-{serial}.json"
        replay = client("a", "runner", "submit", "--runner", "agent-a", "--file", replay_file)
        assert {k:v for k,v in submitted.items() if k != "observed_at"} == {k:v for k,v in replay.items() if k != "observed_at"}
        key = json.loads(replay_file.read_text())["idempotency_key"]
        found = client("a", "runner", "receipt", "--runner", "agent-a", "--idempotency-key", key)
        assert found["found"] is True
        request("progress", dict(lease, summary="stale new write", stage=step), ok=False)
        records.append({"step":step, "status":result["status"], "next_action":submitted["next"]["next_action"]})
    final = client("b", "task", "show", "--task", "task")
    assert final["task"]["status"] == "completed" and did_fail, final
    assert any(item["status"] == "fail" for item in final["task"]["attempts"])
    assert client("a", "task", "show", "--task", "second")["task"]["status"] == "completed"
    assert client("a", "task", "show", "--task", "epic")["task"]["status"] != "completed"
    assert choice["action"] != "completed", choice
    # Revoking write capability does not turn an old receipt into new authority.
    api("POST", "/api/v1/projects/shared/members", {"username":"writer", "role":"reader"}, expected=201)
    client("a", "runner", "submit", "--runner", "agent-a", "--file", replay_file)
    assert client("a", "runner", "receipt", "--runner", "agent-a", "--idempotency-key", key)["found"] is True
    assert not (out / "runtime-a").exists() and not (out / "runtime-b").exists()
    assert not (projects["a"] / ".ael" / "work-items").exists()
    (out / "summary.json").write_text(json.dumps({"mode":"external deterministic test driver, not a real Agent", "records":records, "result":"PASS", "docker_client_dependency":False}, indent=2))
    print(json.dumps({"result":"PASS", "summary":str(out / "summary.json"), "records":records}))
finally:
    if proc is not None:
        proc.terminate()
        try: proc.wait(timeout=10)
        except subprocess.TimeoutExpired: proc.kill(); proc.wait()
    if log is not None: log.close()
    if proxy is not None: proxy.shutdown(); proxy.server_close()
    # Requests/claims contain disposable credentials. Keep them private and out of Git.
    for path in out.glob("*.json"): path.chmod(0o600)

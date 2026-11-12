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
parser.add_argument("--codex-smoke", action="store_true", help="attempt one real Codex responsibility in the disposable checkout")
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


executor_code = r'''import json, os
from pathlib import Path
assert "AEL_REMOTE_CONFIG_DIR" not in os.environ
assert "RUNNER_SENTINEL_SECRET" not in os.environ
assert os.environ["AEL_MANAGED_CHILD"] == "1"
step = os.environ["AEL_STEP"]
result = {"status": "pass", "summary": "fixture responsibility completed"}
if step == "execute":
    path = Path("app.txt")
    current = path.read_text()
    path.write_text("broken\n" if current == "original\n" else "fixed\n")
    result["artifacts"] = ["app.txt"]
    result["knowledge"] = {"status": "unchanged", "reason": "fixture rules were not changed"}
with open(os.environ["AEL_RESULT"], "w") as output:
    json.dump(result, output)
'''
check_command = [sys.executable, "-c", "from pathlib import Path; assert Path('app.txt').read_text() == 'fixed\\n'; print('fixture check passed')"]


def runner_config(name, executor, checks, capabilities=None, codex=False, max_steps=12, timeout=30):
    value = {"project_id": "shared", "repository": "fixture-repo", "checkout_id": "checkout-a", "workspace_id": "workspace-" + name,
             "runner_id": "runner-" + name, "workspace": str(projects["a"]), "runtime_dir": str(out / ("runner-runtime-" + name)),
             "capabilities": capabilities or ["development"], "executor": {"command": executor, "codex": codex, "max_steps": max_steps, "timeout_seconds": timeout}, "checks": checks}
    path = out / ("runner-" + name + ".json")
    path.write_text(json.dumps(value))
    path.chmod(0o600)
    return path


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
    proxy.drop_next_submit = True
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
    definition = {"id": "task", "title": "Repair fixture", "type": "bug", "goal": "verified", "scope": ["app.txt"], "acceptance": ["fixed"],
                  "knowledge": ["source:spec/rules.md"], "checks": [{"id": "unit", "kind": "functional", "method": "command", "command": check_command, "covers": ["fixed"], "required": True}]}
    taskFile = out / "task.json"
    taskFile.write_text(json.dumps(definition))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "task", "--idempotency-key", "task")
    configFile = runner_config("primary", [sys.executable, "-c", executor_code], [{"id": "unit", "command": check_command}])
    client("a", "runner", "register", "--file", configFile, "--operation-id", "register", "--idempotency-key", "register")
    outcome = client("a", "runner", "run", "--task", "task", "--file", configFile)
    assert outcome["action"] == "completed" and (projects["a"] / "app.txt").read_text() == "fixed\n"
    assert proxy.drop_count == 1 and len(list((out / "runner-runtime-primary" / "control" / "attempts").iterdir())) == 5
    observed = client("b", "task", "show", "--task", "task")
    history = observed["task"]["attempts"]
    assert observed["task"]["status"] == "completed"
    assert any(item["unit"] == "check:unit" and item["status"] == "fail" for item in history)
    assert any(item["unit"] == "check:unit" and item["status"] == "pass" for item in history)
    assert any(item["source"] == "remote-command:runner-primary" and item["result"] == "pass" for item in observed["task"]["evidence"])
    assert not (out / "server-data" / "app.txt").exists()

    recoveryTask = dict(definition, id="recovery", title="Lost response recovery")
    taskFile.write_text(json.dumps(recoveryTask))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "recovery-task", "--idempotency-key", "recovery-task")
    recoveryConfig = runner_config("recovery", [sys.executable, "-c", executor_code], [{"id": "unit", "command": check_command}])
    client("a", "runner", "register", "--file", recoveryConfig, "--operation-id", "register-recovery", "--idempotency-key", "register-recovery")
    proxy.drop_next_submit = True
    proxy.drop_next_lookup = True
    client("a", "runner", "run", "--task", "recovery", "--file", recoveryConfig, expect_ok=False)
    assert "RUNNER_RESULT_PENDING" in commands[-1]["stderr"] and proxy.drop_count == 2
    pending = list((out / "runner-runtime-recovery" / "control" / "attempts").glob("*/pending-submission.json"))
    assert len(pending) == 1 and pending[0].stat().st_mode & 0o077 == 0
    client("a", "runner", "resume-result", "--file", recoveryConfig, "--pending", pending[0])
    assert not pending[0].exists()
    recoveryOutcome = client("a", "runner", "run", "--task", "recovery", "--file", recoveryConfig)
    assert recoveryOutcome["action"] == "completed" and len(list((out / "runner-runtime-recovery" / "control" / "attempts").iterdir())) == 3

    scenarioCode = r'''import json, os
from pathlib import Path
context = json.loads(Path(os.environ["AEL_CONTEXT"]).read_text())
step = os.environ["AEL_STEP"]
if step == "verify":
    actual = Path("app.txt").read_text()
    report = Path(os.environ["AEL_RESULT"]).parent / "scenario.txt"
    report.write_text("fixture scenario inspected app.txt\n")
    result = {"task": context["responsibility"]["task"], "check": "review", "candidate": context["candidate"], "method": "scenario", "executor": context["responsibility"]["runner"], "result": "pass" if actual == "fixed\n" else "fail", "reason": "fixture scenario inspected the file", "inputs": ["app.txt"], "artifacts": ["runtime:scenario.txt"], "return_to": ""}
else:
    result = {"status": "pass", "summary": "fixture responsibility completed"}
    if step == "execute":
        Path("app.txt").write_text("fixed\n")
        result["artifacts"] = ["app.txt"]
        result["knowledge"] = {"status": "unchanged", "reason": "fixture rules were not changed"}
with open(os.environ["AEL_RESULT"], "w") as output: json.dump(result, output)
'''
    scenarioTask = dict(definition, id="scenario", title="Scenario review", checks=[{"id": "review", "kind": "functional", "method": "scenario", "covers": ["fixed"], "required": True}])
    taskFile.write_text(json.dumps(scenarioTask))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "scenario-task", "--idempotency-key", "scenario-task")
    scenarioConfig = runner_config("scenario", [sys.executable, "-c", scenarioCode], [], ["development", "scenario"])
    client("a", "runner", "register", "--file", scenarioConfig, "--operation-id", "register-scenario", "--idempotency-key", "register-scenario")
    scenarioOutcome = client("a", "runner", "run", "--task", "scenario", "--file", scenarioConfig)
    assert scenarioOutcome["action"] == "completed"
    scenarioView = client("b", "task", "show", "--task", "scenario")["task"]
    assert any(item["source"] == "scenario:runner-scenario" and item["result"] == "pass" for item in scenarioView["evidence"])

    (projects["a"] / "app.txt").write_text("original\n")
    humanTask = dict(definition, id="human", title="Human review", checks=[{"id": "human-review", "kind": "review", "method": "human", "covers": ["fixed"], "required": True}])
    taskFile.write_text(json.dumps(humanTask))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "human-task", "--idempotency-key", "human-task")
    humanConfig = runner_config("human", [sys.executable, "-c", executor_code], [])
    client("a", "runner", "register", "--file", humanConfig, "--operation-id", "register-human", "--idempotency-key", "register-human")
    humanOutcome = client("a", "runner", "run", "--task", "human", "--file", humanConfig)
    assert humanOutcome["action"] == "wait_human" and client("b", "task", "show", "--task", "human")["task"]["status"] == "open"

    missing = dict(definition, id="missing", title="Missing result")
    taskFile.write_text(json.dumps(missing))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "missing-task", "--idempotency-key", "missing-task")
    noresultConfig = runner_config("noresult", [sys.executable, "-c", "print('zero exit without result')"], [])
    client("a", "runner", "register", "--file", noresultConfig, "--operation-id", "register-noresult", "--idempotency-key", "register-noresult")
    missingOutcome = client("a", "runner", "run", "--task", "missing", "--file", noresultConfig)
    assert missingOutcome["action"] == "wait_environment"
    assert client("b", "task", "show", "--task", "missing")["task"]["status"] != "completed"

    noEvidence = dict(definition, id="no-evidence", title="Missing deliverable")
    taskFile.write_text(json.dumps(noEvidence))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "no-evidence-task", "--idempotency-key", "no-evidence-task")
    noEvidenceCode = r'''import json, os
result = {"status": "pass", "summary": "fixture returned no deliverable"}
if os.environ["AEL_STEP"] == "execute": result["knowledge"] = {"status": "unchanged", "reason": "fixture rules unchanged"}
with open(os.environ["AEL_RESULT"], "w") as output: json.dump(result, output)
'''
    noEvidenceConfig = runner_config("no-evidence", [sys.executable, "-c", noEvidenceCode], [])
    client("a", "runner", "register", "--file", noEvidenceConfig, "--operation-id", "register-no-evidence", "--idempotency-key", "register-no-evidence")
    missingEvidence = client("a", "runner", "run", "--task", "no-evidence", "--file", noEvidenceConfig)
    assert missingEvidence["action"] == "wait_environment" and client("b", "task", "show", "--task", "no-evidence")["task"]["status"] != "completed"

    (projects["a"] / "app.txt").write_text("original\n")
    unauthorizedCommand = [sys.executable, "-c", "from pathlib import Path; Path('unauthorized-marker').write_text('must not run')"]
    unauthorized = dict(definition, id="unauthorized", title="Unauthorized check", checks=[{"id": "unapproved", "kind": "functional", "method": "command", "command": unauthorizedCommand, "covers": ["fixed"], "required": True}])
    taskFile.write_text(json.dumps(unauthorized))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "unauthorized-task", "--idempotency-key", "unauthorized-task")
    deniedConfig = runner_config("denied", [sys.executable, "-c", executor_code], [])
    client("a", "runner", "register", "--file", deniedConfig, "--operation-id", "register-denied", "--idempotency-key", "register-denied")
    denied = client("a", "runner", "run", "--task", "unauthorized", "--file", deniedConfig)
    assert denied["action"] == "wait_environment" and not (projects["a"] / "unauthorized-marker").exists()
    assert not any((out / "runner-runtime-denied" / "io" / "attempts").rglob("unauthorized-marker"))

    (projects["a"] / "app.txt").write_text("original\n")
    ambiguous = dict(definition, id="ambiguous", title="Ambiguous review method", checks=[{"id": "mixed", "kind": "functional", "method": "scenario", "command": unauthorizedCommand, "covers": ["fixed"], "required": True}])
    taskFile.write_text(json.dumps(ambiguous))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "ambiguous-task", "--idempotency-key", "ambiguous-task")
    ambiguousConfig = runner_config("ambiguous", [sys.executable, "-c", executor_code], [{"id": "mixed", "command": unauthorizedCommand}], ["development", "scenario"])
    client("a", "runner", "register", "--file", ambiguousConfig, "--operation-id", "register-ambiguous", "--idempotency-key", "register-ambiguous")
    client("a", "runner", "run", "--task", "ambiguous", "--file", ambiguousConfig, expect_ok=False)
    assert "CHECK_METHOD_MISMATCH" in commands[-1]["stderr"] and not (projects["a"] / "unauthorized-marker").exists()
    assert not any((out / "runner-runtime-ambiguous" / "io" / "attempts").rglob("unauthorized-marker"))

    badWorkspaceConfig = out / "runner-bad-workspace.json"
    unauthorizedConfig = json.loads(configFile.read_text())
    unauthorizedConfig["workspace"] = str(projects["b"])
    badWorkspaceConfig.write_text(json.dumps(unauthorizedConfig)); badWorkspaceConfig.chmod(0o600)
    client("a", "runner", "run", "--task", "task", "--file", badWorkspaceConfig, expect_ok=False)
    assert "RUNNER_WORKSPACE_UNAUTHORIZED" in commands[-1]["stderr"]

    # Even if the runner regresses, the fixture wrapper never performs a push.
    realGit = shutil.which("git", path=env.get("PATH"))
    fakeBin = out / "fake-bin"; fakeBin.mkdir()
    pushMarker = out / "push-would-have-run"
    wrapper = fakeBin / "git"
    wrapper.write_text("#!" + sys.executable + "\nimport os,sys\nif 'push' in sys.argv[1:] or 'tag' in sys.argv[1:]:\n open(" + repr(str(pushMarker)) + ", 'w').write('blocked fixture')\n sys.exit(42)\nos.execv(" + repr(realGit) + ", [" + repr(realGit) + "] + sys.argv[1:])\n")
    wrapper.chmod(0o700)
    env["PATH"] = str(fakeBin) + os.pathsep + env.get("PATH", "")
    (projects["a"] / "app.txt").write_text("original\n")
    publishCommand = ["git", "push", "origin", "main"]
    publishTask = dict(definition, id="publish-check", title="Forbidden publication", checks=[{"id": "publish", "kind": "functional", "method": "command", "command": publishCommand, "covers": ["fixed"], "required": True}])
    taskFile.write_text(json.dumps(publishTask))
    client("a", "task", "create", "--file", taskFile, "--operation-id", "publish-task", "--idempotency-key", "publish-task")
    publishConfig = runner_config("publish", [sys.executable, "-c", executor_code], [{"id": "publish", "command": publishCommand}])
    client("a", "runner", "register", "--file", publishConfig, "--operation-id", "register-publish", "--idempotency-key", "register-publish")
    stopped = client("a", "runner", "run", "--task", "publish-check", "--file", publishConfig)
    assert stopped["action"] == "wait_environment" and not pushMarker.exists()

    (projects["a"] / "app.txt").write_text("original\n")
    parentFile = out / "parent.json"
    parentFile.write_text(json.dumps({"id": "parent", "title": "Sequential parent", "type": "epic", "goal": "verified", "acceptance": ["overall"]}))
    client("a", "task", "create", "--file", parentFile, "--operation-id", "parent", "--idempotency-key", "parent")
    for leaf in ("leaf-1", "leaf-2"):
        taskFile.write_text(json.dumps(dict(definition, id=leaf, title=leaf, parent="parent")))
        client("a", "task", "create", "--file", taskFile, "--operation-id", leaf, "--idempotency-key", leaf)
    parentOutcome = client("a", "runner", "run", "--parent", "parent", "--file", configFile)
    assert parentOutcome["action"] == "wait_integration"
    leaves = [client("b", "task", "show", "--task", leaf)["task"] for leaf in ("leaf-1", "leaf-2")]
    assert all(item["status"] == "completed" for item in leaves)
    assert datetime.datetime.fromisoformat(leaves[0]["completed"].replace("Z", "+00:00")) <= datetime.datetime.fromisoformat(leaves[1]["attempts"][0]["started"].replace("Z", "+00:00"))
    assert client("b", "task", "show", "--task", "parent")["task"]["status"] == "open"
    smoke = {"status": "not_requested"}
    if args.codex_smoke:
        smokeTask = dict(definition, id="codex-smoke", title="Isolated Codex smoke")
        taskFile.write_text(json.dumps(smokeTask))
        client("a", "task", "create", "--file", taskFile, "--operation-id", "codex-task", "--idempotency-key", "codex-task")
        codexConfig = runner_config("codex", [], [], codex=True, max_steps=1, timeout=45)
        client("a", "runner", "register", "--file", codexConfig, "--operation-id", "register-codex", "--idempotency-key", "register-codex")
        try:
            codexOutcome = client("a", "runner", "run", "--task", "codex-smoke", "--file", codexConfig)
            codexTask = client("b", "task", "show", "--task", "codex-smoke")["task"]
            smoke = {"status": "passed" if codexOutcome["action"] == "max_steps" and codexTask["current_step"] != "define" else "blocked", "action": codexOutcome["action"], "task_status": codexTask["status"], "logs": str(out / "runner-runtime-codex" / "io" / "attempts")}
        except RuntimeError:
            smoke = {"status": "blocked", "reason": "Codex runner did not complete; inspect its isolated attempt and command logs", "logs": str(out / "runner-runtime-codex" / "io" / "attempts")}
    for name, project in projects.items():
        assert not (project / ".ael" / "work-items").exists()
        assert not (out / f"runtime-{name}").exists()
    print(json.dumps({"status": "passed", "isolated": True, "repair": "failed check then fixed and passed", "missing_result": "blocked", "unauthorized_check": "not executed", "codex_smoke": smoke}))
finally:
    if proxy:
        proxy.shutdown()
        proxy.server_close()
    if proc and proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=10)
    if log:
        log.close()
    (out / "commands.json").write_text(json.dumps(commands, ensure_ascii=False, indent=2))

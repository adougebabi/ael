#!/usr/bin/env python3
"""Exercise two independent CLI clients against one isolated packaged Server."""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import platform
import select
import subprocess
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--bundle", required=True)
parser.add_argument("--out", required=True)
args = parser.parse_args()
bundle, out = Path(args.bundle).resolve(), Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("refusing a nonempty fixture directory")
out.mkdir(parents=True, exist_ok=True)
(out / "ISOLATED-REMOTE-TASKS").write_text("Disposable server and clients; no business project or real credentials.\n")
system = {"darwin": "darwin", "linux": "linux"}.get(platform.system().lower())
arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit("unsupported fixture platform")
binary = bundle / f"ael-{system}-{arch}"
server_work, data, requests = out / "server-work", out / "server-data", out / "requests"
server_work.mkdir(); requests.mkdir()
projects = {name: out / f"client-{name}" for name in ("a", "b")}
for project in projects.values():
    project.mkdir()
env = dict(os.environ, AEL_DATA_DIR=str(out / "unused-runtime"), AEL_INSTALL_PREFIX=str(out / "unused-install"))
commands = []


def run(argv, cwd, command_env, input_text=None, expect_ok=True):
    result = subprocess.run([str(x) for x in argv], cwd=cwd, env=command_env, input=input_text, capture_output=True, text=True, timeout=45)
    commands.append({"argv": [str(x) for x in argv], "exit_code": result.returncode, "stdout": result.stdout, "stderr": result.stderr})
    if (result.returncode == 0) != expect_ok:
        raise RuntimeError(f"unexpected exit for {argv[1:3]}: {result.stderr or result.stdout}")
    return json.loads(result.stdout) if result.stdout else {}


def write(name, value):
    path = requests / (name + ".json")
    path.write_text(json.dumps(value, ensure_ascii=False))
    return path


def client(name, *argv, input_text=None, expect_ok=True):
    client_env = dict(env, AEL_REMOTE_CONFIG_DIR=str(out / f"config-{name}"), AEL_DATA_DIR=str(out / f"runtime-{name}"))
    return run([binary, *argv, "--project", projects[name], "--json"], projects[name], client_env, input_text, expect_ok)


proc = None
server_log = None
try:
    run([binary, "server", "bootstrap", "--data-dir", data, "--username", "owner", "--password-stdin", "--json"], server_work, env, "fixture-admin-password\n")

    def start(address):
        global proc, server_log
        server_log = (out / ("server-restart.log" if address else "server-first.log")).open("w")
        proc = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(data), "--addr", address or "127.0.0.1:0", "--json"],
                                cwd=server_work, env=env, stdout=subprocess.PIPE, stderr=server_log, text=True)
        ready, _, _ = select.select([proc.stdout], [], [], 30)
        if not ready:
            raise RuntimeError("server did not announce a temporary port")
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("server exited before readiness; see fixture log")
        return json.loads(line)["url"]

    def stop():
        global proc, server_log
        if proc and proc.poll() is None:
            proc.terminate()
        if proc:
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill(); proc.wait(timeout=10)
        if server_log:
            server_log.close()
        proc, server_log = None, None

    base = start("")
    addr = urllib.parse.urlparse(base).netloc
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    csrf = ""

    def api(method, path, payload=None, bearer=None, expected=200):
        data_bytes = json.dumps(payload).encode() if payload is not None else None
        headers = {}
        if payload is not None:
            headers.update({"Content-Type": "application/json", "X-AEL-Protocol": "1", "Origin": base})
            if csrf:
                headers["X-AEL-CSRF"] = csrf
        if bearer:
            headers["Authorization"] = "Bearer " + bearer
        request = urllib.request.Request(base + path, data=data_bytes, headers=headers, method=method)
        try:
            with opener.open(request, timeout=15) as response:
                code, body = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, body = error.code, error.read()
        if code != expected:
            raise RuntimeError(f"API {method} {path} returned {code}, expected {expected}")
        return json.loads(body) if body else {}

    csrf = api("POST", "/api/v1/auth/login", {"username": "owner", "password": "fixture-admin-password"})["csrf"]
    api("POST", "/api/v1/projects", {"id": "shared", "name": "Shared"}, expected=201)
    api("POST", "/api/v1/projects", {"id": "other", "name": "Other"}, expected=201)
    tokens = {}
    for name in ("a", "b"):
        username = "client-" + name
        api("POST", "/api/v1/users", {"username": username, "password": "fixture-client-password"}, expected=201)
        api("POST", "/api/v1/projects/shared/members", {"username": username, "role": "executor"}, expected=201)
        tokens[name] = api("POST", "/api/v1/tokens", {"username": username, "project_id": "shared", "label": "client " + name}, expected=201)
        client(name, "server", "profile", "add", "--name", "central", "--url", base, "--token-stdin", input_text=tokens[name]["token"] + "\n")
        connected = client(name, "project", "connect", "--profile", "central", "--remote-project", "shared")
        assert connected["mode"] == "remote" and connected["online"] is True

    parent = {"id": "parent", "title": "Parent original", "type": "epic", "goal": "verified", "acceptance": ["overall"]}
    parent_file = write("parent", parent)
    first = client("a", "task", "create", "--file", parent_file, "--operation-id", "parent-op", "--idempotency-key", "parent-key")
    child_file = write("child", {"id": "child", "parent": "parent", "title": "Child", "type": "bug", "goal": "verified", "acceptance": ["result"]})
    client("a", "task", "create", "--file", child_file, "--operation-id", "child-op", "--idempotency-key", "child-key")
    seen = client("b", "task", "show", "--task", "child")
    assert seen["task"]["definition"]["parent"] == "parent"
    listing = client("b", "task", "list", "--parent", "parent")
    assert listing["total"] == 1 and listing["items"][0]["task"]["definition"]["id"] == "child"
    before = client("b", "task", "show", "--task", "parent")
    changed = dict(parent, title="Parent changed")
    changed_file = write("changed", changed)
    client("a", "task", "edit", "--task", "parent", "--file", changed_file, "--reason", "fixture edit", "--operation-id", "edit-a", "--idempotency-key", "edit-key-a", "--expected-revision", first["revision"])
    stale_file = write("stale", dict(parent, title="Stale edit"))
    client("b", "task", "edit", "--task", "parent", "--file", stale_file, "--reason", "stale edit", "--operation-id", "edit-b", "--idempotency-key", "edit-key-b", "--expected-revision", before["revision"], expect_ok=False)
    assert "REVISION_CONFLICT" in commands[-1]["stderr"]
    replay = client("a", "task", "create", "--file", parent_file, "--operation-id", "parent-op", "--idempotency-key", "parent-key")
    assert replay["task"]["definition"]["title"] == "Parent original" and replay["revision"] == first["revision"]
    receipt = client("a", "operation", "show", "--idempotency-key", "parent-key")
    assert receipt["operation_id"] == "parent-op" and receipt["revision"] == first["revision"]
    api("GET", "/api/v1/projects/other/tasks/parent", bearer=tokens["b"]["token"], expected=404)

    plan_parent = {"id": "plan-parent", "title": "Plan parent", "type": "epic", "goal": "verified", "acceptance": ["overall"]}
    plan_parent_receipt = client("a", "task", "create", "--file", write("plan-parent", plan_parent), "--operation-id", "plan-parent-op", "--idempotency-key", "plan-parent-key")
    plan_input = {"request": "one isolated result", "items": []}
    prepared = client("a", "plan", "prepare", "--task", "plan-parent", "--file", write("plan-input", plan_input), "--operation-id", "prepare-op", "--idempotency-key", "prepare-key", "--expected-revision", plan_parent_receipt["revision"])
    leaf_definition = {"title": "One", "type": "bug", "goal": "verified", "scope": ["one.txt"], "non_goals": ["no release"], "acceptance": ["result"], "checks": [{"id": "unit", "kind": "functional", "method": "command", "command": ["sh", "-c", "true"], "covers": ["result"], "required": True}]}
    proposal = {"version": 1, "id": "plan-op", "parent": "plan-parent", "parent_revision": 1, "input_hash": prepared["input_hash"], "request": plan_input["request"], "user_items": [],
                "leaves": [{"key": "one", "definition": leaf_definition, "deliverables": ["one.txt"], "basis": ["fixture"], "capabilities": ["development"], "order": 1, "required": True}],
                "coverage": [{"acceptance": "overall", "leaves": ["one"], "integration": "check integration"}]}
    proposal_file = write("proposal", proposal)
    preview = client("a", "plan", "preview", "--file", proposal_file)
    applied = client("a", "plan", "apply", "--file", proposal_file, "--digest", preview["digest"], "--operation-id", "plan-op", "--idempotency-key", "plan-op", "--expected-revision", plan_parent_receipt["revision"])
    assert applied["status"] == "applied"
    plan_replay = client("a", "plan", "apply", "--file", proposal_file, "--digest", preview["digest"], "--operation-id", "plan-op", "--idempotency-key", "plan-op", "--expected-revision", plan_parent_receipt["revision"])
    assert plan_replay["operation_id"] == applied["operation_id"] and plan_replay["result"] == applied["result"]
    assert client("b", "task", "tree", "--task", "plan-parent")["required"] == 1

    api("POST", "/api/v1/tokens/revoke", {"id": tokens["a"]["id"]})
    client("a", "task", "create", "--file", child_file, "--operation-id", "revoked-op", "--idempotency-key", "revoked-key", expect_ok=False)
    assert "AUTH_REQUIRED" in commands[-1]["stderr"]
    stale_dir = projects["b"] / ".ael" / "records"
    stale_dir.mkdir()
    (stale_dir / "fake.json").write_text('{"id":"fake","status":"completed"}')
    stop()
    cached = client("b", "task", "show", "--task", "parent")
    assert cached["online"] is False and cached["authoritative"] is False and cached["cached_at"]
    client("b", "task", "create", "--file", child_file, "--operation-id", "offline-op", "--idempotency-key", "offline-key", expect_ok=False)
    assert "REMOTE_UNAVAILABLE" in commands[-1]["stderr"]
    for name, project in projects.items():
        assert not (project / ".ael" / "work-items").exists()
        assert not (out / f"runtime-{name}").exists()
    assert (stale_dir / "fake.json").read_text() == '{"id":"fake","status":"completed"}'
    assert start(addr) == base
    restored = client("b", "task", "show", "--task", "parent")
    assert restored["online"] is True and restored["task"]["definition"]["title"] == "Parent changed"
    print(json.dumps({"status": "passed", "isolated": True, "clients": 2, "server": base, "offline_cache": True, "plan": "applied and replayed"}))
finally:
    if proc and proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait(timeout=10)
    if server_log:
        server_log.close()
    (out / "commands.json").write_text(json.dumps(commands, ensure_ascii=False, indent=2))

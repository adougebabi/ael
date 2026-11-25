#!/usr/bin/env python3
"""Initialize a disposable copy of a consumer repository and exercise fresh clones."""
import argparse
import concurrent.futures
import http.cookiejar
import json
import os
import platform
import secrets
import socket
import subprocess
import tempfile
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--source", required=True, type=Path)
parser.add_argument("--bundle", required=True, type=Path)
parser.add_argument("--project-only", action="store_true")
parser.add_argument("--upgrade-bundle", type=Path, help="exercise machine upgrade and portable project update before cloning")
args = parser.parse_args()
source, bundle = args.source.resolve(), args.bundle.resolve()
arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}[platform.machine().lower()]
cli = bundle / f"ael-{platform.system().lower()}-{arch}"
password = secrets.token_urlsafe(32)
with tempfile.TemporaryDirectory(prefix="ael-clone-runner-") as tmp:
    tmp = Path(tmp).resolve()

    def environment(name):
        home = tmp / ("home-" + name)
        home.mkdir(exist_ok=True)
        return {"HOME": str(home), "PATH": "/usr/bin:/bin", "XDG_CONFIG_HOME": str(home / "config"),
                "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull, "GIT_TERMINAL_PROMPT": "0"}

    def run(argv, cwd=tmp, env=None, secret=None):
        result = subprocess.run(argv, cwd=cwd, env=env or environment("setup"), text=True,
                                input=secret, capture_output=True, timeout=180)
        if result.returncode:
            raise RuntimeError(f"{argv[:3]}: {result.stderr[:1600]}")
        return result.stdout.strip()

    def git(*argv, cwd=tmp):
        return run(["git", *argv], cwd=cwd)

    seed = tmp / "maintainer"
    git("clone", "--quiet", "--no-local", str(source), str(seed))
    head = git("rev-parse", "HEAD", cwd=seed)
    binding_path = seed / ".ael/remote.json"
    existing = json.loads(binding_path.read_text()) if binding_path.exists() else {}
    project = existing.get("project_id", "clone-test")
    if (seed / ".ael/runner-access.json").exists():
        raise RuntimeError("fixture source already has a live clone grant; use a pre-initialization revision")
    data = tmp / "server"
    run([cli, "server", "bootstrap", "--data-dir", data, "--username", "owner", "--password-stdin"], secret=password+"\n")
    server_cli = args.upgrade_bundle.resolve() / cli.name if args.upgrade_bundle else cli
    with subprocess.Popen([server_cli, "server", "start", "--data-dir", data, "--addr", "127.0.0.1:0", "--json"],
                          env=environment("server"), cwd=tmp, stdout=subprocess.PIPE, text=True) as server:
        try:
            base = json.loads(server.stdout.readline())["url"]
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            def post(path, body, csrf=""):
                req = urllib.request.Request(base+path, data=json.dumps(body).encode(), headers={
                    "Origin": base, "Content-Type": "application/json", "X-AEL-CSRF": csrf, "X-AEL-Protocol": "1"})
                with opener.open(req, timeout=15) as response:
                    return json.load(response)
            csrf = post("/api/v1/auth/login", {"username": "owner", "password": password})["csrf"]
            if existing:
                post("/api/v1/projects", {"id": project, "name": "Clone verification"}, csrf)
                choice = ["--remote-project", project]
            else:
                choice = ["--create", "Clone verification"]
            init = [cli, "init", "--server", base, *choice, "--username", "owner",
                    "--password-stdin", "--from", bundle, "--host", "none", "--update-policy", "notify", "--json"]
            if args.project_only:
                init.append("--project-only")
            result = json.loads(run(init, cwd=seed, secret=password+"\n"))
            assert result["clone_ready"]
            descriptor = json.loads((seed / ".ael/runner-access.json").read_text())
            assert descriptor["url"] == base
            target_version = result["cli_version"]
            if args.upgrade_bundle:
                grant = (seed / ".ael/runner-access.json").read_bytes()
                runtime_lock = seed / ".ael/clone-runtime/toolchain.lock"
                previous_lock = runtime_lock.read_bytes()
                previous_version = result["cli_version"]
                if args.project_only:
                    assert not result["cli_installation"]
                    assert json.loads(run(["./ael", "init", "--json"], seed))["clone_ready"]
                    check = subprocess.run(["./ael","doctor","--json"], cwd=seed, env=environment("setup"), capture_output=True, text=True, check=True)
                    assert json.loads(check.stdout)["online"] and "upgrade" in check.stderr, check.stderr
                    pinned = json.loads(run(["./ael", "session", "start", "--session", "before-upgrade", "--json"], seed))
                    assert Path(pinned["session"]["cli"]["executable"]).is_relative_to(seed / ".ael/clone-runtime")
                    assert not (Path(environment("setup")["HOME"]) / ".local/bin/ael").exists()
                upgrade = json.loads(run(["./ael", "upgrade", "--from", args.upgrade_bundle.resolve(), "--json"], seed))
                target_version = upgrade["available"]["version"]
                if args.project_only:
                    assert json.loads(run(["./ael", "version", "--json"], seed))["version"] == target_version
                    assert json.loads(run(["./ael", "version", "--session", "before-upgrade", "--json"], seed))["version"] == previous_version
                    assert json.loads((seed / ".ael/remote-assets.json").read_text())["version"] == previous_version
                    update = ["./ael", "update"]
                else:
                    assert runtime_lock.read_bytes() == previous_lock
                    update = [upgrade["target"], "update", "--project", seed]
                lock_before_preview = runtime_lock.read_bytes()
                preview = json.loads(run([*update, "--dry-run", "--json"], seed))
                assert preview["status"] == "preview" and runtime_lock.read_bytes() == lock_before_preview
                applied = json.loads(run([*update, "--json"], seed))
                assert applied["status"] == "updated"
                assert runtime_lock.read_text().splitlines()[0] == "version " + target_version
                assert (seed / ".ael/runner-access.json").read_bytes() == grant
                assert json.loads(run(["./ael", "update", "--json"], seed))["status"] == "up_to_date"
            git("add", "-A", cwd=seed)
            git("-c", "user.name=AEL Fixture", "-c", "user.email=ael@localhost", "commit", "-qm", "Initialize clone runners", cwd=seed)

            def create(name):
                path = tmp / name
                git("clone", "--quiet", "--no-local", str(seed), str(path))
                def command(*argv):
                    return json.loads(run(["./ael", *argv, "--json"], path, environment(name)))
                assert command("version")["version"] == target_version
                with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                    doctors = list(pool.map(lambda _: command("doctor"), range(2)))
                runner = doctors[0]["runner"]
                assert runner == doctors[1]["runner"]
                assert runner["alias"] == socket.gethostname() and runner["registered"]
                assert command("runner", "show")["id"] == runner["id"]
                assert command("task", "list")["online"]
                assert command("version", "--check")["compatible"]
                assert git("status", "--porcelain", cwd=path) == ""
                return runner, command

            first, command = create("runner-a")
            second, _ = create("runner-b")
            assert first["id"] != second["id"]
            (tmp / "runner-b").rename(tmp / "previous-runner-b")
            replacement, _ = create("runner-b")
            assert replacement["id"] not in (first["id"], second["id"])
            session = command("session", "start", "--session", "acceptance")
            assert session["session"]["runner"] == first["id"]
            assert Path(session["session"]["cli"]["executable"]).is_relative_to(tmp / "runner-a/.ael/clone-runtime")
            assert not (Path(environment("runner-a")["HOME"]) / ".local/share/ael/clients").exists()
            task = "clone-verification"
            definition = tmp / "definition.json"
            definition.write_text(json.dumps({"id":task,"title":"Inspect the project README from a fresh checkout","scope":["README.md"],"type":"bug","goal":"verified","acceptance":["works"]}))
            command("task","create","--file",str(definition),"--operation-id","create","--idempotency-key","create")
            rev = command("task","show","--task",task)["revision"]
            observation = command("candidate","capture","--task",task,"--expected-revision",rev,"--operation-id","capture","--idempotency-key","capture")
            context = command("context","publish","--task",task,"--observation",observation["id"],"--expected-revision",rev,"--operation-id","context","--idempotency-key","context")
            claim = tmp / "claim.json"
            claim.write_text(json.dumps({"task_id":task,"context_id":context["id"],"expected_revision":rev,"operation_id":"claim","idempotency_key":"claim"}))
            claim.chmod(0o600)
            received = command("runner","claim","--session","acceptance","--file",str(claim),"--out",str(tmp/"lease.json"))
            assert received["attempt"]["id"]
            print(json.dumps({"source":str(source),"source_commit":head,"result":"PASS","runner_ids":[first["id"],second["id"]],
                              "alias":first["alias"],"claimed":task}))
        finally:
            server.terminate()

#!/usr/bin/env python3
"""Exercise packaged center artifacts with two isolated clients and a browser."""
import argparse
import base64
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import platform
import select
import sqlite3
import struct
import subprocess
import urllib.error
import urllib.parse
import urllib.request
import zlib


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--bundle", required=True)
parser.add_argument("--out", required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
bundle, out = Path(args.bundle).resolve(), Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("refusing a nonempty fixture directory")
out.mkdir(parents=True, exist_ok=True)
(out / "ISOLATED-ARTIFACTS").write_text("Disposable server, accounts and clients only.\n")
system = {"darwin": "darwin", "linux": "linux"}.get(platform.system().lower())
arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit("unsupported fixture platform")
binary = bundle / f"ael-{system}-{arch}"
env = dict(os.environ, AEL_DATA_DIR=str(out / "unused-runtime"), AEL_INSTALL_PREFIX=str(out / "unused-install"))
projects = {name: out / f"client-{name}" for name in ("a", "b")}
for directory in projects.values():
    directory.mkdir()
commands = []


def run(argv, cwd, run_env, input_text=None, expect_ok=True):
    process = subprocess.run([str(x) for x in argv], cwd=cwd, env=run_env, input=input_text,
                             capture_output=True, text=True, timeout=45)
    commands.append({"argv": [str(x) for x in argv], "exit": process.returncode,
                     "stdout": process.stdout, "stderr": process.stderr})
    if (process.returncode == 0) != expect_ok:
        raise RuntimeError(f"unexpected command result: {argv}; {process.stderr}")
    return json.loads(process.stdout) if expect_ok else None


def client(name, *argv, input_text=None, expect_ok=True):
    client_env = dict(env, AEL_REMOTE_CONFIG_DIR=str(out / f"config-{name}"), AEL_DATA_DIR=str(out / f"runtime-{name}"))
    return run([binary, *argv, "--project", projects[name], "--json"], projects[name], client_env, input_text, expect_ok)


def png():
    def chunk(kind, body):
        return struct.pack(">I", len(body)) + kind + body + struct.pack(">I", zlib.crc32(kind + body))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(b"\x00\xff\x00\x00\x00\xff\x00" * 2)) + chunk(b"IEND", b""))


proc, server_log = None, None
try:
    run([binary, "server", "bootstrap", "--data-dir", out / "server-data", "--username", "owner", "--password-stdin", "--json"], out, env, "fixture-admin-password\n")
    server_log = (out / "server.log").open("w")
    proc = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(out / "server-data"), "--addr", "127.0.0.1:0", "--json"],
                            cwd=out, env=env, stdout=subprocess.PIPE, stderr=server_log, text=True)
    ready, _, _ = select.select([proc.stdout], [], [], 30)
    if not ready:
        raise RuntimeError("server did not announce its address")
    base = json.loads(proc.stdout.readline())["url"]
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
        req = urllib.request.Request(base + path, data=body, headers=headers, method=method)
        try:
            with opener.open(req, timeout=15) as response:
                status, data = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, data = error.code, error.read()
        if status != expected:
            raise RuntimeError(f"{method} {path}: HTTP {status}, expected {expected}: {data[:300]!r}")
        return json.loads(data) if data else {}

    csrf = api("POST", "/api/v1/auth/login", {"username": "owner", "password": "fixture-admin-password"})["csrf"]
    api("POST", "/api/v1/projects", {"id": "shared", "name": "Shared Evidence"}, expected=201)
    api("POST", "/api/v1/projects", {"id": "other", "name": "Other"}, expected=201)
    tokens = {}
    for name, username, project, role in (("a", "writer", "shared", "executor"), ("b", "reader", "shared", "reader"), ("other", "outsider", "other", "executor")):
        api("POST", "/api/v1/users", {"username": username, "password": f"fixture-{username}-password"}, expected=201)
        api("POST", f"/api/v1/projects/{project}/members", {"username": username, "role": role}, expected=201)
        tokens[name] = api("POST", "/api/v1/tokens", {"username": username, "project_id": project, "label": name}, expected=201)["token"]
        if name != "other":
            client(name, "server", "profile", "add", "--name", "central", "--url", base, "--token-stdin", input_text=tokens[name] + "\n")
            client(name, "project", "connect", "--profile", "central", "--remote-project", "shared")
    definition = out / "task.json"
    definition.write_text(json.dumps({"id": "task", "title": "Evidence task", "type": "bug", "goal": "verified", "acceptance": ["report"]}))
    task = client("a", "task", "create", "--file", definition, "--operation-id", "task", "--idempotency-key", "task")
    revision = task["revision"]
    contents = {"screen.png": png(), "report.txt": b"Synthetic cross-device report\nResult: verified bytes.\n"}
    ids = {}
    for index, (name, data) in enumerate(contents.items()):
        file = projects["a"] / name
        file.write_bytes(data)
        command = ("artifact", "upload", "--task", "task", "--file", file, "--expected-revision", revision,
                   "--operation-id", f"upload-{index}", "--idempotency-key", f"upload-{index}")
        uploaded = client("a", *command)
        replayed = client("a", *command)
        assert uploaded["id"] == replayed["id"] and uploaded["sha256"] == hashlib.sha256(data).hexdigest()
        ids[name] = uploaded["id"]
        file.unlink()
        target = projects["b"] / name
        downloaded = client("b", "artifact", "download", "--artifact", uploaded["id"], "--out", target)
        assert target.read_bytes() == data and downloaded["sha256"] == hashlib.sha256(data).hexdigest()
        client("b", "artifact", "download", "--artifact", uploaded["id"], "--out", target, expect_ok=False)
        assert target.read_bytes() == data
        api("GET", f"/api/v1/projects/shared/artifacts/{uploaded['id']}/content", bearer=tokens["other"], expected=404)

    lost_file = projects["a"] / "lost.txt"
    lost_data = b"the local source is removed after a lost response\n"
    lost_file.write_bytes(lost_data)
    lost_body = {"operation_id": "lost", "idempotency_key": "lost", "expected_revision": revision, "task_id": "task", "filename": "lost.txt", "mime": "text/plain", "size": len(lost_data), "sha256": hashlib.sha256(lost_data).hexdigest(), "source": "CLI"}
    lost = api("POST", "/api/v1/projects/shared/artifacts/uploads", lost_body, bearer=tokens["a"], expected=201)
    lost_file.unlink()
    api("GET", "/api/v1/projects/shared/artifacts/uploads/by-key/lost", bearer=tokens["b"], expected=404)
    located = client("a", "artifact", "upload-status", "--idempotency-key", "lost")
    assert located["upload_id"] == lost["upload_id"] and located["status"] == "open"
    cancelled = client("a", "artifact", "cancel", "--upload", located["upload_id"], "--operation-id", "lost", "--idempotency-key", "lost")
    assert cancelled["status"] == "purged"

    data = (b"resumable cross-device evidence\n" * 12000)
    file = projects["a"] / "resume.txt"
    file.write_bytes(data)
    start_body = {"operation_id": "resume", "idempotency_key": "resume", "expected_revision": revision, "task_id": "task", "attempt_id": "", "candidate": "", "filename": "resume.txt", "mime": "text/plain", "size": len(data), "sha256": hashlib.sha256(data).hexdigest(), "source": "CLI"}
    start = api("POST", "/api/v1/projects/shared/artifacts/uploads", start_body, bearer=tokens["a"], expected=201)
    api("POST", f"/api/v1/projects/shared/artifacts/uploads/{start['upload_id']}/chunks", {"offset": 0, "data": base64.b64encode(data[:100_000]).decode()}, bearer=tokens["a"])
    api("GET", f"/api/v1/projects/shared/artifacts/{start['artifact_id']}", bearer=tokens["b"], expected=404)
    api("POST", f"/api/v1/projects/shared/artifacts/uploads/{start['upload_id']}/finalize", {"operation_id": "resume", "idempotency_key": "resume"}, bearer=tokens["a"], expected=409)
    stats = api("GET", "/api/v1/projects/shared/artifacts/stats", bearer=tokens["b"])
    assert stats["temporary_bytes"] >= 100_000 and stats["artifact_bytes"] >= sum(map(len, contents.values()))
    resumed = client("a", "artifact", "upload", "--task", "task", "--file", file, "--expected-revision", revision, "--operation-id", "resume", "--idempotency-key", "resume")
    assert resumed["id"] == start["artifact_id"] and resumed["sha256"] == hashlib.sha256(data).hexdigest()
    file.unlink()
    client("b", "artifact", "download", "--artifact", resumed["id"], "--out", projects["b"] / "resume.txt")
    assert (projects["b"] / "resume.txt").read_bytes() == data

    databases = list((out / "server-data").rglob("runtime.db"))
    assert databases
    found = False
    for dbpath in databases:
        with sqlite3.connect(dbpath) as db:
            tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
            if "artifact_objects" in tables:
                found = True
                assert all(row[2].upper() != "BLOB" for table in ("artifact_objects", "artifact_uploads") for row in db.execute(f"PRAGMA table_info({table})"))
    assert found
    pending_data = b"pending browser storage upload" * 100
    pending_body = {"operation_id":"browser-pending", "idempotency_key":"browser-pending", "expected_revision":revision, "task_id":"task", "filename":"pending-browser.txt", "mime":"text/plain", "size":len(pending_data), "sha256":hashlib.sha256(pending_data).hexdigest(), "source":"storage-ui-fixture"}
    pending_upload = api("POST", "/api/v1/projects/shared/artifacts/uploads", pending_body, bearer=tokens["a"], expected=201)
    api("POST", f"/api/v1/projects/shared/artifacts/uploads/{pending_upload['upload_id']}/chunks", {"offset":0, "data":base64.b64encode(pending_data[:100]).decode()}, bearer=tokens["a"])
    manifest = out / "manifest.json"
    manifest.write_text(json.dumps({"isolated": True, "out": str(out), "url": base}))
    ui_env = dict(env, AEL_ARTIFACT_FIXTURE=str(manifest), AEL_UI_OUT=str(out / "browser"))
    test = subprocess.run([str(repo / "web/node_modules/.bin/playwright"), "test", "--config=playwright.artifacts.config.ts"], cwd=repo / "web", env=ui_env, timeout=180)
    if test.returncode:
        raise RuntimeError("packaged browser acceptance failed")
    for artifact_id in ids.values():
        assert client("b", "artifact", "show", "--artifact", artifact_id)["status"] == "purged"
        client("b", "artifact", "download", "--artifact", artifact_id, "--out", projects["b"] / (artifact_id + ".out"), expect_ok=False)
    for name, project in projects.items():
        assert not (project / ".ael" / "work-items").exists()
        assert not (out / f"runtime-{name}").exists()
    print(json.dumps({"status": "passed", "isolated": True, "clients": 2, "artifacts": 3, "browser": "storage administration and evidence lifecycle"}))
finally:
    if proc and proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=10)
    if server_log:
        server_log.close()
    (out / "commands.json").write_text(json.dumps(commands, ensure_ascii=False, indent=2))

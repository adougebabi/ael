#!/usr/bin/env python3
"""Exercise Local-to-Remote authority transfer in disposable same-machine fixtures."""
import argparse
import base64
import hashlib
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import select
import shutil
import sqlite3
import struct
import subprocess
import threading
import urllib.error
import urllib.request
import zlib

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--binary", required=True)
parser.add_argument("--source-bundle", required=True)
parser.add_argument("--out", required=True)
parser.add_argument("--browser", action="store_true")
args = parser.parse_args()
binary = Path(args.binary).resolve()
bundle = Path(args.source_bundle).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("fixture output must be empty")
out.mkdir(parents=True, exist_ok=True)
os.chmod(out, 0o700)
(out / "ISOLATED-AUTHORITY-TRANSFER").write_text("Synthetic historical data, isolated Local clones and one real HTTP Server.\n")
old = bundle / "ael-darwin-arm64"
if not old.exists():
    raise SystemExit("provide the explicit native macOS arm64 source bundle")
events = []
base_env = dict(os.environ, AEL_DATA_DIR=str(out / "local-runtime"), AEL_INSTALL_PREFIX=str(out / "unused-install"))
roots = {name: out / name for name in ("mac-old", "cloud-old")}
requests = out / "requests"
requests.mkdir()


def request_file(name, value):
    path = requests / (name + ".json")
    path.write_text(json.dumps(value, ensure_ascii=False))
    return path


def run(executable, root, *words, stdin=None, passed=True):
    env = dict(base_env, AEL_REMOTE_CONFIG_DIR=str(out / ("config-" + root.name)))
    scope = [] if tuple(words[:2]) in (("server", "bootstrap"), ("server", "start")) else ["--project", str(root)]
    result = subprocess.run([str(executable), *map(str, words), *scope, "--json"], cwd=root, env=env, input=stdin, text=True, capture_output=True, timeout=90)
    if (result.returncode == 0) != passed:
        raise RuntimeError(f"{words[:2]} unexpected exit {result.returncode}: {result.stderr[:1000]}")
    if not passed:
        return result.stderr
    return json.loads(result.stdout)


def current(root, *words, **kwargs):
    return run(binary, root, *words, **kwargs)


def png_bytes():
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 1, 1, 8, 6, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(b"\0\xff\x55\x00\xff")) + chunk(b"IEND", b"")


def runtime(root):
    return Path(base_env["AEL_DATA_DIR"]) / hashlib.sha256(str(root.resolve()).encode()).hexdigest()[:24]


roots["mac-old"].mkdir()
run(old, roots["mac-old"], "init", "--bundle-dir", bundle)
definition = {"id": "same-name", "title": "Synthetic historical work", "type": "feature", "goal": "verified", "acceptance": ["historical result"]}
definition_file = request_file("initial", definition)
run(old, roots["mac-old"], "task", "create", "--file", definition_file)
shutil.copytree(roots["mac-old"], roots["cloud-old"])
run(old, roots["cloud-old"], "task", "show", "--task", "same-name")
run(old, roots["cloud-old"], "task", "edit", "--task", "same-name", "--file", request_file("conflict", dict(definition, acceptance=["different clone revision"])), "--reason", "synthetic conflicting revision")
run(old, roots["cloud-old"], "task", "create", "--file", request_file("open-history", dict(definition, id="zz-open-history", title="Synthetic history waiting for reconciliation")))
image = png_bytes()
image_sha = hashlib.sha256(image).hexdigest()
for name, root in roots.items():
    data = runtime(root)
    (data / "actual.png").write_bytes(image)
    specification = b"# Legacy fixture specification\nPreserve historical evidence and source revisions.\n"
    spec_version = hashlib.sha256(specification).hexdigest()
    spec_dir = data / "knowledge-versions" / hashlib.sha256(b"legacy-spec").hexdigest()
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / (spec_version + ".json")).write_text(json.dumps({"path": "docs/legacy.md", "raw": base64.b64encode(specification).decode()}))
    with sqlite3.connect(data / "runtime.db") as db:
        task = json.loads(db.execute("SELECT body FROM tasks WHERE id='same-name'").fetchone()[0])
        task.update(status="completed", current_step="done", confirmation=None, paused=False, created="0001-01-01T00:00:00Z", updated="0001-01-01T00:00:00Z", completed=None, candidate=("a" if name == "mac-old" else "b") * 64)
        task["attempts"] = [{"id": "historical-attempt", "unit": "execute", "status": "passed", "token": "fixture-old-private-token", "pid": 999999, "session": "fixture-old-private-session", "lease_until": "2099-01-01T00:00:00Z", "fence": 7, "outputs": ["runtime:actual.png", "runtime:missing.png"]}]
        task["evidence"] = [{"check": "historical-check", "result": "pass", "source": "synthetic historical fixture", "candidate": task["candidate"], "acceptance_revision": task["definition"]["definition_revision"], "artifacts": ["runtime:actual.png"], "artifact_hashes": {"runtime:actual.png": image_sha}}]
        task["human_review_notes"] = [{"id": "historical-human", "check": "historical-check", "actor": "Historical reviewer", "result": "pass", "reason": "Synthetic historical human result", "status": "confirmed", "candidate": task["candidate"], "definition_revision": task["definition"]["definition_revision"], "plan_revision": task["plan"]["revision"]}]
        task["applicable_knowledge"] = [{"id": "legacy-spec", "source": "docs/legacy.md", "version": spec_version, "status": "current", "reason": "Explicit historical fixture specification"}]
        db.execute("UPDATE tasks SET body=?,status='completed',step='done',updated='',completed='' WHERE id='same-name'", (json.dumps(task),))
events.append("two actual Local clones with same ID, conflicting revision, duplicate PNG and unknown historical time")

server_work = out / "server-work"
server_work.mkdir()
server_data = out / "server-data"
run(binary, server_work, "server", "bootstrap", "--data-dir", server_data, "--username", "owner", "--password-stdin", stdin="fixture-owner-password\n")
server_log = (out / "server.log").open("w")
process = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(server_data), "--addr", "127.0.0.1:0", "--json"], cwd=server_work, env=base_env, stdout=subprocess.PIPE, stderr=server_log, text=True)
proxy = None
release_response = threading.Event()
operation_saved = threading.Event()
fault = {"path": ""}
try:
    ready, _, _ = select.select([process.stdout], [], [], 30)
    if not ready:
        raise RuntimeError("Server readiness timed out")
    base = json.loads(process.stdout.readline())["url"]
    class Proxy(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def forward(self):
            data = self.rfile.read(int(self.headers.get("Content-Length", "0"))) if self.command == "POST" else None
            headers = {key: value for key, value in self.headers.items() if key.lower() not in ("host", "content-length", "connection")}
            request = urllib.request.Request(base + self.path, data=data, headers=headers, method=self.command)
            try:
                response = urllib.request.urlopen(request, timeout=30)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                status, body = response.status, response.read()
                if self.command == "POST" and self.path == fault["path"] and status == 200:
                    fault["path"] = ""
                    operation_saved.set()
                    release_response.wait(timeout=30)
                try:
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError):
                    pass

        do_GET = forward
        do_POST = forward

    proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Proxy)
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    proxy_url = "http://127.0.0.1:" + str(proxy.server_address[1])

    def interrupt_after_commit(root, path, *words):
        operation_saved.clear()
        release_response.clear()
        fault["path"] = path
        env = dict(base_env, AEL_REMOTE_CONFIG_DIR=str(out / ("config-" + root.name)))
        child = subprocess.Popen([str(binary), *map(str, words), "--project", str(root), "--json"], cwd=root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            if not operation_saved.wait(timeout=20):
                raise RuntimeError("interruption fixture did not reach the committed operation")
            child.kill()
            child.communicate(timeout=10)
            assert child.returncode != 0
        finally:
            if child.poll() is None:
                child.kill()
                child.communicate(timeout=10)
            release_response.set()
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    csrf = ""

    def api(method, path, payload=None):
        headers = {"X-AEL-Protocol": "1", "Origin": base}
        if csrf:
            headers["X-AEL-CSRF"] = csrf
        if payload is not None:
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(base + path, data=json.dumps(payload).encode() if payload is not None else None, headers=headers, method=method)
        with opener.open(request, timeout=20) as response:
            return json.load(response)

    csrf = api("POST", "/api/v1/auth/login", {"username": "owner", "password": "fixture-owner-password"})["csrf"]
    api("POST", "/api/v1/projects", {"id": "shared", "name": "Imported histories"})
    api("POST", "/api/v1/users", {"username": "migrator", "password": "fixture-migrator-password"})
    api("POST", "/api/v1/projects/shared/members", {"username": "migrator", "role": "executor"})
    api("POST", "/api/v1/projects", {"id": "aborts", "name": "Abort rehearsal"})
    api("POST", "/api/v1/projects/aborts/members", {"username": "migrator", "role": "executor"})
    for phase in ("absent", "importing"):
        root = out / ("abort-" + phase)
        root.mkdir()
        run(old, root, "init", "--bundle-dir", bundle)
        run(old, root, "task", "list")
        token = api("POST", "/api/v1/tokens", {"username": "migrator", "project_id": "aborts", "label": phase})["token"]
        current(root, "server", "profile", "add", "--name", "central", "--url", proxy_url, "--token-stdin", stdin=token + "\n")
        scan = current(root, "transfer", "scan", "--source-id", "abort." + phase)
        plan = request_file("abort-plan-" + phase, scan)
        export = out / ("abort-export-" + phase)
        current(root, "transfer", "export", "--plan", plan, "--approved-digest", scan["digest"], "--out", export, "--ack-drained")
        common = ["--from", export, "--profile", "central", "--remote-project", "aborts"]
        if phase == "importing":
            current(root, "transfer", "import", *common)
        current(root, "transfer", "rollback-freeze", *common, "--ack-drained")
        current(root, "transfer", "rollback", *common, "--ack-drained")
        rejected = current(root, "transfer", "import", *common, passed=False)
        assert "IMPORT_ABORTED" in rejected
        run(old, root, "task", "create", "--file", request_file("after-abort-" + phase, dict(definition, id="after-abort")))
    events.append("absent and unactivated imports abort safely, reject late stages and release the original Local runtime")
    exports, imports = {}, {}
    for name, root in roots.items():
        token = api("POST", "/api/v1/tokens", {"username": "migrator", "project_id": "shared", "label": name})["token"]
        current(root, "server", "profile", "add", "--name", "central", "--url", proxy_url, "--token-stdin", stdin=token + "\n")
        if name == "mac-old":
            with sqlite3.connect(runtime(root) / "runtime.db") as db:
                db.execute("UPDATE tasks SET body=json_set(body,'$.attempts[0].status','interrupted') WHERE id='same-name'")
            unresolved = current(root, "transfer", "scan", "--source-id", name)
            unresolved_plan = request_file("unresolved", unresolved)
            rejected = current(root, "transfer", "export", "--plan", unresolved_plan, "--approved-digest", unresolved["digest"], "--out", out / "rejected-unresolved", "--ack-drained", passed=False)
            assert "TRANSFER_NOT_DRAINED" in rejected
            with sqlite3.connect(runtime(root) / "runtime.db") as db:
                db.execute("UPDATE tasks SET body=json_set(body,'$.attempts[0].status','passed') WHERE id='same-name'")
            events.append("unknown historical responsibility rejects export despite drain acknowledgement")
        scan = current(root, "transfer", "scan", "--source-id", name)
        assert "fixture-old-private" not in json.dumps(scan)
        plan_input = request_file("scan-" + name, scan)
        plan_path = requests / ("plan-" + name + ".json")
        current(root, "transfer", "plan", "--plan", plan_input, "--out", plan_path)
        export = out / ("export-" + name)
        current(root, "transfer", "export", "--plan", plan_path, "--approved-digest", scan["digest"], "--out", export, passed=False)
        current(root, "transfer", "export", "--plan", plan_path, "--approved-digest", scan["digest"], "--out", export, "--ack-drained")
        current(root, "transfer", "export", "--plan", plan_path, "--approved-digest", scan["digest"], "--out", export, "--ack-drained")
        assert "LOCAL_AUTHORITY_FROZEN" in run(old, root, "task", "create", "--file", request_file("late-local-" + name, dict(definition, id="late-local")), passed=False)
        common = ["--from", export, "--profile", "central", "--remote-project", "shared"]
        if name == "mac-old":
            interrupt_after_commit(root, "/api/v1/projects/shared/imports", "transfer", "import", *common)
            events.append("actual CLI SIGKILL after central import commit, followed by exact input recovery")
        staged = current(root, "transfer", "import", *common)
        current(root, "transfer", "import", *common)
        verified = current(root, "transfer", "verify", *common)
        assert verified["state"] == "verified"
        current(root, "transfer", "commit", *common)
        current(root, "transfer", "cutover", *common, passed=False)
        current(root, "transfer", "cutover", *common, "--ack-drained")
        current(root, "transfer", "cutover", *common, "--ack-drained")
        detail = api("GET", "/api/v1/projects/shared/imports/" + staged["id"])
        archived = next(item for item in detail["archive"]["tasks"] if item["original_id"] == "same-name")
        assert archived["snapshot"]["created"].startswith("0001-")
        mapped = archived["mapped_id"]
        task = current(root, "task", "show", "--task", mapped)["task"]
        assert task["historical"] and task["candidate"] == "" and not task["evidence"] and not task["attempts"]
        assert any(file["status"] == "missing" for file in detail["files"])
        picture = next(file for file in detail["files"] if file["ref"] == "runtime:actual.png")
        downloaded = out / (name + ".png")
        current(root, "artifact", "download", "--artifact", picture["artifact_id"], "--out", downloaded)
        assert hashlib.sha256(downloaded.read_bytes()).hexdigest() == image_sha
        exports[name] = common
        imports[name] = {"id": staged["id"], "task": mapped}
        if name == "cloud-old":
            open_archive = next(item for item in detail["archive"]["tasks"] if item["original_id"] == "zz-open-history")
            imports[name]["open_task"] = open_archive["mapped_id"]
    assert imports["mac-old"]["task"] != imports["cloud-old"]["task"]
    for root in roots.values():
        assert current(root, "task", "list")["total"] == 3
        assert run(old, root, "task", "list")["mode"] == "remote"
    events.append("formal CLI scan/plan/export/import/verify/commit/cutover, exact replay and shared live reads")
    if args.browser:
        manifest = request_file("browser-manifest", {"url": base, "out": str(out), "imports": imports})
        repo = Path(__file__).resolve().parent.parent
        browser_env = dict(base_env, AEL_AUTHORITY_TRANSFER_FIXTURE=str(manifest), AEL_UI_OUT=str(out / "browser"))
        subprocess.run([str(repo / "web/node_modules/.bin/playwright"), "test", "--config=playwright.authority-transfer.config.ts"], cwd=repo / "web", env=browser_env, timeout=180, check=True)
        events.append("actual packaged Dashboard history, image, missing state and slow response isolation")
    root = roots["cloud-old"]
    task = current(root, "task", "show", "--task", imports["cloud-old"]["task"])
    current(root, "artifact", "upload", "--task", imports["cloud-old"]["task"], "--file", out / "cloud-old.png", "--expected-revision", task["revision"], "--source", "new central diagnostic", "--operation-id", "new-central", "--idempotency-key", "new-central")
    current(root, "transfer", "rollback-freeze", *exports["cloud-old"], "--ack-drained", passed=False)
    current(root, "transfer", "rollback", *exports["cloud-old"], "--ack-drained", passed=False)
    events.append("new central evidence prevents old Local authority rollback")
    root = roots["mac-old"]
    current(root, "transfer", "rollback-freeze", *exports["mac-old"], "--ack-drained")
    interrupt_after_commit(root, "/api/v1/projects/shared/imports/" + imports["mac-old"]["id"] + "/rollback-complete", "transfer", "rollback", *exports["mac-old"], "--ack-drained")
    events.append("actual CLI SIGKILL after Local release and central rollback receipt, followed by journal recovery")
    current(root, "transfer", "rollback", *exports["mac-old"], "--ack-drained")
    current(root, "transfer", "rollback", *exports["mac-old"], "--ack-drained")
    assert not (root / ".ael/remote.json").exists()
    run(old, root, "task", "create", "--file", request_file("local-after-rollback", dict(definition, id="new-local")))
    events.append("unchanged selected source rolls back with a durable journal while unrelated central facts remain")
    summary = {"result": "PASS", "mode": "same-machine isolated real HTTP", "synthetic_legacy_history": True, "image_sha256": image_sha, "imports": imports, "events": events, "oracle_access": False}
    (out / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary))
finally:
    release_response.set()
    if proxy is not None:
        proxy.shutdown()
        proxy.server_close()
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)
    server_log.close()

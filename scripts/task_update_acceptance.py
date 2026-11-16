#!/usr/bin/env python3
"""Verify real signed task-time upgrades and frozen tool sessions in isolation."""
import argparse
import copy
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import platform
import select
import shutil
import subprocess
import tarfile
import threading
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--base-release", required=True)
parser.add_argument("--next-release", required=True)
parser.add_argument("--out", required=True)
args = parser.parse_args()
out = Path(args.out).resolve()
if out.exists():
    raise SystemExit("choose a new isolated output directory")
out.mkdir(mode=0o700, parents=True)
(out / "ISOLATED-TASK-UPDATES").write_text("Real packaged CLIs with fixture signing identity and loopback services.\n")
native = platform.system().lower() + "-" + {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}[platform.machine()]
base_release, next_release = Path(args.base_release).resolve(), Path(args.next_release).resolve()
base_manifest = json.loads((base_release / "manifest.json").read_text())
next_manifest = json.loads((next_release / "manifest.json").read_text())
assert base_manifest["version"] != next_manifest["version"]
base_asset = next(a for a in base_manifest["assets"] if a["kind"] == "client" and a["platform"] == native)
base_archive = base_release / base_asset["path"]
package = out / "package"
package.mkdir()
with tarfile.open(base_archive) as archive:
    for member in archive.getmembers():
        assert member.isfile() and Path(member.name).name == member.name
        path = package / member.name
        path.write_bytes(archive.extractfile(member).read())
        path.chmod(member.mode & 0o777)
binary = package / "ael"
prefix = out / "shared-install"
config = out / "private-config"
env = {k: v for k, v in os.environ.items() if not k.startswith(("AEL_", "GIT_"))}
env.update(AEL_REMOTE_CONFIG_DIR=str(config), AEL_CLIENT_CONFIG_DIR=str(out / "machine-clients"),
           AEL_DATA_DIR=str(out / "unused-runtime"), GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
           GIT_TERMINAL_PROMPT="0")
events = []
server = None
concurrent = []
server_log = (out / "server.log").open("w")
source_counts = {"catalog": 0, "downloads": 0, "incompatible_project_reads": 0}
fault = {"mode": ""}
content = {}
catalog = []
secrets = ["fixture-task-update-password"]


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def run(label, executable, *argv, input_text=None, error=None, command_env=None, cwd=out):
    result = subprocess.run([str(executable), *map(str, argv), "--json"], cwd=cwd, env=command_env or env,
                            input=input_text, text=True, capture_output=True, timeout=120)
    if error:
        assert result.returncode != 0 and error in result.stderr, (label, result.stdout, result.stderr)
        value = {"status": error}
    else:
        assert result.returncode == 0, (label, result.stderr)
        value = json.loads(result.stdout)
    for secret in secrets:
        assert secret not in result.stdout + result.stderr, (label, "credential output")
    events.append({"case": label, "passed": True, "status": value.get("status")})
    return value


class SourceHandler(BaseHTTPRequestHandler):
    def log_message(self, *unused):
        pass

    def do_GET(self):
        path = urllib.parse.urlsplit(self.path).path
        if path == "/api/v1/meta":
            body = json.dumps({"mode": "server", "protocol": 2, "skill_protocol": 2}).encode()
        elif path.startswith("/api/v1/projects/"):
            source_counts["incompatible_project_reads"] += 1
            body = b"{}"
        elif path == "/releases":
            source_counts["catalog"] += 1
            if fault["mode"] == "network":
                self.send_error(503)
                return
            body = json.dumps(catalog).encode()
        else:
            body = content.get(path)
            if body is None:
                self.send_error(404)
                return
            if path.endswith("manifest.sig") and fault["mode"] == "signature":
                body = b"invalid signature"
            if path.endswith(".tar.gz"):
                source_counts["downloads"] += 1
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if path.endswith(".tar.gz") and fault["mode"] == "download":
            self.wfile.write(body[: max(1, len(body) // 4)])
            self.wfile.flush()
            self.close_connection = True
        else:
            self.wfile.write(body)


source_server = ThreadingHTTPServer(("127.0.0.1", 0), SourceHandler)
source_origin = "http://127.0.0.1:" + str(source_server.server_address[1])
source_thread = threading.Thread(target=source_server.serve_forever, daemon=True)
source_thread.start()
key = out / "signing-key"
verifier = shutil.which("ssh-keygen")
assert verifier
subprocess.run([verifier, "-q", "-t", "ed25519", "-N", "", "-f", str(key)], check=True, capture_output=True)
signers = out / "allowed_signers"
signers.write_text("task-update-fixture " + Path(str(key) + ".pub").read_text())
source_file = out / "source.json"
source_file.write_text(json.dumps({"releases_url": source_origin + "/releases", "manifest_asset": "manifest.json",
                                  "signature_asset": "manifest.sig", "allowed_origins": [],
                                  "trust": {"type": "openssh", "identity": "task-update-fixture", "allowed_signers": str(signers), "verifier": verifier}}))


def sign_release(manifest, directory, name):
    # The released/clean flags belong solely to this fixture catalog. These are
    # unpublished local artifacts and this identity is generated for this test.
    fixture = copy.deepcopy(manifest)
    fixture.update(dirty=False, publication="released")
    path = out / (name + "-manifest.json")
    path.write_text(json.dumps(fixture))
    subprocess.run([verifier, "-Y", "sign", "-f", str(key), "-n", "ael-release", str(path)], check=True, capture_output=True)
    files = {"manifest.json": path.read_bytes(), "manifest.sig": Path(str(path) + ".sig").read_bytes()}
    files.update({asset["path"]: (directory / asset["path"]).read_bytes() for asset in fixture["assets"]})
    assets = []
    for filename, data in files.items():
        content["/" + name + "/" + filename] = data
        assets.append({"name": filename, "browser_download_url": source_origin + "/" + name + "/" + filename, "size": len(data)})
    return {"tag_name": "v" + fixture["version"], "draft": False, "prerelease": False, "assets": assets}


base_catalog = sign_release(base_manifest, base_release, "base")
next_catalog = sign_release(next_manifest, next_release, "next")
catalog[:] = [base_catalog]
origin = ""


def start_server(address="127.0.0.1:0", minimum="0.1.22", source=False):
    global server, origin
    argv = [str(binary), "server", "start", "--data-dir", str(out / "server-data"), "--addr", address,
            "--client-min-version", minimum, "--json"]
    if source:
        argv.extend(["--client-release-source", str(source_file)])
    server = subprocess.Popen(argv, cwd=out, env=env, stdout=subprocess.PIPE, stderr=server_log, text=True)
    ready, _, _ = select.select([server.stdout], [], [], 20)
    assert ready
    line = server.stdout.readline()
    assert line, "Server exited, inspect server.log"
    origin = json.loads(line)["url"]


def stop_server():
    if server is not None and server.poll() is None:
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait()


def project_call(label, name, *argv, error=None, installation=prefix, executable=None):
    project = out / ("project-" + name)
    local_env = dict(env, AEL_INSTALL_PREFIX=str(installation))
    return run(label, executable or project / "ael", *argv, "--project", project, error=error, command_env=local_env, cwd=project)


def private_file(name, body):
    path = out / (name + ".json")
    path.write_text(json.dumps(body))
    path.chmod(0o600)
    return path


def prepare_task(name, runner, session, suffix):
    project = out / ("project-" + name)
    if not (project / ".git").exists():
        for argv in (["init", "-b", "main"], ["config", "user.name", "Fixture"], ["config", "user.email", "fixture@example.invalid"]):
            subprocess.run(["git", *argv], cwd=project, env=env, check=True, capture_output=True)
        (project / "app.txt").write_text("fixture work\n")
        subprocess.run(["git", "add", "."], cwd=project, env=env, check=True, capture_output=True)
        subprocess.run(["git", "commit", "-m", "fixture baseline"], cwd=project, env=env, check=True, capture_output=True)
        project_call("repository " + name, name, "repository", "register", "--repository", "repo-" + name,
                     "--operation-id", "repo-" + name, "--idempotency-key", "repo-" + name)
    project_call("source index " + suffix, name, "knowledge", "index")
    definition = {"id": "task-" + suffix, "title": "Task-time version fixture " + suffix, "type": "bug", "goal": "verified",
                  "scope": ["app.txt"], "acceptance": ["fixture exists"],
                  "checks": [{"id": "check", "kind": "functional", "method": "command", "command": ["sh", "-c", "test -f app.txt"], "required": True, "covers": ["fixture exists"]}]}
    file = private_file("definition-" + suffix, definition)
    project_call("task " + suffix, name, "task", "create", "--file", file, "--operation-id", "task-" + suffix, "--idempotency-key", "task-" + suffix)
    registration = private_file("register-" + suffix, {"id": runner, "workspace_id": "workspace-" + suffix,
                                "checkout_id": "checkout-" + suffix, "repository": "repo-" + name,
                                "version": base_manifest["version"], "capabilities": ["development"], "expected_revision": "absent"})
    project_call("register " + suffix, name, "runner", "register", "--session", session, "--file", registration,
                 "--operation-id", "register-" + suffix, "--idempotency-key", "register-" + suffix)
    current = project_call("task revision " + suffix, name, "task", "show", "--task", "task-" + suffix)
    observation = project_call("observe " + suffix, name, "candidate", "capture", "--task", "task-" + suffix,
                               "--repository", "repo-" + name, "--checkout", "checkout-" + suffix, "--workspace", "workspace-" + suffix,
                               "--runner", runner, "--expected-revision", current["revision"], "--operation-id", "observe-" + suffix,
                               "--idempotency-key", "observe-" + suffix)
    context = project_call("context " + suffix, name, "context", "publish", "--task", "task-" + suffix, "--observation", observation["id"],
                           "--expected-revision", current["revision"], "--operation-id", "context-" + suffix, "--idempotency-key", "context-" + suffix)
    claim = private_file("claim-input-" + suffix, {"task_id": "task-" + suffix, "context_id": context["id"],
                         "expected_revision": current["revision"], "operation_id": "claim-" + suffix, "idempotency_key": "claim-" + suffix})
    return claim


try:
    run("bootstrap", binary, "server", "bootstrap", "--data-dir", out / "server-data", "--username", "owner", "--password-stdin", input_text=secrets[0] + "\n")
    start_server()
    address = urllib.parse.urlparse(origin).netloc
    for name in ("a", "b"):
        project = out / ("project-" + name)
        project.mkdir()
        result = run("initialize " + name, binary, "init", "--server", origin, "--profile", name, "--create", "Task update " + name,
                     "--username", "owner", "--password-stdin", "--project", project, "--prefix", prefix, "--from", base_archive,
                     "--host", "none", "--update-policy", "auto", "--release-source", source_file, input_text=secrets[0] + "\n")
        secrets.append(json.loads((config / "profiles" / (name + ".json")).read_text())["token"])
    claim_a = prepare_task("a", "runner-a", "active-a", "a")
    project_call("claim active old task", "a", "runner", "claim", "--session", "active-a", "--runner", "runner-a",
                 "--file", claim_a, "--out", out / "lease-a.json")
    lease_a = json.loads((out / "lease-a.json").read_text())
    old_session = project_call("inspect active old session", "a", "session", "show", "--session", "active-a")["session"]
    old_skill_hash = sha(old_session["skill_path"])
    claim_b = prepare_task("b", "runner-b", "setup-b", "b")
    claim_c = prepare_task("a", "runner-c", "setup-c", "c")
    project_call("close setup tools b", "b", "session", "end", "--session", "setup-b")
    project_call("close setup tools c", "a", "session", "end", "--session", "setup-c")
    catalog[:] = [next_catalog]
    stop_server()
    start_server(address, source=True)
    before_downloads = source_counts["downloads"]
    concurrent = []
    for name, runner, session, claim in (("b", "runner-b", "next-b", claim_b), ("a", "runner-c", "next-c", claim_c)):
        project = out / ("project-" + name)
        process = subprocess.Popen([str(project / "ael"), "runner", "claim", "--session", session, "--runner", runner,
                                    "--file", str(claim), "--out", str(out / ("lease-" + session + ".json")), "--json"],
                                   cwd=project, env=dict(env, AEL_INSTALL_PREFIX=str(prefix)), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        concurrent.append((session, process))
    for session, process in concurrent:
        stdout, stderr = process.communicate(timeout=90)
        assert process.returncode == 0, (session, stderr)
        assert "token" not in json.loads(stdout)["attempt"]
        events.append({"case": "concurrent claim " + session, "passed": True})
    assert source_counts["downloads"] - before_downloads == 1, source_counts
    assert run("new global version", prefix / "bin/ael", "version")["version"] == next_manifest["version"]
    assert project_call("old session executes old cache", "a", "version", "--session", "active-a")["version"] == base_manifest["version"]
    assert project_call("new task executes new cache", "b", "version", "--session", "next-b")["version"] == next_manifest["version"]
    project_call("old task responsibility survives update", "a", "runner", "show", "--runner", "runner-a", "--session", "active-a")
    project_call("active responsibility keeps pin", "a", "session", "end", "--session", "active-a", error="SESSION_ACTIVE_RESPONSIBILITY")
    project_call("active runner cannot silently prepare new tools", "a", "session", "start", "--session", "wrong-new-session", "--runner", "runner-a", error="SESSION_ACTIVE_RUNNER")
    assert sha(old_session["skill_path"]) == old_skill_hash
    catalog_before_queries = source_counts["catalog"]
    project_call("ordinary query does not update", "a", "task", "list", "--session", "active-a")
    heartbeat = private_file("heartbeat-a", {"task_id": "task-a", "attempt_id": lease_a["attempt"]["id"], "token": lease_a["attempt"]["token"],
                             "fence": lease_a["attempt"]["fence"], "operation_id": "heartbeat-a", "idempotency_key": "heartbeat-a"})
    project_call("heartbeat does not update", "a", "runner", "heartbeat", "--session", "active-a", "--runner", "runner-a", "--file", heartbeat)
    assert source_counts["catalog"] == catalog_before_queries
    # Explicit project updates affect future sessions. The active snapshot stays exact.
    run("explicit entry update", prefix / "bin/ael", "update", "--project", out / "project-b")
    after_update = project_call("new session loads new Skill", "b", "session", "start", "--session", "new-rules")
    assert after_update["session"]["skill_version"] == next_manifest["version"]
    assert old_session["skill_version"] == base_manifest["version"] and sha(old_session["skill_path"]) == old_skill_hash
    # Each failure case uses its own real old installation and transient check cache.
    for mode in ("signature", "network", "download", "empty"):
        failure_prefix = out / ("failure-" + mode)
        run("install old for " + mode, binary, "upgrade", "--prefix", failure_prefix, "--from", base_archive)
        run("authorize auto for " + mode, binary, "client", "policy", "--prefix", failure_prefix, "--mode", "auto", "--source", source_file)
        before = sha(failure_prefix / "bin/ael")
        fault["mode"] = mode
        if mode == "empty":
            catalog[:] = []
        result = project_call("compatible fallback " + mode, "b", "session", "start", "--session", "failure-" + mode,
                              installation=failure_prefix, executable=binary)
        assert result["session"]["cli"]["version"]["version"] == base_manifest["version"], result
        assert sha(failure_prefix / "bin/ael") == before
        fault["mode"] = ""
        catalog[:] = [next_catalog]
    fixed_prefix = out / "fixed-install"
    run("install fixed base", binary, "upgrade", "--prefix", fixed_prefix, "--from", base_archive)
    run("explicit fixed policy", binary, "client", "policy", "--prefix", fixed_prefix, "--mode", "pinned", "--version", base_manifest["version"], "--source", source_file)
    fixed = project_call("fixed version ignores newer release", "b", "session", "start", "--session", "fixed-session", installation=fixed_prefix, executable=binary)
    assert fixed["session"]["cli"]["version"]["version"] == base_manifest["version"]
    stop_server()
    start_server(address, minimum="99.0.0", source=True)
    project_call("incompatible current and available versions block new work", "b", "session", "start", "--session", "incompatible-new", installation=fixed_prefix, executable=binary, error="CLIENT_VERSION_INCOMPATIBLE")
    stop_server()
    start_server(address, source=True)
    profile_path = config / "profiles/b.json"
    original_profile = profile_path.read_text()
    invalid = json.loads(original_profile)
    invalid["url"] = source_origin
    profile_path.write_text(json.dumps(invalid))
    try:
        project_call("incompatible protocol blocks authority access", "b", "session", "start", "--session", "protocol-new", error="REMOTE_PROTOCOL_UNSUPPORTED")
        assert source_counts["incompatible_project_reads"] == 0
    finally:
        profile_path.write_text(original_profile)
    # Verify the stored public identity is not silently replaced by a changed file.
    original_signers = signers.read_text()
    signers.write_text(original_signers + "# explicit local edit\n")
    changed = project_call("changed publisher requires explicit review", "b", "session", "start", "--session", "changed-publisher", installation=fixed_prefix, executable=binary)
    # Fixed policy never reads an online source. Exercise identity checking under auto.
    run("select auto for identity case", binary, "client", "policy", "--prefix", fixed_prefix, "--mode", "auto")
    changed = project_call("auto rejects changed publisher", "b", "session", "start", "--session", "changed-publisher-auto", installation=fixed_prefix, executable=binary)
    assert "RELEASE_TRUST_CHANGED" in changed["preflight"]["warnings"]
    signers.write_text(original_signers)
    # The Server exposes only last reported versions, with no host control claim.
    report = project_call("reported versions are queryable", "b", "doctor")
    assert report["online"]
    token = json.loads(profile_path.read_text())["token"]
    project_id = json.loads((out / "project-b/.ael/remote.json").read_text())["project_id"]
    request = urllib.request.Request(origin + "/api/v1/projects/" + project_id + "/client-onboarding", headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(request, timeout=10) as response:
        versions = json.load(response)
    assert versions["report_kind"] == "last_reported" and versions["host_execution_state"] == "unknown"
    assert any(row["cli_version"] == next_manifest["version"] for row in versions["clients"])
    assert not list((out / "unused-runtime").rglob("runtime.db*"))
    result = {"result": "PASS", "driver": "deterministic real CLI/Server with active responsibilities",
              "base_version": base_manifest["version"], "next_version": next_manifest["version"], "server_version": base_manifest["version"],
              "concurrent_upgrade_downloads": 1, "old_session_cache": old_session["cli"]["executable"],
              "old_frozen_skill_sha256": old_skill_hash, "formal_publication_available": False,
              "signer": "temporary fixture identity", "events": events}
    (out / "summary.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result))
finally:
    for _, process in concurrent:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
    stop_server()
    server_log.close()
    source_server.shutdown()
    source_server.server_close()
    source_thread.join(timeout=5)
    for path in (key, Path(str(key) + ".pub")):
        if path.exists():
            path.unlink()

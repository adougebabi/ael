#!/usr/bin/env python3
"""Exercise packaged Remote init against an isolated Server and machine configs."""
import argparse
import http.client
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import platform
import pty
import select
import socket
import subprocess
import tarfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--release", required=True)
parser.add_argument("--out", required=True)
parser.add_argument("--mismatched-release")
args = parser.parse_args()
release = Path(args.release).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("refusing a nonempty fixture directory")
out.mkdir(parents=True, exist_ok=True)
(out / "ISOLATED-ONBOARDING").write_text("Disposable Server, projects and machine configs.\n")
native = platform.system().lower() + "-" + {"aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine(), platform.machine())
manifest = json.loads((release / "manifest.json").read_text())
asset = next(item for item in manifest["assets"] if item["kind"] == "client" and item["platform"] == native)
archive = release / asset["path"]
package = out / "package"
package.mkdir()
with tarfile.open(archive) as bundle:
    for member in bundle.getmembers():
        if not member.isfile() or Path(member.name).name != member.name:
            raise RuntimeError("client package must contain flat regular files")
        target = package / member.name
        with bundle.extractfile(member) as content:
            target.write_bytes(content.read())
        target.chmod(member.mode & 0o777)
binary = package / "ael"
env = {key: value for key, value in os.environ.items() if not key.startswith(("AEL_", "GIT_"))}
env.update(AEL_CLIENT_CONFIG_DIR=str(out / "machine-clients"), AEL_DATA_DIR=str(out / "unused-local-runtime"),
           GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT="0")
commands = []
server = None
server_log = (out / "server.log").open("w")
proxy = None
password = "fixture-admin-password"


def cli(*argv, credential=None, ok=True, cwd=out):
    result = subprocess.run([str(binary), *map(str, argv), "--json"], cwd=cwd, env=env,
                            input=credential, text=True, capture_output=True, timeout=90)
    commands.append({"argv": list(map(str, argv)), "exit": result.returncode,
                     "stdout": result.stdout, "stderr": result.stderr})
    if (result.returncode == 0) != ok:
        raise RuntimeError(f"unexpected CLI result {argv}: {result.stderr}")
    return json.loads(result.stdout) if ok else result.stderr


def start_server(address="127.0.0.1:0"):
    global server
    server = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(out / "server-data"),
                               "--addr", address, "--json"], env=env, cwd=out,
                              stdout=subprocess.PIPE, stderr=server_log, text=True)
    ready, _, _ = select.select([server.stdout], [], [], 20)
    if not ready:
        raise RuntimeError("Server did not become ready")
    line = server.stdout.readline()
    if not line:
        raise RuntimeError("Server exited, inspect server.log")
    return json.loads(line)["url"]


def stop_server():
    if server is not None:
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait()


opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
csrf = ""
base = ""


def api(method, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    headers = {"Origin": base, "X-AEL-Protocol": "1", "X-AEL-CSRF": csrf}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
    with opener.open(request, timeout=10) as response:
        return json.load(response)


def initialize(name, *choice, credential=password + "\n", username="owner", ok=True, url=None):
    root = out / ("project-" + name)
    root.mkdir(exist_ok=True)
    argv = ["init", "--server", url or base, "--project", root, "--config-dir", out / ("config-" + name),
            "--profile", "central", "--prefix", out / "install", "--from", archive,
            "--host", "none", "--update-policy", "notify", *choice]
    if username:
        argv.extend(["--username", username, "--password-stdin"])
    return cli(*argv, credential=credential, ok=ok)


def interactive_init(name, responses, ok):
    root = out / ("project-" + name)
    root.mkdir()
    master, slave = pty.openpty()
    process = subprocess.Popen([str(binary), "init", "--project", str(root), "--config-dir", str(out / ("config-" + name)),
                                "--prefix", str(out / "install"), "--from", str(archive), "--host", "none",
                                "--update-policy", "notify", "--json"], stdin=slave, stdout=slave, stderr=slave, env=env, cwd=root)
    os.close(slave)
    output = ""
    next_response = 0
    deadline = time.monotonic() + 25
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                output += chunk.decode(errors="replace")
                if next_response < len(responses) and responses[next_response][0] in output:
                    os.write(master, (responses[next_response][1] + "\n").encode())
                    next_response += 1
            if process.poll() is not None and not ready:
                break
        code = process.wait(timeout=2)
        assert (code == 0) == ok, output
        assert next_response == len(responses), output
        assert password not in output, "interactive password was echoed"
        (out / ("interactive-" + name + ".log")).write_text(output)
        return output
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=5)
        os.close(master)


try:
    cli("server", "bootstrap", "--data-dir", out / "server-data", "--username", "owner", "--password-stdin", credential=password + "\n")
    base = start_server()
    csrf = api("POST", "/api/v1/auth/login", {"username": "owner", "password": password})["csrf"]
    api("POST", "/api/v1/users", {"username": "member", "password": "fixture-member-password"})
    first = initialize("a", "--create", "Same project name")
    project_id = first["project_id"]
    assert first["connected"] and first["specification_state"] == "awaiting_sources", first
    assert first["task_executable"] is False and first["recovery_integration"]["status"] == "explicit_command_and_skill"
    root_a = out / "project-a"
    protected = {name: (root_a / name).read_bytes() for name in ("ael", "AGENTS.md", "CLAUDE.md", ".agents/skills/ael-work/SKILL.md")}
    repeated = initialize("a", "--create", "Same project name", username="", credential=None)
    assert repeated["project_id"] == project_id
    assert all((root_a / name).read_bytes() == content for name, content in protected.items())
    second = initialize("b", "--remote-project", project_id)
    assert second["project_id"] == project_id and second["cli_installation"] == first["cli_installation"]
    assert len(api("GET", "/api/v1/projects")["projects"]) == 1
    assert len(api("GET", "/api/v1/tokens")["tokens"]) == 2
    duplicate_name = initialize("same-name", "--create", "Same project name")
    assert duplicate_name["project_id"] != project_id
    assert len(api("GET", "/api/v1/projects")["projects"]) == 2
    denied = initialize("denied", "--create", "Forbidden", username="member", credential="fixture-member-password\n", ok=False)
    assert "FORBIDDEN" in denied and len(api("GET", "/api/v1/projects")["projects"]) == 2
    # Restart the disposable service between independent phases. This also
    # verifies registry persistence without altering its login-rate policy.
    address = urllib.parse.urlparse(base).netloc
    stop_server()
    assert start_server(address) == base
    interrupted_root = out / "project-interrupted"
    interrupted_root.mkdir()
    (interrupted_root / "ael").write_text("User-owned launcher\n")
    failed = initialize("interrupted", "--create", "Interrupted project", ok=False)
    assert "INIT_LOCAL_SETUP_PENDING" in failed
    interrupted_id = json.loads((interrupted_root / ".ael/remote.json").read_text())["project_id"]
    assert (interrupted_root / "ael").read_text() == "User-owned launcher\n"
    assert len(api("GET", "/api/v1/projects")["projects"]) == 3
    (interrupted_root / "ael").unlink()
    recovered = initialize("interrupted", "--create", "Interrupted project", username="", credential=None)
    assert recovered["project_id"] == interrupted_id
    assert len(api("GET", "/api/v1/projects")["projects"]) == 3
    target = urllib.parse.urlparse(base)

    class LostOnboardingResponse(http.server.BaseHTTPRequestHandler):
        def log_message(self, *unused):
            pass

        def forward(self):
            data = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            headers = {key: value for key, value in self.headers.items() if key.lower() not in ("host", "origin", "connection", "content-length")}
            headers["Host"] = target.netloc
            if "Origin" in self.headers:
                headers["Origin"] = base
            connection = http.client.HTTPConnection(target.hostname, target.port, timeout=10)
            connection.request(self.command, self.path, body=data or None, headers=headers)
            response = connection.getresponse()
            body = response.read()
            if self.path == "/api/v1/onboarding" and self.server.drop_next:
                self.server.drop_next = False
                connection.close()
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
            self.send_response(response.status)
            for key, value in response.getheaders():
                if key.lower() not in ("connection", "transfer-encoding", "content-length"):
                    self.send_header(key, value)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            connection.close()

        do_GET = forward
        do_POST = forward

    proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), LostOnboardingResponse)
    proxy.drop_next = True
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    proxy_url = "http://127.0.0.1:" + str(proxy.server_address[1])
    lost = initialize("lost-response", "--create", "Lost response project", url=proxy_url, ok=False)
    assert "SERVER_UNAVAILABLE" in lost and len(api("GET", "/api/v1/projects")["projects"]) == 4
    resumed = initialize("lost-response", "--create", "Lost response project", url=proxy_url)
    assert len(api("GET", "/api/v1/projects")["projects"]) == 4
    common_responses = [("Server 地址或 profile", base), ("Server 用户名", "owner"),
                        ("凭据（输入隐藏）", password), ("搜索项目", "Same project name")]
    ambiguous = interactive_init("ambiguous", common_responses + [("选择序号或稳定项目 ID", "Same project name")], False)
    assert "INIT_PROJECT_AMBIGUOUS" in ambiguous and project_id in ambiguous and duplicate_name["project_id"] in ambiguous
    interactive = interactive_init("interactive", common_responses + [("选择序号或稳定项目 ID", project_id)], True)
    assert '"project_id":"' + project_id + '"' in interactive
    offline = initialize("offline", "--remote-project", project_id, url="http://127.0.0.1:1", username="", credential=None, ok=False)
    assert "REMOTE_UNAVAILABLE" in offline
    insecure = initialize("insecure", "--remote-project", project_id, url="http://example.invalid", username="", credential=None, ok=False)
    assert "REMOTE_URL_INSECURE" in insecure
    # A shared custom installation and explicit config-dir work through ./ael
    # with AEL_CLI, AEL_INSTALL_PREFIX and AEL_REMOTE_CONFIG_DIR absent.
    for name in ("a", "b"):
        root = out / ("project-" + name)
        result = subprocess.run([str(root / "ael"), "version", "--check", "--json"], cwd=root,
                                env=env, text=True, capture_output=True, timeout=15)
        assert result.returncode == 0, result.stderr
        assert json.loads(result.stdout)["compatible"] is True
        assert not (root / ".ael/bin").exists()
    assert not (out / "unused-local-runtime").exists()
    # User changes to a managed block require explicit reconciliation.
    agents = root_a / "AGENTS.md"
    original = agents.read_text()
    agents.write_text(original.replace("recovery check", "user-changed recovery check"))
    conflict = initialize("a", "--remote-project", project_id, username="", credential=None, ok=False)
    assert "ASSET_CONFLICT" in conflict and "user-changed" in agents.read_text()
    agents.write_text(original)
    if args.mismatched_release:
        other_release = Path(args.mismatched_release).resolve()
        other_manifest = json.loads((other_release / "manifest.json").read_text())
        other_asset = next(item for item in other_manifest["assets"] if item["kind"] == "client" and item["platform"] == native)
        mismatch = initialize("a", "--remote-project", project_id, "--prefix", out / "mismatched-install",
                              "--from", other_release / other_asset["path"], username="", credential=None, ok=False)
        assert "INIT_CLI_SOURCE_MISMATCH" in mismatch, mismatch
        assert not (out / "mismatched-install/bin/ael").exists()
    # Secrets are confined to private machine config, never generated project files.
    for profile in out.glob("config-*/profiles/*.json"):
        token = json.loads(profile.read_text())["token"]
        assert token not in json.dumps(commands)
        for root in out.glob("project-*"):
            for path in root.rglob("*"):
                if path.is_file():
                    assert token.encode() not in path.read_bytes(), path
    summary = {"result": "PASS", "driver": "deterministic packaged CLI and isolated Server",
               "project_id": project_id, "second_machine_project_id": second["project_id"],
               "shared_cli": first["cli_installation"], "same_name_distinct_id": duplicate_name["project_id"],
               "interruption_recovered_id": interrupted_id, "runtime_created": False,
               "lost_response_recovered_id": resumed["project_id"],
               "cases": ["create", "second-machine-connect", "repeat-init", "same-name-distinct-projects",
                         "permission-denied", "offline", "insecure-origin", "remote-created-local-conflict",
                         "retry-keeps-project-id", "lost-registration-response", "interactive-hidden-password",
                         "interactive-same-name-ambiguity", "interactive-stable-id-connect", "custom-install-launcher",
                         "explicit-config-launcher", "user-managed-block-conflict"]}
    (out / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary))
finally:
    stop_server()
    if proxy is not None:
        proxy.shutdown()
        proxy.server_close()
    server_log.close()
    (out / "commands.json").write_text(json.dumps(commands, ensure_ascii=False, indent=2) + "\n")
    (out / "commands.json").chmod(0o600)

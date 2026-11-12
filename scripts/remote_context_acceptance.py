#!/usr/bin/env python3
"""Exercise portable candidates and source snapshots in isolated Git checkouts."""
import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import platform
import select
import subprocess
import urllib.error
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--bundle", required=True)
parser.add_argument("--out", required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
bundle, out = Path(args.bundle).resolve(), Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("refusing a nonempty fixture directory")
out.mkdir(parents=True, exist_ok=True)
(out / "ISOLATED-CONTEXT").write_text("Disposable server and Git checkouts only.\n")
system = {"darwin": "darwin", "linux": "linux"}.get(platform.system().lower())
arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit("unsupported fixture platform")
binary = bundle / f"ael-{system}-{arch}"
env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
env.update(AEL_DATA_DIR=str(out / "unused-runtime"), AEL_INSTALL_PREFIX=str(out / "unused-install"),
           GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT="0")
commands = []


def run(argv, cwd, run_env=env, input_text=None, expect_ok=True):
    result = subprocess.run([str(x) for x in argv], cwd=cwd, env=run_env, input=input_text,
                            capture_output=True, text=True, timeout=45)
    commands.append({"argv": [str(x) for x in argv], "exit": result.returncode,
                     "stdout": result.stdout, "stderr": result.stderr})
    if (result.returncode == 0) != expect_ok:
        raise RuntimeError(f"unexpected command result: {argv}; {result.stderr}")
    return json.loads(result.stdout) if expect_ok and str(argv[0]) == str(binary) else result.stdout


def git(cwd, *argv):
    return run(["git", *argv], cwd)


origin = out / "origin"
origin.mkdir()
git(origin, "init", "-b", "main")
git(origin, "config", "user.name", "Fixture")
git(origin, "config", "user.email", "fixture@example.invalid")
(origin / "spec").mkdir()
(origin / "docs").mkdir()
content = "# Shared constraints\nUse the fixture input.\n"
(origin / "spec" / "project.md").write_text(content)
(origin / "spec" / "extra.md").write_text("---\nid: extra-rule\nkind: spec\nstatus: current\npaths: [extra]\n---\n# Extra scope rule\n")
(origin / "docs" / "old.md").write_text("---\nstatus: archived\nkind: reference\n---\nHISTORICAL_SENTINEL\n")
(origin / "app.txt").write_text("version one\n")
(origin / ".gitignore").write_text(".ael/\n.env\nignored.log\n")
git(origin, "add", "--", ".gitignore", "app.txt", "spec/project.md", "spec/extra.md", "docs/old.md")
git(origin, "commit", "-m", "fixture baseline")
projects = {name: out / f"checkout-{name}" for name in ("a", "b")}
for name, directory in projects.items():
    git(out, "clone", "--no-local", str(origin), str(directory))
    git(directory, "config", "user.name", "Fixture")
    git(directory, "config", "user.email", "fixture@example.invalid")
    (directory / ".env").write_text("SENSITIVE_SENTINEL\n")


def client(name, *argv, input_text=None, expect_ok=True):
    client_env = dict(env, AEL_REMOTE_CONFIG_DIR=str(out / f"config-{name}"), AEL_DATA_DIR=str(out / f"runtime-{name}"))
    return run([binary, *argv, "--project", projects[name], "--json"], projects[name], client_env, input_text, expect_ok)


proc, log = None, None
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
    api("POST", "/api/v1/projects", {"id": "shared", "name": "Shared Context"}, expected=201)
    tokens = {}
    for name, username, role in (("a", "writer", "executor"), ("b", "reader", "reviewer")):
        api("POST", "/api/v1/users", {"username": username, "password": f"fixture-{username}-password"}, expected=201)
        api("POST", "/api/v1/projects/shared/members", {"username": username, "role": role}, expected=201)
        tokens[name] = api("POST", "/api/v1/tokens", {"username": username, "project_id": "shared", "label": name}, expected=201)["token"]
        client(name, "server", "profile", "add", "--name", "profile-" + name, "--url", base, "--token-stdin", input_text=tokens[name] + "\n")
        client(name, "project", "connect", "--profile", "profile-" + name, "--remote-project", "shared")
    definition = out / "task.json"
    check_command = ["python3", "-c", "from pathlib import Path; assert Path('app.txt').read_text() == 'version one\\n'; print('fixture check passed')"]
    check_definition = {"id": "unit", "kind": "functional", "method": "command", "command": check_command, "covers": ["result"], "required": True}
    definition.write_text(json.dumps({"id": "task", "title": "Context task", "type": "bug", "goal": "verified", "scope": ["app.txt"], "acceptance": ["result"], "knowledge": ["source:spec/project.md"], "checks": [check_definition]}))
    task = client("a", "task", "create", "--file", definition, "--operation-id", "task", "--idempotency-key", "task")
    revision = task["revision"]
    initial_revision = revision
    client("a", "repository", "register", "--repository", "fixture-repo", "--operation-id", "repo", "--idempotency-key", "repo")
    client("a", "knowledge", "index")

    def capture(name, task_id, task_revision, key):
        return client(name, "candidate", "capture", "--task", task_id, "--repository", "fixture-repo", "--checkout", "checkout-" + name,
                      "--workspace", "workspace-" + name, "--runner", "runner-" + name, "--expected-revision", task_revision,
                      "--operation-id", key, "--idempotency-key", key)

    first, second = capture("a", "task", revision, "candidate-a"), capture("b", "task", revision, "candidate-b")
    assert first["candidate_id"] == second["candidate_id"] and first["id"] != second["id"]
    assert first["checkout_id"] != second["checkout_id"] and first["workspace_id"] != second["workspace_id"]
    published = client("a", "context", "publish", "--task", "task", "--observation", first["id"], "--expected-revision", revision, "--operation-id", "context", "--idempotency-key", "context")
    assert published["inventory_unverified"] is True and published["capture_stability_proven"] is False
    replay = client("a", "context", "publish", "--task", "task", "--observation", first["id"], "--expected-revision", revision, "--operation-id", "context", "--idempotency-key", "context")
    assert published["id"] == replay["id"]
    report_file = out / "check-report.txt"
    report_file.write_text(run(check_command, projects["a"]))
    report = client("a", "artifact", "upload", "--task", "task", "--file", report_file, "--source", "context-check:" + published["id"] + ":unit", "--expected-revision", revision, "--operation-id", "check-report", "--idempotency-key", "check-report")
    check = client("a", "context", "record-check", "--context", published["id"], "--task", "task", "--check", "unit", "--artifact", report["id"], "--expected-revision", revision, "--operation-id", "check", "--idempotency-key", "check")
    assert check["candidate_id"] == first["candidate_id"] and check["gate_effect"] == "none" and check["reusable"] is False
    reader = client("b", "context", "show", "--context", published["id"])
    assert reader["input_equivalence_proven"] is False and reader["usable_for_context"] is False
    assert reader["candidate_id"] == first["candidate_id"] and reader["commit"] == first["commit"]
    assert len(reader["sources"]) == 1 and reader["sources"][0]["content"] == content
    assert reader["sources"][0]["sha256"] == hashlib.sha256(content.encode()).hexdigest()
    assert reader["checks"][0]["check_id"] == "unit" and reader["checks"][0]["artifact_sha256"] == report["sha256"]
    manifest = out / "manifest.json"
    manifest.write_text(json.dumps({"isolated": True, "out": str(out), "url": base, "candidate": first["candidate_id"], "commit": first["commit"], "content": content, "sha256": hashlib.sha256(content.encode()).hexdigest()}))
    ui_env = dict(env, AEL_CONTEXT_FIXTURE=str(manifest), AEL_UI_OUT=str(out / "browser"))
    test = subprocess.run([str(repo / "web/node_modules/.bin/playwright"), "test", "--config=playwright.context.config.ts"], cwd=repo / "web", env=ui_env, timeout=180)
    if test.returncode:
        raise RuntimeError("packaged browser acceptance failed")
    after_browser = client("a", "task", "show", "--task", "task")
    assert after_browser["task"]["paused"] is True
    resumed = client("a", "resume", "--task", "task", "--ack-stopped", "--expected-revision", after_browser["revision"], "--operation-id", "resume-after-browser", "--idempotency-key", "resume-after-browser")
    revision = resumed["revision"]

    # A resumed responsibility has a new operation revision. Publish its current
    # context before testing the independent local-candidate gate.
    resumed_obs = capture("a", "task", revision, "candidate-resumed")
    resumed_context = client("a", "context", "publish", "--task", "task", "--observation", resumed_obs["id"], "--expected-revision", revision, "--operation-id", "context-resumed", "--idempotency-key", "context-resumed")
    # Exercise local candidate mismatch while the server context is still current.
    original_app = (projects["b"] / "app.txt").read_text()
    (projects["b"] / "app.txt").write_text("local mismatch before observation\n")
    client("b", "context", "record-check", "--context", resumed_context["id"], "--task", "task", "--check", "unit", "--artifact", report["id"], "--expected-revision", revision, "--operation-id", "local-mismatch", "--idempotency-key", "local-mismatch", expect_ok=False)
    assert "CANDIDATE_MISMATCH" in commands[-1]["stderr"]
    (projects["b"] / "app.txt").write_text(original_app)

    (projects["b"] / ".ael" / "outcomes").mkdir()
    (projects["b"] / ".ael" / "outcomes" / "report.txt").write_text("runtime log\n")
    (projects["b"] / "ignored.log").write_text("runtime log\n")
    unchanged = capture("b", "task", revision, "candidate-log")
    assert unchanged["candidate_id"] == first["candidate_id"]
    (projects["b"] / "app.txt").write_text("uncommitted change\n")
    changed = capture("b", "task", revision, "candidate-dirty")
    comparison = client("b", "candidate", "compare", "--before", first["candidate_id"], "--after", changed["candidate_id"])
    assert comparison["changed_paths"] == ["app.txt"] and comparison["exactly_equal"] is False and comparison["commit_changed"] is False
    client("b", "context", "show", "--task", "task", expect_ok=False)
    assert "CONTEXT_STALE" in commands[-1]["stderr"] and "app.txt" in commands[-1]["stderr"]
    git(projects["b"], "add", "app.txt")
    git(projects["b"], "commit", "-m", "different commit")
    committed = capture("b", "task", revision, "candidate-commit")
    comparison = client("b", "candidate", "compare", "--before", first["candidate_id"], "--after", committed["candidate_id"])
    assert comparison["commit_changed"] is True and comparison["exactly_equal"] is False

    expanded_def = out / "expanded.json"
    expanded_def.write_text(json.dumps({"id": "task", "title": "Context task", "type": "bug", "goal": "verified", "scope": ["app.txt", "extra"], "acceptance": ["result"], "knowledge": ["source:spec/project.md"], "checks": [check_definition]}))
    expanded = client("a", "task", "edit", "--task", "task", "--file", expanded_def, "--reason", "fixture scope expansion", "--expected-revision", revision, "--operation-id", "expand", "--idempotency-key", "expand")
    revision = expanded["revision"]
    expanded_obs = capture("a", "task", revision, "candidate-expanded")
    expanded_context = client("a", "context", "publish", "--task", "task", "--observation", expanded_obs["id"], "--expected-revision", revision, "--operation-id", "expanded-context", "--idempotency-key", "expanded-context")
    assert {item["id"] for item in expanded_context["sources"]} == {"source:spec/project.md", "extra-rule"}
    client("b", "context", "record-check", "--context", expanded_context["id"], "--task", "task", "--check", "unit", "--artifact", report["id"], "--expected-revision", revision, "--operation-id", "wrong-version-check", "--idempotency-key", "wrong-version-check", expect_ok=False)
    # Scope expansion awaits an accurate-version decision; that earlier gate
    # rejects this report before the client reaches its local candidate check.
    assert "CONTEXT_STALE" in commands[-1]["stderr"]
    assert client("a", "task", "show", "--task", "task")["task"]["confirmation"] is not None

    old_file = projects["a"] / "docs" / "old.md"
    old_content = old_file.read_text()
    old_file.unlink()
    client("a", "knowledge", "index")
    historic_obs = capture("a", "task", revision, "candidate-deleted-history")
    historic_context = client("a", "context", "publish", "--task", "task", "--observation", historic_obs["id"], "--expected-revision", revision, "--operation-id", "deleted-history", "--idempotency-key", "deleted-history")
    assert historic_context["inventory_unverified"] is True and {item["id"] for item in historic_context["sources"]} == {"source:spec/project.md", "extra-rule"}
    old_file.write_text(old_content)
    client("a", "knowledge", "index")

    missing_def = out / "missing.json"
    missing_def.write_text(json.dumps({"id": "missing", "title": "Missing reference", "type": "bug", "goal": "verified", "scope": ["app.txt"], "acceptance": ["result"], "knowledge": ["source:spec/missing.md"]}))
    missing_task = client("a", "task", "create", "--file", missing_def, "--operation-id", "missing-task", "--idempotency-key", "missing-task")
    missing_obs = capture("a", "missing", missing_task["revision"], "missing-observation")
    client("a", "context", "publish", "--task", "missing", "--observation", missing_obs["id"], "--expected-revision", missing_task["revision"], "--operation-id", "missing-context", "--idempotency-key", "missing-context", expect_ok=False)
    assert "KNOWLEDGE_MISSING" in commands[-1]["stderr"]
    (projects["a"] / "spec" / "project.md").unlink()
    replay_after_change = client("a", "context", "publish", "--task", "task", "--observation", first["id"], "--expected-revision", initial_revision, "--operation-id", "context", "--idempotency-key", "context")
    assert replay_after_change["id"] == published["id"] and replay_after_change["replayed"] is True and replay_after_change["usable_for_current_work"] is False
    deleted = capture("a", "task", revision, "candidate-deleted-spec")
    client("a", "context", "publish", "--task", "task", "--observation", deleted["id"], "--expected-revision", revision, "--operation-id", "deleted-context", "--idempotency-key", "deleted-context", expect_ok=False)
    assert "KNOWLEDGE" in commands[-1]["stderr"]
    (projects["a"] / "spec" / "project.md").write_text(content)
    (projects["a"] / "spec" / "conflict.md").write_text("---\nid: conflict-rule\nkind: spec\nstatus: conflict\npaths: [app.txt]\n---\nConflicting baseline.\n")
    conflict = capture("a", "task", revision, "candidate-conflict")
    client("a", "context", "publish", "--task", "task", "--observation", conflict["id"], "--expected-revision", revision, "--operation-id", "stale-index", "--idempotency-key", "stale-index", expect_ok=False)
    assert "KNOWLEDGE_INDEX_STALE" in commands[-1]["stderr"]
    client("a", "knowledge", "index")
    client("a", "context", "publish", "--task", "task", "--observation", conflict["id"], "--expected-revision", revision, "--operation-id", "conflict-context", "--idempotency-key", "conflict-context", expect_ok=False)
    assert "conflict" in commands[-1]["stderr"].lower()
    (projects["a"] / "spec" / "conflict.md").unlink()
    large = "# Large fixture\n" + "x" * (3 << 20)
    (projects["a"] / "spec" / "large.md").write_text(large)
    client("a", "knowledge", "index")
    large_obs = capture("a", "task", revision, "candidate-large")
    large_context = client("a", "context", "publish", "--task", "task", "--observation", large_obs["id"], "--expected-revision", revision, "--operation-id", "large-context", "--idempotency-key", "large-context")
    large_read = client("b", "context", "show", "--context", large_context["id"])
    large_source = next(item for item in large_read["sources"] if item["id"] == "source:spec/large.md")
    assert large_source["content"] == large and large_source["sha256"] == hashlib.sha256(large.encode()).hexdigest()
    for name, project in projects.items():
        assert not (project / ".ael" / "work-items").exists()
        assert not (out / f"runtime-{name}").exists()
    print(json.dumps({"status": "passed", "isolated": True, "checkouts": 2, "same_candidate": first["candidate_id"], "context": published["id"]}))
finally:
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

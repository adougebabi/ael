#!/usr/bin/env python3
"""Drive eight real fixture changes through a packaged remote AEL protocol."""
import argparse
import http.cookiejar
import itertools
import json
import os
from pathlib import Path
import select
import shlex
import shutil
import subprocess
import sys
import urllib.request
import urllib.parse

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--bundle", required=True)
parser.add_argument("--out", required=True)
parser.add_argument("--codex-smoke", action="store_true")
args = parser.parse_args()
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("choose an empty disposable fixture directory")
out.mkdir(parents=True, exist_ok=True)
out.chmod(0o700)
(out / "ISOLATED-FULL-CHAIN").write_text("Same-machine external host driver, real HTTP and eight real source changes.\n")
binary = bundle / "ael-darwin-arm64"
projects = {name: out / ("client-" + name) for name in ("a", "b")}
env = dict(os.environ, AEL_DATA_DIR=str(out / "unused-runtime"), AEL_INSTALL_PREFIX=str(out / "unused-install"), AEL_CLIENT_CONFIG_DIR=str(out / "client-mappings"), GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT="0")
serial = itertools.count(1)
events = []
human_results = []
private_dir = out / "requests"
private_dir.mkdir(mode=0o700)
client_bin = out / "client-bin"
client_bin.mkdir()
for tool in ("git", "python3"):
    os.symlink(shutil.which(tool), client_bin / tool)
assert not (client_bin / "docker").exists()


def run(argv, cwd=None, process_env=None, stdin=None, passed=True):
    result = subprocess.run(list(map(str, argv)), cwd=cwd or out, env=process_env or env, input=stdin, capture_output=True, text=True, timeout=120)
    if (result.returncode == 0) != passed:
        raise RuntimeError(f"{argv[:3]} unexpected exit {result.returncode}: {result.stderr[:1500]}")
    return result


def private(name, value):
    path = private_dir / (name + ".json")
    path.write_text(json.dumps(value, ensure_ascii=False))
    path.chmod(0o600)
    return path


def cli(name, *words, stdin=None, passed=True, text=False):
    process_env = dict(env, AEL_REMOTE_CONFIG_DIR=str(out / ("config-" + name)), AEL_DATA_DIR=str(out / ("runtime-" + name)), PATH=str(client_bin))
    result = run([binary, *words, "--project", projects[name], *([] if text else ["--json"])], projects[name], process_env, stdin, passed)
    if not passed:
        return result.stderr
    return result.stdout if text else json.loads(result.stdout)


def git(name, *words):
    return run(["git", *words], projects[name]).stdout.strip()


def sync(name, source):
    git(name, "fetch", "--no-tags", str(projects[source]), "main")
    git(name, "merge", "--ff-only", "FETCH_HEAD")


projects["a"].mkdir()
git("a", "init", "-b", "main")
for key, value in (("user.name", "Fixture"), ("user.email", "fixture@example.invalid")):
    git("a", "config", key, value)
(projects["a"] / "modules").mkdir()
(projects["a"] / "spec").mkdir()
(projects["a"] / "spec/rules.md").write_text("# Fixture requirements\nEach numbered module returns its number. The final UI displays all eight values and their sum.\n")
(projects["a"] / ".gitignore").write_text("__pycache__/\nbuild/\n")
(projects["a"] / "checks.py").write_text("import importlib.util, pathlib, sys\nnumber=int(sys.argv[1])\npath=pathlib.Path('modules')/f'module_{number:02d}.py'\nspec=importlib.util.spec_from_file_location('subject',path)\nmodule=importlib.util.module_from_spec(spec)\nspec.loader.exec_module(module)\nassert module.value()==number, (module.value(),number)\nprint(f'module {number} passed')\n")
(projects["a"] / "build.py").write_text("import importlib.util,json,pathlib,py_compile\nvalues=[]\nfor number in range(1,9):\n path=pathlib.Path('modules')/f'module_{number:02d}.py'\n if not path.exists():continue\n py_compile.compile(str(path),doraise=True)\n spec=importlib.util.spec_from_file_location(f'module{number}',path)\n module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)\n values.append(module.value())\npathlib.Path('build').mkdir(exist_ok=True)\npathlib.Path('build/data.js').write_text('window.fixtureValues='+json.dumps(values)+';')\nprint(json.dumps({'compiled':len(values),'sum':sum(values)}))\n")
(projects["a"] / "screen.html").write_text("<!doctype html><html><head><meta charset='utf-8'><title>Eight modules</title><style>#calculate{width:50px;white-space:nowrap;overflow:hidden}</style></head><body><h1>Eight modules</h1><ol id='values'></ol><button id='calculate'>Calculate total</button><output id='total'>Waiting</output><script src='build/data.js'></script><script>for(const value of window.fixtureValues||[]){const item=document.createElement('li');item.textContent=value;document.querySelector('#values').append(item)}document.querySelector('#calculate').onclick=()=>document.querySelector('#total').textContent=window.fixtureValues.reduce((a,b)=>a+b,0)</script></body></html>")
git("a", "add", ".")
git("a", "commit", "-m", "fixture initial requirements and real UI")
run(["git", "clone", "--no-local", projects["a"], projects["b"]])
for key, value in (("user.name", "Fixture"), ("user.email", "fixture@example.invalid")):
    git("b", "config", key, value)

server_work = out / "server-work"
server_work.mkdir()
data = out / "server-data"
run([binary, "server", "bootstrap", "--data-dir", data, "--username", "owner", "--password-stdin", "--json"], server_work, stdin="fixture-full-chain-password\n")
log = (out / "server.log").open("w")
process = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(data), "--addr", "127.0.0.1:0", "--json"], cwd=server_work, env=env, stdout=subprocess.PIPE, stderr=log, text=True)
try:
    ready, _, _ = select.select([process.stdout], [], [], 30)
    if not ready:
        raise RuntimeError("server readiness timed out")
    base = json.loads(process.stdout.readline())["url"]
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    csrf = ""

    def api(method, path, body=None):
        headers = {"X-AEL-Protocol": "1", "Origin": base, "X-AEL-CSRF": csrf}
        if body is not None:
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method)
        with opener.open(request, timeout=30) as response:
            return json.load(response)

    csrf = api("POST", "/api/v1/auth/login", {"username": "owner", "password": "fixture-full-chain-password"})["csrf"]
    api("POST", "/api/v1/projects", {"id": "shared", "name": "Eight-module full chain"})
    api("POST", "/api/v1/users", {"username": "human-reviewer", "password": "fixture-reviewer-password"})
    api("POST", "/api/v1/projects/shared/members", {"username": "human-reviewer", "role": "reviewer"})
    for name in ("a", "b"):
        username = "client-" + name
        api("POST", "/api/v1/users", {"username": username, "password": "fixture-client-password"})
        api("POST", "/api/v1/projects/shared/members", {"username": username, "role": "executor"})
        token = api("POST", "/api/v1/tokens", {"username": username, "project_id": "shared", "label": name})["token"]
        cli(name, "server", "profile", "add", "--name", "central", "--url", base, "--token-stdin", stdin=token + "\n")
        cli(name, "project", "connect", "--profile", "central", "--remote-project", "shared")
        cli(name, "knowledge", "index")
        if name == "a":
            cli(name, "repository", "register", "--repository", "fixture-repo", "--operation-id", "repo", "--idempotency-key", "repo")
        registration = {"id": "agent-" + name, "workspace_id": "workspace-" + name, "checkout_id": "checkout-" + name, "repository": "fixture-repo", "version": "external-full-chain", "capabilities": ["development"], "expected_revision": "absent"}
        cli(name, "runner", "register", "--file", private("runner-" + name, registration), "--operation-id", "register-" + name, "--idempotency-key", "register-" + name)

    def command_check(number, parent=False):
        result = {"id": ("module-" if parent else "unit-") + str(number), "kind": "functional", "method": "command", "command": ["python3", "checks.py", str(number)], "covers": ["module " + str(number)], "required": True, "artifacts": ["runtime:report.json"]}
        if number == 4:
            result["artifacts"] = []
        if parent:
            result["affected_tasks"] = [f"leaf-{number:02d}"]
        return result

    def visual_check(parent=False):
        check = {"id": "layout", "kind": "ui", "method": "human", "covers": ["layout readable"], "required": True}
        if parent:
            check["affected_tasks"] = ["leaf-08"]
        return check

    parent = {"id": "eight", "title": "Eight actual modules", "type": "epic", "goal": "verified", "scope": ["modules", "screen.html"], "acceptance": ["module " + str(i) for i in range(1, 9)] + ["layout readable"], "knowledge": ["source:spec/rules.md"], "checks": [command_check(i, True) for i in range(1, 9)] + [visual_check(True)]}
    created = cli("a", "task", "create", "--file", private("parent", parent), "--operation-id", "parent", "--idempotency-key", "parent")
    request = {"request": "Implement eight numbered modules with explicit tests, then verify their final integrated candidate.", "items": []}
    prepared = cli("a", "plan", "prepare", "--task", "eight", "--file", private("input", request), "--operation-id", "prepare", "--idempotency-key", "prepare", "--expected-revision", created["revision"])
    assert prepared["input_hash"]
    request["items"] = [{"id": "provided-" + str(i), "description": "Implement and check module " + str(i)} for i in range(1, 9)]
    prepared = cli("a", "plan", "prepare", "--task", "eight", "--file", private("provided-input", request), "--operation-id", "prepare-provided", "--idempotency-key", "prepare-provided", "--expected-revision", created["revision"])
    leaves = []
    for number in range(1, 9):
        definition = {"id": f"leaf-{number:02d}", "title": f"Actual module {number}", "type": "bug", "goal": "verified", "scope": [f"modules/module_{number:02d}.py"], "non_goals": ["publishing"], "acceptance": ["module " + str(number)], "knowledge": ["source:spec/rules.md"], "checks": [command_check(number)]}
        if number > 1:
            definition["dependencies"] = [f"leaf-{number-1:02d}"]
        if number == 8:
            definition["ui"] = True
            definition["scope"].append("screen.html")
            definition["acceptance"].append("layout readable")
            definition["checks"].append(visual_check())
        leaves.append({"key": "key-" + str(number), "source_ids": ["provided-" + str(number)], "definition": definition, "deliverables": definition["scope"], "basis": ["fixture specification"], "capabilities": ["development"], "order": number, "required": True})
    proposal = {"version": 1, "id": "eight-plan", "parent": "eight", "parent_revision": 1, "input_hash": prepared["input_hash"], "request": request["request"], "user_items": request["items"], "leaves": leaves, "coverage": [{"acceptance": "module " + str(i), "leaves": ["key-" + str(i)], "integration": "Rerun the module check on the final candidate"} for i in range(1, 9)]}
    proposal["coverage"].append({"acceptance": "layout readable", "leaves": ["key-8"], "integration": "Review the actual final UI image"})
    proposal_file = private("proposal", proposal)
    preview = cli("a", "plan", "preview", "--file", proposal_file)
    applied = cli("a", "plan", "apply", "--file", proposal_file, "--digest", preview["digest"], "--operation-id", "eight-plan", "--idempotency-key", "eight-plan", "--expected-revision", created["revision"])
    assert applied["status"] == "applied"
    readable = cli("a", "task", "show", "--task", "eight", text=True)
    assert "eight" in readable and not readable.lstrip().startswith("{")

    def mutation(name, action, body, passed=True, **flags):
        key = "op-" + str(next(serial))
        argv = ["runner", action, "--runner", "agent-" + name, "--file", private(key, dict(body, operation_id=key, idempotency_key=key))]
        for flag, value in flags.items():
            argv.extend(["--" + flag, value])
        return cli(name, *argv, passed=passed)

    def context_for(name, task):
        state = cli(name, "task", "show", "--task", task)
        key = "context-" + str(next(serial))
        observed = cli(name, "candidate", "capture", "--task", task, "--repository", "fixture-repo", "--checkout", "checkout-" + name, "--workspace", "workspace-" + name, "--runner", "agent-" + name, "--expected-revision", state["revision"], "--operation-id", key, "--idempotency-key", key)
        context = cli(name, "context", "publish", "--task", task, "--observation", observed["id"], "--expected-revision", state["revision"], "--operation-id", key + "-pub", "--idempotency-key", key + "-pub")
        return state, context

    claim_files = {}

    def responsibility(name, task):
        state, context = context_for(name, task)
        destination = out / ("claim-" + str(next(serial)) + ".json")
        key = "claim-" + str(next(serial))
        parent_scope = state["task"].get("epic_membership", {}).get("parent", "")
        if task == "eight":
            parent_scope = "eight"
        body = {"task_id": task, "parent_id": parent_scope, "context_id": context["id"], "expected_revision": state["revision"], "operation_id": key, "idempotency_key": key}
        request = private(key, body)
        cli(name, "runner", "claim", "--runner", "agent-" + name, "--file", request, "--out", destination)
        attempt = json.loads(destination.read_text())["attempt"]
        claim_files[attempt["id"]] = destination
        if task == "leaf-01" and state["current_step"] == "define":
            repeated = out / "replayed-first-claim.json"
            cli(name, "runner", "claim", "--runner", "agent-" + name, "--file", request, "--out", repeated)
            assert json.loads(repeated.read_text())["attempt"]["id"] == attempt["id"]
            body.update(operation_id="conflicting-claim", idempotency_key="conflicting-claim")
            rejected = cli(name, "runner", "claim", "--runner", "agent-" + name, "--file", private("conflicting-claim", body), "--out", out / "forbidden-claim.json", passed=False)
            assert "RUNNER_WIP" in rejected or "CLAIM_CONFLICT" in rejected
        lease = {"task_id": task, "attempt_id": attempt["id"], "token": attempt["token"], "fence": attempt["fence"]}
        return state, attempt, lease

    def submit(name, task, state, attempt, lease, files=(), status="pass", method=None, exit_code=None, expected_error=None):
        observed = mutation(name, "observe", dict(lease, expected_revision=state["revision"]), repository="fixture-repo")
        candidate = observed["candidate_id"]
        bindings = []
        for file, ref in files:
            key = "upload-" + str(next(serial))
            lease_file = private(key + "-lease", dict(lease, runner_id="agent-" + name, expected_revision=state["revision"], lease_until=attempt["lease_until"]))
            source = Path(file)
            binary_attachment = source.suffix not in (".json", ".txt", ".log", ".md", ".png")
            uploaded = cli(name, "artifact", "upload", "--task", task, "--attempt", attempt["id"], "--candidate", candidate, "--file", source, "--lease-file", lease_file, "--expected-revision", state["revision"], "--operation-id", key, "--idempotency-key", key, *(["--mime", "application/octet-stream"] if binary_attachment else []))
            bindings.append({"path": ref, "artifact_id": uploaded["id"]})
        result = {"status": status, "summary": "External host completed " + attempt["unit"], "artifacts": [item["artifact_id"] for item in bindings]}
        if state["current_step"] == "execute":
            result["knowledge"] = {"status": "unchanged", "reason": "Published fixture specification was read and retained"}
        body = dict(lease, expected_revision=state["revision"], candidate_id=candidate, result=result, bindings=bindings)
        if method:
            body.update(method=method, inputs=["checks.py"])
        if exit_code is not None:
            body["exit_code"] = exit_code
        response = mutation(name, "submit", body, passed=expected_error is None)
        if expected_error:
            assert expected_error in response, response
        return response

    def submit_text(name, attempt, summary, *flags):
        return cli(name, "runner", "submit", "--lease-file", claim_files[attempt["id"]], "--repository", "fixture-repo", "--status", "pass", "--summary", summary, *flags)

    def human_review(name, task):
        run([sys.executable, "build.py"], projects[name])
        label = "human-" + str(next(serial))
        image = out / (label + ".png")
        module = (Path(__file__).resolve().parent.parent / "web/node_modules/@playwright/test/index.mjs").as_uri()
        browser_script = Path(__file__).resolve().with_name("full_chain_browser.mjs")
        render_config = private(label + "-render", {"playwright": module, "input": (projects[name] / "screen.html").as_uri(), "image": str(image)})
        observed = json.loads(run(["node", browser_script, "render", render_config]).stdout)
        assert observed["total"] == "36"
        state, context = context_for(name, task)
        key = label + "-image"
        uploaded = cli(name, "artifact", "upload", "--task", task, "--file", image, "--context", context["id"], "--candidate", state["task"]["candidate"], "--source", "external-host Chromium UI; viewport=640x480", "--expected-revision", state["revision"], "--operation-id", key, "--idempotency-key", key)
        result = "fail" if observed["clipped"] else "pass"
        review_config = private(label + "-review", {"playwright": module, "url": base, "task": task, "artifact": uploaded["id"], "result": result, "reason": "External Chromium measured " + json.dumps(observed), "screen": str(out / (label + "-dashboard.png"))})
        accepted = json.loads(run(["node", browser_script, "review", review_config]).stdout)
        assert accepted["accepted"]
        reviewed = cli(name, "task", "show", "--task", task)
        evidence = (reviewed["task"].get("evidence_history", []) + reviewed["task"]["evidence"])[-1]
        assert evidence["source"] == "human:human-reviewer"
        human_results.append({"task": task, "result": result, "candidate": state["task"]["candidate"], "artifact": uploaded["id"], "measurements": observed, "response_loss_replayed": accepted["acceptedResponseDroppedAndReplayed"]})
        return result

    latest = "a"
    failed_once = False
    failed_candidate = None
    human_failed = False
    state, context = context_for("a", "eight")
    rejected = cli("a", "task", "integrate", "--task", "eight", "--context", context["id"], "--expected-revision", state["revision"], "--operation-id", "early-integration", "--idempotency-key", "early-integration", passed=False)
    assert "EPIC_REQUIRED_LEAF_NOT_ACCEPTED" in rejected
    assert cli("a", "task", "show", "--task", "eight")["task"]["status"] == "open"
    for number in range(1, 9):
        name = "a" if number % 2 else "b"
        if name != latest:
            sync(name, latest)
        task_id = f"leaf-{number:02d}"
        for step_count in range(8):
            state = cli(name, "task", "show", "--task", task_id)
            if state["task"]["status"] == "completed":
                break
            choice = cli(name, "runner", "next", "--runner", "agent-" + name, "--parent", "eight")
            if choice["action"] == "wait_human" and number == 8 and state["current_step"] == "verify":
                verdict = human_review(name, task_id)
                human_failed = human_failed or verdict == "fail"
                continue
            assert choice["action"] == "ready" and choice["checkpoint"]["task"] == task_id, choice
            state, attempt, lease = responsibility(name, task_id)
            mutation(name, "heartbeat", lease)
            mutation(name, "progress", dict(lease, summary="External host working", stage=state["current_step"], next="submit measured result"))
            if state["current_step"] == "execute":
                value = 0 if number == 3 and not failed_once else number
                path = projects[name] / f"modules/module_{number:02d}.py"
                path.write_text(f"def value():\n    return {value}\n")
                if number == 8 and human_failed:
                    screen = projects[name] / "screen.html"
                    screen.write_text(screen.read_text().replace("width:50px", "width:auto"))
                run([sys.executable, "build.py"], projects[name])
                git(name, "add", str(path.relative_to(projects[name])))
                if number == 8:
                    git(name, "add", "screen.html")
                git(name, "commit", "-m", f"external module {number} value {value}")
                outputs = [(path, str(path.relative_to(projects[name])))]
                if number == 8:
                    outputs.append((projects[name] / "screen.html", "screen.html"))
                if number == 4:
                    executed = submit_text(name, attempt, "Module 4 now returns 4 from value(). The integration check is pending.")
                    assert executed["next"]["current_step"] == "verify" and not executed["next"]["task"]["attempts"][-1]["outputs"]
                else:
                    submit(name, task_id, state, attempt, lease, outputs)
                if number == 4:
                    receiver = "a"
                    sync(receiver, name)
                    receiving_state, receiving_context = context_for(receiver, task_id)
                    handed = mutation(receiver, "recover", {"task_id": task_id, "attempt_id": attempt["id"], "context_id": receiving_context["id"], "expected_revision": receiving_state["revision"], "mode": "independent-workspace", "reason": "External developer supplies exact committed source to separate verifier"})
                    assert handed["workspace_decision"] == "independent-workspace"
                    name = receiver
            elif state["current_step"] == "verify":
                check = state["task"]["definition"]["checks"][0]
                result = subprocess.run([sys.executable, *check["command"][1:]], cwd=projects[name], env=env, capture_output=True, text=True)
                if result.returncode:
                    assert number == 3 and not failed_once
                    failed_once = True
                observed = mutation(name, "observe", dict(lease, expected_revision=state["revision"]), repository="fixture-repo")
                report = {"candidate": observed["candidate_id"], "check": check["id"], "definition_revision": state["definition_revision"], "plan_revision": state["plan_revision"], "method": "command", "source": "external-command:agent-" + name, "covers": check["covers"], "observations": result.stdout + result.stderr}
                if number == 3 and result.returncode:
                    failed_candidate = observed["candidate_id"]
                if number == 3 and result.returncode == 0:
                    assert failed_candidate and failed_candidate != observed["candidate_id"]
                    stale_report = dict(report, candidate=failed_candidate)
                    submit(name, task_id, state, attempt, lease, [(private("stale-candidate-report", stale_report), "runtime:report.json")], method="command", exit_code=0, expected_error="ASSESSMENT_EVIDENCE_REQUIRED")
                if number == 4:
                    assert result.returncode == 0
                    submit_text(name, attempt, "The module 4 command read the integrated value and returned 4.", "--exit-code", 0, "--candidate", observed["candidate_id"])
                else:
                    submit(name, task_id, state, attempt, lease, [(private("report-" + str(next(serial)), report), "runtime:report.json")], "pass" if result.returncode == 0 else "fail", "command", result.returncode)
            else:
                submit(name, task_id, state, attempt, lease)
        else:
            raise RuntimeError("leaf exceeded the explicit bounded responsibility budget")
        assert cli(name, "task", "show", "--task", task_id)["task"]["status"] == "completed"
        latest = name
        events.append({"leaf": task_id, "client": name, "source_commit": git(name, "rev-parse", "HEAD")})
        if number == 2:
            cached = cli(name, "task", "show", "--task", task_id)
            process.terminate()
            process.wait(timeout=10)
            offline = cli(name, "task", "show", "--task", task_id)
            assert offline["online"] is False and offline["authoritative"] is False and cached["revision"] == offline["revision"]
            rejected = cli(name, "runner", "next", "--runner", "agent-" + name, "--parent", "eight", passed=False)
            assert "REMOTE_UNAVAILABLE" in rejected
            log.close()
            log = (out / "server-restart.log").open("w")
            process = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(data), "--addr", urllib.parse.urlsplit(base).netloc, "--json"], cwd=server_work, env=env, stdout=subprocess.PIPE, stderr=log, text=True)
            ready, _, _ = select.select([process.stdout], [], [], 30)
            assert ready and json.loads(process.stdout.readline())["url"] == base
            assert cli(name, "task", "show", "--task", task_id)["revision"] == cached["revision"]
    assert failed_once
    assert not cli("a", "artifact", "list", "--task", "leaf-04")["items"]
    module = (Path(__file__).resolve().parent.parent / "web/node_modules/@playwright/test/index.mjs").as_uri()
    browser_script = Path(__file__).resolve().with_name("full_chain_browser.mjs")
    text_config = private("text-result-browser", {"playwright": module, "url": base, "task": "leaf-04", "legacy": "leaf-01"})
    run(["node", browser_script, "text-result", text_config])
    if latest != "a":
        sync("a", latest)
    retained = projects["a"] / "modules/module_04.py"
    accepted_bytes = retained.read_bytes()
    retained.write_text("def value():\n    return 0\n")
    reverted_state, reverted_context = context_for("a", "eight")
    rejected = cli("a", "task", "integrate", "--task", "eight", "--context", reverted_context["id"], "--expected-revision", reverted_state["revision"], "--operation-id", "reverted-output", "--idempotency-key", "reverted-output", passed=False)
    assert "EPIC_LEAF_NOT_INCLUDED" in rejected
    retained.write_bytes(accepted_bytes)
    state, context = context_for("a", "eight")
    assert state["task"]["status"] == "open"
    integrated = cli("a", "task", "integrate", "--task", "eight", "--context", context["id"], "--expected-revision", state["revision"], "--operation-id", "integrate", "--idempotency-key", "integrate")
    assert integrated["prepared"] and len(integrated["integration"]["leaves"]) == 8
    for _ in range(10):
        state = cli("a", "task", "show", "--task", "eight")
        if state["task"]["status"] == "completed":
            break
        choice = cli("a", "runner", "next", "--runner", "agent-a", "--parent", "eight")
        if choice["action"] == "wait_human":
            assert human_review("a", "eight") == "pass"
            continue
        state, attempt, lease = responsibility("a", "eight")
        check = next(item for item in state["task"]["definition"]["checks"] if attempt["unit"] == "check:" + item["id"])
        result = run([sys.executable, *check["command"][1:]], projects["a"])
        observed = mutation("a", "observe", dict(lease, expected_revision=state["revision"]), repository="fixture-repo")
        report = {"candidate": observed["candidate_id"], "check": check["id"], "definition_revision": state["definition_revision"], "plan_revision": state["plan_revision"], "method": "command", "source": "external-command:agent-a", "covers": check["covers"], "observations": result.stdout}
        if check["id"] == "module-4":
            submit_text("a", attempt, "The module 4 command returned the expected value on the parent candidate.", "--exit-code", 0, "--candidate", observed["candidate_id"])
        else:
            submit("a", "eight", state, attempt, lease, [(private("parent-report-" + str(next(serial)), report), "runtime:report.json")], method="command", exit_code=0)
    assert cli("a", "task", "show", "--task", "eight")["task"]["status"] == "completed"
    completed_parent = cli("a", "task", "show", "--task", "eight")["task"]
    screen = projects["a"] / "screen.html"
    screen.write_text(screen.read_text().replace("width:auto", "width:50px"))
    git("a", "add", "screen.html")
    git("a", "commit", "-m", "new fixture feedback after parent delivery")
    run([sys.executable, "build.py"], projects["a"])
    sync("b", "a")
    run([sys.executable, "build.py"], projects["b"])
    state, context = context_for("b", "leaf-08")
    module = (Path(__file__).resolve().parent.parent / "web/node_modules/@playwright/test/index.mjs").as_uri()
    browser_script = Path(__file__).resolve().with_name("full_chain_browser.mjs")
    feedback_image = out / "feedback.png"
    rendered = json.loads(run(["node", browser_script, "render", private("feedback-render", {"playwright": module, "input": (projects["b"] / "screen.html").as_uri(), "image": str(feedback_image)})]).stdout)
    assert rendered["clipped"]
    feedback_candidate = context["candidate_id"]

    def feedback_upload(path, source, key):
        return cli("b", "artifact", "upload", "--task", "leaf-08", "--file", path, "--context", context["id"], "--candidate", feedback_candidate, "--source", source, "--expected-revision", state["revision"], "--operation-id", key, "--idempotency-key", key)["id"]

    image_id = feedback_upload(feedback_image, "external new-feedback screenshot", "feedback-image")
    trace_id = feedback_upload(private("feedback-observation", rendered), "external new-feedback interaction trace", "feedback-trace")
    report_id = feedback_upload(private("feedback-report", {"observations": rendered}), "context-check:" + context["id"] + ":layout", "feedback-report")
    finding_body = {"context_id": context["id"], "task_id": "leaf-08", "candidate_id": feedback_candidate, "check_id": "layout", "expected_revision": state["revision"], "artifact_id": report_id, "source": {"method": "scenario", "tool": "external-Chromium", "run_id": "new-feedback"}, "findings": [{"rule": "ui.feedback-clipping", "title": "New feedback clips the total button", "kind": "ui", "severity": "high", "route": "/fixture", "location": {"path": "screen.html", "selector": "#calculate"}, "expected": "Complete action label is readable", "actual": "Measured scroll width exceeds client width", "reproduction": ["Open the changed fixture", "Inspect and click Calculate total"], "impact": "Action text is truncated", "viewport": {"width": 640, "height": 480}, "environment": "external Chromium", "artifacts": [image_id], "trace_artifacts": [trace_id]}], "operation_id": "feedback", "idempotency_key": "feedback"}
    finding_id = cli("b", "finding", "report", "--file", private("finding-feedback", finding_body))["findings"][0]["id"]
    finding = cli("a", "finding", "show", "--finding", finding_id)
    repair_body = {"finding_id": finding_id, "expected_revision": finding["revision"], "expected_task_revision": finding["source_task_revision"], "reason": "Repair the measured new feedback while retaining original parent delivery", "operation_id": "feedback-repair", "idempotency_key": "feedback-repair"}
    repaired = cli("a", "finding", "repair", "--finding", finding_id, "--file", private("feedback-repair", repair_body))
    repair_id = repaired["repair"]["task_id"]
    repair_task = cli("a", "task", "show", "--task", repair_id)["task"]
    assert repair_task["definition"]["parent"] == "" and repair_task["definition"]["feedback_of"] == "leaf-08"
    for _ in range(8):
        state = cli("a", "task", "show", "--task", repair_id)
        if state["task"]["status"] == "completed":
            break
        if state["current_step"] == "verify" and any(item["check"] == "unit-8" and item["result"] == "pass" for item in (state["task"]["evidence"] or [])):
            assert human_review("a", repair_id) == "pass"
            continue
        state, attempt, lease = responsibility("a", repair_id)
        if state["current_step"] == "execute":
            screen.write_text(screen.read_text().replace("width:50px", "width:auto"))
            git("a", "add", "screen.html")
            git("a", "commit", "-m", "external repair of new feedback")
            run([sys.executable, "build.py"], projects["a"])
            submit("a", repair_id, state, attempt, lease, [(screen, "screen.html")])
        elif state["current_step"] == "verify":
            check = next(item for item in state["task"]["definition"]["checks"] if attempt["unit"] == "check:" + item["id"])
            checked = run([sys.executable, *check["command"][1:]], projects["a"])
            observed = mutation("a", "observe", dict(lease, expected_revision=state["revision"]), repository="fixture-repo")
            report = {"candidate": observed["candidate_id"], "check": check["id"], "definition_revision": state["definition_revision"], "plan_revision": state["plan_revision"], "method": "command", "source": "external-command:agent-a", "covers": check["covers"], "observations": checked.stdout}
            submit("a", repair_id, state, attempt, lease, [(private("feedback-check", report), "runtime:report.json")], method="command", exit_code=0)
        else:
            submit("a", repair_id, state, attempt, lease)
    finding = cli("a", "finding", "show", "--finding", finding_id)
    repair_state = cli("a", "task", "show", "--task", repair_id)
    revalidation = {"finding_id": finding_id, "expected_revision": finding["revision"], "expected_task_revision": finding["source_task_revision"], "reason": "Fresh independent browser review confirms the repaired new candidate", "repair_task_id": repair_id, "expected_repair_revision": repair_state["revision"], "candidate_id": repair_state["task"]["candidate"], "evidence_sha256": finding["repair_evidence_sha256"], "operation_id": "feedback-revalidate", "idempotency_key": "feedback-revalidate"}
    cli("a", "finding", "revalidate", "--finding", finding_id, "--file", private("feedback-revalidate", revalidation))
    assert cli("a", "finding", "show", "--finding", finding_id)["status"] == "resolved"
    retained = cli("a", "task", "show", "--task", "eight")["task"]
    assert retained["completed"] == completed_parent["completed"] and retained["candidate"] == completed_parent["candidate"]
    probe_check = {"id": "persistent", "kind": "functional", "method": "command", "command": ["python3", "checks.py", "0"], "covers": ["failure investigation"], "required": True}
    probe = {"id": "bounded-probe", "title": "Bounded persistent fixture failure", "type": "bug", "goal": "verified", "scope": ["modules/module_08.py"], "acceptance": ["failure investigation"], "knowledge": ["source:spec/rules.md"], "max_retries": 2, "checks": [probe_check]}
    cli("a", "task", "create", "--file", private("bounded-definition", probe), "--operation-id", "bounded-create", "--idempotency-key", "bounded-create")
    failures = 0

    def persistent_attempt():
        state, attempt, lease = responsibility("a", "bounded-probe")
        if state["current_step"] == "execute":
            source = projects["a"] / "modules/module_08.py"
            return submit("a", "bounded-probe", state, attempt, lease, [(source, "modules/module_08.py")])
        if state["current_step"] != "verify":
            return submit("a", "bounded-probe", state, attempt, lease)
        checked = subprocess.run([sys.executable, *probe_check["command"][1:]], cwd=projects["a"], env=env, capture_output=True, text=True)
        assert checked.returncode != 0
        observed = mutation("a", "observe", dict(lease, expected_revision=state["revision"]), repository="fixture-repo")
        report = {"candidate": observed["candidate_id"], "check": "persistent", "definition_revision": state["definition_revision"], "plan_revision": state["plan_revision"], "method": "command", "source": "external-command:agent-a", "covers": probe_check["covers"], "observations": checked.stdout + checked.stderr}
        return submit("a", "bounded-probe", state, attempt, lease, [(private("persistent-report", report), "runtime:report.json")], status="fail", method="command", exit_code=checked.returncode)

    for _ in range(8):
        state = cli("a", "task", "show", "--task", "bounded-probe")
        if state["task"]["paused"]:
            break
        failures += state["current_step"] == "verify"
        persistent_attempt()
    stopped = cli("a", "task", "show", "--task", "bounded-probe")
    assert stopped["task"]["paused"] and failures == 2 and stopped["task"]["blocker"]["category"] == "repeated_failure"
    before_count = len(stopped["task"]["attempts"])
    state, context = context_for("a", "bounded-probe")
    rejected = cli("a", "runner", "claim", "--runner", "agent-a", "--file", private("stopped-claim", {"task_id": "bounded-probe", "context_id": context["id"], "expected_revision": state["revision"], "operation_id": "stopped-claim", "idempotency_key": "stopped-claim"}), "--out", out / "forbidden-stopped-claim.json", passed=False)
    assert "NOT_RUNNABLE" in rejected or "ROOT_CAUSE_REVIEW_REQUIRED" in rejected
    investigation = private("bounded-investigation", {"tried": "Ran the same declared command twice on unchanged source", "excluded": "Network, server restart and module 8 implementation were independently checked", "hypothesis": "The deliberate module 0 lookup is the retained fixture root cause"})
    evidence = cli("a", "artifact", "upload", "--task", "bounded-probe", "--file", investigation, "--context", context["id"], "--candidate", context["candidate_id"], "--source", "bounded external root-cause investigation", "--expected-revision", state["revision"], "--operation-id", "investigation", "--idempotency-key", "investigation")["id"]
    review = {"actor": "forged-actor", "tried": "Two identical failing commands on unchanged source", "excluded": "Transport and module 8 correctness", "hypothesis": "Intentional missing module 0", "next": "Permit one retained-input retry to verify the stop rule", "recovery": "Retain the bounded review and stop if no new evidence appears", "evidence": [evidence]}
    reviewed = cli("a", "task", "review-failure", "--task", "bounded-probe", "--file", private("bounded-review", review), "--expected-revision", state["revision"], "--operation-id", "bounded-review", "--idempotency-key", "bounded-review")
    cycle = reviewed["task"]["failure_cycles"][reviewed["task"]["active_failure"]]
    assert cycle["count"] == 2 and cycle["review"]["actor"] == "client-a"
    cli("a", "resume", "--task", "bounded-probe", "--expected-revision", reviewed["revision"], "--operation-id", "bounded-resume", "--idempotency-key", "bounded-resume")
    for _ in range(3):
        if cli("a", "task", "show", "--task", "bounded-probe")["task"]["paused"]:
            break
        persistent_attempt()
    blocked = cli("a", "task", "show", "--task", "bounded-probe")
    cycle = blocked["task"]["failure_cycles"][blocked["task"]["active_failure"]]
    assert blocked["task"]["paused"] and cycle["count"] == 3 and cycle["state"] == "blocked"
    rejected = cli("a", "task", "review-failure", "--task", "bounded-probe", "--file", private("bounded-repeat-review", review), "--expected-revision", blocked["revision"], "--operation-id", "bounded-repeat-review", "--idempotency-key", "bounded-repeat-review", passed=False)
    assert "ROOT_CAUSE_NEW_EVIDENCE_REQUIRED" in rejected
    assert len(blocked["task"]["attempts"]) <= before_count + 2
    bounded_failure = {"initial_failures": 2, "one_limited_review": True, "retained_failure_count": cycle["count"], "final_state": cycle["state"], "same_evidence_review_rejected": True}
    agent_smoke = {"status": "not_requested"}
    if args.codex_smoke:
        codex = shutil.which("codex")
        if not codex:
            raise RuntimeError("real Codex smoke requested but CLI is unavailable")
        final = out / "codex-final.json"
        schema = private("codex-schema", {"type": "object", "properties": {"task_id": {"type": "string"}, "status": {"type": "string"}, "candidate": {"type": "string"}}, "required": ["task_id", "status", "candidate"], "additionalProperties": False})
        command = [str(binary), "task", "show", "--task", "eight", "--project", str(projects["a"]), "--config-dir", str(out / "config-a"), "--json"]
        prompt = "Verify this disposable project by actively invoking the supplied AEL CLI with your shell tool. Run this exact command: " + " ".join(shlex.quote(value) for value in command) + ". Read the actual JSON response and return task_id from task.definition.id, status from task.status, candidate from task.candidate. Keep this verification read-only. The AEL server is already running on localhost. Use the existing host configuration and authentication."
        smoke_env = dict(os.environ, AEL_REMOTE_CONFIG_DIR=str(out / "config-a"))
        with (out / "codex-events.jsonl").open("w") as stdout, (out / "codex-stderr.log").open("w") as stderr:
            result = subprocess.run([codex, "exec", "--ephemeral", "--json", "--output-schema", str(schema), "--output-last-message", str(final), prompt], cwd=projects["a"], env=smoke_env, stdout=stdout, stderr=stderr, timeout=240)
        if result.returncode != 0:
            raise RuntimeError("real Codex smoke failed; see private Codex events/stderr")
        reported = json.loads(final.read_text())
        actual = cli("a", "task", "show", "--task", "eight")["task"]
        assert reported == {"task_id": "eight", "status": actual["status"], "candidate": actual["candidate"]}
        tool_events = [json.loads(line) for line in (out / "codex-events.jsonl").read_text().splitlines() if line.strip()]
        assert any(event.get("item", {}).get("type") == "command_execution" and "ael-" in event.get("item", {}).get("command", "") for event in tool_events)
        agent_smoke = {"status": "PASS", "agent": "existing Codex CLI", "configuration": "existing model, authentication and permission configuration", "actual_task": reported}
    assert human_failed and [entry["result"] for entry in human_results] == ["fail", "pass", "pass", "pass"]
    summary = {"result": "PASS", "mode": "same-machine isolated real HTTP and external deterministic driver", "eight_actual_modules": events, "one_real_failure_repaired": failed_once, "same_task_handoff": {"task": "leaf-04", "developer": "b", "verifier": "a"}, "plan_inputs": {"unsplit_prepared": True, "provided_eight_items_applied": True}, "default_text_cli": True, "faults": {"unfinished_parent_rejected": True, "same_key_claim_replayed": True, "different_key_claim_rejected": True, "offline_cache_non_authoritative": True, "server_restart_retained_revision": True, "old_candidate_report_rejected": True}, "bounded_failure": bounded_failure, "human_reviews": human_results, "reviewer_fixture": "independent authenticated account driven by external browser automation", "new_feedback": {"finding": finding_id, "repair": repair_id, "resolved": True, "parent_delivery_retained": True}, "agent_smoke": agent_smoke, "parent_fresh_checks": 9, "client_without_docker": True, "oracle_access": False}
    (out / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary))
finally:
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)
    log.close()

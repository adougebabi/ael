#!/usr/bin/env python3
"""Exercise installed AEL in a new, isolated project; executors are test programs, not Agents."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import sqlite3
import subprocess
import sys
import tempfile


parser = argparse.ArgumentParser()
parser.add_argument("--bundle", default="dist")
parser.add_argument("--out", help="new or empty directory for fixture and evidence")
args = parser.parse_args()
bundle = Path(args.bundle).resolve()
out = Path(args.out).resolve() if args.out else Path(tempfile.mkdtemp(prefix="ael-concurrency-"))
if out.exists() and any(out.iterdir()):
    raise SystemExit(f"refusing nonempty output directory: {out}")
out.mkdir(parents=True, exist_ok=True)
root, runtime, barrier = (out / name for name in ("project", "runtime", "barrier"))
for path in (root, runtime, barrier):
    path.mkdir()
machine = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(platform.machine().lower())
system = {"darwin": "darwin", "linux": "linux"}.get(platform.system().lower())
if not system or not machine:
    raise SystemExit("unsupported native platform for this fixture")
binary = bundle / f"ael-{system}-{machine}"
env = dict(os.environ, AEL_DATA_DIR=str(runtime))
commands = []


def run(*argv, expect_ok=True, timeout=60):
    result = subprocess.run([str(x) for x in argv], env=env, text=True, capture_output=True, timeout=timeout)
    commands.append({"argv": [str(x) for x in argv], "code": result.returncode, "stdout": result.stdout, "stderr": result.stderr})
    if (result.returncode == 0) != expect_ok:
        raise RuntimeError(f"unexpected exit {result.returncode}: {result.stdout}\n{result.stderr}")
    if not result.stdout:
        return {}
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError:
        return {"stdout": result.stdout}


def cli(*args, expect_ok=True):
    return run(root / "ael", *args, "--project", root, expect_ok=expect_ok)


def json_file(name, data):
    path = out / name
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2))
    return path


def create(name, filename):
    criterion = f"{filename} contains the expected fixture value"
    definition = {
        "id": name, "title": name, "type": "bug", "goal": "verified", "scope": [filename],
        "acceptance": [criterion], "checks": [{"id": "content", "kind": "unit", "method": "command",
            "command": [sys.executable, "-B", "-c", f"from pathlib import Path; assert Path({filename!r}).read_text() == 'done\\n'"],
            "covers": [criterion], "required": True}],
    }
    cli("task", "create", "--file", json_file(name + ".json", definition))


executor = out / "fixture_executor.py"
executor.write_text('''import json, os, pathlib, sys, time
name, barrier = sys.argv[1], pathlib.Path(sys.argv[2])
step = os.environ["AEL_STEP"]
artifacts = []
if step == "execute":
    started = time.monotonic_ns()
    (barrier / (name + ".started")).write_text(str(started))
    deadline = time.monotonic() + 30
    while not all((barrier / (x + ".started")).exists() for x in ("a", "b")):
        if time.monotonic() > deadline: raise RuntimeError("other executor never started")
        time.sleep(0.01)
    pathlib.Path(name + ".txt").write_text("done\\n")
    (barrier / (name + ".written")).write_text(str(time.monotonic_ns()))
    while not all((barrier / (x + ".written")).exists() for x in ("a", "b")):
        if time.monotonic() > deadline: raise RuntimeError("other executor never wrote")
        time.sleep(0.01)
    (barrier / (name + ".ended")).write_text(str(time.monotonic_ns()))
    artifacts = [name + ".txt"]
result = {"status":"pass", "summary":"deterministic fixture executor completed " + step,
          "artifacts":artifacts, "knowledge":{"status":"unchanged","reason":"isolated fixture; no project contract changed","artifacts":[]}}
pathlib.Path(os.environ["AEL_RESULT"]).write_text(json.dumps(result))
''')

summary = {"fixture": "isolated temporary project; deterministic executor programs, not real Agents", "out": str(out)}
try:
    run(binary, "init", "--project", root, "--bundle-dir", bundle)
    summary["version"] = cli("version")
    summary["data_dir"] = cli("doctor")["data_dir"]
    for name in ("a", "b"):
        create("parallel-" + name, name + ".txt")
    processes = []
    for name in ("a", "b"):
        cfg = json_file(name + "-executor.json", {"command": [sys.executable, str(executor), name, str(barrier)], "timeout_seconds": 40})
        argv = [str(root / "ael"), "loop", "run", "--task", "parallel-" + name, "--config", str(cfg), "--project", str(root)]
        processes.append((name, argv, subprocess.Popen(argv, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)))
    outcomes = {}
    for name, argv, proc in processes:
        stdout, stderr = proc.communicate(timeout=60)
        commands.append({"argv": argv, "code": proc.returncode, "stdout": stdout, "stderr": stderr})
        if proc.returncode:
            raise RuntimeError(f"parallel {name} exited {proc.returncode}: {stderr}\n{stdout}")
        outcomes[name] = json.loads(stdout)
        if outcomes[name].get("next_action") != "completed":
            raise RuntimeError(f"parallel {name} did not complete: {stdout}")
    times = {name: {key: int((barrier / (name + "." + key)).read_text()) for key in ("started", "written", "ended")} for name in ("a", "b")}
    overlap = min(times["a"]["ended"], times["b"]["ended"]) - max(times["a"]["started"], times["b"]["started"])
    if overlap <= 0 or any((root / (name + ".txt")).read_text() != "done\n" for name in ("a", "b")):
        raise RuntimeError("actual executor work did not overlap or output was incorrect")
    for name in ("a", "b"):
        task = outcomes[name]["task"]
        if task["definition"]["id"] != "parallel-" + name or not any(e["result"] == "pass" for e in task["evidence"]):
            raise RuntimeError("task or validation evidence was mixed")
    summary["parallel_loops"] = {"status": "passed", "actual_executor_overlap_ms": overlap / 1_000_000, "times_monotonic_ns": times,
                                 "work": ["project/a.txt", "project/b.txt"], "checks": "both actual command checks passed",
                                 "qualification": "two real CLI processes and deterministic child processes; no real Agent inferred"}

    # Demonstrate the remaining whole-workspace candidate coupling with real CLI calls.
    create("candidate-a", "candidate-a.txt")
    for stage in ("define", "execute"):
        claim = cli("claim", "--task", "candidate-a", "--executor", "fixture")
        cli("context", "--task", "candidate-a")
        artifacts = []
        if stage == "execute":
            (root / "candidate-a.txt").write_text("done\n")
            artifacts = ["candidate-a.txt"]
        attempt = claim["attempt"]
        submission = {"task": "candidate-a", "attempt": attempt["id"], "token": attempt["token"], "key": attempt["id"],
                      "result": {"status": "pass", "summary": "actual isolated fixture work", "artifacts": artifacts,
                                 "knowledge": {"status": "unchanged", "reason": "fixture output only", "artifacts": []}}}
        cli("submit", "--file", json_file("candidate-" + stage + ".json", submission))
    (root / "unrelated-b.txt").write_text("a different task changed this file\n")
    cli("verify", "--task", "candidate-a", "--check", "content", expect_ok=False)
    candidate_error = commands[-1]["stderr"]
    if "CANDIDATE_CHANGED" not in candidate_error:
        raise RuntimeError("candidate coupling did not produce the expected error: " + candidate_error)
    summary["whole_workspace_candidate"] = {"status": "reproduced", "error": candidate_error.strip(), "qualification": "unrelated file change in isolated fixture"}

    # Inject a conflicted pending file intent into this fixture's runtime only.
    conflict = root / "conflict-fixture.txt"
    conflict.write_text("user-edited\n")
    old = hashlib.sha256(b"before\n").hexdigest()
    with sqlite3.connect(Path(summary["data_dir"]) / "runtime.db") as db:
        db.execute("INSERT INTO outbox(path,old_hash,body,state) VALUES(?,?,?,?)", (str(conflict), old, b"pending-projection\n", "pending"))
    cli("task", "list", expect_ok=False)
    outbox_error = commands[-1]["stderr"]
    if "FILE_CONFLICT" not in outbox_error or conflict.read_text() != "user-edited\n":
        raise RuntimeError("pending outbox did not block CLI safely: " + outbox_error)
    conflict.write_text("before\n")
    cli("task", "list")
    if conflict.read_text() != "pending-projection\n":
        raise RuntimeError("fixture conflict did not recover")
    summary["global_outbox"] = {"status": "reproduced_with_fault_injection", "error": outbox_error.strip(),
                                "scope": "a conflicting pending intent blocked opening a separate task-list CLI",
                                "recovery": "only the isolated fixture conflict was resolved"}
    summary["status"] = "passed"
finally:
    (out / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2))
    (out / "commands.json").write_text(json.dumps(commands, ensure_ascii=False, indent=2))
print(json.dumps(summary, ensure_ascii=False, indent=2))

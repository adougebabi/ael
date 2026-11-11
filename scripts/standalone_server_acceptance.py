#!/usr/bin/env python3
"""Exercise a packaged standalone server in an empty, isolated directory."""
import argparse
import json
import os
from pathlib import Path
import platform
import select
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--bundle", required=True)
parser.add_argument("--out", required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
bundle, out = Path(args.bundle).resolve(), Path(args.out).resolve()
if out.exists() and any(out.iterdir()):
    raise SystemExit("refusing a nonempty fixture directory")
out.mkdir(parents=True, exist_ok=True)
(out / "ISOLATED-STANDALONE").write_text("Disposable server fixture; no business checkout or real credentials.\n")
system = {"darwin": "darwin", "linux": "linux"}.get(platform.system().lower())
arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(platform.machine().lower())
if not system or not arch:
    raise SystemExit("unsupported fixture platform")
binary = bundle / f"ael-{system}-{arch}"
data = out / "server-data"
env = dict(os.environ, AEL_DATA_DIR=str(out / "unused-local-runtime"), AEL_INSTALL_PREFIX=str(out / "unused-install"))
password = "fixture-admin-password"
boot = subprocess.run([str(binary), "server", "bootstrap", "--data-dir", str(data), "--username", "owner", "--password-stdin", "--json"],
                      input=password + "\n", cwd=out, env=env, capture_output=True, text=True, timeout=30)
(out / "bootstrap-stdout.log").write_text(boot.stdout)
(out / "bootstrap-stderr.log").write_text(boot.stderr)
if boot.returncode or password in boot.stdout + boot.stderr or str(data) in boot.stdout + boot.stderr:
    raise RuntimeError("isolated bootstrap failed or disclosed sensitive input; see redacted fixture logs")
version = json.loads(subprocess.check_output([str(binary), "version", "--json"], cwd=out, env=env, text=True))

for phase in ("initial", "restart"):
    stderr = (out / f"server-{phase}.log").open("w")
    proc = subprocess.Popen([str(binary), "server", "start", "--data-dir", str(data), "--addr", "127.0.0.1:0", "--json"],
                            cwd=out, env=env, stdout=subprocess.PIPE, stderr=stderr, text=True)
    try:
        ready, _, _ = select.select([proc.stdout], [], [], 30)
        if not ready:
            raise RuntimeError("standalone server did not announce its temporary address")
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError(f"standalone server exited; see server-{phase}.log")
        address = json.loads(line)["url"]
        manifest = {"isolated": True, "out": str(out), "url": address, "phase": phase, "version": version}
        manifest_path = out / f"manifest-{phase}.json"
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2))
        test_env = dict(env, AEL_STANDALONE_FIXTURE=str(manifest_path), AEL_UI_OUT=str(out / f"browser-{phase}"))
        test = subprocess.run([str(repo / "web/node_modules/.bin/playwright"), "test", "--config=playwright.standalone.config.ts"],
                              cwd=repo / "web", env=test_env, timeout=180)
        if test.returncode:
            raise SystemExit(test.returncode)
    finally:
        if proc.poll() is None:
            proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=10)
        stderr.close()

if (out / ".ael").exists():
    raise RuntimeError("standalone server unexpectedly initialized a checkout")
print(json.dumps({"status": "passed", "isolated": True, "version": version, "data": str(data)}, ensure_ascii=False))

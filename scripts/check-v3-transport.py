#!/usr/bin/env python3
"""Maintained transport qualification probes; binds loopback only."""
import argparse
import hashlib
import importlib.util
import json
import os
import signal
import subprocess
import tempfile
import time
from contextlib import contextmanager
from pathlib import Path

spec = importlib.util.spec_from_file_location("v3_checks", __file__.replace("check-v3-transport.py", "check-v3.py"))
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("project", choices=["webrtc", "str0m"])
parser.add_argument("--target", action="store_true")
parser.add_argument("--turn", action="store_true", help="webrtc only; local test-only Pion TURN oracle")
args = parser.parse_args()
crate = checks.REPO / "docs/experiments/v3.0/probes" / args.project
checks.OUT = checks.REPO / "work/v3" / ("transport-" + args.project)
checks.OUT.mkdir(parents=True, exist_ok=True)


@contextmanager
def turn_fixture():
    goroot = checks.PLATFORM / "server/goroot"
    go = goroot / "bin/go"
    binary = checks.OUT / "turn-fixture"
    env = dict(os.environ, GOROOT=str(goroot), CGO_ENABLED="0", GOCACHE=str(checks.OUT / "go-cache"))
    checks.run("turn-build", [go, "build", "-tags", "v3oracle", "-o", binary, "./cmd/v3-turn-fixture"], env, checks.REPO / "server")
    with tempfile.TemporaryDirectory(prefix="nk-v3-turn-") as tmp, (checks.OUT / "turn-runtime.log").open("w") as log:
        ready = Path(tmp) / "ready"
        env["V3_TURN_READY"] = str(ready)
        process = subprocess.Popen([binary], env=env, stdout=log, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 10
            while not ready.is_file():
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("TURN oracle failed to start")
                time.sleep(0.05)
            yield ready.read_text()
        finally:
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()

try:
    if args.turn and args.project != "webrtc":
        raise RuntimeError("str0m requires a separately implemented TURN client")
    checks.run("fmt", [checks.CARGO, "fmt", "--all", "--", "--check"], cwd=crate)
    checks.run("clippy", [checks.CARGO, "clippy", "--locked", "--all-targets", "--", "-D", "warnings"], cwd=crate)
    checks.run("host-exchange", [checks.CARGO, "run", "--locked"], cwd=crate)
    if args.target:
        env = checks.target_env()
        checks.run("target-build", [checks.CARGO, "build", "--locked", "--release", "--target", checks.TARGET], env, crate)
        name = "nk-v3-" + args.project + "-probe"
        binary = crate / f"target/{checks.TARGET}/release" / name
        checks.run("target-exchange", [env["CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER"], binary], env, crate)
        checks.RESULTS["binary"] = {"sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                                     "bytes": binary.stat().st_size}
    if args.turn:
        with turn_fixture() as address:
            checks.run("host-turn-exchange", [checks.CARGO, "run", "--locked", "--", "--turn", address], cwd=crate)
            if args.target:
                checks.run("target-turn-exchange", [env["CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER"], binary, "--turn", address], env, crate)
finally:
    (checks.OUT / "results.json").write_text(json.dumps(checks.RESULTS, indent=2) + "\n")

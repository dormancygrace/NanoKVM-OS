#!/usr/bin/env python3
"""Rebuild test-only Go response fixtures and verify a Rust-issued JWT in Go."""
import importlib.util
import json
import os
import subprocess
import tempfile
from pathlib import Path

spec = importlib.util.spec_from_file_location("v3_checks", str(Path(__file__).with_name("check-v3.py")))
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)
out = checks.REPO / "work/v3/oracle"
out.mkdir(parents=True, exist_ok=True)
fixture = checks.REPO / "docs/experiments/v3.0/auth-go-oracle.json"
goroot = checks.PLATFORM / "server/goroot"
go = goroot / "bin/go"
env = dict(os.environ, GOROOT=str(goroot), CGO_ENABLED="0", GOCACHE=str(out / "go-cache"))
with tempfile.TemporaryDirectory(prefix="inputs-", dir=out) as tmp:
    tmp = Path(tmp)
    db = tmp / "database.json"
    db.write_text(json.dumps(json.loads(fixture.read_text())["database"]))
    db.chmod(0o600)
    token = tmp / "rust-token"
    with token.open("w") as f:
        subprocess.run([checks.CARGO, "run", "--locked", "--example", "mint-oracle-token"],
                       cwd=checks.CRATE, stdout=f, check=True)
    token.chmod(0o600)
    env.update(V3_ORACLE_DATABASE=str(db), V3_ORACLE_RUST_TOKEN_FILE=str(token), V3_ORACLE_OUTPUT=str(fixture))
    subprocess.run([goroot / "bin/gofmt", "-w", "cmd/v3-contract-oracle/main.go", "cmd/v3-turn-fixture/main.go"],
                   cwd=checks.REPO / "server", check=True)
    with (out / "go-oracle.log").open("w") as f:
        subprocess.run([go, "run", "-tags", "teststub,v3oracle", "./cmd/v3-contract-oracle"],
                       cwd=checks.REPO / "server", env=env, stdout=f, stderr=subprocess.STDOUT, check=True)
result = json.loads(fixture.read_text())
print(len(result["cases"]), "Go response cases; Rust JWT accepted by Go middleware:", result["rustTokenVerifiedByGo"])

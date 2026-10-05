#!/usr/bin/env python3
"""Reproducible isolated v3 checks. Never installs or modifies a device."""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import signal
import socket
import ssl
import subprocess
import tempfile
import time

REPO = Path(__file__).resolve().parents[1]
CRATE = REPO / "server-rust"
OUT = REPO / "work/v3/qualification"
CARGO = Path.home() / ".cargo/bin/cargo"
PLATFORM = Path(os.environ.get("NK_V3_PLATFORM", REPO.parent / "work/v2.1-b1-20261004/platform"))
TARGET = "riscv64gc-unknown-linux-musl"
RESULTS = {}


def run(name, args, env=None, cwd=CRATE):
    log = OUT / (name + ".log")
    start = time.monotonic()
    with log.open("w") as output:
        result = subprocess.run([str(a) for a in args], cwd=cwd, env=env,
                                stdout=output, stderr=subprocess.STDOUT)
    RESULTS[name] = {"exit": result.returncode, "seconds": round(time.monotonic() - start, 3),
                     "log_sha256": hashlib.sha256(log.read_bytes()).hexdigest()}
    print(name, result.returncode, flush=True)
    if result.returncode:
        print("\n".join(log.read_text(errors="replace").splitlines()[-40:]))
        raise RuntimeError(name + " failed")


def target_env():
    cross = PLATFORM / "buildroot-output/host/bin/riscv64-buildroot-linux-musl-gcc"
    qemu = PLATFORM / "qemu/qemu-riscv64-static"
    if not cross.is_file() or not qemu.is_file():
        raise RuntimeError("Set NK_V3_PLATFORM to the matched read-only build artifacts")
    return dict(os.environ, CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_LINKER=str(cross),
                CC_riscv64gc_unknown_linux_musl=str(cross),
                CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER=str(qemu),
                # Let each dependency keep its required per-file optimization.
                # In particular AWS-LC's jitterentropy source must use -O0.
                CFLAGS_riscv64gc_unknown_linux_musl="-march=rv64gc -mabi=lp64d -fno-tree-vectorize -fno-tree-slp-vectorize",
                RUSTFLAGS="-C target-cpu=generic-rv64 -C target-feature=+crt-static --remap-path-prefix=" + str(REPO) + "=/nanokvm-v3")


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def tls_check(command):
    with tempfile.TemporaryDirectory(prefix="nk-v3-tls-") as tmp:
        root = Path(tmp)
        etc = root / "etc/kvm"
        etc.mkdir(parents=True)
        web = root / "web"
        web.mkdir()
        (web / "index.html").write_text("isolated TLS check")
        crt, key = etc / "server.crt", etc / "server.key"
        run("tls-cert", ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                         "-days", "1", "-keyout", key, "-out", crt, "-subj", "/CN=localhost",
                         "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1"])
        key.chmod(0o600)
        http_port, tls_port = free_port(), free_port()
        while tls_port == http_port:
            tls_port = free_port()
        (etc / "server.yaml").write_text(
            f"proto: https\nhost: 127.0.0.1\nport:\n  http: {http_port}\n  https: {tls_port}\n"
            "jwt:\n  secretKey: test-only-tls-secret\n  revokeTokensOnLogout: true\nsecurity:\n  trustedProxies: []\n")
        (etc / "pwd").write_text(json.dumps({"username": "owner", "password":
            "U2FsdGVkX18zLUxaLNGy7jL96oMO4tq6wDYwVzUMO3XfTY2Zy/ipO4LDEqtBT+fx"}))
        args = command + ["--root", str(root), "--web", str(web)]
        context = ssl.create_default_context(cafile=str(crt))
        log_path = OUT / "tls-runtime.log"
        with log_path.open("w") as log:
            proc = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 20
                while True:
                    if proc.poll() is not None:
                        raise RuntimeError("TLS process exited before ready")
                    try:
                        conn = http.client.HTTPSConnection("localhost", tls_port, context=context, timeout=2)
                        conn.request("GET", "/")
                        response = conn.getresponse()
                        assert response.status == 200 and response.read() == b"isolated TLS check"
                        conn.close()
                        break
                    except (ConnectionRefusedError, ConnectionResetError, TimeoutError):
                        if time.monotonic() > deadline:
                            raise
                        time.sleep(0.05)
                conn = http.client.HTTPConnection("127.0.0.1", http_port, timeout=2)
                conn.request("GET", "/api/branding?x=1")
                response = conn.getresponse()
                assert response.status == 307
                assert response.getheader("location") == f"https://127.0.0.1:{tls_port}/api/branding?x=1"
                response.read()
                conn.close()
                token = (etc / ".picoclaw_internal_token").read_text().strip()
                conn = http.client.HTTPConnection("127.0.0.1", http_port, timeout=3)
                conn.request("POST", "/api/internal/usb/recover", headers={"X-NanoKVM-Internal-Token": token})
                response = conn.getresponse()
                assert response.status == 200 and response.getheader("location") is None
                assert json.loads(response.read())["msg"] == "failed to recover usb"
                conn.close()
                conn = http.client.HTTPConnection("127.0.0.1", http_port, timeout=3)
                conn.request("POST", "/api/internal/usb/recover", headers={"X-NanoKVM-Internal-Token": "wrong"})
                response = conn.getresponse()
                assert response.status == 307
                response.read()
                conn.close()
                conn = http.client.HTTPConnection("127.0.0.1", http_port, timeout=3)
                conn.request("GET", "/api/auth/account", headers={"X-NanoKVM-Internal-Token": token})
                response = conn.getresponse()
                assert response.status == 307
                response.read()
                conn.close()
                conn = http.client.HTTPSConnection("localhost", tls_port, context=context, timeout=10)
                conn.request("POST", "/api/auth/login", json.dumps({"username": "owner", "password":
                    "U2FsdGVkX18zLUxaLNGy7jL96oMO4tq6wDYwVzUMO3XfTY2Zy/ipO4LDEqtBT+fx"}),
                    {"Content-Type": "application/json"})
                response = conn.getresponse()
                assert response.status == 200 and json.loads(response.read())["code"] == 0
                assert "; Secure" in response.getheader("set-cookie")
                assert response.getheader("cache-control") == "no-store"
                conn.close()
                start = time.monotonic()
                proc.send_signal(signal.SIGTERM)
                assert proc.wait(timeout=8) == 0
                RESULTS["tls-runtime"] = {"verified_certificate": True, "redirect": 307,
                                          "secure_cookie": True, "internal_loopback_http_exception": True,
                                          "unauthorized_internal_redirect": 307, "sigterm_exit": 0,
                                          "shutdown_seconds": round(time.monotonic() - start, 3)}
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait()
        for port in (http_port, tls_port):
            with socket.socket() as sock:
                assert sock.connect_ex(("127.0.0.1", port)) != 0, "listener survived shutdown"
        with socket.socket() as occupied, log_path.open("a") as log:
            occupied.bind(("127.0.0.1", tls_port))
            occupied.listen(1)
            result = subprocess.run(args, stdout=log, stderr=subprocess.STDOUT, timeout=20)
            assert result.returncode != 0, "startup must reject an occupied TLS port"
            with socket.socket() as sock:
                assert sock.connect_ex(("127.0.0.1", http_port)) != 0
        RESULTS["tls-runtime"]["occupied_port_fails_closed"] = True


def licenses():
    result = subprocess.run([str(CARGO), "metadata", "--locked", "--format-version", "1"],
                            cwd=CRATE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
    data = json.loads(result.stdout)
    packages = sorted(({"name": p["name"], "version": p["version"], "license": p["license"],
                        "repository": p["repository"]} for p in data["packages"]),
                      key=lambda p: (p["name"], p["version"]))
    (REPO / "docs/experiments/v3.0/dependency-licenses.json").write_text(json.dumps(packages, indent=2) + "\n")
    RESULTS["licenses"] = {"resolved_packages": len(packages),
                           "missing_spdx": [p["name"] for p in packages if not p["license"]]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", action="store_true")
    parser.add_argument("--target", action="store_true")
    parser.add_argument("--tls", action="store_true")
    args = parser.parse_args()
    OUT.mkdir(parents=True, exist_ok=True)
    try:
        if args.host or not (args.target or args.tls):
            run("fmt", [CARGO, "fmt", "--all", "--", "--check"])
            run("clippy", [CARGO, "clippy", "--locked", "--all-targets", "--", "-D", "warnings"])
            run("host-contracts", [CARGO, "test", "--locked"])
        if args.target:
            env = target_env()
            run("target-build", [CARGO, "build", "--locked", "--release", "--target", TARGET], env)
            run("target-contracts", [CARGO, "test", "--locked", "--target", TARGET], env)
            binary = CRATE / f"target/{TARGET}/release/NanoKVM-Server"
            run("target-version", [env["CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER"], binary, "--version"])
            run("target-file", ["file", binary])
            RESULTS["target-binary"] = {"sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                                         "bytes": binary.stat().st_size}
        if args.tls:
            if args.target:
                command = [target_env()["CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER"],
                           str(CRATE / f"target/{TARGET}/release/NanoKVM-Server")]
            else:
                run("host-build", [CARGO, "build", "--locked"])
                command = [str(CRATE / "target/debug/NanoKVM-Server")]
            tls_check(command)
        licenses()
    finally:
        (OUT / "results.json").write_text(json.dumps(RESULTS, indent=2) + "\n")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Throwaway apk-tools 3.0.8 OpenSSL signing matrix.

All keys and package roots are generated under --work and are disposable.
"""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for b in iter(lambda: f.read(1 << 20), b""):
            h.update(b)
    return h.hexdigest()


def run(args, expect=0):
    r = subprocess.run([str(x) for x in args], text=True, capture_output=True)
    if r.returncode != expect:
        raise RuntimeError("rc=%d expected=%d: %s\n%s" %
                           (r.returncode, expect, " ".join(map(str, args)),
                            r.stderr.strip() or r.stdout.strip()))
    return r


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--apk", required=True)
    p.add_argument("--work", required=True)
    a = p.parse_args()
    if os.geteuid() != 0:
        p.error("run as root for isolated apk database installation")
    apk = Path(a.apk).resolve()
    base = Path(a.work).resolve()
    base.mkdir(parents=True, exist_ok=True)
    out = base / ("run-" + str(os.getpid()))
    shutil.rmtree(out, ignore_errors=True)
    out.mkdir(parents=True)
    http_root = out / "http"
    http_root.mkdir()
    class Quiet(http.server.SimpleHTTPRequestHandler):
        def log_message(_, *args):
            pass
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0),
                                              lambda *x, **y: Quiet(*x, directory=str(http_root), **y))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    port = server.server_address[1]
    records = []
    kinds = {
        "rsa4096": ["genrsa", "-out", "{priv}", "4096"],
        "ecdsa-p256": ["ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", "{priv}"],
        "ecdsa-p384": ["ecparam", "-name", "secp384r1", "-genkey", "-noout", "-out", "{priv}"],
        "ed25519": ["genpkey", "-algorithm", "ED25519", "-out", "{priv}"],
    }
    for index, (kind, keygen) in enumerate(kinds.items()):
        d = out / kind
        keys = d / "keys"
        payload = d / "payload/addons/demo"
        repo = http_root / kind / "riscv64"
        root = d / "root"
        cache = d / "cache"
        for x in [keys, payload, repo, root / "etc/apk/keys", root / "lib/apk/db", cache]:
            x.mkdir(parents=True, exist_ok=True)
        priv, pub = keys / "matrix.pem", keys / "matrix.pub"
        run(["openssl", *[str(x).replace("{priv}", str(priv)) for x in keygen]])
        run(["openssl", "pkey", "-in", priv, "-pubout", "-out", pub])
        (payload / "file").write_text("crypto matrix %s\n" % kind)
        package = repo / "nkos-addon-demo-1.0.0.apk"
        mkpkg = [apk, "--sign-key", priv, "mkpkg", "--compat", "3.0.8",
                 "--output", package, "--files", d / "payload",
                 "--info", "name:nkos-addon-demo", "--info", "version:1.0.0",
                 "--info", "arch:riscv64", "--info", "description:crypto matrix",
                 "--info", "license:MIT"]
        make = subprocess.run([str(x) for x in mkpkg], text=True, capture_output=True)
        if make.returncode != 0:
            records.append({"algorithm": kind, "mkpkg_rc": make.returncode,
                            "mkpkg_stderr": make.stderr.strip(),
                            "private_key": str(priv), "public_key": str(pub)})
            continue
        verify = run([apk, "--keys-dir", keys, "verify", package])
        index = repo / "Packages.adb"
        run([apk, "--keys-dir", keys, "mkndx", "--output", index,
             "--pkgname-spec", "${arch}/${name}-${version}.apk",
             "--sign-key", priv, package])
        # APK accepts a local repository URI.  The signed Packages.adb is
        # therefore verified while resolving and installing the package.
        repos = root / "etc/apk/repositories"
        repos.write_text("v3 http://127.0.0.1:%d/%s\n" % (port, kind))
        shutil.copy2(pub, root / "etc/apk/keys/matrix.pub")
        # The package signature key id is the private key basename without
        # its .pem suffix in the v3 generator; retain both candidate names.
        shutil.copy2(pub, root / "etc/apk/keys/matrix.pem")
        installed = run([apk, "--root", root, "--arch", "riscv64",
                         "--repositories-file", repos, "--keys-dir", root / "etc/apk/keys",
                         "--cache-dir", cache, "add", "--initdb",
                         "--no-scripts", "--no-commit-hooks", "nkos-addon-demo=1.0.0"])
        records.append({"algorithm": kind, "mkpkg_rc": make.returncode,
                        "verify": verify.stdout.strip(), "install_rc": installed.returncode,
                        "package_sha256": digest(package), "index_sha256": digest(index),
                        "private_key": str(priv), "public_key": str(pub)})
    (out / "matrix.json").write_text(json.dumps({"apk": str(apk), "apk_sha256": digest(apk),
                                                  "results": records}, indent=2) + "\n")
    print("MATRIX_OK", out / "matrix.json")
    print("APK", apk, digest(apk))
    for r in records:
        if "package_sha256" not in r:
            print(r["algorithm"], "MKPKG_RC", r["mkpkg_rc"], "ERROR", r["mkpkg_stderr"])
        else:
            print(r["algorithm"], "PACKAGE", r["package_sha256"], "INDEX", r["index_sha256"],
                  "VERIFY", r["verify"] or "OK", "INSTALL", r["install_rc"])


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print("MATRIX_FAIL", type(e).__name__, str(e), file=sys.stderr)
        raise

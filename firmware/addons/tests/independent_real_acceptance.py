#!/usr/bin/env python3
"""Independent real apk-tools acceptance batch for NanoKVM addon updates.

The caller supplies the manager and apk binaries.  Every key, package, HTTPS
certificate, image root, and service is generated below the supplied work
directory; no private fixture key is part of the repository.
"""

import argparse
import fcntl
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import re
import socket
import ssl
import stat
import subprocess
import sys
import threading
import time
import traceback


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


class Batch:
    def __init__(self, apk, manager, work):
        self.apk = Path(apk).resolve()
        self.manager_bin = Path(manager).resolve()
        self.base = Path(work).resolve()
        self.base.mkdir(parents=True, exist_ok=True)
        self.run = self.base / ("run-" + time.strftime("%Y%m%d-%H%M%S") + "-" + str(os.getpid()))
        self.run.mkdir()
        self.keys = self.run / "keys"
        self.keys.mkdir()
        self.http = self.run / "http"
        self.repo = self.http / "repo" / "riscv64"
        self.repo.mkdir(parents=True)
        self.events = []
        self.server = None
        self.chroots = {}

    def command(self, args, *, env=None, expect=0, pass_fds=()):
        args = [str(x) for x in args]
        merged = os.environ.copy()
        if env:
            merged.update({str(k): str(v) for k, v in env.items()})
        result = subprocess.run(args, text=True, capture_output=True, env=merged,
                                pass_fds=pass_fds)
        self.events.append({"cmd": args, "rc": result.returncode,
                            "stdout": result.stdout[-1000:], "stderr": result.stderr[-2000:]})
        if result.returncode != expect:
            raise AssertionError("command rc %d (wanted %d): %s\n%s" %
                                 (result.returncode, expect, " ".join(args),
                                  result.stderr.strip() or result.stdout.strip()))
        return result

    def openssl(self, *args):
        return self.command(["openssl", *args])

    def start_https(self):
        self.openssl("genrsa", "-out", self.keys / "current.pem", "4096")
        self.openssl("rsa", "-in", self.keys / "current.pem", "-pubout",
                     "-out", self.keys / "current.pub")
        self.openssl("genrsa", "-out", self.keys / "target.pem", "4096")
        self.openssl("rsa", "-in", self.keys / "target.pem", "-pubout",
                     "-out", self.keys / "target.pub")
        self.openssl("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                     "-keyout", self.run / "https-key.pem", "-out",
                     self.run / "https-cert.pem", "-subj", "/CN=localhost",
                     "-addext", "subjectAltName=DNS:localhost")
        class Quiet(http.server.SimpleHTTPRequestHandler):
            def log_message(_, *args):
                pass
        os.chdir(self.http)
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
        sock.close()
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", port), Quiet)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(self.run / "https-cert.pem"),
                                str(self.run / "https-key.pem"))
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.repo_url = "https://localhost:%d/repo" % port

    def contract(self, pub, baseabi="1.0.0"):
        return {
            "format": 1, "base_abi": baseabi, "server_api": 1,
            "features": ["nkos-feature-shell=1", "nkos-feature-static-riscv64=1"],
            "trust": {"keys": [{
                "id": "batch-target", "filename": "target.pub",
                "public_key_pem": Path(pub).read_text(),
            }]},
            "repositories": [{"url": self.repo_url, "key_ids": ["batch-target"]}],
        }

    def write_json(self, path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, separators=(",", ":")) + "\n")

    def provider_deps(self, abi="1.0.0"):
        return ["nkos-base-abi=" + abi, "nkos-server-api=1",
                "nkos-feature-shell=1", "nkos-feature-static-riscv64=1"]

    def payload(self, version, *, service=True, escaped=False, addon_id="demo",
                service_version=None):
        payload = self.run / ("payload-" + version.replace(".", "_"))
        shutil.rmtree(payload, ignore_errors=True)
        path = payload / "addons" / ("other" if escaped else addon_id) / "bin"
        path.mkdir(parents=True)
        if escaped:
            (path / "evil").write_text("escaped\n")
        (payload / "addons" / addon_id / "bin").mkdir(parents=True, exist_ok=True)
        descriptor = {
            "schema": 1, "id": addon_id, "package": "nkos-addon-" + addon_id,
            "version": version, "arch": "riscv64", "base_abi": "nkos-base-abi=1.0.0",
            "server_api": "nkos-server-api=1",
            "features": ["nkos-feature-shell=1"], "config": "/etc/kvm/" + addon_id,
            "data": "/data/" + addon_id,
            "preserve": ["/etc/kvm/" + addon_id, "/data/" + addon_id],
            "services": [] if not service else [{
                "name": addon_id, "command": "/opt/nkos/addons/" + addon_id + "/bin/service",
                "default_enabled": False, "restart": "on-update",
            }],
        }
        (payload / "addons" / addon_id / "addon.json").write_text(
            json.dumps(descriptor, separators=(",", ":")) + "\n")
        if service:
            service_version = service_version or version
            script = "#!/bin/sh\necho started-%s >> /data/%s/service-events\n" % (service_version, addon_id)
            script += "trap 'echo stopped-%s >> /data/%s/service-events; exit 0' TERM INT\n" % (service_version, addon_id)
            script += "while :; do /bin/sleep 1; done\n"
            service_path = payload / "addons" / addon_id / "bin" / "service"
            service_path.write_text(script)
            service_path.chmod(0o755)
        return payload

    def package(self, version, deps=None, *, payload=None, name="nkos-addon-demo",
                script=None, trigger=None):
        output = self.repo / (name + "-" + version + ".apk")
        args = [self.apk, "--sign-key", self.keys / "target.pem", "mkpkg",
                "--compat", "3.0.8", "--output", output, "--info", "name:" + name,
                "--info", "version:" + version, "--info", "arch:riscv64",
                "--info", "description:independent acceptance fixture",
                "--info", "license:MIT"]
        if payload:
            args += ["--files", payload]
        if deps:
            args += ["--info", "depends:" + " ".join(deps)]
        if script:
            args += ["--script", "pre-install:" + str(script)]
        if trigger:
            args += ["--trigger", trigger]
        self.command(args)
        return output

    def index(self):
        spec = "${arch}/${name}-${version}.apk"
        self.command([self.apk, "--keys-dir", self.keys, "mkndx", "--output",
                      self.repo / "Packages.adb", "--pkgname-spec", spec,
                      "--sign-key", self.keys / "target.pem",
                      *sorted(self.repo.glob("*.apk"))])

    def make_root(self, name, contract, pub, world=(), *, namespace=False, addon_id="demo"):
        parent = self.run / ("root-" + name)
        root = parent / "nkos"
        shutil.rmtree(parent, ignore_errors=True)
        for sub in ["etc/apk/keys", "usr/share/nkos", "addons",
                    "etc/kvm/" + addon_id, "data/" + addon_id]:
            (root / sub).mkdir(parents=True, exist_ok=True)
        if isinstance(contract, Path):
            contract = json.loads(contract.read_text())
        self.write_json(root / "usr/share/nkos/addons-contract.json", contract)
        shutil.copy2(pub, root / "etc/apk/keys/target.pub")
        (root / "etc/apk/repositories").write_text("v3 " + self.repo_url + "\n")
        cache = self.run / ("cache-" + name)
        cache.mkdir()
        for i, provider in enumerate(self.provider_deps()):
            args = [self.apk, "--root", root, "--arch", "riscv64",
                    "--repositories-file", root / "etc/apk/repositories",
                    "--keys-dir", root / "etc/apk/keys", "--cache-dir", cache,
                    "--no-network", "add"]
            if i == 0:
                args += ["--initdb"]
            args += ["--no-scripts", "--no-commit-hooks", "--virtual", provider]
            self.command(args)
        (root / "etc/apk/world").write_text("\n".join(self.provider_deps() + list(world)) + "\n")
        (root / ("etc/kvm/" + addon_id + "/config")).write_text("config-before\n")
        (root / ("data/" + addon_id + "/state")).write_text("data-before\n")
        return parent, root, namespace

    def manager_env(self, root, tag, *, namespace=False):
        if namespace:
            fs = self.chroot_for(root)
            state = fs / "state"
            run = fs / "run"
            scratch = fs / "scratch"
            apk = "/usr/local/apk"
            cert = "/cert/https-cert.pem"
        else:
            state = self.run / ("state-" + tag)
            run = self.run / ("run-" + tag)
            scratch = self.run / ("scratch-" + tag)
            apk = self.apk
            cert = self.run / "https-cert.pem"
        state.mkdir(exist_ok=True)
        (state / "cache").mkdir(exist_ok=True)
        run.mkdir(exist_ok=True)
        env = {
            "NKOS_APK_ROOT": "/opt/nkos" if namespace else root,
            "NKOS_APK_STATE": "/state" if namespace else state,
            "NKOS_APK_RUN": "/run" if namespace else run,
            "NKOS_APK_BIN": apk,
            "NKOS_APK_SCRATCH": "/scratch" if namespace else scratch,
            "SSL_CERT_FILE": cert,
            "NKOS_ADDON_CONTRACT": "/opt/nkos/usr/share/nkos/addons-contract.json" if namespace
            else root / "usr/share/nkos/addons-contract.json",
        }
        return env, state, run

    def chroot_for(self, root):
        key = str(root)
        if key in self.chroots:
            return self.chroots[key]
        fs = self.run / ("chroot-" + root.parent.name)
        shutil.rmtree(fs, ignore_errors=True)
        (fs / "opt").mkdir(parents=True)
        shutil.copytree(root, fs / "opt/nkos", symlinks=True)
        shutil.copytree(root / "etc/kvm", fs / "etc/kvm", symlinks=True)
        shutil.copytree(root / "data", fs / "data", symlinks=True)
        for sub in ["state", "run", "scratch", "cert", "usr/local", "bin", "dev",
                    "lib/x86_64-linux-gnu", "lib64"]:
            (fs / sub).mkdir(parents=True, exist_ok=True)
        os.mknod(fs / "dev/null", stat.S_IFCHR | 0o666, os.makedev(1, 3))
        shutil.copy2(self.manager_bin, fs / "usr/local/nkos-addons")
        shutil.copy2(self.apk, fs / "usr/local/apk")
        shutil.copy2(self.run / "https-cert.pem", fs / "cert/https-cert.pem")
        for binary, destination in [("/bin/sh", fs / "bin/sh"), ("/bin/sleep", fs / "bin/sleep"),
                                    (str(self.apk), fs / "usr/local/apk")]:
            shutil.copy2(binary, destination)
            output = subprocess.run(["ldd", binary], text=True, capture_output=True, check=True).stdout
            deps = set(re.findall(r"(?:=> )?(/[^ ]+)", output))
            for dep in deps:
                source = Path(dep)
                if source.is_file():
                    target = fs / source.relative_to("/")
                    target.parent.mkdir(parents=True, exist_ok=True)
                    if not target.exists():
                        shutil.copy2(source, target)
        shutil.copy2("/etc/hosts", fs / "etc/hosts")
        self.chroots[key] = fs
        return fs

    @staticmethod
    def apk_state_snapshot(fs):
        root = fs / "opt/nkos"
        snapshot = {}
        for relative in ["etc/apk/world", "etc/apk/db/installed"]:
            path = root / relative
            snapshot[relative] = path.read_bytes() if path.exists() else None
        return snapshot

    def manager(self, root, tag, args, *, namespace=False, expect=0, contract=None):
        env, state, run = self.manager_env(root, tag, namespace=namespace)
        if contract:
            env["NKOS_ADDON_CONTRACT"] = contract
        command = [self.manager_bin, *args]
        if not namespace:
            return self.command(command, env=env, expect=expect)
        fs = self.chroot_for(root)
        # The disposable chroot supplies literal /opt/nkos, /etc/kvm, and
        # /data paths while keeping every write below this run directory.
        return self.command(["chroot", fs, "/usr/local/nkos-addons", *args],
                            env=env, expect=expect)

    @staticmethod
    def wait_text(path, needle, timeout=10):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if path.exists() and needle in path.read_text():
                return True
            time.sleep(0.05)
        return path.exists() and needle in path.read_text()

    @staticmethod
    def wait_pid_gone(pid, timeout=10):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if not os.path.exists("/proc/%d" % pid):
                return True
            time.sleep(0.05)
        return not os.path.exists("/proc/%d" % pid)

    def prepare(self, root, tag, contract, *, expect=0, contains=None):
        stage = self.run / ("stage-" + tag)
        stage.mkdir()
        lock = self.run / ("update-lock-" + tag)
        fd = os.open(lock, os.O_CREAT | os.O_RDWR, 0o600)
        fcntl.flock(fd, fcntl.LOCK_EX)
        env, _, _ = self.manager_env(root, tag)
        env["NKOS_UPDATE_LOCK"] = lock
        def child():
            os.dup2(fd, 3)
        result = subprocess.run([str(self.manager_bin), "prepare-upgrade", str(contract), str(stage)],
                                text=True, capture_output=True, env={**os.environ, **{k: str(v) for k, v in env.items()}},
                                pass_fds=(fd, 3), preexec_fn=child)
        fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)
        self.events.append({"cmd": [str(self.manager_bin), "prepare-upgrade", str(contract), str(stage)],
                            "rc": result.returncode, "stdout": result.stdout[-1000:],
                            "stderr": result.stderr[-2000:]})
        if result.returncode != expect or (contains and contains not in result.stderr):
            raise AssertionError("prepare rc/output mismatch: %s\n%s" %
                                 (result.returncode, result.stderr.strip()))
        return stage, result

    def run_batch(self):
        self.start_https()
        current_contract = self.run / "current-contract.json"
        target_contract = self.run / "target-contract.json"
        self.write_json(current_contract, self.contract(self.keys / "current.pub"))
        self.write_json(target_contract, self.contract(self.keys / "target.pub"))

        deps = self.provider_deps()
        valid10 = self.package("1.0.0", deps, payload=self.payload("1.0.0"))
        valid11 = self.package("1.1.0", deps, payload=self.payload("1.1.0"))
        badservice_payload = self.payload("1.2.0")
        (badservice_payload / "addons/demo/bin/service").unlink()
        badservice = self.package("1.2.0", deps, payload=badservice_payload)
        helper = self.package("1.0.0", deps, name="nkos-addon-helper",
                             payload=self.payload("1.0.0", escaped=True, addon_id="helper"))
        self.package("1.3.0", deps + ["nkos-addon-helper=1.0.0"],
                     payload=self.payload("1.3.0"))
        marker = self.run / "service-script-executed"
        script = self.run / "pre-install.sh"
        script.write_text("#!/bin/sh\necho executed > %s\n" % marker)
        script.chmod(0o755)
        self.package("1.4.0", deps, payload=self.payload("1.4.0"), script=script)
        self.package("1.4.1", deps, payload=self.payload("1.4.1"), trigger="/etc")
        rustdesk_payloads = {}
        for revision in ["1.4.10-r1", "1.4.10-r2", "1.4.10-r9", "1.4.10-r10"]:
            rustdesk_payloads[revision] = self.payload(
                revision, addon_id="rustdesk", service_version="rustdesk-1.4.10")
            self.package(revision, deps, name="nkos-addon-rustdesk",
                         payload=rustdesk_payloads[revision])
        assert ((rustdesk_payloads["1.4.10-r1"] / "addons/rustdesk/bin/service").read_bytes() ==
                (rustdesk_payloads["1.4.10-r2"] / "addons/rustdesk/bin/service").read_bytes())
        for version in ["2.3-r1", "1.4.10_rc1-r0"]:
            self.package(version, deps, name="nkos-addon-version",
                         payload=self.payload(version, addon_id="version", service=False))
        self.index()

        source_parent, source_root, _ = self.make_root("source", current_contract,
                                                        self.keys / "current.pub",
                                                        ["nkos-addon-demo=1.0.0"])
        stage, _ = self.prepare(source_root, "positive", target_contract)
        self.manager(source_root, "verify-positive", ["verify-upgrade", target_contract, stage])
        checkpoint = json.loads((stage / "checkpoint.json").read_text())
        assert checkpoint["packages"] and checkpoint["packages"][0]["name"] == "nkos-addon-demo"
        package_path = stage / checkpoint["packages"][0]["path"]
        original = package_path.read_bytes()
        package_path.write_bytes(original[:-1] + bytes([original[-1] ^ 1]))
        self.manager(source_root, "verify-tamper", ["verify-upgrade", target_contract, stage], expect=5)
        package_path.write_bytes(original)
        self.manager(source_root, "verify-repaired", ["verify-upgrade", target_contract, stage])

        restore_parent, restore_root, _ = self.make_root("restore", target_contract,
                                                         self.keys / "target.pub")
        self.manager(restore_root, "restore", ["restore-upgrade", stage])
        assert (restore_root / "addons/demo/bin/service").is_file()
        assert (restore_root / "etc/kvm/demo/config").read_text() == "config-before\n"
        assert (restore_root / "data/demo/state").read_text() == "data-before\n"
        restore_world = (restore_root / "etc/apk/world").read_text()
        assert "nkos-addon-demo=1.0.0" in restore_world
        assert not (self.run / "state-restore/enabled/demo.demo").exists()
        assert not (self.run / "run-restore/demo.demo.pid").exists()

        mismatch = self.run / "mismatch-contract.json"
        self.write_json(mismatch, self.contract(self.keys / "target.pub", "9.9.9"))
        self.manager(source_root, "verify-mismatch", ["verify-upgrade", mismatch, stage], expect=5)

        wrong_parent, wrong_root, _ = self.make_root("wrong-trust", target_contract,
                                                     self.keys / "current.pub")
        self.manager(wrong_root, "restore-wrong-trust", ["restore-upgrade", stage], expect=3)
        assert not (wrong_root / "addons/demo").exists()

        reserved = self.package("2.0.0", ["nkos-base-abi=2.0.0"],
                                payload=self.payload("2.0.0"), name="nkos-addon-demo")
        # A package repository cannot replace an image-owned virtual provider.
        (source_root / "etc/apk/world").write_text("\n".join(self.provider_deps() +
            ["nkos-addon-demo=2.0.0"]) + "\n")
        self.index()
        self.prepare(source_root, "reserved", target_contract, expect=5,
                     contains="cannot install installed addons")

        (source_root / "etc/apk/world").write_text("\n".join(self.provider_deps() +
            ["nkos-addon-demo=1.4.0"]) + "\n")
        self.prepare(source_root, "scripted", target_contract, expect=5,
                     contains="scripts, or triggers")
        assert not marker.exists(), "prepare executed package script"

        (source_root / "etc/apk/world").write_text("\n".join(self.provider_deps() +
            ["nkos-addon-demo=1.3.0"]) + "\n")
        self.prepare(source_root, "dependency-path", target_contract, expect=5,
                     contains="owns path outside addons/helper")

        # Normal CLI transactions must apply the same package policy before
        # handing files to apk.  These are deliberately signed packages;
        # successful installation here is a product-policy failure.
        normal_script_accepted = False
        script_root_parent, script_source_root, _ = self.make_root(
            "normal-script", target_contract, self.keys / "target.pub")
        script_fs = self.chroot_for(script_source_root)
        script_before = self.apk_state_snapshot(script_fs)
        try:
            self.manager(script_source_root, "normal-script",
                         ["add", "demo", "nkos-addon-demo=1.4.0"], namespace=True)
            normal_script_accepted = (script_fs / "opt/nkos/addons/demo/addon.json").exists()
        except AssertionError:
            normal_script_accepted = False
        normal_script_mutated = (self.apk_state_snapshot(script_fs) != script_before or
                                 (script_fs / "opt/nkos/addons/demo").exists())

        normal_trigger_accepted = False
        trigger_root_parent, trigger_source_root, _ = self.make_root(
            "normal-trigger", target_contract, self.keys / "target.pub")
        trigger_fs = self.chroot_for(trigger_source_root)
        trigger_before = self.apk_state_snapshot(trigger_fs)
        try:
            self.manager(trigger_source_root, "normal-trigger",
                         ["add", "demo", "nkos-addon-demo=1.4.1"], namespace=True)
            normal_trigger_accepted = (trigger_fs / "opt/nkos/addons/demo/addon.json").exists()
        except AssertionError:
            normal_trigger_accepted = False
        normal_trigger_mutated = (self.apk_state_snapshot(trigger_fs) != trigger_before or
                                  (trigger_fs / "opt/nkos/addons/demo").exists())

        normal_escape_accepted = False
        escape_root_parent, escape_source_root, _ = self.make_root(
            "normal-escape", target_contract, self.keys / "target.pub")
        escape_fs = self.chroot_for(escape_source_root)
        escape_before = self.apk_state_snapshot(escape_fs)
        try:
            self.manager(escape_source_root, "normal-escape",
                         ["add", "demo", "nkos-addon-demo=1.3.0"], namespace=True)
            normal_escape_accepted = (escape_fs / "opt/nkos/addons/other/bin/evil").exists()
        except AssertionError:
            normal_escape_accepted = False
        normal_escape_mutated = (self.apk_state_snapshot(escape_fs) != escape_before or
                                 (escape_fs / "opt/nkos/addons").exists() and
                                 any((escape_fs / "opt/nkos/addons").iterdir()))

        lifecycle_parent, lifecycle_source_root, _ = self.make_root("lifecycle", target_contract,
                                                                     self.keys / "target.pub")
        lifecycle_fs = self.chroot_for(lifecycle_source_root)
        lifecycle_root = lifecycle_fs / "opt/nkos"
        # All CLI actions use the real manager and real apk-tools in a private
        # chroot, so /opt/nkos, /etc/kvm, and /data are literal paths.
        self.manager(lifecycle_source_root, "cli-add", ["add", "demo", "nkos-addon-demo=1.0.0"], namespace=True)
        world = (lifecycle_root / "etc/apk/world").read_text()
        assert "nkos-addon-demo=1.0.0" in world
        self.manager(lifecycle_source_root, "cli-enable", ["enable", "demo"], namespace=True)
        pid_path = lifecycle_fs / "run/demo.demo.pid"
        assert pid_path.exists()
        pid1 = int(pid_path.read_text())
        assert os.path.exists("/proc/%d" % pid1)
        assert self.wait_text(lifecycle_fs / "data/demo/service-events", "started-1.0.0")
        (lifecycle_fs / "etc/kvm/demo/config").write_text("config-kept\n")
        (lifecycle_fs / "data/demo/state").write_text("data-kept\n")
        self.manager(lifecycle_source_root, "cli-upgrade", ["upgrade", "demo", "nkos-addon-demo=1.1.0"], namespace=True)
        assert (lifecycle_fs / "etc/kvm/demo/config").read_text() == "config-kept\n"
        assert (lifecycle_fs / "data/demo/state").read_text() == "data-kept\n"
        assert "nkos-addon-demo=1.1.0" in (lifecycle_root / "etc/apk/world").read_text()
        pid2 = int(pid_path.read_text())
        assert pid2 != pid1 and os.path.exists("/proc/%d" % pid2)
        assert self.wait_text(lifecycle_fs / "data/demo/service-events", "started-1.1.0")
        unsupported = self.manager(lifecycle_source_root, "cli-failed-version",
                                    ["upgrade", "demo", "nkos-addon-demo=9.9.9"],
                                    namespace=True, expect=5)
        assert "ERROR" in unsupported.stderr or "unable" in unsupported.stderr
        assert os.path.exists("/proc/%d" % int(pid_path.read_text()))
        # This package has a valid APK signature but an incompatible service
        # descriptor.  The action must fail visibly; record whether the old
        # payload remains available after the failed native mutation.
        failed = self.manager(lifecycle_source_root, "cli-failed-descriptor",
                              ["upgrade", "demo", "nkos-addon-demo=1.2.0"],
                              namespace=True, expect=5)
        assert failed.stderr.strip() and "service" in failed.stderr
        bad_payload_survives = (lifecycle_root / "addons/demo/addon.json").exists()
        bad_service_survives = (lifecycle_root / "addons/demo/bin/service").is_file()
        assert bad_payload_survives and bad_service_survives
        # Simulate an interrupted native mutation that leaves the descriptor
        # and enablement record but loses the executable.  Uninstall must
        # still stop the running service and clean its markers.
        (lifecycle_root / "addons/demo/bin/service").unlink()
        self.manager(lifecycle_source_root, "cli-del", ["del", "demo"], namespace=True)
        assert not (lifecycle_root / "addons/demo").exists()
        assert (lifecycle_fs / "etc/kvm/demo/config").read_text() == "config-kept\n"
        assert (lifecycle_fs / "data/demo/state").read_text() == "data-kept\n"
        assert self.wait_pid_gone(pid2)
        enabled_marker = lifecycle_fs / "state/enabled/demo.demo"
        stale_enable_marker = enabled_marker.exists()

        for version in ["2.3-r1", "1.4.10_rc1-r0"]:
            version_parent, version_source_root, _ = self.make_root(
                "version-" + version.replace(".", "_"), target_contract,
                self.keys / "target.pub", addon_id="version")
            version_fs = self.chroot_for(version_source_root)
            version_root = version_fs / "opt/nkos"
            self.manager(version_source_root, "version-add-" + version,
                         ["add", "version", "nkos-addon-version=" + version], namespace=True)
            assert "nkos-addon-version=" + version in (version_root / "etc/apk/world").read_text()
            assert (version_root / "addons/version/addon.json").is_file()
            self.manager(version_source_root, "version-del-" + version,
                         ["del", "version"], namespace=True)
        invalid_version = self.command(
            [self.apk, "version", "--check", "1.2.3-beta.1"], expect=1)

        revision_parent, revision_source_root, _ = self.make_root(
            "rustdesk-revision", target_contract, self.keys / "target.pub",
            addon_id="rustdesk")
        revision_fs = self.chroot_for(revision_source_root)
        revision_root = revision_fs / "opt/nkos"
        self.manager(revision_source_root, "rustdesk-add-r1",
                     ["add", "rustdesk", "nkos-addon-rustdesk=1.4.10-r1"], namespace=True)
        assert "nkos-addon-rustdesk=1.4.10-r1" in (revision_root / "etc/apk/world").read_text()
        self.manager(revision_source_root, "rustdesk-enable", ["enable", "rustdesk"], namespace=True)
        revision_pid_path = revision_fs / "run/rustdesk.rustdesk.pid"
        assert revision_pid_path.exists()
        revision_pid1 = int(revision_pid_path.read_text())
        assert self.wait_text(revision_fs / "data/rustdesk/service-events",
                              "started-rustdesk-1.4.10")
        (revision_fs / "etc/kvm/rustdesk/config").write_text("revision-config-kept\n")
        (revision_fs / "data/rustdesk/state").write_text("revision-data-kept\n")
        self.manager(revision_source_root, "rustdesk-upgrade-r2",
                     ["upgrade", "rustdesk", "nkos-addon-rustdesk=1.4.10-r2"], namespace=True)
        assert "nkos-addon-rustdesk=1.4.10-r2" in (revision_root / "etc/apk/world").read_text()
        assert (revision_fs / "etc/kvm/rustdesk/config").read_text() == "revision-config-kept\n"
        assert (revision_fs / "data/rustdesk/state").read_text() == "revision-data-kept\n"
        revision_pid2 = int(revision_pid_path.read_text())
        assert revision_pid2 != revision_pid1 and os.path.exists("/proc/%d" % revision_pid2)
        assert (revision_fs / "data/rustdesk/service-events").read_text().count(
            "started-rustdesk-1.4.10") >= 2
        self.manager(revision_source_root, "rustdesk-del", ["del", "rustdesk"], namespace=True)
        assert self.wait_pid_gone(revision_pid2)

        ordering_parent, ordering_source_root, _ = self.make_root(
            "rustdesk-ordering", target_contract, self.keys / "target.pub",
            addon_id="rustdesk")
        ordering_fs = self.chroot_for(ordering_source_root)
        ordering_root = ordering_fs / "opt/nkos"
        self.manager(ordering_source_root, "rustdesk-add-r9",
                     ["add", "rustdesk", "nkos-addon-rustdesk=1.4.10-r9"], namespace=True)
        assert "nkos-addon-rustdesk=1.4.10-r9" in (ordering_root / "etc/apk/world").read_text()
        self.manager(ordering_source_root, "rustdesk-upgrade-r10",
                     ["upgrade", "rustdesk", "nkos-addon-rustdesk=1.4.10-r10"], namespace=True)
        ordering_world = (ordering_root / "etc/apk/world").read_text()
        assert "nkos-addon-rustdesk=1.4.10-r10" in ordering_world
        assert "nkos-addon-rustdesk=1.4.10-r9" not in ordering_world
        self.manager(ordering_source_root, "rustdesk-ordering-del", ["del", "rustdesk"], namespace=True)

        self.events.append({"lifecycle_failed_descriptor_old_payload": bad_payload_survives,
                            "lifecycle_failed_descriptor_old_service": bad_service_survives,
                            "lifecycle_stale_enable_marker": stale_enable_marker,
                            "normal_script_accepted": normal_script_accepted,
                            "normal_trigger_accepted": normal_trigger_accepted,
                            "normal_escape_accepted": normal_escape_accepted,
                            "normal_script_mutated": normal_script_mutated,
                            "normal_trigger_mutated": normal_trigger_mutated,
                            "normal_escape_mutated": normal_escape_mutated,
                            "invalid_apk_version_rejected": invalid_version.returncode == 1})
        (self.run / "events.json").write_text(json.dumps(self.events, indent=2) + "\n")
        print("BATCH_OK")
        print("ARTIFACT_ROOT", self.run)
        print("APK", self.apk, sha256(self.apk))
        print("MANAGER", self.manager_bin, sha256(self.manager_bin))
        print("TARGET_CONTRACT", target_contract, sha256(target_contract))
        self.command(["openssl", "pkey", "-in", self.keys / "target.pub", "-pubin",
                      "-outform", "DER", "-out", self.run / "target-public.der"])
        print("TARGET_RSA_DER", self.run / "target-public.der", sha256(self.run / "target-public.der"))
        print("LIFECYCLE_FAILED_DESCRIPTOR_OLD_PAYLOAD", bad_payload_survives)
        print("LIFECYCLE_FAILED_DESCRIPTOR_OLD_SERVICE", bad_service_survives)
        print("LIFECYCLE_STALE_ENABLE_MARKER", stale_enable_marker)
        print("NORMAL_SCRIPT_ACCEPTED", normal_script_accepted)
        print("NORMAL_TRIGGER_ACCEPTED", normal_trigger_accepted)
        print("NORMAL_ESCAPE_ACCEPTED", normal_escape_accepted)
        print("NORMAL_SCRIPT_MUTATED", normal_script_mutated)
        print("NORMAL_TRIGGER_MUTATED", normal_trigger_mutated)
        print("NORMAL_ESCAPE_MUTATED", normal_escape_mutated)
        print("INVALID_APK_VERSION_REJECTED", invalid_version.returncode == 1)
        if (stale_enable_marker or normal_script_accepted or normal_trigger_accepted or
                normal_escape_accepted or normal_script_mutated or normal_trigger_mutated or
                normal_escape_mutated):
            print("PRODUCT_DEFECT_OBSERVED lifecycle or CLI package policy", file=sys.stderr)
        return 1 if (stale_enable_marker or normal_script_accepted or normal_trigger_accepted or
                     normal_escape_accepted or normal_script_mutated or normal_trigger_mutated or
                     normal_escape_mutated) else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--apk", required=True)
    parser.add_argument("--manager", required=True)
    parser.add_argument("--work", required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error("run as root so real APK database and mount namespace are testable")
    try:
        return Batch(args.apk, args.manager, args.work).run_batch()
    except (AssertionError, OSError, subprocess.SubprocessError) as exc:
        print("BATCH_FAIL", type(exc).__name__, repr(str(exc)), file=sys.stderr)
        traceback.print_exc(file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

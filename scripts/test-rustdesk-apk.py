#!/usr/bin/env python3
"""Test unsigned APK transactions in a new disposable chroot with mock OpenRC.
No real service is started. Root is needed only for apk's chroot execution.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--apk", type=Path, required=True, help="host apk-tools 3")
p.add_argument("--package", type=Path, required=True)
a = p.parse_args()
if os.geteuid() != 0:
    p.error("run as root for isolated chroot tests")
repo = Path(__file__).resolve().parents[1]
work = repo / "work"
work.mkdir(exist_ok=True)
test = Path(tempfile.mkdtemp(prefix="rustdesk-apk-test-", dir=work))
root = test / "root"
root.mkdir()
apk = str(a.apk.absolute())
package = a.package.absolute()
def run(argv, ok=True):
    result = subprocess.run(argv, text=True, capture_output=True)
    if ok and result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result
base = [apk, "--root", str(root), "--arch", "riscv64", "--allow-untrusted", "--no-network"]
missing = run(base + ["--initdb", "--no-scripts", "add", str(package)], ok=False)
assert missing.returncode and "nanokvm-rustdesk-bridge" in missing.stderr, missing
for name in ("bin", "sbin", "state"):
    (root / name).mkdir(parents=True, exist_ok=True)
busybox = shutil.which("busybox")
assert busybox, "install a host static busybox for this test"
run(["file", busybox])
assert "statically linked" in run(["file", busybox]).stdout
shutil.copyfile(busybox, root / "bin/busybox")
(root / "bin/busybox").chmod(0o755)
for name in ("sh", "mkdir", "touch", "rm"):
    (root / "bin" / name).symlink_to("busybox")
for name, body in {
    "rc-service": '''#!/bin/sh
printf '%s\\n' "$*" >> /state/calls
case "$2" in
status) [ -f /state/running ];;
start) touch /state/running;;
stop) rm -f /state/running;;
*) exit 1;;
esac
''',
    "rc-update": '''#!/bin/sh
printf 'rc-update %s\\n' "$*" >> /state/calls
exit 0
'''
}.items():
    path = root / "sbin" / name
    path.write_text(body)
    path.chmod(0o755)
empty = test / "empty"
empty.mkdir()
provider = test / "provider.apk"
run([apk, "mkpkg", "--files", str(empty), "--output", str(provider),
    "--info", "name:nanokvm-test-runtime", "--info", "version:1-r0",
    "--info", "arch:riscv64", "--info", "provides:nanokvm-rustdesk-bridge=1 openrc=1"])
run(base + ["--initdb", "add", str(provider), str(package)])
assert (root / "usr/bin/nanokvm-rustdesk").is_file()
assert (root / "usr/share/nanokvm-rustdesk/source.tar.gz").is_file()
assert not (root / "state/calls").exists(), "install must leave daemon stopped"
config = root / "etc/nanokvm-rustdesk/config.json"
config.parent.mkdir(mode=0o700)
config.write_text('{"password":"synthetic-test-state"}\n')
config.chmod(0o600)
softlevel = root / "run/openrc/softlevel"
softlevel.parent.mkdir(parents=True)
softlevel.write_text("default")
(root / "state/running").touch()
original = subprocess.check_output([apk, "adbdump", str(package)], text=True)
assert "version: 0.1.0-r0" in original
# The upgrade uses the identical payload and real lifecycle hooks, with a higher
# test-only version. These mock chroot commands never start the actual daemon.
upgraded = test / "upgrade.apk"
payload = repo / "work/rustdesk-dist/payload"
command = [apk, "mkpkg", "--files", str(payload), "--output", str(upgraded)]
for field in ["name:nanokvm-rustdesk", "version:0.1.1-r0", "arch:riscv64",
              "depends:nanokvm-rustdesk-bridge=1 openrc"]:
    command += ["--info", field]
for action in ["pre-upgrade", "post-upgrade", "pre-deinstall"]:
    script = repo / ("firmware/alpine/packages/nanokvm-rustdesk/nanokvm-rustdesk." + action)
    command += ["--script", action + ":" + str(script)]
run(command)
run(base + ["add", "--upgrade", str(upgraded)])
calls = (root / "state/calls").read_text()
assert "nanokvm-rustdesk stop" in calls and "nanokvm-rustdesk start" in calls, calls
assert (root / "state/running").exists()
assert not (root / "run/nanokvm-rustdesk/restart-after-upgrade").exists()
run(base + ["del", "nanokvm-rustdesk"])
assert not (root / "usr/bin/nanokvm-rustdesk").exists()
assert not (root / "state/running").exists()
assert config.read_text() == '{"password":"synthetic-test-state"}\n'
assert "rc-update del nanokvm-rustdesk default" in (root / "state/calls").read_text()
print("PASS: dependency guard, stopped install, running upgrade, removal and preserved synthetic state")
print("Test root:", root)

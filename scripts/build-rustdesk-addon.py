#!/usr/bin/env python3
"""Build an optional RustDesk APK and its complete corresponding source.
No device access, package installation, signing, publication or image changes.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument("--linker",type=Path,required=True,help="riscv64 musl GCC")
parser.add_argument("--apk",type=Path,required=True,help="host apk-tools 3 with mkpkg")
parser.add_argument("--output",type=Path,required=True)
args=parser.parse_args()
root=Path(__file__).resolve().parents[1]
source=root/"addons/rustdesk"
build=root/"work/rustdesk-dist"
build.mkdir(parents=True, exist_ok=True)
output=args.output.resolve()
output.mkdir(parents=True,exist_ok=True)
stage=build/"nanokvm-rustdesk-0.1.0"
stage.mkdir(exist_ok=True)
for name in ["src","Cargo.toml","Cargo.lock","LICENSE","NOTICE","README.md"]:
    p=source/name
    if p.is_dir():shutil.copytree(p,stage/name,dirs_exist_ok=True)
    elif p.exists():shutil.copyfile(p,stage/name)
vendor=subprocess.check_output(["cargo","vendor","--locked","--versioned-dirs",str(stage/"vendor")],cwd=source,text=True)
# Cargo vendor prints an absolute path; make the archive relocatable.
(stage/".cargo").mkdir(exist_ok=True)
(stage/".cargo/config.toml").write_text(vendor.replace(str(stage/"vendor"),"vendor"))
env=dict(os.environ,CARGO_TARGET_DIR=str(root/"work/rust-target"),CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_LINKER=str(args.linker.absolute()),RUSTFLAGS="-C target-feature=+crt-static")
subprocess.run(["cargo","build","--locked","--release","--target","riscv64gc-unknown-linux-musl"],cwd=source,env=env,check=True)
binary=root/"work/rust-target/riscv64gc-unknown-linux-musl/release/nanokvm-rustdesk"
archive=output/"nanokvm-rustdesk-0.1.0-source.tar.gz"
with tarfile.open(archive,"w:gz") as tar:tar.add(stage,arcname=stage.name)
payload=build/"payload"
def install(src,dest,mode=0o644):
    path=payload/dest.lstrip("/")
    path.parent.mkdir(parents=True,exist_ok=True)
    shutil.copyfile(src,path)
    path.chmod(mode)
pkg=root/"firmware/alpine/packages/nanokvm-rustdesk"
install(binary,"usr/bin/nanokvm-rustdesk",0o755)
install(pkg/"nanokvm-rustdesk.initd","etc/init.d/nanokvm-rustdesk",0o755)
install(source/"LICENSE","usr/share/licenses/nanokvm-rustdesk/LICENSE")
install(source/"NOTICE","usr/share/licenses/nanokvm-rustdesk/NOTICE")
install(archive,"usr/share/nanokvm-rustdesk/source.tar.gz")
apkfile=output/"nanokvm-rustdesk-0.1.0-r0.apk"
command=[str(args.apk.resolve()),"mkpkg","--files",str(payload),"--output",str(apkfile)]
for value in ["name:nanokvm-rustdesk","version:0.1.0-r0","arch:riscv64","license:AGPL-3.0-only","description:RustDesk HDMI and USB HID endpoint for NanoKVM OS","depends:nanokvm-rustdesk-bridge=1 openrc","url:https://github.com/onekvm/onekvm-extension-rustdesk"]:
    command+=["--info",value]
for action in ["pre-upgrade","post-upgrade","pre-deinstall"]:
    command+=["--script",action+":"+str(pkg/("nanokvm-rustdesk."+action))]
# mkpkg preserves filesystem ownership. Never ship a developer's UID.
# Run under fakeroot, or assemble the already-built payload as root.
for path in [payload, *payload.rglob("*")]:
    stat = path.stat()
    if stat.st_uid != 0 or stat.st_gid != 0:
        try:
            os.chown(path, 0, 0)
        except PermissionError:
            raise SystemExit("Payload is ready. Run this script with fakeroot for root:root APK ownership.")
subprocess.run(command,check=True)
print(apkfile)
print(archive)

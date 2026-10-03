#!/usr/bin/env python3
"""Build an optional RustDesk APK and its complete corresponding source.
No device access, package installation, signing, publication or image changes.
"""
import argparse
import hashlib
import json
import re
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tomllib

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument("--linker",type=Path,required=True,help="riscv64 musl GCC")
parser.add_argument("--apk",type=Path,required=True,help="host apk-tools 3 with mkpkg")
parser.add_argument("--output",type=Path,required=True)
parser.add_argument("--source-url",required=True,help="immutable public URL for this package revision source archive")
args=parser.parse_args()
root=Path(__file__).resolve().parents[1]
source=root/"addons/rustdesk"
version=tomllib.loads((source/"Cargo.toml").read_text())["package"]["version"]
pkg=root/"firmware/alpine/packages/nanokvm-rustdesk"
recipe=(pkg/"APKBUILD").read_text()
pkgver=re.search(r"^pkgver=(\S+)$",recipe,re.M).group(1)
revision=re.search(r"^pkgrel=([0-9]+)$",recipe,re.M).group(1)
if pkgver != version:
    raise SystemExit("APKBUILD pkgver must match Cargo.toml")
package_version=f"{version}-r{revision}"
if not args.source_url.startswith("https://"):
    raise SystemExit("--source-url must be an HTTPS URL")
build=root/"work/rustdesk-dist"
build.mkdir(parents=True, exist_ok=True)
output=args.output.resolve()
output.mkdir(parents=True,exist_ok=True)
stage=build/f"nanokvm-rustdesk-{version}"
stage.mkdir(exist_ok=True)
for name in ["src","tests","Cargo.toml","Cargo.lock","LICENSE","NOTICE","README.md","upstream.json"]:
    p=source/name
    if p.is_dir():shutil.copytree(p,stage/name,dirs_exist_ok=True)
    elif p.exists():shutil.copyfile(p,stage/name)
with (build/"cargo-vendor.log").open("w") as vendor_log:
    vendor=subprocess.check_output(["cargo","vendor","--locked","--versioned-dirs",str(stage/"vendor")],cwd=source,text=True,stderr=vendor_log)
# Cargo vendor prints an absolute path; make the archive relocatable.
(stage/".cargo").mkdir(exist_ok=True)
(stage/".cargo/config.toml").write_text(vendor.replace(str(stage/"vendor"),"vendor"))
env=dict(os.environ,CARGO_TARGET_DIR=str(root/"work/rust-target"),CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_LINKER=str(args.linker.absolute()),RUSTFLAGS="-C target-feature=+crt-static")
subprocess.run(["cargo","build","--locked","--release","--target","riscv64gc-unknown-linux-musl"],cwd=source,env=env,check=True)
binary=root/"work/rust-target/riscv64gc-unknown-linux-musl/release/nanokvm-rustdesk"
# Recipes and build inputs travel in the published source, not in the APK.
shutil.copytree(pkg,stage/"packaging",dirs_exist_ok=True)
shutil.copyfile(Path(__file__),stage/"packaging/build-rustdesk-addon.py")
(stage/"packaging/BUILD.md").write_text("""# Packaging this source

The source root builds and tests with cargo --locked --offline using the
vendored crates. See the source README for the exact static RISC-V target.
For a native riscv64 Alpine build, copy APKBUILD and nanokvm-rustdesk.* from
this directory into an abuild recipe directory, run abuild checksum, then
build against the published source archive. The standalone builder here is
preserved as a record of the NanoKVM firmware repository's packaging script;
it expects that repository's addons/rustdesk and firmware/alpine layout.
The APK contains binary, lifecycle hooks, license, upstream metadata and a
source URL/digest record. It does not contain this archive or vendored crates.
""")
# Export the new Go transport as a independently buildable component as well.
# The integrated application is packaged separately; this records its exact IPC hook.
go_transport=stage/"app-webrtc"
go_transport.mkdir(exist_ok=True)
for name in ["webrtc.go","webrtc_test.go"]:
    shutil.copyfile(root/"server/service/rustdesk"/name,go_transport/name)
shutil.copyfile(root/"server/service/rustdesk/bridge.go",go_transport/"bridge.go.integration")
(go_transport/"go.mod").write_text((root/"server/go.mod").read_text().replace("module NanoKVM-Server","module nanokvm-rustdesk-webrtc-source",1))
shutil.copyfile(root/"server/go.sum",go_transport/"go.sum")
# Preserve all local Pion replacements: these are production C906 changes.
for line in (root/"server/go.mod").read_text().splitlines():
    if "=> ./" in line:
        component=Path(line.split("=>",1)[1].strip())
        shutil.copytree(root/"server"/component,go_transport/component,dirs_exist_ok=True)
subprocess.run(["go","mod","tidy"],cwd=go_transport,check=True)
subprocess.run(["go","mod","vendor"],cwd=go_transport,check=True)
(go_transport/"README.md").write_text("""# Pion IPC component

The application runs webrtc.go in service/rustdesk. The standalone source here
includes locked, vendored dependencies and real data-channel tests:
go test -mod=vendor -race ./...
bridge.go.integration records the application start/stop and root-only socket
integration; its common/authn/media dependencies belong to the NanoKVM app source.
The Unix listener must use SO_PEERCRED to allow uid 0 only, directory 0700 and
socket 0600. Offer/answer/candidate/attach JSON has a u32 little-endian length
(maximum 64 KiB). A one-time attach token switches the socket after ready to
RustDesk's 1-4 byte little-endian length framing. Data-channel fragment byte 1
means continuation, byte 0 means final. No source is installed on the device.
""")

(stage/"docs").mkdir(exist_ok=True)
for name in ["rustdesk-handoff.md","rustdesk-device-test.md","rustdesk-1.5-review.md","rustdesk-1.5-migration.md"]:
    shutil.copyfile(root/"docs"/name,stage/"docs"/name)
shutil.copyfile(root/"server/service/rustdesk/webrtc_socket_test.go",go_transport/"webrtc_socket_test.go.integration")
archive=output/f"nanokvm-rustdesk-{version}-source.tar.gz"
with tarfile.open(archive,"w:gz") as tar:tar.add(stage,arcname=stage.name)
payload=build/f"payload-{package_version}"
def install(src,dest,mode=0o644):
    path=payload/dest.lstrip("/")
    path.parent.mkdir(parents=True,exist_ok=True)
    shutil.copyfile(src,path)
    path.chmod(mode)
install(binary,"usr/bin/nanokvm-rustdesk",0o755)
install(pkg/"nanokvm-rustdesk.initd","etc/init.d/nanokvm-rustdesk",0o755)
install(source/"LICENSE","usr/share/licenses/nanokvm-rustdesk/LICENSE")
install(source/"NOTICE","usr/share/licenses/nanokvm-rustdesk/NOTICE")
source_record=build/f"source-{package_version}.json"
source_record.write_text(json.dumps({"url":args.source_url,"sha256":hashlib.sha256(archive.read_bytes()).hexdigest(),"package_version":package_version},indent=2)+"\n")
install(source_record,"usr/share/nanokvm-rustdesk/source.json")
install(source/"upstream.json","usr/share/nanokvm-rustdesk/upstream.json")
apkfile=output/f"nanokvm-rustdesk-{package_version}.apk"
command=[str(args.apk.resolve()),"mkpkg","--files",str(payload),"--output",str(apkfile)]
for value in ["name:nanokvm-rustdesk",f"version:{package_version}","arch:riscv64","license:AGPL-3.0-only","description:RustDesk HDMI and USB HID endpoint for NanoKVM OS","depends:nanokvm-rustdesk-bridge=1 nanokvm-rustdesk-webrtc=1 openrc","url:https://github.com/onekvm/onekvm-extension-rustdesk"]:
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

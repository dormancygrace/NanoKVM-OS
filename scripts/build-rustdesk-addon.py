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
import tempfile
import atexit
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
# A fresh root isolates both source and payload from every earlier build.
# TemporaryDirectory removes only this newly-created host directory at exit.
staging=tempfile.TemporaryDirectory(prefix=f"build-{package_version}-",dir=build)
atexit.register(staging.cleanup)
staging_root=Path(staging.name)
stage=staging_root/f"nanokvm-rustdesk-{version}"
stage.mkdir()
for expected in [f"nanokvm-rustdesk-{version}-source.tar.gz",f"nanokvm-rustdesk-{package_version}.apk","APKBUILD"]:
    if (output/expected).exists():
        raise SystemExit(f"refusing to overwrite artifact: {output/expected}")
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
shutil.copytree(pkg,stage/"packaging")
# A corresponding-source tar cannot embed its own digest. Preserve the build
# recipe as a template; the production recipe beside the archive is checksummed.
embedded_recipe=stage/"packaging/APKBUILD"
embedded_recipe.rename(stage/"packaging/APKBUILD.in")
shutil.copyfile(Path(__file__),stage/"packaging/build-rustdesk-addon.py")
(stage/"packaging/BUILD.md").write_text("""# Packaging this source

The source root builds and tests with cargo --locked --offline using the
vendored crates. See the source README for the exact static RISC-V target.
For a native riscv64 Alpine build, use the checksummed APKBUILD distributed
beside this immutable source archive, and copy nanokvm-rustdesk.* from this
directory into its recipe directory. APKBUILD.in here records the recipe shape,
not a production download checksum (an archive cannot contain its own digest). The standalone builder here is
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
# Exact shared-encoder implementation used by the automatic media subscription.
stream_integration=stage/"app-stream-integration"
stream_integration.mkdir()
for name in ["encoder_config.go","video_source.go","video_source_test.go","state.go","state_test.go"]:
    shutil.copyfile(root/"server/service/stream"/name,stream_integration/(name+".integration"))

for name in ["audio.go","audio_test.go"]:
    shutil.copyfile(root/"server/service/rustdesk"/name,go_transport/(name+".integration"))
# Keep the app module name so the unchanged shared-audio import resolves offline.
(go_transport/"go.mod").write_text((root/"server/go.mod").read_text())
shared_audio=go_transport/"service/stream/audio"
shared_audio.mkdir(parents=True)
for name in ["audio.go","audio_test.go"]:
    shutil.copyfile(root/"server/service/stream/audio"/name,shared_audio/name)
shutil.copyfile(root/"server/go.sum",go_transport/"go.sum")
# Preserve all local Pion replacements: these are production C906 changes.
for line in (root/"server/go.mod").read_text().splitlines():
    if "=> ./" in line:
        component=Path(line.split("=>",1)[1].strip())
        shutil.copytree(root/"server"/component,go_transport/component,dirs_exist_ok=True)
subprocess.run(["go","mod","tidy"],cwd=go_transport,check=True)
subprocess.run(["go","mod","vendor"],cwd=go_transport,check=True)
(go_transport/"README.md").write_text("""# Pion and shared USB audio IPC components

The application runs webrtc.go and audio.go in service/rustdesk. The standalone source here
includes locked, vendored dependencies, shared-hub and real data-channel tests:
go test -mod=vendor -race ./...
bridge.go.integration and audio*.integration record application start/stop,
root-only sockets and integration tests; run those in the full app module. its common/authn/media dependencies belong to the NanoKVM app source.
The Unix listener must use SO_PEERCRED to allow uid 0 only, directory 0700 and
socket 0600. Offer/answer/candidate/attach JSON has a u32 little-endian length
(maximum 64 KiB). A one-time attach token switches the socket after ready to
RustDesk's 1-4 byte little-endian length framing. Data-channel fragment byte 1
means continuation, byte 0 means final. No source is installed on the device.

audio.go.integration attaches to the existing service/stream/audio hub, also exported here.
The audio socket accepts a bounded newline JSON request, version=1 and
 audio=info or opus. OKAF v1 uses a 16-byte header: magic, version, codec
(0 unavailable / 1 Opus), channels, zero reserved byte, sample rate u32be,
payload size u16be, two zero reserved bytes. Format headers have no payload.
Opus packets are 48 kHz stereo, 20 ms, at most 1275 bytes; all subscribers use
one capture helper. Client EOF releases only that subscription. The existing
native/usb-audio capture source and build script are included as integration
references; their dependencies remain pinned by the script. The native helper
belongs to the NanoKVM application package and is not duplicated by the add-on.
""")
shutil.copytree(root/"native/usb-audio",go_transport/"native/usb-audio")
shutil.copyfile(root/"scripts/build-usb-audio.py",go_transport/"build-usb-audio.py.integration")
shutil.copyfile(root/"scripts/nanokvm_cpu_profile.py",go_transport/"nanokvm_cpu_profile.py.integration")

(stage/"docs").mkdir(exist_ok=True)
for name in ["rustdesk-1.5-review.md","rustdesk-1.5-migration.md","rustdesk-usb-audio.md"]:
    shutil.copyfile(root/"docs"/name,stage/"docs"/name)
# Raw diagnostic reports contain private device/controller IDs; publish only
# the generic plan and summarized qualification evidence.
(stage/"docs/rustdesk-device-test.md").write_text("""# Device qualification

Historical 0.2-series lab tests verified public ID registration and encrypted
relay video. Raw diagnostic identifiers and local host/device paths are kept
only in the development workspace. See rustdesk-1.5-migration.md for the current
candidate's host/device qualification evidence and transport limits.
""")
(stage/"docs/rustdesk-handoff.md").write_text("""# Device test plan

Use a coordinated test slot. Upgrade the NanoKVM app with bridge=1, webrtc=1, audio=1 and auto-codec=1
before installing the add-on. Verify the installed add-on and protocol versions,
public source URL/digest, identity/config preservation, registration, temporary
password rotation and reconnect, actual 1.5-client encrypted relay/WebRTC video,
view-only behavior, input ownership/release and transport cleanup under bounded
CPU/RSS. Restore test settings afterwards. Do not create device backups or leave
uploaded APKs/source archives on device storage.
""")
shutil.copyfile(root/"server/service/rustdesk/webrtc_socket_test.go",go_transport/"webrtc_socket_test.go.integration")
archive=output/f"nanokvm-rustdesk-{version}-source.tar.gz"
with tarfile.open(archive,"w:gz") as tar:tar.add(stage,arcname=stage.name)
# Publish/commit this external production recipe after computing the archive.
checksum_block="sha512sums=\""+hashlib.sha512(archive.read_bytes()).hexdigest()+"  "+archive.name+"\n"+hashlib.sha512((pkg/"nanokvm-rustdesk.initd").read_bytes()).hexdigest()+"  nanokvm-rustdesk.initd\"\n"
production_recipe=re.sub(r"(?ms)^sha512sums=.*\Z", "",recipe).rstrip()+"\n\n"+checksum_block
(output/"APKBUILD").write_text(production_recipe)
payload=staging_root/"payload"
def install(src,dest,mode=0o644):
    path=payload/dest.lstrip("/")
    path.parent.mkdir(parents=True,exist_ok=True)
    shutil.copyfile(src,path)
    path.chmod(mode)
install(binary,"usr/bin/nanokvm-rustdesk",0o755)
install(pkg/"nanokvm-rustdesk.initd","etc/init.d/nanokvm-rustdesk",0o755)
install(source/"LICENSE","usr/share/licenses/nanokvm-rustdesk/LICENSE")
install(source/"NOTICE","usr/share/licenses/nanokvm-rustdesk/NOTICE")
source_record=staging_root/"source.json"
source_record.write_text(json.dumps({"url":args.source_url,"sha256":hashlib.sha256(archive.read_bytes()).hexdigest(),"package_version":package_version},indent=2)+"\n")
install(source_record,"usr/share/nanokvm-rustdesk/source.json")
install(source/"upstream.json","usr/share/nanokvm-rustdesk/upstream.json")
apkfile=output/f"nanokvm-rustdesk-{package_version}.apk"
command=[str(args.apk.resolve()),"mkpkg","--files",str(payload),"--output",str(apkfile)]
for value in ["name:nanokvm-rustdesk",f"version:{package_version}","arch:riscv64","license:AGPL-3.0-only","description:RustDesk HDMI and USB HID endpoint for NanoKVM OS","depends:nanokvm-rustdesk-bridge=1 nanokvm-rustdesk-webrtc=1 nanokvm-rustdesk-audio=1 nanokvm-rustdesk-auto-codec=1 openrc","url:https://github.com/onekvm/onekvm-extension-rustdesk"]:
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

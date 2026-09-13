#!/usr/bin/env python3
"""Reconstruct a legacy installed rootfs from a signed format-3 delta."""
import argparse
import gzip
import hashlib
import io
import json
import os
import shutil
import stat
import struct
import subprocess
import tarfile
import tempfile
from pathlib import Path, PurePosixPath

from cryptography.hazmat.primitives import serialization

MAGIC = b"NKOSAPP1"
CONTEXT = b"NanoKVM OS application update v1\n"

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--source-rootfs", type=Path, required=True)
p.add_argument("--package", type=Path, required=True)
p.add_argument("--public-key", type=Path, required=True)
p.add_argument("--expected-source-base", required=True)
p.add_argument("--expected-target-base", required=True)
p.add_argument("--expected-native-abi", required=True)
p.add_argument("--expected-package-sha256", required=True)
p.add_argument("--expected-server-sha256", required=True)
p.add_argument("--expected-helper-sha256", required=True)
p.add_argument("--expected-fit-sha256", required=True)
p.add_argument("--fip", type=Path, required=True)
p.add_argument("--expected-fip-sha256", required=True)
p.add_argument("--output-rootfs", type=Path, required=True)
p.add_argument("--output-fit", type=Path, required=True)
p.add_argument("--provenance", type=Path, required=True)
a = p.parse_args()
if os.geteuid() != 0:
    p.error("run in an isolated WSL root process so the copied image can be loop-mounted")

for value in (a.expected_source_base, a.expected_target_base, a.expected_native_abi,
              a.expected_package_sha256, a.expected_server_sha256,
              a.expected_helper_sha256, a.expected_fit_sha256, a.expected_fip_sha256):
    if len(value) != 64 or any(c not in "0123456789abcdef" for c in value):
        p.error("expected identities must be lowercase SHA-256 values")
for source in (a.source_rootfs, a.package, a.public_key, a.fip):
    if not source.is_file():
        p.error(f"missing input: {source}")
for output in (a.output_rootfs, a.output_fit, a.provenance):
    if output.exists():
        p.error(f"refusing to replace output: {output}")
    output.parent.mkdir(parents=True, exist_ok=True)

def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()

package_sha = sha256(a.package)
if package_sha != a.expected_package_sha256:
    p.error("signed source package hash mismatch")
if sha256(a.fip) != a.expected_fip_sha256:
    p.error("FIP hash mismatch")
raw = a.package.read_bytes()
if raw[:8] != MAGIC or len(raw) < 76:
    p.error("invalid NanoKVM OS package")
length = struct.unpack(">I", raw[8:12])[0]
if length < 1 or 12 + length + 64 >= len(raw):
    p.error("invalid manifest length")
manifest_raw = raw[12:12 + length]
signature = raw[12 + length:12 + length + 64]
payload = raw[12 + length + 64:]
public_key = serialization.load_pem_public_key(a.public_key.read_bytes())
try:
    public_key.verify(signature, CONTEXT + manifest_raw)
except Exception as error:
    p.error(f"release signature verification failed: {error}")
manifest = json.loads(manifest_raw)
if manifest.get("format") != 3 or manifest.get("kind") != "system" or manifest.get("arch") != "riscv64":
    p.error("source package is not a legacy format-3 system update")
kernel = manifest.get("kernel") or {}
if (manifest.get("system_base") != a.expected_source_base or
        kernel.get("system_base") != a.expected_target_base or
        manifest.get("native_abi") != a.expected_native_abi):
    p.error("source or target package identity mismatch")
if len(payload) != manifest.get("payload_bytes") or hashlib.sha256(payload).hexdigest() != manifest.get("payload_sha256"):
    p.error("compressed payload identity mismatch")

entries = {item["path"]: item for item in manifest.get("files", [])}
if len(entries) != len(manifest.get("files", [])):
    p.error("duplicate manifest path")
for required, expected in (("NanoKVM-Server", a.expected_server_sha256),
                           ("rootfs/usr/sbin/nkos-update", a.expected_helper_sha256),
                           ("rootfs/boot/boot.sd", a.expected_fit_sha256)):
    if entries.get(required, {}).get("sha256") != expected:
        p.error(f"required component mismatch: {required}")

source_sha = sha256(a.source_rootfs)
work = Path(tempfile.mkdtemp(prefix="nkos-reconstruct-"))
mount = work / "root"
mount.mkdir()
mounted = False
success = False

def checked_parent(destination, create=True):
    relative = destination.relative_to(mount)
    current = mount
    for part in relative.parts[:-1]:
        current = current / part
        try:
            info = current.lstat()
        except FileNotFoundError:
            if not create:
                return
            current.mkdir(mode=0o755)
            info = current.lstat()
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise RuntimeError(f"unsafe destination parent: {current}")

def remove_file(destination):
    checked_parent(destination, create=False)
    try:
        info = destination.lstat()
    except FileNotFoundError:
        return
    if stat.S_ISDIR(info.st_mode):
        raise RuntimeError(f"refusing to remove directory: {destination}")
    destination.unlink()

def install(destination, content, entry):
    checked_parent(destination)
    remove_file(destination)
    link = entry.get("link", "")
    if link:
        if content != link.encode():
            raise RuntimeError(f"symlink payload mismatch: {entry['path']}")
        destination.symlink_to(link)
        return
    temporary = destination.with_name(destination.name + ".nkos-reconstruct")
    remove_file(temporary)
    with open(temporary, "xb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())
    temporary.chmod(entry["mode"])
    os.replace(temporary, destination)

try:
    subprocess.run(["cp", "--reflink=auto", "--sparse=always", a.source_rootfs, a.output_rootfs], check=True)
    subprocess.run(["e2fsck", "-f", "-y", a.output_rootfs], check=True, stdout=subprocess.PIPE, text=True)
    subprocess.run(["mount", "-o", "loop,rw,nosuid,nodev", a.output_rootfs, mount], check=True)
    mounted = True
    if (mount / "etc/nkos-system-base").read_text().strip() != a.expected_source_base:
        raise RuntimeError("source rootfs foundation mismatch")

    old_server = mount / "kvmapp/server"
    saved_libs = mount / "kvmapp/.nkos-reconstruct-dl_lib"
    if saved_libs.exists():
        raise RuntimeError("temporary native directory already exists")
    (old_server / "dl_lib").rename(saved_libs)
    shutil.rmtree(old_server)
    old_server.mkdir(mode=0o755)
    saved_libs.rename(old_server / "dl_lib")

    seen = set()
    with tarfile.open(fileobj=gzip.GzipFile(fileobj=io.BytesIO(payload)), mode="r|") as archive:
        for member in archive:
            name = PurePosixPath(member.name)
            if not member.isfile() or name.is_absolute() or ".." in name.parts or member.name in seen:
                raise RuntimeError(f"unsafe payload member: {member.name}")
            entry = entries.get(member.name)
            if entry is None or member.size != entry.get("size"):
                raise RuntimeError(f"unlisted payload member: {member.name}")
            content = archive.extractfile(member).read()
            if hashlib.sha256(content).hexdigest() != entry.get("sha256"):
                raise RuntimeError(f"payload member hash mismatch: {member.name}")
            seen.add(member.name)
            if member.name == "rootfs/boot/boot.sd":
                a.output_fit.write_bytes(content)
            elif member.name == "NanoKVM-Server" or member.name.startswith("web/"):
                install(old_server / member.name, content, entry)
            elif member.name.startswith("rootfs/"):
                install(mount / member.name.removeprefix("rootfs/"), content, entry)
            else:
                raise RuntimeError(f"unsupported payload member: {member.name}")
    if seen != set(entries):
        raise RuntimeError("payload does not contain every manifest entry")

    for name in manifest.get("remove", []):
        if not name.startswith("rootfs/"):
            raise RuntimeError(f"unsupported removal: {name}")
        remove_file(mount / name.removeprefix("rootfs/"))

    (mount / "etc/nkos-system-base").write_text(a.expected_target_base + "\n")
    (mount / "kvmapp/version").write_text(manifest["version"] + "\n")
    identity = json.dumps({"version": manifest["version"], "sequence": manifest["sequence"]}, separators=(",", ":"))
    (mount / "kvmapp/.os-update").mkdir(mode=0o700, exist_ok=True)
    (mount / "kvmapp/.os-update/installed.json").write_text(identity)

    for name, entry in entries.items():
        if name == "rootfs/boot/boot.sd":
            continue
        destination = old_server / name if name == "NanoKVM-Server" or name.startswith("web/") else mount / name.removeprefix("rootfs/")
        info = destination.lstat()
        if entry.get("link"):
            if not stat.S_ISLNK(info.st_mode) or os.readlink(destination) != entry["link"]:
                raise RuntimeError(f"installed symlink mismatch: {name}")
            actual = hashlib.sha256(entry["link"].encode()).hexdigest()
            mode_matches = True  # symlink creation uses the kernel-defined 0777 mode
        else:
            if not stat.S_ISREG(info.st_mode):
                raise RuntimeError(f"installed file type mismatch: {name}")
            actual = sha256(destination)
            mode_matches = stat.S_IMODE(info.st_mode) == entry["mode"]
        if actual != entry["sha256"] or not mode_matches:
            raise RuntimeError(f"installed file identity mismatch: {name}")
    for name in manifest.get("remove", []):
        if (mount / name.removeprefix("rootfs/")).exists() or (mount / name.removeprefix("rootfs/")).is_symlink():
            raise RuntimeError(f"removed path remains: {name}")

    native = old_server / "dl_lib"
    rows = []
    for item in native.iterdir():
        if not item.is_file() or item.is_symlink():
            raise RuntimeError(f"unexpected native entry: {item.name}")
        rows.append(f"{item.name}:{sha256(item)}\n")
    native_abi = hashlib.sha256("".join(sorted(rows)).encode()).hexdigest()
    if native_abi != a.expected_native_abi:
        raise RuntimeError("reconstructed native ABI mismatch")
    module_root = mount / "usr/lib/modules"
    target_module_root = module_root / kernel["release"]
    actual_modules = {str(item.relative_to(mount)) for item in target_module_root.rglob("*") if item.is_file() or item.is_symlink()}
    expected_modules = {name.removeprefix("rootfs/") for name in entries
                        if name.startswith(f"rootfs/usr/lib/modules/{kernel['release']}/")}
    if actual_modules != expected_modules:
        raise RuntimeError("active kernel module closure differs from signed target")
    inherited_module_releases = sorted(item.name for item in module_root.iterdir()
                                       if item.is_dir() and item.name != kernel["release"])
    if sha256(a.output_fit) != a.expected_fit_sha256:
        raise RuntimeError("reconstructed FIT mismatch")
    subprocess.run(["sync"], check=True)
    subprocess.run(["umount", mount], check=True)
    mounted = False
    subprocess.run(["e2fsck", "-f", "-n", a.output_rootfs], check=True, stdout=subprocess.PIPE, text=True)
    result_sha = sha256(a.output_rootfs)
    provenance = {
        "qualification": "offline host reconstruction; not booted or hardware tested",
        "source_rootfs": {"path": str(a.source_rootfs), "sha256": source_sha},
        "signed_delta": {"path": str(a.package), "sha256": package_sha, "signature_verified": True,
                         "format": 3, "version": manifest["version"], "sequence": manifest["sequence"]},
        "source_base": a.expected_source_base,
        "target_base": a.expected_target_base,
        "native_abi": native_abi,
        "server_sha256": a.expected_server_sha256,
        "helper_sha256": a.expected_helper_sha256,
        "openssl": {
            "version": "3.6.4",
            "usr_bin_openssl_sha256": entries["rootfs/usr/bin/openssl"]["sha256"],
            "usr_lib_libcrypto_so_3_sha256": entries["rootfs/usr/lib/libcrypto.so.3"]["sha256"],
            "usr_lib_libssl_so_3_sha256": entries["rootfs/usr/lib/libssl.so.3"]["sha256"],
            "identity_source": "signed format-3 beta5 payload",
        },
        "fit": {"path": str(a.output_fit), "sha256": sha256(a.output_fit)},
        "fip": {"path": str(a.fip), "sha256": sha256(a.fip)},
        "kernel_release": kernel.get("release"),
        "kernel_module_entries": len(actual_modules),
        "inherited_inactive_module_releases": inherited_module_releases,
        "verified_payload_entries": len(seen),
        "verified_removals": len(manifest.get("remove", [])),
        "output_rootfs": {"path": str(a.output_rootfs), "sha256": result_sha},
    }
    a.provenance.write_text(json.dumps(provenance, indent=2, sort_keys=True) + "\n")
    success = True
finally:
    if mounted:
        subprocess.run(["umount", mount], check=False)
    shutil.rmtree(work, ignore_errors=True)
    if not success:
        a.output_rootfs.unlink(missing_ok=True)
        a.output_fit.unlink(missing_ok=True)
        a.provenance.unlink(missing_ok=True)

print(json.dumps(provenance, indent=2, sort_keys=True))

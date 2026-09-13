#!/usr/bin/env python3
"""Verify a signed helper bootstrap with the exact published legacy parser."""
import argparse
import gzip
import hashlib
import io
import json
import struct
import subprocess
import tarfile
import tempfile
from pathlib import Path

PUBLISHED = {
    "v1.0.0-beta.3": "c534cce7d1e3ef48809bbcfe1be48852d28f38f3",
    "v1.0.0-beta.4": "f0f65c1372e766d3db2807cbbe8bea06d847b9b8",
}

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--public-source", type=Path, required=True, help="clean NanoKVM-OS git clone")
p.add_argument("--tag", choices=sorted(PUBLISHED), required=True)
p.add_argument("--package", type=Path, required=True)
p.add_argument("--expected-format", type=int, choices=(2, 3), default=2,
               help="legacy manifest format expected by the published parser (default: 2)")
p.add_argument("--native-libs", type=Path, required=True, help="dl_lib extracted from the published image")
p.add_argument("--system-base", required=True)
p.add_argument("--go", type=Path, required=True)
p.add_argument("--readelf", type=Path, required=True)
a = p.parse_args()

commit = subprocess.check_output(["git", "-C", a.public_source, "rev-parse", f"{a.tag}^{{commit}}"], text=True).strip()
if commit != PUBLISHED[a.tag]:
    p.error(f"published tag mismatch: {commit}")

raw = a.package.read_bytes()
if raw[:8] != b"NKOSAPP1" or len(raw) < 76:
    p.error("not a signed NanoKVM OS package")
manifest_len = struct.unpack(">I", raw[8:12])[0]
manifest = json.loads(raw[12:12 + manifest_len])
if manifest.get("format") != a.expected_format or manifest.get("kind") != "system":
    p.error(f"legacy package must be format {a.expected_format} system")
if manifest.get("system_base") != a.system_base:
    p.error("bootstrap system base does not match published image")
rows = []
for item in a.native_libs.iterdir():
    if not item.is_file():
        p.error(f"unexpected native entry: {item}")
    rows.append(f"{item.name}:{hashlib.sha256(item.read_bytes()).hexdigest()}\n")
native_abi = hashlib.sha256("".join(sorted(rows)).encode()).hexdigest()
if manifest.get("native_abi") != native_abi:
    p.error("bootstrap native ABI does not match published image")

payload_at = 12 + manifest_len + 64
with tarfile.open(fileobj=gzip.GzipFile(fileobj=io.BytesIO(raw[payload_at:])), mode="r|") as archive:
    files = {member.name: archive.extractfile(member).read() for member in archive if member.isfile()}
for required in ("NanoKVM-Server", "web/index.html", "rootfs/usr/sbin/nkos-update"):
    if required not in files:
        p.error(f"missing bootstrap member: {required}")

with tempfile.TemporaryDirectory(prefix="nkos-legacy-parser-") as temp_name:
    temp = Path(temp_name)
    archive = subprocess.Popen(["git", "-C", a.public_source, "archive", a.tag], stdout=subprocess.PIPE)
    subprocess.run(["tar", "-x", "-C", temp], stdin=archive.stdout, check=True)
    if archive.wait() != 0:
        raise SystemExit("could not export published source")
    test = temp / "server/osupdate/bootstrap_external_test.go"
    package_literal = json.dumps(str(a.package.resolve()))
    base_literal = json.dumps(a.system_base)
    abi_literal = json.dumps(native_abi)
    format_literal = a.expected_format
    test.write_text(f'''package osupdate
import "testing"
func TestPublishedParserAcceptsBootstrap(t *testing.T) {{
 b, err := Verify({package_literal}); if err != nil {{ t.Fatal(err) }}
 if b.Manifest.Format != {format_literal} || b.Manifest.SystemBase != {base_literal} || b.Manifest.NativeABI != {abi_literal} {{ t.Fatal("contract mismatch") }}
 if err = VerifyContents({package_literal}, b); err != nil {{ t.Fatal(err) }}
}}
''')
    subprocess.run([a.go, "test", "./osupdate", "-run", "TestPublishedParserAcceptsBootstrap", "-count=1"], cwd=temp / "server", check=True)
    server = temp / "NanoKVM-Server"
    helper = temp / "nkos-update"
    server.write_bytes(files["NanoKVM-Server"])
    helper.write_bytes(files["rootfs/usr/sbin/nkos-update"])
    elf_header = subprocess.check_output([a.readelf, "-h", helper], text=True)
    elf_programs = subprocess.check_output([a.readelf, "-l", helper], text=True)
    if "Machine:                           RISC-V" not in elf_header or "Type:                              EXEC" not in elf_header:
        p.error("replacement helper is not RISC-V ELF64 ET_EXEC")
    if "INTERP" in elf_programs or "DYNAMIC" in elf_programs:
        p.error("replacement helper is dynamically linked")
    dynamic = subprocess.check_output([a.readelf, "-d", server], text=True)
    if "Shared library: [libkvm.so]" not in dynamic or "Shared library: [libc.so]" not in dynamic or "$ORIGIN/dl_lib" not in dynamic:
        p.error("bootstrap server has an unexpected runtime linkage contract")

print(json.dumps({
    "tag": a.tag, "commit": commit, "package_sha256": hashlib.sha256(raw).hexdigest(),
    "manifest_format": a.expected_format,
    "system_base": a.system_base, "native_abi": native_abi,
    "published_parser_signature_and_payload": True, "static_helper": True,
    "server_runtime_contract": "libkvm.so + libc.so; hardware health not tested",
}, indent=2))

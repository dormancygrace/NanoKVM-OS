#!/usr/bin/env python3
"""Filter the built C906 Alpine repository into the production-qualified set.

The input repository is treated as immutable.  Selected APKs are copied byte
for byte, then both Alpine repository index formats are regenerated and signed
with the caller supplied key.  The private key is used only as an input and
is never copied to the output or evidence directories.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


ALLOWED_FAMILIES = {"busybox", "coreutils", "lz4", "zstd", "openssl"}
EXCLUDED_FAMILIES = {"xz", "zlib"}
PUBLIC_KEY_NAME = "dgrace-6aaddbb6.rsa.pub"


def run(command: list[str], *, env: dict[str, str] | None = None,
        input_bytes: bytes | None = None,
        cwd: Path | None = None) -> subprocess.CompletedProcess[bytes]:
    merged = os.environ.copy()
    if env:
        merged.update(env)
    print("+", " ".join(command))
    return subprocess.run(command, check=True, env=merged, input=input_bytes,
                          cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def package_info(path: Path) -> tuple[str, str, str]:
    # APK uses an Alpine-specific PAX checksum header that Python's tarfile
    # does not understand.  The system tar reader ignores that extension and
    # is also the reader used by the existing repository tooling.
    raw = subprocess.run(["tar", "-xO", "-f", str(path), ".PKGINFO"],
                         check=True, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL).stdout
    fields: dict[str, str] = {}
    for line in raw.decode("utf-8").splitlines():
        match = re.match(r"^(pkgname|pkgver|arch)\s*=\s*(.*)$", line)
        if match:
            fields[match.group(1)] = match.group(2)
    try:
        return fields["pkgname"], fields["pkgver"], fields["arch"]
    except KeyError as error:
        raise RuntimeError(f"{path.name}: incomplete .PKGINFO") from error


def family_for(pkgname: str) -> str:
    if pkgname == "ssl_client":
        return "busybox"
    if pkgname in {"libcrypto3", "libssl3", "openssl"} or pkgname.startswith("openssl-"):
        return "openssl"
    for family in ("busybox", "coreutils", "lz4", "zstd", "xz", "zlib"):
        if pkgname == family or pkgname.startswith(family + "-"):
            return family
    raise RuntimeError(f"unexpected package in full repository: {pkgname}")


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def apk_command(apk: Path, rootfs: Path | None, qemu: Path | None,
                *args: str) -> list[str]:
    if rootfs is not None and qemu is not None:
        return [str(qemu), "-L", str(rootfs), str(apk), *args]
    return [str(apk), *args]


def apk_run(apk: Path, rootfs: Path | None, qemu: Path | None,
            *args: str, env: dict[str, str] | None = None) -> None:
    result = run(apk_command(apk, rootfs, qemu, *args), env=env)
    if result.stdout:
        sys.stdout.buffer.write(result.stdout)
    if result.stderr:
        sys.stderr.buffer.write(result.stderr)


def parse_source_epoch(source_index: Path) -> int:
    """Use the package timestamp in the source index for deterministic output."""
    raw = subprocess.run(["tar", "-xOzf", str(source_index), "APKINDEX"],
                         check=True, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL).stdout
    match = re.search(rb"^t:(\d+)$", raw, re.MULTILINE)
    if match is None:
        raise RuntimeError(f"{source_index}: no package timestamp")
    return int(match.group(1))


def write_manifest(output: Path, packages: list[Path]) -> None:
    rows = ["pkgname\tversion\tarch\tapk\tsha256"]
    for package in packages:
        name, version, arch = package_info(package)
        if arch not in {"riscv64", "noarch"}:
            raise RuntimeError(f"{package.name}: unsupported architecture {arch}")
        rows.append(f"{name}\t{version}\t{arch}\t{package.name}\t{sha256(package)}")
    (output / "manifest.tsv").write_text("\n".join(rows) + "\n", encoding="utf-8")


def sign_legacy_index(unsigned: Path, signed: Path, key: Path, key_name: str,
                      epoch: int, abuild_tar: Path, rootfs: Path | None,
                      qemu: Path | None, work: Path) -> None:
    signature = work / "index.signature"
    run(["openssl", "dgst", "-sha1", "-sign", str(key), "-out",
         str(signature), str(unsigned)])
    os.utime(signature, (epoch, epoch))

    member_name = f".SIGN.RSA.{key_name}"
    signature_member = work / member_name
    shutil.copyfile(signature, signature_member)
    tar_path = work / "signature.tar"
    run(["tar", "--format=posix", f"--mtime=@{epoch}",
         "--pax-option=exthdr.name=%d/PaxHeaders/%f,atime:=0,ctime:=0",
         "--no-recursion", "--null", "-cf", str(tar_path), member_name],
        env={"SOURCE_DATE_EPOCH": str(epoch)}, cwd=work)
    # abuild-tar adds Alpine's checksum metadata and removes the final tar EOF
    # records.  apk accepts this exact legacy signature envelope.
    command = ([str(qemu), "-L", str(rootfs), str(abuild_tar), "--hash", "--cut"]
               if rootfs is not None and qemu is not None
               else [str(abuild_tar), "--hash", "--cut"])
    cut = run(command, input_bytes=tar_path.read_bytes()).stdout
    compressed = subprocess.run(["gzip", "-n", "-9"], check=True,
                                input=cut, stdout=subprocess.PIPE).stdout
    signed.write_bytes(compressed + unsigned.read_bytes())


def main() -> int:
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path,
                        default=root / "work/alpine/repo-c906-full/c906-scalar")
    parser.add_argument("--output", type=Path,
                        default=root / "work/alpine/repo-c906-qualified")
    parser.add_argument("--evidence", type=Path,
                        default=root / "firmware/alpine/evidence/2026-09-19-qualified-c906")
    parser.add_argument("--sign-key", type=Path,
                        default=Path(os.environ["SIGN_KEY"]) if os.environ.get("SIGN_KEY") else None)
    parser.add_argument("--rootfs", type=Path,
                        default=Path(os.environ.get("ALPINE_TOOL_ROOTFS", root / "work/alpine/tuned-builder-full/rootfs")))
    parser.add_argument("--qemu", type=Path,
                        default=Path(os.environ.get("QEMU_RISCV64", root / "work/alpine/tuned-builder-full/rootfs/usr/bin/qemu-riscv64-static")))
    parser.add_argument("--apk", type=Path,
                        default=Path(os.environ.get("APK_BIN", "")) if os.environ.get("APK_BIN") else None)
    parser.add_argument("--abuild-tar", type=Path,
                        default=Path(os.environ.get("ABUILD_TAR", root / "work/alpine/tuned-builder-full/rootfs/usr/bin/abuild-tar")))
    parser.add_argument("--custom-repository", default=os.environ.get(
        "CUSTOM_REPOSITORY", "https://packages.nanokvm.invalid/alpine/v3.24/c906-scalar-qualified"))
    parser.add_argument("--force", action="store_true",
                        help="replace the exact output and evidence directories")
    args = parser.parse_args()

    if args.sign_key is None:
        parser.error("--sign-key or SIGN_KEY is required; the private key is never copied")
    source = args.source.resolve()
    output = args.output.resolve()
    evidence = args.evidence.resolve()
    key = args.sign_key.resolve()
    if not source.is_dir():
        parser.error(f"source repository does not exist: {source}")
    if not key.is_file():
        parser.error(f"signing key does not exist: {key}")
    for path in (output, evidence):
        if path.exists():
            if not args.force:
                parser.error(f"output exists; use --force only to replace it: {path}")
            shutil.rmtree(path)

    source_arch = source / "riscv64"
    source_index = source_arch / "APKINDEX.tar.gz"
    if not source_index.is_file():
        parser.error(f"source index does not exist: {source_index}")
    source_packages = sorted(source_arch.glob("*.apk"))
    if not source_packages:
        parser.error(f"source repository has no APKs: {source_arch}")

    apk = args.apk
    rootfs = args.rootfs.resolve() if args.rootfs and args.rootfs.exists() else None
    qemu = args.qemu.resolve() if args.qemu and args.qemu.exists() else None
    if apk is None:
        apk = (rootfs / "sbin/apk") if rootfs else Path("apk")
    if not apk.exists() and str(apk) != "apk":
        parser.error(f"apk binary does not exist: {apk}")
    if rootfs is not None and qemu is None:
        parser.error(f"qemu binary does not exist: {args.qemu}")
    if not args.abuild_tar.exists():
        parser.error(f"abuild-tar does not exist: {args.abuild_tar}")

    epoch = int(os.environ.get("SOURCE_DATE_EPOCH", parse_source_epoch(source_index)))
    key_name = key.name + ".pub" if not key.name.endswith(".pub") else key.name
    with tempfile.TemporaryDirectory(prefix="nanokvm-qualified-") as temp_name:
        temp = Path(temp_name)
        keys = temp / "keys"
        keys.mkdir()
        public_key = keys / key_name
        public_source = key.with_name(key.name + ".pub")
        if public_source.is_file():
            shutil.copyfile(public_source, public_key)
        else:
            run(["openssl", "rsa", "-in", str(key), "-pubout", "-out", str(public_key)])
        public_hash = sha256(public_key)
        key_stem = public_key.name

        selected: list[Path] = []
        families: dict[str, int] = {family: 0 for family in sorted(ALLOWED_FAMILIES)}
        for package in source_packages:
            name, _, _ = package_info(package)
            family = family_for(name)
            if family in EXCLUDED_FAMILIES:
                continue
            if family not in ALLOWED_FAMILIES:
                raise RuntimeError(f"unapproved family {family}: {package.name}")
            selected.append(package)
            families[family] += 1
        if not selected:
            raise RuntimeError("the qualified package set is empty")

        # Verify every selected source package with the existing public key
        # before copying it.  The source APK bytes remain unchanged.
        for package in selected:
            apk_run(apk, rootfs, qemu, "--keys-dir", str(keys), "verify", str(package))

        staging = temp / "repository"
        staging_arch = staging / "riscv64"
        staging_arch.mkdir(parents=True)
        staged_packages: list[Path] = []
        for package in selected:
            _, _, arch = package_info(package)
            package_dir = staging / arch
            package_dir.mkdir(exist_ok=True)
            destination = package_dir / package.name
            shutil.copyfile(package, destination)
            staged_packages.append(destination)

        apk_env = {"SOURCE_DATE_EPOCH": str(epoch), "QEMU_CPU": "thead-c906"}
        unsigned = temp / "APKINDEX.unsigned.tar.gz"
        apk_run(apk, rootfs, qemu, "index", "--output", str(unsigned),
                "--description", "NanoKVM Alpine v3.24 c906-scalar qualified",
                *[str(package) for package in staged_packages], env=apk_env)
        packages_adb = staging_arch / "Packages.adb"
        apk_run(apk, rootfs, qemu, "--keys-dir", str(keys), "--sign-key", str(key),
                "mkndx", "--output", str(packages_adb),
                "--description", "NanoKVM Alpine v3.24 c906-scalar qualified",
                *[str(package) for package in staged_packages], env=apk_env)
        signed_index = staging_arch / "APKINDEX.tar.gz"
        sign_legacy_index(unsigned, signed_index, key, key_stem, epoch,
                          args.abuild_tar, rootfs, qemu, temp)

        write_manifest(staging, staged_packages)
        for name in ("aports-commit", "c906-scalar-flags"):
            source_meta = source / name
            if source_meta.is_file():
                shutil.copyfile(source_meta, staging / name)
        source_pkgrel = source / "pkgrel.tsv"
        if source_pkgrel.is_file():
            wanted = {package_info(package)[0] for package in selected}
            lines = [line for line in source_pkgrel.read_text().splitlines()
                     if line.split("\t", 1)[0] in wanted]
            (staging / "pkgrel.tsv").write_text("\n".join(lines) + "\n", encoding="utf-8")
        (staging / "repositories").write_text(
            args.custom_repository + "\n"
            "https://dl-cdn.alpinelinux.org/alpine/v3.24/main\n"
            "https://dl-cdn.alpinelinux.org/alpine/v3.24/community\n", encoding="utf-8")
        (staging / "APKINDEX.tar.gz.sha256").write_text(
            f"{sha256(signed_index)}  riscv64/APKINDEX.tar.gz\n", encoding="utf-8")
        (staging / "Packages.adb.sha256").write_text(
            f"{sha256(packages_adb)}  riscv64/Packages.adb\n", encoding="utf-8")

        # Verify both newly generated index signatures and all package
        # signatures before making the output visible.
        apk_run(apk, rootfs, qemu, "--keys-dir", str(keys), "verify", str(signed_index))
        apk_run(apk, rootfs, qemu, "--keys-dir", str(keys), "verify", str(packages_adb))
        for package in staged_packages:
            apk_run(apk, rootfs, qemu, "--keys-dir", str(keys), "verify", str(package))

        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(staging, output)

        evidence.mkdir(parents=True, exist_ok=True)
        for name in ("manifest.tsv", "repositories", "aports-commit",
                     "c906-scalar-flags", "pkgrel.tsv", "APKINDEX.tar.gz.sha256",
                     "Packages.adb.sha256"):
            source_meta = output / name
            if source_meta.is_file():
                shutil.copyfile(source_meta, evidence / name)
        (evidence / "public-key.sha256").write_text(
            f"{public_hash}  {key_stem}\n", encoding="utf-8")
        (evidence / "repository-audit.txt").write_text(
            "qualified_families=busybox coreutils lz4 openssl zstd\n"
            "excluded_families=xz zlib\n"
            f"apk_count={len(selected)}\n"
            f"noarch_apk_count={sum(package_info(package)[2] == 'noarch' for package in selected)}\n"
            + "family_counts=" + " ".join(f"{name}={families[name]}" for name in sorted(families)) + "\n"
            + f"source={source.relative_to(root)}\nsource_index_sha256={sha256(source_index)}\n"
            + f"public_key_sha256={public_hash}\n"
            + f"index_sha256={sha256(signed_index)}\npackages_adb_sha256={sha256(packages_adb)}\n"
            + "apk_signatures=verified\nlegacy_index_signature=verified\npackages_adb_signature=verified\n"
            + "private_key=not copied to output or evidence\n",
            encoding="utf-8")
        (evidence / "README.md").write_text(
            "# Production-qualified Alpine C906 repository\n\n"
            "This evidence records the repository derived from the already built "
            "`work/alpine/repo-c906-full/c906-scalar` repository. The qualified set "
            "contains the busybox, coreutils, lz4, zstd and openssl families. "
            "`xz` and `zlib` are excluded.\n\n"
            "The APK bytes are retained only under the ignored work output "
            "`work/alpine/repo-c906-qualified`. `APKINDEX.tar.gz` and `Packages.adb` "
            "are regenerated and signed with the existing RSA key. Package and index "
            "signatures were verified before publication of the output directory. "
            "Each APK is stored under its declared `riscv64` or `noarch` path. "
            "The private key is not copied to either output or evidence.\n\n"
            "Reproduce from the repository root (the key path is an external secret):\n\n"
            "```sh\n"
            "SIGN_KEY=/secure/keys/dgrace-6aaddbb6.rsa \\\n"
            "  python3 scripts/qualify-alpine-c906-repo.py\n"
            "```\n\n"
            "The script requires Alpine `apk` and `abuild-tar`; in this checkout it "
            "uses the saved riscv64 builder rootfs and qemu runner. Set `APK_BIN`, "
            "`ALPINE_TOOL_ROOTFS`, `QEMU_RISCV64` and `ABUILD_TAR` to use another "
            "matching Alpine toolchain.\n",
            encoding="utf-8")

    print(f"qualified repository: {output}")
    print(f"evidence: {evidence}")
    print(f"packages: {len(selected)}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except subprocess.CalledProcessError as error:
        sys.stderr.buffer.write(error.stderr or b"")
        raise

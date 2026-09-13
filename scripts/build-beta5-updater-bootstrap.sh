#!/bin/bash
# Build the source-bound beta-5 updater transport bootstrap. Never installs it.
set -euo pipefail
usage() { echo "usage: $0 PUBLIC_NANOKVM_OS_GIT BETA5_SOURCE_NKOS BETA5_NATIVE_LIBS GO BUILDROOT_OUTPUT RELEASE_KEY OUTPUT_NKOS" >&2; exit 2; }
[ "$#" -eq 7 ] || usage
script_dir=$(realpath "$(dirname "$0")")
repo=$(realpath "$script_dir/..")
public_git=$(realpath "$1")
source_package=$(realpath "$2")
native_libs=$(realpath "$3")
go=$(realpath "$4")
buildroot=$(realpath "$5")
key=$(realpath "$6")
output=$(realpath -m "$7")
[ ! -e "$output" ] || { echo "output already exists" >&2; exit 2; }
tag=v1.0.0-beta.4
commit=f0f65c1372e766d3db2807cbbe8bea06d847b9b8
beta5_source_commit=e60a6dd1a191c3292566ebcae89918e985e1586d
[ "$(git -C "$public_git" rev-parse "$tag^{commit}")" = "$commit" ] || { echo "wrong public beta-4 verifier tag" >&2; exit 2; }
# The beta-5 transport changes only the update parser/router/helper.  Refuse to
# rebuild if the application implementation has drifted from the clean beta-5
# composition source.  The retained beta-5 and public beta-4 update parsers are
# byte-identical, which makes the public-tag parser an exact compatibility test.
git -C "$repo" cat-file -e "$beta5_source_commit^{commit}"
git -C "$repo" diff --quiet "$commit" "$beta5_source_commit" -- \
  server/osupdate server/router/os_update.go server/cmd/nkos-update
git -C "$repo" diff --quiet "$beta5_source_commit" -- server \
  ':(exclude)server/osupdate' ':(exclude)server/router/os_update.go' \
  ':(exclude)server/cmd/nkos-update' ':(exclude)server/cmd/nkos-full-package' \
  ':(exclude)server/cmd/nkos-updater-package' ':(exclude)server/build.sh' || {
    echo "application sources drifted from the beta-5 composition source" >&2
    exit 2
  }
source_package_hash=b08ef47d1dc02c2effb4eebc472ce8d4d55ec9405b3fa30e7a151664495cddb2
source_base=022f1f4e6f7eba53f4fc9bee193c499b93a3a671641cce3e5ad451d8470f9d16
target_base=a841663bd6d7a54e091e0bf617f6bc56f4dbf579e9a958d65bf8d938833fd1cb
native_abi=f296357935b98b2d13a0930fd882a39d839bdff0543f817c8beb8d1b1bf215d6
installed_helper=4d52f1942c6eebede38b95a9ca571f7ebad3fe7e9496e27b2d5d5e3f466fe1a1
installed_server=b9dff88e64894b19a8049f39dcea5e54ff55e4c842d9b71ebcf5bfa806160257
[ "$(sha256sum "$source_package" | cut -d' ' -f1)" = "$source_package_hash" ] || { echo "wrong signed beta-5 source package" >&2; exit 2; }
work=$(mktemp -d /tmp/nkos-beta5-bootstrap.XXXXXX)
cleanup() { case "$work" in /tmp/nkos-beta5-bootstrap.*) rm -rf -- "$work";; esac; }
trap cleanup EXIT
mkdir -p "$work/source-extract" "$work/build/app" "$work/build/system/usr/sbin"
printf '%s\n' "$source_base" >"$work/source-base"
python3 "$script_dir/verify-legacy-bootstrap.py" --public-source "$public_git" --tag "$tag" --package "$source_package" \
  --expected-format 3 \
  --native-libs "$native_libs" --system-base "$source_base" --go "$go" \
  --readelf "$buildroot/host/bin/riscv64-buildroot-linux-musl-readelf" >/dev/null
python3 - "$source_package" "$work/source-extract" "$target_base" "$installed_helper" "$installed_server" <<'PY'
import gzip, hashlib, io, json, struct, sys, tarfile
from pathlib import Path, PurePosixPath

package, output, target_base, helper_hash, server_hash = sys.argv[1:]
raw = Path(package).read_bytes()
if raw[:8] != b"NKOSAPP1": raise SystemExit("invalid beta-5 source package")
length = struct.unpack(">I", raw[8:12])[0]
manifest = json.loads(raw[12:12+length])
if manifest.get("format") != 3 or manifest.get("version") != "1.0.0-beta.5" or manifest.get("sequence") != 11:
    raise SystemExit("wrong beta-5 source identity")
if manifest.get("kernel", {}).get("system_base") != target_base:
    raise SystemExit("wrong beta-5 target system base")
entries = {entry["path"]: entry for entry in manifest.get("files", [])}
if entries.get("rootfs/usr/sbin/nkos-update", {}).get("sha256") != helper_hash:
    raise SystemExit("beta-5 helper provenance mismatch")
if entries.get("NanoKVM-Server", {}).get("sha256") != server_hash:
    raise SystemExit("beta-5 server provenance mismatch")
destination = Path(output)
with tarfile.open(fileobj=gzip.GzipFile(fileobj=io.BytesIO(raw[12+length+64:]))) as archive:
    seen = set()
    for member in archive:
        name = PurePosixPath(member.name)
        if not member.isfile() or name.is_absolute() or ".." in name.parts:
            raise SystemExit("unsafe beta-5 source payload")
        entry = entries.get(member.name)
        if not entry or member.size != entry.get("size"):
            raise SystemExit("unlisted beta-5 source payload")
        content = archive.extractfile(member).read()
        if hashlib.sha256(content).hexdigest() != entry.get("sha256"):
            raise SystemExit("beta-5 source payload hash mismatch")
        if member.name == "rootfs/usr/sbin/nkos-update":
            (destination / "installed-helper").write_bytes(content)
        elif member.name == "NanoKVM-Server":
            (destination / "installed-server").write_bytes(content)
        elif len(name.parts) > 1 and name.parts[0] == "web":
            path = destination.joinpath(*name.parts)
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
            seen.add(member.name)
if "web/index.html" not in seen:
    raise SystemExit("beta-5 source web payload missing")
PY
[ "$(sha256sum "$work/source-extract/installed-helper" | cut -d' ' -f1)" = "$installed_helper" ]
[ "$(sha256sum "$work/source-extract/installed-server" | cut -d' ' -f1)" = "$installed_server" ]
python3 "$script_dir/build-server-existing-libs.py" --libraries "$native_libs" --buildroot-output "$buildroot" --go "$go" --output "$work/current-server"
cp -a "$work/current-server/dl_lib" "$work/build/app/dl_lib"
cp "$work/current-server/NanoKVM-Server.stripped" "$work/build/app/NanoKVM-Server"
cp -a "$work/source-extract/web" "$work/build/app/web"
cp "$work/current-server/nkos-update" "$work/build/system/usr/sbin/nkos-update"
printf '%s\n' "$target_base" >"$work/target-base"
(
 cd "$script_dir/../server"
 "$go" run ./cmd/nkos-package --app "$work/build/app" --system "$work/build/system" --system-base "$work/target-base" \
   --key "$key" --version 1.0.0-beta.5-bootstrap.1 --sequence 13 --output "$output"
)
python3 "$script_dir/verify-legacy-bootstrap.py" --public-source "$public_git" --tag "$tag" --package "$output" \
  --native-libs "$native_libs" --system-base "$target_base" --go "$go" \
  --readelf "$buildroot/host/bin/riscv64-buildroot-linux-musl-readelf"
echo "Hardware GUI/application health remains untested: $output"

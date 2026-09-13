#!/bin/sh
# Initialize the immutable image's dedicated APK database.
#
# This helper records the exact base/API/capability providers supplied by the
# image.  They are fileless virtual packages created by apk itself; no
# installable provider packages or repository data are written to the image.
set -eu

target=${1:?target root is required}
base_abi=${2:?image ABI version is required}
contract=${3:-$target/usr/share/nkos/addons-contract.json}

case "$base_abi" in
    ''|*[!0-9.]*) exit 1 ;;
esac
printf '%s\n' "$base_abi" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'

[ -d "$target" ] || exit 1
apk_tool=${NKOS_HOST_APK:-}
if [ -z "$apk_tool" ] && [ -n "${HOST_DIR:-}" ]; then
    apk_tool="$HOST_DIR/bin/apk"
fi
[ -n "$apk_tool" ] && [ -x "$apk_tool" ] || {
    echo "initialize-nkos-apk-db: executable host apk is required" >&2
    exit 1
}

apk_root="$target/opt/nkos"
test -f "$apk_root/etc/apk/arch"
test "$(cat "$apk_root/etc/apk/arch")" = riscv64
test -d "$apk_root/etc/apk/keys"
test -f "$contract"
test ! -e "$apk_root/etc/apk/world"
test ! -e "$apk_root/lib/apk/db/installed"
if [ -d "$apk_root/lib/apk/db" ] &&
   [ -n "$(find "$apk_root/lib/apk/db" -mindepth 1 -print -quit)" ]; then
    echo "initialize-nkos-apk-db: APK database is not fresh" >&2
    exit 1
fi
mkdir -p "$apk_root/lib/apk/db"

# Read only the immutable provider declarations from the contract.  The
# contract deliberately contains no addon world, package catalog, or index
# digest, so independent addon updates remain possible.
provider_file=$(mktemp)
trap 'rm -f "$provider_file"' EXIT HUP INT TERM
python3 - "$contract" "$base_abi" >"$provider_file" <<'PY'
import json
import re
import sys

path, base_abi = sys.argv[1:3]
with open(path, encoding="utf-8") as stream:
    document = json.load(stream)
if document.get("format") != 1:
    raise SystemExit("contract format must be 1")
if document.get("base_abi") != base_abi:
    raise SystemExit("contract base_abi does not match image ABI")
server_api = document.get("server_api")
if type(server_api) is not int or server_api < 1:
    raise SystemExit("contract server_api must be a positive integer")

name_re = re.compile(r"^[A-Za-z0-9][A-Za-z0-9+_.-]*$")
version_re = re.compile(r"^[0-9][A-Za-z0-9+_.-]*$")
print(f"nkos-base-abi={base_abi}")
print(f"nkos-server-api={server_api}")
features = document.get("features")
if not isinstance(features, list):
    raise SystemExit("contract features must be an array")
for feature in features:
    if not isinstance(feature, str) or feature.count("=") != 1:
        raise SystemExit("contract feature must be NAME=VERSION")
    name, version = feature.split("=")
    if not name.startswith("nkos-feature-") or not name_re.fullmatch(name):
        raise SystemExit(f"invalid feature provider name: {name!r}")
    if not version_re.fullmatch(version):
        raise SystemExit(f"invalid feature provider version: {version!r}")
    print(feature)
PY

run_apk() {
    "$apk_tool" \
        --root "$apk_root" \
        --root-tmpfs=no \
        --repositories-file /dev/null \
        --keys-dir "$apk_root/etc/apk/keys" \
        --no-network \
        --no-cache \
        --no-logfile \
        "$@"
}

run_apk_add() {
    if [ "$(id -u)" -eq 0 ]; then
        run_apk --no-scripts "$@"
    else
        run_apk --no-scripts --usermode "$@"
    fi
}

first=1
while IFS= read -r provider; do
    if [ "$first" -eq 1 ]; then
        run_apk_add add --initdb --virtual "$provider"
        first=0
    else
        run_apk_add add --virtual "$provider"
    fi
done <"$provider_file"
[ "$first" -eq 0 ]

test -s "$apk_root/lib/apk/db/installed"
test -s "$apk_root/etc/apk/world"
while IFS= read -r provider; do
    run_apk info --installed "${provider%%=*}"
done <"$provider_file"
# A provider must never turn into a downloadable package artifact.
test -z "$(find "$apk_root" -type f -name '*.apk' -print -quit)"
exit 0

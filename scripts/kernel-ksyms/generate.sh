#!/usr/bin/env bash
# Write the exports imported by the external modules of one complete build.
# Bootstrap from an untrimmed build when adding an import that modpost rejects.
set -euo pipefail
images=${1:?usage: generate.sh OUTPUT/images}
here=$(cd "$(dirname "$0")/../.." && pwd)
nm=${NM:-nm}
modules=("$images"/lib/modules/*/extra/*.ko)
dest=$here/platform/kernel/ksyms-keep.txt
temp=$(mktemp "${dest}.XXXXXX")
trap 'rm -f "$temp"' EXIT
{
    echo "# Kernel exports the out-of-tree modules import (scripts/kernel-ksyms/generate.sh)."
    for ko in "${modules[@]}"; do
        "$nm" -u "$ko" | awk 'NF == 2 { print $2; count++ } END { if (!count) exit 1 }' || exit 1
    done | LC_ALL=C sort -u
} > "$temp"
chmod 644 "$temp"
mv -f "$temp" "$dest"

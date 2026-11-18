#!/bin/sh
# generate.sh IMAGES: write platform/kernel/ksyms-keep.txt, the kernel exports that the out-of-tree modules
# import. With CONFIG_TRIM_UNUSED_KSYMS the kernel exports only what its own modules use and this list; the
# out-of-tree modules are built afterwards, so their imports must be listed. A missing one fails their modpost
# ("undefined!"); run this on the IMAGES directory of a build made with the list emptied, then rebuild.
set -e
images=${1:?usage: generate.sh OUTPUT/images}
here=$(cd "$(dirname "$0")/../.." && pwd)
nm=${NM:-nm}
{
    echo "# Kernel exports the out-of-tree modules import (scripts/kernel-ksyms/generate.sh)."
    for ko in "$images"/lib/modules/*/extra/*.ko; do
        "$nm" -u "$ko" | awk '{ print $2 }'
    done | sort -u
} > "$here/platform/kernel/ksyms-keep.txt"

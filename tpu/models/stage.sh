#!/bin/bash
# Stage converted models as the device installs them: their command buffers
# already in the form the runtime gives them at load (helpers/cvimodel-dmabuf.c),
# for the nanokvm-tpu-abi of the pinned runtime.
#   tpu/models/stage.sh MODELS OUTPUT
# MODELS is the output of convert.sh, all parts or some. Writes
# OUTPUT/usr/lib/nanokvm-tpu/models. The runtime's source comes from the
# tpu-runtime entry of platform/sources.lock, checked by its tree ids; the
# host needs git and a C compiler.
set -euo pipefail
models=$(realpath "${1:?usage: stage.sh MODELS OUTPUT}")
out=$(realpath -m "${2:?output directory}")
here=$(dirname "$(realpath "$0")")
repo=$(realpath "$here/../..")
stage=$out/usr/lib/nanokvm-tpu/models
rm -rf "$out"
mkdir -p "$stage" "$out/build"
(cd "$models" && sha256sum -c --quiet SHA256SUMS)

read -r _ url commit specs < <(awk '$1 == "tpu-runtime"' "$repo/platform/sources.lock")
src=$out/build/src
git -C "$out/build" init -q src
git -C "$src" fetch -q --depth 1 --filter=blob:none "$url" "$commit"
git -C "$src" sparse-checkout set --no-cone /cviruntime /cvikernel
git -C "$src" checkout -q FETCH_HEAD
for spec in $specs; do
    path=${spec%%=*}
    case $path in cviruntime|cvikernel) ;; *) continue ;; esac
    [ "$(git -C "$src" rev-parse "HEAD:$path")" = "${spec#*=}" ] || { echo "stage.sh: $path does not match the pin" >&2; exit 1; }
done
soc=$src/cviruntime/src/soc
cc -O2 -Wall -I"$src/cviruntime/include" -I"$src/cvikernel/include" -I"$soc/181x" -I"$soc/common" \
    "$here/../helpers/cvimodel-dmabuf.c" "$soc/181x/bm_dmabuf.c" -o "$out/build/cvimodel-dmabuf"

while read -r _ path; do
    mkdir -p "$stage/$(dirname "$path")"
    case $path in
        *.cvimodel) "$out/build/cvimodel-dmabuf" "$models/$path" "$stage/$path" ;;
        *) cp "$models/$path" "$stage/$path" ;;
    esac
done < "$models/SHA256SUMS"
rm -rf "$out/build"
(cd "$stage" && find . -type f | sort)

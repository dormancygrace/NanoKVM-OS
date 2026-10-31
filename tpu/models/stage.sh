#!/bin/bash
# Stage converted models as the device installs them: their command buffers
# already in the form the runtime gives them at load (helpers/cvimodel-dmabuf.c),
# for the nanokvm-tpu-abi this tree's runtime provides.
#   tpu/models/stage.sh PLATFORM_OUTPUT MODELS OUTPUT
# PLATFORM_OUTPUT is a platform/build.sh output with the tpu step done (its
# runtime sources); MODELS is the output of convert.sh. Writes
# OUTPUT/usr/lib/nanokvm-tpu/models.
set -euo pipefail
platform=$(realpath "${1:?usage: stage.sh PLATFORM_OUTPUT MODELS OUTPUT}")
models=$(realpath "${2:?models directory}")
out=$(realpath -m "${3:?output directory}")
here=$(dirname "$(realpath "$0")")
src=$platform/tpu/src
[ -d "$src/cviruntime" ] || { echo "stage.sh: no runtime sources in $platform/tpu (run its tpu step)" >&2; exit 1; }
stage=$out/usr/lib/nanokvm-tpu/models
rm -rf "$out"
mkdir -p "$stage" "$out/build"
(cd "$models" && sha256sum -c --quiet SHA256SUMS)
soc=$src/cviruntime/src/soc
cc -O2 -Wall -I"$src/cviruntime/include" -I"$src/cvikernel/include" -I"$soc/181x" -I"$soc/common" \
    "$here/../helpers/cvimodel-dmabuf.c" "$soc/181x/bm_dmabuf.c" -o "$out/build/cvimodel-dmabuf"
"$out/build/cvimodel-dmabuf" "$models/ocr-det.cvimodel" "$stage/ocr-det.cvimodel"
"$out/build/cvimodel-dmabuf" "$models/screen.cvimodel" "$stage/screen.cvimodel"
for lang in latin chinese cyrillic; do
    mkdir -p "$stage/$lang"
    for model in "$models/$lang"/ocr-*.cvimodel; do
        "$out/build/cvimodel-dmabuf" "$model" "$stage/$lang/${model##*/}"
    done
    cp "$models/$lang/ocr-dict.txt" "$stage/$lang/"
done
rm -rf "$out/build"
du -sh "$stage"

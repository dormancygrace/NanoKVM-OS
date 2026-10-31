#!/bin/bash
# Convert the TPU models to CV181x cvimodels.
#   tpu/models/convert.sh OUTPUT [PART...]
# PART: det, latin, chinese, cyrillic, screen; all of them by default. A model
# add-on's build converts only its own (.github/workflows/addon.yml).
# Runs Sophgo's tpu-mlir in its official container (sophgo/tpuc_dev); the
# host needs Docker only. The models are PaddleOCR's (Apache-2.0), INT8:
#   ocr-det.cvimodel               PP-OCRv6 medium detection, 960x544: a 1080p
#                                  screen at half size in one pass
#   LANGUAGE/ocr-rec.cvimodel      a recognizer with three programs, 48 high and
#   LANGUAGE/ocr-dict.txt          160, 320 or 640 wide, sharing the weights (a
#                                  line goes through the narrowest that holds it)
#     latin     PP-OCRv6 small with a Latin head (English and the European
#               languages written in Latin letters)
#     chinese   PP-OCRv6 small as it is (Simplified and Traditional Chinese,
#               Japanese, English: 18 708 characters)
#     cyrillic  PP-OCRv5 mobile Cyrillic (Russian, Ukrainian, Belarusian,
#               Serbian, Bulgarian and others, with English)
#   latin/ocr-sure.cvimodel        PP-OCRv6 medium with the same head as the
#   chinese/ocr-sure.cvimodel      small one: the helper reads the lines the
#                                  small model is unsure of again with it
# The PP-OCRv5 recognizer gets its LearnableAffineBlocks folded into the
# convolutions first (fold_affine.py); kept, they run in BF16 at a third of
# the speed. INT8 needs calibration: the tables in calibration/ were measured
# on synthetic screens and text lines, and work for these sources and this
# tpu-mlir only (layer names).
set -euo pipefail
out=$(realpath -m "${1:?usage: convert.sh OUTPUT [PART...]}")
shift
parts=${*:-det latin chinese cyrillic screen}
for part in $parts; do
    case $part in det|latin|chinese|cyrillic|screen) ;; *) echo "convert.sh: unknown part $part" >&2; exit 2 ;; esac
done
here=$(dirname "$(realpath "$0")")
mkdir -p "$out"
cp "$here/sources.txt" "$out/"
# The pip downloads (about 1 GB) stay in a Docker volume between runs.
docker run --rm -v "$out:/work" -v "$here:/models:ro" -v nanokvm-tpu-pip:/root/.cache/pip -w /work \
    -e PARTS="$parts" sophgo/tpuc_dev:v3.4 bash -euo pipefail -c '
want() { case " $PARTS " in *" $1 "*) return 0 ;; esac; return 1; }
# The PaddleOCR inputs each part needs (sources.txt).
needs() {
    case $1 in
        det.tar) want det ;;
        small.tar|medium.tar) want latin || want chinese ;;
        cyrillic.tar) want cyrillic ;;
        *) return 1 ;;
    esac
}
pip install -q --retries 10 --timeout 120 \
    tpu_mlir==1.30.2 paddlepaddle==3.0.0 paddle2onnx==2.0.1 pyyaml "numpy<2"
# (the last line counts without its newline too)
while read -r name sha url || [ -n "$name" ]; do
    [ -n "$name" ] && [ "${name#\#}" = "$name" ] || continue
    needs "$name" || continue
    [ -s "$name" ] || wget -q -O "$name" "$url"
    echo "$sha  $name" | sha256sum -c --quiet
    m=${name%.tar}
    rm -rf "$m.src" && mkdir "$m.src"
    tar -xf "$name" -C "$m.src" --strip-components=1
    # PaddleOCR 3 models describe the network in inference.json.
    paddle2onnx --model_dir "$m.src" --model_filename inference.json --params_filename inference.pdiparams \
        --save_file "$m.onnx" --opset_version 14 >/dev/null
done < sources.txt
dict() {  # MODEL: the dictionary is part of the model configuration
    python3 -c "
import sys, yaml
chars = yaml.safe_load(open(sys.argv[1] + \".src/inference.yml\"))[\"PostProcess\"][\"character_dict\"]
open(sys.argv[1] + \".dict.txt\", \"w\", encoding=\"utf-8\").write(\"\n\".join(chars) + \"\n\")
" "$1"
}
if want latin; then
    python3 /models/latin_head.py small.src small.onnx latin.head.onnx latin.dict.txt
    python3 /models/latin_head.py medium.src medium.onnx latin-sure.head.onnx latin-sure.dict.txt
    # The sure model reads with the dictionary of the small one.
    cmp latin.dict.txt latin-sure.dict.txt
fi
if want chinese; then
    cp small.onnx chinese.head.onnx
    cp medium.onnx chinese-sure.head.onnx
    dict small
    dict medium
    mv small.dict.txt chinese.dict.txt
    cmp chinese.dict.txt medium.dict.txt
fi
if want cyrillic; then
    dict cyrillic
    python3 /models/fold_affine.py cyrillic.onnx cyrillic.head.onnx
fi

# The TPU does the normalization (fused preprocessing): the models take 8-bit
# BGR planes, the layout VPSS can produce. PaddleOCR normalizes BGR with the
# ImageNet mean and std (detection) and to [-1, 1] (recognition).
if want det; then
    model_transform.py --model_name ocr_det --model_def det.onnx --input_shapes "[[1,3,544,960]]" \
        --mean 123.675,116.28,103.53 --scale 0.017125,0.017507,0.017429 --pixel_format bgr --mlir det.mlir >/dev/null
    model_deploy.py --mlir det.mlir --quantize INT8 --calibration_table /models/calibration/det.cali --chip cv181x \
        --fuse_preprocess --customization_format BGR_PLANAR --model ocr-det.cvimodel >/dev/null
fi
recognizer() {  # MODEL OUTPUT
    # --merge_weight shares the weights with every model deployed before in
    # the same directory: one directory per recognizer.
    local m=$1 programs=()
    rm -rf $m.build && mkdir $m.build
    for width in 160 320 640; do
        (cd $m.build &&
        model_transform.py --model_name ocr_${m/-/_}_$width --model_def ../$m.head.onnx --input_shapes "[[1,3,48,$width]]" \
            --mean 127.5,127.5,127.5 --scale 0.0078431,0.0078431,0.0078431 --pixel_format bgr --mlir $m$width.mlir >/dev/null &&
        model_deploy.py --mlir $m$width.mlir --quantize INT8 --calibration_table /models/calibration/$m.cali \
            --chip cv181x --fuse_preprocess --customization_format BGR_PLANAR \
            --merge_weight --model $m$width.cvimodel >/dev/null)
        programs+=($m.build/$m$width.cvimodel)
    done
    model_tool --combine "${programs[@]}" -o "$2" >/dev/null
}
for m in latin chinese cyrillic; do
    want $m || continue
    mkdir -p $m
    cp $m.dict.txt $m/ocr-dict.txt
    recognizer $m $m/ocr-rec.cvimodel
    [ $m = cyrillic ] || recognizer $m-sure $m/ocr-sure.cvimodel
done
# The screen-state classifier, ours (screen/): trained on synthetic screens
# by screen/train.sh; it takes the B, G and R planes scaled to 0..1.
if want screen; then
    model_transform.py --model_name screen --model_def /models/screen/screen.onnx --input_shapes "[[1,3,128,224]]" \
        --mean 0,0,0 --scale 0.0039216,0.0039216,0.0039216 --pixel_format bgr --mlir screen.mlir >/dev/null
    model_deploy.py --mlir screen.mlir --quantize INT8 --calibration_table /models/calibration/screen.cali --chip cv181x \
        --fuse_preprocess --customization_format BGR_PLANAR --model screen.cvimodel >/dev/null
fi
: > SHA256SUMS
for f in ocr-det.cvimodel screen.cvimodel */ocr-*.cvimodel */ocr-dict.txt; do
    [ ! -f "$f" ] || sha256sum "$f" >> SHA256SUMS
done
'
cat "$out/SHA256SUMS"

#!/bin/bash
# Convert the PaddleOCR models of the TPU add-on to CV181x cvimodels.
#   addons/tpu/models/convert.sh OUTPUT
# Runs Sophgo's tpu-mlir in its official container (sophgo/tpuc_dev); the
# host needs Docker only. Writes OUTPUT/ocr-det.cvimodel, ocr-rec-WIDTH.cvimodel
# (160, 320, 640), ocr-dict.txt and SHA256SUMS. The models are PaddleOCR's (Apache-2.0):
#   detection:   PP-OCRv5 mobile, 960x544: a 1080p screen at half size in one
#                pass, larger screens in tiles (32 MB of TPU memory: it
#                fits next to 2160p capture in the 144 MiB region)
#   recognition: East Slavic PP-OCRv5 mobile (Russian, Belarusian, Ukrainian
#                and English), 48 high and 160, 320 or 640 wide
# BF16 needs no calibration images and keeps the float accuracy closely.
set -euo pipefail
out=$(realpath -m "${1:?usage: convert.sh OUTPUT}")
here=$(dirname "$(realpath "$0")")
mkdir -p "$out"
cp "$here/sources.txt" "$out/"
# The pip downloads (about 1 GB) stay in a Docker volume between runs.
docker run --rm -v "$out:/work" -v nanokvm-tpu-pip:/root/.cache/pip -w /work sophgo/tpuc_dev:v3.4 bash -euo pipefail -c '
pip install -q --retries 10 --timeout 120 \
    tpu_mlir==1.30.2 paddlepaddle==3.0.0 paddle2onnx==2.0.1 pyyaml "numpy<2"
while read -r name sha url; do
    [ -n "$name" ] && [ "${name#\#}" = "$name" ] || continue
    [ -s "$name" ] || wget -q -O "$name" "$url"
    echo "$sha  $name" | sha256sum -c --quiet
done < sources.txt
rm -rf det rec && mkdir -p det rec
tar -xf det.tar -C det --strip-components=1
tar -xf rec.tar -C rec --strip-components=1
for m in det rec; do
    # PaddleOCR 3 models describe the network in inference.json.
    graph=inference.json
    [ -s $m/$graph ] || graph=inference.pdmodel
    paddle2onnx --model_dir $m --model_filename $graph --params_filename inference.pdiparams \
        --save_file $m.onnx --opset_version 14 >/dev/null
done
# The recognition dictionary is part of the model configuration.
python3 -c "
import yaml
chars = yaml.safe_load(open(\"rec/inference.yml\"))[\"PostProcess\"][\"character_dict\"]
open(\"ocr-dict.txt\", \"w\", encoding=\"utf-8\").write(\"\n\".join(chars) + \"\n\")
"
# The TPU does the normalization (fused preprocessing): the models take 8-bit
# BGR planes, the layout VPSS can produce. PaddleOCR normalizes BGR with the
# ImageNet mean and std (detection) and to [-1, 1] (recognition).
model_transform.py --model_name ocr_det --model_def det.onnx --input_shapes "[[1,3,544,960]]" \
    --mean 123.675,116.28,103.53 --scale 0.017125,0.017507,0.017429 --pixel_format bgr --mlir det.mlir >/dev/null
model_deploy.py --mlir det.mlir --quantize BF16 --chip cv181x --fuse_preprocess --customization_format BGR_PLANAR \
    --model ocr-det.cvimodel >/dev/null
# Recognition in three widths: a line goes through the narrowest that holds
# it, and the TPU time follows the width (most lines on a screen are short).
for width in 160 320 640; do
    model_transform.py --model_name ocr_rec_$width --model_def rec.onnx --input_shapes "[[1,3,48,$width]]" \
        --mean 127.5,127.5,127.5 --scale 0.0078431,0.0078431,0.0078431 --pixel_format bgr --mlir rec$width.mlir >/dev/null
    model_deploy.py --mlir rec$width.mlir --quantize BF16 --chip cv181x --fuse_preprocess --customization_format BGR_PLANAR \
        --model ocr-rec-$width.cvimodel >/dev/null
done
sha256sum ocr-det.cvimodel ocr-rec-*.cvimodel ocr-dict.txt > SHA256SUMS
'
ls -la "$out"/ocr-*.cvimodel "$out/ocr-dict.txt" "$out/SHA256SUMS"

#!/bin/bash
# Train the screen-state classifier and measure its INT8 calibration table.
#   tpu/models/screen/train.sh WORK
# Draws 30 000 synthetic screens to learn from and 3 000 to check against
# (synth.py), trains on the CPU (train.py) and writes screen.onnx here and
# ../calibration/screen.cali, both used by ../convert.sh. Runs in Sophgo's
# tpu-mlir container, which has PyTorch; the host needs Docker only. The
# seeds are fixed, so a run draws the same screens.
set -euo pipefail
work=$(realpath -m "${1:?usage: train.sh WORK}")
here=$(dirname "$(realpath "$0")")
mkdir -p "$work"
docker run --rm -v "$work:/work" -v "$here:/src:ro" -v nanokvm-tpu-pip:/root/.cache/pip -w /work \
    sophgo/tpuc_dev:v3.4 bash -euo pipefail -c '
pip install -q --retries 10 --timeout 120 tpu_mlir==1.30.2 "numpy<2"
rm -rf train valid calibration
python3 /src/synth.py train 30000 1
python3 /src/synth.py valid 3000 2
python3 /src/train.py train valid screen.onnx
# Calibration on fresh screens of every class.
python3 /src/synth.py calibration 400 3
mkdir -p calibration/all && find calibration -mindepth 2 -name "*.png" -exec cp {} calibration/all/ \;
model_transform.py --model_name screen --model_def screen.onnx --input_shapes "[[1,3,128,224]]" \
    --mean 0,0,0 --scale 0.0039216,0.0039216,0.0039216 --pixel_format bgr --mlir screen.mlir >/dev/null
run_calibration.py screen.mlir --dataset calibration/all --input_num 400 -o screen.cali >/dev/null
'
cp "$work/screen.onnx" "$here/screen.onnx"
cp "$work/screen.cali" "$here/../calibration/screen.cali"
sha256sum "$here/screen.onnx"

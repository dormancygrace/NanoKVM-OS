# NanoKVM TPU add-on

The CV181x TPU runtime and an OCR helper for the screen. The MCP tool
`cube_screen_text` uses it to read the text on the remote screen. It is an
add-on because of the runtime's licence: see NOTICE. Until Sophgo states a
licence for cviruntime, it is built for testing and not published.

## What it installs

Everything goes to `/usr/lib/nanokvm-tpu`:

- `lib/`: `libcviruntime.so`, `libcvikernel.so`, `libcnpy.so`.
- `bin/nanokvm-ocr`: text detection and recognition on a picture (see the
  source for the protocol).
- `bin/model_runner`: Sophgo's tool to run and time a model.
- `models/`: `ocr-det.cvimodel`, `ocr-rec-160.cvimodel`, `ocr-rec-320.cvimodel`,
  `ocr-rec-640.cvimodel`, `ocr-dict.txt`.

The kernel side (the TPU driver and `/dev/cvi-tpu0`, the CMA dma-buf heap)
is part of the system since kernel r6.

## Models

PaddleOCR (Apache-2.0), converted with Sophgo's tpu-mlir to BF16 for CV181x,
with the normalization fused into the models (8-bit BGR planes in):

- **Detection:** PP-OCRv5 mobile, 960×544, on the screen at half size: a
  1080p screen in one pass, larger ones in tiles. Recognition reads the boxes
  from the full picture. It needs 32 MB of TPU memory; the full 1080p frame
  would need 123 MB, more than the region video shares leaves.
- **Recognition:** East Slavic PP-OCRv5 mobile, 48 high and 160, 320 or 640
  wide: each line goes through the narrowest that holds it. Russian,
  Belarusian, Ukrainian and English, with digits and punctuation.

The helper loads one model at a time. The server gives it the screen as B, G
and R planes from VPSS (`kvmv_read_bgr_planar`). Measured on a NanoKVM with a
1920×1080 browser page (74 text boxes): detection 1.1 s, against 3.4 s for
1920×256 strips at full size, with the same lines and text; recognition 7 s.

Tried and dropped:
- Detection at full size in 1920×256 strips: three times slower, no better.
- PaddleOCR's unclip ratio (1.5) at half size: the boxes joined adjacent
  lines; 0.9 gives the boxes of the full picture.
- INT8 detection: 40 % faster per tile, but its map broke lines into words,
  so recognition ran four times as often and the whole read took longer.

`models/convert.sh OUTPUT` converts them in the official `sophgo/tpuc_dev`
container, so the host needs only Docker. `models/sources.txt` pins the
inputs by SHA-256.

## Build

    addons/tpu/models/convert.sh MODELS
    addons/tpu/build.sh PLATFORM_OUTPUT MODELS OUTPUT

`PLATFORM_OUTPUT` is a `platform/build.sh` output with the toolchain and
tools steps. The runtime source is pinned in `upstream.json`, and
`patches/` holds two fixes for musl.

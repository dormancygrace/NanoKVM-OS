# NanoKVM TPU

The programs that run neural networks on the CV181x TPU: `nanokvm-ocr`
reads the text on the remote screen (the web UI's Read text on the screen,
the MCP tool `screen_text`, PicoClaw's `kvm_screen_text`), and
`nanokvm-screen` says what the screen shows, for the screen-crash
notification. They are part of the system; the models they run are
add-ons, so that a model can be updated or added without a release.

## In the system

The core package `nanokvm-tpu` installs `/usr/lib/nanokvm-tpu/bin`:

- `nanokvm-ocr`: text detection and recognition on a picture (see the
  source for the protocol).
- `nanokvm-screen`: a desktop, a Windows stop screen, a text console, a
  blank screen or a firmware screen.

Both are linked statically with Sophgo's runtime (cviruntime and cvikernel,
as ported to dma-buf heaps for the mainline driver) and the C++ library:
they need only the C library, about 1.4 MB each. The runtime's tools
(`model_runner`, cnpy) and its PMU profiling are not built. The kernel side
(the `cv181x_tpu` driver, loaded on demand, and the CMA dma-buf heap) is part
of the kernel.

The package provides `nanokvm-tpu-abi=1`. Model add-ons depend on it: the
models carry command buffers in the layout of this runtime (see Build), and
the programs take them as arguments of a fixed form. A change of either is
a new ABI number and new model add-ons.

## Model add-ons

They install `/usr/lib/nanokvm-tpu/models`: `ocr-det.cvimodel`, per language
`LANGUAGE/ocr-rec.cvimodel`, `ocr-dict.txt` and, for `latin` and `chinese`,
`ocr-sure.cvimodel`; and `screen.cvimodel`, the screen-state classifier.
Without them nothing offers to read the screen, and the crash event finds
no classifier.

## Models

PaddleOCR (Apache-2.0), converted with Sophgo's tpu-mlir to INT8 for CV181x,
with the normalization fused into the models (8-bit BGR planes in):

- **Detection:** PP-OCRv6 medium, 960×544, on the screen at half size: a
  1080p screen in one pass. Recognition reads the boxes from the full
  picture. A selected area up to 1280 wide is detected at full size, which
  keeps small text. The server sends at most 1920×1080.
- **Recognition**, one model per language, 48 high with programs 160, 320
  and 640 wide sharing the weights: each line goes through the narrowest
  that holds it.
  - `latin`: PP-OCRv6 small with its output cut to Latin letters (English
    and the European languages written in Latin letters).
  - `cyrillic`: PP-OCRv5 mobile Cyrillic (Russian, Ukrainian, Belarusian,
    Serbian, Bulgarian and others, with English). Its LearnableAffineBlocks
    are folded into the convolutions first; kept, they run in BF16 at a
    third of the speed.
  - `chinese`: PP-OCRv6 small as it is (Simplified and Traditional Chinese,
    Japanese, English).
- **Second reading:** for `latin` and `chinese`, a line whose least certain
  character is below 0.6 is read again with PP-OCRv6 medium. It loads only
  when a line needs it.
- **Mixed Cyrillic and Latin:** a letter that looks the same in both
  scripts takes the script of the nearest letters that do not.

TPU memory (CMA, shared with video): detection 60 MiB; recognition 5 MiB
(`latin`, `cyrillic`) and 22 MiB (`chinese`); second reading 23 MiB (`latin`)
and 34 MiB (`chinese`). While the TPU runs one model, the helper reads the
next model's file into the page cache. It takes the detector's TPU memory
before reading that file, so the cache does not land in memory the TPU is
about to take back.

Measured on a NanoKVM, whole 1080p screen, with a viewer watching the video:

| Language | Test pages: wrong characters | Time per read |
|---|---|---|
| `latin` | English 0.4 %; German, French, Spanish, Italian, Polish, Czech 0.5–2.7 % | 4.2–4.6 s |
| `cyrillic` | Russian 1.4 %; Russian with English 5.4 % | 2.6–3.9 s |
| `chinese` | Simplified 1.1 %, Traditional 1.9 %, Japanese 1.4 % | 3.7–5.8 s |

On photos of real screens (a GitHub page, a BIOS setup, a kernel panic)
`latin` with the second reading leaves 1.6–2.1 % of characters wrong.

Tried and dropped:
- PP-OCRv6 tiny: about as accurate on clean pages, 8.5 % wrong on a photo.
- Keeping the models loaded: one language takes 89–119 MiB, which does not
  fit beside the video.
- Copying the model files into memory: the copies pushed the page cache out,
  and the next read loaded more from the SD card.
- Korean (PP-OCRv5): needs 33 layers in BF16 to stay accurate; left for
  later.

## Screen classifier

`models/screen/`, ours (GPL-3.0-or-later): a small CNN (0.4 M weights, input
224×128, INT8) that says what the screen shows: `normal` (a desktop or an
application), `bsod` (a Windows stop screen, blue, green or black, old and
new), `console` (a text console, where a kernel panic shows), `blank`, or
`firmware` (BIOS setup, GRUB, a text installer: blue screens that are not a
crash). The server's screen watcher runs it on a still screen and reads a
`bsod` or `console` with the OCR before it reports a crash.

There are no free pictures of real crash screens to learn from, so it is
trained on screens drawn by `synth.py` (nothing copied from real screens)
by `screen/train.sh`, which writes `screen.onnx` and
`calibration/screen.cali`.

- **Accuracy on synthetic screens:** 99.7 % right on 3 000 screens it had not seen, none of them a desktop taken for a crash. The INT8 model on the TPU agreed on 299 of 300.
- **On a real screen:** a desktop in a dark browser read as `normal` at 0.89 in the server, which scales the capture by block means (0.72 when scaled bilinearly, with `console` at 0.12). The watcher reads a screen with the OCR from 0.7.
- **Speed:** 16–18 ms with loading.
- **The OCR check on full HD drawings:**
  - stop screens: the crash words were found on 7 of 8. The miss had only Cyrillic text, which the Latin recognizer does not read; the stop code line is in English in every language of Windows.
  - kernel panics: found on all 8 consoles, from 25 to 67 lines per screen.

## Build

The programs: `platform/build.sh tpu`. The runtime's source is the
`tpu-runtime` entry of `platform/sources.lock` (cviruntime, cvikernel, the
model schema and flatbuffers, pinned by Git tree ids); `patches/` holds two
fixes for musl, makes the tools optional and takes the runtime version from
the pin instead of Git and the build date.

The models:

    tpu/models/convert.sh MODELS
    tpu/models/stage.sh PLATFORM_OUTPUT MODELS OUTPUT

`convert.sh` runs in the official `sophgo/tpuc_dev` container, so the host
needs only Docker. `models/sources.txt` pins its inputs by SHA-256. INT8
needs calibration: the tables in `models/calibration/` were measured on
synthetic screens and text lines, and fit these sources and this tpu-mlir
only (they name layers). `stage.sh` stores the models' command buffers in
the form the runtime would give them at load (`helpers/cvimodel-dmabuf.c`),
which saves that step on the device; `PLATFORM_OUTPUT` is a
`platform/build.sh` output with the tpu step done.

A failed TPU run ends the read with exit status 1, never a partial or empty
result. To check that on a device, link a test `nanokvm-ocr` with
`tests/forward-failure.c` and `-Wl,--wrap=CVI_NN_Forward` (the platform's
compiler, the tpu step's command) and run
`tests/forward-failure.sh HELPER PICTURE`: it fails the detector's and a
recognizer's run in turn.

The daily upstream watch (`.github/upstream-watch.json`) opens a
notification PR for new commits on the runtime branch, a new PP-OCR
generation (PaddleX model list and PaddlePaddle on Hugging Face) and a new
tpu-mlir. It does not change the pins: a new runtime can change the command
buffer layout the models carry (a new ABI), and a new model needs
conversion, calibration and a check of accuracy and speed on a device.

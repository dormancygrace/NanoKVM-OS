# Source and redistribution status

NanoKVM OS retains component-specific upstream licenses. The root GPL-3.0 license covers the application derivative; it does not relicense vendor firmware or every library in the SD image.

## What this repository contains

- The application, web UI, board service, packaging, OpenRC services and build recipes.
- Patches to upstream components (`platform/kernel`, `platform/uboot`, `platform/modules`, `platform/native`).
- Pins of every upstream input in `platform/sources.lock`, with their hashes.
- `platform/fip/base-fip.bin`, the stock Sipeed first-stage boot firmware (see [platform/README.md](../platform/README.md#corresponding-source)).
- The public APK signing key of NanoKVM OS releases (`firmware/alpine/keys`).

Upstream sources are not copied into this repository. `platform/build.sh` downloads each one from its own publisher and checks it against `sources.lock`.

## Components without a license grant

These components are published by their authors in public repositories but carry no license file. They are fetched from those repositories at build time; this repository and the source archive do not redistribute them:

| Component | Publisher | Used for |
|---|---|---|
| `cvi_mpi`, including about 900 prebuilt ISP and audio objects | [SOPHGO](https://github.com/sophgo/cvi_mpi) | the media libraries in `/kvmapp/server/dl_lib` |
| `SensorSupportList` | [SOPHGO](https://github.com/sophgo/SensorSupportList) | the LT6911 HDMI receiver driver in `libkvm_mmf` |
| `coda980.bin`, `monet.bin` video codec firmware | [Sipeed LicheeRV-Nano-Build](https://github.com/sipeed/LicheeRV-Nano-Build) | `/usr/share/fw_vcodec` |

The SD image contains the binaries built from them, as the Sipeed images do. The AIC8800 Wi-Fi firmware comes from the [Radxa aic8800 package](https://github.com/radxa-pkg/aic8800), the same source as the driver; Radxa's `debian/copyright` declares its `src/` tree, which includes the firmware, GPL-2.0, and the image installs that file in `/usr/share/licenses/nanokvm-firmware-sg2002`.

## Corresponding source

`platform/build.sh source` writes the corresponding-source archive of a release: the GPL-licensed upstream inputs, the Buildroot downloads of the initramfs userland and this repository at the release commit. Alpine Linux publishes the source of its packages; the installed versions are listed in `/lib/apk/db/installed` of each image and in `release/installed-packages.txt` of the build.

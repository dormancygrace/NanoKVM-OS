# NanoKVM OS build scripts

The complete SD card image is built by [platform/build.sh](../platform/README.md).
It calls these component builders:

| Script | Builds |
|---|---|
| build-mpi.sh, build-isp.py, build-mpi-bin.py | the SOPHGO media libraries |
| build-mmf.py, build-capture.py | `libkvm_mmf.so` and `libkvm.so` |
| build-system.py | `kvm_system` |
| build-busybox-devmem.sh, build-usb-audio.py | `devmem` and `usb-audio-capture` |
| build-qhd-edid.py, build-monitor-edids.py, build-portrait-edid.py | the EDID profiles |
| edid-rgb-range.py | lets the source send full-range RGB: the last step of the EDID profiles |
| build-openssl.py | Pinned OpenSSL 3.5 LTS runtime with the C906/O3 profile |
| build-alpine-packages.sh | the OS packages, inside the Alpine builder tree |
| build-alpine-sd-image.py | the SD card image |

Run them through `platform/build.sh`: it prepares their pinned sources and toolchain.
The update format is described in [docs/UPDATES.md](../docs/UPDATES.md).
Releases use signed Alpine APKs. Buildroot is limited to the platform compiler,
host tools and boot initramfs.

## Alpine port tooling

The Alpine port keeps normal APK installation in the system root and packages
the board-specific kernel, modules, firmware, OpenRC integration and application
separately. The main entry points are:

| Script | Purpose |
|---|---|
| build-openssl.py | Pinned OpenSSL 3.5 LTS runtime with the C906/O3 profile |
| build-alpine-packages.sh | Build the signed NanoKVM APK repository from exported payloads. |
| build-alpine-update-bundle.sh | Package a root and normal FIT into the tested recovery bundle. |
| build-alpine-recovery-fit.py | Build recovery bound to the root and normal-FIT hashes. |

See firmware/alpine/README.md for architecture and the physical recovery path.

## Make targets

The root Makefile delegates to the same pinned platform builder:

```sh
make platform PLATFORM_ARGS=-d       # complete image with local test keys
make release-build                  # build components and write their hashes
make package PLATFORM_ARGS='-e path/to/key.key'
```

`OUT` and `JOBS` select the output directory and build parallelism. Component
aliases (`app`, `support`, `vision`) use the prepared platform toolchain and
sources. `make web` builds `web/dist` for frontend development. Release versions
come from `firmware/alpine/release.env`, not a separate Makefile variable.

## Stock migration

`install-stock.py` downloads and verifies the stock migration package from a
GitHub release; its default is a compatibility check and `--yes` starts a
detached installation. `build-stock-installer.py` assembles the matching Sipeed
offline package from completed platform outputs. See
[stock installation](../docs/STOCK-INSTALL.md) for requirements, state
transitions, recovery and the build.

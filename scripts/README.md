# NanoKVM OS build scripts

The complete SD card image is built by [platform/build.sh](../platform/README.md).
It calls these component builders:

| Script | Builds |
|---|---|
| build-mpi.sh, build-isp-vendor.py, build-mpi-bin.py | the SOPHGO media libraries |
| build-mmf.py, build-capture.py | `libkvm_mmf.so` and `libkvm.so` |
| build-system.py | `kvm_system` |
| build-server-existing-libs.py | `NanoKVM-Server`, `nkos-update` and `nkos-apply-updates` |
| build-busybox-devmem.sh, build-usb-audio.py | `devmem` and `usb-audio-capture` |
| build-qhd-edid.py, build-monitor-edids.py, build-portrait-edid.py | the EDID profiles |
| edid-rgb-range.py | lets the source send full-range RGB: the last step of the EDID profiles |
| build-alpine-packages.sh | the thirteen APK packages, inside the Alpine builder tree |
| build-alpine-sd-image.py | the SD card image |

Run them through `platform/build.sh`: it prepares their pinned sources and toolchain.
The update format is described in [docs/UPDATES.md](../docs/UPDATES.md).
The retired Docker builder and stock NanoKVM tarball/manifest release scripts
are available in Git history; current releases use signed Alpine APKs.
Buildroot is limited to the platform compiler, host tools and boot initramfs;
the previous Buildroot OS and private addon-manager build paths were removed.

## Alpine port tooling

The Alpine port keeps normal APK installation in the system root and packages
the board-specific kernel, modules, firmware, OpenRC integration and application
separately. The main entry points are:

| Script | Purpose |
|---|---|
| build-alpine-packages.sh | Build the signed NanoKVM APK repository from exported payloads. |
| build-alpine-update-bundle.sh | Package a root and normal FIT into the tested recovery bundle. |
| build-alpine-recovery-fit.py | Build recovery bound to the root and normal-FIT hashes. |

See firmware/alpine/README.md for architecture and the physical recovery path.

## Make targets

The root Makefile delegates to the same pinned platform builder:

```sh
make platform PLATFORM_ARGS=-d       # complete image with local test keys
make release-build                  # build components and write their hashes
make package PLATFORM_ARGS='-k path/to/key.rsa -e path/to/key.key'
```

`OUT` and `JOBS` select the output directory and build parallelism. Component
aliases (`app`, `support`, `vision`) use the prepared platform toolchain and
sources. `make web` builds `web/dist` for frontend development. Release versions
come from `firmware/alpine/release.env`, not a separate Makefile variable.

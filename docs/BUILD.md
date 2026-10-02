# Building NanoKVM OS components

This source snapshot includes project modifications and dependency pins, not a ready-made SDK/sysroot. Full SD-image reproduction still needs the external source/toolchain and static board assets. Do not interpret a component build as a device qualification.

## Web interface

Use Node.js 22 or newer and pnpm 11 or newer:

```sh
cd web
pnpm install --frozen-lockfile
pnpm build
```

The result is `web/dist`. Keep `pnpm-lock.yaml`; do not refresh dependencies unintentionally.

## Application and updater

The server needs the matching 19 native SG2002 libraries and the Buildroot musl RISC-V toolchain. Its Go runtime includes the changes in `firmware/cpu/sysmon-runtime`; building with ordinary Go does not reproduce the selected scheduler policy. Follow that directory's preparation recipe and source pin.

```sh
python3 scripts/build-server-existing-libs.py \
  --libraries /absolute/native/dl_lib \
  --buildroot-output /absolute/buildroot-output \
  --go /absolute/selected-go/bin/go \
  --output /absolute/new-server-output
```

This builds the server, the static `nkos-update` and `nkos-apply-updates` helpers and a hash manifest. Preserve the source's local Pion SRTP replacement in `server/third_party/pion-srtp`. `nanokvm-base` installs `nkos-update` as `/usr/sbin/nkos-update`; the server runs it to perform GUI package and software transactions detached from the web server.

From `server/`, the update code can be tested on the host without device access:

```sh
CGO_ENABLED=0 go test -tags teststub ./osupdate ./cmd/nkos-update
```

See [UPDATES.md](UPDATES.md) for the native APK update flow. Development builds need their own APK signing key if the maintainer key is unavailable; a different public key produces a different trust domain.

## Kernel, modules and boot images

`platform/build.sh` builds the toolchain, Linux kernel, all kernel modules, U-Boot, `fip.bin`, initramfs and `boot.sd` images from the pinned inputs in `platform/sources.lock` and checks the result; see [platform/README.md](../platform/README.md). `firmware/buildroot/` contains the Buildroot external tree and source patches it uses, including the pinned OpenVPN 3 Core adapter in `firmware/vpn/` and WireGuard tools. OpenVPN 2 remains a system CLI; the GUI selects OpenVPN 3 Core. `firmware/sources.json` records the source pins of the native media libraries; MPI changes are in `firmware/mpi/`.

## Full images

Several image staging tools still expect retained stock board assets under `build/release/nanokvm_2.6.0`. Those assets, proprietary vendor objects, full vendor source trees, compiler/sysroot and release archives are not embedded in this Git repository. A self-contained downloadable build-input bundle and final corresponding-source notices remain to be consolidated before a public binary release. The current application/web can be built against an existing matched native/toolchain set; a turnkey clean-machine full-image build is not claimed.

When staging a fresh OS image, pass the application version (for example `--version 1.0.0-beta.11`) to `stage-enhanced-app.py`. Beta image assembly requires all four monitor EDID profiles.

The previous upstream Docker/dev-container recipe used an older native toolchain and SDK and has been removed. It must not be treated as a reproducible NanoKVM OS build.

## USB audio helper

Build `native/usb-audio/capture.c` with `scripts/build-usb-audio.py`, using its pinned tinyalsa and Opus sources. Stage the helper as `system/bin/usb-audio-capture` with the license/source notices under `system/share/usb-audio`. The full-image hook installs these paths explicitly.

## Dependency maintenance

See [DEPENDENCY-UPDATES.md](DEPENDENCY-UPDATES.md) for automatic dependency PRs, native upstream monitoring and the boundaries of each.

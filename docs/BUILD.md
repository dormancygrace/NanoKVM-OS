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

This builds the server, the static `nkos-update` helper and a hash manifest. Preserve the source's local Pion SRTP replacement in `server/third_party/pion-srtp`. The helper must be installed in the system image as `/usr/sbin/nkos-update`, with `S94nanokvm-update` before `S95nanokvm`.

From `server/`, bounded updater checks can run on the host without device access:

```sh
CGO_ENABLED=0 go test ./osupdate ./cmd/nkos-update ./cmd/nkos-package
```

See [UPDATES.md](UPDATES.md) for signed package creation. Development builds need their own matched trust key if the maintainer signing key is unavailable; changing the public key produces a different trust domain.

## Kernel, drivers and full images

`firmware/sources.json` records external source pins. `firmware/buildroot/` contains the external tree and source patches, including the pinned OpenVPN 3 Core adapter in `firmware/vpn/` and WireGuard tools. OpenVPN 2 remains a system CLI; the GUI selects OpenVPN 3 Core. Kernel configuration and port patches are in `firmware/kernel/`; media-driver changes are in `firmware/osdrv/` and MPI changes in `firmware/mpi/`. The cumulative kernel patch/config in `firmware/release/source-components/kernel/` captures the selected Linux 7.2.4 kernel source state; its recipe records the compiler, ISA flags and build timestamp; do not apply both cumulative and incremental patches on top of each other.

`build-enhanced-kernel.sh`, `build-enhanced-release-modules.py`, board staging and image assembly tools require explicit external paths and matched artifacts. CryptoDMA source under `firmware/crypto/experimental/sg2002-aes-probe` remains the selected beta module prerequisite despite its historical directory name. Small-core FreeRTOS/AliOS experiments are excluded.

Several image staging tools still expect retained stock board assets under `build/release/nanokvm_2.6.0`. Those assets, proprietary vendor objects, full vendor source trees, compiler/sysroot and release archives are not embedded in this Git repository. A self-contained downloadable build-input bundle and final corresponding-source notices remain to be consolidated before a public binary release. The current application/web can be built against an existing matched native/toolchain set; a turnkey clean-machine full-image build is not claimed.

When staging a fresh OS image, pass `--version 1.0.0-beta.2 --update-sequence 4` to `stage-enhanced-app.py` for the current application. Use the actual signed release sequence for future versions. Beta image assembly now requires this metadata, the independent updater/recovery scripts and all four monitor EDID profiles.

The previous upstream Docker/dev-container recipe used an older native toolchain and SDK and has been removed. It must not be treated as a reproducible NanoKVM OS build.

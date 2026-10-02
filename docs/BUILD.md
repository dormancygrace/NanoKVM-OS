# Building NanoKVM OS

`platform/build.sh` builds the complete SD card image of NanoKVM OS from this repository and the inputs pinned in `platform/sources.lock`:

- the toolchain, Linux kernel and modules, U-Boot, `fip.bin` and the boot images;
- the SOPHGO media libraries, the board service, the server and the web UI;
- the six `nanokvm-*` APK packages, the Alpine root file system and the image.

Host requirements, the steps, the outputs and how they are checked are described in [platform/README.md](../platform/README.md). Buildroot (`firmware/buildroot/configs/nanokvm_platform_defconfig`) builds only the toolchain, host tools and initramfs utilities; the installed OS and optional software are Alpine APKs.

## Working on one component

The web UI and the server can be developed without the full build. The web UI needs Node.js 24 LTS and pnpm 11 or newer (CI uses pnpm 12); the root `.nvmrc` selects Node 24, so with nvm run `nvm install` and `nvm use` from the repository root:

```sh
cd web && pnpm install --frozen-lockfile && pnpm dev
```

```sh
cd server && CGO_ENABLED=0 go test -tags teststub ./...
```

A release-equivalent server needs the NanoKVM Go runtime (`firmware/cpu/sysmon-runtime`) and the native libraries of the same build; `platform/build.sh native server` builds both. See [UPDATES.md](UPDATES.md) for the native APK update flow.

## Dependency maintenance

See [DEPENDENCY-UPDATES.md](DEPENDENCY-UPDATES.md) for automatic dependency PRs, native upstream monitoring and the boundaries of each.

The `BUILD-*.md` files next to this one describe how earlier releases were built. Their scripts have been replaced by `platform/build.sh`.

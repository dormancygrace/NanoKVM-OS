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


## NanoKVM CPU build policy

`platform/cpu-profile.json` defines the CPU policy for our own C/C++ components.
`scripts/nanokvm_cpu_profile.py` supplies the flags to the native library, board
service, USB audio, CGO and platform builders. Production optimization is `-O2`,
with C906 scheduling and `-mno-fence-tso`. Official Alpine packages and prebuilt
SOPHGO algorithm objects are not recompiled by this policy; Go retains its own
compiler and `GORISCV64` baseline.

Userspace enables the C906 T-Head extensions and XTheadVector. This permits
supported instructions; it does not promise automatic vectorization or a frame
rate increase. Kernel/modules retain their qualified integer ISA and `lp64`,
without compiler-generated floating-point or vector operations. U-Boot uses a
separate integer-only C906 profile with `CONFIG_CC_OPTIMIZE_FOR_SPEED=y`. Kbuild's
architecture-specific optimized routines retain their own context handling.

Buildroot's target defaults and the MPI Makefile patch mirror the profile so
standalone builds do not fall back to a different optimization level. Update
those generated values alongside the JSON; `scripts/test-cpu-profile.py` rejects
drift. It can also exercise the real cross-compiler with `--compiler PATH`.
Component output directories contain `cpu-profile*.json` with the policy hash,
compiler identity and flags used by the build script. These files describe only
our compiled objects, not the flags originally used for vendor binary objects.
The `BUILD-*.md` files next to this one describe how earlier releases were built. Their scripts have been replaced by `platform/build.sh`.

# Building NanoKVM OS

`platform/build.sh` builds the complete SD card image of NanoKVM OS from this repository and the inputs pinned in `platform/sources.lock`:

- the toolchain, Linux kernel and modules, U-Boot, `fip.bin` and the boot images;
- the SOPHGO media libraries, the board service, the server and the web UI;
- the eighteen core NanoKVM APK packages, the Alpine root file system and the image.

Host requirements, the steps, the outputs and how they are checked are described in [platform/README.md](../platform/README.md). Buildroot (`firmware/buildroot/configs/nanokvm_platform_defconfig`) builds only the toolchain, host tools and initramfs utilities; the installed OS and optional software are Alpine APKs.

The root Makefile uses the same builder: `make platform PLATFORM_ARGS=-d`
creates a local test image, and `make help` lists the component targets.
`OUT` and `JOBS` override the output directory and build parallelism.

## Working on one component

The web UI can be developed without the full build. The web UI needs Node.js 26 LTS and pnpm 11 or newer (CI uses pnpm 12); the root `.nvmrc` selects Node 26, so with nvm run `nvm install` and `nvm use` from the repository root:

```sh
cd web && pnpm install --frozen-lockfile && pnpm dev
```

The server (`server-cpp/`, C++23, CMake) links the native libraries, OpenSSL and zlib-ng of the same build: `platform/build.sh native tools server` builds them and then the server with the libraries pinned in `platform/sources.lock` (`server-cpp/build.sh`). See [UPDATES.md](UPDATES.md) for the native APK update flow.

## Dependency maintenance

See [DEPENDENCY-UPDATES.md](DEPENDENCY-UPDATES.md) for automatic dependency PRs, native upstream monitoring and the boundaries of each.

## NanoKVM CPU build policy

`platform/cpu-profile.json` defines the CPU policy for our own C/C++ components.
`scripts/nanokvm_cpu_profile.py` supplies the flags to the native library, board
service, USB audio, server and platform builders. Source-owned userspace uses
`-O2`; kernel and modules use `-O3` without LTO. Both use C906 scheduling
and `-mno-fence-tso`. Official Alpine packages and prebuilt
SOPHGO algorithm objects are not recompiled by this policy.

Userspace enables the C906 T-Head extensions and XTheadVector. This permits
supported instructions; it does not promise automatic vectorization or a frame
rate increase. Kernel/modules use the integer ISA and `lp64`,
without compiler-generated floating-point or vector operations. U-Boot uses a
separate integer-only C906 profile with `CONFIG_CC_OPTIMIZE_FOR_SPEED=y`. Kbuild's
architecture-specific optimized routines retain their own context handling.

Buildroot's target defaults and the MPI Makefile patch mirror the profile so
standalone builds do not fall back to a different optimization level. Update
those generated values alongside the JSON.
Component output directories contain `cpu-profile*.json` with the policy hash,
compiler identity and flags used by the build script. These files describe only
our compiled objects, not the flags originally used for vendor binary objects.

The policy also covers the standalone EDID, board-probe and devmem tools. A
builder records its compiler and supplied flags in `cpu-profile*.json`; conflicting
optimization, ISA, tuning or ABI options are rejected. This validates the options
supplied by the builder, not every option subsequently added by an upstream
Makefile.

To inspect the effective compiler commands after a kernel or module build, run
`scripts/audit-kbuild-profile.py OUTPUT/kernel/build` (or `OUTPUT/modules`).
For U-Boot, use `--kind bootloader OUTPUT/uboot/build`. The audit reads Kbuild's
saved commands and checks the final optimization, ISA, ABI and tuning options.
The vDSO and CFI-vDSO retain Kbuild's explicit userspace ABI instruction sets;
these are reported separately, while still requiring the kernel optimization level (`-O3`) and C906 tuning.

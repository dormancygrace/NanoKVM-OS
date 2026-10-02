# Source and redistribution status

NanoKVM OS retains component-specific upstream licenses. The root GPL-3.0 license covers the application derivative; it does not relicense vendor firmware or every library in the SD image.

## Included materials

- Application/native modifications, local Pion forks and retained license notices.
- Pinned kernel, driver, MPI, Buildroot and runtime sources/patches.
- The [platform build](../platform/README.md): toolchain, kernel, all modules, U-Boot, FIP, initramfs and boot images from pinned sources, verified against the released kernel and modules, and the per-release corresponding-source archive and written offer.
- Collected Buildroot target-package license texts and package/source manifest in `firmware/release/licenses/`.

## Incomplete materials for a full binary distribution

- The pinned AIC8800 SDIO firmware package and SG2002 codec firmware package have no license-file declaration in their Buildroot recipes. Inclusion in a public upstream repository alone is not treated as a redistribution grant; their precise firmware terms still need confirmation. The pinned upstream trees are [AIC firmware](https://github.com/lxowalle/aic8800-sdio-firmware/tree/c56f910044cc854d6c553bcb9a644f3bca5a4c38) and [codec firmware](https://github.com/0x754C/sg2002_codec_fw/tree/1e339b782642ce1b2c8aa81f9fdef212912a6a83).
- Some ISP/3A algorithm objects are vendor supplied. Their hashes/provenance are retained, but the snapshot does not claim those objects are fully open source.
- The Git tree is not a self-contained corresponding-source bundle for every full-image component. External SDK trees, static board assets, compiler/sysroot and consolidated source archives remain required, as explained in [BUILD.md](BUILD.md). The kernel, modules and boot images are covered by the [platform build](../platform/README.md).

Opening source code and making all attached binaries publicly downloadable are separate distribution decisions. This page records outstanding materials rather than asserting that a build or source pin completes every distribution requirement.

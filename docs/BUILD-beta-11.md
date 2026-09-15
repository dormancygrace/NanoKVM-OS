# Building beta-11 sequence 27

Version `1.0.0-beta.11`; GitHub tag `v1.0.0-beta.11`. Only the full SD image is released.
Exact image and input hashes are in `firmware/release/beta11/build-manifest.json`.

The release keeps the accepted -O2 userspace, OpenSSL 4.0.2, kernel
`7.2.5-nanokvm-os-r3`, 92 matched modules, bootloader and normal initramfs.
The server includes the bound-gadget WoL filter. Native `libkvm.so` includes
the variadic debug formatting fix. Web includes OLED latest-value queuing,
DNS layout and opt-in USB Serial resize/initial Enter.

For the base kernel, apply the complete
`firmware/release/beta8/linux-7.2.5-nanokvm-os-r2.patch` once to Linux 7.2.5,
then apply the incremental `firmware/release/beta11/hdd-led-dts.patch`.
Use `firmware/release/beta11/kernel.config`. Do not also apply the individual
kernel patches over the complete patch. The beta-11 FIT changes only the DTB
relative to the accepted beta-10 kernel/initramfs; reuse matched binaries when
assembling from those accepted inputs. See [beta-10 build](BUILD-beta-10.md)
for the refreshed base and external driver prerequisites.

Build native capture and server using the enhanced build scripts and the
matched native libraries/sysroot and patched Go runtime. Build Web with the
frozen lockfile, TypeScript and Vite. The release assembly scripts under
`firmware/release/beta11/` use exact input hashes and accepted-input paths;
they are not a turnkey downloader for missing third-party build prerequisites.

Stage a pristine beta-10 rootfs with `stage-rootfs.sh` / `prepare-rootfs.py`,
providing the accepted server, capture library, board stage and combined Web.
Then run `build-artifacts.sh` with `NANOKVM_BUILD_BASE`,
`NANOKVM_RELEASE_OUTPUT` and `SOURCE_DATE_EPOCH` from the release manifest.
The output is the image ZIP, SHA256SUMS and a build manifest; no package is signed.

Refer to [general build prerequisites](BUILD.md) and
[distribution status](DISTRIBUTION.md) for external source/native inputs and
the limits of this curated source snapshot. Hardware checks are documented
in [acceptance](BETA11-ACCEPTANCE.md).

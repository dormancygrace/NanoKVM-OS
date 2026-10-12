# Linux 7.2.10 and build cleanup — 2026-10-12

## Source

Linux is pinned to upstream stable tag `v7.2.10`, commit
`7b20d05fd38de8e0be168956f6fd241d750c7d69`.
The kernel.org archive SHA-256 is
`2feaa70453e91f541be6a5c9078d8168e081af45a3d9718054850538cff2ebf3`;
the downloaded archive matched the kernel.org checksum list.
The installed module ABI is `7.2.10-nanokvm-os-r1`.

Both source inventories and the platform builder select the same archive.
The fixed kernel timestamp is the upstream release time. `olddefconfig`
adds only `CONFIG_CC_OPT_INLINE_MEMSET="-finline-stringops=memset"` for
GCC 16.2; existing options are retained, including MGLRU and Netfilter flow
offload. Kernel/modules retain the integer T-Head `-O3`, no-LTO profile.

## Verification

Initial component checks passed using the existing qualified GCC 16.2 /
Binutils 2.47 compiler and current U-Boot 2026.10 `mkimage`:

- Kernel and all 82 modules built, including 17 external modules. Every
  module reports `7.2.10-nanokvm-os-r1`; depmod found no unresolved symbols.
- The compiler-command audit passed for 2,133 kernel and 382 external-module
  C commands. Kbuild's explicit vDSO/CFI instruction-set overrides are retained.
- All 25 board/memory FIT variants built below the 16 MiB boot-image limit.
  DTB memory sizes, CMA/fixed properties and reserved-memory entries passed;
  on-device composition reproduced each original FIT byte for byte.
- CPU-profile, kernel activation, SD-image boot layout, Alpine network/service,
  board selection, platform-build and upstream-watch host tests passed.
  All root Makefile test targets passed; help and build-target dry runs passed.
- Clean Buildroot source patching and configuration passed: 14 target and
  49 host/virtual entries, without the retired OS package graph.
- PR #103's frozen web install, production build, lint and all 198 tests passed
  on Node 24 before its merge. The full build includes its dependency lockfile.

A fresh Buildroot toolchain and every platform component subsequently built
successfully. The fresh compiler reproduced the initially checked kernel Image
byte for byte. All compiler audits, the FIP layout/payload checks, OpenSBI
reservations and all 25 FIT composition checks passed again on the fresh outputs.

The strict output manifest now covers 590 files. Its previous baseline predates
merged memory-profile and dependency changes: 218 outputs remain identical,
65 existing paths change, 307 paths are added and 255 retired paths disappear.
The additions/removals are the renamed kernel module directory, expanded boot
profiles and content-addressed web bundles. Other changes are the kernel,
U-Boot/FIP, generated boot metadata, Go applications, system service and devmem.
Native media libraries and firmware blobs remain identical. The independent
PR #103 web build is byte-identical to the 201-file platform web output.

Seven signed APKs, version `2.6_alpha2-r0`, and Image `v1.1-a2` with applications
`v2.6-a2` were built. Both the RSA and ECDSA keys independently verify the v3
package index. The image ZIP passes its CRC test. Its root partition remains
768 MiB; the full 832 MiB image compresses to 68,068,876 bytes.

The external vendor modules emit compiler warnings, mainly missing prototypes;
this update does not hide or claim to fix them. The initial build recorded
1,075 warning lines and no depmod errors.

The first full-image installation passed the complete SD read-back hash check.
UART confirmed U-Boot 2026.10, OpenSBI 1.9 and Linux 7.2.10. First boot then
exposed an exfatprogs 1.4.1 change: mkfs refuses an old filesystem signature on
a reused card. Storage initialization failed, preventing dependent services
from starting. The marker-gated firstboot formatter now explicitly uses `-F`.
A loop-disk regression with the exact image's exfatprogs passes fresh allocation,
a normal second boot that preserves user files, and a reflash that removes the
partition table entry but leaves the previous exFAT signature in place.

The one-off host installer also failed to preserve the absence of the SSH-stop
flag. Its restoration procedure was corrected, and UART access recovered the
first installation without changing the user's password.

The corrected full image was then written in full and its SD read-back hash
matched `ba561bb513678874fd014c5ac0be06fd493916741fc4217530156d0ea635a5c2`.
Firstboot formatting and all default OpenRC services succeeded. SSH, Wi-Fi and
the existing WireGuard connection returned; the root password and 102 saved-file
hashes matched. The two USB-function markers were intentionally removed to keep
the gadget disabled. RustDesk 0.5.3-r2 was reinstalled from the signed repository
with its settings intact and service stopped. Root uses about 202 MiB of its
768 MiB partition; the data filesystem is empty apart from filesystem metadata.

Chrome Main displayed the actual desktop in FHD WebRTC H.264 and Direct H.264
at about 60 fps, and FHD MJPEG 4:2:2 at about 41 fps. QHD initially failed with
the restored legacy manual HDMI-detection mode (`hdmi_mode=2`). A bounded A/B
check using the preserved 7.2.9 kernel/modules and then 7.2.10, with identical
userspace and the new U-Boot, displayed QHD H.264 on both at about 59 fps.
After returning HDMI detection to automatic (`hdmi_mode=0`), live FHD-to-QHD
switching also succeeded without rebooting. QHD WebRTC H.264 and Direct H.264
both displayed the desktop at about 60 fps. This comparison did not reproduce
a 7.2.10-specific video regression. HEVC playback was not qualified: Chrome
reported it unsupported. These are bounded functional checks, not a long soak
or an exhaustive board/codec matrix.

A separate MJPEG problem was reproduced independently of the kernel: the
five-second per-frame HTTP write deadline stayed armed during deduplication's
five-second idle interval. HTTP/2 then reset the stream with `INTERNAL_ERROR`.
The server now clears the deadline after a successful flush, while retaining
bounded writes and error propagation. A real TLS HTTP/2 regression test fails
on the previous implementation and passes with the fix; the MJPEG race tests,
all server tests and `go vet` pass.

The updated signed application APK was installed on the already-flashed device.
Chrome Main then displayed QHD MJPEG 4:2:2 over HTTP/2 on one sustained request,
including over a minute of a mostly static desktop with continued data and no
reset or reconnection. The inactive Chrome tab briefly rendered black in
automation screenshots; bringing it to the foreground immediately painted the
correct desktop without resetting the stream. A direct API capture also decoded
as 2560x1440. Rebuilding
the 12 media modules without the new inline-memset flag produced byte-identical
modules; those diagnostic copies were not deployed.

The final full image includes this server fix; its SHA-256 is
`b525cee901de2ab0b8a98af7466d473d99d93fe6e3bc40441c0d6fa1434c11a0`.
The final image was assembled and CRC checked, while device qualification used
the complete image installation above followed by the signed application update.
All seven APK signatures, the RSA legacy index, and both RSA and ECDSA signatures
of the v3 index verify after the final rebuild.

The temporary 7.2.9 module tree was removed after returning to 7.2.10. MGLRU
is enabled, the running QHD profile has 68 MiB CMA, and nf_flow_table loads.
No kernel panic, Oops, OOM-kill or lockup was found in the final dmesg. Vendor
VPSS buffer-pressure messages remain visible during capture; this bounded check
does not establish that they are absent or harmless in every workload.
The USB gadget finishes disabled. USB internet forwarding through the managed
computer was not exercised, per the user's instruction. Exactly the current
and two previous boot-log archives are retained; no test backup is left on SD.

## Repository cleanup

The root Makefile now calls `platform/build.sh` and the signed APK pipeline.
The removed Docker/stock-tarball scripts depended on a Dockerfile and release
workflows that no longer exist. Their history remains available in Git.

Removed the already-integrated RustDesk handoff patch and the obsolete duplicate
video-encoder guide. Build docs now describe actual optimization levels and APK
ownership of native libraries and the board service. The validation index marks
old beta records as historical instead of advertising beta-13 as current.
Historical release records, license material and the optional C906 experiment
remain available; they are not active build instructions.

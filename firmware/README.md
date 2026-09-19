# NanoKVM Enhanced

This is an active port, not a release image. The target is the latest available
stable system with all NanoKVM hardware functions retained and qualified. The
old working 5.10/GCC 10.2 setup is a comparison and recovery baseline only.

The Alpine distribution port is a separate active delivery path documented in
firmware/alpine/README.md and docs/development/alpine-port.md. Its physical
device evidence uses the newer tested 7.2.5-nanokvm-os-r4 kernel; the 7.2.3
status below belongs to this earlier Enhanced workstream.

## Target and source provenance

Exact source pins are in `sources.json`. As verified on 2026-09-05, the target is
Buildroot 2026.08, Linux 7.2.3, GCC 16.2, and the newest accessible Xuantie tools.
The vendor documents Xuantie 3.4.1/GCC 14.3; its exact downloadable build/source
is still being located. GitHub 3.0.1 is GCC 14.1.1, not the newest vendor release.
Latest Sophgo osdrv and cvi_mpi source snapshots are from 2026-08-24.

The final work includes: current system and application dependencies; the new
kernel, T-Head support, boot firmware; rebuilt open media libraries/drivers and
explicitly tracked vendor algorithm objects;
H.264/H.265 Direct and WebRTC; USB HID/composition/serial/network/storage;
Ethernet, Wi-Fi, SD, indicators and board services. Documentation, rollback,
reproducible artifacts and local commits are required. No push or UAC requests.

## Application dependency selection

Needed MaixCDK basic/peripheral components are restored at 4.11.3. The user
explicitly asked to retain useful components and exercise engineering judgment.
OpenCV and Maix vision are not needed by the current native capture path; the
exploratory OpenCV checkout is not built or selected. Reconsider optional parts
only for a demonstrated function, rather than applying an absolute library ban.

The NanoKVM capture library now builds from its own MMF capture/frame and Linux
I2C code, without MaixCDK or OpenCV. The unused legacy vision component remains
in the source tree; it is no longer a dependency of the kvm component. JPEG,
frame sampling and direct H.264/H.265 code paths are retained in the new code.
The full board application/rootfs still need rebuilding and hardware testing.
Checking the final dependency graph, link inputs and rootfs for OpenCV remains
required; shared-library dependencies alone cannot detect static linkage.

## Building the toolchain

Use a Linux filesystem, including WSL ext4 (not /mnt/c), and run:

```sh
JOBS=12 scripts/build-enhanced.sh toolchain
```

The default scratch directory is `build/enhanced` (ignored by Git). Override
`NANOKVM_BUILD_DIR`, `NANOKVM_BUILDROOT_DIR`, or `NANOKVM_BUILDROOT_OUTPUT` for
existing source/cache/output directories. `all` additionally requires
`NANOKVM_APP_STAGE` and `NANOKVM_BOARD_ASSETS`; these must hold the application
rebuilt for the new toolchain and the new kernel/modules. The post-build hook
rejects a 5.10 module set for Enhanced. The source lists and dependency versions
can be obtained with the `show-info` and `legal-info` targets.

`buildroot/configs/nanokvm_sg2002_defconfig` is the earlier compatibility
experiment using GCC 15.3 and the existing kernel ABI. Its output is not the
Enhanced target and must not be presented as completion of this port.

## T-Head requirements and measured findings

- Linux 7.2.3 includes XTheadVector and T-Head memory/cache errata options. They
  are enabled in the provisional kernel configuration; board support and driver
  integration remain to be completed.
- Actual C906 runtime probes compiled with GCC 15.3 and GCC 16.2 and
  `-march=rv64gc_xtheadvector -mtune=thead-c906 -mno-fence-tso` passed 4099 vector
  results, including the tail. Its assembly contains `th.vsetvli`, `th.vle.v`,
  `th.vmul.vx`, `th.vadd.vv`, and `th.vse.v`. Source is in `probes/thead-vector.c`.
  This proves those instructions work, not complete vendor-media compatibility.
- Generic RVV 1.0 must not be enabled on this C906. OpenSSL's standard-vector
  dispatch is disabled in the current Buildroot integration.
- Default GCC runtime libraries contain `fence.tso`, which raises SIGILL on our current NanoKVM hardware/firmware
  (see [direct runtime probe](toolchain/fence-tso.md)). The Enhanced wrapper includes `-mno-fence-tso`. Buildroot's GCC
  runtime build invokes xgcc without that wrapper, so `external.mk` propagates
  the flags explicitly into both C and C++ target-runtime builds. The ELF
  instruction audit passed after rebuilding them: no fence.tso in libstdc++ or
  libgomp. Incremental builds also need gcc-final-reinstall to copy the rebuilt
  host toolchain runtimes into staging and target; otherwise stale copies remain.
- GCC 16.2 static runtime probes passed on the device without reboot: C++
  async/future/acquire-release atomics and correct waitpid/ECHILD errno, plus
  4099 XTheadVector results. Sources are in probes/. This does not qualify
  the new kernel, media stack or complete root filesystem.
- Current vendor musl has a reproduced waitpid/ECHILD errno bug. Enhanced uses
  fresh upstream musl; its final application must use the generic loader rather
  than inheriting that old vendor libc.

## Kernel/media port status

Linux 7.2.3 Image and modules build successfully with the Enhanced GCC 16.2
toolchain. The kernel release is `7.2.3-nanokvm-enhanced`. The provisional
SG2002 board DTB also compiles. These are build results, not boot qualification.

Ordered patches in `kernel/patches/` restore the vendor ION carveout ABI, port
its lifetime and DMA/cache interfaces, add the efuse compatibility interface,
and introduce the provisional board description. Factory efuse programming
has not been executed or qualified. Ordered patches in `osdrv/patches/` port
sys, base, CIF, VI, VPSS, VPU, JPEG, common codec, IVE and DWA/GDC; all ten pass compilation and strict MODPOST against the
actual new kernel Module.symvers. CIF also fixes named resource mapping,
initialization order and propagation of missing clocks/IRQs and probe errors.

VI also passes strict MODPOST. Its port removes unused profiling headers,
uses current timer/proc/class APIs, makes private helpers explicit, checks the
previously ignored copy_from_user result and preserves FIFO priority 90 using
the exported scheduler API. Failed thread creation/scheduling clears ownership.
VI probe/remove unwind and timer shutdown still require lifecycle review before
hardware loading; a successful compile does not qualify those paths.

IVE uses current proc/class/remove APIs, exact vendor ioctl argument sizes,
serialized requests, checked resize-array copies and checked startup allocation.
The startup probe now waits by polling and checks both processing stages before
publishing the device. CPU fault-injection tests pass on the host and C906,
without exercising hardware. See `osdrv/ive-port.md` for remaining runtime gates.
DWA/GDC also passes strict compilation/MODPOST with current proc/remove APIs,
checked worker creation and preserved FIFO priority 50. Its runtime and remaining
probe/remove gates are recorded in `osdrv/dwa-port.md`.

The remaining board/media drivers and complete cvi_mpi integration,
board clocks/PHY/SDIO and boot firmware remain unfinished. Build the kernel and
the ten currently ported modules using scripts/build-enhanced-kernel.sh after
applying the patches as described below.

System package compilation completed. A stale working .config had retained the
optional vendor-libc compatibility package; reapplying the Enhanced defconfig
correctly disables it. The experimental target directory still contains those
old named loaders. Use a fresh Buildroot output for a release build; changing
Kconfig does not uninstall previously installed package files.

Old Sipeed SDK has binary-only soph_vc_driver and soph_jpeg modules. The latest
Sophgo osdrv contains source code for those modules, allowing a real forward
port instead of trying to load old module binaries. The media ABI, allocator,
cache maintenance, clocks, device tree, and module lifecycle need qualification.

The kernel configuration currently starts from upstream riscv defconfig and is
provisional. It is not yet a minimal or boot-tested NanoKVM configuration.
No Enhanced kernel/image has been booted or flashed. Device configuration and
its working H.265 installation are preserved.

## Sources

- https://buildroot.org/download.html
- https://gcc.gnu.org/releases.html
- https://www.kernel.org/
- https://gcc.gnu.org/onlinedocs/gcc/RISC-V-Options.html
- https://support.xrvm.cn/software/linux-sdk/release-notes
- https://github.com/sophgo/linux/wiki
- https://github.com/sophgo/osdrv/tree/sg200x-dev
- https://github.com/sophgo/cvi_mpi/tree/sg200x-dev

## Reproducing the current kernel compile milestone

Extract the exact Linux tarball from sources.json into a clean Linux filesystem
directory and check its SHA-256 before extraction. Apply kernel/patches/*.patch
in filename order with `patch -p1`. Check out the pinned sophgo_osdrv commit
from sources.json in a separate clean clone and apply osdrv/patches/*.patch in
filename order there. Do not apply these to an already patched working tree.
The current patch series is specific to those source pins.

```sh
NANOKVM_KERNEL_SOURCE=/path/to/patched/linux-7.2.3 \
NANOKVM_OSDRV_SOURCE=/path/to/patched/osdrv \
NANOKVM_BUILDROOT_OUTPUT=/path/to/enhanced/buildroot-output \
NANOKVM_KERNEL_OUTPUT=/path/to/kernel-output \
scripts/build-enhanced-kernel.sh
```

This builds Image, DTB, kernel modules and sys/base/CIF/VI/VPSS/VPU/JPEG/common-codec only. It neither creates
a complete bootable image nor installs anything on the device. Later modules
must pass strict MODPOST with their real dependency symbol tables; missing
symbols must not be downgraded to warnings.

## VPSS and codec hardware compile milestone

VPSS passes strict MODPOST with CONFIG_RGN_EX=0 (the vendor default). The disabled
RGN_EX command-queue DMA path is not qualified and still requires conversion
from legacy cache operations with a correct DMA mapping. Do not enable it based
on this build result. VPSS region handle prototypes now preserve the full 32-bit
layer value. Worker allocation failure clears the error pointer before further
use; propagation through initialization/resume still needs lifecycle review.

VPU and JPEG hardware modules pass strict MODPOST. VPU's void mutex-lock API now
actually guarantees acquisition instead of ignoring an interrupted wait. JPEG
uses the vendor JDI polling path (800–1200 microsecond sleeps); its disabled IRQ
handler was never registered, so the erroneous free_irq on removal was removed.
An interrupt-driven JPEG path is a later measured candidate, not enabled here.
Unused Arm Streamline annotations were removed; the existing driver diagnostic
logging is retained. The common codec module now compiles, with the runtime limits below.

## Common codec compile milestone

The full `cvi_vc_driver.ko` links and passes strict MODPOST against the new kernel
and the real sys/base/VPU/JPEG exports. No missing-symbol or compiler-warning
suppression was added. Disassembly of all eight ported modules contains no
`fence.tso`. This check does not substitute for instruction/ABI qualification on
C906 or real encoding tests. The codec module is approximately 1.5 MiB.

The port makes private helpers explicit and shares feeder/diagnostic prototypes.
The ES feeder rewind implementation now matches its caller's signature. Optional
JPEG test-only YUV loading stays under VC_DRIVER_TEST; that configuration has not
been qualified. Obsolete proc/class/platform/scheduler/file APIs were updated.
Existing RR priorities (95 for bind workers, 99 for SBM) are retained; scheduler
failure is reported. Failed thread allocations no longer leave those error
pointers for later reuse; complete startup/teardown rollback still needs review.

Packet update/release paths now use mandatory mutex acquisition rather than
ignoring an interruptible wait. Existing callers that check interruption retain
that behavior. The JPEG lock API likewise guarantees acquisition for its existing
callers. SEI insertion rechecks queue capacity after allocation, before indexing
the packet array. JPEG input-buffer validation checks pFrame[0], not the always
non-null array address. Floating-point rounding state is initialized.

VDI file wrappers use kernel_read/kernel_write without removed set_fs/get_fs.
They validate fopen modes, truncate w files, check multiplication and arguments,
return zero rather than converting negative I/O errors into huge size_t values,
track EOF and reset it on a successful seek. The legacy byte-count convention is
retained for fread/fwrite. Actual filesystem/error-path tests on the new kernel
are pending. Hardware video, cache ownership, DMA lifetime, channel concurrency,
module remove/error unwind, and complete board integration remain unqualified.

## Core MPI compile milestone

Apply mpi/patches in order to the pinned cvi_mpi source. Set
NANOKVM_MPI_SOURCE, NANOKVM_OSDRV_SOURCE, NANOKVM_KERNEL_SOURCE and
NANOKVM_BUILDROOT_OUTPUT, then run scripts/build-enhanced-mpi.sh. It builds
SYS/VI/VPSS/VO/RGN/GDC/VENC/VDEC/MISC/IVE static and shared libraries for CV181X
with GCC16.2 and fresh musl. It checks that headers select CV181X without also
selecting CV180X. The compiler triple now receives explicit C906/XTheadVector
flags; generic RVV 1.0 is not selected. The GDC BufWrap getter now copies its
result before freeing the ioctl structure and validates the output pointers.

All ten libraries compile; none contain fence.tso. Their combined dynamic
symbol set plus fresh musl still requires CVI_ISP_GetVDTimeOut from ISP. This is
not a complete runnable media stack or proof of correct runtime symbol scope.
ISP/BIN/3A integration and the application rebuild are pending.

The public cvi_mpi source pin supplies ISP algorithms and parts of 3A as object
files without their C source. The first full ISP attempt fails because
isp_algo/src/dpcm_api.c is absent from the Git tree. Do not silently treat those
objects as newly GCC16-built source. The latest vendor objects and an open HDMI
path need qualification. The reference project scpcom/sophgo-middleware at
cd8bb74c94e18afc109ed5ba2c584f8c0508f36b has JSON/miniz source forks and a NanoKVM
ISP-light approach; it has only been inspected, not merged into Enhanced.
Its dummy camera algorithms must not replace required behavior without proving
that the HDMI capture path does not use it. Current firmware source pins stay
unchanged. The JSON/miniz forks also need a latest-version audit before adoption.


## Open ISP and vendor algorithm milestone

The open CV181X ISP and common BIN library now compile with GCC16.2. The MLSC
packing path uses the current __riscv_ intrinsics with XTheadVector (not RVV1.0).
It also avoids reading the absent second sample at the edge of the 37-column
mesh. firmware/probes/build-isp-mlsc-probe.py extracts the actual patched function
and compares vector/scalar packing. On the C906 device, 256 patterns over all
three 37x37 channel arrays pass byte-for-byte, with source/output ends against
PROT_NONE pages. Removing just the new load guard causes SIGSEGV (exit139).
This standalone fresh-musl probe ran on the original 5.10.4 kernel; it does not
qualify ISP hardware or the new kernel. Existing video service was not changed.

scripts/build-enhanced-isp-vendor.py verifies all 53 pinned vendor object hashes,
checks their names against the Makefile source list, and relinks four libraries
(ISP algorithms, AE, AWB, AF) before building open ISP. Set the same NANOKVM_*
variables as for core MPI, then run it with python3. No ISA/ABI attributes are
removed. Fresh output archives avoid stale members from another toolchain.
These closed objects retain their original Xuantie GCC10.2 provenance; they are
not GCC16 source builds. They are retained to preserve vendor behavior while a
fully open HDMI-specific alternative remains unqualified.

All 16 shared libraries currently built contain zero fence.tso instructions.
Their aggregate dynamic exports plus fresh musl cover ISP and 3A references;
libcvi_bin still has 48 unresolved names from ISP_BIN and CVI JSON/miniz. This is
an aggregate symbol inventory, not an RTLD_NOW test or ABI compatibility proof.
The ISP_BIN build stops because vendor JSON/miniz prebuilt archives do not exist
for the new compiler triple. They must be built from maintained source with
CVI compatibility, rather than copying the old archives into a renamed folder.


## Upstream JSON/miniz and loader runtime milestone

The BIN build now uses json-c 0.19 and miniz 3.1.2 from their exact upstream
release commits in sources.json. A small compile-time compatibility header maps
CVI JSON names to current upstream declarations; the array-aware get_ex2 helper
remains in MPI. No old fork archives or old struct definitions are copied into
the new build. miniz's three compression APIs are mapped to mz_ equivalents.
These adapters cover the internal BIN sources, not a promise of compatibility
for arbitrary external users of the former cvi_json-c library.

After applying MPI patches, run scripts/build-enhanced-mpi-bin.py with the four
core NANOKVM_* variables, plus NANOKVM_JSON_C_SOURCE, NANOKVM_MINIZ_SOURCE and a
dedicated NANOKVM_MPI_THIRDPARTY_OUTPUT. It verifies both upstream Git pins and
rejects tracked source edits. Both dependencies build with GCC16.2/C906 flags
and PIC. Their licenses are installed under the private stage/share/licenses.
Fresh temporary object/archive directories avoid stale members; explicit make
targets bypass the vendor third-party extraction race. MPI warning-as-error
checks and normal platform defines remain enabled. System CMake 3.28.3 was used.

The first actual loader/JSON test exposed a Sophgo bug: CVI_U64_JSON clamps reads
to CVI_U32_MAX. Patch 0003 changes the limit to CVI_U64_MAX, preserving round trips
through UINT64_MAX. scripts/build-enhanced-mpi-probe.py packages all 17 libraries,
a fresh libc and probes/mpi-load-json.c. Use the same environment plus a dedicated
NANOKVM_MPI_PROBE_OUTPUT. Invoke the copied loader explicitly:

    ./libc.so --library-path . ./mpi-load-json-probe

All 19 executable/library file hashes matched after copying to the device.
With the corrected helper, 256 rounds passed: CVI U64/S32/U16 limits, the vendor
array lookup helper, UTF-8/ANSI/embedded-NUL string preservation, compression
round trips and rejection of a corrupt checksum. The musl loader resolved all
17 DSOs. The only observed constructor/destructor entries were standard GCC
frame_dummy/__do_global_dtors_aux in libsys; hardware initialization APIs were
not called. Aggregate symbol/disassembly audit also passed: no unresolved strong
symbols with these DSOs plus fresh musl, and no fence.tso. Evidence is in
mpi/bin-runtime-audit.json and mpi/bin-runtime-manifest.json.

This ran on the existing 5.10.4 kernel in an isolated /tmp directory, without
replacing the running video service. It qualifies loading and the tested data
paths, not hardware ISP algorithms, complete PQ-bin import/export, video, or
Linux 7.2.3. The application still needs rebuilding against this MPI split.
The Buildroot json-c package remains 0.18 and needs alignment if selected; this
milestone updates the private MPI dependency build. New board drivers, boot
firmware, final clean rootfs/image assembly, rollback and hardware tests remain.

Upstream release references: https://github.com/json-c/json-c/releases/tag/json-c-0.19-20260627
and https://github.com/richgel999/miniz/releases/tag/3.1.2 (checked 2026-09-05).


## MMF wrapper and capture configuration milestone

scripts/build-enhanced-mmf.py builds 20 current source files into libkvm_mmf.so:
our C++ wrapper, current MPI sample/common, pinned SensorSupportList LT6911
and sensor list, upstream inih r62, and the NanoKVM capture-size adapter.
Use NANOKVM_MPI_SOURCE, NANOKVM_SENSOR_SOURCE, NANOKVM_INIH_SOURCE,
NANOKVM_BUILDROOT_OUTPUT and a dedicated NANOKVM_MMF_OUTPUT. Apply both MPI
and sensor patch sets first. No old MaixCDK middleware libraries are used by
this standalone build. Its -Wall/-Wextra/-Werror compilation and -z defs link
pass. All 17 MPI shared dependencies are explicit; unused libstdc++/libgcc_s
are not added to this C-compatible wrapper's runtime dependencies.

The wrapper no longer guesses ION/VB ownership from debug/proc output, forcibly
releases arbitrary physical buffers, or unloads/reloads hard-coded soph_ modules.
It uses the existing SYS/VB lifecycle and its frame/channel teardown APIs.
The obsolete generated global_config include was unused and removed. C++
zero-initialization and calloc argument order now pass current diagnostics.
The optional unused RGB-to-NV21 helper is explicitly marked maybe_unused.
Crash recovery and interactions with the boot-time media owner still need real
initialization/teardown tests; removing unsafe recovery is not proof of those.

Current MPI's LT6911 defaults to fixed1080p. Under NANOKVM_ENHANCED, it now maps
to PIC_CUSTOMIZE and reads /run/nanokvm/{width,height}, preserving NanoKVM's
existing dynamic capture contract. The new parser bounds reads/conversions,
rejects malformed and partial pairs without updating output, and uses1080p only
when both files are absent. Values are bounded to the16-bit sensor-mode fields;
this is not a promise every such dimension fits hardware or memory. The two
runtime files are still separately written, so atomic pair publication remains
to be addressed in the vision producer. Size errors propagate through sensor
attribute setup and VI device startup rather than using uninitialized values.

LT6911 RX previously indexed mode0/image1 instead of the selected mode/image0.
The sensor patch fixes that and uses the configured I2C bus. Enhanced defaults
match the observed NanoKVM configuration: bus4, MIPI0, lanes2/4/3/1/0, no polarity
swap. Image-mode callbacks now preserve the requested geometry. The upstream
7-bit0x2b address is retained; the old MaixCDK sensor adapter used0x56 and ignored
probe failures, while the working vision code uses0x2b. Do not copy that old
behavior or its devmem pinmux shell calls into the port. The device is LT6911UXC;
the upstream sensor's0x1605 ID probe still needs variant/ownership integration
with the vision layer before any hardware initialization test.

probes/mmf-config.c exercises the actual loaded sensor callbacks without ISP or
I2C initialization. On C906/old5.10.4, remote hashes matched and these passed:
size-file error cases, currentinih parsing, LT6911→PIC_CUSTOMIZE mapping, real
sensor image/RX callbacks at640x480/1280x720/1920x1080/2560x1440, lane defaults,
and read-only parsing of the real sensor_cfg.ini and1920x1080 runtime geometry.
The library has no fence.tso. Evidence is mpi/mmf-runtime-audit.json. The running
video service was not replaced. The full vision/system/application build and
new-kernel video qualification remain pending. The old MaixCDK CMake path is
still present for the comparison build; Enhanced uses the standalone script.

Sensor source reference: https://github.com/sophgo/SensorSupportList/tree/e2824e1d07f516c41238e123fefbab71a34843bc
and currentinih release: https://github.com/benhoyt/inih/releases/tag/r62.

## Native capture build and limited runtime qualification

Run `scripts/build-enhanced-capture.py` after the MMF build, with
`NANOKVM_BUILDROOT_OUTPUT`, `NANOKVM_MPI_SOURCE`, `NANOKVM_MMF_OUTPUT` and
`NANOKVM_CAPTURE_OUTPUT`. It builds all four kvm sources with GCC16.2, C906
flags and `-Wall -Wextra -Werror`, then links `libkvm.so` with `-z defs`.
Compiler dependency files and the link map reject OpenCV/MaixCDK inputs.
The board service is separate and still needs its new-toolchain build.

`build-enhanced-capture-probes.py` builds and packages two probes, the real
capture/MMF/MPI libraries and current compiler runtimes. It audits all 25 ELF
artifacts for fence.tso and records their SHA256 hashes. Execute probes with
the packaged musl loader (`./libc.so --library-path . ./capture-load`, and
similarly `capture-contract` and `mmf-config-probe`). The contract probe uses
an intentionally mocked MMF provider to exercise actual Capture/Frame/Sampler
code; it is not an encoder, ISP or DMA test. It also passed host ASan/UBSan.

On the actual C906, all 25 hashes matched and all three probes passed. The
real loader probe verifies public API symbols, no MMF initialization, no new
open device descriptors and no signal-handler replacement on library load.
MMF orientation bounds and closed-channel rejection use the real wrapper.
The contract probe covers first-black-frame/change detection, different luma
and chroma strides, plane gaps, VI lease ownership, JPEG buffer ownership and
injected initialization/encoding failures. The unchanged LT6911 configuration
probe verifies actual public callbacks and parsing without I2C/ISP startup.
Audit: `mpi/capture-runtime-audit.json`; raw output: `mpi/capture-runtime.log`.

New capture objects have no hardware constructor. Explicit initialization
owns the process MMF lifetime; restarting clears encoder initialization state
and reapplies automatic output resolution. Capture errors propagate to reads.
The CPU NV21 view validates contiguous physical planes and actual strides before
mapping/copying; unsupported layouts fail instead of being interpreted as packed
pixels. Native padded output falls back to the requested visible width. I2C
reads use bounded caller buffers and reject failed/short transactions before
inspecting data. Bridge bank sequences and variant-specific ownership still
need full integration/qualification, including higher-level EDID failure paths.

Device remains on Linux5.10.4 with unchanged boot ID. No video service replaced,
no new-kernel boot, no claim of hardware H.265/JPEG success for this build. Full
new-kernel/driver lifecycle, cached encoder copy buffers, board services, LT6911UXC
probe integration and atomic geometry publication remain to be completed.

## Encoder frame buffers: ownership and cache fix

The CPU copy paths for H.264/H.265 and JPEG now use
`kvm_mmf/include/internal/frame_buffer.hpp`. Allocated VB frames retain the
original block handle and the exact single mapping address/length. Failure to
obtain a physical address or mapping releases the block; teardown unmaps once
using the original mapping, rather than unmapping individual plane pointers.
Virtual plane offsets match aligned physical addresses, including plane gaps.

The helper validates all source/destination planes before writing, handles Y
and VU strides separately, fills neutral chroma for grayscale, and flushes each
written plane before VENC/VPSS submission. Cache errors propagate and prevent
submission. Fresh allocation clears padding. Mapped VI fallback copies use
actual plane lengths/strides rather than assuming packed source data. Unknown
noncontiguous layouts are rejected by this mapping interface.

JPEG copy implementations are consolidated and check initialization failures.
Video native-frame lookup matches the data pointer as well as dimensions and
tracks the VI lease through successful fallback submission. Busy/uninitialized
video channels reject new submissions instead of silently overwriting state.

`frame-buffer-contract.cpp` exercises these real helpers with a CPU-backed CVI
memory provider, including null/MAP_FAILED allocation, plane alignment gaps,
NV21/RGB/grayscale, source/destination stride differences, flush failure, and
the actual vendor layout calculator for 16x16, 1280x720, 1366x768 and 1920x1080.
It passes host ASan/UBSan and actual C906 execution. This is not proof that
physical cache maintenance or hardware encoding works on the new kernel.
The capture probe package now has 26 verified ELF artifacts; all lack fence.tso.
Real-library loading, capture contracts and sensor configuration probes also
pass with the updated MMF. Latest evidence is `mpi/frame-buffer-runtime-audit.json`
and `mpi/frame-buffer-runtime.txt`; earlier capture audits are historical.

Still pending: JPEG partial-initialization cleanup at every vendor API failure,
complete VI/VPSS/VENC teardown qualification, physical DMA/cache experiments,
LT6911UXC integration and new-kernel video boot. The working service was not
replaced and the old kernel boot ID is unchanged.

## Updated MaixCDK board service

MaixCDK 4.11.3 basic/peripheral components now build into the Enhanced board
service with GCC 16.2. C906 self-tests pass; production board operation remains
pending. See `maixcdk/README.md` for pins, patches, build commands and evidence.

## Boot staging and recovery

See boot/README.md and its audit. The owner requests manual Boot-button recovery,
without automatic rollback. The current loader can be retained for initial
new-kernel qualification; its FIT input address needs a verified layout change.
New upstream boot firmware is downloaded only, not built or installed.

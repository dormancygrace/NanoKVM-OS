# Accelerators

The SG2002 has engines beside the CPU:

- the IVE, for image comparisons and filters;
- the TPU, for neural networks;
- DWA, for dewarp and rotation (no driver is built);
- RGN, for overlays;
- the AXI monitors in front of the DDR controller;
- the true random number generator, which U-Boot reads to seed Linux ([Random numbers](#random-numbers)).

NanoKVM OS builds and installs drivers for the others, but loads them only when something uses them. MJPEG uses the IVE to send only the parts of the screen that changed ([Video](VIDEO.md#mjpeg-changed-tiles-only)); the server loads its driver for the first tiled viewer. All numbers on this page are from a NanoKVM PCIe with a 1920 × 1080 input at 90 Hz.

## Loading

The device tree describes every engine. Without a rule, udev would load their drivers at boot by device-tree alias. `/etc/modprobe.d/nanokvm-accelerators.conf` blacklists them, which stops only that automatic load, so an idle device holds no memory and has no device nodes. A user loads a driver by name:

| Module | Device |
|---|---|
| `cv181x_ive` | `/dev/cvi-ive` |
| `cv181x_tpu` | `/dev/cvi-tpu0` |
| `cv181x_rgn` | `/dev/cvi-rgn` |
| `cv181x_aximon` | `/proc/ddr_bandwidth` |

For example, `modprobe cv181x_tpu`. The media modules load first at boot (`nanokvm-modules`), so their dependencies are already present.

## Memory

The IVE and the TPU take their buffers from the reusable CMA region that video uses (see [Video memory](VIDEO.md#video-memory)):

- the IVE through the media allocator;
- the TPU as dma-buf buffers from `/dev/dma_heap/ion`. The kernel builds the CMA dma-buf heap (`CONFIG_DMABUF_HEAPS_CMA`), so every named CMA region is a heap.

Nothing is reserved for either while it is idle.

## IVE

`cv181x_ive` (`/dev/cvi-ive`) handles frame difference, SAD, thresholds, filters and DMA copies.

Measured: SAD in 16 × 16 blocks, bit-exact against the CPU.

| Picture | Time |
|---|---|
| 480 × 272 | 0.42 ms |
| 960 × 544 | 1.4 ms |
| 1280 × 720 | 2.5 ms |
| 1920 × 1080 | 5.5 ms |

Measured at 1920 × 1080 with the tiled MJPEG layout: SAD over the 67 whole block rows about 8 ms, the 16-row strip below them under 1 ms, a DMA copy of the whole luma plane about 6 ms. SAD in 16 × 16 blocks compares whole block rows only (rows 1072-1079 of a 1080-high picture are left out) and writes one value per block: its outputs need only the block grid's size.

**An operation longer than 4 ms used to fail while it ran.** An operation that sleeps until the engine's interrupt gave up after one tick (`msecs_to_jiffies(1)`, 4 ms at HZ=250), and patch 0012 then reset the engine in the middle of its writes: a full HD SAD returned wrong maps, and the SoC hung twice on the stand. Osdrv patch 0020 waits as long as the polling path (100 ms), as the read-only module parameter `timeout_ms`; NanoKVM OS uses full-size IVE operations only with a driver that has it.

**A 3840 × 2160 operation hangs the engine.** Before osdrv patch 0012, one such operation left every later two-input operation reading its second image from a stale address, with wrong results and no error, until the board restarted. The patch resets the engine after an operation that times out. The vendor user library does not report IVE errors, so callers must check sizes themselves.

## TPU

The TPU runs neural networks: about 0.5 TOPS INT8, and BF16 too.

**Driver.** `cv181x-tpu` (`/dev/cvi-tpu0`) is part of the kernel since r6. It is Armbian's mainline port of the vendor driver (kernel patches 0047 and 0048, from armbian/build#10760). Patch 0047 also carries our changes: DMA mappings are retained until queued jobs finish, and device initialization failures are checked. The TPU has no IOMMU. The driver takes dma-buf buffers and gives the runtime their bus address, and its character-device interface is that of the vendor runtime.

**Runtime.** Sophgo's cviruntime, ported to dma-buf heaps, is linked into the programs of the core package `nanokvm-tpu`: `nanokvm-ocr` (text on the screen) and `nanokvm-screen` (the screen-crash classifier). Their models are add-ons; see [tpu/README.md](../tpu/README.md). Like the other SOPHGO components without a licence file, its source is fetched at build time ([DISTRIBUTION.md](DISTRIBUTION.md)).

**Measured:**

| Model | Time | CMA |
|---|---|---|
| yolov8n, 640 × 640, INT8 | 34.5 ms | 7.7 MB |
| yolov5s | 69 ms | 16.8 MB |

Either takes about 5 % of a core. 1080p90 H.265 keeps 90 fps while yolov5s runs without pause.

## DWA and RGN

DWA (rotation, mirroring and lens correction) and RGN (overlays and masks in the encoded picture) are the dewarp and region engines. Only `cv181x_rgn` is built: nothing in NanoKVM OS rotates or dewarps the picture, and the `dwa` module is not in the osdrv list of `platform/build.sh`. A feature that needs DWA adds it back there.

The media library in the image does not open their devices: `CVI_SYS_Init` skips them (cvi_mpi patch 0012). A feature that uses them loads the module and enables them in that patch.

## DDR bandwidth

The AXI monitors in front of the DDR controller count the bytes read and written per port. `cv181x_aximon`, a small driver of our own, exposes them in `/proc/ddr_bandwidth`:

- first line: the time in nanoseconds since sampling started;
- then one line per port, with the bytes read and written.

Two reads a few seconds apart give the rate. The register layout is that of Sophgo's osdrv `mon` driver, whose own module only prints to the console.

| Port | Masters |
|---|---|
| `vip_rt` | HDMI capture (VI) |
| `vip_off` | VPSS (offline video processing) |
| `cpu` | the CPU |
| `tpu` | the TPU |
| `vc` | the H.264/H.265 and JPEG encoders |
| `hsperi` | high-speed peripherals |

Measured at 1920 × 1080, 90 Hz:

| Load | DDR traffic |
|---|---|
| Idle, no viewer | 373 MB/s, the capture writing 1920 × 1080 × 2 bytes × 90 |
| H.265 at 90 fps to one viewer, through VPSS | 1.6 GB/s (VPSS 653 MB/s, encoder 519 MB/s, CPU 58 MB/s) |
| The same, plus yolov5s on the TPU | 2.3 GB/s (TPU 697 MB/s); the video kept 89.5 fps |

## Random numbers

The TRNG of the security subsystem (SG2002 TRM, section 22.2) seeds a DRBG from its noise source and returns 128 bits per command. Linux has no driver for it: U-Boot reads it once per boot (`platform/uboot/0010-rng-cv181x-trng.patch`) and puts two values into `/chosen` before it starts Linux:

- `rng-seed`, 32 bytes: the 256 bits the kernel needs to count its CRNG as seeded. Linux credits a boot loader's seed by default (`random.trust_bootloader`), so `random: crng init done` is printed at 0.000000, before any driver runs; without it, about 2 to 4 s. From then on the kernel reseeds from interrupt timing and CPU jitter, as on any board without a hardware generator;
- `kaslr-seed`, 8 bytes, for a kernel built with `RANDOMIZE_BASE`.

Linux clears both properties once it has read them. If the TRNG fails in U-Boot, U-Boot prints `No RNG device` or `dm_rng_read failed` and boots as before, without the seeds.

The registers are those of the Synopsys NIST TRNG core, which the TRM documents only in part. A second seed on a running generator is an illegal command sequence for that core: it raises an alarm, and the core then ignores every command; a zeroize with an alarm pending hung the bus. The driver therefore clears the alarms and the status, zeroizes the core, instantiates it from fresh noise, checks the alarms after every command, and zeroizes it again after its read. Every wait has a timeout. `tests/test-trng.py` runs that code against a model with alarms and timeouts.

The block sits behind the efuse clock gates. With the gates off, any access to its registers hangs the bus, and the board restarts through the watchdog. U-Boot's device tree node names both clocks, so the driver enables them before it touches a register; Linux turns them off again at boot as unused. Do not read its registers with `devmem`.

Checked on the NanoKVM PCIe stand: three boots, `crng init done` at 0.000000 each time; with a `RANDOMIZE_BASE` kernel, `_text` at a different address each boot. The Linux driver this replaces, with the same command sequence, read 1 MiB without an alarm across 64 seeds, and gzip -9 did not compress its output.

## CryptoDMA

Unlike the engines above, the CryptoDMA engine (AES, SM4, DES, SHA) is in use: its driver, `nanokvm_hwcrypto`, loads at boot unless `/etc/kvm/disable_hardware_aes` exists. Kernel users of these algorithms, such as IPsec or OpenVPN data channel offload with AES-GCM, reach it through the kernel Crypto API; the server's SRTP uses the driver's own device.

There is no `/dev/crypto`: the cryptodev module was removed in 4.0.0, since nothing in NanoKVM OS used it and Alpine's OpenSSL has no devcrypto engine. Each request through it cost an `ioctl`, so it was faster than the CPU only for large blocks. Measured on the stand through an OpenSSL devcrypto engine (Linux 7.2), AES-256-CBC took 1 KiB at 14 MB/s and 8 KiB at 47 MB/s against about 8 MB/s in software, and blocks of 16–256 bytes were 7–25 times slower than software.

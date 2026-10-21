# Accelerators

The SG2002 has engines beside the CPU:

- the IVE, for image comparisons and filters;
- the TPU, for neural networks;
- DWA, for dewarp and rotation;
- RGN, for overlays;
- the AXI monitors in front of the DDR controller;
- the true random number generator.

NanoKVM OS builds and installs their drivers, but loads them only when something uses them. None of NanoKVM OS's own features use them yet. All numbers on this page are from a NanoKVM PCIe with a 1920 × 1080 input at 90 Hz.

## Loading

The device tree describes every engine. Without a rule, udev would load their drivers at boot by device-tree alias. `/etc/modprobe.d/nanokvm-accelerators.conf` blacklists them, which stops only that automatic load, so an idle device holds no memory and has no device nodes. A user loads a driver by name:

| Module | Device |
|---|---|
| `cv181x_ive` | `/dev/cvi-ive` |
| `cv181x_tpu` | `/dev/cvi-tpu0` |
| `cv181x_dwa` | `/dev/cvi-dwa` |
| `cv181x_rgn` | `/dev/cvi-rgn` |
| `cv181x_aximon` | `/proc/ddr_bandwidth` |
| `cv181x_trng` | `/dev/hwrng` |

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

**A 3840 × 2160 operation hangs the engine.** Before osdrv patch 0012, one such operation left every later two-input operation reading its second image from a stale address, with wrong results and no error, until the board restarted. The patch resets the engine after an operation that times out. The vendor user library does not report IVE errors, so callers must check sizes themselves.

## TPU

The TPU runs neural networks: about 0.5 TOPS INT8, and BF16 too.

**Driver.** `cv181x-tpu` (`/dev/cvi-tpu0`) is part of the kernel since r6. It is Armbian's mainline port of the vendor driver (kernel patches 0047 and 0048, from armbian/build#10760), with patch 0049 retaining DMA mappings until queued jobs finish and checking device initialization failures. The TPU has no IOMMU. The driver takes dma-buf buffers and gives the runtime their bus address, and its character-device interface is that of the vendor runtime.

**Runtime.** The runtime that loads models (Sophgo cviruntime) is not part of the image, because Sophgo publishes it without a licence.

**Measured:**

| Model | Time | CMA |
|---|---|---|
| yolov8n, 640 × 640, INT8 | 34.5 ms | 7.7 MB |
| yolov5s | 69 ms | 16.8 MB |

Either takes about 5 % of a core. 1080p90 H.265 keeps 90 fps while yolov5s runs without pause.

## DWA and RGN

`cv181x_dwa` and `cv181x_rgn` are the dewarp and region engines:

- DWA: rotation, mirroring and lens correction;
- RGN: overlays and masks in the encoded picture.

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

The TRNG of the security subsystem (SG2002 TRM, section 22.2) seeds a DRBG from its noise source and returns 128 bits per command. `cv181x_trng`, a small driver of our own, registers it with the kernel's hw_random core as `/dev/hwrng`:

- it seeds on load, then again after every 1024 outputs (16 KiB), and zeroizes the generator when it is unloaded;
- `/sys/class/misc/hw_random/rng_current` names `cv181x-trng` while it is loaded;
- use blocking reads for fresh output. An `O_NONBLOCK` read uses only bytes already buffered by hw_random, or returns `EAGAIN`; it does not wait for generation or reseeding.

The registers are those of the Synopsys NIST TRNG core, which the TRM documents only in part. A second seed on a running generator is an illegal command sequence for that core: it raises an alarm, and the core then ignores every command. Each seed therefore clears the alarms, zeroizes the core and instantiates it from fresh noise.

The block sits behind the efuse clock gates, which Linux turns off when nothing uses them. With the gates off, any access to its registers hangs the bus, and the board restarts through the watchdog. The device tree node names both clocks, so the driver enables them before it touches a register. Do not read its registers with `devmem` while the module is not loaded.

While the driver is loaded, the hw_random core also feeds the kernel's entropy pool from it, with the full quality the core assumes for a device that names none. `/sys/class/misc/hw_random/rng_quality` lowers that. Nothing in NanoKVM OS loads the module or reads `/dev/hwrng`; the kernel's own random number generator does not need it.

Checked on the stand (Linux 7.3):
- 1 MiB read in 3.6 s, across 64 seeds;
- 256 KiB does not compress (gzip -9 output is larger than its input);
- three unloads and loads in a row, each followed by new output;
- the efuse clocks are on only while the module is loaded.

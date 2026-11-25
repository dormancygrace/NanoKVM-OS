# Accelerators

The SG2002 has engines beside the CPU:

| Engine | Purpose | Used by NanoKVM OS |
|---|---|---|
| [IVE](#ive) | image comparisons, filters, DMA copies | MJPEG changed-tiles detection |
| [TPU](#tpu) | neural networks | on-screen text reading, screen-crash classifier |
| [DWA, RGN](#dwa-and-rgn) | dewarp and rotation; overlays | not used |
| [AXI monitors](#ddr-bandwidth) | DDR traffic per port | on demand (`/proc/ddr_bandwidth`) |
| [TRNG](#random-numbers) | random numbers | U-Boot, to seed Linux |
| [Small core](#small-core) | Opus audio encoding | USB speaker audio |
| [CryptoDMA](#cryptodma) | AES, SHA-1, SHA-256 | kernel crypto API, WebRTC SRTP and DTLS, OpenSSL |

Performance figures on this page are for a 1920 × 1080 input at 90 Hz.

## Loading

The device tree describes every engine, so udev would load their drivers at boot by device-tree alias. `/etc/modprobe.d/nanokvm-accelerators.conf` blacklists them; this stops only the automatic load. An idle engine therefore holds no memory and has no device node. An explicit `modprobe` by name loads a driver:

| Module | Device |
|---|---|
| `cv181x_ive` | `/dev/cvi-ive` |
| `cv181x_tpu` | `/dev/cvi-tpu0` |
| `cv181x_dwa` | `/dev/cvi-dwa` |
| `cv181x_rgn` | `/dev/cvi-rgn` |
| `cv181x_aximon` | `/proc/ddr_bandwidth` |

For example, `modprobe cv181x_tpu`. The media modules load first at boot (`nanokvm-modules`), so the dependencies are already present. The server loads `cv181x_ive` for the first tiled MJPEG viewer ([Video](VIDEO.md#mjpeg-changed-tiles-only)) and `cv181x_tpu` on first use of the TPU.

The CryptoDMA driver is different: it loads at boot (see [CryptoDMA](#cryptodma)).

## Memory

The IVE and the TPU take their buffers from the reusable CMA region that video uses (see [Video memory](VIDEO.md#video-memory)):

- the IVE through the media allocator;
- the TPU as dma-buf buffers from `/dev/dma_heap/ion`. The kernel builds the CMA dma-buf heap (`CONFIG_DMABUF_HEAPS_CMA`), so every named CMA region is a heap.

Nothing is reserved for either while it is idle.

## IVE

`cv181x_ive` (`/dev/cvi-ive`) runs frame difference, SAD, thresholds, filters and DMA copies. MJPEG uses SAD in 16 × 16 blocks to find the tiles that changed; the results are bit-exact against the CPU.

SAD in 16 × 16 blocks:

| Picture | Time |
|---|---|
| 480 × 272 | 0.42 ms |
| 960 × 544 | 1.4 ms |
| 1280 × 720 | 2.5 ms |
| 1920 × 1080 | 5.5 ms |

With the tiled MJPEG layout at 1920 × 1080, SAD over the 67 whole block rows takes about 8 ms, the 16-row strip below them under 1 ms, and a DMA copy of the whole luma plane about 6 ms. SAD compares whole block rows only (rows 1072–1079 of a 1080-high picture are left out) and writes one value per block, so its output buffer needs only the block grid's size.

**Module parameter.** `timeout_ms` (read-only, default 100) is how long an operation may run when its caller sleeps until the engine's interrupt; it equals the polling path's limit. User space reads it to recognise a driver that waits long enough: NanoKVM OS uses full-size IVE operations only when the parameter is present.

**Failure behaviour.** An operation that times out resets the engine, so later operations work again. A 3840 × 2160 operation hangs the engine and ends in that reset. The vendor user library does not report IVE errors, so callers must check picture sizes themselves.

## TPU

The TPU runs neural networks: about 0.5 TOPS INT8, and BF16.

**Driver.** `cv181x_tpu` (`/dev/cvi-tpu0`) is built with the kernel. It is Armbian's mainline port of the vendor driver (kernel patches 0047 and 0048), with DMA mappings kept until queued jobs finish and device initialisation failures checked. The TPU has no IOMMU: the driver takes dma-buf buffers and gives the runtime their bus address. Its character-device interface is that of the vendor runtime.

**Runtime.** Sophgo's cviruntime, ported to dma-buf heaps, is linked into the programs of the core package `nanokvm-tpu`: `nanokvm-ocr` (text on the screen) and `nanokvm-screen` (the screen-crash classifier). Their models are add-ons; see [tpu/README.md](../tpu/README.md). Like the other SOPHGO components without a licence file, its source is fetched at build time ([DISTRIBUTION.md](DISTRIBUTION.md)). Without `/dev/cvi-tpu0` the runtime aborts, so the server loads the driver before it starts a model.

**Performance:**

| Model | Time | CMA |
|---|---|---|
| yolov8n, 640 × 640, INT8 | 34.5 ms | 7.7 MB |
| yolov5s | 69 ms | 16.8 MB |

Either takes about 5 % of a CPU core. 1080p90 H.265 keeps 90 fps while yolov5s runs continuously.

## DWA and RGN

`cv181x_dwa` and `cv181x_rgn` are the dewarp and region engines:

- DWA: rotation, mirroring and lens correction;
- RGN: overlays and masks in the encoded picture.

`CVI_SYS_Init` does not open their devices (cvi_mpi patch 0012). DWA is unused.

RGN draws the [watermark](VIDEO.md#watermark). The server loads `cv181x_rgn` when a watermark is on, and the region library opens `/dev/cvi-rgn` on its first call. A region is drawn by the VPSS as it writes an output, so it exists only on VPSS outputs, not on frames taken straight from capture. Each watermark block gets an ARGB8888 canvas in video memory (4 bytes a pixel) and a region handle on every output (16 + 8 × output + block). The VPSS draws up to eight regions per layer on an output, in two layers; regions of one layer must not overlap.

## DDR bandwidth

The AXI monitors in front of the DDR controller count the bytes read and written per port. `cv181x_aximon` exposes them in `/proc/ddr_bandwidth`:

- first line: the time in nanoseconds since sampling started;
- then one line per port, with the bytes read and written.

Two reads a few seconds apart give the rate. The register layout is that of Sophgo's osdrv `mon` driver.

| Port | Masters |
|---|---|
| `vip_rt` | HDMI capture (VI) |
| `vip_off` | VPSS (offline video processing) |
| `cpu` | the CPU |
| `tpu` | the TPU |
| `vc` | the H.264/H.265 and JPEG encoders |
| `hsperi` | high-speed peripherals |

Typical traffic at 1920 × 1080, 90 Hz:

| Load | DDR traffic |
|---|---|
| Idle, no viewer | 373 MB/s, the capture writing 1920 × 1080 × 2 bytes × 90 |
| H.265 at 90 fps to one viewer, through VPSS | 1.6 GB/s (VPSS 653 MB/s, encoder 519 MB/s, CPU 58 MB/s) |
| The same, plus yolov5s on the TPU | 2.3 GB/s (TPU 697 MB/s); the video keeps 89.5 fps |

## Random numbers

The TRNG of the security subsystem (SG2002 TRM, section 22.2) seeds a DRBG from its noise source and returns 128 bits per command. Linux has no driver for it. U-Boot reads it once per boot (`platform/uboot/0010-rng-cv181x-trng.patch`) and puts two values into `/chosen` before it starts Linux:

- `rng-seed`, 32 bytes: the 256 bits the kernel needs to count its CRNG as seeded. Linux credits a boot loader's seed by default (`random.trust_bootloader`), so `random: crng init done` appears at 0.000000, before any driver runs (without the seed, after about 2 to 4 s). From then on the kernel reseeds from interrupt timing and CPU jitter;
- `kaslr-seed`, 8 bytes, for a kernel built with `RANDOMIZE_BASE`.

Linux clears both properties once it has read them.

**Failure behaviour.** If the TRNG fails, U-Boot prints `No RNG device` or `dm_rng_read failed` and boots without the seeds. Every wait of the U-Boot driver has a timeout, and it checks the core's alarms after every command; a core in alarm state ignores commands, and reseeding a running generator raises an alarm, so the driver zeroizes and re-instantiates the core for each read.

**Do not read its registers with `devmem`.** The block sits behind the efuse clock gates, which Linux turns off at boot as unused. With the gates off, any access to its registers hangs the bus and the board restarts through the watchdog.

## CryptoDMA

The CryptoDMA engine runs AES (ECB, CBC, CTR; 128-, 192- and 256-bit keys), SHA-1 and SHA-256. Its driver, `nanokvm_hwcrypto`, loads at boot (`/etc/init.d/S94hwcrypto`) unless `/etc/kvm/disable_hardware_aes` exists; if it does not load, the application uses software AES. The engine also implements DES, 3DES, SM4 and Base64; the driver does not offer them.

There is no `/dev/crypto`; use the kernel crypto API or the hwcrypto ioctls.

### Kernel crypto API

The driver registers these algorithms (driver names `nanokvm-*`):

| Algorithm | Priority |
|---|---|
| `ecb(aes)`, `cbc(aes)`, `ctr(aes)` | 300 |
| `sha1`, `sha256` | 300 |
| `gcm(aes)`, `ccm(aes)`, `cmac(aes)` | 400 |

Kernel users such as IPsec and OpenVPN data channel offload reach the engine through them. For GCM the engine runs AES-CTR while the CPU computes GHASH of the previous or next piece, so a request takes about the time of its GHASH plus the part the engine runs alone.

**Callers that cannot sleep.** AES requests (ECB, CBC, CTR, GCM, CCM, CMAC) from softirq, such as OpenVPN's kernel data channel, run on the engine at once with their own set of DMA memory; the SHA hashes from such callers go through a workqueue. AES requests too short for the engine go to the kernel's AES code for the C906 (0.6 µs per block with a 128-bit key; the engine costs about 4.5 µs before its first byte). They go to software up to:

| Mode | 128-bit key | 192-bit key | 256-bit key |
|---|---:|---:|---:|
| ECB | 112 bytes | 96 bytes | 96 bytes |
| CTR | 144 bytes | 128 bytes | 112 bytes |
| GCM text | 96 bytes | 80 bytes | 64 bytes |

The kernel has no software `cbc(aes)`, so CBC requests use the engine at any length.

**Request size.** The driver cuts a request from a caller that may sleep into pieces of 64 KiB (block ciphers, CCM, CMAC, hashes); GCM takes pieces of 16 KiB on the caller's memory and 4 KiB through the driver's buffers; atomic callers use 4 KiB pieces.

### Zero-copy requests

For AES in ECB, CTR and CBC decryption, the engine works directly on the caller's memory instead of the driver copying the data in and out: the driver maps the caller's pages (pinning them for a process) and gives the engine one chain of descriptors. This applies to:

| Path | From |
|---|---|
| `ecb(aes)`, `cbc(aes)` (decryption), `ctr(aes)` | 192 bytes |
| `gcm(aes)` | 1 KiB (4 KiB for a caller that cannot sleep) |
| `HWCRYPTO_GCM` | 8 KiB |
| `HWCRYPTO_RUN` with AES | 16 KiB |

A request qualifies when every range starts on a 16-byte boundary and is a whole number of blocks (the last may end inside one). For a process, input and output must also have the same offset within their pages and be either the same buffer or not overlap. Everything else, including CBC encryption and the hashes, goes through the driver's buffers. Because the CPU is not cache-coherent with DMA, the engine writes only whole 64-byte cache lines of the caller's memory; a head or tail that shares a line with other data is copied through the driver's buffer.

### Failure behaviour

- **Through the driver's buffers,** the output is written only after the engine has finished, so a failed request leaves the caller's data unchanged. An engine that stops answering is given up until the next reboot (the module stays loaded; `HWCRYPTO_INFO` reports `poisoned`), and later requests go to the kernel's software implementation where one exists; the others, and every ioctl, fail with `EIO`.
- **On the caller's memory,** an engine that does not finish a chain within 20 ms (a 64 KiB chain takes 0.7 ms) may still be writing to memory the caller has already reused, and the engine has no abort. The driver then stops the system with `panic`; the device restarts after 10 s (`kernel.panic`). A timeout or error status in the chain path used by the large `HWCRYPTO_GCM` on a kernel copy also panics.
- **Partly written buffers.** When the engine has been given up by another request, an in-place request on the caller's memory returns `EIO` with part of its buffer written. `HWCRYPTO_GCM` then fails as a whole; callers must fail the operation, not redo it in software. OpenSSL and the server do; `hwcrypto.h` states the rule.

### The device: `/dev/nanokvm-hwcrypto`

User-space programs use the misc device `/dev/nanokvm-hwcrypto` (mode 0600) with the ioctls of `platform/modules/hwcrypto/hwcrypto.h` (ABI version 1). Buffers are passed as user-space virtual addresses.

| ioctl | Purpose |
|---|---|
| `HWCRYPTO_INFO` | ABI version, algorithms, `max_input` (64 KiB) and `max_output`, `poisoned`, per-algorithm request counters |
| `HWCRYPTO_RUN` | one AES (ECB, CBC, CTR) or SHA-1/SHA-256 request; hashes take whole 64-byte blocks and the caller pads |
| `HWCRYPTO_GCM` | AES-CTR on the engine with GHASH on the CPU; the caller keeps associated data, partial blocks, lengths and the tag |
| `HWCRYPTO_GCM_BATCH` | seal up to 16 whole messages with one key in one call |
| `HWCRYPTO_CTR_SHA1` | up to 64 messages: AES-128-CTR over a range, then SHA-1 over the message, in one engine start |

**`HWCRYPTO_GCM`.** The caller passes the key, the hash key H, the GHASH accumulator and the counter block; length is a nonzero multiple of 16. Below 8 KiB the driver works in 4 KiB chunks on a kernel copy. If a chunk after the first fails, the call succeeds with the completed prefix in `length` (except in the in-place case above). The driver keeps the GHASH tables of the last two hash keys, so a TLS connection (one key per direction) builds them once. OpenSSL uses it for AES-GCM updates from 4 KiB (16 KiB records: 46 to 64 MB/s).

**`HWCRYPTO_GCM_BATCH`.** Encrypts the DTLS records of a burst on a WebRTC data channel (the RustDesk bridge), which are too short one by one for the engine to pay off. Each message takes its length rounded up to 16, plus 16 bytes, of a 64 KiB buffer: 16 messages of up to 4080 bytes, or 15 of 4 KiB; a larger batch is `EINVAL`. Each record is encrypted in place and its 16-byte tag written after it. The CPU computes the tags while the engine encrypts. Results are written after the engine has finished every record: a call that fails with `EFAULT` may have written some, any other failure none, and a caller that gets an error must not send any record. A batch of 16 records of 1205 bytes takes about 437 µs, against 660 µs for 16 single AES-GCM seals.

**`HWCRYPTO_CTR_SHA1`.** The server uses it for the SRTP packets of a video frame under AES128_CM_HMAC_SHA1: the engine encrypts the payload and computes the inner SHA-1 of the HMAC; the server finishes the outer hash. The crypt range is a nonzero multiple of 16 at an offset that is a multiple of 4; the hashed length is a multiple of 64, at most 4 KiB. `count` returns the number of messages completed. Packets that do not fit (no whole AES block, no room for the padding), audio, RTCP and retransmissions use one AES request per packet and SHA-1 on the CPU, as does a server running with a module that lacks this ioctl (it answers `ENOTTY`).

### Performance

Engine alone:

| Operation | Time / rate |
|---|---|
| AES-128-CTR, 1216 bytes | 4.4 µs |
| SHA-1, 1216 bytes | 4.6 µs (about 17 µs for the HMAC on the CPU) |
| AES-128-CTR and SHA-1 chained, 1216 bytes | 8.6 µs |
| AES, 4 KiB | 13.2 µs (128-bit ECB) to 16.9 µs (256-bit CTR); 240 to 330 MB/s |
| SHA-1 / SHA-256, 4 KiB | 13.9 / 11.6 µs; 295 / 350 to 370 MB/s |
| Copy, 4 KiB | 2.8 µs; 1.5 to 1.7 GB/s |
| 1 MiB in one descriptor | AES-128-CTR 3.15 ms, SHA-256 2.86 ms |

Through the kernel crypto API:

| Request | Time / rate |
|---|---|
| `ctr(aes)`, 1 MiB in 4 KiB elements | 276 MB/s |
| `ctr(aes)`, 1504 bytes | 117 MB/s |
| `gcm(aes)` AES-256, 1400 bytes | 32 µs encrypt, 26 µs decrypt (112 µs in software; ChaCha20-Poly1305 25 µs) |
| `gcm(aes)` AES-256, 16 KiB | 206 µs encrypt, 193 µs decrypt |
| `gcm(aes)` AES-256, 64 KiB | 732 µs encrypt, 773 µs decrypt |

Through the device, 64 KiB: `HWCRYPTO_RUN` AES-CTR 245 MB/s, ECB 246 MB/s, CBC decryption 145 MB/s; `HWCRYPTO_GCM` AES-256 in place about 720 to 760 µs.

OpenVPN data channel offload over UDP with AES-256-GCM on a 100 Mbit/s link reaches about 82 Mbit/s received and 67 Mbit/s sent, with the application and video running. With WebRTC streaming 2560 × 1440 H.265 at 60 fps, `HWCRYPTO_CTR_SHA1` lowers the CPU load of the core by about 4 percentage points.

### Engine limits

A descriptor does not continue the counter or chaining of the descriptor before it, and a hash result is only in the engine's registers, so every hashed message is an engine start of its own. The engine never answers again until a reboot after any of these:

- a cipher and a hash selected in one descriptor, or a copy together with a hash;
- a hash descriptor that continues the state in the registers instead of carrying its own, or a chain of two hash descriptors;
- a cipher writing to an address that is not a multiple of 8;
- an unknown algorithm selection.

The driver never issues these. The engine has no SM3, MD5, SHA-224, SHA-384/512, CRC or AES modes beyond ECB, CBC and CTR.

## Small core

The SG2002's second C906 (C906L) encodes the audio of the USB speaker. It runs at 700 MHz (594 MHz without FSBL patch 0014), has an FPU but no vector unit, a 16 KiB 4-way data cache and an 8 KiB 2-way instruction cache.

The module `nanokvm_c906l` (`platform/modules/c906l/`) binds the device-tree node `nanokvm,sg2002-c906l` and starts the core with its own program: libopus 1.6.1 (float, plain C) and a job loop, built by `scripts/build-c906l-firmware.py` from the `smallcore` CPU profile. The program is a static PIE that relocates itself, so the driver can place it anywhere in its 2 MiB of memory.

`/dev/nanokvm-opus` (`platform/modules/c906l/nanokvm_opus.h`) gives each open file one encoder, two at a time. `NANOKVM_OPUS_SETUP` sets bitrate, complexity and VBR constraint; `NANOKVM_OPUS_ENCODE` takes a 20 ms frame of 16-bit stereo PCM and returns the packet. `usb-audio-capture` encodes through it.

- **Cost.** A 20 ms frame (192 kbit/s, complexity 3, stereo 48 kHz) takes the small core 2.0 ms, 10 % of its time; the big core no longer encodes audio. The packets are byte-identical to libopus on the big core.
- **Failure.** If a job is not answered within 50 ms, the driver holds the small core in reset, later frames fail, and the helper exits with the error.
- **Hardware.** The node names the boot-address registers in SEC_SYS, the mailbox and its interrupt (101), the efuse clocks and the reset line (bit 6 of 0x03003024). Any access to SEC_SYS hangs the bus while the efuse clocks are gated, so the driver enables them around it. Mailbox channel 0 raises the small core, channel 1 the big one.
- **Updates.** The helper comes with the application package, the module and device tree with the kernel packages; audio works after the reboot that activates the new kernel.

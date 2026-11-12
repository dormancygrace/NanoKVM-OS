# Accelerators

The SG2002 has engines beside the CPU:

- the IVE, for image comparisons and filters;
- the TPU, for neural networks;
- DWA, for dewarp and rotation;
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
| `cv181x_dwa` | `/dev/cvi-dwa` |
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

The TRNG of the security subsystem (SG2002 TRM, section 22.2) seeds a DRBG from its noise source and returns 128 bits per command. Linux has no driver for it: U-Boot reads it once per boot (`platform/uboot/0010-rng-cv181x-trng.patch`) and puts two values into `/chosen` before it starts Linux:

- `rng-seed`, 32 bytes: the 256 bits the kernel needs to count its CRNG as seeded. Linux credits a boot loader's seed by default (`random.trust_bootloader`), so `random: crng init done` is printed at 0.000000, before any driver runs; without it, about 2 to 4 s. From then on the kernel reseeds from interrupt timing and CPU jitter, as on any board without a hardware generator;
- `kaslr-seed`, 8 bytes, for a kernel built with `RANDOMIZE_BASE`.

Linux clears both properties once it has read them. If the TRNG fails in U-Boot, U-Boot prints `No RNG device` or `dm_rng_read failed` and boots as before, without the seeds.

The registers are those of the Synopsys NIST TRNG core, which the TRM documents only in part. A second seed on a running generator is an illegal command sequence for that core: it raises an alarm, and the core then ignores every command; a zeroize with an alarm pending hung the bus. The driver therefore clears the alarms and the status, zeroizes the core, instantiates it from fresh noise, checks the alarms after every command, and zeroizes it again after its read. Every wait has a timeout.

The block sits behind the efuse clock gates. With the gates off, any access to its registers hangs the bus, and the board restarts through the watchdog. U-Boot's device tree node names both clocks, so the driver enables them before it touches a register; Linux turns them off again at boot as unused. Do not read its registers with `devmem`.

Checked on the NanoKVM PCIe stand: three boots, `crng init done` at 0.000000 each time; with a `RANDOMIZE_BASE` kernel, `_text` at a different address each boot. The Linux driver this replaces, with the same command sequence, read 1 MiB without an alarm across 64 seeds, and gzip -9 did not compress its output.

## CryptoDMA

Unlike the engines above, the CryptoDMA engine (AES, SM4, DES, SHA) is in use: its driver, `nanokvm_hwcrypto`, loads at boot unless `/etc/kvm/disable_hardware_aes` exists. Kernel users of these algorithms, such as IPsec or OpenVPN data channel offload with AES-GCM, reach it through the kernel Crypto API; the server's SRTP uses the driver's own device.

**Callers that cannot sleep.** A caller in softirq, such as OpenVPN's kernel data channel encrypting a packet, runs its AES request (ECB, CBC, CTR, GCM, CCM, CMAC) on the engine at once, with its own set of DMA memory; the engine registers are owned under a spinlock with bottom halves off, for one transaction at a time. Until 4.1.0 such requests went through an ordered workqueue, which cost more than the encryption of a packet. DES, 3DES, SM4 and the SHA hashes from such callers still use the queue. AES requests too short for the engine go to the software implementation, the kernel's AES block code for the C906 (0.6 us per block with a 128-bit key; the engine costs about 4.5 us before the first byte). The limits were measured per mode and key length (128 / 192 / 256 bits): ECB up to 112 / 96 / 96 bytes, CTR up to 144 / 128 / 112 bytes, GCM text up to 96 / 80 / 64 bytes. The kernel has no software `cbc(aes)`, so CBC requests use the engine at any length.

**The engine on the caller's memory.** For AES in ECB, CTR and CBC decryption (the modes where a block needs no result of the block before it) the driver no longer copies a request into its DMA buffers and back. It maps the caller's pages, pinning them when they are a process's, and gives the engine one chain of descriptors, a descriptor per range with its own IV; the engine answers once, at the end of the chain. This serves the kernel's `ecb(aes)`, `cbc(aes)` and `ctr(aes)` from 192 bytes (below three cache lines the copies are faster), `gcm(aes)` from 1 KiB (4 KiB for a caller that may not sleep), `HWCRYPTO_RUN` from 16 KiB and `HWCRYPTO_GCM` from 8 KiB.

- **What qualifies.** Every range of the request starts on a 16-byte boundary and is a whole number of blocks, except that the last may end inside one. For a process, input and output also sit at the same offset in their pages and are either the same buffer or do not overlap. Anything else, CBC encryption and the other algorithms take the copies as before.
- **Whole cache lines only.** The CPU is not coherent with DMA: after the engine has written, the driver invalidates the cache over what it wrote, in whole 64-byte lines, and a byte the CPU wrote into such a line meanwhile would be lost. So the engine writes only the whole lines of a range of the caller's memory; a head or tail that shares its line with other data goes through the set's own buffer, as more descriptors of the same chain, and the CPU copies those few bytes afterwards.
- **GCM.** The engine is three times as fast as GHASH (333 against 100 MB/s), so a request takes the time of its GHASH plus whatever the engine runs with nothing for the CPU to hash. A start takes up to 16 KiB; meanwhile the CPU hashes the ciphertext of the pieces before (encryption) or of the piece after (decryption in place, where the engine writes the plaintext over the ciphertext; the first piece is hashed before its start). The piece with nothing to overlap is made the short one: the first of an encryption, the last of a decryption in place. The engine is given back as soon as it is done, while GHASH goes on. `E(J0)` for the tag is one more descriptor of the first chain.
- **When the engine is lost.** Through the copies the driver writes a request's output only after the engine has finished it, so a failed request leaves the caller's data as it was. On the caller's memory two things differ.

  An engine that does not report a chain done within 20 ms (a 64 KiB chain takes 0.7 ms) may still be reading and writing the caller's memory, and the driver knows no reset or abort of the block that would stop it. The request cannot be completed then: its memory goes back to the caller, who frees or reuses it (a slab object, an skb, a process's buffer; a page reference keeps none of them), and a late write of the engine would land in whatever is there next. The caller cannot be held forever either, it may be a softirq. So the driver stops the system (`panic`; the device restarts after 10 s, `kernel.panic`). The legacy single-descriptor copy path still gives up a lost engine until reboot without panicking. The descriptor-chain path also serves the large `HWCRYPTO_GCM` fallback on kernel copies; a timeout or unsuccessful status in that path stops the system too. In the soaks of this change (hours under video, millions of requests) the engine never failed to answer.

  A request in place can also fail with part of its buffer written: when the engine was given up meanwhile by a request through the copies, the next start of the request on the caller's memory returns `EIO`. `HWCRYPTO_GCM` then fails as a whole instead of reporting a completed prefix, and the callers fail their operation instead of redoing it in software: OpenSSL and Go do (`hwcrypto.h` states the rule).
- **The buffers of the small sets.** The two sets that hold one 4 KiB request (atomic callers, SRTP) have input and output in one 16 KiB block, on different quarters of it. 16 KiB is one way of the L1 data cache, so where a buffer falls in it is which cache sets it uses, and input, output and the caller's buffer are all hot during a request. As two allocations the output came out in a different quarter on every other load of the module; when that was the quarter of the caller's buffer, a 2 KiB `gcm(aes)` request from an atomic caller took 46.7 / 35.4 us to encrypt / decrypt instead of 39.8 / 30.5 (4 KiB: 79.6 / 57.8 instead of 70.5 / 52.1). A caller's buffer may be anywhere, so one of the two can still share its sets; the block keeps them off each other and the same from load to load.

Measured on the SG2002 with the application stopped, the copies -> the caller's memory. The kernel's `ctr(aes)`: 1 MiB in 4 KiB elements 150 -> 276 MB/s, 64 KiB 155 -> 263, 4 KiB 132 -> 206, 1504 bytes 86 -> 117. `HWCRYPTO_RUN`, 64 KiB: AES-CTR 207 -> 245 MB/s, ECB 207 -> 246, CBC decryption 131 -> 145. `gcm(aes)` with AES-256, encrypt / decrypt in us: 1400 bytes 37 / 30 -> 36 / 30, 4 KiB 79 / 60 -> 72 / 63, 8 KiB 138 / 118 -> 114 / 105, 16 KiB 249 / 226 -> 206 / 193, 64 KiB 1027 / 995 -> 732 / 773. Below 1 KiB (4 KiB for an atomic caller) `gcm(aes)` keeps the copies: they are faster there. `HWCRYPTO_GCM` beside a 20 Mbit/s video stream, AES-256, in place: 16 KiB 237 / 219 -> 219 / 188 us, 64 KiB 1037 / 970 -> 719 / 759; a buffer that does not qualify goes through the same code on a kernel copy from 8 KiB: 16 KiB 227 / 209 -> 211 / 190, 64 KiB 1014 / 996 -> 833 / 823.

What the engine allows was measured first, with single starts: a chain of 2 to 64 cipher descriptors takes no longer than one descriptor of the same total (64 KiB: 216 us as one, 200 us as sixteen of 4 KiB, 208 us as sixty-four of 1 KiB); the pieces may be anywhere in memory and in place; input at byte offsets 1 to 8 and output at 1, 2, 4, 8 and 24 worked in these single starts (the hang on an output address that is not a multiple of 8, below, was found before the driver set the engine's burst to 2; the driver does not rely on either and gives the engine whole cache lines); a descriptor does not continue the counter or the chaining of the one before it. A chain of two hash descriptors, whether each carries its state or the second continues from the registers, leaves the engine giving wrong results until a reboot: hashes stay one descriptor per start.

**GCM for user space.** `HWCRYPTO_GCM` gives a process the overlap the kernel's `gcm(aes)` has: AES-CTR on the engine and, while it runs, GHASH on the CPU; from 8 KiB as described above, below that in 4 KiB chunks on a kernel copy (this chunk's input when decrypting, the previous chunk's output when encrypting). The caller passes the key, the hash key H, the GHASH accumulator and the counter block, and keeps the associated data, partial blocks, lengths and the tag. OpenSSL uses it for AES-GCM updates from 4 KiB (16 KiB records: 46 to 64 MB/s). The tables of the last two hash keys are kept (a TLS connection has one per direction), so a connection builds them once.

Measured on the SG2002, one `gcm(aes)` request of 1400 bytes with AES-256: 32 us to encrypt and 26 us to decrypt on the engine (GHASH on the CPU meanwhile), 112 us in software, 25 us for ChaCha20-Poly1305. OpenVPN data channel offload over UDP with AES-256-GCM, the device as the server on a 100 Mbit/s link, Mbit/s received / sent: 42 / 39 with the queue, 82 / 67 now (with the table GHASH of `lib/crypto`), 83 / 69 with ChaCha20-Poly1305 (the application and video running). The server's SRTP requests (1188 bytes, 1250 per second) while such a tunnel carries 30 Mbit/s each way: 99th percentile 216 us with the queue and 167 us now, 99.9th 4.9 ms and 1.0 ms. With the tunnel saturated the 99th percentile is 1.2 ms and 0.5 ms, and the 99.9th 5.4 ms and 10.7 ms, the tunnel then carrying 1.7 times the traffic.

**SRTP packets: cipher and hash in one start.** `HWCRYPTO_CTR_SHA1` takes a batch of messages and, for each, runs AES-128-CTR over a range of it and then SHA-1 over the whole of it, as one chain of two descriptors. The server uses it for the packets of a video frame under AES128_CM_HMAC_SHA1: the engine encrypts the payload and hashes header, ciphertext, rollover counter and the inner hash's padding from the HMAC key's inner state; the server does the payload's last partial AES block and the outer hash block (all of a frame's at once, four in a vector step). Packets it does not take (no whole AES block, no room for the padding) and everything else (audio, RTCP, retransmissions) keep the AES request per packet and the multi-buffer SHA-1 on the CPU, as does a server running with a module from before this request (it answers ENOTTY once).

What the engine does, measured on an SG2002 with the application stopped, 1216 bytes: AES-128-CTR 4.4 us, SHA-1 4.6 us (the multi-buffer SHA-1 on the CPU needs about 17 us for the HMAC of such a packet), the chain of both 8.6 us. Descriptors chain through their "next" words (low control bits 0x9 on all but the last, 0xf on the last; the status comes once, at the end); the engine runs them one after the other, so sixteen chained AES requests take 61.7 us against 68.2 us started singly. Three things it does not survive (it never answers and must be reset by a reboot), found the hard way: a cipher and a hash selected in one descriptor; a hash descriptor that continues the state in the parameter registers instead of carrying its own; and a cipher writing to an address that is not a multiple of 8. A hash may read from any word address, so the driver keeps a message shifted in its buffer to put the encrypted range at a multiple of 16. A hash result is only in the parameter registers, never written back to the descriptor, which is why every message is a start of its own.

While the engine works on a message the driver moves its neighbours: the previous message's result out to the caller and the next message in, each in a set of DMA memory of its own, with copies that do not sleep (the engine lock is held); one that would have to wait for a page is repeated after the engine has finished. Without this the CPU only waited for the engine: in a profile of the stream below, 690 samples of 40 000 in the wait loop, 124 with it.

Streaming 2560x1440 H.265 at 60 fps over WebRTC (21 Mbit/s, 2100 packets a second), the busy share of the core over 15 s windows went from 39.5-40.5% to 35.0-36.3% (36.4-38.6% before the copies overlapped the engine), and the instructions executed from 1540 to 1190 million per window.

**Everything the engine does.** The vendor publishes two headers (the FSBL's `cryptodma.h` and the Linux driver of the Milk-V Duo SDK); the SDK of the newer CV184x names more bits. Every code was run on an SG2002 and compared with OpenSSL, with a single descriptor and the application stopped (a code the engine does not know makes it stop answering until a reboot; this took more than twenty):

| Selection | What it is | 4 KiB | Rate |
| --- | --- | ---: | ---: |
| class bit 8 | copy ("bypass") | 2.8 us | 1.5 to 1.7 GB/s |
| class bit 9, key field `d[1]` bits 5/4/3 | AES-128/192/256; ECB, CBC (`d[1]` bit 1), CTR (bit 2) | 13.2 us (128, ECB), 16.9 us (256, CTR) | 240 to 330 MB/s |
| class bit 10 | DES; with `d[1]` bit 3, 3DES | 20 us, 59 us | 200, 70 MB/s |
| class bit 11 | SM4; ECB, CBC, CTR | 237 us | 17 MB/s |
| class bit 12, `d[1]` bit 1 clear | SHA-1 | 13.9 us | 295 MB/s |
| class bit 12, `d[1]` bit 1 set | SHA-256 | 11.6 us | 350 to 370 MB/s |
| class bit 13 | Base64 | | |

- **Hashes.** `d[1]` bit 0 says that the caller gives the chaining state (bit 19: in the descriptor); with it clear the engine starts from the standard initial value. The result is in the registers at 0x1c0.
- **One descriptor may be long.** 8 KiB, 64 KiB and 1 MiB run at the same rate as 4 KiB (AES-128-CTR 1 MiB in 3.15 ms, SHA-256 in 2.86 ms, a copy in 0.62 ms). The driver cuts a request of a caller that may sleep into pieces of 64 KiB (block ciphers, CCM, CMAC, the hashes, `HWCRYPTO_RUN`); GCM takes 16 KiB on the caller's memory and 4 KiB through the copies, callers in atomic context 4 KiB. Through the kernel providers, 1 MiB with pieces of 4 KiB and of 64 KiB: AES-CTR 11.3 and 7.7 ms (92 and 135 MB/s), AES-CBC 10.4 and 6.8 ms, CCM 15.6 and 11.2 ms (67 and 93 MB/s); the engine alone needs 3.2 ms.
- **Key and IV slots.** Class bits 16 to 18 take the key from the registers at 0x100 (written and read back; slot 0 verified), bits 20 to 22 the IV from the registers at 0x180, bit 19 and bit 23 from the descriptor. With none of the key bits the key is all zeros.
- **The IV comes back.** With bit 24 the engine leaves the next IV in slot 0: the last ciphertext block after CBC, the counter advanced by the number of blocks after CTR. A CTR descriptor that takes its IV from slot 0 continues the stream (verified).
- **Not in this engine:** SM3 (class bit 14 in the CV184x; here it never answers, with that SDK's descriptor too, and the result registers at 0x1e0 read zero), MD5, SHA-224, SHA-384/512, a CRC, and any AES mode beyond ECB, CBC and CTR: the other bits of `d[1]` change nothing (bit 3 with SM4, OFB in the CV184x, is plain ECB here). The OTP key (bit 27 in the CV184x) gives the result of the zero key.
- **What it does not survive,** besides the three cases above: a hash selection with `d[1]` bit 2 or 3 set (values 4 to 8), an AES key field of zero, a class bit it does not know alone (14, 15, 17, 18), and the copy together with a hash. Unknown class bits beside a known class are ignored.

There is no `/dev/crypto`: the cryptodev module was removed in 4.0.0, since nothing in NanoKVM OS used it and Alpine's OpenSSL has no devcrypto engine. Each request through it cost an `ioctl`, so it was faster than the CPU only for large blocks. Measured on the stand through an OpenSSL devcrypto engine (Linux 7.2), AES-256-CBC took 1 KiB at 14 MB/s and 8 KiB at 47 MB/s against about 8 MB/s in software, and blocks of 16–256 bytes were 7–25 times slower than software.

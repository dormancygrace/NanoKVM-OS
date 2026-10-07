# NanoKVM OS v2.5-a1

v2.5-a1 opens the **2.5 line**. It is the v2.1-b1 base plus the video, memory and CryptoDMA work merged up to #61.

## Scope of the 2.5 line

2.5 takes **fixes and optimisation only**: bugs, stability, performance, memory and size, simplification of the existing interface, translations, documentation and tests. **New features wait for the next line.** A change that adds a user-visible capability, setting, API or package is out of scope even when small. Removing or merging existing controls to simplify the interface is in scope.

## Changes since v2.1-b1

### Video
- 3840×2160 at up to 30 fps with a video overclock: codec clock 594 MHz, AXI video codec 450 MHz, VIP 396 MHz. The core voltage is unchanged.
- Frame-rate tiers: 1280×720 up to 120 fps, 1920×1080 up to 100 fps, 2560×1440 up to 60 fps, larger sizes up to 30 fps.
- The monitor refresh follows the stream frame rate: each monitor profile offers several refresh rates, and the EDID is written once per Apply.
- VPSS drops surplus input frames when the input is faster than the stream.
- Video settings are built on profiles and device capabilities (`GET /api/vm/video/capabilities`, `POST /api/vm/video`); the recommended bitrate follows size and rate.
- H.265 over WebRTC is available again. Hardware SRTP AES is used only when the loaded CryptoDMA module reports the fixed driver (out of place, write bursts of 4); otherwise SRTP uses software until the module is reloaded.

### CryptoDMA
- Ciphers run out of place, and CryptoDMA writes use bursts of at most 4 beats. Concurrent HDMI capture, scaling, H.265 encoding and CryptoDMA writes hung the SoC: mean time to hang about 78 s with the vendor's bursts of 6, none in 15 minutes with bursts of 4 on the same stand, and none in 13 minutes of 3840×2160 H.265 WebRTC. These are soak durations, not proof that every hang is gone.

### Memory and storage
- Video memory modes: CMA (128 MiB lent to Linux, default), Fixed 64 MiB, and **4K**, a 128 MiB fixed carveout that 3840×2160 requires. With CMA, 2 of 4 cold boots at 3840×2160 had no video; in the 4K mode 4 of 4 did.
- zram **Half of RAM** (auto) size, the default in the 4K mode.
- The kernel package ships one FIT template plus device trees, and the device composes `boot.sd` from them: `/usr/lib/nanokvm/boot` shrinks from 133 MiB to about 9.5 MiB.

### Platform
- OpenSBI 1.9 FIP for SG2002 (#54).
- XTheadVector context fixes and aligned 64-bit LZ4 decompression on the C906 (#56). The opt-in vector kernel backends of #46 are not included; they gave no gain on the NanoKVM.
- RustDesk: reusable framing buffers and detached XSalsa20-Poly1305, with about 35 % less writer CPU for 16–256 KiB messages (#55); video dimensions outside the frame header range are rejected.
- The watchdog stays active across application restarts; private data was removed from the release line (#53).

## Validation scope

- The video overclock and the 4K mode were tested on the NanoKVM PCIe board (LT6911UXC) only. Other board revisions are not qualified.
- The CryptoDMA durations above come from one board and are soak results.
- See `docs/VIDEO.md` for the video and memory modes.

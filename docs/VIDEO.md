# Video and HDMI settings

Fresh-browser default: the **device transport**, **H.265 Direct** over HTTPS until an administrator chooses another. Existing browser preferences are retained. Browsers without secure WebCodecs fall back to WebRTC, then MJPEG when WebRTC is unavailable; H.265 falls back to H.264 when the browser cannot decode it for the chosen transport.

The device transport (`/kvmapp/kvm/transport`: `direct`, `webrtc` or `mjpeg`) is what an administrator last applied as the transport in **Settings → Video** or the quick video menu. A browser that has never chosen a transport of its own opens the video with it, and the RustDesk add-on follows it: it accepts WebRTC connections exactly when the device transport is WebRTC, and uses TCP and relay otherwise. When an administrator's browser plays another transport than the device's, the video settings offer to make it the device transport. A device upgraded with RustDesk WebRTC enabled starts with the WebRTC device transport.

H.265 is available over WebRTC again. It was disabled in v2.1-b1 because the device could freeze while CryptoDMA encrypted SRTP; the AES driver now runs ciphers out of place, which removed the freeze.

The quick video menu shows the measured stream size, frame rate and bitrate. Its Mode, Bitrate and Transport rows change the corresponding settings; browser scale and a link to **Settings → Video** remain available.

## Video modes and bitrate

Video modes combine the monitor resolution and stream frame rate. The page separates shared picture settings from this browser's transport and playback settings. Unavailable choices explain the hardware, memory, permission or browser limitation. A resolution that the booted video memory cannot capture (only before the restart that completes a kernel update) says so (see Video memory).

**Auto bitrate** selects a ceiling for motion from the prospective stream dimensions, frame rate and codec: about 0.09 bit per pixel for H.265 and 1.3 times that for H.264, rounded up to an offered value, capped at 20 Mbit/s. H.265 examples: 1280×720@120 selects 10 Mbit/s, 1920×1080@60 selects 12 Mbit/s, and QHD@60 or UHD@30 selects 20 Mbit/s. **Economy** selects 3 Mbit/s. Manual bitrate remains available. A still desktop normally sends much less than the ceiling.

The encoder applies rate-control limits before capture starts and uses NormalP, with keyframes on viewer request plus a ten-second safety interval. There are no GOP or SmartP settings, and the screen API no longer accepts them. H.264 remains Main. Native encoder diagnostics are included in the application log.

## HDMI monitor and automatic input detection

The monitor profile advertises preferred and fallback modes through EDID. **Automatic** is 1920×1080 at up to 90 Hz wherever the EDID can be written live (the stock receiver profile elsewhere); 2560×1440 and 3840×2160 are explicit choices. The landscape profiles prefer 3840×2160, 2560×1440, 1920×1080 or 1280×720; the portrait profiles 720×1280, 1080×1920, 1296×2304 and 1440×2560 are in the same list. 1296×2304 is the tallest portrait the H.264 encoder takes (at most 4096×2304); 1440×2560 needs H.265, over Direct or WebRTC. A source may choose another supported resolution; applying the profile does not require the source to adopt its preferred timing. The profiles are single-rate: each advertises no mode at or above its own resolution other than its preferred timing (1920×1080 at 75 Hz lists no other 1080p, 1080i or 1440p timing), because the Windows GPU driver tested here does not honour the preferred timing and otherwise outputs a fallback such as 1080p50 or 2560×1440 at 50 Hz. Lower-resolution fallbacks (VGA, 800×600, 720p, and 1024×768 or 1280×1024 where lower than the profile) stay, so a source that cannot drive the chosen timing falls back to a lower resolution (choose a slower profile to restore the larger one). Changing a profile briefly reconnects HDMI. Which profiles a device offers depends on its HDMI receiver, and how a new profile takes effect on its board. The LT6911UXC takes every profile; the LT6911C and the LT6911D take the 720p and 1080p profiles at 60 Hz until they are measured beyond that. PCIe boards apply a profile at once (they reset the receiver); Cube and Lite boards need a physical power cycle after each write. This does not affect automatic source detection.

**The monitor refresh follows the frame rate.** When video settings are applied, the monitor uses the slowest refresh rate of its profile that is not below the stream frame rate:

| Profile | Refresh rates (Hz) |
|---|---|
| 1280×720, 720×1280 | 120, 60, 30 |
| 1920×1080, 1080×1920 | 90, 75, 60, 30 |
| 2560×1440 | 60, 50, 40, 30 |
| 1296×2304, 1440×2560 | 60, 50, 30 |
| 3840×2160 | 30 |

A 30 fps stream therefore makes the computer render and send 30 frames per second instead of 90. These profiles leave out the faster modes of their resolution, because a source otherwise picks the fastest one. On Cube/Lite boards the monitor does not follow the stream rate, because every EDID write needs a power cycle: it keeps the fastest rate of the chosen resolution (an LT6911UXC) or the single 60 Hz profile (an LT6911C or LT6911D), and the stream rate only limits the encoder. A Cube receiver keeps its EDID in flash across reinstalls, so on the first start of a Cube with an LT6911C or LT6911D the server reads it once and, if it advertises a mode beyond 720p/1080p at 60 Hz (for example 1920×1080 at 100 Hz left by another system), programs the saved 60 Hz profile and asks for a power cycle. An LT6911D on a PCIe board is programmed only when the user applies a profile. A profile explicitly deferred by a video-memory switch is applied after the new pool boots, including on Cube/Lite; a failed write is retried on the next server start, and Cube/Lite still requires a physical power cycle afterwards. Changing FPS through the single-setting API now resynchronizes the EDID too, but only when a different monitor timing is selected.

**Automatic has two parts, and both are strict.**

- *The generated static profile* is `NanoKVM-monitor-auto.bin`, installed as `NanoKVM-final-video-profiles.bin`; it is byte for byte `NanoKVM-monitor-1080-90.bin`. 1920×1080 at 90 Hz (CVT reduced blanking, 89.90 Hz) is the only mode at or above 1080 lines or 1920 pixels (no 1080p60, 1080p50 or 1440p timing); 720p, VGA and the other lower modes stay. Where the EDID can be written live, this file is what an install programs and what the server uses when a rate profile file is missing. The stock receiver profile (other receivers) and the single 60 Hz Cube/Lite profiles are separate files.
- *The runtime selection* applies when video settings are applied with a stream frame rate, or when the monitor setting changes. Automatic then uses the same files as an explicit 1920×1080 setting: `NanoKVM-monitor-1080-<hz>.bin` with the slowest rate not below the stream rate. That is 90 Hz for 76 fps and up (including 100 and 120), 75 Hz for 61 to 75 fps, 60 Hz for 31 to 60 fps and 30 Hz up to 30 fps. So Automatic is not always the static 1080p90 file: at 60 fps the computer is offered 1080p60 alone.

A computer that cannot drive the offered timing (a 60 Hz-only source at the default 90 fps, or a source limited below the selected rate) falls to a lower resolution such as 720p, because nothing else at 1080 is listed. Lower the stream frame rate in the video settings so that Automatic selects a slower profile (60 fps gives 1080p60), or choose a slower explicit profile. The static file deliberately has no 1080p60 fallback, and neither does the runtime profile: the Windows GPU driver tested here ignores the preferred timing and outputs a listed fallback (1080p50, 1080p60 or 2560×1440 at 50 Hz were measured) whenever more than the preferred mode is listed at or above the resolution, so a 60 Hz entry would make Automatic output 60 Hz instead of 90 Hz on that computer. This is a trade-off: the compatibility fallback that a 60 Hz-only source would have had is given up for a predictable timing. Automatic follows no feedback from the source. The single-setting FPS API and the full video-settings path both resynchronize the offered rate.

When the input is still faster than the stream (an unchanged monitor, or a source that ignores the preferred mode), VPSS drops the surplus input frames before converting them.

Capture follows the actual HDMI dimensions when the BIOS, bootloader or operating system changes modes. No EDID rewrite is needed for each source transition. The page shows the input size and its measured refresh rate, the encoded size and the measured server frame rate (not browser presentation FPS), and the monitor profile with its refresh rate.

## Stream resolution

**Same as input** uses the actual source dimensions. **Up to 2160p / 1440p / 1080p / 720p / 600p** fits the source within the corresponding bounding box, preserves aspect ratio and never enlarges a smaller image. NV21 dimensions are rounded down to even pixels. Every size up to 2160p fits the video memory of the current boot images.

## Video memory

Every boot image has the same video memory: one reusable CMA region of 144 MiB. Linux uses the idle part of the region for movable memory (page cache, programs, zram) and migrates it away when video allocates; outside the region about 90 MiB stay for unmovable kernel memory. MemTotal on a NanoKVM PCIe is 235 MiB (with the fixed carveouts of earlier releases it was 194 MiB for FHD, 178 MiB for QHD and 130 MiB for UHD), and every resolution up to 3840 × 2160 fits without a restart. **Settings → Memory** shows the size of the region.

| Video use | Largest size | Measured |
|---|---|---|
| 1920 × 1080 | 1920 × 1080, 100 Hz with MJPEG | 39.8 MiB |
| 2560 × 1440 | 2560 × 1440, portrait | 53.9 MiB (about 59 MiB with the third VPSS buffer the region allows) |
| 3840 × 2160 | 3840 × 2160 | 102.7 MiB |

Earlier CMA images sized the region to the video peak: once video had been watched they lent Linux 1–3 MiB, and as CMA the UHD encoder could not always get its buffers on a cold boot, because a busy page left no other range to allocate from. The 144 MiB region keeps at least 40 MiB of slack. The whole 2160p buffer set (about 105 MiB in 8 allocations) was allocated in every test: right after boot (0.25 s), with the region full of page cache (0.1 s) and with page cache plus 100 MiB of anonymous memory (2.4 s, the slowest buffer 0.9 s). Under full load (1080p90 H.265 and 500 MB over Wi-Fi, on a test build that also ran a TPU model and the IVE at 1080p) unmovable kernel memory peaked at 31.4 MiB and at least 32 MiB stayed free outside the region; a 160 MiB region would leave about 16 MiB.

The video peaks were measured on a NanoKVM PCIe, with any codec, transport and order of clients: 39.6 MiB at 1920 × 1080 and 100 Hz with MJPEG running at the same time (39.8 MiB for the 1080 × 1920 portrait), 53.9 MiB at 2560 × 1440 and 60 Hz with MJPEG (the 1440 × 2560 portrait has no MJPEG and takes 41.3 MiB), and 102.7 MiB at 3840 × 2160 (H.265; H.264 takes no more, and MJPEG is not available at 2160p). The encoder runs NormalP only, and the JPEG encoder's bitstream buffer is sized from the picture instead of the driver's 9 MiB default.

A fault does not show as use but as an allocation that fails: the encoder then refuses the frame (it no longer encodes without its buffers), the channel is recreated, the log says "video memory exhausted", and video resumes once memory is free.

The capability follows the booted region: a device that installed a kernel update keeps the fixed carveout of its earlier image (40, 56 or 104 MiB) until it restarts, and until then refuses what that carveout cannot capture; the capture status then asks for the restart. Earlier releases chose the carveout as a video memory mode in **Settings → Memory**; activating the kernel now removes that choice and every device boots its board's image.

zram is on by default with the shared region, as it was with the UHD carveout. **Auto** is half of the memory Linux keeps while 2160p capture holds its buffers: MemTotal outside the region plus what 2160p leaves of the region, 65 MiB on a NanoKVM PCIe. Devices with their own setting in `/etc/kvm/memory.conf` keep it.

The device never advertises a monitor profile that its video memory cannot capture: a profile saved for more memory is lowered when the server starts on an earlier image.

## Frame rate limits

The frame rate is capped by the larger of the input and the encoded size, in either orientation: up to 1280×720 at 120 fps, up to 1920×1088 at 100 fps, up to 2560×1440 at 60 fps, larger sizes at 30 fps. With the video overclock the encoder sustains about 250 million pixels per second: 3840×2160 at 30 and 2560×1440 at 60 fps; at 1920×1080 a fixed per-frame cost limits it to about 109 fps, so the 1080p monitor is offered at up to 90 Hz, which keeps a margin (the capture table still allows 100 fps). The server and the native capture library use one table (`server/common/video_status.go`, `kvm_mmf/include/internal/capture_rate.hpp`); a test keeps them equal. The saved request is retained across source changes and is restored when the source allows it again. Settings show the delivered rate when it is below the request.

MJPEG is unavailable while the HDMI input is 3840×2160, whatever the stream limit is (next to a 4K stream the JPEG channel wedges the hardware encoder until reboot, and with the stream limited to 1440p it produces no frames): the MJPEG stream, MCP and screenshots, and selecting MJPEG are refused with a message, and `stream.mjpeg` in the capabilities is unavailable with the reason `mjpeg-4k`; use H.264 or H.265.

## API

- `GET /api/vm/video/capabilities` lists every monitor mode with its refresh rates, the portrait profiles with the codecs and transports they need, the stream limits, the frame-rate table, the input size and rate, the codecs per transport, the device transport (`stream.transport`) and the size of the video memory (`videoMemoryMiB`). Unavailable entries carry a reason: `video-memory` (a size the booted video memory cannot capture: an earlier image before its restart) or `receiver`.
- `POST /api/vm/video` (administrators) applies several settings together: `type`, `quality` (MJPEG), `bitRate`, `mjpegChroma`, `height`, `fps`, `portraitResolution`, `portrait`, `monitor`, `confirmPowerCycle`. Every setting is validated before any is applied; the EDID is written at most once, last, and follows `fps`. It returns the new capabilities.
- `GET/POST /api/vm/screen` remain for single settings.

## Advanced settings

Direct playback (smooth or lowest latency), JPEG color sampling, frame-change detection and HDMI recovery remain available in Video settings. MJPEG adapts quality to viewer delivery pressure and recovers up to the selected quality ceiling. Opening the menu or page reads device settings rather than overwriting them from stale browser storage.

## Colour

The HDMI receivers send limited-range BT.709 YCbCr. The VPSS converts its input to RGB with a group matrix and each YUV output back with the output's own. The native library sets the receiver's matrix at the input, so the VPSS works in true RGB: the video outputs convert back to limited-range BT.709 (osdrv patch 0010; the H.264 and H.265 streams say so in their VUI), and NV16, which only JPEG takes, to full-range BT.601, as JPEG decoders read it. Before, YUV passed through unchanged and JPEG came out pale (black at 16, white at 235): a dark taskbar of (40, 44, 58) showed as (51, 54, 65) in MJPEG and screenshots; it is now (40, 44, 58) in JPEG and H.265 alike. With 4:2:0 sampling MJPEG shares the video output and stays as before. The kernel module and the native library must come together: on a kernel without patch 0010, `/proc/cvitek/vpss` does not show `BT709` and the library keeps the vendor matrices.

JPEG quality 50 is encoded as 51: the encoder rejects a quality factor of exactly 50, and the failure used to switch MJPEG to the 4:2:0 fallback until restart once the adaptive quality reached it.

The source decides the RGB range on HDMI. In CE video formats such as 1920×1080 at 60 Hz it sends limited-range RGB (16–235) unless the monitor says it accepts either. The receivers convert RGB to YCbCr as if it were full range, so black arrived as 16 in every codec. Every monitor profile except the stock one carries a CTA Video Capability Data Block with QS=1 (`scripts/edid-rgb-range.py`, the last step of the profile build), and the source may then send full-range RGB. An NVIDIA card then does so by default: on the stand the dark taskbar went from (40, 44, 58) to (27, 33, 48) and black from 16 to 0. QY stays 0, because the capture expects limited-range YCbCr from the receiver. A source that ignores the block, or is set to limited range by hand, still sends limited range; set its output to full range (NVIDIA: *Output dynamic range*).

A device updated from a release without the block keeps the profile it programmed. At the first start the server finds the shipped profile whose earlier version has the recorded hash and programs the current version of it once, with the same resolution and rate. A Cube is left alone, since every EDID write there needs a power cycle; it gets the block with the next profile it programs.

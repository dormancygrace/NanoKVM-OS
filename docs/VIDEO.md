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

The HDMI detector publishes a receiver sample only after its register-access session has completed, including cleanup. A transient I2C error keeps the last valid geometry; three consecutive failed or incomplete polls withdraw the signal without replacing that geometry. A confirmed disconnect is immediate. Failed reads are retried once per second even when the kernel supplies edge events. LT6911D requires both active dimensions to be nonzero. The detector, sensor probes and EDID tool share receiver ownership; reset and EDID maintenance drain the active detector session with a bounded wait. Receiver errors and failed cleanup are recorded in the application log. Reset and the standalone EDID tool wait up to two seconds for a short competing register session; detector admission remains nonblocking. Resuming maintenance starts a fresh consecutive-failure allowance even if no detector poll observed the pause.

For physically qualified UXC receivers, bank selection, register pointer and read data travel in one Linux
[`I2C_RDWR` transaction](https://docs.kernel.org/i2c/dev-interface.html); register writes combine the bank and payload in the same
way in the detector and EDID programmer. C/D retain separate STOP-delimited
bank/register transfers. Sensor probes also retain that framing because they
run before the receiver variant has been identified. Full-session ownership
protects both transports from cooperating clients.
For combined transfers the adapter must report `I2C_FUNC_I2C`; an unsupported adapter or a short
message count is an error. Failed transfers are not replayed, and failed reads
do not publish partial bytes. Every operation carries its own bank, so the
EDID programmer no longer needs a bank cache.

After a successful PCIe reset release, detector admission returns `settling`
without I2C until a monotonic deadline: LT6911C 2500 ms, LT6911D 2500 ms,
LT6911UXC 250 ms. Cube/Lite EDID maintenance does not reset the receiver:
resuming it retains the previous 250 ms post-access gap for every model,
without shortening an already pending reset deadline. C and D have independent policy
constants even though their initial values match. The standalone EDID tool
uses the same model-specific delay after each successful reset pulse, with
normalized seconds/nanoseconds and interrupted sleeps resumed. Service startup
also waits 2.5 seconds for C/D after releasing an asserted receiver reset,
before either service can probe; UXC keeps its existing one-second startup
warmup. A missing model marker uses the longer startup window.

These are minimum quiet windows, not promises of HDMI lock. UXC first access
was about 251 ms after release and a stable sample about 1.77 seconds after
release on the PCIe stand. The 2.5-second C/D starting values come from
[Comet C measurements](https://github.com/macpit/glkvm-rm1pe-kernel/blob/d38a0af2fee3ae49542c21149e896a45a271096a/patches/0004-lt6911c-hdmi-capture-and-cold-lock.patch)
and [ESP-KVM D measurements](https://github.com/espkvm/espkvm/blob/7b61f7c1196824a5bc3f37bf0bc426a38afa9dd9/docs/HARDWARE-NOTES.md),
not NanoKVM C/D hardware qualification. Normal readiness checks and bounded
retries still apply.

The cached board model selects a register protocol. Diagnostics identify the
silicon separately: C reads A000/A001 (normalized ID 0516), UXC reads
8100/8101 (normalized ID 0417; wire bytes 17 04). D's E100/E101 ID 2102 is
read only by the exclusive stopped-capture probe, once per reset generation;
normal D diagnostics report the cached board-model hint and
`chip_id=exclusive-probe-only`, and perform no register writes. An unexpected ID is reported as
unknown together with the board-model hint; it does not switch capture
protocols. Firmware bytes never decide the model. Every internal-ID session
attempts to close its register window, including failed enable/read paths.
The exclusive D probe caches both successful and failed ID attempts until a
new reset generation. An ID/cleanup failure aborts that probe before further
telemetry; it cannot enter normal capture as a disposable diagnostic error.

Diagnostic fields are model-specific:

| Receiver | Available diagnostic data | Explicitly unavailable |
| --- | --- | --- |
| C | Hardware ID; five raw firmware bytes D20C..D210; HDMI active size, totals, sync and porches from D2; separate CSI size from C2; raw RX status bits and TX lane configuration | Decoded lane count, PLL-lock polarity and qualified clock units |
| D | Hardware ID in exclusive probe only; four raw firmware bytes E080..E083; active size and totals; lane count, format, event, stream control and byte-clock registers; version-matched MIPI TX sync/porches/polarity and raw TX clock | Independently measured HDMI/CSI sizes and qualified clock/FPS; extra timing fields on other firmware |
| UXC | Firmware revision, CSI lanes, HDMI active size/totals/sync/porches/polarity/format bits and raw TMDS; separate CSI size | Qualified clock/FPS on NanoKVM firmware 00000300 |

C measurements enable D283=11 and wait 5 ms, matching the existing sampling
sequence. Horizontal measurement fields use half-pixel units. C firmware
registers were measured on GL.iNet firmware; other revisions may return
unavailable bytes. D's E084 is an event code, not live cable presence, and
E080..E083 are kept raw rather than decoded as a date or pixel clock. Normal D diagnostics are read-only. The exclusive probe alone may open the
internal ID window. No D stream toggle, flash command, frequency-meter
selection or color/lane configuration is performed. Zero/all-FF firmware bytes are marked unavailable
without rejecting a known hardware ID.

Additional C/D fields follow the [firmware analysis](LT6911-FIRMWARE.md).
C reads B8B0 and C201/C256 inside the existing checked session. B8B0's bit 0
is the reversed firmware's clock-detect predicate; bit 6 is reported without
assuming lock polarity because the pseudocode and dump disagree. Lane bytes
stay raw. No PLL training or lane configuration is performed.

D firmware 25.05.13.01 publishes additional **MIPI TX** timings, not an
independent HDMI measurement. Only this version enables the extra E085..87,
E098..9F and E0A1..A6 reads. E0A0 is not read. Its format 0A uses full
horizontal units; the other branch uses half units. This scaling applies to
diagnostics only, not capture sizing. The additional clock remains raw.
Unknown firmware keeps the baseline reads/decoding and reports the extra
fields unavailable. Version matching selects a known layout; it does not
authenticate the entire installed image. Matching versions on other boards
still need physical qualification.

Known D timings and C status are checked again to reject an update during
the sample. D's producer publishes the event before the payload, so the
event alone cannot prove coherence. Two matching reads reduce torn samples;
they do not make the MCU update atomic. Clock counter jitter is permitted.

Telemetry runs on receiver/mode transitions (including same-size IRQs) and
after reset/maintenance. Only I2C failures retry periodically, at most every
five seconds while state is unchanged; invalid measurements wait for a
transition or IRQ. Only changed diagnostic text is logged. C/D re-read timing
fields to reject mixed mode changes; invalid/FFFF dimensions, D width overflow
and inconsistent C timing sums are rejected. Optional diagnostics never
replace capture geometry or withdraw an otherwise valid signal.

Register addresses and units follow
[Rockchip C](https://github.com/rockchip-linux/kernel/blob/e81982d9e242815c9ce1b1e3f2db2ce7450c94d4/drivers/media/i2c/lt6911c.c),
[Comet C firmware measurements](https://github.com/macpit/glkvm-rm1pe-kernel/blob/d38a0af2fee3ae49542c21149e896a45a271096a/patches/0004-lt6911c-hdmi-capture-and-cold-lock.patch),
[Espressif D](https://github.com/espressif/esp-video-components/blob/b413578f9186a814f7df2bd04442e796c978fae3/esp_cam_sensor/sensors/lt6911/lt6911.c),
[Rockchip UXC](https://github.com/rockchip-linux/kernel/blob/470f9dccbdc42e7b8a824d0a5c5640a10e9457d2/drivers/media/i2c/lt6911uxc.h)
and [ZHAW](https://github.com/InES-HPMM/Lontium_lt6911uxc/blob/c6a314423d1209df82a7c669f45ae7a765601715/source/lt6911uxc_regs_zhaw.h).
New C/D telemetry reads use individual register accesses, matching the donors
rather than assuming long auto-increment bursts. C/D keep STOP-delimited
transfers; repeated-START, timing and telemetry still
require physical qualification on those NanoKVM receivers. The exclusive
`hdmi-receiver-runtime` probe accepts `diagnostics c|d|ux` or
`reset-ready c|d|ux` for that later bench work.


The monitor profile advertises preferred and fallback modes through EDID. **Automatic** is 1920×1080 at up to 90 Hz wherever the EDID can be written live (the stock receiver profile elsewhere); 2560×1440 and 3840×2160 are explicit choices. The landscape profiles prefer 3840×2160, 2560×1440, 1920×1080 or 1280×720; the portrait profiles 720×1280, 1080×1920 and 1440×2560 are in the same list. 1080×1920 is the tallest portrait H.264 encodes (see H.264 below); 1440×2560 needs H.265, over Direct or WebRTC. The 1296×2304 portrait of earlier releases, the tallest H.264 portrait before H.264 stopped at 1080×1920, is retired: a device that had it selected gets 1440×2560 (1080×1920 when its device codec is H.264) and programs that profile on the first start after the update. A source may choose another supported resolution; applying the profile does not require the source to adopt its preferred timing. The profiles are single-rate: each advertises no mode at or above its own resolution other than its preferred timing (1920×1080 at 75 Hz lists no other 1080p, 1080i or 1440p timing), because the Windows GPU driver tested here does not honour the preferred timing and otherwise outputs a fallback such as 1080p50 or 2560×1440 at 50 Hz. Lower-resolution fallbacks (VGA, 800×600, 720p, and 1024×768 or 1280×1024 where lower than the profile) stay, so a source that cannot drive the chosen timing falls back to a lower resolution (choose a slower profile to restore the larger one). Changing a profile briefly reconnects HDMI. Which profiles a device offers depends on its HDMI receiver, and how a new profile takes effect on its board. The LT6911UXC takes every profile; the LT6911C and the LT6911D take the 720p and 1080p profiles at 60 Hz until they are measured beyond that. PCIe boards apply a profile at once (they reset the receiver); Cube and Lite boards need a physical power cycle after each write. This does not affect automatic source detection.

**16:10 and 21:9.** An LT6911UXC also offers 1920×1200 and 2560×1600 (16:10) and 2560×1080 and 3440×1440 (21:9); in a Cube or Lite like every profile there, with a power cycle after the write and at the fastest rate of the size. An LT6911C or LT6911D, measured up to 1080p60 only, offers the two sizes near 1920×1080 at the rates within its 148.5 MHz pixel clock: 1920×1200 at 50 and 30 Hz and 2560×1080 at 30 Hz, not yet tried on those receivers (`slowWiderMonitorRates`). **Settings → Video → Aspect ratio** picks the ratio, then **Resolution** the size; the default stays 16:9 and the quick video menu lists the sizes of the current ratio. Each profile is CVT reduced blanking with the 800×600 legacy fallback, and of the stock EDID's established and standard timings only those smaller than the profile in both directions: a GPU outputs a mode as wide or as tall (1920×1080 next to 1920×1200) instead of the preferred one (`scripts/build-wide-monitor-edids.py`). A saved 16:10 or 21:9 profile the receiver does not take reads as Auto in both screen APIs. The `monitor` setting (`/etc/kvm/monitor_resolution`, the API's `monitor`) holds a profile id: the height of a 16:9 or 16:10 profile, the width of a 21:9 one, whose height is that of a 16:9 profile (2560 is 2560×1080, 3440 is 3440×1440); `/api/vm/video/capabilities` lists each mode's `id`, size and `aspect`. Measured on a NanoKVM PCIe with an LT6911UXC, H.265 VI-direct: 1920×1200 and 2560×1080 at 60 Hz; 2560×1600 at 50 Hz (at 60 the capture delivered about 50 frames and H.265 fell to 15 fps); 3440×1440 at 40 Hz (at 50 one frame takes the encoder a whole frame period and H.265 delivered 25 fps). 3440×1440 is H.265 only: H.264 and MJPEG stop at 2560 pixels wide. H.264 encoded 2560×1600 at 50 fps through the 4:2:0 capture.

**The monitor refresh follows the frame rate.** When video settings are applied, the monitor uses the slowest refresh rate of its profile that is not below the stream frame rate:

| Profile | Refresh rates (Hz) |
|---|---|
| 1280×720, 720×1280 | 120, 60, 30 |
| 1920×1080, 1080×1920 | 90, 75, 60, 30 |
| 2560×1440 | 60, 50, 40, 30 |
| 1440×2560 | 60, 50, 30 |
| 3840×2160 | 30 |
| 1920×1200, 2560×1080 | 60, 50, 30 |
| 2560×1600 | 50, 30 |
| 3440×1440 | 40, 30 |

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

Before the latest-frame VI queue and disabled-output VPSS pool reclamation, video peaks were measured on a NanoKVM PCIe, with any codec, transport and order of clients: 39.6 MiB at 1920 × 1080 and 100 Hz with MJPEG running at the same time (39.8 MiB for the 1080 × 1920 portrait), 53.9 MiB at 2560 × 1440 and 60 Hz with MJPEG (the 1440 × 2560 portrait has no MJPEG and takes 41.3 MiB), and 102.7 MiB at 3840 × 2160 (H.265; neither H.264 nor MJPEG runs at 2160p). The encoder runs NormalP only, and the JPEG encoder's bitstream buffer is sized from the picture instead of the driver's 9 MiB default.

A fault does not show as use but as an allocation that fails: the encoder then refuses the frame (it no longer encodes without its buffers), the channel is recreated, the log says "video memory exhausted", and video resumes once memory is free.

The capability follows the booted region: a device that installed a kernel update keeps the fixed carveout of its earlier image (40, 56 or 104 MiB) until it restarts, and until then refuses what that carveout cannot capture; the capture status then asks for the restart. Earlier releases chose the carveout as a video memory mode in **Settings → Memory**; activating the kernel now removes that choice and every device boots its board's image.

zram is on by default with the shared region, as it was with the UHD carveout. **Auto** is half of the memory Linux keeps while 2160p capture holds its buffers: MemTotal outside the region plus what 2160p leaves of the region, 65 MiB on a NanoKVM PCIe. Devices with their own setting in `/etc/kvm/memory.conf` keep it.

The device never advertises a monitor profile that its video memory cannot capture: a profile saved for more memory is lowered when the server starts on an earlier image.

## Frame rate limits

The frame rate is capped by the larger of the input and the encoded size, in either orientation: up to 1280×720 at 120 fps, up to 1920×1088 at 100 fps, up to 2560×1440 at 60 fps, larger sizes at 30 fps. With the video overclock the encoder sustains about 250 million pixels per second: 3840×2160 at 30 and 2560×1440 at 60 fps; at 1920×1080 a fixed per-frame cost limits it to about 109 fps, so the 1080p monitor is offered at up to 90 Hz, which keeps a margin (the capture table still allows 100 fps). The server and the native capture library use one table (`server/common/video_status.go`, `kvm_mmf/include/internal/capture_rate.hpp`); a test keeps them equal. The saved request is retained across source changes and is restored when the source allows it again. Settings show the delivered rate when it is below the request.

MJPEG is unavailable while the HDMI input is 3840×2160, whatever the stream limit is (next to a 4K stream the JPEG channel wedges the hardware encoder until reboot, and with the stream limited to 1440p it produces no frames): the MJPEG stream, MCP and screenshots, and selecting MJPEG are refused with a message, and `stream.mjpeg` in the capabilities is unavailable with the reason `mjpeg-4k`; use H.265, or H.264 with a stream resolution of 1440p or lower.

H.264 encodes pictures up to 2560×1440, and 1080×1920 in portrait: a long side of at most 2560 and a height of at most 1920, after the stream resolution. At 3840×2160 the H.264 encoder writes corrupted blocks into the stream (white blocks where the picture changes, the same in hardware and software decoders, while H.265 of the same picture is clean; the vendor gives 2880×1620 as its largest frame). A 4K input therefore needs H.265, or a stream resolution of 1440p or lower with H.264 (3840×2160 is then encoded as 2560×1440). The 1440×2560 portrait needs H.265, or a stream resolution of 1080p or lower with H.264 (it is then encoded as 1080×1920). A larger picture is refused for H.264 with a message: a viewer or RustDesk client asking for H.264, selecting H.264 as the device codec, and the native capture library itself. The video settings switch such a resolution to H.265 and mark H.264 unavailable with the reason `h264-4k` (`portrait` for a portrait picture).

## API

- `GET /api/vm/video/capabilities` lists every monitor mode with its refresh rates, the portrait profiles with the codecs and transports they need, the stream limits, the frame-rate table, the input size and rate, the codecs per transport, the codecs each monitor mode can be encoded with at the Auto stream limit (`codecs`: H.265 only for 3840×2160), MJPEG and H.264 availability for the current input and stream limit (`stream.mjpeg`, `stream.h264` with its size limits), the device transport (`stream.transport`) and the size of the video memory (`videoMemoryMiB`). Unavailable entries carry a reason: `video-memory` (a size the booted video memory cannot capture: an earlier image before its restart), `receiver`, `mjpeg-4k` or `h264-4k`.
- `POST /api/vm/video` (administrators) applies several settings together: `type`, `quality` (MJPEG), `bitRate`, `mjpegChroma`, `height`, `fps`, `portraitResolution`, `portrait`, `monitor`, `confirmPowerCycle`. Every setting is validated before any is applied; the EDID is written at most once, last, and follows `fps`. It returns the new capabilities.
- `GET/POST /api/vm/screen` remain for single settings. GET reports the measured rates, each a moving average over five seconds: `measuredFps` for the main stream (H.264/H.265 while it runs, else MJPEG; `/run/nanokvm/now_fps`, as the OLED shows it), `mjpegMeasuredFps` for MJPEG alone (`/run/nanokvm/mjpeg_fps`) and `measuredKbps` for the H.264/H.265 bit rate. The video menus show the rate of the transport the browser plays.

## Advanced settings

Direct playback (lowest latency by default, or smooth), JPEG color sampling, frame-change detection and HDMI recovery remain available in Video settings. MJPEG adapts quality to viewer delivery pressure and recovers up to the selected quality ceiling. Opening the menu or page reads device settings rather than overwriting them from stale browser storage.

## Video straight from capture

The receiver's frame arrives as packed 4:2:2 (UYVY) in DDR. For MJPEG the VPSS reads it and writes a converted copy, and so it did for every video stream. The H.265 encoder can read the capture buffer itself (osdrv patch 0011, packed 4:2:2 source) and subsample it, so an H.265 stream at the input size skips the VPSS: about 6 ms less from the end of a captured frame to the encoded frame at 1080p60 (14.6 → 8.7 ms in a stand probe), 25 ms less at 2160p30 (58 → 33 ms), and one frame copy less of memory traffic. Measured on the stand: 2560×1440 at 60 fps in 14.9 ms, 3840×2160 at 29 fps in 33 ms. The colours do not change: the encoder gets the receiver's limited-range BT.709 as captured.

The H.264 encoder takes only 4:2:0. The capture bridge has its own 4:2:2 → 4:2:0 writer (Y plane and VU plane, NV21), which the vendor driver uses only for a second YUV sensor. Osdrv patch 0013 enables it for the receiver when the VI parameter `nk_yuv420` is set as the capture starts (`/sys/module/cv181x_vi/parameters/nk_yuv420`); the native library sets it. In this 4:2:0 capture an H.264 stream at the input size reads the capture buffer directly: measured on the stand at 1080p30, 8.3 ms instead of 14.6 ms from the end of a captured frame to the encoded frame, and 414 instead of 668 MB/s of DDR traffic. The VPSS then reads NV21 for every other output, so MJPEG has the capture's 4:2:0 chroma resolution: with the 4:2:2 choice it keeps its own NV16 output, in full-range BT.601, and Video settings says the capture is 4:2:0. The writer needs the capture interface's U/V swap only (the vendor's Y/C and U/V swap breaks the picture with these receivers); the driver turns the writer, its averaging and the swap off again for 4:2:2 capture, which the vendor driver never did.

The capture mode can only change when the capture starts. It is 4:2:0 while an H.264 stream encodes the whole input at the input rate, without mirror or flip and the setting is on. Any even input width works: the writer stores each row at the pitch the frame description gives, the width rounded up to 64 bytes (1088 for a 1080 wide portrait). When a new video encoder, a codec change (H.264 ↔ H.265), the setting or a lower stream rate asks for the other mode, the capture restarts once in it: a short gap and a new key frame. MJPEG or frame detection joining or leaving never changes the mode; without a video stream the mode stays until the next capture start. A 4:2:0 frame whose layout differs from the expected one (that pitch for both planes, VU plane right after the Y plane) is logged once and H.264 reads the VPSS output until the capture restarts.

Both paths are on by default and only for streams at the input size and rate; MJPEG, scaled streams, a stream rate below the input rate (the VPSS drops the frames in between) and mirrored or flipped capture stay on the VPSS. When MJPEG or frame detection reads the same output (above 1920 pixels the video and MJPEG share one) in 4:2:2 capture, or frame detection in 4:2:0 capture, that output returns to the VPSS until they have been gone for 2 s. MJPEG on its own output (the 4:2:2 choice, without frame detection) leaves the video output straight from capture, with H.265 and with H.264 in 4:2:0 capture alike. Next to a video stream the server captures MJPEG at up to 30 fps and its own VPSS output runs at up to 33 fps, at every encoded video width. A wider-than-1920 video stream still limits its independent JPEG output to 1920 × 1080. UHD inputs refuse MJPEG entirely. The following older measurements used the three-buffer/no-ready-queue VI policy. With H.265 straight from capture and MJPEG at 30, 1280 × 720 ran at 107-116 fps (120 Hz), 1920 × 1080 at 80-85 (90 Hz) and 57-60 (60 Hz), as with MJPEG at 27; at 2560 × 1440, 30 over 33 was worse than 25 over 27. The native JPEG reader waits for its frame and encodes outside the capture lock, allowing concurrent native video reads; video reads leave MJPEG's queued frame in place, and an MJPEG read no longer takes a frame from an output straight from capture (that dropped the video stream's next frame and logged a VI error on every empty read). In those earlier measurements, each JPEG cost the video stream about one frame. Measured on the stand at 1080p90 with H.264 and an MJPEG viewer: H.264 83-87 fps next to MJPEG at 30 fps (90 alone). At 2560 × 1440 and 60 fps, H.264 45 fps next to MJPEG at 25 (60 alone): the video stream pre-submits its next frame next to MJPEG on its own output, and those measurements used three VI capture surfaces up to that size. The server's MJPEG rate stays below the rate of MJPEG's VPSS output: with the server at 30 over an output at 27 the JPEG reader waited for frames and H.264 fell to 22. When the video output returned to the VPSS it was H.264 68 fps, MJPEG 25.7 fps and 1.61 GB/s; MJPEG without a limit took 53 fps and left H.264 32. With the 4:2:0 choice MJPEG shares the video output: in 4:2:0 capture it takes the H.264 stream's NV21 capture frame in a lease of its own (limited range, as MJPEG on a shared output always is next to video) and the VPSS stays off. With H.265 at 1920 × 1080 and 60 fps on that shared output, MJPEG at 30 gave H.265 30 fps and MJPEG 20 (at 27: 33 and 14-17). **Video settings → Video straight from capture** turns both off; the setting is `/etc/kvm/hevc_vi_direct` (`0` off, the name predates H.264) and applies within a second, with one new key frame (H.264 with a capture restart). The switch shows for H.265 when the codec driver has patch 0011 (`/sys/module/cvi_vc_driver/parameters/nk_packed_src`) and for H.264 when the VI driver has patch 0013 (`nk_yuv420`; GET `/api/vm/screen` reports them as `hevcDirectSupported` and `viDirectH264Supported`).

## Capture buffers

On the shared-CMA layout (144 MiB or larger), inputs up to 2560×1440 pixels use four VI source buffers and a ready queue of one frame. A reader delayed by CPU work can take the latest completed frame; the driver replaces an unread queued frame, so it does not accumulate a backlog. The two DMA slots, an encoder lease and the ready frame have separate capacity. The queue depth is set before the VI channel starts. Larger inputs keep two source buffers and no ready queue. Older fixed carveouts and smaller CMA regions keep their previous three-buffer/no-queue policy below that input size, avoiding increased memory requirements before the device boots the shared-CMA layout.

A disabled VPSS output releases its unused private pool after any old VPSS frame has been returned by the encoder. This covers both direct capture and parked outputs. Returning to VPSS recreates and attaches the pool before the output is enabled. Failures retain ownership for a later retry; a pool is never freed while the old frame is leased. An active MJPEG reader on its independent NV16 output keeps its own pool. At 2560×1440, the source pool grows by 7.03MiB, but releasing the direct output's three unused NV21 surfaces returns 15.82MiB, a net reduction of 8.79MiB in these two pool reservations. This is steady direct-mode usage, not a measurement of peak memory during transitions or all mixed-client modes.

With the four-buffer policy, a 2560×1440 at 60 Hz stand was also measured through the server's native capture worker and `ReadMjpeg`, including its pause/resume of video capture. JPEG used its own 1920×1080 output, quality 80 and the 4:2:2 setting. In 20-second steady windows without synthetic load, both H.264 and H.265 stayed near 60 fps with JPEG at 25, 27 or 30 fps when paired with VPSS limits of 27, 30 or 33 respectively. Under approximately 80% total CPU load, H.265 delivered 58.9 fps at 25/27, 58.1 at 27/30 and 56.2 at 30/33; H.264 delivered 58.9 at 27/30. All these windows had zero capture errors and zero `VbFail` increments. These are capture rates before network delivery and duplicate-image suppression; MJPEG viewers share one capture stream. The production pairing is a uniform 30/33; a lower reader rate can preserve more primary-video throughput under CPU load. Raising only the reader to 30 while leaving VPSS at 27 stalled JPEG reads and reduced video to about 22 fps.

A follow-up QHD comparison used the same 20-second windows for a uniform 30/33 and 30/35 pairing. Both delivered about 59.95 video fps and 30 JPEG fps without extra load. At about 80% total CPU, H.265 delivered 58.53 versus 58.24 video fps, and H.264 58.40 versus 58.27; 35 did not improve delivery, so 30/33 is retained. At 1080p60, 30/33 delivered 59.63 H.265 / 59.98 H.264 fps under load; at 720p120, 118.06 / 117.69 fps (about 88% CPU). JPEG stayed at 30 fps and all these windows had zero capture errors and zero `VbFail` increments. Individual loaded windows vary; these are bounded stand comparisons rather than a guaranteed minimum frame rate.

A later UHD retest temporarily lifted the input-size guard while keeping JPEG at 1920×1080, quality 80 and 4:2:2. H.265 alone delivered 29.6 fps. A fresh process requesting 30 JPEG/s stalled VPSS frame delivery after initial frames (1.25 video fps and 2.80 JPEG fps averaged over the 20-second window, with capture errors); requesting 10 JPEG/s also failed to sustain either stream. Restarting capture restored H.265 alone to 29.0 fps without a device reboot. The input-size guard remains: these results do not qualify UHD MJPEG, including a downscaled JPEG output. UHD retains two VI source buffers and no ready queue; its buffering was not changed in this experiment.

In a native H.265 test at 2560×1440 and 60 Hz with about 89% CPU load, the ready queue raised delivery from 51.2 to 60.0 fps. Input PTS to encoded availability stayed near 14.9 ms median and 18.5 ms at the 99th percentile. The baseline skipped 132 input sequence numbers in 15 seconds; the new queue skipped none in 12 seconds. This measures the capture-to-encoder stage, not display or network latency.

`VbFail` counts failed immediate attempts to replenish the VI queue. The driver can still queue a returned block through its deferred callback before another frame needs it; this counter alone does not show dropped video frames. In the pinned VI driver, `LostFrame` is printed but never updated. Diagnose delivery with VI completion/frame sequence counts, deferred-buffer timing and actual encoded frames instead.

## Colour

The HDMI receivers send limited-range BT.709 YCbCr. The VPSS converts its input to RGB with a group matrix and each YUV output back with the output's own. The native library sets the receiver's matrix at the input, so the VPSS works in true RGB: the video outputs convert back to limited-range BT.709 (osdrv patch 0010; the H.264 and H.265 streams say so in their VUI), and NV16, which only JPEG takes, to full-range BT.601, as JPEG decoders read it. Before, YUV passed through unchanged and JPEG came out pale (black at 16, white at 235): a dark taskbar of (40, 44, 58) showed as (51, 54, 65) in MJPEG and screenshots; it is now (40, 44, 58) in JPEG and H.265 alike. With 4:2:0 sampling, frame detection or after the 4:2:2 fallback, MJPEG reads the NV21 video output: it is switched to full-range BT.601 while only MJPEG reads it and back to limited-range BT.709 as soon as an H.264 or H.265 read arrives, until video has been quiet for 2 s (a new matrix makes the driver drop the queued frames; the frame in flight is skipped). MJPEG viewed next to an H.264 or H.265 stream in those modes stays a little pale. The video streams stay limited range: RustDesk converts every stream as limited-range BT.601 and ignores the VUI. The kernel module and the native library must come together: on a kernel without patch 0010, `/proc/cvitek/vpss` does not show `BT709` and the library keeps the vendor matrices.

JPEG quality 50 is encoded as 51: the encoder rejects a quality factor of exactly 50, and the failure used to switch MJPEG to the 4:2:0 fallback until restart once the adaptive quality reached it.

The source decides the RGB range on HDMI. In CE video formats such as 1920×1080 at 60 Hz it sends limited-range RGB (16–235) unless the monitor says it accepts either. The receivers convert RGB to YCbCr as if it were full range, so black arrived as 16 in every codec. Every monitor profile except the stock one carries a CTA Video Capability Data Block with QS=1 (`scripts/edid-rgb-range.py`, the last step of the profile build), and the source may then send full-range RGB. An NVIDIA card then does so by default: on the stand the dark taskbar went from (40, 44, 58) to (27, 33, 48) and black from 16 to 0. QY stays 0, because the capture expects limited-range YCbCr from the receiver. A source that ignores the block, or is set to limited range by hand, still sends limited range; set its output to full range (NVIDIA: *Output dynamic range*).

A device updated from a release without the block keeps the profile it programmed. At the first start the server finds the shipped profile whose earlier version has the recorded hash and programs the current version of it once, with the same resolution and rate. A Cube is left alone, since every EDID write there needs a power cycle; it gets the block with the next profile it programs.

# Video and HDMI settings

## Transports and defaults

A fresh browser uses the **device transport**, which is **H.265 Direct** over HTTPS until an administrator chooses another. Browser preferences that already exist are kept. A browser without secure WebCodecs falls back to WebRTC, then to MJPEG when WebRTC is unavailable. H.265 falls back to H.264 when the browser cannot decode it over the chosen transport. H.265 is available over Direct and WebRTC.

The device transport (`/kvmapp/kvm/transport`: `direct`, `webrtc` or `mjpeg`) is the transport an administrator last applied in **Settings → Video** or the quick video menu. A browser that has never chosen a transport of its own opens the video with it. Browsers that chose different transports can watch at the same time: each transport is one more subscriber of the same encoder, and only a different codec is refused. When an administrator's browser plays another transport than the device's, the video settings offer to make it the device transport. The RustDesk add-on does not use the device transport: it accepts a WebRTC connection whenever the client offers one, and TCP or relay otherwise.

The quick video menu shows the measured stream size, frame rate and bitrate. Its Mode, Bitrate and Transport rows change those settings. Browser scale and a link to **Settings → Video** are also there.

## Video modes and bitrate

A video mode combines the monitor resolution and the stream frame rate. The page separates the shared picture settings from this browser's transport and playback settings. An unavailable choice says why: hardware, memory, permission or browser. A resolution that the booted video memory cannot capture says so (see [Video memory](#video-memory)).

The **bitrate** is a ceiling for motion, from 1 to 20 Mbit/s. New devices use 20 Mbit/s, which keeps QHD@60 sharp in motion; 3 Mbit/s suits remote and mobile connections. A still desktop sends much less than the ceiling.

The encoder applies its rate-control limits before capture starts and uses NormalP, with a keyframe when a viewer asks for one and at least every ten seconds. There are no GOP or SmartP settings. H.264 uses the Main profile. Native encoder diagnostics go to the application log.

## HDMI receivers

NanoKVM boards use one of three Lontium HDMI-to-CSI receivers: **LT6911C**, **LT6911D** or **LT6911UXC**. The board model selects the register protocol. The receiver runs its own MCU firmware; NanoKVM OS does not install or change it.

**Signal detection.** The detector reads the receiver over I2C when the receiver's interrupt line (GPIOE1) signals a change, so a healthy, unchanged input causes no periodic reads. If the interrupt line is unavailable, the detector logs it, polls once per second and tries to get the line back every 5 seconds. Fresh VI faults (no input frames, CSI size mismatch) also trigger a read, so a missed interrupt is recovered. A transient I2C error keeps the last valid geometry; three failed reads in a row withdraw the signal. A confirmed disconnect is immediate. Failed reads are retried once per second.

**Quiet window after reset.** After the receiver is released from reset it must see no I2C until it has loaded its firmware: 2.5 s for the LT6911C and LT6911D, 250 ms for the LT6911UXC (1 s at service start). Until then the detector reports the input as settling. This is a minimum, not a promise that the receiver has locked to the source.

**Diagnostics.** On changes, the detector logs the receiver's identity, firmware bytes and HDMI/CSI timings to the application log. The fields available differ by receiver. Diagnostics are read-only and never change the capture geometry or the signal state.

Register maps: [Rockchip C](https://github.com/rockchip-linux/kernel/blob/e81982d9e242815c9ce1b1e3f2db2ce7450c94d4/drivers/media/i2c/lt6911c.c), [Comet C](https://github.com/macpit/glkvm-rm1pe-kernel/blob/d38a0af2fee3ae49542c21149e896a45a271096a/patches/0004-lt6911c-hdmi-capture-and-cold-lock.patch), [Espressif D](https://github.com/espressif/esp-video-components/blob/b413578f9186a814f7df2bd04442e796c978fae3/esp_cam_sensor/sensors/lt6911/lt6911.c), [Rockchip UXC](https://github.com/rockchip-linux/kernel/blob/470f9dccbdc42e7b8a824d0a5c5640a10e9457d2/drivers/media/i2c/lt6911uxc.h), [ZHAW UXC](https://github.com/InES-HPMM/Lontium_lt6911uxc/blob/c6a314423d1209df82a7c669f45ae7a765601715/source/lt6911uxc_regs_zhaw.h).

## Monitor profiles

The monitor profile advertises a preferred mode and lower fallbacks through EDID. The landscape profiles prefer 3840×2160, 2560×1440, 1920×1080 or 1280×720; the portrait profiles 720×1280, 1080×1920 and 1440×2560 are in the same list. 1080×1920 is the tallest portrait H.264 encodes; 1440×2560 needs H.265, over Direct or WebRTC. A saved 1296×2304 portrait profile is replaced by 1440×2560 (1080×1920 when the device codec is H.264) on the next start.

Each profile is single-rate: at or above its own resolution it lists only its preferred timing (1920×1080 at 75 Hz lists no other 1080p, 1080i or 1440p timing). Windows GPU drivers can ignore the preferred timing and output another listed mode at that resolution, such as 1080p50 or 2560×1440 at 50 Hz. Lower-resolution fallbacks (VGA, 800×600, 720p, and 1024×768 or 1280×1024 where lower than the profile) stay, so a source that cannot drive the chosen timing falls back to a lower resolution; choose a slower profile to get the larger one back. The source may still choose any other supported resolution.

Changing a profile briefly reconnects HDMI. Which profiles are offered, and how a new one takes effect, depends on the receiver and the board:

| Receiver | Profiles | PCIe | Cube / Lite |
|---|---|---|---|
| LT6911UXC | all | applied at once (receiver reset) | power cycle after each write |
| LT6911D | all | applied at once | applied at once (software replug) |
| LT6911C | Cube / Lite: 720p and 1080p at 60 Hz, plus the slow wide sizes below | stock EDID; programming unsupported | power cycle after each write |

On a Cube or Lite that needs a power cycle and has no recorded profile hash, Video settings reports an unrecorded receiver profile (`stockProfile` in `/api/vm/video/capabilities`) instead of guessing a size and rate. That is the stock EDID or one written without a recorded hash.

**LT6911D on a Cube or Lite.** These boards have no receiver reset line, and while the EDID tool holds register access the receiver firmware ignores input events, so a mode change during the write would be missed. `nanokvm_update_edid` therefore follows the write with a software replug: 3 s after releasing register access it drops HPD and the input termination for 2 s and restores them, so the source reads the EDID again.

**Receiver checks at start.** A Cube receiver keeps its EDID in flash across reinstalls. On the first start of a Cube with an LT6911C, the server reads it once; if it advertises a mode beyond 720p/1080p at 60 Hz, it programs the saved 60 Hz profile and asks for a power cycle. On a PCIe board with an LT6911UXC or LT6911D, and on a Cube or Lite with an LT6911D, the server reads the receiver on the first boot after an installation or system update and programs the saved profile again when the receiver holds another EDID; on PCIe this resets the receiver and delays the first frames by about 1.25 s. `/etc/kvm/monitor_edid_verified` records the profile and system version last confirmed. A profile deferred by a video-memory switch is applied after the new image boots; a failed write is retried on the next server start.

### 16:10, 21:9 and 8:9

An LT6911UXC or LT6911D also offers 1920×1200 and 2560×1600 (16:10), 2560×1080 and 3440×1440 (21:9), and 1280×1440 and 2560×2880 (8:9: half of a QHD screen, for a remote screen that fits beside other windows without scaling, and two QHD screens one above the other); a Cube or Lite with an LT6911UXC needs a power cycle after the write and uses the fastest rate of the size. On Cube / Lite, an LT6911C offers the two sizes near 1920×1080 at rates within its 148.5 MHz pixel clock: 1920×1200 at 50 and 30 Hz and 2560×1080 at 30 Hz (`slowWiderMonitorRates`). These LT6911C sizes are experimental.

**Settings → Video → Aspect ratio** picks the ratio, then **Resolution** the size. The default is 16:9, and the quick video menu lists the sizes of the current ratio. Each profile uses CVT reduced blanking with the 800×600 fallback, plus only those stock established and standard timings smaller than the profile in both directions, because a GPU otherwise outputs a mode as wide or as tall instead of the preferred one (`scripts/build-wide-monitor-edids.py`). A saved 16:10, 21:9 or 8:9 profile that the receiver does not take reads as Auto in both screen APIs.

The `monitor` setting (`/etc/kvm/monitor_resolution`, the API's `monitor`) holds a profile id: the height of a 16:9 or 16:10 profile, or the width of a 21:9 one and of 1280×1440 (2560 is 2560×1080, 3440 is 3440×1440, 1280 is 1280×1440); 2880 is 2560×2880. 1280×1440 needs the QHD video memory although it has fewer pixels than 1920×1080: the capture takes more than 1080 lines only with it. `/api/vm/video/capabilities` lists each mode's `id`, size and `aspect`.

Capture and encoder throughput set the top rates: 2560×1600 at up to 50 Hz and 3440×1440 at up to 40 Hz. 3440×1440 is H.265 only, because H.264 and MJPEG stop at 2560 pixels wide. 2560×2880 runs at 30 Hz with the UHD video memory and is H.265 only as well: H.264 encodes at most 1920 lines, MJPEG a long side of at most 2560.

### Monitor identity

Every profile identifies the monitor as NanoKVM's: manufacturer ID `SPD`, product code 0x3301, monitor name `NanoKVM`, and a serial number of its own per profile (number and text, such as `NKVM1080-90`).

**Settings → Video → Monitor identity** replaces those fields for a computer that should see another monitor: the three-letter PNP manufacturer ID, the product code, the monitor name (at most 13 printable ASCII characters), the week and year of manufacture, and optionally a fixed serial number with its text. The fields can be typed, or taken from a monitor's EDID file (binary, as `/sys/class/drm/*/edid` or CRU saves it, or a text hex dump such as Monitor Asset Manager's); the browser reads the file and uses only the identity. The modes stay the profile's, so software that reads the whole EDID can still recognise a NanoKVM by its short mode list.

Without a fixed serial number each profile keeps its own, and the serial text becomes that number in decimal: Windows remembers the display mode per monitor identity, so distinct serials keep a mode chosen under one rate profile from returning after a switch to another. A fixed serial number lets a headless workstation see its usual monitor, at the cost of that behaviour.

The server writes the identity into the base block of the profile it programs (through `/run/nanokvm-monitor-edid.bin`) and records the hash of what it wrote (`server/common/monitor_identity.go`). The identity is saved in `/etc/kvm/monitor_identity` only after a successful write and applies to every later profile change. Restoring NanoKVM's identity deletes the file and writes the profile as shipped. Like any EDID write, it briefly reconnects HDMI, and a Cube or Lite with an LT6911C or LT6911UXC needs a power cycle afterwards.

### Refresh follows the frame rate

When video settings are applied, the monitor uses the slowest refresh rate of its profile that is not below the stream frame rate:

| Profile | Refresh rates (Hz) |
|---|---|
| 1280×720, 720×1280 | 120, 60, 30 |
| 1920×1080, 1080×1920 | 90, 75, 60, 30 |
| 2560×1440 | 60, 50, 40, 30 |
| 1440×2560 | 60, 50, 30 |
| 3840×2160 | 30 |
| 1920×1200, 2560×1080, 1280×1440 | 60, 50, 30 |
| 2560×1600 | 50, 30 |
| 3440×1440 | 40, 30 |
| 2560×2880 | 30 |

A 30 fps stream therefore makes the computer render and send 30 frames per second instead of 90. Changing the frame rate through the single-setting API also resynchronizes the EDID, when a different timing results.

On Cube/Lite boards that need a power cycle after every EDID write, the monitor does not follow the stream rate: it keeps the fastest rate of the chosen resolution (LT6911UXC) or the single 60 Hz profile (LT6911C), and the stream rate only limits the encoder.

When the input is faster than the stream (an unchanged monitor, or a source that ignores the preferred mode), the VPSS drops the surplus input frames before converting them.

Capture follows the actual HDMI dimensions when the BIOS, bootloader or operating system changes modes; no EDID rewrite is needed. The page shows the input size and its measured refresh rate, the encoded size and the measured server frame rate (not the browser's presentation rate), and the monitor profile with its refresh rate.

### Automatic

**Automatic** is 1920×1080 at up to 90 Hz wherever the EDID can be written live (the stock receiver profile elsewhere); 2560×1440 and 3840×2160 are explicit choices. It has two parts:

- *The static profile* is `NanoKVM-monitor-auto.bin`, installed as `NanoKVM-final-video-profiles.bin` and identical to `NanoKVM-monitor-1080-90.bin`: 1920×1080 at 90 Hz (CVT reduced blanking) plus the lower modes. Where the EDID can be written live, an install programs it, and the server uses it when a rate profile file is missing.
- *The runtime selection* applies when video settings are applied with a frame rate, or when the monitor setting changes. Automatic then uses the same files as an explicit 1920×1080 setting, `NanoKVM-monitor-1080-<hz>.bin` with the slowest rate not below the stream rate: 90 Hz for 76 fps and up (including 100 and 120), 75 Hz for 61–75, 60 Hz for 31–60 and 30 Hz up to 30. At 60 fps the computer is offered 1080p60 alone.

A computer that cannot drive the offered timing (a 60 Hz-only source at the default 90 fps) falls to a lower resolution such as 720p, because nothing else at 1080 is listed. Lower the stream frame rate so that Automatic selects a slower profile (60 fps gives 1080p60), or choose a slower explicit profile. Neither profile carries a 1080p60 fallback, because a GPU driver that ignores the preferred timing would then output 60 Hz instead of 90 Hz. Automatic follows no feedback from the source.

## Stream resolution

**Same as input** uses the actual source dimensions. **Up to 2160p / 1440p / 1080p / 720p / 600p** fits the source within that bounding box, keeps the aspect ratio and never enlarges a smaller image. NV21 dimensions are rounded down to even pixels. Every size up to 2160p fits the video memory.

## Video memory

Every boot image has the same video memory: one reusable CMA region of 144 MiB. Linux uses the idle part of the region for movable memory (page cache, programs, zram) and migrates it away when video allocates; outside the region about 90 MiB stay for unmovable kernel memory. MemTotal on a NanoKVM PCIe is 235 MiB, and every resolution up to 3840×2160 fits without a restart. **Settings → Memory** shows the size of the region.

The largest video use is about 103 MiB, for 3840×2160 with H.265; the region leaves room for the encoder's buffers to be allocated even when its pages are full of page cache.

When memory runs out, an allocation fails rather than showing as use: the encoder refuses the frame, the channel is recreated, the log says "video memory exhausted", and video resumes once memory is free.

After a kernel update and before the restart, the device still runs on the fixed video carveout of its earlier image (40, 56 or 104 MiB) and refuses what that carveout cannot capture; the capture status asks for the restart. A monitor profile saved for more memory is lowered when the server starts on such an image.

zram is on by default. **Auto** is half of the memory Linux keeps while 2160p capture holds its buffers: MemTotal outside the region plus what 2160p leaves of the region, 65 MiB on a NanoKVM PCIe. Devices with their own setting in `/etc/kvm/memory.conf` keep it.

## MJPEG: changed tiles only

The web page opens `GET /api/stream/mjpeg/tiles` (a WebSocket) and draws on a canvas. The server sends only the 128×128 tiles that changed since the previous message, each a JPEG, and nothing while the screen is still.

- **Finding changes.** The IVE compares each captured frame with a kept copy in 16×16 blocks (SAD, see [Accelerators](ACCELERATORS.md#ive)). Each read compares every other row of both planes, even rows on one read and odd rows on the next: luma for brightness, chroma for a colour change at equal brightness. A change one pixel row high is sent at most one frame later; anything taller at once. A tile is sent when it covers a changed block; tiles of the last column and row move inward and overlap their neighbours. The kept copy takes only what was sent.
- **Encoding.** A tile is a region of the captured frame handed to the JPEG encoder in place, without a copy, on a JPEG channel of its own. Whole pictures use another channel.
- **Whole pictures** go to a new viewer, after a failure, to a viewer that fell behind (8 queued messages), every 5 s, and when more than 24 tiles changed.
- **Message** (little-endian): `u8 version (1)`, `u8 flags (1: whole picture)`, `u16 count`, `u16 width`, `u16 height`, then `count` records `{u16 x, u16 y, u16 w, u16 h, u32 bytes}`, then the JPEGs in order (`kvm_vision.h`). The browser decodes the tiles of a message together and draws messages in order.
- **Fallbacks.** With a classic MJPEG viewer connected, every read is a whole JPEG and tiled viewers get it as a whole-picture message. Without a change map, tiled viewers get whole pictures, sent only when they differ: without the IVE driver fix (osdrv patch 0020, the `timeout_ms` parameter of `cv181x_ive`), for inputs wider or taller than 1920, a width that is not a multiple of 16, a frame packed straight from capture, or a copied frame. The browser uses tiles where `stream.mjpeg.tiles` in the capabilities says the server sends them, and the classic stream otherwise.
- **Pace.** A picture is read when a viewer will take it: nothing is queued for it, and the message it is writing is due to end within one capture tick plus the usual read time, by its usual write time. A picture read sooner would wait behind the write and be replaced by the next one. A tick with no such viewer counts as busy for the adaptive quality, so on a slow link the quality steps down, for whole pictures as for changed tiles.
- **Memory.** The kept planes (4 MiB at 1080p with 4:2:2, 3 MiB with 4:2:0) and the two JPEG channels are freed when the last MJPEG viewer leaves.
- **Hidden tab.** A browser tab hidden for 5 s closes its MJPEG stream, tiled or classic, unless it is recording, and opens it again as soon as it is shown, starting with a whole picture; without other viewers the device stops reading and encoding JPEGs at once and parks the capture outputs 20 s later. A WebRTC viewer in a hidden tab keeps its connection, with its audio, and asks the device after 5 s to send it no video, unless it is recording: it is then no viewer (the encoder stops with the last one), and it joins again with a key frame when the tab is shown.
- **Encoder faults.** A tile whose stream never comes is retired by destroying its channel; every later read retries a destroy that failed.

Effect at 1920×1080 and 60 Hz, one viewer, a running stopwatch on screen:

| | Classic MJPEG | Tiles |
|---|---|---|
| Frames per second | 47 | 55 |
| Sent | 40.4 Mbit/s | 2.9 Mbit/s |
| Device CPU busy | 88 % | 40–44 % |

A tiled read takes about 10 ms at 1080p, most of it the IVE map while the CPU sleeps, plus about 0.7 ms per tile. Scrolling or a video changes most tiles and falls back to whole pictures at the classic rate.

Next to a video stream, MJPEG and video reads run side by side. With 4:2:0 MJPEG chroma, MJPEG shares the video output, and the two take turns.

## Frame rate and codec limits

The frame rate is capped by the larger of the input and the encoded size, in either orientation: up to 1280×720 at 120 fps, up to 1920×1088 at 100 fps, up to 2560×1440 at 60 fps, larger sizes at 30 fps. With the video overclock the encoder sustains about 250 million pixels per second: 3840×2160 at 30 and 2560×1440 at 60 fps. At 1920×1080 a fixed per-frame cost limits it to about 109 fps, so the 1080p monitor is offered at up to 90 Hz. The server and the native capture library use one table (`server/common/video_status.go`, `kvm_mmf/include/internal/capture_rate.hpp`). The saved request is kept across source changes and restored when the source allows it again. Settings show the delivered rate when it is below the request.

**MJPEG** is unavailable while the HDMI input is 3840×2160, whatever the stream limit: next to a 4K stream the JPEG channel wedges the hardware encoder until reboot, and with the stream limited to 1440p it produces no frames. The MJPEG stream, MCP and screenshots, and selecting MJPEG are refused with a message, and `stream.mjpeg` in the capabilities is unavailable with the reason `mjpeg-4k`. Use H.265, or H.264 with a stream resolution of 1440p or lower.

**H.264** encodes pictures up to 2560×1440, and 1080×1920 in portrait: a long side of at most 2560 and a height of at most 1920, after the stream resolution. At 3840×2160 the H.264 encoder writes corrupted blocks into the stream (white blocks where the picture changes); the vendor gives 2880×1620 as its largest frame. A 4K input therefore needs H.265, or H.264 with a stream resolution of 1440p or lower (encoded as 2560×1440). The 1440×2560 portrait needs H.265, or H.264 with a stream resolution of 1080p or lower (encoded as 1080×1920). A larger picture is refused for H.264 with a message: a viewer or RustDesk client asking for H.264, selecting H.264 as the device codec, and the native capture library itself. The video settings switch such a resolution to H.265 and mark H.264 unavailable with the reason `h264-4k` (`portrait` for a portrait picture).

## API

- `GET /api/vm/video/capabilities` lists every monitor mode with its refresh rates, the portrait profiles with the codecs and transports they need, the stream limits, the frame-rate table, the input size and rate, the codecs per transport, the codecs each monitor mode can be encoded with at the Auto stream limit (`codecs`: H.265 only for 3840×2160), MJPEG and H.264 availability for the current input and stream limit (`stream.mjpeg`, `stream.h264` with its size limits), the device transport (`stream.transport`), the monitor identity the profiles carry (`monitor.identity`, null for NanoKVM's own) and the size of the video memory (`videoMemoryMiB`). Unavailable entries carry a reason: `video-memory` (a size the booted video memory cannot capture, before the restart after a kernel update), `receiver`, `mjpeg-4k` or `h264-4k`.
- `POST /api/vm/video` (administrators) applies several settings together: `type`, `quality` (MJPEG), `bitRate`, `mjpegChroma`, `height`, `fps`, `portraitResolution`, `portrait`, `monitor`, `monitorIdentity` (`custom: false` restores NanoKVM's identity; otherwise `custom: true` with `manufacturer`, `productCode`, `name`, `serialNumber` (null: each profile's own), `serialText`, `week` and `year`), `confirmPowerCycle`. Every setting is validated before any is applied; the EDID is written at most once, last, and follows `fps`. It returns the new capabilities.
- `GET/POST /api/vm/screen` handle single settings; GOP and SmartP are not accepted. GET reports the measured rates, each a moving average over five seconds: `measuredFps` for the main stream (H.264/H.265 while it runs, else MJPEG; `/run/nanokvm/now_fps`, as the OLED shows it), `mjpegMeasuredFps` for MJPEG alone (`/run/nanokvm/mjpeg_fps`) and `measuredKbps` for the H.264/H.265 bit rate. The video menus show the rate of the transport the browser plays.

## Advanced settings

Direct playback (lowest latency by default, or smooth), JPEG colour sampling, frame-change detection and HDMI recovery are in Video settings. MJPEG adapts quality to how fast viewers take frames, and recovers up to the selected quality ceiling. Opening the menu or page reads the device settings rather than overwriting them from browser storage.

## Watermark

**Settings → Video → Watermark** (administrators) puts up to eight blocks of text and pictures into every stream: H.264, H.265, MJPEG, screenshots, recordings and RustDesk. The VPSS draws them into each output (the [region engine](ACCELERATORS.md#dwa-and-rgn)), so a viewer cannot remove them and they are the same for every viewer.

- **Blocks.** The editor composes each block from text (up to three lines, font size, colour, bold), an optional logo and a background, with an opacity, and sends it as a PNG with 8-bit alpha. A block is drawn for a 1920-pixel-wide picture, at most 960 × 270 and 256 KiB of PNG; all blocks together have at most as many pixels as one such block and 360 KiB of PNG. Each is scaled to the stream's width, so it covers the same share of every stream.
- **Placement.** Blocks are dragged in the preview, or put in a corner, at an edge or in the centre. A place is kept as a fraction of the free room inside the margin, so a layout lands the same way on 16:9, 16:10, 21:9 and portrait pictures. Blocks must not overlap: the VPSS draws at most two overlapping regions (two layers of eight).
- **Cost.** Video no longer goes [straight from capture](#video-straight-from-capture) while the watermark is on: every stream goes through the VPSS, about 6 ms more latency at 1080p60 and 25 ms at 2160p30. The video path status gives the watermark as the reason. The blocks take video memory on every output: up to about 4 MiB per output at 2160p.
- **Machine readers.** Screen text (OCR), crash-screen detection and MCP screenshots read the same outputs, so they see the watermark too.

File: `/etc/kvm/watermark.json` (on/off, margin, block places and pictures, the editor's design), replaced whole on every change and part of the settings that backup, restore and reset handle. API: `GET` and `POST /api/vm/video/watermark` (administrators). `POST` takes `enabled`, `margin`, `design` and `blocks`, each `{x, y, image}` with `x` and `y` in thousandths of the free room (0 left or top, 1000 right or bottom) and `image` a base64 PNG; a block without `image` keeps the saved picture of the same index.

## Video straight from capture

The receiver's frame arrives in DDR as packed 4:2:2 (UYVY). Normally the VPSS reads it and writes a converted copy for the encoder. Video straight from capture lets the encoder read the capture buffer itself, which saves one frame copy of memory traffic and about 6 ms from the end of a captured frame to the encoded frame at 1080p60 (about 25 ms at 2160p30). The colours do not change.

- **H.265** reads the packed 4:2:2 capture buffer and subsamples it (osdrv patch 0011).
- **H.264** takes only 4:2:0. The capture bridge then writes 4:2:0 (NV21) itself (osdrv patch 0013, VI parameter `nk_yuv420`, `/sys/module/cv181x_vi/parameters/nk_yuv420`, set by the native library as capture starts). In this 4:2:0 capture the VPSS reads NV21 for every other output, so MJPEG gets 4:2:0 chroma resolution; Video settings says the capture is 4:2:0.

**When it is on.** Only for a stream at the input size and rate, without mirror or flip, and without a [watermark](#watermark). MJPEG, scaled streams, a stream rate below the input rate and mirrored or flipped capture use the VPSS. When MJPEG or frame detection reads the same output as video (above 1920 pixels the two share one) in 4:2:2 capture, or frame detection in 4:2:0 capture, that output returns to the VPSS until they have been gone for 2 s. MJPEG on its own output (the 4:2:2 choice, without frame detection) leaves video straight from capture. Next to a video stream the server captures MJPEG at up to 30 fps, its own output limited to 1920×1080.

The capture mode (4:2:2 or 4:2:0) can change only when capture starts. It is 4:2:0 while an H.264 stream encodes the whole input at the input rate and the setting is on. When a new encoder, a codec change, the setting or a lower stream rate needs the other mode, capture restarts once: a short gap and a new key frame. MJPEG or frame detection joining or leaving never changes the mode. For the first capture start the server picks the mode the device's own stream will want (4:2:0 for an H.264 device codec over WebRTC or Direct at the input size and rate); a wrong guess costs one restart.

**The switch.** **Video settings → Video straight from capture** turns both paths off. The setting is `/etc/kvm/hevc_vi_direct` (`0` off; it covers H.264 too) and applies within a second, with one new key frame (for H.264, with a capture restart). The switch shows for H.265 when the codec driver has patch 0011 (`/sys/module/cvi_vc_driver/parameters/nk_packed_src`) and for H.264 when the VI driver has patch 0013. GET `/api/vm/screen` reports them as `hevcDirectSupported` and `viDirectH264Supported`.

## Capture buffers

With the 144 MiB shared CMA region, inputs up to 2560×1440 use four VI source buffers and a ready queue of one frame: a reader delayed by CPU work takes the latest completed frame, and an unread frame is replaced rather than queued, so no backlog builds up. Larger inputs, and smaller or fixed video memory, use fewer buffers and no ready queue. Startup logs the buffer count, queue depth and reason.

A VPSS output that is turned off (video straight from capture, or no readers) releases its private buffer pool once the encoder has returned its last frame, and gets it back before it is enabled again.

Once the detector reports no HDMI signal, video reads are refused. Before that, a reader can still receive the last frame completed before the source stopped.

## Colour

The HDMI receivers send limited-range BT.709 YCbCr. The native library sets the receiver's matrix at the VPSS input, so the VPSS works in true RGB (osdrv patch 0010). The video outputs convert back to limited-range BT.709, which the H.264 and H.265 streams declare in their VUI; the NV16 output, used only for JPEG, converts to full-range BT.601, as JPEG decoders expect. With 4:2:0 sampling, frame detection or the 4:2:2 fallback, MJPEG reads the NV21 video output: it is switched to full-range BT.601 while only MJPEG reads it, and back to limited-range BT.709 when an H.264 or H.265 read arrives, until video has been quiet for 2 s. MJPEG next to a video stream in those modes is therefore slightly pale. RustDesk converts every stream as limited-range BT.601 and ignores the VUI. The kernel module and the native library must match: on a kernel without patch 0010, `/proc/cvitek/vpss` does not show `BT709` and the library keeps the vendor matrices.

JPEG quality 50 is encoded as 51, because the encoder rejects a quality factor of exactly 50.

The source decides the RGB range on HDMI. In CE video formats such as 1920×1080 at 60 Hz it sends limited-range RGB (16–235) unless the monitor says it accepts either, and the receivers convert RGB as if it were full range, so black would arrive as 16. Every monitor profile except the stock one carries a CTA Video Capability Data Block with QS=1 (`scripts/edid-rgb-range.py`, the last step of the profile build), so the source may send full-range RGB; NVIDIA cards do so by default. QY stays 0, because capture expects limited-range YCbCr from the receiver. A source that ignores the block, or is set to limited range by hand, still sends limited range: set its output to full range (NVIDIA: *Output dynamic range*). The stock EDID of an LT6911D declares no RGB range, which is why its first start programs the saved profile.

A device that programmed a profile without the block gets the current version of the same profile (same resolution and rate) once, on its first start. A Cube or Lite that needs a power cycle after every EDID write reads its receiver once instead, and if the EDID lacks the block, programs the saved profile (an LT6911C its Cube profile) and shows the power-cycle reminder; the colours are right after the next power cycle. `/etc/kvm/monitor_rgb_range_checked` records that the check ran.

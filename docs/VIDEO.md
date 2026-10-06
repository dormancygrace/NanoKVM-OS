# Video and HDMI settings

Fresh-browser default: **H.265 Direct** over HTTPS. Existing browser preferences are retained. Browsers without secure WebCodecs fall back to WebRTC, then MJPEG when WebRTC is unavailable; H.265 falls back to H.264 when the browser cannot decode it for the chosen transport.

H.265 is available over WebRTC again. It was disabled in v2.1-b1 because the device could freeze while CryptoDMA encrypted SRTP; the AES driver now runs ciphers out of place, which removed the freeze.

The quick video menu shows what is streamed now (size, measured frame rate, bitrate) and switches between the profiles below with one click; browser scale and a link to **Settings → Video** stay there. The capture button is green while capture is enabled and amber while disabled; its tooltip names the next action.

## Profiles

A profile sets the monitor, the stream and the player together, adapted to the device and the browser. A profile that cannot apply says why (for example: not enough video memory, or the browser cannot play it).

| Profile | Monitor | Stream |
|---|---|---|
| Recommended | Automatic: 1920×1080 at up to 100 Hz | Same as input, 100 fps, H.265 when the browser plays it, bitrate for motion |
| Sharpest | The largest available: 3840×2160 at 30 Hz, else 2560×1440 at 60 Hz | Same as input, the highest rate, 20 Mbit/s |
| Balanced | 2560×1440 at 60 Hz | 60 fps, bitrate for motion |
| Lowest latency | 1280×720 at 120 Hz | H.264, 120 fps, Direct without a playback buffer |
| Compatible | 1920×1080 | WebRTC H.264, 60 fps, bitrate for motion |
| Low traffic | 1920×1080 at 30 Hz | Up to 1080p, 30 fps, 1 Mbit/s |

**Bitrate for motion** is about 0.05 bit per pixel for H.265 (video playback, scrolling) and 1.5 times that for H.264, rounded up to an offered value: 1280×720@120 8, 1920×1080@60 8, 1920×1080@100 12, 2560×1440@60 12, 3840×2160@30 15 Mbit/s with H.265. A static desktop needs much less; the manual settings show the recommendation for the chosen size and rate. Factory defaults match Recommended: 100 fps, 12 Mbit/s.

Profiles keep a portrait monitor as it is, and do not change the monitor on boards whose receiver needs a power cycle after an EDID write (Cube/Lite). Settings that match no profile are shown as **Custom**. **Manual settings** contains every individual choice; unavailable choices stay visible, disabled, with the reason.

## HDMI monitor and automatic input detection

The monitor profile advertises preferred and fallback modes through EDID. **Automatic** is 1920×1080 at up to 100 Hz wherever the EDID can be written live (the stock receiver profile elsewhere); 2560×1440 and 3840×2160 are explicit choices. The landscape profiles prefer 3840×2160, 2560×1440, 1920×1080 or 1280×720; the portrait profiles 720×1280, 1080×1920, 1296×2304 and 1440×2560 are in the same list. 1296×2304 is the tallest portrait the H.264 encoder takes (at most 4096×2304); 1440×2560 needs H.265, over Direct or WebRTC. A source may choose another supported resolution; applying the profile does not require the source to adopt its preferred timing. Changing a profile briefly reconnects HDMI. Explicit profile switching requires NanoKVM PCIe/LT6911UXC, or a power cycle on Cube/Lite; this restriction does not disable automatic source detection on other receivers.

**The monitor refresh follows the frame rate.** When video settings are applied, the monitor uses the slowest refresh rate of its profile that is not below the stream frame rate:

| Profile | Refresh rates (Hz) |
|---|---|
| 1280×720, 720×1280 | 120, 60, 30 |
| 1920×1080, 1080×1920 | 100, 75, 60, 30 |
| 2560×1440 | 60, 50, 40, 30 |
| 1296×2304, 1440×2560 | 60, 50, 30 |
| 3840×2160 | 30 |

A 30 fps stream therefore makes the computer render and send 30 frames per second instead of 100. These profiles leave out the faster modes of their resolution, because a source otherwise picks the fastest one. Cube/Lite boards keep their single 60 Hz profiles. Changing only the frame rate from the quick menu never rewrites the EDID.

When the input is still faster than the stream (an unchanged monitor, or a source that ignores the preferred mode), VPSS drops the surplus input frames before converting them.

Capture follows the actual HDMI dimensions when the BIOS, bootloader or operating system changes modes. No EDID rewrite is needed for each source transition. The page shows the input size and its measured refresh rate, the encoded size and the measured server frame rate (not browser presentation FPS), and the monitor profile with its refresh rate.

## Stream resolution

**Same as input** uses the actual source dimensions. **Up to 2160p / 1440p / 1080p / 720p / 600p** fits the source within the corresponding bounding box, preserves aspect ratio and never enlarges a smaller image. NV21 dimensions are rounded down to even pixels. 2160p needs 128 MiB of video memory (the default CMA mode); 1440p needs 62 MiB.

Examples:

| HDMI input | Stream limit | Encoded output |
|---|---|---|
| 3840×2160 | 1440p | 2560×1440 |
| 2560×1440 | 1080p | 1920×1080 |
| 1920×1080 | 720p | 1280×720 |
| 1024×768 | 720p | 960×720 |
| 800×600 | 720p | 800×600 |

Scaling occurs in VPSS before encoding. Browser scale only changes local presentation. Stream resolution, FPS and bitrate are device-wide; transport and codec preferences belong to the viewing browser (concurrent encoder constraints still apply).

## Frame rate limits

The frame rate is capped by the larger of the input and the encoded size, in either orientation: up to 1280×720 at 120 fps, up to 1920×1088 at 100 fps, up to 2560×1440 at 60 fps, larger sizes at 30 fps. With the video overclock the encoder sustains about 250 million pixels per second: 3840×2160 at 30 and 2560×1440 at 60 fps; at 1920×1080 a fixed per-frame cost limits it to about 109 fps, so 1080p is offered at 100. The server and the native capture library use one table (`server/common/video_status.go`, `kvm_mmf/include/internal/capture_rate.hpp`); a test keeps them equal. The saved request is retained across source changes and is restored when the source allows it again. Settings show the delivered rate when it is below the request.

## API

- `GET /api/vm/video/capabilities` lists every monitor mode with its refresh rates, the portrait profiles with the codecs and transports they need, the stream limits, the frame-rate table, the input size and rate, the codecs per transport and the video memory. Unavailable entries carry a reason: `video-memory` or `receiver`.
- `POST /api/vm/video` (administrators) applies several settings together: `type`, `quality` (MJPEG), `bitRate`, `gop`, `gopMode`, `mjpegChroma`, `height`, `fps`, `portraitResolution`, `portrait`, `monitor`, `confirmPowerCycle`. Every setting is validated before any is applied; the EDID is written at most once, last, and follows `fps`. It returns the new capabilities.
- `GET/POST /api/vm/screen` remain for single settings.

## Advanced settings

Direct playback (smooth or lowest latency), GOP, H.265 GOP mode, JPEG color sampling, frame-change detection and HDMI recovery are under **Advanced and recovery** in the manual settings. Opening the menu or page reads device settings rather than overwriting them from stale browser storage.

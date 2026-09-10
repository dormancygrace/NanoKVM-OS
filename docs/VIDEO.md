# Video and HDMI settings

Fresh-browser default: **H.265 Direct** over HTTPS. Existing browser preferences are retained. Browsers without secure WebCodecs fall back to WebRTC, then MJPEG when WebRTC is unavailable; unsupported H.265 falls back to H.264.

> **Known issue: QHD H.265 WebRTC is unstable and can freeze or restart the device. Use H.265 Direct for QHD.** The mode remains available with warnings. A self-recovering reboot was observed on 2026-09-10; its root cause is unresolved.

The quick video menu contains stream resolution, frame rate, quality/bitrate, browser scale and a link to **Settings → Video**. The capture button is green while capture is enabled and amber while disabled; its tooltip names the next action.

## HDMI monitor and automatic input detection

The monitor profile advertises preferred and fallback modes through EDID. **Automatic** uses the board's supported default profile (QHD-capable on supported memory configurations). **Prefer FHD 60 Hz** and **Prefer QHD 30 Hz** change the preferred timing, retaining legacy/BIOS fallback timings. A source may choose another supported resolution; applying the profile does not require the source to adopt its preferred timing. Changing a profile briefly reconnects HDMI. Explicit profile switching currently requires NanoKVM PCIe/LT6911UXC; this restriction does not disable automatic source detection on other supported receivers.

Capture follows the actual HDMI dimensions when the BIOS, bootloader or operating system changes modes. No EDID rewrite is needed for each source transition. The current source dimensions and encoded dimensions are displayed separately. Source refresh rate is not guessed from dimensions; the page shows requested FPS and the measured server frame output rate, which is not browser presentation FPS.

## Stream resolution

**Same as input** uses the actual source dimensions. **Up to 1440p / 1080p / 720p / 600p** fits the source within the corresponding bounding box, preserves aspect ratio and never enlarges a smaller image. NV21 dimensions are rounded down to even pixels.

Examples:

| HDMI input | Stream limit | Encoded output |
|---|---|---|
| 2560×1440 | 1080p | 1920×1080 |
| 1920×1080 | 720p | 1280×720 |
| 1024×768 | 720p | 960×720 |
| 800×600 | 720p | 800×600 |

Scaling occurs in VPSS before encoding. Browser scale only changes local presentation. Stream resolution, FPS and bitrate are device-wide; transport and codec preferences belong to the viewing browser (concurrent encoder constraints still apply).

The saved FPS request is retained across source changes. Current QHD capture is capped at 30 FPS, including when it is downscaled; returning to FHD restores a saved 60 FPS request automatically. Neither setting 60 FPS nor receiving HDMI60 proves that every stage delivers 60 frames: inspect the live counters.

## Advanced settings

Transport (WebRTC/Direct/MJPEG), codec and stream settings are available on the Video page. GOP/frame-change detection and HDMI recovery are under **Advanced and recovery**. Opening the menu/page reads device settings rather than overwriting them from stale browser cookies.

# 🚀 NanoKVM OS v2.6-a1 — Same tiny board. More room to breathe.

**📦 Applications v2.6-a1 · 💾 Image v1.1-a1 · 🧪 Alpha**

One C906 core. A small RAM budget. This release makes more of both: **lower streaming CPU use, faster encrypted traffic, Direct playback that actively recovers after dropped frames, and video-memory profiles sized for the picture you actually need.**

> ⚡ **Less overhead behind the scenes. More headroom for your next session.**

This update builds on **v2.5-a1**. Its 4K support, Internet over USB, RustDesk integration and mobile controls remain available; the changes below focus on performance, recovery and memory use.

## ⚡ Give that single core a break

The capture pipeline now uses a native thread to wait for video frames and wake Go only when they are ready. Encoder mappings stay available between frames, and the browser batches Direct acknowledgements instead of sending one for every frame.

Development measurements on the SG2002 test device, using QHD H.265 Direct:

| Metric | Before | After |
| --- | --- | --- |
| Server CPU | 20.6–21.1% | **12.5–14.1%** |
| Context switches per second | ~1,445 | **620–840** |
| Go scheduler handoffs per second | ~246 | **8** |

ATX LEDs use GPIO edge events where supported. ZRAM maintenance runs without spawning a chain of helper processes. Restarting the application during the overclock settling period no longer leaves a stale marker that incorrectly disables the saved overclock on the next boot.

These are development measurements, not a guaranteed result for every source, bitrate or hardware variant.

## 🔐 Faster encryption. More CPU left for video.

C906-specific ChaCha20 and Poly1305 improvements accelerate HTTPS traffic. In the development benchmark, TLS-record encryption rose from **20 to 49 MB/s** for 16 KiB records.

WebRTC batches video packets for authentication and UDP output, including payload types negotiated by Chrome. Multi-buffer HMAC-SHA1 uses the C906 vector unit. At the measured 7 Mbit/s H.265 workload, server CPU fell from **about 51% to 31%** across the WebRTC optimizations.

The SRTP shutdown path also handles a blocked network write without deadlocking. Experimental CryptoDMA descriptor chains are not included.

## 🎬 Direct playback that asks for a fresh start

When Direct drops frames, it now **requests a fresh keyframe** instead of waiting for the natural GOP to arrive. Recovery requests are rate-limited, and an unusually large frame triggers bounded backoff rather than permanently disabling recovery.

The pending-frame queue follows the selected frame rate, and new viewers request the keyframe they need to join an existing stream. Diagnostics report why frames were dropped and how long recovery took. Changing FPS through the single-setting API now updates the corresponding monitor timing too.

This improves recovery; it does not remove bandwidth, decoder or CPU limits.

## 🧠 Spend video memory where it matters

Choose **FHD, QHD or UHD** in Video settings. FHD and QHD offer reusable CMA memory or an optional **Fixed** allocation; UHD uses Fixed memory.

| Mode | CMA | Fixed |
| --- | --- | --- |
| **FHD** | **52 MiB** | **50 MiB** |
| **QHD / QHD portrait** | **68 MiB** | **66 MiB** |
| **UHD** | — | **118 MiB** |

CMA pages can serve Linux while video is idle. Fixed memory stays reserved for capture. New installations default to FHD; existing CMA and Fixed selections migrate to QHD and QHD Fixed respectively, while UHD stays UHD.

The application checks whether the chosen memory mode can support the monitor profile. At startup, an oversized saved profile is lowered to fit the running mode. A synchronized native geometry check prevents stale application state from admitting MJPEG at a detected 4K input.

## 📸 MJPEG alongside QHD video

MJPEG no longer fails simply because the video encoder holds a frame lease. Concurrent QHD H.265 and MJPEG now deliver both streams.

**They still share the same hardware budget:** in development tests, QHD H.265 fell from roughly 59 FPS to 30–33 FPS while MJPEG delivered 24–29 FPS. FHD at a requested 100 FPS remains particularly constrained when both run. MJPEG is unavailable with a 4K HDMI input; use H.264 or H.265 there.

## 🖥️ The monitor timing you selected

EDID profiles stop advertising competing modes at or above their selected resolution. On the tested Windows driver, this made **FHD@75, FHD@100, QHD@50/60 and HD@120** select their intended timings instead of an unrelated fallback.

**Automatic is strict too.** It chooses an FHD timing from the requested stream FPS; the static installation profile is FHD@100. If the source cannot drive that rate, it may fall back to 720p. Lower the stream FPS or choose a slower monitor profile to retain Full HD. Automatic does not detect the source's maximum supported refresh rate.

## 💾 A little more RAM from the full image

Board device trees remove unused small-core/RTOS reservations. The new full image also removes the unused **2 MiB small-core reservation from OpenSBI**.

**Image v1.1-a1 bundles applications v2.6-a1.** An APK update preserves the original image identity and installed FIP: it does not apply this OpenSBI change or reclaim those final 2 MiB. Linux remains **7.2.9-nanokvm-os-r1**, with official Alpine 3.24 packages.

## 🚀 Install or upgrade

**Download sizes:** about **70.1 MB** for the full-image ZIP, or **38.4 MB** for the seven core APK packages. APK may also download updates to installed Alpine packages.

### ✨ Fresh SD card

Extract **NanoKVM-OS-Image-v1.1-a1-apps-v2.6-a1.img.zip** and write the `.img` to an SD card of at least **2 GB**. The system root partition remains **768 MiB**.

### 🔄 Existing installation: v2.0-a2 or newer

After publication, use **Settings → System → Updates**; on a2, use **Settings → Updates → Package updates**. Alternatively, as root:

```sh
apk update
apk upgrade
reboot
```

Keep your settings, user data and independently installed packages. Reboot to activate the updated boot images and video-memory layout. Application/package updates preserve the installed bootloader.

For local APK pins or the older experimental C906 overlay, follow the [upgrade and stock Alpine migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/main/firmware/alpine/README.md).

## 🧪 Alpha status

Both **v2.6-a1** and **Image v1.1-a1** are alpha releases. Development measurements and host tests do not replace hardware acceptance of this exact combined release. The new full-image FIP and physical HDMI mode transitions still require device qualification; no installation was performed during release preparation.

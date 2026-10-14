# 🚀 NanoKVM OS v2.6-a3 — Same tiny board. More room to breathe.

**📦 Applications v2.6-a3 · 💾 Image v1.1-a3 · 🧪 Alpha**

One C906 core. A small RAM budget. A lot more room for your picture. This release brings **a leaner video pipeline, adaptive MJPEG, clearer video controls, faster encryption, configurable USB devices and HTTPS certificates you can manage from the browser.**

> ⚡ **Less work behind the scenes. More of your NanoKVM.**

This release collects the changes since **v2.5-a1**, including the unpublished v2.6-a1/a2 work. 4K, Internet over USB, RustDesk integration and mobile controls remain available.

## 🎬 Better video, with fewer settings to fight

Choose the monitor resolution and frame rate together through **Video modes**. Shared picture settings are separated from this browser's transport and player, and the toolbar provides quick access to **Mode, Bitrate and Transport**.

- **Auto and Economy bitrate**, alongside manual selection.
- **Measured bitrate** next to the actual stream dimensions and frame rate.
- Unavailable combinations explain the limitation. Switching transport respects browser support and the viewer's permissions.
- UHD selection can take you through the required memory-mode change and reboot.

The hardware encoder now applies its rate-control limits **before encoding starts**. Previously, the displayed limits could differ from what the hardware actually used. It uses **NormalP with keyframes on request**, plus a ten-second safety interval; separate GOP and SmartP controls are removed. H.264 remains Main for compatibility.

On the development QHD@60 H.265 test scene—a mostly static desktop with a running stopwatch—the encoder changes reduced traffic from **5.46 to 0.51 Mbit/s** and server CPU from **14.0% to 10.3%**. Moving video still uses the bitrate budget. These are scoped development measurements, not guaranteed rates for every desktop.

## 🖼️ A picture that recovers and adapts

- **Direct playback requests a fresh keyframe** after dropped frames, with bounded recovery and a queue sized for the selected frame rate.
- A stalled encoder gets a bounded recovery path instead of retaining capture buffers indefinitely.
- **MJPEG adapts its JPEG quality** when all viewers fall behind, then raises it again up to the selected quality when delivery recovers.
- Concurrent QHD video and MJPEG can share capture without failing just because the other encoder holds a frame.
- **BT.709 colour information** is carried in H.264/H.265 headers, reducing decoder guesswork between Direct and WebRTC.
- Fix MJPEG over HTTP/2 stopping with an internal stream error after a per-frame write deadline expires.

JPEG colour sampling remains selectable: **4:2:2 by default**, or 4:2:0. MJPEG and video share the hardware budget; concurrent playback can lower delivered FPS. MJPEG and JPEG-based screenshots remain unavailable with a 4K HDMI input.

## 💾 Smaller, simpler video-memory modes

Three fixed allocations replace the CMA/Fixed combinations:

| Mode | Reserved video memory |
| --- | --- |
| **FHD** | **40 MiB** |
| **QHD / QHD portrait** | **56 MiB** |
| **UHD** | **104 MiB** |

New installations default to FHD. Older CMA/Fixed/QHD Fixed selections migrate to QHD, FHD Fixed to FHD, and UHD stays UHD. Changing the mode requires a reboot.

The driver now rejects missing encoder buffers before trying to use them. A full pool alone no longer triggers recovery: the watchdog responds to **sustained allocation failure**, and the application releases stale fallback buffers when native capture recovers.

The HDMI runtime is now the **six-library light build**. Open media components are linked into the shared capture library, while the required vendor algorithm libraries remain separate.

## 🖥️ The monitor timing you actually selected

EDID profiles advertise one refresh rate at the selected resolution, with lower-resolution fallbacks. **Each refresh-rate profile now has its own monitor identity**, preventing Windows from reusing a different profile's cached timing.

Windows may treat a newly selected profile as a new monitor, so layout or scaling can need a one-time adjustment.

**Automatic follows the requested stream FPS** within the available FHD timings. Its static installation profile is FHD@100; a source unable to drive that rate may choose a lower resolution. Select a slower FPS/profile to retain Full HD on a 60 Hz-only source.

## ⚡ More headroom from one C906 core

The native capture thread waits for frames and wakes Go when they are ready. Encoder mappings are reused; Direct acknowledgements and WebRTC packet authentication/output are batched. GPIO events, cached authentication state and fewer helper processes reduce background work.

C906-specific work now covers **ChaCha20, Poly1305, multi-buffer HMAC-SHA1 and kernel GHASH**. The CryptoDMA AES-GCM path overlaps hardware AES with CPU authentication work.

Development tests measured kernel AES-GCM at **18–20 MB/s instead of 11 MB/s** for 1,420-byte messages and **33 MB/s instead of 13 MB/s** for 64 KiB messages. These are cryptographic microbenchmarks, not claimed VPN or browser throughput.

WebRTC audio uses the shared SRTP acceleration path. USB descriptor DMA, sleeping Wi-Fi flow control and interrupted HID polling also receive fixes.

## 🔌 USB that fits the job

- **USB composition presets** explain which functions each combination enables and check the controller's endpoint budget.
- Configure the USB device identity from settings.
- Create and manage a **user disk** for virtual USB storage.
- Set up a managed Linux host's USB serial console using the downloadable helper or its read-only helper disk.
- Serial sessions have explicit lifecycle and ownership handling.
- Create **hotkey sequences** for repeated keyboard actions.

Internet over USB NCM keeps its separate switch and the existing DHCP, DNS, NAT and flow-offload support.

## 🔐 HTTPS, under your control

Upload your own certificate and key in Network settings, or use the optional **acme.sh** integration for issuance and renewal through HTTP-01 or DNS-01.

Certificates can be replaced for new connections without restarting the application. Invalid custom certificates leave the generated certificate available, and ACME operations expose their status and errors in the interface.

## 🧩 Add-ons and package updates

- **acme.sh can also be installed from Software → Add-ons**, with a shortcut to its HTTPS settings.
- **RustDesk stays a separate optional APK**, outside the full image and main release archive. Check for updates and install newer versions directly from the signed package repository, including installations pinned to an older add-on version. Failed or unchanged upgrades now show an error instead of reporting success.
- **Daily package-index refresh**, off by default: enable the checkbox in Software → Packages and choose a time in the device timezone. Only the available-package lists are refreshed; installing or upgrading packages remains manual.

## 🐧 A refreshed foundation

- **Linux 7.2.10-nanokvm-os-r4**, with matching kernel modules.
- **U-Boot 2026.10** and **OpenSBI 1.9** in the full image.
- Official **Alpine Linux 3.24** packages.
- Updated Go, Node.js 24 LTS, frontend and networking dependencies; expanded dependency tracking.
- Corrected first-boot formatting of reused SD cards and application/service recovery.
- Updated public build recipes, pinned sources, output checksums and corresponding-source archive.

The full image removes unused firmware memory reservations. APK updates preserve the installed bootloader and OpenSBI, so those full-image changes require reflashing.

## 📦 Install or upgrade

### ✨ Fresh SD card

Extract **NanoKVM-OS-Image-v1.1-a3-apps-v2.6-a3.img.zip** and write the `.img` to an SD card of at least **2 GB**. The system root partition remains **768 MiB**; the full raw image also includes the boot partition and partition-table space.

### 🔄 Existing installation: v2.0-a2 or newer

After publication, use **Settings → System → Updates**; on v2.0-a2, use **Settings → Updates → Package updates**. Alternatively, as root:

```sh
apk update
apk upgrade
reboot
```

Settings, user data and independently installed packages are retained. Reboot to activate the matching kernel, modules and video-memory layout. Package updates retain the original image identity and installed bootloader.

For locally pinned APKs or the older experimental C906 overlay, follow the [upgrade and stock Alpine migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/main/firmware/alpine/README.md).

## 🧪 Alpha status and verification

Both **v2.6-a3** and **Image v1.1-a3** are alpha builds. Host build, artifact and signature verification results are recorded with the release candidate.

The development measurements above come from the individual changes' documented tests. The a3 core packages have been installed and boot-tested on the PCIe development device. The complete SD image has passed host-side verification; it has not been reflashed in this round. Cube/Lite, all receiver/browser combinations and prolonged concurrent workloads are not covered by those measurements.

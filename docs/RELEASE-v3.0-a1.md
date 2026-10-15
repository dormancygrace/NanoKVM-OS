# 🚀 NanoKVM OS v3.0-a1 — Massive Overhaul

**📦 Applications v3.0-a1 · 💾 Image v1.2-a1 · 🧪 Alpha**

**A major leap for a tiny machine.** NanoKVM OS v3.0 brings a **massive overhaul** across video capture, memory allocation, C906 performance, USB controls, networking and the build system. Deep engineering work throughout the stack makes this release a new chapter for NanoKVM OS.

One C906 core. A small RAM budget. A lot more room for your picture. Discover **a leaner video pipeline, adaptive MJPEG, clearer video controls, faster encryption, configurable USB devices and HTTPS certificates you can manage from the browser.**

> ⚡ **Small hardware. Massive engineering. Every byte counts.**

This release collects the changes since **v2.5-a1**, including the entire unpublished v2.6 alpha series. 4K, Internet over USB, RustDesk integration and mobile controls remain available.

## 🌍 Built by developers. Made welcoming by our translators.

A special thank-you to **[Pavel Borecki (pavelbo)](https://hosted.weblate.org/user/pavelbo/)** for the Czech translation contributed through [#207](https://github.com/dormancygrace/NanoKVM-OS/pull/207). Thank you also to **[Alexander-Ger-Reich](https://github.com/Alexander-Ger-Reich)** and **[polyzium](https://github.com/polyzium)** for the German, English, and Russian localization foundations carried forward from upstream.

**[Hosted Weblate](https://hosted.weblate.org/engage/nanokvm-os/)** deserves its own thanks: the translation platform and hosting that bring this community work together. Translators now have a dedicated place in the in-app **Credits**, with names, languages, and profile links. **[Help us speak your language](https://hosted.weblate.org/engage/nanokvm-os/).**

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
- Correct HEVC sublayer buffering information and Edge's incorrectly halved Direct-video rectangle.
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

The standard media runtime is now the **six-library HDMI-light bundle**. Open media components are linked into the shared capture library, while the required vendor algorithm libraries remain separate. The board service builds directly from this repository **without MaixCDK**, and idle capture parks until a viewer needs frames.

An HDMI input that exceeds the selected memory mode now produces a clear notice naming the mode it needs, with consistent capture status.

## 🖥️ The monitor timing you actually selected

EDID profiles advertise one refresh rate at the selected resolution, with lower-resolution fallbacks. **Each refresh-rate profile now has its own monitor identity**, preventing Windows from reusing a different profile's cached timing.

Windows may treat a newly selected profile as a new monitor, so layout or scaling can need a one-time adjustment.

Saved monitor settings are synchronized on the first server start, with guards around failed input fitting. Fresh installations start at **100 FPS**, matching the Automatic FHD@100 profile; saved preferences are retained.

**Automatic follows the requested stream FPS** within the available FHD timings. Its static installation profile is FHD@100; a source unable to drive that rate may choose a lower resolution. Select a slower FPS/profile to retain Full HD on a 60 Hz-only source.

## ⚡ More headroom from one C906 core

The native capture thread waits for frames and wakes Go when they are ready. Encoder mappings are reused; Direct acknowledgements and WebRTC packet authentication/output are batched. GPIO events, cached authentication state and fewer helper processes reduce background work.

C906-specific work now covers **ChaCha20, Poly1305, multi-buffer HMAC-SHA1 and kernel GHASH**. The CryptoDMA AES-GCM path overlaps hardware AES with CPU authentication work.

Development tests measured kernel AES-GCM at **18–20 MB/s instead of 11 MB/s** for 1,420-byte messages and **33 MB/s instead of 13 MB/s** for 64 KiB messages. These are cryptographic microbenchmarks, not claimed VPN or browser throughput.

WebRTC audio uses the shared SRTP acceleration path. USB descriptor DMA, sleeping Wi-Fi flow control and interrupted HID polling also receive fixes.

The kernel also uses the C906's hardware support for misaligned accesses. Source-owned userspace retains its qualified scalar `-O2` profile; the kernel and modules use `-O3`. Official Alpine packages, including OpenSSL, remain official Alpine builds.

## 🔌 USB that fits the job

- **USB composition presets** explain which functions each combination enables and check the controller's endpoint budget.
- Configure the USB device identity from settings.
- Create a **user disk** for virtual USB mass storage.
- Set up a managed Linux host's USB serial console using the downloadable helper or its read-only helper disk.
- Serial sessions have explicit lifecycle and ownership handling.
- Create **hotkey sequences** for repeated keyboard actions.

Internet over USB NCM keeps its separate switch and the existing DHCP, DNS, NAT and flow-offload support.

## 📶 Wi-Fi that follows your preference

A preferred band is a preference, not a hard lock: a dual-band network can fall back to 2.4 GHz, then return to **5 GHz** when it becomes available again.

Changing the preferred band applies it immediately, and the interface handles an interrupted response without blindly repeating the operation. Driver fixes correct disconnect-event ordering, AIC8800 command handling and sleeping flow control. The SDIO card enters **High Speed before the clock is raised to 50 MHz**.

Alpine's DHCP integration also handles DNS and gateway updates consistently.

**Ethernet settings now show live link status and the current IPv4 address**, refreshing without discarding unsaved edits. Static addresses accept **CIDR notation**, and the gateway is optional for direct computer connections; leaving it empty adds no default route or DNS server through that link.

## 🔐 HTTPS, under your control

Upload your own certificate and key in Network settings, or use the optional **acme.sh** integration for issuance and renewal through HTTP-01 or DNS-01.

Certificates can be replaced for new connections without restarting the application. Invalid custom certificates leave the generated certificate available, and ACME operations expose their status and errors in the interface.

## 🧩 Add-ons and package updates

- **acme.sh can also be installed from Software → Add-ons**, with a shortcut to its HTTPS settings.
- **RustDesk stays a separate optional APK**, outside the full image and main release archive. Check for updates and install newer versions directly from the signed package repository, including installations pinned to an older add-on version. Failed or unchanged upgrades now show an error instead of reporting success.
- **Daily package-index refresh**, off by default: enable the checkbox in Software → Packages and choose a time in the device timezone. Only the available-package lists are refreshed; installing or upgrading packages remains manual.

PicoClaw continues to use its official latest RISC-V release download. RustDesk's C906 build and dependencies are refreshed separately; updating NanoKVM applications alone does not upgrade an installed add-on.

## 🌍 A more complete interface

German, Spanish, French, Japanese and Chinese translations are completed alongside English and Russian. Translation files are prepared for Weblate, plural forms are corrected, and empty translations fall back to English. Czech translations are expanded, and **Hebrew (עברית)** joins the language menu as a partial translation starter, ready for community contributions.

Login password verification no longer holds a global authentication lock while bcrypt runs. Log archiving appends rather than repeatedly rewriting existing archives, OLED detection handles absent hardware more gracefully, and unused frontend dependencies and obsolete compatibility paths are removed.

## 🐧 A cleaner foundation

- **Linux 7.2.10-nanokvm-os-r5**, with matching kernel modules.
- **U-Boot 2026.10** and **OpenSBI 1.9** in the full image.
- Official **Alpine Linux 3.24** packages; the image starts from **minirootfs 3.24.2**.
- **Go 1.27.2** and **Node.js 24.22.0**, plus updated web, networking and Rust dependencies.
- Obsolete MaixCDK, old beta tooling and retired Buildroot OS paths removed.
- Corrected first-boot formatting of reused SD cards. Failed `/data` preparation records its cause, keeps incomplete storage unavailable, and can retry at the next boot without preventing system access.
- Reduced background work, including F2FS background garbage collection and noisy video diagnostics.
- Updated public build recipes, pinned sources, output checksums and corresponding-source archive.

The full image removes unused firmware memory reservations. APK updates preserve the installed bootloader and OpenSBI, so those full-image changes require reflashing.

The full image is **64.0 MiB compressed**. The seven core APKs total **34.3 MiB**; optional add-ons and Alpine dependency updates are additional.

## 📦 Install or upgrade

### ✨ Fresh SD card

Extract **NanoKVM-OS-Image-v1.2-a1-apps-v3.0-a1.img.zip** and write the `.img` to an SD card of at least **2 GB**. Flashing replaces the existing installation; first boot creates/formats `/data` in the remaining card space. The system root remains **768 MiB**, with a separate **64 MiB** boot partition.

### 🔄 Existing installation: v2.0-a2 or newer

Use **Settings → System → Updates**; on v2.0-a2, use **Settings → Updates → Package updates**. Alternatively, as root:

```sh
apk update
apk upgrade
reboot
```

Settings, user data and independently installed packages are retained. Reboot to activate the matching kernel, modules and video-memory layout. Package updates retain the original image identity and installed bootloader.

If core packages were installed from local APK files and remain pinned, replace those selections before upgrading:

```sh
apk update
apk add nanokvm-keys nanokvm-base nanokvm-app nanokvm-release nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002
apk upgrade
reboot
```

Installations still using the historical C906 package overlay should follow the [stock Alpine migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/main/firmware/alpine/README.md#existing-c906-installations). Stock Sipeed firmware, pre-v2 betas and the original v2.0-a1 image require the full image.

## 🧪 Alpha status and verification

Both **v3.0-a1** and **Image v1.2-a1** are alpha builds. Development measurements above describe the documented test setups; they are not guarantees for every receiver, browser or workload.
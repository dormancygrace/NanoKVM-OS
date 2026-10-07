# 🚀 NanoKVM OS v2.5-a1 — Small hardware. A legendary leap.

**📦 Applications v2.5-a1 · 💾 Image v1.0-a1 · 🧪 Alpha**

Your NanoKVM is getting a serious upgrade: **4K support, Internet over USB, the return of H.265 WebRTC with a CryptoDMA fix, optional RustDesk access, and a much better experience from your phone.** Underneath it all: a refreshed Linux kernel, official Alpine packages, hardware-specific optimizations, and a complete build you can reproduce from the public repository's pinned sources.

This release brings together the work since **v2.0-b7**, including the previously unpublished v2.1 changes. Video, networking, remote access, touch controls, packaging and the system underneath them all move forward together.

> ⚡ **More ways to connect. More control from your pocket. More of what this tiny machine can do.**

## ⚡ Less waiting. More NanoKVM.

The interface does much less work to show the same information: five background pollers become **one shared live-status request**, static assets are compressed during the build, and browsers can reuse unchanged assets across visits.

Measurements reported during development on the test device:

| Operation | Before | After |
| --- | --- | --- |
| Idle API requests over 20 seconds | 26 | **7** |
| Cold desktop load | 131 requests / ~1.6 MB | **43 requests / ~530 KB** |
| Repeat desktop load | Full asset transfer | **~4 KB; 42 of 43 assets cached** |
| RustDesk status | ~7 seconds | **~30 ms** |
| Wi-Fi / gateway / VPN status polls | Up to 0.3–0.7 seconds | **10–30 ms** |
| New TLS handshake | 70–95 ms | **18–30 ms** |

These measurements describe the development test setup; browser cache, installed add-ons and device configuration affect the result. Newly generated HTTPS certificates use **ECDSA P-256**; existing certificates are preserved.

## ✨ Settings, brought together

Eight clear top-level entries: **Dashboard, Video, USB, Network, VPN, System, Software and Appearance**. VPN providers share one overview with installation state, version and active profile, then open into their own settings with back navigation.

Shared cards, typography, colours and confirmations make the pages consistent. Dashboard CPU and RAM tiles link directly to their settings, the VPN card includes NetBird, and risky actions such as disabling Wi-Fi/USB, changing HTTPS or overclocking ask for confirmation.

## 🌐 Give the connected computer Internet — over USB

The USB connection gains a new job. Enable **Internet over USB** to share NanoKVM's Ethernet or Wi-Fi uplink with the computer you are managing.

- One independent switch, **off by default**.
- **IPv4 DHCP, DNS and NAT** through USB NCM.
- Keep local USB access when Internet sharing is switched off.
- **Software flow offload** accelerates eligible forwarded TCP/UDP connections, with ordinary NAT as the fallback.
- Respect VPN routing and existing forwarding policies; avoid acceleration when those policies conflict.


## 🎬 Push the picture further

**4K joins the lineup**, with higher landscape frame-rate targets across the familiar resolutions:

| Resolution | Landscape target |
| --- | --- |
| **4K / UHD** | **Up to 30 FPS** |
| **QHD** | **Up to 60 FPS** |
| **Full HD** | **Up to 100 FPS** |
| **HD** | **Up to 120 FPS** |

These are selectable targets, not a promise of sustained output in every configuration. Capture hardware, source timing, codec and operating conditions determine the actual rate. Portrait profiles retain their supported mode limits.

Video profiles now apply monitor resolution, orientation and refresh changes together in a single EDID operation. FPS following and saved portrait preferences behave consistently, and **Same as input** works correctly again.

Higher video clocks and a new **UHD fixed-memory profile with automatic ZRAM sizing** support the expanded video modes. Kernel packages share a compact FIT template instead of carrying repeated complete boot images for every board and memory mode.

### 🔥 H.265 WebRTC returns

This release includes the reviewed **CryptoDMA fix: separate DMA input/output buffers and a burst size of four**. H.265 is available over WebRTC again.

The application also recognizes older loaded drivers and uses software SRTP until the updated module is loaded and the application restarts. WebRTC reuses more allocations and batches UDP output. Explicit codec changes propagate across active viewers, and the selected codec survives restarts.

## 📱 A KVM you can actually use from your phone

The mobile work reaches well beyond fitting a page onto a smaller screen:

- Settings open on the first tap; **Apply** stays reachable.
- Video and panels follow the browser's changing viewport.
- Nested scrolling, briefly flashing encoder errors and floating-toolbar corner glitches are fixed.
- **Hold one finger and tap with another to right-click**, with improved touch scrolling.
- An optional **Windows absolute-pointer profile** adds USB/EDID display association.
- Disabled USB input functions no longer leave their controls visible.
- When the controlling viewer disconnects, the **sole remaining viewer automatically receives control**.
- Shared video settings are restricted to administrators.

## 🌍 Choose how you connect

### 🖥️ RustDesk, directly on your NanoKVM

The optional **RustDesk integration 0.5.3**, using the **RustDesk 1.5 protocol**, adds another way to reach the captured computer:

- Use the official servers or your own.
- Connect with temporary or permanent passwords.
- Share the HDMI capture and control the computer through USB input.
- Follow the selected H.264/H.265 codec.
- Use direct/relay TCP by default, with optional WebRTC.

Enabling remote access prepares the required USB input functions while preserving unrelated gadget settings. The integration can forward USB audio. Frame-encryption buffers are reused, and dimensions outside the protocol header's range are rejected.

This integration exposes the captured HDMI display. Clipboard, file transfer, terminal, chat and ATX control are not included.

### 🧩 Add-ons with a home of their own

Manage extensions from **Software**. Install, update and remove **PicoClaw** using its official latest RISC-V release. Its navigation entry appears only while installed, and its mobile layout and contrast are improved.

## ⚙️ A stronger foundation — and a build you can own

- **Linux 7.2.9**, upgraded from 7.2.6, with matching modules and board boot images for CMA and fixed video-memory modes.
- Official **Alpine Linux 3.24** packages by default, including OpenSSL. The optional C906 package profile remains available for experiments.
- **GCC 16.2 / T-Head C906**: `-O3` without LTO for the kernel and modules; scalar `-O2` for native userspace. Corrected vector-context handling, an optimized aligned LZ4 decode path, and fixes for ISP bugs exposed by optimization.
- Updated MaixCDK, Pion DTLS/ICE/SRTP, inih, tinyalsa and relevant driver/firmware pins. **json-c 0.19** stays on its official stable release; web builds use **Node.js 24 LTS**.
- Consistent **OpenRC** application startup, with networking, SSH and watchdog operation independent of video initialization failures.
- A complete public build path for the SD image, application, native libraries, kernel, drivers and signed APK packages, with **pinned upstream sources and recorded output checksums**. HDMI receiver wiring and detection patches are retained in clean builds.

### 🛠️ Everyday fixes

The everyday fixes matter too: React cleanup and state handling across audio, input, memory, software management and the terminal; stricter image filename, video value, autostart and time-zone validation; and reuse of unchanged sanitized log snapshots.

## 🛡️ Make the most of a small memory budget

Package operations now share a process-wide and cross-process execution lock. Index queries run at low CPU priority, and an operation is refused with an explanation if the memory check finds it cannot fit. APK helpers are preferred OOM victims, while the main application receives protection.

The saved **Go memory limit** now takes effect on startup; without a saved setting, the default is one quarter of the RAM visible to Linux. RustDesk respects a deliberate USB-off choice across daemon restarts.

CPU frequency gains an explicit **Apply at startup** option. An overclocked startup uses a two-minute settling marker; an uncleared marker at the next boot triggers a fallback to **1000 MHz** and disables automatic overclocking. This recovery logic is covered by tests; a real overclocked reboot was not exercised for this release.

## 🔐 A new generation of signed updates

Release keys now have their own **nanokvm-keys** package. An ordinary update installs the public keys and automatically switches the official NanoKVM repository to the new **apk v3 index**, signed with **ECDSA P-256 and RSA** during the transition.

Older installations keep their upgrade path through the **RSA-signed legacy index**. Packages use **RSA with SHA-256**. No manual key installation or repository editing is needed for the official NanoKVM repository; custom repositories and C906 overlay entries are left unchanged.

## 💾 One image version. An evolving system.

Full SD images now have their own version number:

> 📦 **Image v1.0-a1 includes applications v2.5-a1.**

Future application updates keep the original image version and its bundled application version visible in **About**. You can identify what the card started with and which application version it runs today.

The full image is **66.8 MiB compressed**. Its root partition remains **768 MiB**, with the remaining SD-card space available for user data. The core update consists of **seven packages totalling 36.5 MiB**, including the new public-key package; optional add-ons and dependency updates are additional.

## 🚀 Install or upgrade

### ✨ Fresh SD card

Extract **NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip** and write the `.img` to an SD card of at least **2 GB**.

### 🔄 Existing installation: v2.0-a2 or newer

After publication, use **Settings → System → Updates**. On a2, use **Settings → Updates → Package updates**. You can also update as root:

```sh
apk update
apk upgrade
reboot
```

If NanoKVM packages were installed from local `.apk` files, first replace the local package pins with repository selections:

```sh
apk update
apk add nanokvm-base nanokvm-app nanokvm-release nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002
apk upgrade
reboot
```

**Keep your settings, user data and independently installed packages.** Application updates do not require rewriting the SD card. The kernel and modules update together; reboot to start **7.2.9-nanokvm-os-r1**. Full images include the rebuilt bootloader; APK updates preserve the installed bootloader.

Installations using the experimental C906 overlay should follow the [stock Alpine migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/main/firmware/alpine/README.md#existing-c906-installations). Updating NanoKVM components alone does not replace that overlay.

## 🧪 Alpha status

Both **v2.5-a1** and **Image v1.0-a1** are alpha releases.

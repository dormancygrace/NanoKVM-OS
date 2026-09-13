# 🚀 NanoKVM OS v1.0.0 beta-9 — Massive Dependency Refresh, APK & Device Improvements

A massive dependency refresh across the OS, backend, browser interface and build tools — now combined with an APK-managed application layer, improved Wi-Fi settings and a cleaner USB Serial Console. Beta-9 carries forward the refreshed foundation from beta-5 and brings the latest device, video and update improvements together in a complete SD image.

## ✨ What's new

- 💾 **64 MiB boot partition:** increased from 16 MiB so the running kernel and a pending RAM installer can fit together. This fixes the boot-space limitation encountered with the earlier layout. Beta-9 carries forward all beta-8 improvements listed below.

- 📦 **Optional applications through APK:** additional applications and their dependencies live in a separate package-managed layer.
- 🧩 **Install the tools you need:** **Midnight Commander, Superfile, nano, htop, tcpdump, ethtool and BlueZ utilities** are available as optional packages instead of being bundled in the base image.
- 🧭 **Reorganized device settings:** separate Wi-Fi, Ethernet and General sections, clearer grouping of settings and a reorganized toolbar. Audio controls have their own group, and disk controls follow the enabled USB functions.
- 📶 **5 GHz Wi-Fi and improved network setup:** you can now connect to **5 GHz Wi-Fi networks** with a compatible adapter, alongside 2.4 GHz support. Network scanning, band selection, hidden-network setup and clearer adapter information make connections easier to configure. Updated AIC8800/D80 detection and firmware selection are paired with **50 MHz SDIO** and corrected command/data pull-ups. RTL8733BS modules are rebuilt for the matching kernel.
- 🖥️ **A cleaner USB Serial Console:** serial sessions launch `picocom` directly, keep their settings within their own browser tab and close the underlying process when the session ends. Physical UARTs retain configurable serial settings; USB gadget ports no longer show settings that do not apply to physical UART signalling. Terminal URLs remain clean. The USB Serial toolbar button now has a transparent background, and USB gadget sessions use nominal **9600 baud** line coding; this does not limit USB transfer speed or change physical UART settings.
- 🔌 **One switch for USB Composition:** turn the configured USB functions off or back on immediately. The configuration panel hides while USB is off. **Apply** remains for changing profiles or a custom composition. Turning USB back on restores the last applied composition remembered by this browser, or the standard profile if none has been saved; unapplied edits are not used.
- 🎬 **Additional video profiles:** **720p / 120 FPS, 1080p / 70 FPS and 1440p / 40 FPS** options, with corresponding native capture changes and EDID profiles. The 1080p / 70 FPS capture option uses the accepted nominal-75 Hz HDMI timing. Actual delivery depends on the source, codec and load; these options do not guarantee the requested FPS in every mode.
- 🖥️ **Automatic now prefers QHD40:** the monitor preference menu offers **Automatic**, **2560×1440 · 40 Hz**, **1920×1080 · 75 Hz** and **1280×720 · 120 Hz**. Automatic advertises the tested QHD40 preferred timing, while the host chooses its output mode.
- 🎞️ **15 and 20 Mbit/s video options:** select the higher CBR targets for H.264 or H.265 in Video settings or the toolbar. Configuration validation and the native capture limits accept both values. MJPEG quality levels remain unchanged; these settings specify a bitrate target, not guaranteed throughput or frame rate.
- 🔐 **SSH off on a fresh installation:** new images start with SSH disabled. Updating an already configured device preserves the owner's SSH preference.
- ⚙️ **OpenSSL 4 and consistent optimization:** the system uses **OpenSSL 4.0.2**. Target C/C++ builds are standardized on **`-O2`** with GCC **16.2.0**.
- 📚 **A refreshed software foundation:** retains the updated system packages, server libraries and Web dependency graph. Compatibility fixes accompany the upgrades, including the React 19, Ant Design 6 and Tailwind 4 migrations.
- 🔊 **Audio that remembers your preferences:** Listen and volume survive a page reload. Playback retries after a connection failure, and existing listeners can recover when the audio capture helper restarts. If the browser blocks automatic playback, a **Resume** action lets you start it again.
- 🎵 **Opus 1.6.1:** updated from 1.5.2, retaining stereo at 48 kHz, 192 kbps and complexity 3. Audio continues to work alongside Direct, WebRTC and MJPEG video.
- 🎥 **Less video allocation overhead:** WebRTC allocates RTP packet headers together for each frame while preserving independent sequence numbers, timestamps and MTU handling for each viewer. MJPEG avoids an extra frame copy. This reduces allocation and copying work; it is not a claim of higher FPS.
- 📊 **More accurate system load reporting:** fixed video capture waiting being counted toward load average even when the capture thread was idle. This removes misleading load from frame waits.
- 🌐 **Smarter network queues:** FQ-CoDel is selected for Ethernet, Wi-Fi and USB NCM to manage competing flows and queue buildup. 
- 🐧 **Kernel diagnostics and extensibility:** enabled BPF JIT and pressure stall information (PSI), which exposes CPU, memory and I/O pressure.
- 🧹 **A leaner system image:** removed the iptables compatibility tools and unused USB gadget functions: OBEX, ECM/EEM, generic serial and FunctionFS. HID, ACM serial, NCM networking, mass storage and USB audio remain supported. 
- 🛠️ **Build compatibility fixes:** refreshed cross-compilation patches, dependency locks and source/license hashes so the updated components build together. Beta-9 also checks for malformed ELF interpreters, removes stale optional-package files and preserves existing SSH preferences during full-system updates.

## 🔄 Updated Components

### 🌐 Browser interface

The refreshed Web stack is retained from beta-5. This table shows the cumulative change from beta-4.

| Component | Beta-4 | Beta-9 |
| --- | --- | --- |
| React / React DOM | 18.3.1 | **19.3.0** |
| Ant Design | 5.29.3 | **6.6.3** |
| Jotai | 2.18.0 | **3.0.0** |
| React Router DOM | 6.30.3 | **7.18.3** |
| Axios | 1.15.2 | **1.20.0** |
| Tailwind CSS | 3.4.19 | **4.3.3** |
| Vite | 8.2.0 | **8.3.0** |

The tooling retains **TypeScript 7.0.2** alongside the **TypeScript 6.0.2** compiler API compatibility package. Transitive and optional dependencies retain the beta-5 refresh within their parent packages' supported ranges.

### 🐧 Linux kernel

Linux **7.2.5** remains the base, rebuilt as **7.2.5-nanokvm-os-r2** with matching modules, the SDIO and pin-controller fixes, FQ/FQ-CoDel support, BPF JIT, PSI and the USB configuration cleanup.

### 🐧 System utilities and libraries

- **OpenSSL 3.6.4 → 4.0.2**.
- Retained base utilities include **Bash 5.3**, **tmux 3.7c**, **strace 7.2**, **file 5.48** and **iproute2 7.2.0**.
- **exfatprogs 1.4.3**, **parted 3.7**, **util-linux / util-linux-libs 2.42.3** and **SysVinit 3.15** remain part of the refreshed system foundation.
- **wireless-regdb 2026.09.03**, **ca-certificates 20260816** and **tzdata 2026d** are retained.
- The refreshed optional tools include **htop 3.5.3**, **BlueZ utilities 5.87** and **Superfile 1.6.0**. Their required dependency libraries are installed with the relevant packages rather than kept in the base image solely for those tools.

### ⚙️ Backend and build tools

- Refreshed Go dependencies across the server and Superfile, including networking, validation, configuration, MIME detection, filesystem notifications and the MCP SDK.
- Updated Pion SRTP to **3.0.15**, retaining hardware AES integration, alongside refreshed TURN and QUIC dependencies.
- Retains the **Binutils 2.45.1 → 2.47** upgrade.
- Refreshed Meson, Automake, libtool, pkgconf, patchelf, ccache and Python packaging tools. 

## 📦 Installing beta-9

**Beta-9 is distributed as a complete SD image only. There is no `.nkos` package for this release.** Moving from beta-8 or earlier requires the new partition layout.

Download **`NanoKVM-OS-1.0.0-beta.9.img.zip`** and **`SHA256SUMS`**, verify the ZIP checksum, extract the `.img`, and write it to the NanoKVM SD card using an image-writing tool. A standard full-image installation replaces the existing card layout and data.

The new layout uses **64 MiB for boot** and **1488 MiB for the system**. The data partition starts at the same sector as before, allowing controlled RAM migration to preserve it; ordinary image-writing tools do not perform that preservation automatically. Future compatible system updates use the new layout contract.

[Installation guide](https://github.com/dormancygrace/NanoKVM-OS/blob/main/docs/INSTALL.md)

## 🤝 Thank you

Thanks to **Sipeed/NanoKVM, IronKVM, PiKVM and OneKVM**, and to the maintainers of **Buildroot, APK Tools, OpenSSL, Pion, Opus, Superfile** and the many upstream libraries behind this refresh. Credits for earlier adaptations are collected in the [beta-4 release notes](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v1.0.0-beta.4).

Try the refreshed stack and tell us how it works on your setup. Reports, testing and fixes are welcome! ❤️

> ⚠️ QHD H.265 over WebRTC remains unstable; use H.265 Direct for QHD. 

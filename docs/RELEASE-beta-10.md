# 🚀 NanoKVM OS v1.0.0 beta-10 — Portrait Video, Screenshots & Smoother Direct Streaming

Portrait video comes to NanoKVM OS, alongside one-click screenshots, clearer download progress and adaptive Direct playback. This release builds on the refreshed system and APK application layer from earlier releases, with new video-driver, Wi-Fi and interface fixes.

## 🖥️ Portrait video

- **A dedicated Portrait setting:** enable tall HDMI timings and retain your landscape monitor preference separately.
- **Four portrait profiles:** **720×1280 at 120 Hz**, **1080×1920 at 75 Hz** with a **70 FPS capture limit**, **1296×2304 at 50 Hz for H.264**, and **1440×2560 at 40 Hz for H.265 Direct**.
- **Correct image geometry:** fixed packed-YUV row alignment in the VI driver and exact active-width handling in the multimedia wrapper.
- **Codec-aware controls:** settings and quick menus enforce the supported codec/transport combinations. The server also validates them, including saved settings and Automatic modes.
- **Landscape retained:** the existing HD, FHD and QHD landscape profiles remain available. The 720p monitor option is now accepted correctly by the server API.

Refresh rates and FPS settings are targets. Actual delivery depends on the source, codec, browser and load. Portrait video and landscape regression checks were performed on the PCIe/UXC test device

## 📸 One-click screenshots

- A new **camera button** saves a **PNG** of the current video frame at its native video resolution, with a local timestamp in the filename.
- Screenshots support **Direct, WebRTC and MJPEG**, without including the NanoKVM toolbar.
- Capture happens in the browser: no second video stream, server restart or screenshot write to the NanoKVM SD card.

## 🎬 Direct playback improvements

- **Adaptive playback buffering** responds to recent frame-arrival jitter.
- **Improved handling of delayed acknowledgements:** a short ACK delay no longer immediately discards the pending prediction chain. Frame queues remain bounded.
- **QHD H.265 over WebRTC is now disabled**, including Automatic and previously saved configurations. Use **H.265 Direct for QHD**; supported lower-resolution WebRTC modes remain available.

These changes reduce avoidable playback interruptions. Network stalls can still occur during competing Wi-Fi traffic; this is not a claim of stall-free streaming.

## 📥 Clearer downloads and controls

- **Image Downloader progress:** transferred bytes, percentage and current speed, with support for downloads whose total size is unknown.
- **Toolbar download indicator:** a miniature progress bar and a green animated indicator in the download arrow keep transfer status visible.
- **USB-off indication:** the toolbar icon is muted and crossed out when all USB functions are disabled; its menu remains accessible.
- **Simpler serial-port selection:** choose `ttyS1` or `ttyS2` directly.

## 📶 Wi-Fi and system fixes

- **Updated AIC8800D80 SDIO+Bluetooth firmware**
- **AIC SDIO monitor RX fix:** guard against a missing monitor interface.
- **FQ-CoDel now installs correctly in full images:** the boot policy applies to Ethernet, Wi-Fi and USB NCM, without changing VPN interface policy.
- **Leaner kernel configuration:** removed unused module facilities while retaining the required video, USB audio, Wi-Fi/Bluetooth, WireGuard, OpenVPN DCO, nftables and NBD functionality.
- **Matching kernel and modules:** the release is prepared as one coherent system image, including the new VI driver and its corresponding userspace components.
- **APK add-on preservation:** fixed verification and restoration of installed add-ons during full-system updates.

## 📦 Installing or updating

- **Hardware scope:** this release image and its signed full-system updater were built and qualified for the **NanoKVM Enhanced PCIe/UXC** board only. They are not a Cube Full firmware release. Do not install the `.nkos` package on a Cube Full.
- **Already on beta-9's 64 MiB boot layout on the supported Enhanced PCIe/UXC board:** use the signed `NanoKVM-OS-update.nkos` package through **Settings → Updates**.
- **Fresh installation, stock firmware, or beta-8 and earlier:** download `NanoKVM-OS-1.0.0-beta.10.img.zip`, verify it against `SHA256SUMS`, extract the image and flash the complete SD card. A normal full-image installation replaces the existing card layout and data.

The layout remains **64 MiB boot + 1488 MiB system + the remaining card space for data**. Compatible full-system updates keep the kernel, modules, native libraries, application and Web interface together. APK add-ons remain a separate application layer.

## 🤝 Thank you

Thanks to **Sipeed/NanoKVM, SOPHGO/CVITEK, Radxa, IronKVM, PiKVM and OneKVM**, and to the maintainers of **Linux, Buildroot, APK Tools, OpenSSL, Go, Pion, Opus** and the many upstream projects behind NanoKVM OS.

Please include your board revision, HDMI source, browser, codec, transport and video profile when reporting an issue. Feedback and testing are welcome! ❤️

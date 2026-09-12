# 🚀 NanoKVM OS v1.0.0 beta-4

Hear your remote computer, mount an ISO straight from your browser, and keep an eye on temperature and CPU frequency. Beta-4 brings USB audio, remote optical media, thermal protection, and improvements to input and streaming.

## ✨ What's new

- 🔊 **USB audio in your browser:** enable USB Audio in the composition and listen from the viewer, with local volume control. Works alongside WebRTC, Direct and MJPEG video. Uses Opus at 192 kbps; playback has been confirmed on desktop Chrome and Xiaomi Chrome. Multiple listeners share one encoder.
- 🌐 **Mount an ISO from your computer:** serve requested blocks directly from your browser without uploading the complete image to the SD card. Keep the browser tab open while using it. Inspired by [OneKVM's browser-backed virtual media](https://github.com/onekvm/linux-onekvm-nbd); implemented here using upstream Linux NBD.
- 💿 **Larger CD/DVD images:** optical emulation supports images up to 31.625 GiB, for both SD-hosted files and remote browser media. A 6 GiB image was tested on Windows. Adapted from [PiKVM's DVD support](https://github.com/pikvm/packages/blob/0ba34d3d6e0933c6b5dbecf00466f3e191e8bbd8/packages/linux-rpi-pikvm/1102-pikvm-msd-dvd-support.patch).
- 🌡️ **Temperature and thermal protection:** Dashboard displays SoC temperature and actual CPU frequency. The CPU normally runs at 1000 MHz; Linux limits it to 850 MHz at 75°C and restores the selected frequency below 70°C, independently of the application.
- ⚡ **Optional runtime overclocking:** Device settings offer 1050, 1075, 1100, 1125 and 1150 MHz. Every overclock applies only until the next device reboot, which returns to 1000 MHz. Stability depends on the individual chip; no overclock is guaranteed safe.
- 🖥️ **OLED controls and burn-in protection:** a separate on/off switch, an idle timeout, and periodically shifting status information. Includes a fix for a blank PCIe display after restarting the board service. Inspired by [IronKVM's OLED work](https://github.com/yuzi-co/IronKVM/commit/3eb019f5efdf88111b491778bccd15174c22eca8).
- ⌨️ **Input improvements:** IME handling no longer leaves previously pressed keys stuck, and both mouse modes support horizontal scrolling ([IME](https://github.com/yuzi-co/IronKVM/commit/c7f3c34bc0fe09bc662ae684e862211cccc09a05), [horizontal scrolling](https://github.com/yuzi-co/IronKVM/commit/1f5d9d6b7183577dc99fdadde49c27519fc5e47e)).
- 🎥 **Better sharing between viewers:** independent WebRTC senders prevent a slow peer from blocking another, while retaining per-client MTU handling. MJPEG avoids repeatedly sending identical frames and still refreshes late-joining viewers ([WebRTC](https://github.com/yuzi-co/IronKVM/commit/e324cdaeaf569d9f5797a1fe306dfcad03e2be27), [MJPEG](https://github.com/yuzi-co/IronKVM/commit/143861014c0385912b22f5ebbd13152ca5aacb55)).
- 🛠️ **Less background work:** fewer unnecessary SD writes, direct file operations instead of shell subprocesses, and reduced CPU overhead in video diagnostics. Startup no longer unconditionally disables HDMI capture ([direct writes](https://github.com/yuzi-co/IronKVM/commit/bfac1e1fffc8fb53a107e4d9f7511634050e80f8), [OneKVM video pipeline](https://github.com/onekvm/onekvm-nanokvm-mmf)).
- 🌐 **DHCP and connection feedback:** improved broadcast acquisition and classless-route updates, preserving unrelated static and VPN routes. DHCP now publishes DNS correctly on a fresh system. The USB menu icon indicates a reconnecting or failed browser control channel ([DHCP](https://github.com/yuzi-co/IronKVM/commit/649adb199dbe634337e200904656682e17012ad5), [route-option work](https://github.com/sipeed/NanoKVM/pull/749), [connection feedback](https://github.com/yuzi-co/IronKVM/commit/dd62e79adc2d567b931abb1aa73f5ea1fcba982c)).
- 🔌 **USB composition:** all functions can be disabled without switching USB roles or cutting power through USB-HID. Reset preserves the disabled state. USB networking uses NCM; RNDIS is removed from the new kernel configuration.
- 🛡️ **Clearer VPN status:** Dashboard keeps VPNs together without duplicating them among network interfaces. WireGuard shows the active MTU and a live “last handshake … ago” indicator in both Dashboard and VPN settings. Disconnected Ethernet no longer starts an Avahi IPv4LL address after DHCP failure.
- 🧹 **Cleaner controls:** hide the keyboard and mouse toolbar buttons independently. Images now says **From SD**, defaults new mounts to **CD/DVD**, and has clearer spacing. Device no longer duplicates System Information or Swap; memory settings stay in Memory.

## 🔄 Updated Components

- 🐧 Linux Kernel: **7.2.4 → 7.2.5**

## 📦 Updating

**Beta-4 is distributed as a complete SD image and establishes the new kernel-capable updater.** Subsequent compatible signed `.nkos` packages can replace the application, supported system components, and the kernel with its matching modules while preserving settings.

Kernel packages verify their contents and compatibility, prepare the matching modules, and replace the existing `boot.sd`; the new kernel takes effect after a restart. **This uses the current single boot partition, without A/B or automatic kernel rollback.** An unbootable update or an interrupted boot-file write may require reflashing the SD card. The bootloader is not replaced.

The original beta-3 updater cannot read these kernel packages. Upgrading it first requires a compatible system bootstrap package; installing the full beta-4 image provides the complete baseline directly.

## 🤝 Thank you

Thanks to **Sipeed/NanoKVM, IronKVM, PiKVM and OneKVM** and their contributors. Links above identify work adapted or used as a starting point; they are not claims of wholesale merges. Earlier community PR contributions remain credited in the [beta-3 release notes](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v1.0.0-beta.3).

Worked on one of these projects? **Come try [NanoKVM OS](https://github.com/dormancygrace/NanoKVM-OS)** and tell us how these adaptations work for you. Ideas, testing and fixes are welcome! ❤️

> ⚠️ QHD H.265 over WebRTC remains unstable; use H.265 Direct for QHD. Overclocking can cause freezes, restarts, data loss or hardware damage. USB audio carries host playback, not microphone input.

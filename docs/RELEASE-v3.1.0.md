# 🚀 NanoKVM OS v3.1.0 — Boot. Transfer. Accelerate.

**📦 Applications v3.1.0 · 💾 Image v1.3**

**Your tiny KVM just became a more capable recovery station.** Boot an installer from the internet, move files between your browser and the controlled computer, and put more of the SG2002's capabilities to work. v3.1.0 builds on the v3.0 overhaul with new tools, faster hashing, leaner wireless drivers and safer storage operations.

## 🌐 Boot a computer from the internet

**Images → Network boot** brings **[netboot.xyz](https://github.com/netbootxyz/netboot.xyz)** to NanoKVM OS using the official **[iPXE](https://github.com/ipxe/ipxe)** boot images. Start installers, live systems and recovery tools without first uploading a large ISO to the SD card.

- Enable USB network and the virtual disk, then boot the computer from the NanoKVM CD/DVD drive.
- NanoKVM temporarily enables USB internet sharing if needed. The menus and installer payloads use its Ethernet or Wi-Fi uplink.
- **Check for updates** discovers newer iPXE and netboot.xyz releases. Downloaded iPXE images are checked against their published SHA-256 digests and verified again before mounting.
- **UEFI Secure Boot** uses the signed x86-64 iPXE image. Legacy BIOS needs this option off. The selected installer must also support Secure Boot; this is not universal support for every menu entry.

Network boot owns the virtual media while enabled. Turning it off, or restarting the server, ends the session and restores the previous internet-sharing setting. [How it works and requirements](https://github.com/dormancygrace/NanoKVM-OS/blob/v3.1.0/docs/netboot.md).

## 📂 Your USB disk, managed from the browser

**Settings → USB → User disk** now has a file manager. Browse folders, create folders, upload multiple files with progress and cancellation, download files, and delete files or empty folders. Free space, file sizes and modification times are visible in the same place.

Safely eject the disk on the controlled computer, switch it off in NanoKVM, manage the files, then attach it again. The browser and computer deliberately take turns owning the filesystem. Transfers stream through bounded buffers, existing files are protected from accidental overwrite, and stalled transfers release the disk.

The user disk remains a separate **FAT32 disk image**, with FAT32's per-file limit of just under 4 GiB. It does not expose the raw data partition. Reconnecting USB storage can briefly reconnect the other USB functions too.

## 🛡️ Safer image uploads and downloads

- Mounted images cannot be deleted or replaced while in use, including through another filename or hard link.
- Image transfers leave **128 MiB free** on the destination filesystem. The check covers uploads, downloads with unknown sizes and resumed downloads; other applications can still consume space concurrently.
- Browser uploads check ISO/UDF or sector-aligned raw IMG structure before accepting the file.
- Abandoned temporary files are cleaned up after a server restart, while valid download-resumption state is preserved. Resumption metadata does not survive a device reboot.

Thanks to **[watermeko](https://github.com/watermeko)** and **[imguoguo](https://github.com/imguoguo)** for the upstream file-management and transfer-safety work in [Sipeed NanoKVM #957](https://github.com/sipeed/NanoKVM/pull/957), which inspired and informed this implementation.

## ⚡ Faster work on one C906 core

**Large SHA-256 jobs use the SG2002 crypto engine**, with software handling small messages and providing a fallback when the engine is unavailable or fails. Downloads and update verification use this path. The Go runtime also gains T-Head SHA-1 and SHA-256 routines for software hashing.

Development measurements on the test device:

| Operation | Before | After |
| --- | ---: | ---: |
| Go SHA-256, 8 KiB buffers | 15.30 MB/s | 23.08 MB/s |
| Bulk SHA-256, 16 MiB buffers | about 22 MB/s in software | about 76 MB/s with the engine |
| Kernel software checksum, 1,500 bytes | 1,222 ns | 936 ns |

Measurements: [software SHA](https://github.com/dormancygrace/NanoKVM-OS/pull/214), [hardware SHA](https://github.com/dormancygrace/NanoKVM-OS/pull/216), [kernel checksum](https://github.com/dormancygrace/NanoKVM-OS/pull/234).

These measure individual operations, not download speed, VPN throughput or video FPS. Heavy hardware hashing shared with WebRTC over Wi-Fi lost about 1.5–2% of frames in the recorded development test; it also completed the hashing sooner. Wired tests showed less interference.

Kernel memory copying now uses fast misaligned word accesses where the C906 supports them, and the software network-checksum loop handles long buffers more efficiently. Ethernet's hardware-offloaded checksums do not benefit from that loop change. The kernel release remains **7.2.10-nanokvm-os-r5**; the updated kernel and driver APKs carry the **3.1.0** package version.

## 📶 Leaner Wi-Fi, optional Bluetooth

The **AIC8800** driver now validates firmware-message, scan-frame and SDIO transfer lengths. Malformed input is dropped instead of allowing out-of-bounds reads or an endless receive loop. Unused factory/private commands are removed from the normal build, reducing driver size and removing unnecessary calibration interfaces.

**Bluetooth is off by default** and has its own module. On supported AIC hardware, enable it in **Settings → Network → Bluetooth**; the setting survives reboots. Switching it off leaves Wi-Fi running. The page reports the controller's version when available. This switch enables the controller; it is not a Bluetooth peripheral-pairing manager.

Controller reset/reload handling also receives fixes. The Wi-Fi settings page now explains when no Wi-Fi device is present instead of presenting unusable controls.

## 📦 Smaller, independent application updates

The former monolithic application package is split into:

- **nanokvm** — server, native libraries and board services;
- **nanokvm-web** — browser interface;
- **nanokvm-app** — compatibility metapackage that installs both.

Existing installations migrate through the normal updater. The full release has **nine core packages**. A later web-only update can replace the UI without restarting the backend; refresh the browser to load it. The first migration can restart the service through the older package's hooks.

A separate **RustDesk 0.5.3-r5** refresh updates its locked dependencies and Rust compiler to 1.99.1. The add-on remains alpha and uses the RustDesk 1.5 protocol.

Optional add-ons remain separate from the full image. PicoClaw continues to come from its upstream releases, and ordinary system packages come from Alpine.

## 🌍 Everyday polish and community work

- Fix menu focus that could swallow the first interaction after a page reload.
- Keep the Settings close control reachable when a page scrolls.
- Improve common translations across 23 locales and refine Czech technical wording.
- Refresh the patched Pion DTLS dependency and web dependencies.

Thank you to **[Pavel Borecki (pavelbo)](https://hosted.weblate.org/user/pavelbo/)** and everyone translating, reviewing and testing NanoKVM OS. **[Hosted Weblate](https://hosted.weblate.org/engage/nanokvm-os/)** provides the platform and hosting for browser-based contributions. **Cube, Lite and PCIe reports are especially welcome** — a short report that a feature works is useful too.

## 🔄 Update or install

**Already on NanoKVM OS v2.0-a2 or newer?** Use **Settings → System → Updates**, or run as root:

```sh
apk update
apk upgrade
reboot
```

Settings, user data and separately installed packages are retained. **Reboot to activate the new kernel and drivers.** If packages were installed from local APK files, follow the [file-pin migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/v3.1.0/docs/UPDATES.md) first. Existing C906-overlay installations need the separate [stock Alpine migration](https://github.com/dormancygrace/NanoKVM-OS/blob/v3.1.0/firmware/alpine/README.md#existing-c906-installations).

**Fresh installation:** flash **NanoKVM-OS-Image-v1.3-apps-v3.1.0.img.zip** to an SD card of at least **2 GB** after verifying SHA256SUMS. Flashing replaces the card's contents. The image contains applications v3.1.0, a 64 MiB boot partition and a 768 MiB F2FS root; first boot creates the data partition in the remaining space. It includes U-Boot 2026.10 and OpenSBI 1.9.

Image and application numbering stay independent: updating applications does not rewrite the installed image's origin. 


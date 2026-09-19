# 🚀 NanoKVM OS v2.0 a2 — Alpine, Native APK Updates & Linux 7.2.6

This release brings together the changes made since beta-14: a new Alpine base, native package updates, refreshed branding and a simpler Wi-Fi interface, with the existing NanoKVM video and device features carried forward.

## 📦 Native packages and updates

- **Alpine Linux 3.24 with a writable F2FS root.** Install and remove packages with ordinary `apk add` and `apk del`; update them with `apk update` and `apk upgrade`. APK handles dependencies, and packages install into the system root.
- **Signed package repositories** provide the NanoKVM application, base integration, kernel, matching modules and hardware firmware as separate components. Additional software is available from the configured Alpine repositories.
- **Package updates in the GUI:** open **Settings → Updates → Package updates**, check available versions and install them from the same page.
- **Automatic service restarts through OpenRC.** Application updates briefly reconnect video and control; no manual apply command is required. The APK transaction continues when the application restarts.
- **Kernel updates through APK:** the matching kernel and modules are installed together, the boot image is selected for the detected board, and the GUI offers a restart. Routine package updates preserve settings without replacing or reformatting rootfs.
- **System reinstallation is a separate action**, with image profile and package selection. It replaces the system filesystem and restores supported saved settings, including the root password and SSH identity.

## ⚙️ Kernel and system

- **Linux `7.2.6-nanokvm-os-r1`**, retaining the NanoKVM board patches and `-O2` kernel build policy, with **79 matching modules** rebuilt for this kernel.
- Boot profiles for the SG2002 NanoKVM family, including Cube, PCIe/UXC and Lite, with automatic board selection.
- Existing **OpenSSL 4** integration and selected C906-optimized packages are retained.
- Fixed Alpine integration for **EDID programming** and detection of **built-in zram** support.
- Restored automatic **loopback** startup through OpenRC.
- Fixed root password login when SSH is enabled. **SSH is disabled by default on fresh installations**; an existing enabled setting is preserved during package updates. Factory Linux credentials are `root` / `root`; the web account is separate.
- NanoKVM-specific terminal greeting replaces Alpine setup instructions.

## 🎨 Branding and Dashboard

- **New NanoKVM OS branding:** the green connection logo is the default, with a large symbol above the name on the login screen and a separate favicon optimized for browser tabs.
- **Appearance settings:** choose the alternate blue logo or upload custom PNG/JPEG branding. Changes are saved on the device and applied immediately.
- Disabled network interfaces are hidden from Dashboard, and disabled-state labels are clearer.
- **CPU usage arrives with the first Dashboard response.** The server now samples it continuously instead of making the browser wait for a second poll.
- Completed system-installation messages no longer remain permanently visible on every visit.

## 📶 Wi-Fi

- Opening Wi-Fi settings automatically scans **both supported bands**, with periodic refresh while the panel is visible.
- **One network list for 2.4 and 5 GHz:** compatible entries sharing an SSID are grouped and labelled with their available bands.
- **Signal-strength icons** make reception easier to compare.
- Restored adapter capability discovery and scanning on Alpine. Band availability depends on the installed adapter.

## 💾 Installation and upgrading

Download **`NanoKVM-OS-v2.0-a2.img.zip`** and **`SHA256SUMS`**, verify the ZIP checksum, extract the `.img`, and flash the SD card.

The full image uses a **64 MiB boot partition** and **768 MiB F2FS root**. First boot creates an exFAT data partition using the remaining card capacity, selects the board profile and restarts automatically. Use an SD card of at least **2 GB**.

**Coming from beta-14 or an older beta: install the full SD image. Flashing replaces the existing installation and data.** There is no legacy `.nkos` update file.

The a2 image includes native GUI package updates from the start. Future v2 component updates use APK and do not require reflashing the card.

# 🚀 NanoKVM OS v2.0 b1 — Software Manager, Session Control & USB Fixes

This release brings the changes made since v2.0 a2: a graphical APK software manager, reorganized settings, safer sharing of keyboard and mouse control, and fixes for USB composition changes on Alpine.

## 📦 Software and native updates

- **New Software settings:** search the package catalogue, install and remove packages, view installed software and available updates, and refresh repository indexes from the web interface.
- **Ordinary APK underneath:** dependencies are handled by APK, packages install into the writable system root, and GUI operations share the same transaction handling as native package updates.
- **Optional VPN clients:** install or remove OpenVPN, Tailscale and NetBird from their settings. OpenVPN now uses Alpine's native **OpenVPN 2** package instead of the bundled OpenVPN 3 client. The former private OpenSSL 4 dependency is removed in favour of Alpine's standard libraries.
- The base remains **Alpine 3.24**. A tagged edge/community repository is used for optional clients that require it; this does not move the whole system to edge.
- Lightweight background status polling avoids repeatedly loading the installed-package list; loading failures are shown in the relevant Software tab instead of repeated pop-up errors.
- Services are applied through OpenRC after package transactions. No separate manual apply command is needed.

## 🖱️ Sessions and USB

- **Additional browser sessions start without keyboard and mouse control.** Control ownership is enforced by the server, with explicit release and transfer between sessions.
- The control lock appears only when at least one USB input function is enabled: keyboard, relative mouse or absolute pointer.
- Fixed USB composition changes on Alpine by preserving the init-script symlink, including rollback after an unsuccessful change.

## 📶 Wi-Fi and Wake-on-LAN

- **Preferred Wi-Fi band:** choose 2.4 or 5 GHz, with **5 GHz preferred by default** and fallback to the other band. Saving the preference does not interrupt the current connection; it applies when connecting again.
- Add friendly names to entries in the existing Wake-on-LAN history for quicker selection. Save with the button or Enter.
- Wake-on-LAN feedback confirms that the magic packet was sent; it does not claim that the remote computer has already started.

## 🎨 Settings and mobile layout

- Reorganized system and network settings, with improved navigation on small screens.
- Software tabs and package controls fit narrow panels, Dashboard status stacks on mobile, and settings no longer create an unnecessary horizontal scrollbar.
- NanoKVM ASCII SSH banner with the OS version and a coloured shell prompt.

## ⬆️ Updating from v2.0 a2

**No SD-card reflash is required.** After b1 is published, open **Settings → Updates → Package updates** in a2, refresh the package list and install the available updates. Alternatively, run:

```sh
apk update
apk upgrade
```

The update preserves settings, user data and independently installed packages. The web application restarts automatically, briefly reconnecting video and control. Follow any restart request shown by the interface.

The b1 packages use the key already trusted by the published a2 image: **no manual key import is needed**. Linux remains **`7.2.6-nanokvm-os-r1`**, so this update does not replace the kernel or its matching modules.

**OpenVPN users:** the old bundled OpenVPN 3 package is removed during this update. Install the optional OpenVPN client from the new settings before reconnecting; existing profile files are retained.

## 💾 Fresh installation

Download **`NanoKVM-OS-v2.0-b1.img.zip`** and **`SHA256SUMS`**, verify the checksum, extract the `.img` and flash the SD card. Use a card of at least **2 GB**.

The image retains automatic board selection for SG2002 NanoKVM Cube, PCIe/UXC and Lite, a **64 MiB boot partition**, a **768 MiB F2FS system partition**, and an exFAT data partition created from the remaining space on first boot.

**From beta-14 or an older beta, use the full image. Flashing replaces the installation and data.**


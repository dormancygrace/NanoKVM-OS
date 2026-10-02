# NanoKVM OS v2.0-b8

### 🛠️ Complete firmware builds

- Build the complete SD image from the public repository: kernel, drivers, boot images, native media libraries, application, web interface and signed APK packages.
- Pin upstream sources, record build settings and verify the generated files against reference checksums. Include the corresponding platform sources with the release.
- Use a shared **C906 / T-Head + `-O2`** profile for NanoKVM-built native components, with the appropriate integer-only settings for the kernel and bootloader.
- Remove the Realtek SDIO driver’s local `-O1` override so it also follows `-O2`.
- Preserve the NanoKVM HDMI receiver patches in clean builds, including I²C/MIPI wiring and LT6911UXC/LT6911D detection.
- Fix ISP initialization and fixed-width metadata handling errors exposed by `-O2`, without disabling compiler warnings.

### 📦 Standard Alpine packages

- Use official **Alpine Linux 3.24** packages by default, including OpenSSL, BusyBox, coreutils, LZ4 and Zstandard.
- Retain the optional C906 package profile for experiments; it is no longer the default system package source.
- Continue to install software directly into the writable root filesystem through native APK and OpenRC.

### 🖥️ Portrait video

- Match portrait and landscape frame-rate targets: **QHD up to 50 FPS, FHD up to 75 FPS and HD up to 120 FPS**. Update the maximum 1440×2560 H.265 Direct EDID to 50 Hz.

### 🔧 Reliability and interface fixes

- Keep networking and SSH independent of video and board initialization failures.
- Correct React effect cleanup and state handling across audio, keyboard/mouse controls, memory settings, software management and the browser terminal.
- Fix returning stream resolution to **Same as input** and reject invalid numeric video settings consistently.
- Tighten image-download, resume and mount filename validation, together with autostart and time-zone path handling.
- Update the Pion networking libraries and web dependencies while retaining NanoKVM-specific changes. Web builds now use Node.js 24 LTS.
- Align AIC8800 firmware with the pinned driver package and include the cfg80211 compatibility fix.

### 🚀 Installation and updates

For a fresh installation, extract **NanoKVM-OS-v2.0-b8.img.zip** and write the `.img` to an SD card of at least **2 GB**.

From **v2.0-a2 or later**, use **Settings → System → Updates** (on a2: **Settings → Updates → Package updates**), or run as root:

```sh
apk update
apk upgrade
reboot
```

If NanoKVM packages were installed from local `.apk` files, release their package pins first:

```sh
apk update
apk add nanokvm-base nanokvm-app nanokvm-release nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002
apk upgrade
reboot
```

Settings, user data and independently installed packages are retained. Linux remains **7.2.6-nanokvm-os-r1**. The full image also includes the rebuilt U-Boot; APK updates preserve the installed bootloader.

Fresh images use official Alpine packages. Existing installations using the experimental C906 overlay can follow the [stock migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/main/firmware/alpine/README.md#existing-c906-installations); a NanoKVM application update alone does not replace that overlay.

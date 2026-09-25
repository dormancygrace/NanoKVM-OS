# NanoKVM OS v2.0-b5

🧪 **Pre-release — LT6911D initialization update**

### 🎥 HDMI receiver

- Add a dedicated initialization path for boards detected as **LT6911D**, including the required pin configuration and bank E0 reads instead of applying the legacy receiver ID check.
- Keep the existing LT6911UXC initialization path.
- Retain the multimedia `devmem` dependency restored in b4.

This is a test release for the LT6911D issue reported in [#8](https://github.com/dormancygrace/NanoKVM-OS/issues/8). The fix has been built and packaged, but successful video capture on LT6911D hardware has **not yet been confirmed**.

### 🔧 System

- Correct the application version displayed after installation.
- Linux remains **7.2.6-nanokvm-os-r1**; CMA remains the default.
- Includes the changes from b4 and earlier v2 releases.

### 📦 Installation

**Fresh installation:** write `NanoKVM-OS-v2.0-b5.img.zip` to the SD card using an image-writing tool. This replaces the existing installation.

**Update from v2.0-a2 or v2.0-b1–b4:** download the three attached b5 APK files into the same directory on the device, then run as root:

```sh
apk add ./nanokvm-base-2.0_beta5-r0.apk ./nanokvm-app-2.0_beta5-r0.apk ./nanokvm-release-2.0_beta5-r0.apk
reboot
```

Existing configuration is preserved by the package update. The signed stable repository must remain configured to resolve dependencies, including the current kernel when upgrading from an older release. The b4 → b5 package transaction was checked in an isolated root filesystem; other starting versions were not retested for b5.

**b4 remains Latest.** These pre-release packages are separate downloads and will not replace the stable APK repository. A normal stable-channel `apk upgrade` does not install b5.

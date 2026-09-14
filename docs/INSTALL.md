# 🚀 Installing NanoKVM OS beta-10

On **beta-9 with its 64 MiB boot layout**, open **Settings → Updates** and select
the beta-10 release, or upload the signed `NanoKVM-OS-update.nkos` package. This
updates the system, kernel, matching modules, native libraries, application and
Web interface together. Supported settings and APK add-ons are preserved.

For **stock firmware, beta-8 or earlier, or a fresh installation**:

1. Download `NanoKVM-OS-1.0.0-beta.10.img.zip` and `SHA256SUMS` from
   [the release](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v1.0.0-beta.10).
2. Verify the ZIP checksum and extract the `.img`.
3. Power off NanoKVM, connect its SD card to a reader and write the image to the
   whole card. This replaces the existing layout and data.
4. Safely eject, install the card and power on. Use Ethernet/DHCP initially and
   open `https://DEVICE-IP/`. Confirm the device certificate or install a trusted one.

The factory Web account is `admin` / `admin`; change its password when prompted.
SSH is disabled on fresh installations and can be enabled in Web settings.
Compatible adapters support both 2.4 GHz and 5 GHz Wi-Fi.

The layout remains 64 MiB FAT boot, 1488 MiB ext4 system and remaining space for
exFAT data. S01fs creates the absent data partition when USB disk support is
enabled. Fresh-card creation was not exercised in this release's hardware check.
There is no A/B rollback. See [acceptance](BETA10-ACCEPTANCE.md),
[layout](SD-LAYOUT-v2.md) and [release notes](RELEASE-beta-10.md).

Use H.265 Direct for QHD; QHD H.265 WebRTC is disabled.

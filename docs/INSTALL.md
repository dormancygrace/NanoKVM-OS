# Installing Image v1.0-a1 / NanoKVM OS v2.5-a1

**Image v1.0-a1 bundles applications v2.5-a1. Both are alpha releases.**
For an existing v2.0-a2 or newer installation, use [APK updates](UPDATES.md)
instead of reflashing if you want to retain settings and data.

## Fresh SD card

1. Download [NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v2.5-a1/NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip)
   and [SHA256SUMS](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v2.5-a1/SHA256SUMS).
2. Verify the ZIP checksum, extract the `.img` and flash it to an SD card of
   at least **2 GB**. Flashing replaces the existing installation and data.
3. Boot NanoKVM. First boot creates the exFAT data partition in the remaining
   card space, detects the board and automatically restarts. Start with
   Ethernet/DHCP and open `https://DEVICE-IP/`.
4. Sign in with **admin / admin** and change the password when prompted.
   Until then, the web interface only allows the password change. For the
   administrator this also sets the Linux root password used by SSH.
   SSH is disabled by default; enable it in settings if needed.

The ZIP is 66.8 MiB. The image has a 64 MiB FAT boot partition and a 768 MiB
F2FS system partition, with remaining space used for exFAT user data.
It includes Linux 7.2.9-nanokvm-os-r1, matching modules and native GUI/APK updates.

Full images and applications are versioned independently. About keeps the
original image version and its bundled application version visible after
subsequent APK updates.

PCIe/UXC is the development test platform; Cube/Lite physical acceptance and
persistent cold boot of this exact full-image release remain pending. See
[release notes](RELEASE-v2.5-a1.md), [validation](RELEASE-v2.5-a1-VALIDATION.md)
and [board profiles](../firmware/boards/README.md).

---

## Historical beta installation documentation

The following describes the pre-v2 system; its legacy update commands do not
apply to Alpine v2.

# Installing NanoKVM OS beta-13

Beta-13 is distributed as a complete SD image only. There is no `.nkos`
package for this release, including for devices already running beta-9 or beta-10.

**Hardware scope:** automatic Alpha, serial Full, PCIe and Lite/base profiles. The final image was tested on PCIe/UXC; other models still need physical acceptance. See [release notes](RELEASE-beta-13.md).

1. Download `NanoKVM-OS-v1.0.0-beta-13.img.zip` and `SHA256SUMS` from the
   [release](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v1.0.0-beta.13).
2. Verify the ZIP checksum and extract the `.img`.
3. Power off NanoKVM, connect its SD card to a reader and flash the whole card.
   This replaces the existing installation, settings and data; export anything
   you want to retain before flashing.
4. Safely eject the card, install it and power on. Allow automatic board
   selection and its restart to finish. Use Ethernet/DHCP initially
   and open `https://DEVICE-IP/`.

The factory Web account is `admin` / `admin`; change its password when prompted.
SSH is disabled by default and can be enabled in Web settings. Compatible Wi-Fi
adapters support 2.4 GHz and 5 GHz.

The layout is 64 MiB FAT boot, 1488 MiB ext4 system and remaining card space
for exFAT data. S01fs creates the absent data partition when USB disk support
is enabled. Fresh-card creation remains outside hardware qualification.
There is no A/B rollback. See [release notes](RELEASE-beta-13.md),
[acceptance](BETA13-ACCEPTANCE.md) and [layout](SD-LAYOUT-v2.md).

Moving a configured card between different board revisions requires reflashing
to repeat board selection. See [detection limits](../firmware/boards/README.md).

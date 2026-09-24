# Installing NanoKVM OS v2.0 b3

Download NanoKVM-OS-v2.0-b3.img.zip and SHA256SUMS from the release. Verify the
ZIP checksum, extract the .img and flash it to an SD card of at least 2 GB.
Flashing replaces existing installation and data. First boot creates the data
partition in the remaining space, detects the board and automatically restarts.

The image includes native GUI package updates. SSH is off by default; enable it
in settings if needed. Factory Linux credentials are root/root; the web account
is separate. See RELEASE-v2.0-b3.md and UPDATES.md for current release details.

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

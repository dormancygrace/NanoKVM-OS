# Installing NanoKVM OS beta-11

Beta-11 is distributed as a complete SD image only. There is no `.nkos`
package for this release, including for devices already running beta-9 or beta-10.

1. Download `NanoKVM-OS-v1.0.0-beta-11.img.zip` and `SHA256SUMS` from the
   [release](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v1.0.0-beta.11).
2. Verify the ZIP checksum and extract the `.img`.
3. Power off NanoKVM, connect its SD card to a reader and flash the whole card.
   This replaces the existing installation, settings and data; export anything
   you want to retain before flashing.
4. Safely eject the card, install it and power on. Use Ethernet/DHCP initially
   and open `https://DEVICE-IP/`.

The factory Web account is `admin` / `admin`; change its password when prompted.
SSH is disabled by default and can be enabled in Web settings. Compatible Wi-Fi
adapters support 2.4 GHz and 5 GHz.

The layout is 64 MiB FAT boot, 1488 MiB ext4 system and remaining card space
for exFAT data. S01fs creates the absent data partition when USB disk support
is enabled. Fresh-card creation remains outside hardware qualification.
There is no A/B rollback. See [release notes](RELEASE-beta-11.md),
[acceptance](BETA11-ACCEPTANCE.md) and [layout](SD-LAYOUT-v2.md).

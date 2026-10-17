# Installing NanoKVM OS

A full SD image replaces everything on the card. An existing v2.0-a2 or newer
installation keeps its settings and data with [package updates](UPDATES.md)
instead.

## Fresh SD card

1. Download `NanoKVM-OS-Image-*.img.zip` and `SHA256SUMS` from the
   [latest release](https://github.com/dormancygrace/NanoKVM-OS/releases/latest).
2. Verify the ZIP checksum, extract the `.img` and flash it to an SD card of
   at least **2 GB**.
3. Boot NanoKVM. First boot creates the exFAT data partition in the remaining
   card space, detects the board and restarts once. Start with Ethernet/DHCP
   and open `https://DEVICE-IP/`.
4. Sign in with **admin / admin** and change the password when prompted.
   Until then, the web interface only allows the password change. For the
   administrator this also sets the Linux root password used by SSH.
   SSH is disabled by default; enable it in settings if needed.

## Card layout

| Partition | Size | File system | Contents |
| --- | --- | --- | --- |
| 1 | 64 MiB | FAT | bootloader (`fip.bin`), kernel (`boot.sd`), boot settings |
| 2 | 768 MiB | F2FS | Alpine Linux system root |
| 3 | remaining space | exFAT | user data, created on first boot |

## Versions

Full images and applications are versioned independently. **About** keeps the
original image version and its bundled application version visible after
later package updates.

Board detection and the supported models are described in
[board profiles](../firmware/boards/README.md).

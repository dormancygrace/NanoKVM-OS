# 🚀 Installing NanoKVM OS beta-9

**Beta-9 requires a complete SD image. No `.nkos` package is distributed for this release.** The boot partition grows from 16 MiB to 64 MiB; older layouts cannot receive this change through the ordinary full-system package.

1. Download `NanoKVM-OS-1.0.0-beta.9.img.zip` and `SHA256SUMS` from [the release](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v1.0.0-beta.9).
2. Verify the ZIP checksum and extract the `.img`.
3. Power off NanoKVM, connect its SD card to a card reader and write the image to the whole card with an image-writing tool. Select the correct card. This replaces its existing layout and data; copying a file to a mounted card is not flashing.
4. Safely eject, install the card and power on. Use Ethernet/DHCP for initial setup, then open `https://DEVICE-IP/`. Confirm the device's locally generated HTTPS certificate or install a trusted certificate.

Use the factory Web account `admin` / `admin` and change its password when prompted. SSH is disabled on fresh installations; enable it through Web settings if needed. Wi-Fi settings support 5 GHz networks with a compatible adapter.

The layout is 64 MiB FAT boot, 1488 MiB ext4 system, and the remaining card space for exFAT data. S01fs creates the data partition on a fresh card when required. Its start sector is unchanged from beta-8, but preserving existing data during a controlled RAM migration is a separate process; normal image-writing tools do not do it automatically.

Future compatible updates use `sg2002-sd-v2`. There is no A/B rollback. See [the release notes](RELEASE-beta-9.md) and [exact geometry](SD-LAYOUT-v2.md). QHD H.265 over WebRTC remains unstable; use Direct for QHD.

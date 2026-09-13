# 🚀 Installing NanoKVM OS beta-8

Beta-8 is a complete system image, release sequence 21, with Linux 7.2.5-nanokvm-os-r2, OpenSSL 4.0.2 and the APK application layer.

## Choose the right file

- **NanoKVM-OS-1.0.0-beta.8.img.zip** — the complete SD image for a blank card, original NanoKVM firmware or an incompatible OS base.
- **NanoKVM-OS-1.0.0-beta.8.nkos** — the signed complete system update for an installation with a compatible full-system updater (capability 2).
- **SHA256SUMS** — checksums of both downloads.

Get these files from [the beta-8 release](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v1.0.0-beta.8). Original NanoKVM application archives are not NanoKVM OS updates.

## Full-image installation

1. Check the downloaded ZIP against SHA256SUMS, then extract its `.img` file.
2. Power off the device and make its SD card available as a whole disk, normally with a card reader. Select the correct card by identity and capacity.
3. Write the `.img` with an image-writing tool, including its partition table. This replaces the target card's layout and data; copying the image into a mounted data volume is not flashing. Decline Windows prompts to format Linux partitions.
4. Safely eject the card, install it and power on. Use wired Ethernet/DHCP for initial setup where available, then open **https://DEVICE-IP/**. A unique self-signed certificate is created on first launch; confirm its local trust or install your own certificate.

The raw image is 1,627,390,464 bytes. The first-boot script creates the remaining data partition when required. Fresh-card partition creation and hardware USB recovery of this exact sequence 21 image have not been physically qualified during its assembly.

## First login

Use the factory Web account `admin` / `admin` and change its password when prompted. **SSH is disabled on a fresh image.** Enable it in the Web settings when needed. Changing the owner password also updates the Linux root password.

Wi-Fi settings support scanning, band selection and connecting to 5 GHz networks with a compatible adapter. Automatic monitor preference advertises QHD40; the connected host selects its output timing.

## Updating an existing installation

With a compatible full-system updater, install the signed `.nkos` through **Settings → Updates**. The package contains the complete system filesystem and matching kernel/modules. The device restarts into a RAM installer and then into the new system. Supported configuration, including the existing SSH preference and video settings, is preserved; installed APK applications are migrated through the package manager and data-partition files are retained.

If the installed updater does not support this package format, use the full SD image. Do not assume that an older beta's application-only or kernel-capable updater supports capability 2. There is no A/B rollback; interruption can require reflashing. Updates are not installed automatically.

See [the release notes](RELEASE-beta-8.md) for included changes. QHD H.265 over WebRTC remains unstable; use H.265 Direct for QHD.

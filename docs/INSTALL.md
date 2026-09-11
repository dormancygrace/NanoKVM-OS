# 🚀 First installation and application updates

**NanoKVM OS v1.0.0 beta-2 — full image, 2026-09-11 build (application sequence 4).** Includes Linux 7.2.4, current native/video components, QHD, CryptoDMA, WireGuard and OpenVPN 3 Core/DCO. HTTPS is enabled on first installation and H.265 Direct is the default for a fresh browser.

> **QHD H.265 WebRTC is unstable:** it can freeze or restart the device. Use H.265 Direct for QHD. The exact assembled image has passed content/filesystem checks; fresh-card first boot and hardware Boot flashing of this exact ZIP remain untested.

## Choose the right file

This release contains **`NanoKVM-OS-1.0.0-beta.2.img.zip`**, the full SD image. Use it for original NanoKVM firmware, a blank card or an incompatible OS base. A separate `.nkos` package is not distributed for beta-2; upgrading from beta-1 also needs this full image because the kernel and system packages changed. Original NanoKVM application archives are not NanoKVM OS updates.

Get the files and `SHA256SUMS` from [this project's releases](https://github.com/dormancygrace/NanoKVM-OS/releases).

## Full-image installation

1. Verify the complete SHA256 digest against the release's `SHA256SUMS`. Extract the `.img.zip` archive. On Windows, right-click it and choose **Extract All…**; on Linux/macOS, use an archive manager or `unzip NanoKVM-OS-1.0.0-beta.2.img.zip`. Verify the unpacked `.img` digest too.
2. Make the NanoKVM SD card available as a **whole disk**. For a blank/unbootable card, power off and use a card reader. For a card with working firmware, its hardware USB update mode can expose the card without removing it: power off, hold the board's update key while connecting USB/power, and wait for the card to appear. On the PCIe test device this is the BOOT control; consult the Cube hardware illustration for its control location.
3. Use an image-writing tool to write the unpacked `.img` to that SD card, including the partition table. This replaces the target card's existing layout/data. Select the card by identity and capacity; copying the image into a normal mounted data volume is not flashing it. Decline Windows prompts to format Linux partitions.
4. Wait for writing and verification to finish, safely eject, and power up NanoKVM with the update key released. Use wired Ethernet/DHCP for the initial setup where available; then open **https://DEVICE-IP/** and configure access/network settings. HTTP redirects to HTTPS. A unique self-signed certificate is created on the device at first launch; confirm the local-certificate warning in your browser, or install your own trusted certificate later.

The raw image is 1,627,390,464 bytes; the compressed ZIP is about 56.9 MiB. The first-boot script creates the remaining data partition. Its behavior on a fresh card, this exact image's hardware USB recovery, and Cube-specific operation still need physical qualification.

The distinction between card-reader flashing and USB updating of an already bootable card follows [Sipeed's flashing guide](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM/system/flashing.html). That guide describes the original firmware. NanoKVM OS preserves an initramfs recovery path that exports the whole card with local partitions unmounted; the new full-image recovery path is not claimed tested merely because its source is present.

## First login

Sign in with the factory web account `admin` / `admin` and change its password when prompted. Changing the owner password also updates the Linux root password. Enable SSH only when needed and use the updated owner password. A self-signed HTTPS certificate encrypts the connection but requires local trust confirmation.

## After the first OS installation

Use **Settings → Updates** for future compatible application/web releases. The image initializes installed release sequence 4. Future signed packages need a higher sequence and the matching native-library fingerprint. Kernel/rootfs or native-library changes require another complete image.

The application updater checks GitHub automatically but never installs automatically. No separate application package is published for this first release. See [update validation and rollback](UPDATES.md).

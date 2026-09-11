# 🚀 First installation and application updates

**NanoKVM OS v1.0.0 beta-3 — full image, 2026-09-11 build (update sequence 8).** Includes Linux 7.2.4, current native/video components, QHD, CryptoDMA, WireGuard and OpenVPN 3 Core/DCO. HTTPS is enabled on first installation and Direct mode selects H.265 when the browser supports it, otherwise H.264.

> **QHD H.265 WebRTC is unstable:** it can freeze or restart the device. Use H.265 Direct for QHD. The exact assembled image has passed content/filesystem checks; fresh-card first boot and hardware Boot flashing of this exact ZIP remain untested.

## Choose the right file

This release contains **`NanoKVM-OS-1.0.0-beta.3.img.zip`**, the full SD image. Use it for original NanoKVM firmware, a blank card or an incompatible OS base. A separate `.nkos` package is not distributed for beta-3; upgrading from beta-1 or beta-2 also requires this full image to establish the system updater. Original NanoKVM application archives are not NanoKVM OS updates.

Get the files and `SHA256SUMS` from [this project's releases](https://github.com/dormancygrace/NanoKVM-OS/releases).

## Full-image installation

1. Verify the complete SHA256 digest against the release's `SHA256SUMS`. Extract the `.img.zip` archive. On Windows, right-click it and choose **Extract All…**; on Linux/macOS, use an archive manager or `unzip NanoKVM-OS-1.0.0-beta.3.img.zip`. Verify the unpacked `.img` digest too.
2. Make the NanoKVM SD card available as a **whole disk**. For a blank/unbootable card, power off and use a card reader. For a card with working firmware, its hardware USB update mode can expose the card without removing it: power off, hold the board's update key while connecting USB/power, and wait for the card to appear. On the PCIe test device this is the BOOT control; consult the Cube hardware illustration for its control location.
3. Use an image-writing tool to write the unpacked `.img` to that SD card, including the partition table. This replaces the target card's existing layout/data. Select the card by identity and capacity; copying the image into a normal mounted data volume is not flashing it. Decline Windows prompts to format Linux partitions.
4. Wait for writing and verification to finish, safely eject, and power up NanoKVM with the update key released. Use wired Ethernet/DHCP for the initial setup where available; then open **https://DEVICE-IP/** and configure access/network settings. HTTP redirects to HTTPS. A unique self-signed certificate is created on the device at first launch; confirm the local-certificate warning in your browser, or install your own trusted certificate later.

The raw image is 1,627,390,464 bytes; the compressed ZIP size is listed with the release asset. The first-boot script creates the remaining data partition. Its behavior on a fresh card, this exact image's hardware USB recovery, and Cube-specific operation still need physical qualification.

The distinction between card-reader flashing and USB updating of an already bootable card follows [Sipeed's flashing guide](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM/system/flashing.html). That guide describes the original firmware. NanoKVM OS preserves an initramfs recovery path that exports the whole card with local partitions unmounted; the new full-image recovery path is not claimed tested merely because its source is present.

## First login

Sign in with the factory web account `admin` / `admin` and change its password when prompted. Changing the owner password also updates the Linux root password. Enable SSH only when needed and use the updated owner password. A self-signed HTTPS certificate encrypts the connection but requires local trust confirmation.

## After the first OS installation

Beta-3 initializes release sequence 8 and the system-update foundation. Use **Settings → Updates** for subsequent compatible signed `.nkos` packages. Application packages replace the server/web interface; system packages can also update supported programs, libraries and data while preserving configuration.

Kernel, kernel modules, bootloader and native media libraries are not replaced by this package format. Beta-1 and beta-2 require the full beta-3 image to establish the system updater. Updates are never installed automatically. See [update validation and rollback](UPDATES.md).

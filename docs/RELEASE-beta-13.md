# 🧩 NanoKVM OS v1.0.0 beta-13 — Automatic Board Setup

One full SD image with automatic first-boot setup for the SG2002 NanoKVM family. This release includes the improvements from beta-12 and replaces its fixed PCIe boot configuration with board-specific boot profiles.

## 🛠️ Automatic first boot

- **Board-specific startup:** the image detects the supported board wiring before starting normal services, selects the appropriate boot profile, and restarts automatically.
- **Cube Full and PCIe:** separate profiles cover early Alpha hardware, serial-production Full hardware and PCIe boards.
- **Lite/base mode:** when the hardware probes complete successfully without finding an OLED, the image selects a base profile with ATX control disabled.
- **Consistent hardware configuration:** the kernel and application use the selected profile together. The early Alpha OLED check runs before pins shared with its host-reset control can be used for another profile.
- **Detection failures:** conflicting responses or bus errors stop automatic setup instead of selecting an arbitrary profile.

## 📦 Installation

**Full SD image only. No `.nkos` update package is included.**

Download **`NanoKVM-OS-v1.0.0-beta-13.img.zip`** and **`SHA256SUMS`**, verify the checksum, extract the `.img`, and flash the SD card.

The first boot performs board selection and an automatic restart. Allow this process to finish before connecting to the web interface.

**Flashing an existing card replaces its installation and data.** Moving a configured card to a different board revision requires flashing the image again to repeat automatic setup.

## 🧪 Hardware status

The final image was installed and tested on a **PCIe/UXC** device: automatic board selection, normal startup, live H.265 video and 5 GHz Wi-Fi worked.

Cube Full and Lite profiles are included and covered by source-level tests, but physical acceptance on those models is still pending. An early Alpha board needs a working identity OLED for reliable automatic identification; this image does not provide generic discovery for arbitrary carrier boards.

## 🙏 Thanks

Thanks to everyone reporting hardware differences and sharing diagnostics. Please include your board model and OS version when reporting an issue.

# 🛠️ NanoKVM OS v1.0.0 beta-12 — Hardware Detection Fixes

This release focuses on hardware detection at startup and includes all improvements from beta-11.

## 🖥️ OLED and board detection

- **Cube OLED profile:** a detected OLED at I²C5 address `0x3d` now selects the Cube profile, even when the Device Tree declares `pcie`. This corrects the profile mismatch reported in issue #2, which made the OLED service look at the wrong address.
- **PCIe without an OLED response:** an explicitly declared PCIe board retains its existing profile when the display does not respond.
- **Optional I²C probe:** a failed probe on I²C1 no longer prevents detection on I²C5.

## 🎥 HDMI detection

- Hardware detection continues to the next supported HDMI receiver address when a successful probe finds no device at the first address.
- An actual HDMI I²C command failure stops detection without publishing a new hardware configuration.

## 📦 Installation

**Full SD image only. No `.nkos` update package is included.**

Download **`NanoKVM-OS-v1.0.0-beta-12.img.zip`** and **`SHA256SUMS`**, verify the checksum, extract the `.img`, and flash the SD card.

**Flashing replaces the existing installation, including settings and data.**

## 🧪 Hardware status

The image retains the Enhanced PCIe/UXC boot assets. It includes the Cube OLED-profile correction, but a complete Cube boot, OLED, HDMI and ATX acceptance test is still pending. This is not yet a fully qualified universal Cube/PCIe release.

## 🙏 Thanks

Thanks to everyone reporting issues and providing hardware diagnostics. Please include your board model and OS version when reporting a problem.

# 🛠️ NanoKVM OS v1.0.0 beta-14 — Video, Networking & OLED Improvements

A full-image update for the SG2002 NanoKVM family, combining video stability fixes, new network controls and clearer OLED status.

## 🎥 Video and display

- **Direct playback preference:** choose **Smooth picture** or **Lowest latency** in the browser. Smooth picture uses adaptive buffering to reduce uneven frame delivery; Lowest latency minimizes that buffering. The choice is saved in each browser.
- **Higher-rate PCIe/UXC profiles:** updated monitor profiles add QHD at 50 Hz and Full HD at 75 Hz, with matching stream controls. Actual displayed frame rate depends on the HDMI source, network and browser.
- **More reliable codec transitions:** fixes cover encoder teardown, recovery after frame-buffer allocation failures, and VPU/JPEG clock ownership.
- **Capture improvements:** fewer frame copies, removal of redundant cache invalidation, and revised capture buffering and pacing.
- **Cube EDID workflow:** dedicated 720p60 and 1080p60 profiles, explicit power-cycle confirmation and a persistent reminder when a receiver power cycle is required. These do not enable PCIe-only high-rate or QHD modes on Cube hardware.

## 🌐 Networking

- **Ethernet control:** enable or disable Ethernet persistently; the interface status distinguishes administrative state from physical link state.
- **VLAN support:** configure an Ethernet VLAN ID and use DHCP or a static address on that interface.
- **AIC8800 SDIO fix:** invalid survey-channel values are handled safely instead of triggering a kernel assertion.
- **Built-in essentials:** VLAN, bridging, CPU frequency control, the SG2002 temperature sensor, zram and FQ/FQ-CoDel are now built into the kernel. Optional drivers and features remain loadable modules.

## 📟 OLED

- **Temporary IP display:** even when OLED is disabled in settings, acquiring an Ethernet or Wi-Fi client IPv4 address can show the addresses for 10 minutes, including Ethernet VLAN addresses.
- **Bounded display time:** address changes and reconnections do not extend an active window. Changing the OLED setting cancels the temporary display.
- **Burn-in protection:** the temporary address display moves periodically. Wi-Fi access-point and link-local addresses do not trigger it.
- **Correct codec label:** H.265 is displayed separately from H.264.

## ⚙️ System

- **Linux `7.2.5-nanokvm-os-r4`**, with matching modules and board boot profiles.
- Integrated codec clock configuration replaces the temporary runtime clock setup used during development.

## 📦 Installation

**Full SD image only. No `.nkos` update package is included.**

Download **`NanoKVM-OS-v1.0.0-beta-14.img.zip`** and **`SHA256SUMS`**, verify the checksum, extract the `.img`, and flash the SD card.

First boot selects the board profile and restarts automatically. Allow this process to finish before connecting to the web interface.

**Flashing an existing card replaces its installation and data.**

## 🙏 Thanks

**Special thanks to [@gxcreator](https://github.com/gxcreator) (Nikita S.) for his invaluable help.** He has opened every issue in this repository so far. His reports have helped identify problems and shape the fixes in this release — that contribution makes a real difference to NanoKVM OS.

Thanks to everyone reporting issues and testing hardware variants. Please include your board model, OS version and reproduction steps when reporting a problem.

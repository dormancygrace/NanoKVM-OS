# NanoKVM OS v1.0.0 beta-1

Community firmware for **NanoKVM Cube and NanoKVM PCIe (SG2002)**, based on Sipeed NanoKVM.

- 🖥️ **QHD 2560×1440 at 30 Hz**, automatic HDMI input detection and independent stream downscaling. FHD stream target up to 60 FPS.
- 🎞️ **H.265 Direct by default**, alongside H.264, WebRTC and MJPEG.
- 🔒 **HTTPS enabled by default**, with a unique certificate per device.
- 🔌 **USB Serial for headless Linux**, USB composition switching and capture controls.
- 🐧 **Linux 7.2.4**, updated userspace, reusable CMA/ION memory and configurable memory/network settings.
- ⚙️ **CryptoDMA with `/dev/crypto`** and **OpenVPN 2.7.7 with DCO**.
- 📦 **Signed application updates** for future releases; beta-1 ships as a full SD image.

> ⚠️ **QHD + H.265 + WebRTC is unstable and can freeze or restart the device. Use H.265 Direct for QHD.** Fresh-card first boot of this exact image, Cube-specific testing and sustained video/CryptoDMA qualification remain pending.

[Installation](https://github.com/dormancygrace/NanoKVM-OS/blob/main/docs/INSTALL.md) · [Known issues and validation](https://github.com/dormancygrace/NanoKVM-OS/blob/main/docs/VALIDATION.md) · [Source and redistribution status](https://github.com/dormancygrace/NanoKVM-OS/blob/main/docs/DISTRIBUTION.md)

<div align="center">

# 🖥️ NanoKVM OS

### Your tiny KVM. More possibilities.

**Community firmware compatible with NanoKVM Cube and NanoKVM PCIe.**

![Version: 1.0.0 beta-1](https://img.shields.io/badge/version-1.0.0--beta.1-orange)
![Hardware: Cube and PCIe](https://img.shields.io/badge/hardware-Cube%20%7C%20PCIe-blue)
![Platform: SG2002 RISC-V](https://img.shields.io/badge/platform-SG2002%20RISC--V-6366f1)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-green)](LICENSE)

[🚀 Install](docs/INSTALL.md) · [📦 Releases](https://github.com/dormancygrace/NanoKVM-OS/releases) · [🔌 Compatibility](#compatibility) · [⚖️ Compare](#comparison) · [💻 Build](docs/BUILD.md) · [🐛 Report an issue](https://github.com/dormancygrace/NanoKVM-OS/issues)

</div>

---

## ✨ What is NanoKVM OS?

**NanoKVM OS v1.0.0 beta-1** brings QHD video, a USB console for headless Linux, a newer Linux system and signed application updates to the SG2002-based NanoKVM you already own. Control a desktop through HDMI, or reach a Linux server's console through USB — from your browser.

The aim is a responsive IP-KVM with maintained system components, explicit recovery behavior and measurable resource use. This is an independent community project built on Sipeed NanoKVM and SOPHGO/CVITEK software, with credit to the original authors.

> [!NOTE]
> **Beta-1 full image — 2026-09-10 build:** HTTPS on by default, H.265 Direct as the initial video mode. **QHD H.265 WebRTC is unstable and can freeze or restart the device.** Use Direct for QHD. Fresh-card first boot and sustained qualification of this exact image remain pending; see [current limitations](#limitations).

## 🚀 At a glance

- **🖼️ More desktop space:** QHD 2560×1440 at 30 Hz, alongside FHD and lower resolutions.
- **🔌 A console without HDMI:** USB Serial (CDC ACM) for headless Linux, with access through the browser terminal.
- **🎞️ More video choices:** H.265 Direct by default; H.264, H.265 and MJPEG available. QHD H.265 WebRTC is marked unstable.
- **🔒 HTTPS from first boot:** a unique device certificate, HTTP redirect and secure browser access.
- **🧠 More control over memory:** a reusable CMA/ION pool and configurable memory settings.
- **🌐 VPN options:** OpenVPN 2.7.7 with the upstream DCO module, alongside Tailscale.
- **📦 Project-owned updates:** signed application packages, GitHub discovery, manual upload and startup rollback.

<a id="compatibility"></a>

## 🔌 Compatible devices

**NanoKVM OS is compatible with NanoKVM Cube and NanoKVM PCIe (SG2002).**

| Device | Firmware compatibility | Current physical validation |
|---|:---:|---|
| 🧊 **NanoKVM Cube** | ✅ Compatible | Cube-specific testing remains to be completed |
| 🧩 **NanoKVM PCIe** | ✅ Compatible | Active test platform; current checks use the UXC HDMI receiver |
| **NanoKVM Pro** | ❌ Not supported | Different hardware platform |
| **NanoKVM USB** | ❌ Not supported | Different product; not an SG2002 IP-KVM target |

Compatibility and completed testing are separate: the firmware targets both Cube and PCIe, while the recorded device tests currently come from PCIe/UXC. Features tied to the HDMI receiver, including QHD monitor switching, still need verification on other board revisions.

<a id="comparison"></a>

## ⚖️ Compared with original NanoKVM

This comparison uses the documented SG2002 Cube/PCIe features in the [Sipeed NanoKVM repository](https://github.com/sipeed/NanoKVM), not NanoKVM Pro. Upstream evolves, and some fixes contributed upstream may already be shared by both projects.

| Area | Original SG2002 NanoKVM | NanoKVM OS beta-1 |
|---|---|---|
| 🖼️ Video resolution | Up to 1920×1080 documented | Adds 2560×1440 at 30 Hz; validated mode transitions on the PCIe/UXC test board |
| 🎞️ Video formats | MJPEG and H.264 documented | MJPEG, H.264 and H.265; Direct and WebRTC paths for H.264/H.265 |
| 🖥️ Virtual HDMI monitor | Stock EDID and resolution controls | Separate automatic HDMI input/monitor profile and stream resolution; downscale while preserving aspect ratio. FHD prefers 60 Hz, QHD 30 Hz; BIOS fallback timings retained |
| ⏱️ Stream frame-rate control | Existing FPS control | 60/30/15/10 presets, integer custom 10–60; QHD capped at 30. This is a stream target, not a guarantee of captured/displayed FPS |
| 🐧 System | Vendor firmware baseline | Linux 7.2.4, Buildroot 2026.08 integration, updated userspace and matched drivers |
| 🧠 Memory | Vendor allocation policy | Reusable 64 MiB CMA/ION region and memory controls; allocations can still fail under pressure |
| 🔐 Crypto | Standard application encryption | SG2002 CryptoDMA SRTP adapter with software fallback; sustained stability remains under evaluation |
| 🌐 VPN | Tailscale and system networking | Includes OpenVPN 2.7.7 with upstream `ovpn` DCO module, alongside Tailscale |
| 📦 Updates | Original NanoKVM update ecosystem | Signed NanoKVM OS application packages, daily GitHub discovery and manual upload; original archives are rejected |
| 🔌 Headless Linux console | Serial terminal documented | Adds USB Serial (CDC ACM): access the managed Linux host through the browser terminal without HDMI, after configuring a host-side serial login service |
| ⌨️ Core KVM functions | Browser video, keyboard/mouse, virtual media, ATX, WoL and terminals | Retained, with USB composition and local access controls; device-level coverage is still being completed |

### 🔌 USB Serial: headless Linux, from your browser

NanoKVM presents a USB serial port to the managed computer and exposes its other end in the browser terminal. This supports a Linux login without a monitor after the host USB driver and serial login service start. It does not provide pre-USB BIOS/UEFI output. See [USB Serial setup](docs/usb-composition.md#host-serial-console).

H.265 needs a browser/platform that actually supports decoding it. Pion packetizes and transports encoded video; it does not transcode H.265 into H.264. Browser playback, latency and stability must be measured separately from encoder counters.

## 🚀 Installing NanoKVM OS

**First installation from stock firmware needs the full SD image.** No separate `.nkos` package is distributed for this first release; it would not install NanoKVM OS on stock firmware.

| First installation / system replacement | Later application updates |
|---|---|
| 📀 Download `.img.zip`, decompress and flash the whole SD card | 📦 Upload a signed `.nkos` package under Settings → Updates |
| Includes Linux, native libraries, application and updater | Replaces the server and web interface on a compatible OS base |

The full image is the only installation payload for **beta-1**, with fresh-card first-boot testing still pending. See [installation and recovery](docs/INSTALL.md).

## 📦 Updates

Open **Settings → Updates** to check GitHub or upload a `.nkos` package. The device checks this repository after startup and then daily; it never installs an update automatically. The page shows validation and installation status.

Application updates replace **only the server and web interface**. Kernel, root filesystem, native media libraries and device settings are not replaced. Packages must match the system's native-library fingerprint, carry the project's Ed25519 signature and have a newer release sequence. Renaming an original NanoKVM archive does not make it compatible.

Beta-1 is distributed as a full SD image. Application update packages will be provided with future compatible releases.

Full system images use the hardware **Boot flashing procedure**. They are not accepted by the application updater. See [update format and recovery](docs/UPDATES.md).

<a id="limitations"></a>

## 🧪 Current limitations

- **QHD H.265 WebRTC is unstable and can freeze or restart the device.** The underlying fault remains unresolved; use H.265 Direct for QHD.
- QHD monitor switching has been exercised on the test board, but the final full image and all browser/client combinations remain to be qualified.
- Forced application termination can leave native media buffers in an unusable state; application rollback is not a hardware reset.
- A watchdog cannot be assumed to recover every bus/SoC lockup. Physical power cycling may still be necessary.
- OpenVPN DCO passed an isolated tunnel/traffic/restart test on the device. External VPN interoperability and concurrent KVM load are separate checks.
- Full firmware source/notice consolidation and fresh-card recovery validation remain incomplete; see [distribution status](docs/DISTRIBUTION.md) and [validation](docs/VALIDATION.md).

## 💻 Source and builds

- `server/`: Go API, video sessions, Pion integration and signed updater.
- `web/`: React/TypeScript browser interface.
- `support/`: SG2002 native capture and board-service source.
- `firmware/`: kernel/driver patches, Buildroot integration, runtime changes and source pins.
- `scripts/`: component build/staging tools; external SDK and toolchain inputs are required.
- `tools/` and `kvmapp/system/init.d/`: EDID tools and device startup services.

See [BUILD.md](docs/BUILD.md). The repository starts with one source snapshot, without private development history or device credentials. Vendor dependencies are not automatically relicensed or made fully source-available by this snapshot.

## ❤️ Credits and licenses

NanoKVM application changes retain the upstream [GPL-3.0 license](LICENSE). Individual kernel, driver, Go, Pion, SDK and other third-party components retain their respective licenses and notices. See [third-party notices](docs/THIRD-PARTY.md).

Thanks to [Sipeed](https://github.com/sipeed/NanoKVM), [SOPHGO](https://github.com/sophgo), [Milk-V](https://github.com/milkv-duo), the Linux/Buildroot/Go communities and [Pion](https://github.com/pion). Please include board revision, browser, codec/transport, resolution, FPS target and reproduction steps when reporting an issue. Remove credentials and private screen contents from logs.

<div align="center">

# 🖥️ NanoKVM OS

### Your tiny KVM. More possibilities.

**Community firmware compatible with NanoKVM Cube and NanoKVM PCIe.**

![Version: 1.0.0 beta-11](https://img.shields.io/badge/version-1.0.0--beta.11-orange)
![Hardware: Cube and PCIe](https://img.shields.io/badge/hardware-Cube%20%7C%20PCIe-blue)
![Platform: SG2002 RISC-V](https://img.shields.io/badge/platform-SG2002%20RISC--V-6366f1)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-green)](LICENSE)

[🚀 Install](docs/INSTALL.md) · [📦 Releases](https://github.com/dormancygrace/NanoKVM-OS/releases) · [🔌 Compatibility](#compatibility) · [⚖️ Compare](#comparison) · [💻 Build](docs/BUILD-beta-11.md) · [🐛 Report an issue](https://github.com/dormancygrace/NanoKVM-OS/issues)

</div>

---

## ✨ What is NanoKVM OS?

**NanoKVM OS** brings QHD video, 5 GHz Wi-Fi support, a USB console for headless Linux, a refreshed Linux system and signed system-package updates to the SG2002-based NanoKVM you already own. Control a desktop through HDMI, or reach a Linux server's console through USB — from your browser.

The aim is a responsive IP-KVM with maintained system components, explicit recovery behavior and measurable resource use. This is an independent community project built on Sipeed NanoKVM and SOPHGO/CVITEK software, with credit to the original authors.

> [!NOTE]
> **Beta-11 — sequence 27:** HTTPS on by default, H.265 Direct when supported by the browser, with H.264 fallback. **QHD H.265 WebRTC is disabled.** Use H.265 Direct for QHD. Fresh-card partition creation and broad hardware/endurance qualification remain pending; see [validation](docs/VALIDATION.md).

## 🚀 At a glance

- **🖼️ More desktop space:** QHD 2560×1440 at 40 Hz, alongside FHD and lower resolutions.
- **📐 Portrait video:** four portrait monitor profiles with matching codec-aware capture controls.
- **📸 Screenshots:** save the current frame as a native-resolution PNG from Direct, WebRTC or MJPEG.
- **🔌 A console without HDMI:** USB Serial (CDC ACM) for headless Linux, with access through the browser terminal.
- **🎞️ More video choices:** H.265 Direct by default; H.264, H.265 and MJPEG available. QHD H.265 WebRTC is disabled.
- **🔒 HTTPS from first boot:** a unique device certificate, HTTP redirect and secure browser access.
- **🧠 More control over memory:** a reusable CMA/ION pool and configurable memory settings.
- **🌐 VPN menu:** WireGuard, OpenVPN 3 Core with upstream DCO and Tailscale, with profile controls and component versions.
- **📶 5 GHz Wi-Fi:** connect to 2.4 GHz and 5 GHz networks with a compatible adapter; scan networks, select a band and configure hidden networks in the browser.
- **📦 System updates and optional tools:** signed system packages for compatible layouts, plus an APK-managed add-on layer with dependency resolution.

## ✨ More project improvements

USB audio reaches the browser with one shared Opus encoder. Mount an ISO directly from your computer without copying it to SD; CD/DVD emulation now supports images up to 31.625 GiB. Dashboard shows SoC temperature and CPU frequency, with independent thermal protection and optional runtime overclocking.

OLED controls, IME input, horizontal scrolling, per-viewer WebRTC delivery, MJPEG, DHCP and VPN status have also improved. The system uses **Linux 7.2.5-nanokvm-os-r3**, **OpenSSL 4.0.2** and consistent **`-O2`** target C/C++ builds. Optional tools are installed through APK, while signed system updates keep the kernel and modules together. See [release notes and community credits](docs/RELEASE-beta-11.md).

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

| Area | Original SG2002 NanoKVM | NanoKVM OS |
|---|---|---|
| 🖼️ Video resolution | Up to 1920×1080 documented | Adds 2560×1440 at 40 Hz, with QHD monitor switching exercised on the PCIe/UXC test board |
| 🎞️ Video formats | MJPEG and H.264 documented | MJPEG, H.264 and H.265; Direct and WebRTC paths for H.264/H.265. Use Direct for QHD H.265; its WebRTC path is disabled |
| 🖥️ Virtual HDMI monitor | Stock EDID and resolution controls | Separate monitor preference and stream resolution; aspect-ratio-preserving downscaling. Automatic prefers QHD 40 Hz; explicit QHD 40 Hz, FHD 75 Hz and HD 120 Hz monitor profiles retain BIOS fallback timings |
| ⏱️ Stream frame-rate control | Existing FPS control | Adds 720p / 120 FPS, 1080p / 70 FPS and 1440p / 40 FPS profiles. Targets depend on source timing, codec and load; they do not guarantee delivered FPS |
| 🎚️ Video bitrate | Existing video quality controls | Adds 15 and 20 Mbit/s CBR targets for H.264/H.265 in Video settings and the toolbar; MJPEG keeps its quality controls |
| 📶 Wi-Fi | Optional Wi-Fi hardware | Adds connection to 5 GHz Wi-Fi networks alongside 2.4 GHz on compatible adapters, with network scanning, band selection, hidden-network setup and adapter information |
| 🐧 System | Vendor firmware baseline | Linux 7.2.5 with matching drivers/modules, Buildroot 2026.08, OpenSSL 4.0.2, refreshed system and Web dependencies, and consistent `-O2` target C/C++ builds |
| 🧠 Memory | Vendor allocation policy | Reusable 64 MiB CMA/ION region and configurable memory controls; allocations can still fail under pressure |
| 🔐 Crypto | Standard application encryption | SG2002 CryptoDMA SRTP adapter with software fallback; sustained stability remains under evaluation |
| 🌐 VPN | Tailscale and system networking | Browser-managed WireGuard and OpenVPN 3 Core with upstream `ovpn` DCO, alongside Tailscale; per-profile routing control |
| 🧩 Optional applications | Software supplied with stock firmware | Separate APK-managed add-on layer with dependency resolution and package revisions; install mc, Superfile, nano, htop, tcpdump, ethtool and BlueZ utilities as needed |
| 📦 Updates | Original NanoKVM update ecosystem | Signed full-system packages for compatible layouts, with GitHub discovery and manual upload; kernel and matching modules update together. Initial installation and partition-layout changes use a complete SD image |
| 🔌 Headless Linux console | Serial terminal documented | Adds USB Serial (CDC ACM): access the managed Linux host through the browser terminal without HDMI, after configuring a host-side serial login service; sessions keep independent settings and close their processes when finished |
| ⌨️ Core KVM functions | Browser video, keyboard/mouse, virtual media, ATX, WoL and terminals | Retained, with configurable USB composition, immediate USB on/off control and local access controls; device-level coverage is still being completed |

### 🔌 USB Serial: headless Linux, from your browser

NanoKVM presents a USB serial port to the managed computer and exposes its other end in the browser terminal. This supports a Linux login without a monitor after the host USB driver and serial login service start. It does not provide pre-USB BIOS/UEFI output. See [USB Serial setup](docs/usb-composition.md#host-serial-console).

H.265 needs a browser/platform that actually supports decoding it. Pion packetizes and transports encoded video; it does not transcode H.265 into H.264. Browser playback, latency and stability must be measured separately from encoder counters.

## 🚀 Installing NanoKVM OS

**First installation from stock firmware needs the full SD image.** A partition-layout change also requires a full image; subsequent system packages must match the installed layout.

| First installation / system replacement | Later package updates |
|---|---|
| 📀 Download `.img.zip`, decompress and flash the whole SD card | 📀 Beta-11 also requires the full image; no `.nkos` is published |
| Includes Linux, native libraries, application and updater | Updates the application and supported system components on a compatible OS base |

The current beta-11 release is a full SD image only; no `.nkos` package is published. Fresh-card first-boot testing remains pending. See [installation and recovery](docs/INSTALL.md).

## 🌐 VPN profiles

Open **Settings → VPN** and choose WireGuard, OpenVPN or Tailscale. WireGuard accepts `.conf` profiles; OpenVPN accepts `.ovpn` profiles and referenced certificate/key files in the same upload. Versions and connection status are shown in each page.

**Route Allowed IPs** is off by default for each WireGuard profile: only the subnet from the interface Address is routed. Enable it while the profile is stopped to install routes from AllowedIPs, including default routes. Uploaded AllowedIPs values are preserved. See [VPN setup](docs/VPN.md).

## 📦 Updates

Beta-11 is available as a **full SD image only**. Follow [installation](docs/INSTALL.md); flashing replaces settings and data. The existing package updater remains available for compatible packages from other releases, but beta-11 has no `.nkos` asset.

Signed system packages can update the application and supported system programs, libraries and data while preserving settings. The new package format can replace the kernel and matched modules on a compatible foundation. Bootloader updates and A/B boot rollback are not supported. Packages require a compatible system foundation, matching native libraries and a newer release sequence. See [package updates](docs/UPDATES.md).

Optional applications use a separate APK repository with dependency resolution. System packages and APK add-ons serve different purposes; see [installation](docs/INSTALL.md) for the current release’s supported update path.

Full system images use the hardware **Boot flashing procedure**. They are not accepted by the application updater. See [update format and recovery](docs/UPDATES.md).

<a id="limitations"></a>

## 🧪 Current limitations

- **QHD H.265 WebRTC is disabled.** The underlying fault remains unresolved; use H.265 Direct for QHD.
- Portrait and landscape video were exercised on the PCIe/UXC test board. Fresh-card creation and all browser/client combinations remain to be qualified; see [beta-11 acceptance](docs/BETA11-ACCEPTANCE.md).
- Forced application termination can leave native media buffers in an unusable state; application rollback is not a hardware reset.
- A watchdog cannot be assumed to recover every bus/SoC lockup. Physical power cycling may still be necessary.
- OpenVPN supports routed TUN profiles; TAP, scripts and interactive SSO/MFA are not supported. Local DCO traffic and DNS restoration were checked; broad provider interoperability remains unqualified.
- Simultaneous viewers share encoder settings. New viewers adopt the active settings; all browsers must support the selected codec.
- Full firmware source/notice consolidation and fresh-card recovery validation remain incomplete; see [distribution status](docs/DISTRIBUTION.md) and [validation](docs/VALIDATION.md).

## 💻 Source and builds

- `server/`: Go API, video sessions, Pion integration and signed updater.
- `web/`: React/TypeScript browser interface.
- `support/`: SG2002 native capture and board-service source.
- `firmware/`: kernel/driver patches, Buildroot integration, runtime changes and source pins.
- `scripts/`: component build/staging tools; external SDK and toolchain inputs are required.
- `tools/` and `kvmapp/system/init.d/`: EDID tools and device startup services.

See [BUILD.md](docs/BUILD-beta-11.md) for build instructions.

## ❤️ Credits and licenses

NanoKVM application changes retain the upstream [GPL-3.0 license](LICENSE). Individual kernel, driver, Go, Pion, SDK and other third-party components retain their respective licenses and notices. See [third-party notices](docs/THIRD-PARTY.md).

Thanks to [Sipeed](https://github.com/sipeed/NanoKVM), [SOPHGO](https://github.com/sophgo), [Milk-V](https://github.com/milkv-duo), the Linux/Buildroot/Go communities and [Pion](https://github.com/pion). Please include board revision, browser, codec/transport, resolution, FPS target and reproduction steps when reporting an issue. Remove credentials and private screen contents from logs.

## Beta-11

See [beta-11 release notes](docs/RELEASE-beta-11.md) and [beta-11 build inputs](docs/BUILD-beta-11.md). Fresh images disable SSH; enable it in the Web settings if needed.

Beta-11 retains the 64 MiB boot layout introduced in beta-9 and is distributed only as `NanoKVM-OS-v1.0.0-beta-11.img.zip`, under the GitHub tag `v1.0.0-beta.11`. See [release naming](docs/RELEASE-NAMING.md).

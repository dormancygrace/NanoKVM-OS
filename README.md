<div align="center">

# 🖥️ NanoKVM OS

### Your tiny KVM. More possibilities.

**Community firmware for SG2002 NanoKVM. PCIe/UXC is the current test platform; physical validation of Cube and Lite is pending.**

![Version: 2.0 b2](https://img.shields.io/badge/version-2.0--b2-orange)
![Hardware: Cube, Lite and PCIe](https://img.shields.io/badge/hardware-Cube%20%7C%20Lite%20%7C%20PCIe-blue)
![Platform: SG2002 RISC-V](https://img.shields.io/badge/platform-SG2002%20RISC--V-6366f1)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-green)](LICENSE)

[🚀 Install](docs/INSTALL.md) · [📦 Releases](https://github.com/dormancygrace/NanoKVM-OS/releases) · [🔌 Compatibility](#compatibility) · [⚖️ Compare](#comparison) · [💻 Build](docs/BUILD-v2.0-b1.md) · [🐛 Report an issue](https://github.com/dormancygrace/NanoKVM-OS/issues)

</div>

---

## ✨ What is NanoKVM OS?

**NanoKVM OS** brings QHD video, 5 GHz Wi-Fi support, a USB console for headless Linux, an Alpine Linux base and native signed APK updates to the SG2002-based NanoKVM you already own. Control a desktop through HDMI, or reach a Linux server's console through USB — from your browser.

The aim is a responsive IP-KVM with maintained system components, package updates that preserve settings, and measurable resource use. This is an independent community project built on Sipeed NanoKVM and SOPHGO/CVITEK software, with credit to the original authors.

## 🚀 At a glance

- **🖼️ More desktop space:** **QHD@50** (2560×1440), **FHD@75** (1920×1080) and **HD@120** (1280×720); delivered frame rate depends on source timing, codec and load.
- **📐 Portrait video:** four portrait monitor profiles with matching codec-aware capture controls.
- **📸 Screenshots:** save the current frame as a native-resolution PNG from Direct, WebRTC or MJPEG.
- **🔌 A console without HDMI:** USB Serial (CDC ACM) for headless Linux, with access through the browser terminal.
- **🎞️ More video choices:** H.265 Direct by default; H.264, H.265 and MJPEG available. QHD H.265 WebRTC is disabled.
- **🔒 HTTPS from first boot:** a unique device certificate, HTTP redirect and secure browser access.
- **🧠 More control over memory:** a reusable CMA/ION pool and configurable memory settings.
- **🌐 VPN menu:** WireGuard plus optional OpenVPN 2, Tailscale and NetBird clients, installed through APK from their settings.
- **📶 Wi-Fi across both bands:** automatic scanning, grouped 2.4/5 GHz networks and signal-strength icons. Prefer 5 GHz by default, with fallback to 2.4 GHz on compatible adapters.
- **📦 Software manager:** search, install, remove and update APK packages from **Settings → System → Software**, or use ordinary `apk` commands over SSH/the terminal.
- **🖱️ Shared viewing, explicit control:** additional browser sessions start without keyboard/mouse control; release or transfer control explicitly. The lock appears only when USB input is enabled.
- **⚙️ Native component updates:** the application, system integration, kernel, matching modules and firmware are packaged separately. Routine updates preserve settings and the writable root filesystem.

## ✨ More project improvements

USB audio reaches the browser with one shared Opus encoder. Mount an ISO directly from your computer without copying it to SD; CD/DVD emulation now supports images up to 31.625 GiB. Dashboard shows SoC temperature and CPU frequency, with independent thermal protection and optional runtime overclocking.

OLED controls, IME input, horizontal scrolling, per-viewer WebRTC delivery, MJPEG, DHCP and VPN status have also improved. The system uses **Alpine Linux 3.24**, **Linux 7.2.6-nanokvm-os-r1** and Alpine's standard OpenSSL libraries. The custom kernel retains its **`-O2`** build policy and selected userspace packages are optimized for C906. Packages install directly into the writable **F2FS root**, with dependency resolution and service handling through OpenRC. The interface also includes mobile settings improvements, custom branding and named Wake-on-LAN history entries. See [release notes](docs/RELEASE-v2.0-b2.md).

<a id="compatibility"></a>

## 🔌 Compatible devices

**The full image automatically selects a board-specific boot profile for the SG2002 NanoKVM family. The b1 components and native APK update were tested on PCIe/UXC. The b1 full image was assembled and checked, but fresh-card boot and Cube/Lite physical validation remain pending.**

| Device | Firmware compatibility | Current physical validation |
|---|:---:|---|
| 🧊 **NanoKVM Cube Full** | Alpha and serial-production profiles included | Physical acceptance pending |
| **NanoKVM Lite** | Base profile; ATX disabled | Physical acceptance pending |
| 🧩 **NanoKVM PCIe** | ✅ Compatible | Active test platform; current checks use the UXC HDMI receiver |
| **NanoKVM Pro** | ❌ Not supported | Different hardware platform |
| **NanoKVM USB** | ❌ Not supported | Different product; not an SG2002 IP-KVM target |

Recorded device tests currently come from PCIe/UXC. Included Cube Full and Lite profiles do not establish physical qualification of those models. Features tied to the HDMI receiver, including QHD monitor switching, still need verification on other board revisions.

<a id="comparison"></a>

## ⚖️ Compared with original NanoKVM

This comparison uses the documented SG2002 Cube/PCIe features in the [Sipeed NanoKVM repository](https://github.com/sipeed/NanoKVM), not NanoKVM Pro. Upstream evolves, and some fixes contributed upstream may already be shared by both projects.

| Area | Original SG2002 NanoKVM | NanoKVM OS |
|---|---|---|
| 🖼️ Video resolution | Up to 1920×1080 documented | QHD@50 (2560×1440), FHD@75 (1920×1080) and HD@120 (1280×720), with QHD monitor switching exercised on the PCIe/UXC test board |
| 🎞️ Video formats | MJPEG and H.264 documented | MJPEG, H.264 and H.265; Direct and WebRTC paths for H.264/H.265. Use Direct for QHD H.265; its WebRTC path is disabled |
| 🖥️ Virtual HDMI monitor | Stock EDID and resolution controls | Separate monitor preference and stream resolution; aspect-ratio-preserving downscaling. QHD@50, FHD@75 and HD@120 monitor profiles retain BIOS fallback timings |
| ⏱️ Stream frame-rate control | Existing FPS control | QHD / 50 FPS, FHD / 75 FPS and HD / 120 FPS targets. Targets depend on source timing, codec and load; they do not guarantee delivered FPS |
| 🎚️ Video bitrate | Existing video quality controls | Adds 15 and 20 Mbit/s CBR targets for H.264/H.265 in Video settings and the toolbar; MJPEG keeps its quality controls |
| 📶 Wi-Fi | Optional Wi-Fi hardware | Adds 5 GHz alongside 2.4 GHz on compatible adapters, automatic scanning across both bands, signal icons, hidden-network setup and a preferred band with fallback |
| 🐧 System | Vendor firmware baseline | Alpine 3.24 on writable F2FS, Linux 7.2.6 with matching modules, Alpine OpenSSL libraries and selected C906-optimized packages |
| 🧠 Memory | Vendor allocation policy | Reusable 64 MiB CMA/ION region and configurable memory controls; allocations can still fail under pressure |
| 🔐 Crypto | Standard application encryption | SG2002 CryptoDMA SRTP adapter with software fallback; sustained stability remains under evaluation |
| 🌐 VPN | Tailscale and system networking | Browser-managed WireGuard and optional OpenVPN 2, Tailscale and NetBird; native APK installation and profile/status controls |
| 🧩 Optional applications | Software supplied with stock firmware | Software GUI and native `apk add` / `apk del`, using the same package database in the system root; install tools such as nano, htop, mc and Python as needed |
| 📦 Updates | Original NanoKVM update ecosystem | Signed APK component updates through the GUI or `apk upgrade`, preserving settings and installed software. Kernel and matching modules update together; initial installation uses a full SD image |
| 🔌 Headless Linux console | Serial terminal documented | Adds USB Serial (CDC ACM): access the managed Linux host through the browser terminal without HDMI, after configuring a host-side serial login service; sessions keep independent settings and close their processes when finished |
| ⌨️ Core KVM functions | Browser video, keyboard/mouse, virtual media, ATX, WoL and terminals | Retained, with configurable USB composition, USB on/off control, server-enforced session ownership and named Wake-on-LAN history |

### 🔌 USB Serial: headless Linux, from your browser

NanoKVM presents a USB serial port to the managed computer and exposes its other end in the browser terminal. This supports a Linux login without a monitor after the host USB driver and serial login service start. It does not provide pre-USB BIOS/UEFI output. See [USB Serial setup](docs/usb-composition.md#host-serial-console).

H.265 needs a browser/platform that actually supports decoding it. Pion packetizes and transports encoded video; it does not transcode H.265 into H.264. Browser playback, latency and stability must be measured separately from encoder counters.

## 🚀 Installing NanoKVM OS

Download **[NanoKVM-OS-v2.0-b1.img.zip](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v2.0-b1/NanoKVM-OS-v2.0-b1.img.zip)** and **[SHA256SUMS](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v2.0-b1/SHA256SUMS)**, verify the ZIP checksum, extract the `.img` and flash the SD card. Use a card of at least **2 GB**.

| Your current installation | How to install b1 |
|---|---|
| Stock firmware, beta-14 or an older beta, or a blank SD card | Flash the full image; this replaces the existing installation and data |
| Published **v2.0 a2** | Use the existing GUI package updater or `apk update` followed by `apk upgrade`; no reflash or new signing key is needed |

The full image contains a **64 MiB boot partition** and **768 MiB F2FS system partition**. First boot creates an exFAT data partition from the remaining card space, selects the board profile and restarts automatically. See [installation and recovery](docs/INSTALL.md).

## 🌐 VPN profiles

Open **Settings → VPN** and choose WireGuard, OpenVPN, Tailscale or NetBird. OpenVPN, Tailscale and NetBird are optional: install them from their settings when needed. WireGuard accepts `.conf` profiles; OpenVPN accepts routed TUN `.ovpn` profiles and referenced certificate/key files in the same upload.

**Upgrading from a2:** b1 removes the bundled OpenVPN 3 client and its private OpenSSL 4 dependency. Install the optional **OpenVPN 2** client before reconnecting; existing profile files are retained. The OS base stays on Alpine 3.24; tagged edge/community packages are used only where required for optional clients.

**Route Allowed IPs** is off by default for each WireGuard profile: only the subnet from the interface Address is routed. Enable it while the profile is stopped to install routes from AllowedIPs, including default routes. Uploaded AllowedIPs values are preserved. See the [b1 release notes](docs/RELEASE-v2.0-b2.md) for the current VPN transition.

## 📦 Updates

NanoKVM OS v2 uses **native APK packages in the writable system root**. In b1, open **Settings → System → Software → Updates**. On a2, use **Settings → Updates → Package updates**. The same operations are available from the terminal:

```sh
apk update
apk upgrade
```

To add or remove software, use `apk add PACKAGE` and `apk del PACKAGE`, or the Software GUI. APK resolves dependencies and verifies signatures.

**The a2 → b1 component download is about 27.2 MB**, excluding repository metadata and updates to additional software. Settings, user data and independently installed packages are retained. Linux remains **7.2.6-nanokvm-os-r1**, so this transition does not rewrite the kernel or modules.

Completed transactions apply affected services through **OpenRC**. An application update briefly reconnects video and control; no manual apply command is needed. A future kernel update installs matching modules, selects the board's boot image and requires a reboot. Full system reinstallation remains a separate operation. See [updates](docs/UPDATES.md).

<a id="limitations"></a>

## 🧪 Current limitations

- **QHD H.265 WebRTC is disabled.** The underlying fault remains unresolved; use H.265 Direct for QHD.
- Portrait and landscape video were exercised on the PCIe/UXC test board. Fresh-card creation and all browser/client combinations remain to be qualified; see [beta-14 acceptance](docs/BETA14-ACCEPTANCE.md).
- Forced application termination can leave native media buffers in an unusable state; application rollback is not a hardware reset.
- A watchdog cannot be assumed to recover every bus/SoC lockup. Physical power cycling may still be necessary.
- OpenVPN supports routed TUN profiles; TAP, scripts and interactive SSO/MFA are not supported. DCO availability depends on the installed client and active kernel interface; broad provider interoperability remains unqualified.
- Simultaneous viewers share encoder settings. New viewers adopt the active settings and start without manual input ownership; all browsers must support the selected codec.
- Full firmware source/notice consolidation and fresh-card recovery validation remain incomplete; see [distribution status](docs/DISTRIBUTION.md) and [validation](docs/VALIDATION.md).

## 💻 Source and builds

- `server/`: Go API, video sessions, Pion integration and native APK update orchestration.
- `web/`: React/TypeScript browser interface.
- `support/`: SG2002 native capture and board-service source.
- `firmware/`: Alpine packaging and OpenRC services, kernel/driver patches, retained SDK/Buildroot inputs and source pins.
- `scripts/`: component build/staging tools; external SDK and toolchain inputs are required.
- `tools/` and `kvmapp/system/init.d/`: EDID tools and device startup services.

See [BUILD.md](docs/BUILD-v2.0-b1.md) for build instructions.

## ❤️ Credits and licenses

NanoKVM application changes retain the upstream [GPL-3.0 license](LICENSE). Individual kernel, driver, Go, Pion, SDK and other third-party components retain their respective licenses and notices. See [third-party notices](docs/THIRD-PARTY.md).

Thanks to [Sipeed](https://github.com/sipeed/NanoKVM), [SOPHGO](https://github.com/sophgo), [Milk-V](https://github.com/milkv-duo), the Alpine/Linux/Buildroot/Go communities and [Pion](https://github.com/pion). Please include board revision, browser, codec/transport, resolution, FPS target and reproduction steps when reporting an issue. Remove credentials and private screen contents from logs.

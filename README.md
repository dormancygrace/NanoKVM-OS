<div align="center">

# 🖥️ NanoKVM OS

### Your tiny KVM. More possibilities.

**Browser KVM, Internet over USB, remote access and a full Alpine system — on SG2002 NanoKVM hardware.**

[![Applications: v2.5-a1 Alpha](https://img.shields.io/badge/applications-v2.5--a1%20Alpha-orange)](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v2.5-a1)
[![Image: v1.0-a1 Alpha](https://img.shields.io/badge/SD%20image-v1.0--a1%20Alpha-orange)](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v2.5-a1)
![Platform: SG2002 RISC-V](https://img.shields.io/badge/platform-SG2002%20RISC--V-6366f1)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-green)](LICENSE)

[🚀 Install](#install) · [✨ Features](#features) · [📦 Releases](https://github.com/dormancygrace/NanoKVM-OS/releases) · [🔌 Hardware](#compatibility) · [🔄 Update](#updates) · [💻 Build](#build) · [🐛 Issues](https://github.com/dormancygrace/NanoKVM-OS/issues)

</div>

---

## ✨ What is NanoKVM OS?

**NanoKVM OS** is independent community firmware for the SG2002-based NanoKVM. Control a computer through HDMI and USB from a desktop or phone, mount installation media, reach a headless Linux console, or give the connected computer Internet through the same USB cable.

Underneath the interface is **Alpine Linux 3.24 on a writable F2FS root**, with ordinary APK packages, OpenRC services and official Alpine userspace. You can install software, update individual components and retain your settings without rebuilding or reflashing the entire card.

**Current alpha: applications v2.5-a1, bundled in Image v1.0-a1.** This release brings together the changes since v2.0-b7, including 4K support, restored H.265 WebRTC with a CryptoDMA fix, USB Internet sharing, RustDesk and the mobile-control improvements. See the [complete release notes](docs/RELEASE-v2.5-a1.md).

<a id="features"></a>
<a id="comparison"></a>

## 🚀 What you can do

### 🎬 Video, from HD to 4K

| Landscape resolution | Selectable frame-rate target |
| --- | --- |
| **4K / UHD — 3840 × 2160** | **Up to 30 FPS** |
| **QHD — 2560 × 1440** | **Up to 60 FPS** |
| **Full HD — 1920 × 1080** | **Up to 100 FPS** |
| **HD — 1280 × 720** | **Up to 120 FPS** |

These are targets, not guaranteed delivered rates. Available modes depend on the HDMI receiver, video-memory profile, source timing, codec and browser. The interface reports the input and encoded stream separately and explains unavailable choices.

- **H.264 and H.265 over Direct or WebRTC**, plus **MJPEG**. H.265 WebRTC is available again in v2.5-a1; older releases' QHD restriction no longer describes the current release.
- **Hardware video encoding**, bitrate and GOP controls, H.265 NormalP/SmartP, and Direct playback options for responsiveness or a smoother picture. Bitrate is an encoder target; actual traffic depends on the image and operating conditions.
- **MJPEG quality and JPEG colour sampling:** choose **4:2:2** (default) or **4:2:0**, with the selection saved across reboots.
- **Virtual monitor / EDID controls:** choose the resolution, refresh and orientation seen by the managed computer independently from the stream-size limit. Presets apply related settings together; **Same as input** preserves the input dimensions.
- **Portrait profiles:** 720 × 1280, 1080 × 1920, 1296 × 2304 for H.264, and 1440 × 2560 for H.265, subject to the reported device and browser capabilities.
- **PNG screenshots and browser-side recording**, plus USB audio playback in the browser through a shared Opus encoder.
- **HDMI enable/disable and capture idle timeout**, so capture can stop when no viewer is watching.

H.265 requires a browser and platform with a compatible decoder. Direct playback uses WebCodecs in a secure context. The device does not transcode a shared H.265 stream separately for a browser that only supports H.264.

### 📱 Control from a desktop or phone

- A movable, collapsible toolbar and responsive settings with reachable **Apply** controls.
- Relative mouse and absolute pointer modes, touchpad gestures, scrolling and IME input. **Hold one finger and tap with another to right-click.**
- An optional **Windows absolute-pointer profile** for USB/EDID display association on multi-monitor hosts.
- Multiple viewers with server-enforced input ownership. Additional sessions start with control locked; control can be transferred explicitly, and the sole remaining viewer receives it automatically when the owner disconnects.
- Controls follow the USB composition: disabled input functions are hidden, and the control lock is shown only when USB input is enabled.
- User accounts with administrator/user roles, administrator-only shared video settings and a **Logout** action.

Viewers share the device's encoder and video settings. Multiple sessions do not create independent Windows desktops or independent host mouse pointers.

### 🔌 One USB connection, several jobs

Configure the USB gadget through presets or individual switches for the keyboard, relative mouse, absolute pointer, network, virtual storage and serial console. The interface checks the controller's endpoint budget before applying a combination. USB can also be disabled entirely.

| Function | What it provides |
| --- | --- |
| **Keyboard and pointers** | Browser control, with normal and HID compatibility profiles |
| **Virtual media** | SD-backed images or direct ISO mounting from your computer without first copying the ISO to the card; CD/DVD images up to 31.625 GiB |
| **USB network** | Local access to NanoKVM through NCM/RNDIS configurations |
| **Internet over USB NCM** | Share the NanoKVM Ethernet/Wi-Fi uplink with the managed computer |
| **USB Serial / CDC ACM** | Reach a configured host serial console through the browser terminal |

**Internet over USB** has its own switch in **Settings → USB**, off by default. It provides IPv4 DHCP, DNS and NAT, with software flow offload for eligible TCP/UDP connections. Turning sharing off keeps local USB access. Existing VPN routing and firewall policies are respected; conflicting routes can leave sharing waiting for a usable uplink. See [USB Internet sharing](docs/usb-internet-sharing.md).

For a **headless Linux login**, enable USB Serial and configure a serial login service on the managed host. This works after its USB stack loads; it does not provide pre-USB BIOS/UEFI output. See [USB composition and serial setup](docs/usb-composition.md#host-serial-console).

ATX power/reset controls are available on supported boards. **Wake-on-LAN** includes a saved list of named MAC addresses for frequently used machines.

### 🌐 Network and remote access

- **Ethernet and Wi-Fi controls**, hostname, DNS, gateway, IPv6 and mDNS settings.
- **2.4 / 5 GHz Wi-Fi** on compatible adapters: automatic scanning across both bands, grouped network entries, signal-strength icons, hidden-network setup and a preferred band. **5 GHz is preferred by default**, with fallback to 2.4 GHz.
- A single **VPN overview** for **WireGuard, OpenVPN 2, Tailscale and NetBird**, with installation state, version and connection/profile controls. OpenVPN, Tailscale and NetBird are optional APK installations.
- **WireGuard `.conf` profiles** with an explicit Route Allowed IPs option; **OpenVPN routed TUN `.ovpn` profiles**, including referenced certificate/key files in the same upload.
- **HTTPS from first boot** with a unique device certificate and HTTP redirect. Newly generated certificates use ECDSA P-256; existing certificates are preserved. SSH can be enabled from settings.

### 🧩 Software and add-ons

**Software is a top-level settings section.** Its package manager searches repositories and installs, removes or updates packages using the same APK database as the terminal. Install tools such as `nano`, `htop`, `mc` or Python as needed.

| Add-on | Installation and capabilities |
| --- | --- |
| **RustDesk** | Optional signed APK, managed from Software. Share the captured HDMI display and USB input using the official RustDesk servers or your own; temporary/permanent passwords, H.264/H.265, direct/relay TCP and optional WebRTC. Based on the RustDesk 1.5 protocol. |
| **PicoClaw** | Install, update or remove the official latest RISC-V release from Software → Add-ons. Configure the model, API key and endpoint for remote-control or general-assistant use. Its navigation entry appears only while installed. |

RustDesk can forward USB audio; its integration does not include clipboard, file transfer, terminal, chat or ATX control. Its [signed package and corresponding source](https://github.com/dormancygrace/NanoKVM-OS-packages/releases/tag/nanokvm-rustdesk-0.5.3-r2) are published separately from the base image.

PicoClaw is downloaded from its upstream release, **not packaged as a NanoKVM APK**. An **MCP endpoint** with enable/disable and API-key controls is also available under System for external AI clients.

### ⚙️ System controls and everyday tools

| Area | Controls and information |
| --- | --- |
| **Dashboard** | CPU load, RAM, SoC temperature, CPU frequency, storage, network and VPN status |
| **Memory** | video-memory modes by resolution (FHD 56 MiB by default, QHD 72 MiB, UHD 128 MiB; reusable CMA or fixed); ZRAM with automatic sizing and optional ZSTD recompression; SD swap; application memory-limit setting |
| **CPU** | Frequency control, thermal protection and an explicit Apply at startup option; an incomplete overclocked startup triggers a 1000 MHz fallback on the next boot |
| **Diagnostics** | Service, USB, firewall and package-operation status, plus system logs in the GUI |
| **Terminal** | Browser shell and serial sessions, with shell command history |
| **Appearance** | Menu layout/icons, colours, language, web title, bundled branding choices and custom logo/favicon uploads |
| **Device** | OLED settings, mouse jiggler, date/time and reboot controls |

The interface shares live-status polling, loads settings pages on demand, and serves compressed, cacheable assets. Package operations are serialized and check available memory before starting. OpenRC manages services independently of video initialization.

<a id="compatibility"></a>

## 🔌 Compatible devices

NanoKVM OS targets the **SG2002 RISC-V NanoKVM family**. The full image includes automatic board-profile selection.

| Device | Included support | Physical validation |
| --- | --- | --- |
| **NanoKVM Cube Full** | Alpha and serial-production board profiles | Pending |
| **NanoKVM Lite** | Base profile, without ATX | Pending |
| **NanoKVM PCIe** | Board profile and receiver detection | PCIe/UXC is the active development test platform |
| **NanoKVM Pro** | Not supported | Different hardware platform |
| **NanoKVM USB** | Not supported | Different product; not an SG2002 IP-KVM target |

Receiver-specific features, including higher resolutions and programmable EDID, vary by board. Included profiles are not a claim of physical qualification on every revision. See [board support](firmware/boards/README.md).

<a id="install"></a>

## 🚀 Install: Image v1.0-a1

**Image v1.0-a1 bundles applications v2.5-a1. Both are alpha releases.**

1. Download the [full SD image](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v2.5-a1/NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip) and [SHA256SUMS](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v2.5-a1/SHA256SUMS).
2. Verify the ZIP checksum, extract the `.img` and flash it to an SD card of at least **2 GB**. Flashing replaces the card's existing installation and data.
3. Boot NanoKVM, allow automatic board selection and its restart to finish, then open `https://DEVICE-IP/`.
4. Sign in with **admin / admin** and set a new password when prompted. For the administrator, this also sets the Linux root password used by SSH. SSH is disabled by default.

The ZIP is **66.8 MiB**. The image uses a **64 MiB boot partition** and **768 MiB F2FS root**; first boot creates an exFAT data partition in the remaining space. See [installation details](docs/INSTALL.md).

Image and application versions are independent: later APK updates preserve the original image identity. **About** shows the installed image, its bundled applications and the current application version.

<a id="updates"></a>

## 🔄 Update an existing installation

**v2.0-a2 or newer:** use **Settings → System → Updates** (on a2: **Settings → Updates → Package updates**) or run as root:

```sh
apk update
apk upgrade
reboot
```

Updates retain settings, user data and independently installed packages. The seven core v2.5-a1 packages total **36.5 MiB**; add-ons and Alpine dependency updates are additional. The kernel and matching modules update to **7.2.9-nanokvm-os-r1** and need a reboot. Application service updates use OpenRC automatically; no separate apply command is needed.

- **Local `.apk` installations:** file-pinned packages may require an `apk add` step before upgrading; see [update instructions](docs/UPDATES.md).
- **Signing transition:** `nanokvm-keys` installs the public keys and moves the official repository to the RSA/ECDSA-signed v3 index automatically. The RSA-signed legacy index remains available for older installations.
- **C906 overlay installations:** follow the [stock Alpine migration instructions](firmware/alpine/README.md#existing-c906-installations). Updating NanoKVM alone does not remove the overlay.
- **Stock firmware or pre-v2 releases:** install the full SD image. Legacy `.nkos` updates do not migrate the system to Alpine.

Component updates preserve the installed bootloader. A full image includes the rebuilt bootloader as well as the applications and system packages.

<a id="limitations"></a>

## 🧪 Release status and limits

- **v2.5-a1 / Image v1.0-a1 is alpha.** The final combined artifacts passed host builds, tests, signature and integrity checks; installation of this exact set and persistent full-image SD cold boot remain unqualified. Earlier component tests do not replace final hardware acceptance. See [validation](docs/RELEASE-v2.5-a1-VALIDATION.md).
- Frame-rate targets, portrait modes and H.265 decoding depend on the source, receiver, memory profile and client. Viewers share the active codec and encoder settings.
- USB functions share a limited endpoint budget; not every combination can be enabled together. Internet sharing is IPv4-only.
- OpenVPN supports routed TUN profiles; TAP, scripts and interactive SSO/MFA are not supported.
- Native media or SoC lockups can require physical power cycling; a service restart or watchdog is not guaranteed to recover every hardware fault.

<a id="build"></a>

## 💻 Build from the public repository

The repository contains the application, browser UI, native integration, kernel/driver patches and complete image build. It fetches upstream sources at the revisions and hashes recorded in [`platform/sources.lock`](platform/sources.lock), then checks build outputs against [`platform/expected.sha256`](platform/expected.sha256). Separate private SDK checkouts are not required.

For a local test build, after installing the [host prerequisites](platform/README.md#build):

```sh
git clone https://github.com/dormancygrace/NanoKVM-OS.git
cd NanoKVM-OS
git checkout v2.5-a1
platform/build.sh -d
```

`-d` generates **local test signing keys**. Official release builds require the maintainer's RSA and ECDSA keys; test-signed packages are not official updates. See [build and signing instructions](platform/README.md).

The platform uses **Linux 7.2.9**, **U-Boot 2026.07**, **OpenSBI 1.9** and a **GCC 16.2 / T-Head C906** toolchain. Kernel/modules use `-O3` without LTO; native userspace uses the reviewed scalar `-O2` profile. Web builds use **Node.js 24 LTS**. Ordinary userspace, including OpenSSL, comes from official Alpine repositories; the optional C906 package profile is retained for experiments.

Pinned platform inputs, Go modules and the web lockfile fix their respective source versions. Alpine 3.24 repositories continue receiving updates, so later builds can install newer Alpine packages; the build records them in `release/installed-packages.txt`.

| Source directory | Contents |
| --- | --- |
| `server/`, `web/` | Go application, streaming/API services and React/TypeScript interface |
| `support/`, `native/`, `tools/` | Native media/board integration, USB audio and device tools |
| `firmware/` | Alpine recipes, OpenRC integration, retained SDK inputs and board support |
| `platform/`, `scripts/` | Pinned platform build, patches, packaging and image generation |

## ❤️ Credits and licenses

Built on the work of [Sipeed](https://github.com/sipeed/NanoKVM), [SOPHGO](https://github.com/sophgo), [Milk-V](https://github.com/milkv-duo), Alpine, Linux, Buildroot, Go, [Pion](https://github.com/pion) and [OneKVM](https://github.com/onekvm), with thanks to the contributors and hardware testers.

NanoKVM application changes retain the upstream [GPL-3.0 license](LICENSE). Other components retain their own licenses and notices. See [third-party notices](docs/THIRD-PARTY.md), [distribution status](docs/DISTRIBUTION.md) and the [corresponding-source build](platform/README.md#corresponding-source). Release downloads include the corresponding-source archive; RustDesk has its own source release.

When [reporting an issue](https://github.com/dormancygrace/NanoKVM-OS/issues), include the board/HDMI receiver, application version, browser/client, codec and transport, resolution/FPS target and reproduction steps. Remove credentials and private screen contents from logs.

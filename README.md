<div align="center">

# 🖥️ NanoKVM OS

### Your tiny KVM. More possibilities.

**Browser KVM, Internet over USB, remote access and a full Alpine system — on SG2002 NanoKVM hardware.**

[![Applications: v3.0-a1 Alpha](https://img.shields.io/badge/applications-v3.0--a1%20Alpha-orange)](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v3.0-a1)
[![Image: v1.2-a1 Alpha](https://img.shields.io/badge/SD%20image-v1.2--a1%20Alpha-orange)](https://github.com/dormancygrace/NanoKVM-OS/releases/tag/v3.0-a1)
![Platform: SG2002 RISC-V](https://img.shields.io/badge/platform-SG2002%20RISC--V-6366f1)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-green)](LICENSE)

[🚀 Install](#install) · [✨ Features](#features) · [📦 Releases](https://github.com/dormancygrace/NanoKVM-OS/releases) · [🔌 Hardware](#compatibility) · [🔄 Update](#updates) · [💻 Build](#build) · [🧪 Help test](#help-test) · [🐛 Issues](https://github.com/dormancygrace/NanoKVM-OS/issues)

</div>

---

## ✨ What is NanoKVM OS?

**NanoKVM OS** is independent community firmware for the SG2002-based NanoKVM. Control a computer through HDMI and USB from a desktop or phone, mount installation media, reach a headless Linux console, or give the connected computer Internet through the same USB cable.

Underneath the interface is **Alpine Linux 3.24 on a writable F2FS root**, with ordinary APK packages, OpenRC services and official Alpine userspace. You can install software, update individual components and retain your settings without rebuilding or reflashing the entire card.

**Current alpha: applications v3.0-a1, bundled in Image v1.2-a1 — Massive Overhaul.** This release collects the changes since v2.5-a1, including the unpublished v2.6 alpha series: reworked video controls and memory modes, recovery and performance improvements, USB storage and serial tools, Wi-Fi preferences, HTTPS/ACME, add-on updates, and community translations. See the [complete release notes](docs/RELEASE-v3.0-a1.md).

> [!IMPORTANT]
> **Help wanted — NanoKVM Cube, Lite and PCIe testers!**
> More real devices make NanoKVM OS better. We need owners to try releases and report what works, as well as what does not. **No coding required.** Even a short “works on my board” report helps us confirm support and catch regressions. [Help test your NanoKVM →](#help-test)

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

- **H.264 and H.265 over Direct or WebRTC**, plus **MJPEG**. Each browser chooses its transport and player; viewers share the device’s codec, picture settings and hardware budget.
- **Hardware video encoding**, automatic/economy/manual bitrate controls, measured bitrate, and Direct playback options for responsiveness or a smoother picture. NormalP keyframes are requested as needed, with a periodic safety interval. Bitrate is an encoder ceiling; actual traffic depends on the image and operating conditions.
- **Adaptive MJPEG quality and JPEG colour sampling:** quality falls when all viewers lag and recovers up to your selected limit. Choose **4:2:2** (default) or **4:2:0**, with the selection saved across reboots.
- **Virtual monitor / EDID controls:** choose the resolution, refresh and orientation seen by the managed computer independently from the stream-size limit. Video modes apply monitor resolution and frame rate together; **Same as input** preserves the input dimensions.
- **Portrait profiles:** 720 × 1280, 1080 × 1920, 1296 × 2304 for H.264, and 1440 × 2560 for H.265, subject to the reported device and browser capabilities.
- **PNG screenshots and browser-side recording**, plus USB audio playback in the browser through a shared Opus encoder.
- **Playback recovery:** Direct requests fresh keyframes after dropped frames; stalled capture has a bounded recovery path. H.264/H.265 streams carry BT.709 colour information.
- **HDMI enable/disable and capture idle timeout**, so capture can stop when no viewer is watching.

H.265 requires a browser and platform with a compatible decoder. Direct playback uses WebCodecs in a secure context. The device does not transcode a shared H.265 stream separately for a browser that only supports H.264. MJPEG and JPEG-based screenshots are unavailable with a 4K HDMI input; concurrent MJPEG and H.264/H.265 viewers share capture resources and can reduce delivered FPS.

### 📱 Control from a desktop or phone

- A movable, collapsible toolbar and responsive settings with reachable **Apply** controls.
- Relative mouse and absolute pointer modes, touchpad gestures, scrolling and IME input. **Hold one finger and tap with another to right-click.**
- **One-click keyboard sequences:** Ctrl+Alt+Del, Magic SysRq and REISUB, with timing handled by the device.
- An optional **Windows absolute-pointer profile** for USB/EDID display association on multi-monitor hosts.
- Multiple viewers with server-enforced input ownership. Additional sessions start with control locked; control can be transferred explicitly, and the sole remaining viewer receives it automatically when the owner disconnects.
- Controls follow the USB composition: disabled input functions are hidden, and the control lock is shown only when USB input is enabled.
- User accounts with administrator/user roles, administrator-only shared video settings and a **Logout** action.

Viewers share the device's encoder and video settings. Multiple sessions do not create independent Windows desktops or independent host mouse pointers.

### 🔌 One USB connection, several jobs

Configure the USB gadget through presets (KVM, with USB network, with the serial console, a headless server, remote desktop, keyboard and mouse only) or individual switches for the keyboard, relative mouse, absolute pointer, network, virtual storage, serial console and audio. The interface checks the controller's endpoint budget before applying a combination and says what each preset gives up. USB can also be disabled entirely. You can also configure the USB device identity (VID, PID and identifying strings).

| Function | What it provides |
| --- | --- |
| **Keyboard and pointers** | Browser control, with normal and HID compatibility profiles |
| **Virtual media** | SD-backed images or direct ISO mounting from your computer without first copying the ISO to the card; CD/DVD images up to 31.625 GiB |
| **Network boot** | Boot the managed computer from the [netboot.xyz](docs/netboot.md) menus over USB network, with the official iPXE images (UEFI Secure Boot signed one included) |
| **User disk** | A writable FAT32 USB drive backed by an image on the SD card, for BIOS update files, profiles and logs; selectable sizes from 64 MiB to 4 GiB |
| **USB network** | Local access to NanoKVM through the USB NCM adapter |
| **Internet over USB NCM** | Share the NanoKVM Ethernet/Wi-Fi uplink with the managed computer |
| **USB Serial / CDC ACM** | Reach a configured host serial console through the browser terminal |

**Internet over USB** has its own switch in **Settings → USB**, off by default. It provides IPv4 DHCP, DNS and NAT, with software flow offload for eligible TCP/UDP connections. Turning sharing off keeps local USB access. Existing VPN routing and firewall policies are respected; conflicting routes can leave sharing waiting for a usable uplink. See [USB Internet sharing](docs/usb-internet-sharing.md).

For a **headless Linux login**, enable USB Serial and set the login up on the managed host with the console helper from the USB settings page: a script that reads the host first, refuses when it cannot work safely, keeps backups and can undo; it also comes on a read-only helper disk for hosts without a network. This works after the host USB stack loads; it does not provide pre-USB BIOS/UEFI output. See [USB composition and serial setup](docs/usb-composition.md#host-serial-console).

ATX power/reset controls are available on supported boards. **Wake-on-LAN** includes a saved list of named MAC addresses for frequently used machines.

### 🌐 Network and remote access

- **Ethernet and Wi-Fi controls**, hostname, DNS, gateway, IPv6 and mDNS settings. Ethernet shows live link status and the current address; static IPv4 accepts CIDR notation and an optional gateway for direct computer connections.
- **2.4 / 5 GHz Wi-Fi** on compatible adapters: automatic scanning across both bands, grouped network entries, signal-strength icons, hidden-network setup and a preferred band. **5 GHz is preferred by default**, with fallback to 2.4 GHz.
- A single **VPN overview** for **WireGuard, OpenVPN 2, Tailscale and NetBird**, with installation state, version and connection/profile controls. OpenVPN, Tailscale and NetBird are optional APK installations.
- **WireGuard `.conf` profiles** with an explicit Route Allowed IPs option; **OpenVPN routed TUN `.ovpn` profiles**, including referenced certificate/key files in the same upload.
- **HTTPS from first boot** with a unique device certificate and HTTP redirect. Newly generated certificates use ECDSA P-256; existing certificates are preserved. A **custom certificate** (your own CA or a public one) can be uploaded in Settings → Network and takes effect for new connections without a restart; a custom pair that cannot be loaded never blocks start-up (the generated one stands in). **Let's Encrypt / ZeroSSL / Buypass / Google** certificates through **acme.sh**, installed from its latest release on demand: HTTP-01 on port 80 or DNS-01 with every DNS provider acme.sh ships, renewed from the server and served without a restart. SSH can be enabled from settings.

### 🧩 Software and add-ons

**Software is a top-level settings section.** Its package manager searches repositories and installs, removes or updates packages using the same APK database as the terminal. Install tools such as `nano`, `htop`, `mc` or Python as needed.

**Daily package-index refresh** is optional and off by default. Enable it in Software → Packages and choose a time in the device timezone. It refreshes the available-package lists; it does **not** automatically install updates. Manage optional software in Software → Add-ons. RustDesk updates are discovered from the signed repository, including for older file-pinned installations.

| Add-on | Installation and capabilities |
| --- | --- |
| **[RustDesk](https://github.com/rustdesk/rustdesk)** | Optional signed APK, managed from Software. Share the captured HDMI display and USB input using the official RustDesk servers or your own; temporary/permanent passwords, H.264/H.265, direct/relay TCP and optional WebRTC. Based on the RustDesk 1.5 protocol. |
| **[PicoClaw](https://github.com/sipeed/picoclaw)** | Install, update or remove the official latest RISC-V release from Software → Add-ons. Configure the model, API key and endpoint for remote-control or general-assistant use. Its navigation entry appears only while installed. |
| **[acme.sh](https://github.com/acmesh-official/acme.sh)** | Install on demand from Software → Add-ons, then configure HTTPS certificates in Settings → Network. Issue and renew certificates through ACME using HTTP-01 or DNS-01 with supported DNS providers; renewed certificates take effect without restarting the service. |

RustDesk can forward USB audio; its integration does not include clipboard, file transfer, terminal, chat or ATX control. Its [signed packages and corresponding sources](https://github.com/dormancygrace/NanoKVM-OS-packages/releases) are published separately from the base image.

PicoClaw is downloaded from its upstream release, **not packaged as a NanoKVM APK**. An **MCP endpoint** with enable/disable and API-key controls is also available under System for external AI clients.

### ⚙️ System controls and everyday tools

| Area | Controls and information |
| --- | --- |
| **Dashboard** | CPU load, RAM, SoC temperature, CPU frequency, storage, network and VPN status |
| **Memory** | Fixed video-memory modes: **FHD 40 MiB** (default), **QHD 56 MiB**, **UHD 104 MiB**, changed with a reboot; ZRAM with automatic sizing and optional ZSTD recompression; SD swap; application memory-limit setting |
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
| **NanoKVM Cube Full** | Alpha and serial-production board profiles | Community-tested: serial-production board detection and OLED confirmed on beta13 ([#2](https://github.com/dormancygrace/NanoKVM-OS/issues/2#issuecomment-5694454091)) |
| **NanoKVM Lite** | Base profile, without ATX | Community-tested on v3 after upgrading from v2.5: boot, 1080p60 video and KVM control confirmed ([#215](https://github.com/dormancygrace/NanoKVM-OS/issues/215)) |
| **NanoKVM PCIe** | Board profile and receiver detection | PCIe/UXC development bench; LT6911D video confirmed by a community tester on v2.0-b5 ([#8](https://github.com/dormancygrace/NanoKVM-OS/issues/8#issuecomment-5825067894)) |
| **NanoKVM Pro** | Not supported | Different hardware platform |
| **NanoKVM USB** | Not supported | Different product; not an SG2002 IP-KVM target |

Receiver-specific features, including higher resolutions and programmable EDID, vary by board. Included profiles are not a claim of physical qualification on every revision. See [board support](firmware/boards/README.md).

<a id="help-test"></a>

## 🧪 Help wanted: test on Cube, Lite and PCIe

**Hardware testing is one of the most valuable ways to help this project.** The development bench cannot cover every board revision, HDMI receiver, computer and browser. Cube and Lite owners are especially welcome; additional PCIe units and receivers are just as useful.

1. Try the [current release](https://github.com/dormancygrace/NanoKVM-OS/releases/latest) using the [installation](#install) or [update](#updates) instructions.
2. Check what you normally use: boot and network access, video and audio, keyboard/mouse, USB media, and OLED or ATX controls where your board has them. A desktop or phone session and reconnecting after a reboot are useful checks. **You do not need to test every feature.**
3. [Share a test report](https://github.com/dormancygrace/NanoKVM-OS/issues/new?title=Hardware%20test%20report%3A%20) — a successful test is worth reporting too. Include your **board/revision**, HDMI receiver if known, **application and image versions** from About, fresh install or upgrade, browser/OS, and the resolution/FPS, codec and transport you tried. Say what worked and what failed; for a failure, add reproduction steps and relevant GUI logs.

Already helping with an issue? Add your results there instead of opening a duplicate. Please remove passwords, tokens and private screen contents from reports. **Thank you — each real-device report helps us fix regressions and make the compatibility table more useful.**

<a id="install"></a>

## 🚀 Install: Image v1.2-a1

**Image v1.2-a1 bundles applications v3.0-a1. Both are alpha releases.**

1. Download the [full SD image](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v3.0-a1/NanoKVM-OS-Image-v1.2-a1-apps-v3.0-a1.img.zip) and [SHA256SUMS](https://github.com/dormancygrace/NanoKVM-OS/releases/download/v3.0-a1/SHA256SUMS).
2. Verify the ZIP checksum, extract the `.img` and flash it to an SD card of at least **2 GB**. Flashing replaces the card's existing installation and data.
3. Boot NanoKVM, allow automatic board selection and its restart to finish, then open `https://DEVICE-IP/`.
4. Sign in with **admin / admin** and set a new password when prompted. For the administrator, this also sets the Linux root password used by SSH. SSH is disabled by default.

The ZIP is **64.0 MiB**. The image uses a **64 MiB boot partition** and **768 MiB F2FS root**; first boot creates an exFAT data partition in the remaining space. See [installation details](docs/INSTALL.md).

Image and application versions are independent: later APK updates preserve the original image identity. **About** shows the installed image, its bundled applications and the current application version.

<a id="updates"></a>

## 🔄 Update an existing installation

**v2.0-a2 or newer:** use **Settings → System → Updates** (on a2: **Settings → Updates → Package updates**) or run as root:

```sh
apk update
apk upgrade
reboot
```

Updates retain settings, user data and independently installed packages. The seven core v3.0-a1 packages total **34.3 MiB**; add-ons and Alpine dependency updates are additional. The kernel and matching modules update to **7.2.10-nanokvm-os-r5** and need a reboot. Application service updates use OpenRC automatically; no separate apply command is needed.

- **Local `.apk` installations:** file-pinned packages may require an `apk add` step before upgrading; see [update instructions](docs/UPDATES.md).
- **Signing transition:** `nanokvm-keys` installs the public keys and moves the official repository to the RSA/ECDSA-signed v3 index automatically. The RSA-signed legacy index remains available for older installations.
- **C906 overlay installations:** follow the [stock Alpine migration instructions](firmware/alpine/README.md#existing-c906-installations). Updating NanoKVM alone does not remove the overlay.
- **Stock firmware, pre-v2 releases or the original v2.0-a1:** install the full SD image. Legacy `.nkos` updates do not migrate the system to Alpine.

Component updates preserve the installed bootloader. A full image includes the rebuilt bootloader as well as the applications and system packages.

<a id="limitations"></a>

## 🧪 Release status and limits

- **v3.0-a1 / Image v1.2-a1 is alpha.** The rebuilt artifacts passed host tests, source and image integrity checks, and signature verification. An earlier candidate was tested on hardware; the final refresh was not installed on the test device. See the [release notes](docs/RELEASE-v3.0-a1.md).
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
git checkout v3.0-a1
platform/build.sh -d
```

`-d` generates **local test signing keys**. Official release builds require the maintainer's RSA and ECDSA keys; test-signed packages are not official updates. See [build and signing instructions](platform/README.md).

The platform uses **Linux 7.2.10**, **U-Boot 2026.10**, **OpenSBI 1.9** and a **GCC 16.2 / T-Head C906** toolchain. Kernel/modules use `-O3` without LTO; native userspace uses the reviewed scalar `-O2` profile. Web builds use **Node.js 24 LTS**. Ordinary userspace, including OpenSSL, comes from official Alpine repositories; the optional C906 package profile is retained for experiments.

Pinned platform inputs, Go modules and the web lockfile fix their respective source versions. Alpine 3.24 repositories continue receiving updates, so later builds can install newer Alpine packages; the build records them in `release/installed-packages.txt`.

| Source directory | Contents |
| --- | --- |
| `server/`, `web/` | Go application, streaming/API services and React/TypeScript interface |
| `support/` | Native media/board integration and device tools (`kvm_system`, MMF, `nanokvm_update_edid`) |
| `firmware/` | Alpine recipes, OpenRC integration, retained SDK inputs and board support |
| `platform/`, `scripts/` | Pinned platform build, patches, packaging and image generation |
| `tests/` | Host-side tests of the init scripts, build, packaging and update tooling |

## 🌍 Translations

The language menu includes 25 languages, with partial translations falling back to English. Hebrew is available as a translation starter. Translations are maintained on [Hosted Weblate](https://hosted.weblate.org/engage/nanokvm-os/): anyone can fix or add strings in the browser, without git. See [TRANSLATING.md](TRANSLATING.md).

### 🌍 Made welcoming by our translators

Thank you to **[Pavel Borecki](https://hosted.weblate.org/user/pavelbo/)** for Czech, **[Alexander-Ger-Reich](https://github.com/Alexander-Ger-Reich)** for German and English UI maintenance, and **[polyzium](https://github.com/polyzium)** for Russian localization — and to everyone who translates, reviews, and improves the words on screen. See the [translation credits and contribution guide](TRANSLATING.md#translation-credits).

**[Hosted Weblate](https://hosted.weblate.org/engage/nanokvm-os/)** is our community translation platform. Thank you to the Weblate project for the tools and hosting that let people contribute directly from their browser. [Help translate NanoKVM OS](https://hosted.weblate.org/engage/nanokvm-os/).

## ❤️ Credits and licenses

Built on the work of [Sipeed](https://github.com/sipeed/NanoKVM), [SOPHGO](https://github.com/sophgo), [Milk-V](https://github.com/milkv-duo), Alpine, Linux, Buildroot, Go, [Pion](https://github.com/pion) and [OneKVM](https://github.com/onekvm), with thanks to the contributors and hardware testers.

NanoKVM application changes retain the upstream [GPL-3.0 license](LICENSE). Other components retain their own licenses and notices. See [third-party notices](docs/THIRD-PARTY.md), [distribution status](docs/DISTRIBUTION.md) and the [corresponding-source build](platform/README.md#corresponding-source). Release downloads include the corresponding-source archive; RustDesk has its own source release.

When [reporting an issue](https://github.com/dormancygrace/NanoKVM-OS/issues), include the board/HDMI receiver, application version, browser/client, codec and transport, resolution/FPS target and reproduction steps. Remove credentials and private screen contents from logs.

<div align="center">

# 🖥️ NanoKVM OS

### Your tiny KVM. More possibilities.

**Browser KVM, Internet over USB, remote access and a full Alpine system — on SG2002 NanoKVM hardware.**

[![Latest NanoKVM OS](https://img.shields.io/github/v/release/dormancygrace/NanoKVM-OS?filter=v*&label=NanoKVM%20OS&color=blue)](https://github.com/dormancygrace/NanoKVM-OS/releases/latest)
![Platform: SG2002 RISC-V](https://img.shields.io/badge/platform-SG2002%20RISC--V-6366f1)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-green)](LICENSE)
[![Sponsor](https://img.shields.io/badge/Sponsor-%E2%9D%A4-ea4aaa)](https://github.com/sponsors/dormancygrace)

[🚀 Install](#install) · [✨ Features](#features) · [📦 Releases](https://github.com/dormancygrace/NanoKVM-OS/releases) · [🔌 Hardware](#compatibility) · [🔄 Update](#updates) · [💻 Build](#build) · [🧪 Help test](#help-test) · [💡 Ideas & votes](https://github.com/dormancygrace/NanoKVM-OS/issues/406) · [🐛 Issues](https://github.com/dormancygrace/NanoKVM-OS/issues)

</div>

---

## ✨ What is NanoKVM OS?

**NanoKVM OS** is independent community firmware for the SG2002-based NanoKVM. Control a computer through HDMI and USB from a desktop or phone, mount installation media, reach a headless Linux console, or give the connected computer Internet through the same USB cable.

Underneath the interface is **Alpine Linux 3.24 on a writable F2FS root**, with ordinary APK packages, OpenRC services and the Alpine userspace, plus NanoKVM builds of OpenSSL and zlib-ng. You can install software, update individual components and retain your settings without rebuilding or reflashing the entire card.

See the [latest OS release](https://github.com/dormancygrace/NanoKVM-OS/releases/latest) for what’s new, or get the [latest SD image](https://dormancygrace.github.io/NanoKVM-OS/latest-image/) for a fresh installation. New SD images use the OS release version; package-only updates keep the previous image available. New release downloads contain the image (when included), corresponding sources and checksums. APKs and build metadata live in [technical package storage](firmware/alpine/README.md#public-downloads-and-technical-package-storage) in this repository; devices continue to update through the signed APK repository.

**💡 Help shape what comes next.** [Share your ideas and feature requests](https://github.com/dormancygrace/NanoKVM-OS/issues/406), browse community suggestions, and add a **👍 reaction** to the ones you would use. One idea per comment makes it easy for others to find it and vote.

<a id="features"></a>
<a id="comparison"></a>

## 🚀 What you can do

### 🎬 Video, from HD to 4K — and ultrawide

**More room for your desktop: 16:10 and ultrawide 21:9, alongside classic 16:9 and portrait modes, and 8:9 for a remote screen beside your other windows.** Choose the aspect ratio in **Settings → Video → Aspect ratio**, then pick the resolution. The full set of profiles, including 4K, QHD and portrait, needs an LT6911UXC or LT6911D receiver.

#### 🖥️ 16:9 — Classic widescreen

| Resolution | Maximum monitor refresh rate |
| --- | --- |
| **4K / UHD — 3840 × 2160** | **Up to 30 Hz** (H.265) |
| **QHD — 2560 × 1440** | **Up to 60 Hz** |
| **Full HD — 1920 × 1080** | **Up to 90 Hz** |
| **HD — 1280 × 720** | **Up to 120 Hz** |

#### 📐 16:10 — More vertical space

| Resolution | Maximum monitor refresh rate |
| --- | --- |
| **2560 × 1600** | **Up to 50 Hz** |
| **1920 × 1200** | **Up to 60 Hz** |

#### 🌄 21:9 — Ultrawide

| Resolution | Maximum monitor refresh rate |
| --- | --- |
| **3440 × 1440** | **Up to 40 Hz** (H.265) |
| **2560 × 1080** | **Up to 60 Hz** |

#### 🪟 8:9 — Half a QHD screen, or two of them stacked

| Resolution | Maximum monitor refresh rate |
| --- | --- |
| **2560 × 2880** | **30 Hz** (H.265) |
| **1280 × 1440** | **Up to 60 Hz** |

These are the highest advertised monitor timings on an LT6911UXC or LT6911D receiver, not guaranteed delivered video rates. LT6911C receivers offer 720p and 1080p at 60 Hz, plus experimental 1920 × 1200 (up to 50 Hz) and 2560 × 1080 (30 Hz); a PCIe board with an LT6911C keeps the receiver's stock EDID. Available modes also depend on video memory, source timing, codec and browser. The interface reports the input and encoded stream separately and explains unavailable choices. See [video modes and receiver limits](docs/VIDEO.md).

- **H.264 up to 2560 × 1600 and H.265 up to 4K, over Direct or WebRTC**, plus **MJPEG**. New browsers start with the device’s selected transport; existing browser preferences are retained, and viewers of different transports watch at the same time. RustDesk accepts WebRTC whenever its client offers it, whatever the device transport. Viewers share the device’s codec, picture settings and hardware budget.
- **Hardware video encoding**, a bitrate ceiling from 1 to 20 Mbit/s (20 by default), measured bitrate, and Direct playback options for responsiveness or a smoother picture. NormalP keyframes are requested as needed, with a periodic safety interval. Bitrate is an encoder ceiling; actual traffic depends on the image and operating conditions.
- **Adaptive MJPEG quality and JPEG colour sampling:** quality falls when all viewers lag and recovers up to your selected limit. Choose **4:2:2** (default) or **4:2:0**, with the selection saved across reboots. MJPEG and screenshots are full-range JPEG; with 4:2:0 or frame detection, MJPEG next to an H.264/H.265 stream is slightly pale. The monitor profiles let the PC send full-range RGB, so black stays black in every codec when the source follows them (NVIDIA does by default); a source set to limited range by hand still sends 16–235.
- **Virtual monitor / EDID controls:** choose the resolution, refresh and orientation seen by the managed computer independently from the stream-size limit. Resolution and frame rate have separate controls; the monitor timing follows the stream rate where the receiver can be updated live. On a Cube or Lite with an LT6911C or LT6911UXC, EDID changes require a physical power cycle, and the interface explains when it is needed; PCIe boards with an LT6911UXC or LT6911D, and Cube/Lite boards with an LT6911D, apply them at once. **Same as input** preserves the input dimensions.
- **Portrait profiles** on an LT6911UXC or LT6911D: 720 × 1280 and 1080 × 1920 with H.264 or H.265, 1440 × 2560 with H.265 (H.264 only with a stream resolution of 1080p or lower), subject to the reported device and browser capabilities.
- **PNG screenshots and browser-side recording**, plus USB audio playback in the browser through a shared Opus encoder. Direct and WebRTC record the H.264/H.265 stream into MP4 as received, without re-encoding; MJPEG records WebM. Recordings include the USB audio while it plays in the page. Direct/WebRTC preserve the received Opus packets when encoded receiver access is available (the shared device encoder targets 192 kbit/s); the browser encoding fallback also targets 192 kbit/s. A buffer limit or source interruption saves the captured portion with a warning. The single file is committed when recording finishes; a browser crash can still lose an unfinished recording. Recording availability depends on the browser’s recording and encoded-stream APIs.
- **Playback recovery:** Direct requests fresh keyframes after dropped frames; stalled capture has a bounded recovery path. H.264/H.265 streams carry BT.709 colour information.
- **HDMI enable/disable and capture idle timeout**, so capture can stop when no viewer is watching. While crash-screen notifications are on, the timeout leaves capture on, idle between the periodic checks of the screen for a crash. Viewers count as watching while a page is visible and keyboard or mouse input came from any page within 5 minutes. An unattended video stream still reserves the memory needed by OCR: high-confidence Windows stop screens are reported with a screenshot and without their text; console panics wait until the video stops.

H.265 requires a browser and platform with a compatible decoder. Direct playback uses WebCodecs in a secure context. The device does not transcode a shared H.265 stream separately for a browser that only supports H.264. The H.264 encoder corrupts 3840 × 2160 pictures, so 4K needs H.265; with H.264, choose a stream resolution of 1440p or lower. MJPEG and JPEG-based screenshots are unavailable with a 4K HDMI input; concurrent MJPEG and H.264/H.265 viewers share capture resources and can reduce delivered FPS.

### 📱 Control from a desktop or phone

- A movable, collapsible toolbar and responsive settings with reachable **Apply** controls.
- Relative mouse and absolute pointer modes, touchpad gestures, scrolling and IME input. **Hold one finger and tap with another to right-click.**
- **One-click keyboard sequences:** Ctrl+Alt+Del, Magic SysRq and REISUB, with timing handled by the device.
- Multiple viewers with server-enforced input ownership. Additional sessions start with control locked; control can be transferred explicitly, and the sole remaining viewer receives it automatically when the owner disconnects, unless that viewer chose view-only, its page is asleep in the background or a RustDesk client holds control.
- Controls follow the USB composition: disabled input functions are hidden, and the control lock is shown only when USB input is enabled.
- User accounts with administrator/user roles, [two-factor authentication](docs/TWO-FACTOR.md), administrator-only shared video settings and a **Logout** action.

Viewers share the device's encoder and video settings. Multiple sessions do not create independent Windows desktops or independent host mouse pointers.

### 🔌 One USB connection, several jobs

Configure the USB gadget through presets (KVM, with USB network, with the serial console, a headless server, remote desktop, keyboard and mouse only) or individual switches for the keyboard, relative mouse, absolute pointer, network, virtual storage, serial console and audio. The interface checks the controller's endpoint budget before applying a combination and says what each preset gives up. USB can also be disabled entirely. You can also configure the USB device identity (VID, PID and identifying strings), plus separate names for mounted images, the Setup Disk and User Disk.

| Function | What it provides |
| --- | --- |
| **Keyboard and pointers** | Browser control, with normal and HID compatibility profiles |
| **Virtual media** | SD-backed images or direct read-only ISO mounting from your computer, HTTP(S) or SMB2/3 without copying the ISO to the card; CD/DVD images up to 31.625 GiB. [Source requirements and lifetime](docs/STORAGE-TRANSFER-SAFETY.md#https-mounted-iso-reads) |
| **Network boot** | Boot the managed computer from the [netboot.xyz](docs/NETBOOT.md) menus over USB network, with the official iPXE images (UEFI Secure Boot signed one included) |
| **User disk** | A writable FAT32 USB drive backed by an image on the SD card, for BIOS update files, profiles and logs; selectable sizes from 64 MiB to 4 GiB; browse folders, upload/download files, upload whole folders (also by drag and drop) and manage contents while detached from the computer |
| **USB network** | A USB NCM network to the managed computer, for Internet sharing, network boot and computer sensors; the computer cannot open the NanoKVM's web interface or SSH over it unless [allowed](docs/USB-COMPOSITION.md#what-the-computer-reaches-over-the-usb-network) |
| **Internet over USB NCM** | Share the NanoKVM Ethernet/Wi-Fi uplink with the managed computer |
| **USB Serial / CDC ACM** | Reach a configured host serial console through the browser terminal |

**Internet over USB** has its own switch in **Settings → USB**, off by default. It provides IPv4 DHCP, DNS and NAT, with software flow offload for eligible TCP/UDP connections. Turning sharing off keeps the USB network and the computer's address. Existing VPN routing and firewall policies are respected; conflicting routes can leave sharing waiting for a usable uplink. See [USB Internet sharing](docs/USB-INTERNET-SHARING.md).

For a **headless Linux login**, enable USB Serial and set the login up on the managed host with the console helper from the USB settings page: a script that reads the host first, refuses when it cannot work safely, keeps backups and can undo; it also comes on a read-only helper disk for hosts without a network. This works after the host USB stack loads; it does not provide pre-USB BIOS/UEFI output. See [USB composition and serial setup](docs/USB-COMPOSITION.md#host-serial-console).

ATX power/reset controls are available on supported boards. **Wake-on-LAN** includes a saved list of named MAC addresses for frequently used machines.

### 🌐 Network and remote access

- **Ethernet and Wi-Fi controls**, hostname, DNS, gateway, IPv6 and mDNS settings. Ethernet shows live link status and the current address; static IPv4 accepts CIDR notation and an optional gateway for direct computer connections. IPv6 is configurable per interface, with settings retained across reboots.
- **2.4 / 5 GHz Wi-Fi** on compatible adapters: automatic scanning across both bands, grouped network entries, signal-strength icons, hidden-network setup and a preferred band. **5 GHz is preferred by default**, with fallback to 2.4 GHz. Connection details and a Disconnect action are available in the interface.
- **Saved Wi-Fi networks**: every network the device joins is remembered (up to 32). With **Connect automatically**, chosen when joining, the device joins any known network wherever it is, after a reboot or when moved. The Wi-Fi page lists them under **Saved networks** to join or forget; a saved network is joined again without its password.
- **Wi-Fi access point**: under **Network → Wi-Fi → Access point**, the device serves a Wi-Fi network of its own instead of joining one, until it is turned off.
- **Shared STUN/TURN settings** under Network for browser WebRTC video/audio and RustDesk: use the default or custom STUN servers, disable STUN, or configure an authenticated TURN relay.
- **Bluetooth controller switch** on supported AIC8800 boards, off by default. Enable it under Network; turning it on or off leaves Wi-Fi unaffected. This is a controller switch, not a peripheral-pairing interface.
- A single **VPN** menu entry for **WireGuard, OpenVPN 2, Tailscale and NetBird**, with installation state, version and connection/profile controls. OpenVPN is installed with the system; Tailscale and NetBird are optional APK installations.
- **WireGuard `.conf` profiles** with an explicit Route Allowed IPs option; **OpenVPN TUN and TAP `.ovpn` profiles**, including referenced certificate/key files in the same upload.
- **HTTPS from first boot** with a unique device certificate and HTTP redirect. Newly generated certificates use ECDSA P-256; existing certificates are preserved. A **custom certificate** (your own CA or a public one) can be uploaded in Settings → System → Security and takes effect for new connections without a restart; a custom pair that cannot be loaded never blocks start-up (the generated one stands in). **Let's Encrypt / ZeroSSL / Buypass / Google** certificates through **acme.sh**, installed from its latest release on demand: HTTP-01 on port 80 or DNS-01 with every DNS provider acme.sh ships, renewed from the server and served without a restart. The web server's HTTPS and HTTP ports accept at most 256 connections in total and 64 from one address (one IPv6 /64), closing the rest before any TLS work, and run at most 16 TLS handshakes at a time; local add-ons on the loopback address are not limited. SSH can be enabled from settings; administrators can [import public SSH keys](docs/SSH-KEYS.md) from a file or pasted text.

### 🧩 Software and optional features

**Software is a top-level settings section.** Its package manager searches repositories and installs, removes or updates packages using the same APK database as the terminal. Install tools such as `nano`, `htop`, `mc` or Python as needed.

**Daily package-index refresh** is optional and off by default. Enable it in Software → Packages and choose a time in the device timezone. It refreshes the available-package lists; it does **not** automatically install updates.

| Feature | Installation and capabilities |
| --- | --- |
| **[RustDesk](https://github.com/rustdesk/rustdesk)** | Part of NanoKVM OS, off until switched on in Services → RustDesk. Share the captured HDMI display and USB input using the official RustDesk servers or your own; temporary/permanent passwords, H.264/H.265, direct/relay TCP, and WebRTC whenever the client offers it. Based on the RustDesk 1.5 protocol. |
| **[Computer sensors](docs/HOST-SENSORS.md)** | Part of NanoKVM OS, off until a connection is chosen in Services → Computer sensors. Agents for Windows and Linux send the managed computer's temperatures, voltages, fan speeds and power over USB; the readings appear on that page. |
| **[Screen text and screen models](tpu/README.md)** | The screen model, part of NanoKVM OS, recognizes Windows stop screens and kernel panics for crash-screen notifications, which are off until enabled. OCR models for Latin, Cyrillic or Chinese text are downloaded on request in AI → Text recognition and read the remote screen (toolbar, MCP and PicoClaw). |
| **[PicoClaw](https://github.com/sipeed/picoclaw)** | Install the official latest RISC-V release from AI → PicoClaw, then configure the model, API key and endpoint there for remote-control or general-assistant use; remove it from the PicoClaw panel. |
| **[acme.sh](https://github.com/acmesh-official/acme.sh)** | Install on demand in Settings → System → Security, where its HTTPS certificates are configured. Issue and renew certificates through ACME using HTTP-01 or DNS-01 with supported DNS providers; renewed certificates take effect without restarting the service. |

RustDesk can forward USB audio. The capture runs at the frame rate a connected RustDesk client asks for (30 FPS unless set higher in the client), for every viewer while that client is connected. The integration does not include clipboard, file transfer, terminal, chat or ATX control. Its [packages and corresponding sources](https://github.com/dormancygrace/NanoKVM-OS/releases?q=nanokvm-rustdesk) are published independently of OS releases, as are the computer sensors and the TPU models: a new version reaches devices with the next package update, without waiting for a new NanoKVM OS version.

PicoClaw is downloaded from its upstream release, **not packaged as a NanoKVM APK**. An **MCP endpoint** with enable/disable and API-key controls is also available under **AI → MCP** for external AI clients.

### ⚙️ System controls and everyday tools

- **[Settings backup, migration and reset](docs/SETTINGS-RECOVERY.md)**: encrypted exports, a preview before restore, safe migration exclusions, and factory reset with independent **Keep users** and **Keep apps** options. Keep apps retains APK-installed software while managed app settings reset.


| Area | Controls and information |
| --- | --- |
| **Dashboard** | CPU load, RAM, SoC temperature, CPU frequency, storage, network, VPN and time-synchronization status |
| **Memory** | One reusable 144 MiB video region shared with Linux (every resolution up to 2160p, no reboot); ZRAM (LZ4) with automatic sizing; SD swap; a configurable Go runtime soft memory limit, defaulting to a quarter of the memory Linux keeps under 2160p capture |
| **CPU** | Frequency control, thermal protection and an explicit Apply at startup option; an incomplete overclocked startup triggers a 1000 MHz fallback on the next boot |
| **Diagnostics** | Service, USB, firewall and package-operation status, plus system logs in the GUI |
| **Terminal** | Browser shell and serial sessions, with shell command history |
| **Scripts** | Shell and Python scripts on the device, run on demand or once per boot, with startup status and bounded logs. Startup scripts survive application service restarts without running twice; Python is installed separately. See [Scripts](docs/SCRIPTS.md) |
| **Redfish and IPMI** | Host power on/off and reset through the ATX connection for data-centre tools (`fence_redfish`, Ansible, `ipmitool -I lanplus`, `fence_ipmilan`), each on its own switch, off by default, with one BMC account; IPMI shows its sessions and lockouts. See [Redfish and IPMI](docs/BMC.md) |
| **Notifications** | Host power, HDMI signal, sign-in security, host watchdog and crash-screen events, sent by email, Telegram, Slack, Microsoft Teams, Google Chat, Discord, ntfy, Gotify, Pushover, Matrix, webhook or syslog |
| **[LLDP, SNMP and UPS](docs/NETWORK-SERVICES.md)** | Services under Settings → Services, installed with the system and off until switched on: LLDP/CDP announcements with a neighbour list, a read-only SNMP v2c agent, and Network UPS Tools sharing a UPS attached to the NanoKVM. |
| **Scheduled tasks** | Scripts or shell commands on a schedule, from simple repeats to cron expressions. See [Scheduled tasks](docs/SCHEDULE.md) |
| **Event log** | Host power, HDMI signal, crash screens, sign-ins, button presses and image mounts, with who did what. See [Event log](docs/EVENTS.md) |
| **Appearance** | Menu layout/icons, colours, language, web title, bundled branding choices and custom logo/favicon uploads |
| **System** | OLED and ATX settings, date/time and shutdown; **Reboot** sits next to **Logout**, and the mouse jiggler is in USB settings |

Settings pages load on demand, and package operations check available memory before starting.

<a id="compatibility"></a>

## 🔌 Compatible devices

**Supports NanoKVM Cube, Lite and PCIe** — the SG2002 RISC-V NanoKVM family. The full image selects the board profile automatically.

| Device | HDMI receiver | ATX / OLED | Notes |
| --- | --- | --- | --- |
| ✅ **NanoKVM Cube** | LT6911C, LT6911UXC or LT6911D | ATX and OLED | Alpha and serial-production board profiles; thanks to [@gxcreator](https://github.com/dormancygrace/NanoKVM-OS/issues/2#issuecomment-5694454091) |
| ✅ **NanoKVM Lite** | LT6911C, LT6911UXC or LT6911D | No ATX; an added OLED is detected | Base profile; thanks to [@Korenchkin](https://github.com/dormancygrace/NanoKVM-OS/issues/215) |
| ✅ **NanoKVM PCIe** | LT6911UXC or LT6911D; an LT6911C keeps its stock EDID | ATX and OLED | Board profile and receiver detection; thanks to [@rockymtngeek](https://github.com/dormancygrace/NanoKVM-OS/issues/8#issuecomment-5825067894) |

**Incompatible hardware:** NanoKVM Pro and NanoKVM USB use different hardware platforms. This firmware cannot run on them; they are not untested variants of the supported models.

Receiver-specific features, including higher resolutions and programmable EDID, vary by board. See [board support](firmware/boards/README.md) and [video modes and receiver limits](docs/VIDEO.md).

<a id="help-test"></a>

## 🧪 Help wanted: test on Cube, Lite and PCIe

**Hardware testing is one of the most valuable ways to help this project.** Pick the features you use on your Cube, Lite or PCIe and [share a test report](https://github.com/dormancygrace/NanoKVM-OS/issues/new?template=hardware_compatibility.yml&title=Hardware%20test%20report%3A%20) — a successful test is worth reporting too. **You do not need to test every feature.** See [CONTRIBUTING.md](CONTRIBUTING.md#test-reports) for what to include.

**Thank you — each real-device report helps us fix regressions and make the compatibility table more useful.**

<a id="install"></a>

## 🚀 Install the latest SD image

The [latest SD image page](https://dormancygrace.github.io/NanoKVM-OS/latest-image/) always points to the newest published image. An application-only release does not replace it.

1. Open that page and download the image ZIP and `SHA256SUMS` from the same image release.
2. Verify the ZIP checksum, extract the `.img` and flash it to an SD card of at least **1 GB** (**2 GB or larger** recommended). Flashing replaces the card's existing installation and data.
3. Boot NanoKVM, allow automatic board selection and its restart to finish, then open `https://DEVICE-IP/`.
4. Sign in with **admin / admin** and set a new password when prompted. For the administrator, this also sets the Linux root password used by SSH. SSH is disabled by default.
5. Open **Settings → System → Updates** to install application and system-package updates published since the image was built.

The image uses a **64 MiB boot partition** and **768 MiB F2FS root**; first boot creates an exFAT data partition in the remaining space. See [installation details](docs/INSTALL.md).

SD images use the version of the OS release they contain. **About** shows the current OS version, and **System → Diagnostics** the image used for the initial installation; package updates preserve that installation history.

<a id="updates"></a>

## 🔄 Update an existing installation

**v2.0-a2 or newer:** use **Settings → System → Updates** or run as root:

```sh
apk update
apk upgrade
reboot
```

**Release channel:** Settings → System → Updates offers **Stable** (default)
and **Beta** (opt-in previews, which may have regressions). Switching selects
where future updates come from; it installs nothing by itself. After returning to
Stable, **Install stable version** restores the current stable firmware even
when its packages are older. Confirm the rollback; settings and data are kept,
and a kernel or bootloader change requires a reboot.
Existing installations stay on Stable. Packages released on their own
(RustDesk, the computer sensors, the TPU models) are available in both channels.

Updates retain settings, user data and independently installed packages. The core packages include **nanokvm** (backend), **nanokvm-web** (UI) and **nanokvm-release**, which also provides the old **nanokvm-app** name. Web-only updates need a browser refresh instead of a backend restart. Kernel and bootloader updates need a reboot to take effect. Application service updates use OpenRC automatically; no separate apply command is needed.

- **Local `.apk` installations:** file-pinned packages may require an `apk add` step before upgrading; see [update instructions](docs/UPDATES.md).
- **Signing:** core packages and both repository indexes are ECDSA-signed. `nanokvm-keys` installs the public keys and moves older repository lines to GitHub Pages. A device on a release before 3.3 first updates to 3.3.0 and boots it once with network access, to install the EC key and finish repository migrations; see [the transition instructions](docs/UPDATES.md#component-versions-and-support).
- **C906 overlay installations:** follow the [stock Alpine migration instructions](firmware/alpine/README.md#existing-c906-installations). Updating NanoKVM alone does not remove the overlay.
- **Stock firmware or pre-v2 releases:** use the [stock migration installer](docs/STOCK-INSTALL.md) or the full SD image. Legacy `.nkos` updates do not migrate the system to Alpine.
- **Coming from older versions:** see [other migration cases](docs/UPDATES.md#other-migration-cases).

The bootloader package updates fip.bin on existing installations; see [bootloader updates and recovery](docs/BOOT.md#updating-fipbin). A full image includes the rebuilt bootloader as well as the applications and system packages.

<a id="build"></a>

## 💻 Build from the public repository

The repository contains the application, browser UI, native integration, kernel/driver patches and complete image build. It fetches upstream sources at the revisions and hashes recorded in [`platform/sources.lock`](platform/sources.lock). CI publishes output hashes and compares changed components against the main build. Reproducing outputs requires matching build inputs; moving Alpine repositories and release-archive timestamps can still differ between builds. Separate private SDK checkouts are not required.

For a local test build, after installing the [host prerequisites](platform/README.md#build):

```sh
git clone https://github.com/dormancygrace/NanoKVM-OS.git
cd NanoKVM-OS
git checkout v4.2.1   # or the release tag you want
platform/build.sh -d
```

`-d` generates **local test signing keys**. Official release builds require the maintainer's ECDSA key; test-signed packages are not official updates. See [build and signing instructions](platform/README.md).

The platform uses **Linux 7.3.5**, **U-Boot 2026.10**, **OpenSBI 1.9** and a **GCC 16.2 / T-Head C906** toolchain built with **Buildroot 2026.08.2**. Kernel/modules use `-O3` without LTO; native userspace uses the reviewed scalar `-O2` profile. The server uses **Go 1.27.4**, and web builds use **Node.js 26 LTS**. Ordinary userspace comes from official Alpine repositories; NanoKVM packages provide **OpenSSL 3.5.9 LTS** with the C906 `-O3` profile, the zlib-compatible zlib-ng library, exfatprogs, i2c-tools, lolcat and BusyBox devmem.

Pinned platform inputs, Go modules and the web lockfile fix their respective source versions. Alpine 3.24 repositories continue receiving updates, so later builds can install newer Alpine packages; the build records them in `release/installed-packages.txt`.

| Source directory | Contents |
| --- | --- |
| `server/`, `web/` | Go application, streaming/API services and React/TypeScript interface |
| `addons/` | Packages released on their own (RustDesk, computer sensors, OCR and screen models), with their source and packaging |
| `support/` | Native media/board integration and device tools (`kvm_system`, MMF, `nanokvm_update_edid`) |
| `tpu/` | TPU runtime integration and the OCR and screen-crash programs |
| `kvmapp/` | Init scripts and PicoClaw agent files |
| `firmware/` | Alpine recipes, OpenRC integration, retained SDK inputs and board support |
| `platform/`, `scripts/` | Pinned platform build, patches, packaging and image generation |
| `tests/` | Host-side tests |

## 🌍 Translations

The language menu includes 25 languages, with partial translations falling back to English. Hebrew is available as a translation starter. Translations are maintained on [Hosted Weblate](https://hosted.weblate.org/engage/nanokvm-os/): anyone can fix or add strings in the browser, without git. See [TRANSLATING.md](TRANSLATING.md).

### 🌍 Made welcoming by our translators

Thank you to **[Pavel Borecki](https://github.com/p-bo)** for Czech, **[Alexander-Ger-Reich](https://github.com/Alexander-Ger-Reich)** for German and English UI maintenance, and **[polyzium](https://github.com/polyzium)** for Russian localization — and to everyone who translates, reviews, and improves the words on screen. See the [translation credits and contribution guide](TRANSLATING.md#translation-credits).

**[Hosted Weblate](https://hosted.weblate.org/engage/nanokvm-os/)** is our community translation platform. Thank you to the Weblate project for the tools and hosting that let people contribute directly from their browser. [Help translate NanoKVM OS](https://hosted.weblate.org/engage/nanokvm-os/).

## 💖 Support development

If NanoKVM OS makes your hardware more useful, consider [sponsoring Gleb Pesin on GitHub](https://github.com/sponsors/dormancygrace). Your support helps fund development time and hardware testing. One-time and monthly contributions of any amount are welcome.

Testing, bug reports, translations and code contributions are just as valuable; see [CONTRIBUTING.md](CONTRIBUTING.md). Thank you for helping improve NanoKVM OS.

### 💚 Community-funded hardware

**Thank you, [Pavel Borecki](https://github.com/p-bo), for generously funding a Sipeed NanoKVM Cube!**

## ❤️ Credits and licenses

Built on the work of [Sipeed](https://github.com/sipeed/NanoKVM), [SOPHGO](https://github.com/sophgo), [Milk-V](https://github.com/milkv-duo), Alpine, Linux, Buildroot, Go and [Pion](https://github.com/pion), with thanks to the contributors and hardware testers.

Special thanks to **[iPXE](https://github.com/ipxe/ipxe)** for the network bootloader and official boot images, including UEFI Secure Boot support, and to **[netboot.xyz](https://github.com/netbootxyz/netboot.xyz)** for the network boot menus, operating system installers and recovery tools that make our [Network boot feature](docs/NETBOOT.md) possible.

NanoKVM application changes retain the upstream [GPL-3.0 license](LICENSE). Other components retain their own licenses and notices. See [third-party notices](docs/THIRD-PARTY.md), [distribution status](docs/DISTRIBUTION.md) and the [corresponding-source build](platform/README.md#corresponding-source). Release downloads include the corresponding-source archive; RustDesk has its own source release.

When [reporting an issue](https://github.com/dormancygrace/NanoKVM-OS/issues), include the board/HDMI receiver, OS version, browser/client, codec and transport, resolution/FPS target and reproduction steps. Remove credentials and private screen contents from logs.

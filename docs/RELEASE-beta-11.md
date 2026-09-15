# 🛠️ NanoKVM OS v1.0.0 beta-11 — Bug Fixes & Everyday Improvements

This release focuses on OLED controls, Wake-on-LAN, scripts, virtual media and everyday usability. It also adds persistent scheduled jobs and optional Python with pip.

## 🐛 Fixes and reliability

- **OLED responds to rapid toggles:** quickly switching the display off and back on no longer loses the final request. Repeated switching was confirmed on the local PCIe device.
- **Wake-on-LAN uses eligible interfaces:** select Ethernet or Wi-Fi; USB gadget NCM stays excluded even after it has fully initialized.
- **Scripts run from the correct paths:** nested scripts remain distinct, paths are validated, symlink escapes are rejected and filenames are not interpreted as shell commands. Background jobs report startup failures and are reaped when finished.
- **HTTPS keeps your certificates:** turning HTTPS back on preserves the configured certificate and key pair.
- **Virtual media handles locked images:** force-eject a host-locked local CD/DVD image after confirmation. Mounting an image while USB storage is disabled now reports what needs to be enabled.
- **Static Ethernet parsing is more robust:** comments, blank lines and Windows line endings are handled consistently. Ethernet and Wi-Fi DHCP clients advertise the configured hostname.
- **USB network startup is consistent:** removed stale RNDIS-era service naming from the NCM boot path.
- **Capture debug logging is corrected:** formatting arguments are passed correctly to debug output.

## 🖥️ Interface and USB Serial

- **Simpler DNS settings:** DNS addresses appear directly below the mode selector. Network details are tucked into an expandable section below Save.
- **Login prompt on connect:** opening the USB Serial terminal sends one Enter to wake the host prompt. If a shell or program is already active, it receives that Enter.

## 🌐 Network and scheduled jobs

- **Route-aware TCP MSS clamping** for IPv4 and IPv6 helps connections work over lower-MTU VPN paths. Ordinary 1500-byte paths and already smaller MSS values are left unchanged.
- **Persistent crontabs** are stored under `/etc/kvm/cron`, so scheduled jobs survive reboots.

## 🐍 Optional Python and pip

**Python 3.14.7-r1** with **pip 26.2.1** is available from the signed package repository:

```sh
nkos-addons install python
. /etc/profile
```

Both **`python` / `python3`** and **`pip` / `pip3`** work. Python remains optional. The interpreter lives in rootfs; pip-installed libraries use `/data/python` by default. Console commands installed by pip are placed in `/data/python/bin`.

## 📦 Full SD image only

**Beta-11 is distributed as a complete SD image. No `.nkos` update package is published for this release.**

**Hardware scope: NanoKVM Enhanced PCIe/UXC only.** The shipped kernel Device Tree and boot hardware service declare `pcie`; this is not a Cube Full image. Do not flash it to a Cube Full.

Download **`NanoKVM-OS-v1.0.0-beta-11.img.zip`** and **`SHA256SUMS`**, verify the checksum, extract the `.img` and flash the SD card.

**Writing the full image replaces the existing installation, including settings and data.** Export anything you want to retain before flashing. This download does not perform the settings-preserving Web updater migration.

## 🤝 Thank you

Thanks to **Sipeed/NanoKVM, SOPHGO/CVITEK, Radxa, IronKVM, PiKVM and OneKVM**, and to the maintainers of **Linux, Buildroot, APK Tools, OpenSSL, Python, Go, Pion, Opus** and the many upstream projects behind NanoKVM OS.

Please include your board revision, firmware version and reproduction steps when reporting an issue. Feedback and testing are welcome! ❤️

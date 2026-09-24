# 🛠️ NanoKVM OS v2.0 b3 — Network Startup & NTP Fix

This maintenance release fixes network startup on devices without a Wi-Fi adapter, including Ethernet-only NanoKVM PCIe units.

## 🌐 Networking and time synchronization

- Fixed Wi-Fi initialization being attempted when `wlan0` is absent. This could leave the OpenRC network service stopped and prevent `chronyd` from starting, leaving the clock at the Unix epoch and Dashboard reporting that synchronization was not confirmed.
- Ethernet and Wi-Fi startup are now handled independently. Networking remains available when at least one enabled interface starts successfully.
- Wi-Fi module initialization is skipped when `wlan0` is already present, when no SDIO device is detected, or when Wi-Fi is disabled.
- **Wi-Fi remains supported on PCIe units equipped with an adapter.** Detection follows the actual hardware and interface availability, rather than disabling Wi-Fi for the entire PCIe profile.

## ⬆️ Updating from v2.0 a2, b1 or b2

**Update directly — no intermediate release or SD-card reflash is required.** Use **Package updates** in Settings, or run:

```sh
apk update
apk upgrade
```

Then reboot to verify network startup with the updated service. Settings, user data and independently installed packages are retained.


## 💾 Fresh installation

Use **`NanoKVM-OS-v2.0-b3.img.zip`** for a fresh installation and verify it with **`SHA256SUMS`**. Existing v2 installations can use the package update above.

**From beta-14 or an older beta, use the full image. Flashing replaces the installation and data.**

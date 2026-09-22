# 🚀 NanoKVM OS v2.0 b2 — Video Memory, Frame Rates & Interface Polish

This release adds a choice of video-memory allocation, consistent capture-rate limits and H.265 GOP selection, alongside diagnostics, SSH and interface improvements since v2.0 b1.

## 🧠 Video memory

- **CMA remains the default.** Choose **CMA** or **Fixed** under **Settings → System → Memory**. Both modes provide a 64 MiB video-memory region.
- CMA allows Linux to use idle pages in that region. Fixed reserves the whole region exclusively for video and reduces the RAM available to Linux by 64 MiB.
- The interface shows the active mode separately from the saved choice. **Reboot to apply a change.** The selection is retained when the kernel package is updated.

## 🎬 Video and streaming

- Capture limits now consistently follow the actual HDMI input and encoded resolution: **QHD up to 50 FPS, FHD up to 75 FPS, and HD up to 120 FPS**. Existing portrait-specific limits are retained.
- Downscaling a QHD source to FHD or HD no longer introduces the old 30 FPS cap. The saved FPS request is retained; the actual source timing and processing capacity still determine delivered frame rates.
- **H.265 GOP selection:** SmartP remains the default; NormalP is available to use less video memory. The selection applies after a device reboot.
- Stale encoder cleanup now runs before MMF producers are initialized, improving the initialization order after a service restart.
- WebRTC DTLS prefers ChaCha20-Poly1305 when supported, with AES-GCM fallback. This affects the DTLS connection; it does not switch media encryption to ChaCha20.

## 🛠️ Diagnostics and SSH

- New administrator diagnostics with clearer pending-update states and privacy filtering for exported reports.
- Selectable SSH banner styles and a restored coloured shell prompt.
- Temporary storage is limited to 64 MiB.

## 🎨 Interface

- Separate customization of the login logo and favicon, responsive branding cards, and a configurable primary button colour.
- Optional toolbar logout button and clearer logout confirmation.
- Updated About and Credits view, with automatic closing of the expanded credits.
- Consistent installation screens for optional VPN clients.
- Fixed the Dashboard shortcut to Memory settings.
- Fixed horizontal overflow in the settings sidebar. Scrollbars are thinner, with a transparent track and corner instead of a black strip and white square.

## ⬆️ Updating from v2.0 b1

**No SD-card reflash is required.** After publication, open **Settings → System → Updates → Package updates**, refresh the package list and install the available updates. Alternatively:

```sh
apk update
apk upgrade
```

Settings, user data and independently installed packages are retained. The application restarts after its package update. **Reboot after this update** to activate the new boot payload and video-memory selection support.

Linux remains **`7.2.6-nanokvm-os-r1`**. The kernel package adds the CMA/fixed boot variants; the kernel binary and matching modules are unchanged. Packages use the signing key already trusted by b1.

## 💾 Fresh installation

Download **`NanoKVM-OS-v2.0-b2.img.zip`** and **`SHA256SUMS`**, verify the checksum, extract the image and flash an SD card of at least **2 GB**.

Automatic board selection is retained for SG2002 NanoKVM Cube, PCIe/UXC and Lite. The image uses a **64 MiB boot partition**, a **768 MiB F2FS system partition**, and an exFAT data partition created from the remaining space on first boot. Fresh installations use **CMA** by default.

**From beta-14 or an older beta, use the full image. Flashing replaces the installation and data.**

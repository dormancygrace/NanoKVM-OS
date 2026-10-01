# NanoKVM OS v2.0-b6

### 🎥 MJPEG

- Fix delayed display of the latest JPEG in Chromium, including static screens.
- Remove an intermediate compressed-frame copy between the hardware JPEG encoder and the server.
- Apply JPEG quality changes between frames without recreating the encoder channel.
- Add **JPEG color sampling: 4:2:0 / 4:2:2** to Video settings. The choice persists across reboots; 4:2:2 is the default; an explicitly saved 4:2:0 choice is preserved.
- Keep MJPEG **4:2:2** alongside H.264/H.265 **4:2:0** using independent capture outputs.
- When concurrent video needs the wide output, limit MJPEG to **1920 pixels in width**, preserving aspect ratio. MJPEG returns to its selected resolution about two seconds after the video stream ends. Standalone MJPEG still supports QHD 4:2:2.
- Show the actual output dimensions for each viewer in Video settings and Dashboard, including an explanation when the MJPEG width limit is active. Frame detection and hardware fallback reporting remain supported.

### 📋 System logs in the browser

- Add a **Logs** tab under **System → Diagnostics**, with system, kernel and application sources.
- Search, highlight and filter matching lines; refresh manually or enable five-second automatic refresh.
- Keep bounded diagnostic archives for the **current boot and two previous boots**. Archives accumulate after installation.
- Enable DHCP client messages in the system log.

### ⚡ Interface and reliability

- Load settings pages, translations, the virtual keyboard and PicoClaw on demand to reduce the initial GUI download.
- Pause additional background polling while the browser tab is hidden.
- Correct ION memory parsing, including `100%` and missing data, and avoid shell processes for memory checks.
- Cache HDMI mode-file reads while detecting file changes.
- Preserve active failed-login lockouts when the tracking table fills.
- Bound terminal/WebRTC WebSocket messages and reduce per-connection signaling buffers.
- Allow local PicoClaw providers such as Ollama, LM Studio and vLLM without an API key.

### 🔧 Platform

- Backport MaixCDK file-lifetime and empty-version handling fixes.
- Add AIC8800 SDIO source compatibility for Linux 7.3 and correct repeatable application of the survey patch. **This release still uses Linux 7.2.6-nanokvm-os-r1.**


### 📦 Installation and updates

For a fresh installation, write **NanoKVM-OS-v2.0-b6.img.zip** to the SD card using an image-writing tool.

Devices running **v2.0-a2 or later** can update through the signed NanoKVM APK repository, preserving configuration. Run as root:

```sh
apk update
apk upgrade
reboot
```

Keep the NanoKVM repository enabled. The package manager installs the matching server, native libraries and required system components together.


### 📝 Notes

4:2:2 uses more raw-frame memory and may increase JPEG bandwidth.

### 🙏 Credits

Thanks to [@rockymtngeek](https://github.com/rockymtngeek) for testing on LT6911D PCIe hardware, providing detailed diagnostic logs, and confirming the capture fix.

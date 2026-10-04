# NanoKVM OS v2.1-b1

### System and updates

- Update Linux from **7.2.6 to 7.2.9**, with matching drivers and boot images for the supported boards and both CMA/fixed video-memory modes.
- Enable **nftables software flow offload** for established forwarded TCP/UDP connections. Local traffic, including video served by NanoKVM itself, does not use this forwarding fast path.
- Use official **Alpine Linux 3.24** packages by default, including OpenSSL. Keep the optional C906 package profile available for experiments.
- Build the complete SD image, application, native libraries, kernel, drivers and signed APK packages from the public repository with pinned upstream sources and recorded output checksums.
- Use a shared **C906 / T-Head + `-O2`** profile for NanoKVM-built native components, with the kernel and bootloader's required register restrictions. Fix the ISP errors exposed by optimization and preserve HDMI receiver wiring/detection patches in clean builds.
- Refresh MaixCDK, Pion DTLS/ICE/SRTP, inih, tinyalsa and relevant driver/firmware source pins. Keep json-c on the official stable **0.19** release. Web builds use **Node.js 24 LTS**.
- Run the application through OpenRC with the same startup policy used by its compatibility entry point. Keep networking and SSH independent of video initialization failures.

### Video and mobile controls

- Match portrait and landscape targets: **QHD up to 50 FPS, FHD up to 75 FPS, HD up to 120 FPS**. Advertise 50 Hz for the maximum 1440×2560 H.265 Direct portrait profile.
- Apply explicit codec changes across active viewers and preserve the selected codec after restarts. Fix returning stream resolution to **Same as input**.
- Disable **H.265 over WebRTC at every resolution** in both the interface and server. Use **H.265 Direct** or **H.264 WebRTC**. Hardware AES acceleration remains enabled.
- Reduce WebRTC allocations and batch UDP output.
- Fit video and settings to the mobile browser's changing viewport. Fix settings requiring a second tap, inaccessible Apply buttons, nested scrolling, flashing transient encoder errors and the floating toolbar's corners.
- Add the optional **Windows absolute-pointer profile** with USB/EDID display association.
- Support right-click by holding one finger and tapping with another; improve touch scrolling.
- Hide input controls when the corresponding USB gadget functions are disabled. Automatically grant control to the sole remaining viewer, and restrict shared video settings to administrators.

### Software and extensions

- Manage add-ons in **Software**. Install, update and remove **PicoClaw** using its official latest RISC-V release; show its navigation entry only while installed. Improve its mobile layout and contrast.
- Add optional **RustDesk 0.5.3**, using the RustDesk 1.5 protocol, with official or custom servers, temporary/permanent passwords, shared HDMI video and USB input. Direct/relay TCP is the default; WebRTC is optional.
- RustDesk follows the device's H.264/H.265 selection and can forward USB audio. Enabling remote access prepares the required USB input functions; audio prepares USB sound while preserving unrelated gadget settings.
- RustDesk exposes the HDMI capture, not the source computer's other monitors. Clipboard, file transfer, terminal, chat and ATX control are not included. Official-client audio playback still requires separate confirmation.

### Other fixes

- Correct React cleanup and state handling across audio, input, memory, software management and the browser terminal.
- Tighten image-download/resume/mount filename checks, numeric video validation, autostart and time-zone path handling.
- Reuse unchanged sanitized log snapshots to reduce repeated processing.

### Installation and updates

For a fresh installation, extract **NanoKVM-OS-v2.1-b1.img.zip** and write the `.img` to an SD card of at least **2 GB**. The root partition remains **768 MiB** and user data uses the remaining card space.

From **v2.0-a2 or newer**, use **Settings → System → Updates** (on a2: **Settings → Updates → Package updates**), or run as root after this release is published:

```sh
apk update
apk upgrade
reboot
```

If NanoKVM packages were installed from local `.apk` files, remove those local package pins first by selecting their repository versions:

```sh
apk update
apk add nanokvm-base nanokvm-app nanokvm-release nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002
apk upgrade
reboot
```

Settings, user data and independently installed packages are retained. The kernel and modules update together; reboot to start **7.2.9-nanokvm-os-r1**. Full images include the rebuilt bootloader; APK updates preserve the installed bootloader.

Existing installations using the experimental C906 overlay can follow the [stock migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/main/firmware/alpine/README.md#existing-c906-installations). Updating NanoKVM components alone does not replace that overlay.

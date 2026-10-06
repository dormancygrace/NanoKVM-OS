# NanoKVM OS v2.5-a1 · Image v1.0-a1 (Alpha)

**Alpha release.** Full SD images now have their own version: **Image v1.0-a1** ships with **applications v2.5-a1**. Updating applications later keeps the original image version and bundled application version visible in About.

Changes below cover everything since **v2.0-b7**, including the previously unpublished v2.1 work.

### System and updates

- Update Linux from **7.2.6 to 7.2.9**, with matching drivers and boot images for the supported boards and both CMA/fixed video-memory modes.
- Add an independent **Internet over USB** switch: share the NanoKVM Ethernet/Wi-Fi uplink with the managed computer through USB NCM. Disabled by default; IPv4 sharing includes DHCP, DNS and NAT. Turning sharing off keeps local USB access available.
- Use **nftables software flow offload** for eligible USB-forwarded TCP/UDP connections, with ordinary NAT fallback. Preserve VPN routing and suppress acceleration where other forwarding policies would conflict. Local video traffic does not use this forwarding fast path.
- Use official **Alpine Linux 3.24** packages by default, including OpenSSL. Keep the optional C906 package profile available for experiments.
- Build the complete SD image, application, native libraries, kernel, drivers and signed APK packages from the public repository with pinned upstream sources and recorded output checksums.
- Use **GCC 16.2 / T-Head C906**, with **`-O3` and no LTO for the kernel and modules**, and scalar **`-O2` for native userspace**. Correct vector-context handling and optimize the aligned LZ4 decode path. Fix the ISP errors exposed by optimization and preserve HDMI receiver wiring/detection patches in clean builds.
- Refresh MaixCDK, Pion DTLS/ICE/SRTP, inih, tinyalsa and relevant driver/firmware source pins. Keep json-c on the official stable **0.19** release. Web builds use **Node.js 24 LTS**.
- Run the application through OpenRC with the same startup policy used by its compatibility entry point. Keep networking and SSH independent of video initialization failures.

### Video and mobile controls

- Add **4K at up to 30 FPS** on supported capture hardware. Raise landscape targets to **QHD up to 60 FPS, FHD up to 100 FPS and HD up to 120 FPS**; portrait profiles use their supported mode limits. These are requested limits, not guaranteed frame rates for every source and codec.
- Add video profiles and apply monitor resolution, orientation and refresh changes as one EDID operation. Keep FPS following and inactive portrait preferences consistent.
- Raise video clock rates and add the UHD fixed-memory profile with automatic ZRAM sizing. Package the shared FIT template once instead of duplicating complete boot images for every board and memory mode.
- Apply explicit codec changes across active viewers and preserve the selected codec after restarts. Fix returning stream resolution to **Same as input**.
- Restore **H.265 WebRTC** with the reviewed CryptoDMA fix: separate DMA input/output buffers and a burst size of four. On an older loaded driver, use software SRTP until the updated module is loaded and the application is restarted.
- Reduce WebRTC allocations and batch UDP output. Keep networking, SSH and the watchdog independent of video startup failures.
- Fit video and settings to the mobile browser's changing viewport. Fix settings requiring a second tap, inaccessible Apply buttons, nested scrolling, flashing transient encoder errors and the floating toolbar's corners.
- Add the optional **Windows absolute-pointer profile** with USB/EDID display association.
- Support right-click by holding one finger and tapping with another; improve touch scrolling.
- Hide input controls when the corresponding USB gadget functions are disabled. Automatically grant control to the sole remaining viewer, and restrict shared video settings to administrators.

### Software and extensions

- Manage add-ons in **Software**. Install, update and remove **PicoClaw** using its official latest RISC-V release; show its navigation entry only while installed. Improve its mobile layout and contrast.
- Add optional **RustDesk 0.5.3**, using the RustDesk 1.5 protocol, with official or custom servers, temporary/permanent passwords, shared HDMI video and USB input. Direct/relay TCP is the default; WebRTC is optional.
- Reuse RustDesk frame-encryption buffers and reject video dimensions that exceed the protocol header range.
- RustDesk follows the device's H.264/H.265 selection and can forward USB audio. Enabling remote access prepares the required USB input functions; audio prepares USB sound while preserving unrelated gadget settings.
- RustDesk exposes the HDMI capture, not the source computer's other monitors. Clipboard, file transfer, terminal, chat and ATX control are not included. Official-client audio playback still requires separate confirmation.

### Other fixes

- Correct React cleanup and state handling across audio, input, memory, software management and the browser terminal.
- Tighten image-download/resume/mount filename checks, numeric video validation, autostart and time-zone path handling.
- Reuse unchanged sanitized log snapshots to reduce repeated processing.

### Installation and updates

For a fresh installation, extract **NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip** and write the `.img` to an SD card of at least **2 GB**. The root partition remains **768 MiB** and user data uses the remaining card space.

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

An application update does **not** require rewriting the SD card or changing the original image version. Settings, user data and independently installed packages are retained. The kernel and modules update together; reboot to start **7.2.9-nanokvm-os-r1**. Full images include the rebuilt bootloader; APK updates preserve the installed bootloader.

Existing installations using the experimental C906 overlay can follow the [stock migration instructions](https://github.com/dormancygrace/NanoKVM-OS/blob/main/firmware/alpine/README.md#existing-c906-installations). Updating NanoKVM components alone does not replace that overlay.

# ⚡ NanoKVM OS v3.2.0 — Unleash the Card. Lighten the Load.

**📦 Applications v3.2.0 · 💾 Image v1.4**

**A hidden storage bottleneck, removed. Faster unpacking, built for the C906. Updates you can actually see.** This release makes everyday system work faster while keeping normal upgrades simple: install the packages, reboot, and keep your settings.

## 💾 Nearly three times the SD read speed on the test bench

An inherited device-tree error assigned the SD controller's two clocks the wrong way around. Linux calculated its divider from the 300 MHz bus clock, while the card actually received the 100 MHz functional clock. The reported 50 MHz was really about 16.7 MHz — enough to explain the roughly 8 MB/s reads reported by the community.

The corrected clock assignment lets the SD card run at its requested 50 MHz. On the PCIe development device, repeated 256 MiB direct-read tests went from **8.0 MB/s to 23.3–23.8 MB/s**, and back to 8.0 MB/s when the old boot image was restored. No MMC errors were observed in those tests.

The same clock assignment is corrected for **Wi-Fi SDIO**. Association and traffic were checked, but the tested wireless link remained radio-limited: **a Wi-Fi throughput increase has not been demonstrated**.

This is a fix to the board boot images, delivered in **nanokvm-kernel-sg2002**. **Reboot after updating** to activate it. The Linux release string remains **7.2.10-nanokvm-os-r5**; the package version identifies this update. Results depend on the card and workload; the SD benchmark is not a measurement of complete OS installation time. Lite, D80 boards and other cards still need broader testing. [Details and measurements: #251](https://github.com/dormancygrace/NanoKVM-OS/pull/251).

Thank you **[@Korenchkin](https://github.com/Korenchkin)** for the NanoKVM Lite report and follow-up measurements in [#247](https://github.com/dormancygrace/NanoKVM-OS/issues/247), which helped uncover this bottleneck.

## 🏁 Faster boot from the SD card

**Image v1.4 also fixes the SD clock in U-Boot.** In PCIe bench tests, loading the 8.9 MB `boot.sd` took **0.37 seconds instead of 2.93 seconds** — about 2.6 seconds saved in that boot stage. The new bootloader was loaded into RAM, verified the kernel/ramdisk/device-tree hashes and successfully booted Linux; booting the new FIP directly from an SD card and other boards/cards remain untested. [Details: #252](https://github.com/dormancygrace/NanoKVM-OS/pull/252).

This bootloader improvement is included in **new Image v1.4 flashes only**. APK upgrades retain the existing U-Boot and still receive the Linux SD/SDIO fix described above.

## 🚀 Faster decompression, less waiting

**[zlib-ng](https://github.com/zlib-ng/zlib-ng)** replaces the system zlib through the new **nanokvm-zlib** package, built for the C906 and checked against Alpine's zlib API. On the development device, verification of the same Python APK took **1.23 s instead of 1.47 s** with the pinned build. This measures that verification operation, not a complete system upgrade.

The server also gains buffered native **gzip and xz** readers. PicoClaw and acme.sh archive extraction now uses the gzip reader. Earlier development benchmarks measured roughly **40 MB/s for buffered gzip** and **11 MB/s for xz**; those measurements used zlib-ng 2.3.3, before the final API-compatible development pin. This is groundwork for more compressed-image workflows. [System library: #220](https://github.com/dormancygrace/NanoKVM-OS/pull/220) · [Server readers: #223](https://github.com/dormancygrace/NanoKVM-OS/pull/223).

The system library is pinned to a reviewed zlib-ng development commit because released versions lack parts of Alpine zlib 1.3.2's API. Its updates now come through NanoKVM OS. **Alpine's exact-version zlib-dev package cannot be installed alongside this replacement.** Ordinary Alpine software, including OpenSSL, continues to use Alpine packages.

## 🔔 Know what is installed — and what is new

**Settings → System → Updates** now lists installed NanoKVM components, Alpine and the running kernel, plus the next-boot kernel when different.

- A small **daily NanoKVM update check**, enabled by default, notifies administrators when new component versions are available. It can be switched off on the Updates page.
- A notification and dots on the settings navigation make available updates visible. **Check now** runs a manual check.
- This check fetches only the NanoKVM repository index and **installs nothing**. The separate optional scheduled refresh of all package indexes keeps its own setting.
- APK indexes live in RAM after reboot, reducing repeated writes to the SD card.
- The Software page correctly recognises APK 3's update-list format instead of silently dropping available updates.

## 📦 A leaner, stable package foundation

The default repository list now uses **Alpine 3.24 stable main and community**, without edge. Existing edge-tagged installations migrate automatically. New Tailscale and NetBird installations use stable packages; an already-installed newer edge version is retained until stable catches up, rather than silently downgraded.

## 🧹 Everyday polish

- The active video-session counter has an explanatory tooltip and accessible name on its visible toolbar button, including keyboard focus.
- Remove the obsolete image builder and old /tmp compatibility links used by the previous layout.
- Clean up stale GitHub Actions caches automatically to keep development builds manageable.

## 🔄 Update or install

**Already running NanoKVM OS v2.0-a2 or newer?** Use **Settings → System → Updates**, or run as root:

```sh
apk update
apk upgrade
reboot
```

Settings, user data and separately installed packages are retained. **The reboot is essential for the SD/SDIO fix.** Existing local APK file pins may need the [migration step](https://github.com/dormancygrace/NanoKVM-OS/blob/v3.2.0/docs/UPDATES.md#packages-previously-installed-from-local-files). Experimental C906-overlay installations need their separate [stock Alpine migration](https://github.com/dormancygrace/NanoKVM-OS/blob/v3.2.0/firmware/alpine/README.md#existing-c906-installations).

**Fresh installation:** verify SHA256SUMS and flash **NanoKVM-OS-Image-v1.4-apps-v3.2.0.img.zip** to an SD card of at least **2 GB**. Flashing replaces the card's contents. The image has a 64 MiB boot partition and a 768 MiB F2FS root; first boot creates the data partition in the remaining space. It includes U-Boot 2026.10 and OpenSBI 1.9.

Application and image versions remain independent. An ordinary package upgrade preserves the installed image's origin; no reflash is needed to receive the SD clock fix.

**Cube, Lite and PCIe testers: please share your results!** Boot, video/control, SD reads and wireless stability on different boards and cards are especially useful for this release.

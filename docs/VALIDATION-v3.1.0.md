# v3.1.0 / Image v1.3 validation

Release source: 4d40cc1eba9a106925a662aa1e26191f65999997.
Validation date: 2026-10-16. The release notes retain the maintainer's approved wording.

## Build and artifact checks

- The complete platform build reproduces all 568 pinned outputs. Kernel, module and U-Boot CPU-profile audits pass.
- Go build, vet, module tests and focused storage/download/vm/network race tests pass.
- Production web build, lint and 212 web tests pass; all 54 script-check commands pass.
- Real APK split-migration tests preserve settings and file ownership, exercise a subsequent web-only upgrade, and reject an incompatible web API.
- AIC receive-path, command and timeout checks pass with ASan/UBSan.
- All nine core APKs are 3.1.0-r0, with root ownership and valid release signatures.
- The v3 package index and optional RustDesk package verify with the RSA and ECDSA public keys independently. The legacy index verifies with RSA.
- Image ZIP contents, MBR geometry, partition hashes, FAT/F2FS consistency, clean defaults and application/image versions pass.
- Both application manifests identify clean source at the release commit; corresponding-source archive hashes were checked.

## Physical device

The PCIe/UXC development device upgraded all nine core packages from v3.0-a1 using a local signed repository and rebooted successfully. The kernel is 7.2.10-nanokvm-os-r5. OpenRC services, Wi-Fi, SSH and the configured WireGuard interface returned normally.

Hashes of existing settings, root password, SSH identity and authorized keys matched before and after the update. The image-origin metadata correctly remained v1.1-a4 / v2.6-a4. USB gadget stayed disabled; Bluetooth remained disabled. The installed FIP hash equals the FIP in Image v1.3.

Chrome Main displayed v3.1.0. Settings opened on the first click. At the existing 1920x1080 / 100 Hz input, live UI observations were:

| Mode | Observed frames/s |
| --- | --- |
| H.264 Direct | 99–100 |
| H.265 Direct | 99–100 |
| H.264 WebRTC | 97–100 |
| MJPEG | 36–60 |

These are short functional observations over the current Wi-Fi connection, not sustained benchmarks or guarantees for other clients. The original H.264 Direct mode was restored. No kernel panic, oops, lockup or crypto timeout was observed during this run.

## Scope limits

The complete SD image was verified on the host, not freshly flashed in this round. The device check covers the package upgrade, boot components and runtime on the existing installation. It does not qualify clean-card first boot on every board, Cube/Lite, every resolution/browser, USB HID/media interaction, host internet sharing, or long-duration concurrent workloads. USB stayed disconnected as requested.

The separate RustDesk 0.5.3-r5 alpha add-on passed 59 host tests, a RISC-V target check and a C906 QEMU startup check; three device tests were excluded. No new end-to-end RustDesk client session was performed for r5.

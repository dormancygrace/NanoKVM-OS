# v2.5-a1 / Image v1.0-a1 validation

## Source and host build

- Fresh platform output, pinned upstream downloads; no reuse of previously compiled components.
- Full Go teststub suite; production web build, lint and 147 web tests passed.
- Packaging/service dependency checks and Linux namespace USB-sharing qualification passed.
- Effective kernel/module/bootloader compiler-profile audits passed. Kernel uses the exact committed config; NAT, flow table and nft flow offload are modules.
- Rebuilt kvm_system in a second output directory: shipped binaries compare identical.
- Signed optional RustDesk 0.5.3-r2 APK: signature, install/upgrade/removal, device executable precheck passed. Corresponding source is external and versioned.

## Output checksum audit

The previous expected.sha256 had not incorporated all integrated code/profile changes. Updated checksums cover the fresh integrated build, including:

- Kernel/vector-context/LZ4/config changes and matching modules/FITs. The previous experimental build additionally contained disabled vector-experiment Kconfig entries; these alter embedded config bytes and are absent from the release source.
- CryptoDMA out-of-place burst4 driver and loaded-driver capability marker.
- Native/system/audio components built with the reviewed compiler profiles. 76 differing outputs from the old manifest matched the preceding platform build byte for byte. kvm_system differs from that older source only in its debug-link CRC and reproduces exactly from current source.
- OpenSBI binary is unchanged from the qualified candidate; its manifest records the current build-script checksum.
- Current EDID profiles, web bundle and MSW worker; separate image/application About metadata; USB-sharing helper.
- Full FIT files kept as host build outputs are now included in the complete image-output manifest; APKs still carry the compact shared template.

## Hardware acceptance

Pending installation and validation of this exact package set. Earlier component experiments and namespace tests do not replace release hardware acceptance.

The full-image OpenSBI/FIP candidate previously passed UART RAM boot; persistent candidate SD cold boot is not yet qualified. APK installation preserves the existing FIP.

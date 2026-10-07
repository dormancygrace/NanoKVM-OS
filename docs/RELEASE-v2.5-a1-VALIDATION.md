# v2.5-a1 / Image v1.0-a1 validation

## Source and host build

- Fresh platform output, pinned upstream downloads; no reuse of previously compiled components.
- Full Go teststub suite; production web build, lint and 168 web tests passed.
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

## Signing/index integration follow-up

The release now includes nanokvm-keys, RSA-SHA256 package signatures and a
v3 Packages.adb index signed with RSA and ECDSA. The prior six-package output,
full image and source archive predate these changes and must be refreshed before
publication. Platform binaries need no rebuild for this packaging-only change.

The key/repository and stock-profile suites passed (16 tests), along with the
platform-build and OpenRC dependency suites. Restored executable modes on seven
imported build scripts. USB Internet helper/service and dnsmasq/iproute2 dependencies
were retained while resolving the package-manifest conflicts.

The existing private EC key was found and verified by comparing its derived
public DER with nkos-release-ec-b8e89b66.pub. No replacement key was generated. Device installation and reboot remain paused by
explicit user instruction.

## PR #64 integration

PR #64 was merged into main at dc42a323 and then into this release branch.
USB NCM Internet sharing, its dependencies and the independent image/application
version display were preserved. Its controls use the shared settings colours,
and USB-off confirmation includes the network/Internet-sharing effect.

The combined Go teststub suite and vet pass. The actual fresh production web
build passes all 168 tests and lint. The initial chunk test against the stale
checkout dist directory was discarded; the fresh build includes the expected
lazy chunks. The checked output manifest now contains 546 files. All outputs
outside server/ and web/ remain byte-for-byte unchanged from the preceding
qualified host build. No device was changed or rebooted during integration.

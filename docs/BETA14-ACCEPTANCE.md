# beta-14 preparation status

- Distribution: full SD image only.
- Image: `NanoKVM-OS-v1.0.0-beta-14.img.zip`, 130,290,543 bytes.
- SHA-256: `3d9da6def6fce101c34f05b4a3e312f335c9edaa26dc2109e82c927621af3302`.
- Firmware version/sequence: `1.0.0-beta.14` / `30`.
- Implementation commit: `8a72b747`; release notes committed separately.
- Kernel: `7.2.5-nanokvm-os-r4`; 79 matched loadable modules, dependency validation passed.
- Built-in: bridge, 802.1Q, SG2002 CPUFreq, SG2002 temperature, zram, FQ, FQ-CoDel.
- All five boot profiles use the newly built kernel. PCIe DT clock assignment: 500 MHz codec source, 360 MHz codec AXI.
- Fresh-image SSH default: disabled.

## Passed

- Kernel configuration gate, build and module dependency/vermagic checks.
- Full image FIT/kernel identity, embedded module inventory, FAT and ext4 checks.
- Fresh server, MMF, capture library, OLED service, web and EDID utility builds.
- Go common, service/vm and service/network tests (`CGO_ENABLED=0`, `teststub`).
- All 18 Direct playback tests after the production web build.
- Board-selection failure/pin-ownership fixtures and three monitor EDID tests.
- OLED window expiry, no-extension, manual-setting cancellation and maximal IPv4 formatting tests.
- Sophgo bypass-mux transitions and VPU/JPU clock lifecycle/failure tests against the release sources.

## Not yet accepted on hardware

The completed beta-14 image has not been installed on the local device. The device still reports beta-13 / kernel r3. Earlier individual-component testing does not constitute final-image acceptance. Cube and Lite physical validation is also pending.

OpenSSL 4 was retained without additional benchmarking. The QHD H.265 WebRTC guard remains enabled. Release assets: full image ZIP and SHA256SUMS.

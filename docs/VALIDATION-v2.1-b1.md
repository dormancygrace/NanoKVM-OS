# v2.1-b1 qualification — 2026-10-04

## Scope and provenance

Candidate prepared for release-text approval; not published. Application payload
source is clean commit `c00e82de571d5a97a98eb7bc29e1d67c6d2fbdd8`.
`ad5750a` implements the unconditional browser H.265 WebRTC block. The following
manifest commit records the final server/web outputs. Kernel/native output hashes
are unchanged by this final application rebuild.

The tested board is NanoKVM PCIe, SG2002/C906, LT6911UXC according to its earlier
inventory. Linux `7.2.9-nanokvm-os-r1`, Alpine 3.24.2. Cube/Lite/other receivers
were not physically tested in this run.

## Completed checks

- Production server and web build: passed. Final full Go suite with `teststub`:
  passed; 134 web tests passed. Changed frontend files passed scoped ESLint and
  formatting checks. `git diff --check` passed.
- The server test uses the signaling handler and verifies that H.265 is rejected
  before negotiation for both hardware/software AES settings, that the connection
  closes, and that default/explicit H.264 still receives ICE configuration.
  H.265 cannot create a media engine or a manager subscription.
- Frontend tests cover saved H.265 preferences, joining an active H.265 encoder,
  and a HEVC-capable browser. Direct support remains available.
- Full image assembly, output-manifest verification and ZIP CRC passed. Image
  geometry remains a 64 MiB boot partition and 768 MiB F2FS root partition; data
  occupies remaining card space after first boot. Raw image: 872415744 bytes.
  Rootfs package assembly: 187.4 MiB in 163 packages.
- Six core APK signatures and the separate optional RustDesk 0.5.3 APK signature
  verified with the existing release public key. No untrusted-install override
  was used on the device.
- Core packages were installed through APK and the device rebooted into 7.2.9.
  Initial post-upgrade comparison preserved 68 configuration hashes and the
  shadow-file hash. Network, SSH, board, modules and application services started.
- Final signed app APK was subsequently installed through `apk add`; APK replaced
  the same-version candidate and its post-commit mechanism restarted the app.
  The installed server SHA-256 matches the build:
  `790802687d2c2f049d2786d0b3f1414fbda32458729d999ad1296d3766798c7b`.
  Installed payload manifest reports the clean source commit above.
- Chrome Main: final H.264 WebRTC FHD video had readyState 4, native dimensions
  1920×1080 and advancing playback time. Dashboard observed 57–59 output FPS for
  60 requested. Runtime environment reports `NANOKVM_SRTP_AES=hardware`; the server
  has `/dev/sg2002-aes-probe` open. This does not measure the hardware fraction of
  every SRTP packet.
- Chrome Main: the H.265 option is disabled while WebRTC is selected. In Direct,
  H.265 becomes selectable. Applying it displayed the HDMI image on a 1920×1080
  canvas; Dashboard reported H.265 Direct, 60/60 FPS and 10 Mbit/s. A subsequent
  SSH check confirmed codec h265 and unchanged boot ID. These are short smoke
  tests, not an endurance qualification.
- `nf_flow_table`, `nf_flow_table_inet` and `nft_flow_offload` loaded successfully.
  Forward-flow policy was inspected after the kernel upgrade. No forwarding
  throughput claim is made; NanoKVM's locally served video does not use this path.
- Codec clocks remain VC source 500 MHz and codec AXI/H.264/H.265 360 MHz.

## CryptoDMA limitation

During earlier qualification of this candidate, FHD H.265 WebRTC SmartP with
hardware AES lost network/video progress and the device rebooted. The same class
of failure was already known on 7.2.6, especially QHD. Software AES comparisons
were short and did not establish a root cause. Hardware AES was restored and
remains the default. No kernel panic trace proves the underlying mechanism.

The release blocks browser H.265 WebRTC at all resolutions. This is a functional
restriction, **not a CryptoDMA fix**. H.265 Direct remains available. Prior
investigation also found a hang with H.265 Direct plus an independent AES load;
therefore this policy is not a guarantee against every concurrent CryptoDMA
consumer. See [Sophgo issue #9](https://github.com/sophgo/sophpi/issues/9).

## Limits

- The freshly assembled full SD image was structurally validated, not written to
  a fresh card and booted. On-device validation used component APK installation.
- Portrait target rates and mobile changes include earlier project work; the
  complete portrait/mobile matrix was not rerun on this final kernel/application.
- H.264 Direct and MJPEG had short checks earlier in candidate qualification;
  the final app smoke tests focused on H.264 WebRTC and H.265 Direct.
- No complete two-viewer endurance run or official RustDesk-client audio playback
  qualification was completed here. Optional RustDesk remains separate from the
  default image. Its package was signed and verified, not newly published.
- Component updates preserve the installed bootloader; only the full image
  contains the newly assembled bootloader. Existing image-version metadata may
  continue to identify the originally flashed v2.0-a1 while OS/app/kernel advance.

## Final artifact hashes

| Artifact | Bytes | SHA-256 |
|---|---:|---|
| `NanoKVM-OS-v2.1-b1.img.zip` | 140375400 | `4f4ccf7fcfaaed8926473195471f82778a2281ddb41f5c574d34925a42fb4268` |
| `nanokvm-app-2.1_beta1-r0.apk` | 14401421 | `f1d3af1522ef98cdab9cfcdaecf852ecee827cd3a06125ff49161ec842f4f655` |
| `nanokvm-base-2.1_beta1-r0.apk` | 3307771 | `93403521278ba9d498cfa963fdfc3199b6d971451bee298e85503292169353d4` |
| `nanokvm-firmware-sg2002-2.1_beta1-r0.apk` | 4405928 | `81cac36cbbf5d1f4cf4ab33d20eae0a100fbd30b3972e9a6ebd66d597232201f` |
| `nanokvm-kernel-sg2002-2.1_beta1-r0.apk` | 83448661 | `58990d7a0cc924fc952c3f7a0942b1fa8cef0642a2fa386d67bcee0a17c6c270` |
| `nanokvm-kmod-sg2002-2.1_beta1-r0.apk` | 4000120 | `837573a3d18878e739665c90b0b56e15e2b0733366e24aa104cc04ffd68734cd` |
| `nanokvm-release-2.1_beta1-r0.apk` | 1825 | `6785bcdf6fe6328ad087e827d1eb106a5e7a5c4d4ee53a63c46b4a124180ca67` |
| `nanokvm-rustdesk-0.5.3-r0.apk` | 1175963 | `b39830b2cc42b76417fc8557f9a0f376b7b5e5c6e79b95e5c0e579d35743ddb7` |

## Evidence location

Host evidence: `/home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/`:
`build-h265-block.log`, `packages-h265-block.log`, `final-go-tests.log`,
`h265-block-go-tests.log`, `h265-block-web-tests.log`, `final-app-install.log`,
`final-device-check.txt`, `final-direct-check.txt`, `final-apk-signatures.log`,
`final-artifacts.json`, plus earlier boot/configuration/AES comparison records.
Raw host logs may contain private device configuration and are not release assets.

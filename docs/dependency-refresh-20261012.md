# Dependency refresh — 2026-10-12

This change collects the applicable updates from the former upstream issue in one PR. Linux remains **7.2.9**: kernel sources, configuration, patches and module ABI are unchanged. No device was modified.

## Updated inputs

| Component | Before | After |
|---|---|---|
| U-Boot and host FIT tools | 2026.07 | 2026.10 |
| Go release toolchain/runtime | 1.27.1 | 1.27.2 |
| Patched Pion ICE | 4.4.5 | 4.4.7 |
| miniz | 3.1.2 / `77d0dce86277` | `2948eebafc5b` (3.1.2 plus upstream fixes) |
| Standalone BusyBox devmem | 1.36.1 | 1.38.0 |
| Build-host QEMU Ubuntu package | 1:8.2.2+ds-0ubuntu1.18 | 1:8.2.2+ds-0ubuntu1.19 |
| Build-host CMake | 4.4.0 | 4.4.4 |
| Build-host Python | 3.14.7 | 3.14.8 |
| RustDesk hbb_common protocol reference | `229b90450836` | `94ec710894ec` |
| Radix Scroll Area | 1.2.18 | 1.3.0 |
| i18next / react-i18next | 26.4.2 / 17.0.15 | 26.4.3 / 17.0.16 |
| Lucide React | 1.49.0 | 1.55.0 |
| React Simple Keyboard | 3.8.278 | 3.8.280 |
| Node 24 types | 24.19.1 | 24.19.2 |
| TypeScript ESLint, parser and plugin | 8.71.0 | 8.71.1 |
| Vite / React plugin | 8.3.2 / 6.1.1 | 8.3.4 / 6.1.2 |
| ESLint | 10.11.0 | 10.12.0 |
| MSW | 3.0.1 | 3.0.3 |
| PostCSS / Prettier | 8.5.28 / 3.9.9 | 8.5.29 / 3.9.10 |

CORS and the AES Bridge test-fixture migration were already integrated in [PR #93](https://github.com/dormancygrace/NanoKVM-OS/pull/93) before this combined follow-up was requested.

## Compatibility work

- U-Boot is pinned to the official `v2026.10` Git tag archive, SHA-256 `f943a88389c8fd70a6b0d061a88267465e143c7ca6ea2b75837ab20f5b60c739`, commit `5508406582f6f7120dce0c4b3059819a41788db2`. The FTP release URL returned 404 during this refresh. The SD board-limit patch was rebased around upstream's new `no-1-8-v` handling, preserving that handling and our 25 MHz limit. Other local boot patches remain intact. Buildroot's boardless host/target-tool configuration explicitly sets `CONFIG_ENV_FLAGS_LIST_STATIC` to U-Boot's empty-string default; 2026.10 otherwise fails to compile `envtools`.
- Go's pinned runtime/proc.go and all five patched std-vendored crypto files have exactly the same hashes in 1.27.2. The runtime preparation still checks them before applying the C906 crypto/sysmon changes. Both binary and source archive checksums are updated.
- ICE upstream changes preserve listener-created UDP mux connections during initial agent construction and specify the configured relay address family for TURN. The four changed files were merged against the previous upstream base; local PMTU and TURN metadata support and upstream regression tests are retained.
- miniz is pinned by commit and Git tree ID in the platform lock and the companion native-source manifest. Both BIN and ISP_BIN are rebuilt against it for link compatibility.
- QEMU extraction is invalidated by its pinned package checksum, so an existing build directory cannot silently keep the previous emulator. Cold extraction, unchanged-cache reuse and replacement of a stale cached executable were checked.
- MSW's checked-in service worker is regenerated from the selected package; it matches its runtime rather than retaining the previous generated file.
- hbb_common's upstream diff changes only standalone Cargo patches. Its protobuf files are unchanged; no protocol code or Cargo dependency is substituted merely to update reference metadata.

## Reviewed rows that are not upgrades

Buildroot stays on stable 2026.08 with the existing platform patches and the new focused host-tool patch. The audited reference was Buildroot master `a2142664e94e87a2aa90c70ebe14a932b4e357bc`.

- Binutils **2.47** and initramfs BusyBox **1.38.0** are already selected. Recipe differences do not mean an older installed source version.
- The platform's patchelf **0.19.1** is newer than Buildroot master's **0.13**; it is not downgraded.
- e2fsprogs **1.47.4** and zstd **1.5.7** are unchanged upstream. New recipe options (host libarchive integration / pzstd) do not update the sources and are not needed for the current image builder.
- pnpm **12.8.1** is ahead of the executable package's reported latest channel; it is not downgraded.
- Node remains **24.21.0**. Its types move to the latest 24.x release, not the incompatible 26.x major advertised by registry latest.
- Stock Alpine packages and PicoClaw use their existing signed repository/latest-download channels. The availability rows were not proof that installed device packages were old; no on-device package upgrade is performed here.

## Source selection and remaining release checks

- BusyBox 1.38.0 is labelled **unstable** by upstream. This PR updates only the
  separately built `devmem` applet; it does not replace Alpine's `/bin/busybox`.
  The initramfs already selected 1.38.0 before this PR. The standalone build
  passes, but its use by Sophgo `CVI_SYS_Init` still needs device qualification
  as part of the next image release.
- U-Boot uses the official `v2026.10` tag archive from GitHub because the DENX
  2026.10 release archive was unavailable (404, rechecked 2026-10-12). The pinned
  SHA-256 checks the downloaded bytes; it is not an upstream signature. This is
  an explicit source-authentication limitation, not a claim that the tarball
  was verified against a signed release.
- miniz is an upstream snapshot after 3.1.2, not a newer tagged release. Both
  commit and tree IDs are pinned. The changes include ZIP write-error and EOCD
  scan-underflow fixes; sanitizer tests and RISC-V BIN/ISP_BIN links passed.
- The matching BusyBox 1.38.0 archive is included by `platform/build.sh source`
  and must be published with the release. The checked-in 1.36.1 archive remains
  historical corresponding source for the v2.0-b4 devmem executable. It is not
  used by the new builder.

## Automated report removal

The public dashboard issue was deleted on request. The watcher no longer contains the issue publication function or `--publish` option; HTTP helpers support reads only and the workflow has no `issues: write` permission. Dependabot and read-only checks remain. The existing scheduled workflow was disabled before deleting the issue so old main cannot recreate it. Re-enable `upstream-watch.yml` **after this PR is merged**, when main contains the read-only implementation.

## Verification and release boundary

Completed locally:

- Web build, lint and all 184 frontend tests; the Node types follow-up also passes build/lint.
- Patched Go 1.27.2 server build and race tests for auth, utilities, middleware, router, all stream services and RustDesk.
- Full Pion ICE module tests, including upstream and local tests.
- Production RISC-V NanoKVM-Server cross-build with the prepared runtime, plus runtime smoke-test binaries. Disassembly confirms all 202 XTheadVector instructions in the linked ChaCha20 kernel match the source.
- Vendored ChaCha20, ChaCha20-Poly1305 and Poly1305 upstream/local test overlays pass under the updated riscv64 QEMU. C906-only vector cases skip when the emulator does not expose XTheadVector; this is not a device performance test.
- U-Boot 2026.10 RISC-V build with all local patches; standalone devmem builds with exactly one applet.
- miniz upstream tests with AddressSanitizer/UndefinedBehaviorSanitizer; RISC-V BIN/ISP_BIN static/shared library builds.
- All numbered Buildroot patches apply to a fresh pinned source archive; platform/config/CPU/light-link/dependency-check tests pass.

The clean Buildroot host CMake 4.4.4, Python 3.14.8 and U-Boot 2026.10 tools build passed, including their host dependencies. Version checks and FIT creation/listing/payload-extraction round-trip passed. Upstream source and license hashes matched.

No new image is claimed to be device-qualified. The existing `platform/expected.sha256` remains the previous qualified-output baseline; a full platform rebuild, boot qualification and new output hashes are still required before releasing an image from these inputs. The kernel exclusion also applies during that qualification.

# RustDesk add-on integration handoff

Worktree: /home/dgrace/nanokvm-astra/rustdesk-addon-20261003
Branch: codex/rustdesk-addon-20261003
Base: c818067d92217a6ed3265733e17191838e4f610c

This work implements an optional RustDesk protocol daemon, local NanoKVM
HDMI/HID bridge, Alpine APK lifecycle, admin API and standalone Add-ons card.
The daemon is derived from the open OneKVM RustDesk extension at
7a278b5897716786dcc075a167b72f04d5b73766, with source and dependencies bundled.
The proprietary OneKVM NanoKVM release core is not copied.

## Integration boundaries

Cherry-pick the commits from this branch into the owner's active source tree.
Only one line mounts the new router in router.go. The app APK advertises
nanokvm-rustdesk-bridge=1; release a newly versioned app package with that
capability before making the optional package available. The add-on is absent
from platform/packages.list, so existing images do not install it by default.

The web component is intentionally not mounted in this base worktree, because
the owner is changing Software into Add-ons / Packages concurrently. Apply
rustdesk-addons-integration.patch to the owner's current addons.tsx after
integration, or add its import and JSX manually if that page has moved.
The patch is based on the current owner tree and changes only those two lines.

The GUI install/upgrade/remove actions invoke fixed APK arguments. Config and
status routes require an admin session. Configuration can enable public ID
servers or a custom ID/relay server and key, password, codec and viewer limit.
Source can be downloaded from the installed admin card. There is no hidden
package installation, device access, signing, publication or app deployment.

## Validation completed

- Rust: 21 tests pass; two live-server/device tests are explicitly ignored.
  The protocol test checks rejected login and ongoing video during a slow HID
  response. New tests cover absolute wheel transport, bounded bridge errors
  and separate relay viewers with bounded duplicate connection candidates.
- Go: the complete go test -tags teststub ./... suite passes. The new bridge,
  HID and WebSocket ownership packages also pass the race detector.
- Web: TypeScript and Vite production build pass. The standalone component is
  typechecked; no browser/device UI test has been performed.
- The daemon cross-build produces a static RISC-V RV64GC binary. QEMU runs its
  config check successfully and confirms refusal of an empty password.
- The exported corresponding source passes its full Rust test suite offline.
- APK mkpkg metadata and root:root payload ownership were inspected.
  An isolated chroot test with mock OpenRC verifies the dependency guard,
  stopped installation, restart of a previously running service on upgrade,
  removal and preserved synthetic configuration. No real service runs in it.

A production CGO build of the main app could not link against the selected
existing native library set. That set lacks kvmv_read_video_sink and
kvmv_read_mjpeg_sink expected by this base and has unresolved OpenMP dependencies.
This is a native-bundle mismatch, not a successful production app build.
The integration owner must build against its current qualified native bundle.
The inherited build-server-existing-libs.py also expects older per-component
SDK libraries, unlike the available set using libvpu.so; it was not modified.

## Artifacts and build

scripts/build-rustdesk-addon.py builds an unsigned local APK and vendored source
archive. scripts/test-rustdesk-apk.py runs isolated lifecycle tests with a static
host BusyBox and host apk-tools 3, requiring root solely for chroot execution.
The APK contains its source archive and AGPL LICENSE/NOTICE. Build/install details
and runtime limitations are documented in addons/rustdesk/README.md.

The APK is a test artifact. It is not signed or published. Installing it on an
older app deliberately fails because the bridge capability is absent.

## Coordinated live test

The device 192.168.4.128 is owned by the main runtime task. This work has not
connected to it or changed its services, USB composition or video settings.

1. Coordinate a test window with the runtime owner. Preserve 1440x2560 portrait,
   Direct H.265 and Absolute Pointer Windows-only.
2. Integrate the bridge and web card; build the updated app with its current
   native bundle and deploy using the existing release workflow.
3. Install the optional package; verify it stays stopped until configured.
4. Choose an agreed ID/relay server and a unique password. Confirm actual ID
   registration; connect a current client explicitly advertising H.265 support.
5. Verify live HDMI, modifiers/key releases, absolute position, clicks, wheel,
   browser video, explicit browser takeover and disconnect cleanup.
6. Check OpenRC stop/start and APK upgrade/removal/reinstallation with the same
   ID and settings. Check reboot persistence only in an approved test window.
7. Exercise H.264 separately within the current hardware resolution limits.

Hardware codec output, HID and Windows pointer behavior, live registration and
official client compatibility remain unqualified until that test is completed.

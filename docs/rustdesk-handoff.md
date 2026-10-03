# RustDesk add-on integration handoff

Current accepted version: **nanokvm-rustdesk 0.5.3-r0**, based on RustDesk **1.5.0**,
qualified with **nanokvm-app 2.0_beta8-r25** on 2026-10-04.
The user confirmed working screen control. See
[rustdesk-0.5.3-finalization.md](rustdesk-0.5.3-finalization.md) for frozen digests,
validation, display association and the state left after testing.

## Integrated source and package boundaries

Accepted source/recipe commit: `a2bd8b710a9f11c006140262abcaa2eb3936e251`.
The common branch `codex/screen-role-permissions-20261003` integrated it at
`7160a0c26c7e5511837bfacbf98107bb3df8a426`; do not reapply the historical UI patch.
Finalization changes documentation only and does not rebuild the accepted APK.

The optional package is absent from the base image package list. Its app
capabilities are bridge=1, webrtc=1, audio=1, auto-codec=1 and usb-defaults=1;
install a compatible app first. Software > Add-ons owns installation, available
upgrades and removal. Settings > Extensions owns installed add-on management.
Both package and RustDesk reference versions are shown. Config/status changes
require an admin session; permanent passwords are excluded from status.

The endpoint uses the existing HDMI encoder and USB input/audio services.
Enabled remote access prepares keyboard and both mice, and Transmit sound
prepares USB audio. This preserves unrelated USB functions and the selected
pointer profile. Input errors revoke that session's control while video/audio
continue. Direct TCP/relay are supported and WebRTC is optional, disabled by default.

## Publication and release state

[0.5.3-r0 corresponding source](https://github.com/dormancygrace/NanoKVM-OS-packages/releases/tag/nanokvm-rustdesk-0.5.3-r0)
contains the locked/vendor source and external packaging/lifecycle files.
The APK contains LICENSE, NOTICE, upstream.json and a public source URL/digest;
it does not contain source archives or vendored dependencies.
The source release is finalized independently of the unsigned local APK.
No signed APK or package repository index was published by this test task.
Signing and repository distribution use the normal release workflow.

Do not overwrite accepted source assets or change installed source.json to a
new archive digest under the same package revision. Documentation corrections
and final acceptance are published separately in the finalization note.

## Qualification and cleanup

Rust 57 tests passed, 3 hardware tests remain opt-in; strict Clippy passed.
Real Windows/Android sessions, on-device HID responses and user screen-control
acceptance confirm the repaired input path. Audio PCM/Opus qualification and
historical transport measurements have separate evidence and limits.

The generic absolute Windows pointer maps to the primary monitor. User control
worked while the captured NanoKVM display was temporarily primary; the Acer
primary display was restored afterwards. Only one HDMI display is advertised.
OS monitor enumeration/switching, clipboard, files, terminal, chat and ATX
remain outside this version.

The runtime-owner task was notified of acceptance and cleanup. RustDesk,
remote test windows, diagnostic proxy and all test loads are stopped; USB audio
and current USB input functions remain enabled. Password, identity, configuration,
native libraries and performance settings were preserved. Do not restart tests
or services as part of finalization while the user is listening to music.

Historical evidence:

- [Initial 0.1 device test](rustdesk-device-test.md).
- [RustDesk 1.5 migration and transport qualification](rustdesk-1.5-migration.md).
- [USB audio path and remaining client checks](rustdesk-usb-audio.md).
- [USB preparation and input isolation](rustdesk-usb-defaults.md).

# NanoKVM RustDesk 0.5.3-r0 finalization

Finalized on 2026-10-04 after the user confirmed working screen control.
This records the accepted build; it introduces no runtime changes or new tests.

## Frozen versions and artifacts

- Add-on: `nanokvm-rustdesk 0.5.3-r0`.
- RustDesk protocol reference: `1.5.0`, commit
  `fada664df7a294d1d1a9ca3e7cd3637069122f17`.
- Qualified application: `nanokvm-app 2.0_beta8-r25`.
- Accepted source/recipe commit: `a2bd8b710a9f11c006140262abcaa2eb3936e251`.
- Integrated accepted code: `7160a0c26c7e5511837bfacbf98107bb3df8a426`.

[Corresponding source and packaging files](https://github.com/dormancygrace/NanoKVM-OS-packages/releases/tag/nanokvm-rustdesk-0.5.3-r0)
are published separately from the device. The source archive and existing
release assets remain immutable. This finalization note supersedes historical
qualification and USB behavior statements in the archived README; the accepted
code and installed source metadata are unchanged.

| Artifact | SHA-256 |
| --- | --- |
| `nanokvm-rustdesk-0.5.3-source.tar.gz` | `c13342660bbbef0857befe75a346399c47b5f46707684baa297deb3de2b9ad25` |
| Local test `nanokvm-rustdesk-0.5.3-r0.apk` | `8a16b620790e22a4cfc0d92ed6d11ff759fb4dc090831e85b10f0528efd88374` |

The APK is an unsigned local device-test artifact. This source release does not
publish a signed APK or change the Alpine repository index. The installed APK
contains a public source URL/digest, LICENSE and NOTICE, not source archives.

## Accepted behavior

The device shares its selected/active H.264 or H.265 encoder with RustDesk.
H.265 1920x1080 video and first-attempt session startup were confirmed in the
final test. TCP/direct/relay remain available; WebRTC stays optional and off
by default. Transport and audio do not require another video codec on the SoC.

Enabled remote access prepares the USB keyboard and both mouse interfaces.
Transmit sound prepares optional USB audio and defaults to enabled. Existing
unrelated USB functions and the selected pointer profile are preserved; USB
factory defaults are unchanged. Stopping RustDesk leaves shared USB functions
available. Input refusal disables that session's input while video/audio continue.

The r25 bridge executes HID writes in the HTTP handler with bounded nonblocking
USB writes. The daemon keeps its HTTP write half open until the response and
sends bodyless prepare/heartbeat/close requests. Headers and any HID report body
are written together. These fixes remove request-context cancellation and the
race with an immediate control response that previously revoked input.

## Validation and practical limits

- Both HTTP regressions failed before their respective fixes and passed after.
  Rust: 57 passed, 0 failed, 3 opt-in hardware tests ignored. Strict Clippy and
  formatting passed. No runtime source changed during finalization.
- On-device HID routes returned HTTP 204. One hundred bodyless heartbeat requests
  succeeded. Windows and Android sessions retained input permission.
- A real Android trace contained 512 absolute mouse reports, including button
  press and release; HID responses were HTTP 204 in 3-9 ms.
- The user confirmed working screen control with the captured NanoKVM display
  temporarily selected as the Windows primary monitor.
- Earlier USB audio qualification decoded 593 real Opus packets to the expected
  stereo tones and verified mute/unmute. Audible official-client playback and
  WebRTC audio remain separate, unconfirmed acceptance checks; see
  [USB audio evidence](rustdesk-usb-audio.md).

Windows maps a generic absolute mouse to its primary display. The tested source
PC had a separate Acer primary display and a secondary NanoKVM HDMI capture;
click acceptance required making the captured display primary. This is a display
association limit, not proof that every multi-monitor arrangement works. See
[Microsoft's absolute mouse coordinate documentation](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-rawmouse).

The endpoint advertises one HDMI capture. It cannot enumerate the source OS's
monitors or switch their capture through USB HID alone. A host helper and actual
additional capture/selection path are future work, not part of 0.5.3. The normal
RustDesk clipboard, files, terminal, chat and ATX features are also outside scope.
Earlier transport measurements are retained in
[RustDesk 1.5 migration evidence](rustdesk-1.5-migration.md); they are not new
performance claims for this accepted build.

## State left after acceptance

The Acer display was restored as the Windows primary monitor. RustDesk runtime,
remote client windows, diagnostic proxy and test loads were stopped. The original
control socket was restored. USB keyboard, both mouse interfaces and USB audio
remain enabled, using the Default pointer variant. The existing configuration,
password, identity, native libraries and performance settings were preserved.
No agent-generated device backups or source archives were retained.

Finalization did not reconnect to the device, restart services or introduce load.
The manually stopped runtime is a cleanup state, not a change to saved access
preferences. Resume development or additional tests only in a later work session.

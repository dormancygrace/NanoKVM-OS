# RustDesk optional USB audio

Package candidate: nanokvm-rustdesk 0.4.0-r0. RustDesk protocol reference remains
1.5.0 (fada664df7a294d1d1a9ca3e7cd3637069122f17).

The existing USB speaker function supplies 48 kHz stereo PCM to the app's
shared native helper; one Opus encoder serves browser and RustDesk listeners.
The add-on sends canonical AudioFormat (Misc tag 8), AudioFrame (Message tag 11)
and Audio permission (PermissionInfo enum 3); disable_audio uses Option tag 7.
It forwards the original encoded packets without decode/re-encode. Transport
selection and video codec are independent of audio. Sound is optional and
never enables USB HID or changes USB composition.

IPC is root-only (0700 directory, 0600 socket, SO_PEERCRED uid 0), at most eight
connections, 256-byte requests and 1275-byte packets. Client mute closes only
its capture subscription; the last listener stops the shared helper. App USB
rebind closes old listeners before changing functions. Queries while muted do
not open ALSA. Rust keeps one latest frame and discards frames older than 100 ms
at the network writer. Video never waits for PCM availability.

The add-on requires nanokvm-rustdesk-audio=1 in addition to bridge=1/webrtc=1.
The corresponding updated app must be installed first. The source archive is
published separately; it is not installed on the device.

Host acceptance covers canonical 1.5 wire tags, fragmented/bounded IPC parsing,
shared subscription lifetime, optional source absence, mute/unmute/rebind,
latest-frame delivery, server denial, authenticated video/audio coexistence,
format-before-frame and complete cleanup. A separate package test rejects
pre-audio runtimes and checks upgrade/removal preserve user state.

Actual USB PCM and audible desktop-client playback require hardware
qualification; host mocks alone do not establish these outcomes.

## Qualification on 2026-10-03

Installed normal app 2.0_beta8-r18 and add-on 0.4.0-r0 on the own lab device,
using the r17-qualified native19 bundle. Config and identity hashes were
preserved. No source archives or backups were placed on the device.

A temporary audio-only USB function exposed Speakers (AC Interface) to Windows.
Targeted waveOut playback sent 440 Hz left and 880 Hz right without changing
the default audio output or enabling HID. A headless authenticated RustDesk
client over SSH-protected loopback TCP received 868 H.265 video messages and
593 Opus packets in 15 seconds. During mute it received 180 video messages and
zero audio packets; unmute supplied a new AudioFormat and resumed sound. All
593 packets decoded with libopus 1.6.1 to 960 samples per channel, with the
expected independent 440/880 Hz tones. This establishes the actual USB PCM,
shared encoder, app IPC, RustDesk message and decoder path. It does not claim
desktop-client audible playback or remote WebRTC audio qualification.

The public ID server direct-request probe timed out before session setup.
That attempt is not an audio failure or a successful direct/WebRTC qualification.
The already implemented common audio session path applies to TCP, relay and
DTLS data-channel transports; a laptop client remains the desktop acceptance
step. USB Off, original RustDesk codec/config and the running service were
restored after every probe. The test did not alter the shared video settings.

Published source: https://github.com/dormancygrace/NanoKVM-OS-packages/releases/tag/nanokvm-rustdesk-0.4.0-r0
Source SHA-256: 6e99b196d69bd81c5e8eca7a346bc2d467992bad91b52b0b10a7ae9e9a8982b1

For a laptop viewer: enable USB audio on NanoKVM, select Speakers (AC Interface)
on the HDMI/USB source computer, play sound, and connect from RustDesk 1.5.0
on the laptop using the add-on ID/current password. Verify sound and mute while
video continues. For WebRTC, allow it on the endpoint and controller and reconnect;
inspect the session transport, since connection success alone may be relay.

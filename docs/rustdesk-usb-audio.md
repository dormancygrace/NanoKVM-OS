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

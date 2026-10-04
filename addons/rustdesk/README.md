# NanoKVM RustDesk add-on

Optional HDMI/USB HID endpoint derived from
[onekvm/onekvm-extension-rustdesk](https://github.com/onekvm/onekvm-extension-rustdesk)
at 7a278b5897716786dcc075a167b72f04d5b73766 (AGPL-3.0-only).
It implements RustDesk password authentication, ID registration and relay
encryption. It obtains encoded HDMI frames and submits USB input to the existing
NanoKVM app. The original OneKVM firmware is not used.

## Architecture and limits

The daemon uses four root-only Unix sockets under /run/nanokvm-rustdesk.
A JSON version-1 subscription is followed by 40-byte OKVF headers and Annex B
H.264/H.265 frames; codec zero carries a bounded error message. The app shares
its existing encoder. At each login the add-on automatically adopts the shared
selected/active codec (H.265 by default when idle without a selection). Old add-on
codec preferences are ignored. Reconnect after changing the device codec; a client
without support receives a clear error. Device bitrate, GOP and
frame rate remain in the existing Screen settings. No additional codec runs on
the SoC. Maximum 1440x2560 portrait output uses H.265 and requires client support.

Files, clipboard, chat and ATX are not implemented. RustDesk 1.5
sessions attempt encrypted direct TCP by ID using LAN address exchange and NAT
punching, then fall back to encrypted relay. IPv6-only direct-by-ID currently
uses relay. The explicit IP listener remains loopback by default.

WebRTC is an optional setting, disabled by default. It uses ICE/DTLS/SCTP through
the existing Go/Pion stack without decode/re-encode. SDP and ICE use authenticated
encrypted hbbs TCP; the signed RustDesk identity binds the local DTLS fingerprint.
Relay remains available when the ID server cannot route signaling. Relay-only
ICE requests use the TCP relay because no TURN configuration is supplied. The
first data channel must be ordered and fully reliable. Configure the same custom
ID server and public key in the client; see docs/rustdesk-1.5-migration.md for
device qualification and transport limits.

Only one session owns input. An existing browser controller blocks external
input. Browser joins stay view-only while RustDesk owns control; explicit
takeover drains held keys/buttons before new browser input. The heartbeat is
two seconds and expiry ten seconds. Input refusal disables input for that session
while video/audio continue. Absolute scrolling uses the absolute HID interface
and the app's selected pointer translation. Enabled remote access prepares USB
keyboard and both mouse interfaces through the existing validated composition;
unrelated functions and the selected pointer profile are preserved.

## Package and settings

nanokvm-rustdesk is an optional riscv64 package, absent from the base image list.
It depends on bridge, WebRTC, audio, auto-codec and USB-defaults capability version
1 supplied by the updated nanokvm-app, plus OpenRC. Upgrade the application before
this package. Installation alone leaves it stopped. Upgrade restarts only a service
that was running; removal stops it and removes its runlevel entry.

Software > Add-ons contains package installation, removal and upgrade when an
upgrade candidate is available, plus a link to management. Settings > Extensions
lists installed RustDesk and PicoClaw,
including stopped services. RustDesk has one management screen with its ID,
temporary password, service switch and password mode. Server and viewer
settings are under Advanced settings. PicoClaw opens its existing control panel.
English and Russian labels are provided. The old two-line UI patch is historical;
do not apply it again to the integrated branch.

The private /etc/nanokvm-rustdesk directory contains config.json, the stable ID,
UUID and signing key. User state survives package removal and reinstallation.
Configuration writes are atomic with mode 0600; the status API always omits the permanent password.
Fresh installs default to temporary passwords. The daemon generates ten easy-to-read
characters from OS randomness on each start and after a successful new login.
An authenticated peer may reconnect with its previous credential for thirty
seconds since its last received message, using the same peer ID, name and
nonzero session ID. The cache is bounded to 32 peers; wrong identities and new
sessions require the current password. Ten nonempty wrong attempts rotate it. Only the admin status API exposes
this password while the service runs; its runtime file has mode 0600 and is removed
on stop. The "New password" action restarts RustDesk and disconnects its sessions.
Existing configurations retain permanent mode on upgrade. Switching to temporary
mode preserves the permanent credential, but accepts only the temporary password.
An empty GUI field in permanent mode preserves the stored password; permanent
passwords need 8-64 UTF-8 bytes. The daemon publishes settings-output.json for the ID and a runtime status
file showing sessions and recent registration acknowledgements.

Example config.json (automatic temporary password):
{
  "service_enabled": true,
  "use_official_id_server": true,
  "rendezvous_server": "",
  "relay_server": "",
  "server_key": "",
  "password_mode": "temporary",
  "codec": "auto",
  "max_clients": 1
}

CLI validation: nanokvm-rustdesk --check --config /etc/nanokvm-rustdesk/config.json.
Use the normal apk and rc-service/rc-update commands for manual lifecycle.
NANOKVM_RUSTDESK_DATA overrides the identity directory for isolated tests.

## Build and source

Cargo.lock pins the Rust dependencies; no native codec or crypto library is
required. Host tests run with cargo test --locked. Exported archives include
vendor/ and .cargo/config.toml and support --locked --offline builds and tests.

For an unsigned local test package from the repository root, with cargo,
fakeroot, a riscv64 musl GCC and apk-tools 3 mkpkg available:
fakeroot python3 scripts/build-rustdesk-addon.py --linker /path/to/riscv64-linux-gcc --apk /path/to/host-apk --output work/rustdesk-artifacts --source-url https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/nanokvm-rustdesk-0.5.3-r0/nanokvm-rustdesk-0.5.3-source.tar.gz

The exported source can also cross-build directly without repository scripts:
CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_LINKER=/path/to/riscv64-linux-gcc RUSTFLAGS='-C target-feature=+crt-static' cargo build --locked --offline --release --target riscv64gc-unknown-linux-musl

Install the Rust riscv64gc-unknown-linux-musl target first. Keep the linker
symlink name intact for Buildroot wrappers. The builder produces a static RV64GC
binary without vector instructions and root:root payload ownership. It exports
adapted source, Cargo.lock, vendored dependencies and packaging recipes as a
separate source archive published in NanoKVM-OS-packages. The APK installs only
LICENSE, NOTICE, upstream.json and source.json (public URL and SHA-256), alongside
the binary and lifecycle files. It never installs the source archive or vendored
crates. The installed web link opens the source for the exact package revision.
The builder performs no signing, publication or installation.

For Alpine repository builds, place the exported source archive next to
firmware/alpine/packages/nanokvm-rustdesk/APKBUILD, generate abuild checksums
and build on riscv64 Alpine. Signing and repository publication belong to the
normal release workflow after device qualification. GUI installation requires
a configured repository containing the package.

## Qualification

Host tests cover the protocol password challenge, rejected logins, continued
video during a slow HID reply, input mapping, crypto/framing and persistent
identity. Go tests with hardware stubs validate application logic only.
Live HDMI, USB, client compatibility and registration require coordinated
testing on the real device before production use.

The accepted 0.5.3-r0 build, evidence and current limitations are recorded in
[finalization](../../docs/rustdesk-0.5.3-finalization.md). The integration handoff
is in [rustdesk-handoff.md](../../docs/rustdesk-handoff.md).

## Versions and upstream reference

The add-on has its own Cargo/APK version. `upstream.json` records the audited
RustDesk 1.5.0 protocol reference and exact source commits; it is embedded in
the daemon and installed alongside it so the UI follows the installed package.
This partial endpoint is derived through OneKVM, not a full RustDesk fork.
`nanokvm-rustdesk --version` prints both versions without starting the service.
The network version remains the reference version, not the newest client release.

TCP/relay advertises KX v1, which derives independent transmit/receive keys with
the upstream BLAKE2b transcript. Legacy v0 controllers remain supported; an
unoffered version fails closed. WebRTC carries the same protobuf messages over
DTLS, advertises KX v0 in the signed identity, and does not add secretbox framing.
The IPC bridge and daemon bound message sizes, fragment reassembly, ICE queues,
pending handshakes and session teardown. View-only ControlPermissions cannot
submit HID, acquire an input lease or send release events.
Host verification is distinct from device/client qualification; see
docs/rustdesk-1.5-migration.md for the current evidence and remaining limits.

Client OptionMessage.disable_keyboard is honored at login and during a session.
Switching to view-only discards queued movement, releases held input and ends
its input lease; remote keyboard options cannot override server-side permissions.

## Default transport policy (0.3.0-r2)

WebRTC is disabled by default, including upgrades whose configuration has no
webrtc_enabled field. Enable it explicitly through the installed add-on settings
(or set webrtc_enabled to true). Classic TCP rendezvous now attempts encrypted
direct TCP and local-address connection before relay. A direct listener is
created only for a bounded ID-server request; the permanent direct-IP listener
keeps its existing loopback default. Authentication, signed identity, KX v1,
view-only and viewer limits apply to these direct sessions.

The official 1.5 client does not try TCP in a round that contains a WebRTC offer.
For TCP connections by ID, disable WebRTC in that client too. Requests forcing
relay, or declaring symmetric NAT, retain relay fallback. Unsupported KCP/UDP
requests can use the client's TCP leg when no WebRTC offer is present.

## Optional USB audio

Sound from the connected computer uses the existing optional USB speaker
function. Transmit sound prepares USB audio while remote access is enabled;
choose the NanoKVM USB audio output on that computer. No keyboard/mouse
function is required for sound.

The app shares one existing Opus capture/encoder with browser listeners:
48 kHz, stereo, 20 ms packets. RustDesk forwards the encoded packets unchanged
inside the same authenticated encrypted TCP, relay or optional WebRTC session.
The normal RustDesk mute option detaches its audio subscription; video continues.
Unmute and USB rebind reconnect to the current capture. There is no microphone
or bidirectional voice-call support. No extra codec process runs per viewer.
A slow connection keeps only recent sound instead of accumulating a backlog.

The fourth root-only socket, audio.sock, uses bounded OKAF v1 headers. Requests
for info query the USB function without starting ALSA capture. The bridge and
Rust daemon bound packet sizes to 1275 bytes and release subscriptions on
mute, disconnect or shutdown. Rendezvous audio permission uses the canonical
1.5 two-bit permission slot and cannot be overridden by a client option.
See docs/rustdesk-usb-audio.md for qualification evidence and limits.

## Remote access USB defaults

The enabled daemon asks the private app bridge to prepare USB before accepting
connections, including on boot and service restart. Enabling remote access also
prepares USB keyboard and both absolute/relative mouse. The app uses its existing
validated, serialized composition and preserves other functions and pointer
profile. Endpoint budget failures are reported; unrelated functions are not
silently removed. Disabling RustDesk does not disable shared USB functions.

Transmit sound defaults to enabled for new and legacy configs; audio_enabled=false
disables RustDesk sound and client permission while leaving shared USB audio
available to other viewers. Enabling it prepares USB audio automatically. The
source computer must select the NanoKVM speaker output. Keyboard/mouse errors
disable that session's input without ending video/audio; reconnect after restoring
USB input. Individual HID requests time out after three seconds.

When the USB profile exposes only the relative mouse, the application bridge
converts Android absolute positions into screen pixel deltas, preserves small
movements and sends large deltas as multiple USB reports. Its first position
anchors the session. The normal two-mouse profile still uses absolute input.
Gadget writes use bounded nonblocking syscalls; a missing readiness event must
not hold input indefinitely. Login announces the negotiated keyboard permission
on every connection. The application waits up to two seconds for the first
usable encoder frame without changing HDMI power intent.

The HID HTTP client keeps its write half open until the server response. The
Content-Length header delimits each report; sending an early EOF would cancel
Go net/http request contexts and disable input while video continues.

HID preparation, heartbeat and close requests send no body: the Go bridge answers
these routes immediately, so a separate unused JSON body write could race its
connection close and disable input with `Broken pipe`. HTTP headers and any report
body are sent together.

## Display association

This endpoint advertises one HDMI capture. A generic absolute USB mouse maps to
the Windows primary display, so a secondary HDMI capture needs correct display
association. In the accepted two-monitor test, control worked after making the
captured NanoKVM display primary. OS monitor enumeration and capture switching
require a host helper and additional capture/selection support; they are not
implemented in this version.

## C906 performance build

Use cargo --config performance.toml build --locked --offline --profile performance --target riscv64gc-unknown-linux-musl with the existing cross-linker environment. The named profile selects optimization level 3, full LTO and one codegen unit; the config enables the qualified scalar T-Head extensions and static CRT. The generic release remains available. The repository packaging builder accepts --build-profile performance and records exact flags and compiler identity in build-profile.json. Use a separate output directory for each profile. This Rust backend does not implement RVV 0.7.1; this is a scalar optimized variant.

Local unpublished APK/source pairs can be built with --local-only instead of --source-url; source.json records the local archive digest and publication state. This avoids claiming a public download exists before publication.

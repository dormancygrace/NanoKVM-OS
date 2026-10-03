# NanoKVM RustDesk add-on

Optional HDMI/USB HID endpoint derived from
[onekvm/onekvm-extension-rustdesk](https://github.com/onekvm/onekvm-extension-rustdesk)
at 7a278b5897716786dcc075a167b72f04d5b73766 (AGPL-3.0-only).
It implements RustDesk password authentication, ID registration and relay
encryption. It obtains encoded HDMI frames and submits USB input to the existing
NanoKVM app. The original OneKVM firmware is not used.

## Architecture and limits

The daemon uses two root-only Unix sockets under /run/nanokvm-rustdesk.
A JSON version-1 subscription is followed by 40-byte OKVF headers and Annex B
H.264/H.265 frames; codec zero carries a bounded error message. The app shares
its existing encoder. Codec conflicts fail visibly, and device bitrate, GOP and
frame rate remain in the existing Screen settings. No additional codec runs on
the SoC. Maximum 1440x2560 portrait output uses H.265 and requires client support.

Audio, files, clipboard, chat and ATX are not implemented. The inherited
rendezvous implementation falls back to relay instead of completing direct NAT
traversal. The direct TCP listener defaults to loopback: inherited direct
sessions lack transport encryption. The GUI offers ID/relay access only.
Do not expose port 21118 remotely. Configure the same custom ID server and
public key in the client. Public registration and encrypted relay video were confirmed on the test device;
see docs/rustdesk-device-test.md for limits.

Only one session owns input. An existing browser controller blocks external
input. Browser joins stay view-only while RustDesk owns control; explicit
takeover drains held keys/buttons before new browser input. The heartbeat is
two seconds and expiry ten seconds. Input refusal closes the RustDesk session.
Absolute scrolling uses the absolute HID interface and the app's existing
Windows pointer translation. The bridge never changes USB composition.

## Package and settings

nanokvm-rustdesk is an optional riscv64 package, absent from the base image list.
It depends on nanokvm-rustdesk-bridge=1 supplied by the updated nanokvm-app and
on OpenRC. Installation alone leaves it stopped. Upgrade restarts only a service
that was running; removal stops it and removes its runlevel entry.

Software > Add-ons contains package installation, upgrade and removal, plus a
link to management. Settings > Extensions lists installed RustDesk and PicoClaw,
including stopped services. RustDesk has one management screen with its ID,
temporary password, service switch and password mode. Server, codec and viewer
settings are under Advanced settings. PicoClaw opens its existing control panel.
English and Russian labels are provided. The old two-line UI patch is historical;
do not apply it again to the integrated branch.

The private /etc/nanokvm-rustdesk directory contains config.json, the stable ID,
UUID and signing key. User state survives package removal and reinstallation.
Configuration writes are atomic with mode 0600; the status API always omits the permanent password.
Fresh installs default to temporary passwords. The daemon generates ten easy-to-read
characters from OS randomness on each start. Only the admin status API exposes
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
  "codec": "h265",
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
fakeroot python3 scripts/build-rustdesk-addon.py --linker /path/to/riscv64-linux-gcc --apk /path/to/host-apk --output work/rustdesk-artifacts --source-url https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/nanokvm-rustdesk-0.2.1-r1/nanokvm-rustdesk-0.2.1-source.tar.gz

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

The device test plan is in docs/rustdesk-handoff.md.

## Versions and upstream reference

The add-on has its own Cargo/APK version. `upstream.json` records the audited
RustDesk 1.4.9 protocol reference and exact source commits; it is embedded in
the daemon and installed alongside it so the UI follows the installed package.
This partial endpoint is derived through OneKVM, not a full RustDesk fork.
`nanokvm-rustdesk --version` prints both versions without starting the service.
The network version remains the reference version, not the newest client release.

RustDesk 1.5.0's selected KX version is decoded and rejected unless it is the
offered KX v0. KX v1 and WebRTC are not implemented. Wire fixtures generated
from the pinned 1.5.0 schema and encrypted loopback tests cover this behavior.
The repository's `docs/rustdesk-1.5-review.md` describes the transport/crypto
changes and the proposed porting order.

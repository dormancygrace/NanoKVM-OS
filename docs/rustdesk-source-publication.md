# Published RustDesk source instead of a device archive

nanokvm-rustdesk 0.2.1-r1 is a packaging revision of 0.2.1-r0. The daemon
binary and RustDesk protocol base (1.4.9) remain the same. The APK no longer
owns /usr/share/nanokvm-rustdesk/source.tar.gz, so a normal APK upgrade removes
the old archive. LICENSE and NOTICE stay installed with upstream.json.

The complete Rust source, Cargo.lock, vendored crates, offline Cargo settings,
wire test fixtures and packaging recipes are published as the versioned
nanokvm-rustdesk-0.2.1-source.tar.gz release asset in NanoKVM-OS-packages:
https://github.com/dormancygrace/NanoKVM-OS-packages/releases/tag/nanokvm-rustdesk-0.2.1-r1

The builder requires --source-url and writes source.json containing URL,
SHA-256 and the full APK version. Package revisions are read from APKBUILD,
which must agree with the Cargo source version. Both web views read source_url
from the installed package's status and open that public HTTPS URL. The
previous protected /api/addons/rustdesk/source endpoint redirects to it; it
no longer reads a device archive. Status polling does not contact GitHub.

Before device installation, verify the public release asset digest against
source.json. Publish the source before distributing the package. Keep each
release revision immutable. Publishing these source assets does not modify
the repository's older addon ABI, signed package index or other packages.

Validation includes a real isolated APK r0-to-r1 upgrade: removal of the old
archive, source metadata, running service lifecycle and retained configuration.
The exported source is tested with cargo test --locked --offline. Go tests
exercise source metadata and HTTPS URL validation; web changes pass the usual
TypeScript/Vite build and scoped lint/format checks.

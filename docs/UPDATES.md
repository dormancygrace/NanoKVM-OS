# NanoKVM OS updates

Beta-3 is distributed as a complete SD image. It establishes the system updater
used by subsequent signed `.nkos` releases. Install that image before using a
system package; the beta-1/beta-2 application updater cannot install system files.
See [installation instructions](INSTALL.md) for the full-image procedure.

## What packages update

A system package contains the application, web interface, and selected system
programs, libraries and data, including dependencies assembled by the maintainer.
Existing supported `/etc` configuration files are preserved. Explicit signed
removals retire obsolete program files and service scripts. Nothing is downloaded
or executed as an unsigned post-install script.

The kernel, kernel modules, boot partition, libc/loader, account databases,
network credentials, and the fixed recovery dispatcher are excluded. Updating the
kernel in a package is technically possible, but requires a separate boot-slot
and recovery design. The current SD layout has one FAT boot partition and one
`boot.sd`; this updater does not claim recovery from an unbootable kernel.

Legacy format-1 application packages remain supported when their native-library
fingerprint matches. They update only the application and web interface, without
a system reboot. System packages require the beta-3 foundation identifier and
matching native libraries; incompatible packages are rejected before installation.

## Installation and recovery

The device checks `dormancygrace/NanoKVM-OS` releases once a day. Checking is
automatic; installation requires an administrator action. System packages use the
asset name `NanoKVM-OS-update.nkos`; legacy `NanoKVM-OS-application.nkos` assets are
also recognized. Manual file upload uses the same verification path.

The independent, statically linked updater stages files on the SD root filesystem
under `/kvmapp/.os-update`. It checks free space and file hashes, snapshots files
that will be replaced, retains the old application and a recovery copy of itself,
and writes a durable transaction journal before requesting a reboot.

At early boot, before userspace services start, the recovery dispatcher applies
the staged files with atomic per-file replacement. It rejects symlinked parents
and destinations on other mounts. Existing configuration files are retained.
The application then starts and must pass five consecutive local health checks.
A successful check commits the version and monotonic sequence. Failure triggers
a reboot; the next boot restores the previous files and application. Interrupted
application of a package is also rolled back on the next boot.

This is a journaled file update, **not an A/B root filesystem**. Application health
checks do not prove every VPN, video mode or peripheral works. Bootloader damage,
SD failure and sudden power-loss behavior require separate qualification.

## Package trust and format

`NKOSAPP1` magic, a big-endian manifest length, JSON manifest, an Ed25519 signature,
and a gzip tar payload form the package. The signature binds format, product,
architecture, version, sequence, system foundation, native-library fingerprint,
removals, payload checksum and every file's path, size, mode and checksum.

Format 1 has kind `application`; format 2 has kind `system` and adds `rootfs/...`
entries. Symbolic links are encoded as regular signed payload files containing
the link target, and are created only after validation. Archive links and special
files remain forbidden. Path traversal, conflicting entries, unexpected modes,
foreign keys, unsigned original NanoKVM archives and altered files are rejected.

Limits: 192 MiB package, 512 MiB expanded payload, 64 MiB per file, 8192 payload
files and 4096 explicit removals. System configuration preservation and permitted
paths are enforced by the verifier, not merely by the package builder.

The public key is embedded in `server/osupdate/release-ed25519.pub.pem`; the
private signing key is never included in source exports, images or packages.

## Building a system package

From `server/`, using the project's selected Go runtime:

```sh
go run ./cmd/nkos-package \
  --app /absolute/stage/server \
  --system /absolute/curated-rootfs \
  --system-base /absolute/image/etc/nkos-system-base \
  --key /secure/location/release-ed25519.pem \
  --version VERSION --sequence NEXT_SEQUENCE \
  --output /new/output/NanoKVM-OS-update.nkos
```

`--app` contains `NanoKVM-Server`, `web/` and matching `dl_lib/`. The native libraries
are fingerprinted and retained from the installed image, not replaced by the
package. `--system` is a curated tree such as `usr/bin`, `usr/lib`, `usr/share` and
supported `etc` paths; include each program's complete dependency set. Optionally
pass `--remove-list FILE` with one `rootfs/...` path per line. Configuration files,
protected paths and the updater itself cannot be removed.

The system foundation is derived during image assembly from musl, the kernel
release and the matched kernel modules. The builder checks the signing key,
verifies its output and extracts the package before reporting success. Release
qualification must additionally check installation on the device.

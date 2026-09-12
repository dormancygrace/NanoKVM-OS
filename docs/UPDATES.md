# NanoKVM OS updates

Beta-4 is distributed as a complete SD image and establishes the kernel-capable
updater for subsequent compatible signed `.nkos` releases. Older installations
need this image or an explicitly compatible updater bootstrap before format-3
kernel packages can be accepted.
See [installation instructions](INSTALL.md) for the full-image procedure.

## What packages update

A system package contains the application, web interface, and selected system
programs, libraries and data, including dependencies assembled by the maintainer.
Existing supported `/etc` configuration files are preserved. Explicit signed
removals retire obsolete program files and service scripts. Nothing is downloaded
or executed as an unsigned post-install script.

Format-2 packages exclude the kernel and boot partition. Format 3 adds kernel
and matching module replacement as described below. The libc/loader, account
databases, credentials and bootloader remain excluded.

Legacy format-1 application packages remain supported when their native-library
fingerprint matches. They update only the application and web interface, without
a system reboot. System packages require the matching source foundation identifier and
matching native libraries; incompatible packages are rejected before installation.

## Installation and recovery

The device checks `dormancygrace/NanoKVM-OS` releases once a day. Checking is
automatic; installation requires an administrator action. System packages use the
asset name `NanoKVM-OS-update.nkos`; legacy `NanoKVM-OS-application.nkos` assets are
also recognized. Manual file upload uses the same verification path.

For format 2, the independent, statically linked updater stages files on the SD root filesystem
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


## Kernel packages (format 3, beta-4 updater)

The beta-4 updater adds signed kernel replacement on the existing single boot
partition. **No A/B or automatic kernel rollback is provided.** A power failure
while FAT metadata is written, a broken kernel or an interrupted system update
may require reflashing the SD card. Format-1/2 behavior remains unchanged.

Upgrade the updater before uploading a format-3 package. Beta-3's original
updater rejects format 3; the beta-4 full image includes support. A compatible
format-2 bootstrap package can update the application and `/usr/sbin/nkos-update`
first, followed by a format-3 package. Installing only an application package does
not update the separate helper.

A kernel package has `format: 3`, `kind: system`, and `kernel` metadata with
`release` (a new, unique uname release) and `system_base` (the target foundation).
It must include `rootfs/boot/boot.sd` and the complete matching module tree under
`rootfs/usr/lib/modules/RELEASE/`, including `modules.dep` and `modules.builtin`.
Build/source symlinks are excluded. The bootloader, partition table, `uEnv.txt`,
libc/loader and credentials remain protected.

Installation validates the signature, source foundation/native ABI, paths,
space on rootfs and the mounted writable FAT partition, and all file hashes.
The updater prepares the complete new module directory alongside the old one
before publishing the FIT: existing images run S00kmod before the system update
dispatcher. It copies `boot.sd` to a temporary file on the boot partition, fsyncs
it, verifies its read-back hash, then renames it over the existing boot file.
The running kernel remains unchanged until reboot. At boot the updater mounts the FAT boot partition itself (S01fs has not run
yet), then checks the expected uname release and boot-file hash before applying other system files.
Confirmation commits the application version, sequence and target foundation.
An interrupted/unconfirmed kernel transaction is stopped for recovery instead of
restoring old userspace/modules underneath the new kernel.

For the packager command above add:

```sh
--kernel-release 7.2.5-nanokvm-os \
--target-system-base /absolute/new-image/etc/nkos-system-base
```

`--system-base` is the source foundation, while `--target-system-base` comes from
the new image with its newly built kernel and complete vendor module set. Never
reuse modules from another uname release or omit the video/Wi-Fi drivers.

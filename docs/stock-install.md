# Migrating stock Sipeed firmware to NanoKVM OS

**Development preview.** The offline package and SSH installer are implemented
and tested on host fixtures. Booting the migration FIT through stock Sipeed
U-Boot, cold boot after the partition switch, and real device migration still
need qualification. No migration package is included in releases by default.
Use the [full SD image](INSTALL.md) for a qualified installation today.

Both methods install the same `nanokvm_X.Y.Z.tar.gz` release asset. This is a
stock **offline application update package**, not an APK. It replaces the stock
operating system and applications with NanoKVM OS. Existing NanoKVM OS devices
must use [package updates](UPDATES.md). The stock updater accepts only numeric
`X.Y.Z` filenames. A beta release therefore uses the same numeric wrapper name
(e.g. `nanokvm_3.7.0.tar.gz` inside release `v3.7.0-beta.2`); the checked
`migration/target-version` and installed OS retain the complete beta version.

## Requirements and retained settings

The first implementation supports SG2002 NanoKVM Cube/Full, Lite and PCIe with
an MBR card, FAT partition 1, the running stock ext4 root on partition 2, and an
existing, writable exFAT partition 3 mounted at `/data`. It refuses other
layouts, a fourth partition, an unexpected boot device, an existing `dbg.sd`,
insufficient space, or a mounted USB virtual disk/image. Detach virtual media
in the stock UI first; the installer does not disconnect USB networking for you.

Partition 3 and its files stay at their existing sectors. Wi-Fi credentials,
boot network configuration, hostname, the root password, SSH host keys and
`root/.ssh/authorized_keys` are carried over. The existing SSH enabled/disabled
choice is retained. Stock web users, application settings and installed tools
are not migrated. The new web login is **admin / admin** and requires a password
change; that change also updates the root password used by SSH.

The preflight calculates the actual space requirement: the complete payload,
saved stock boot image and 8 MiB of headroom on `/data`, the recovery FIT plus 1 MiB free on the old boot volume,
and at least 128 MiB of root filesystem headroom. The stock updater also needs
its own download/extraction space on the old root. For a card without a usable
partition 3, install the full SD image instead.

## Offline package

After a release has been qualified and includes the installer:

1. Download `nanokvm_X.Y.Z.tar.gz` and `SHA256SUMS` from that NanoKVM OS release.
   Verify the package against the published checksum. Keep its original filename.
2. In stock Sipeed **2.4.0 or newer**, open the offline application update,
   upload the package, and start the update. This authorizes installation and
   the required reboots.
3. Wait for NanoKVM OS to start and finish board selection. Open
   `https://DEVICE-IP/`, sign in, and change the initial web password.

The package restores the stock updater's own `/root/old` application before
preflight. If preflight refuses the migration, the stock UI remains available.
Read `/var/log/nanokvm-stock-install.log` over SSH for the reason. A failed
attempt can leave `/root/.nanokvm-stock-package` and
`/data/.nanokvm-stock-install` for inspection; do not remove the latter after
recovery has started formatting. The installer never silently replaces an
existing transaction.

The package shape follows the stock
[offline updater](https://github.com/sipeed/NanoKVM/blob/509a22f6bd94af85bca264d37f2fab70763b60e3/server/service/application/update_offline.go)
and [archive validator](https://github.com/sipeed/NanoKVM/blob/509a22f6bd94af85bca264d37f2fab70763b60e3/server/service/application/archive.go).

## GitHub script over SSH

Enable SSH in stock settings and connect as root. The stock image needs
Python 3.8 or newer. Set `TAG` to an actual release that contains the migration
package; `vX.Y.Z` below is a placeholder. Download the script from the same tag:

```sh
TAG=vX.Y.Z
curl --fail --location --proto '=https' --proto-redir '=https' \
  "https://raw.githubusercontent.com/dormancygrace/NanoKVM-OS/${TAG}/scripts/install-stock.py" \
  -o /tmp/nanokvm-install.py
python3 /tmp/nanokvm-install.py --version "$TAG" --check
```

After the compatibility check succeeds:

```sh
python3 /tmp/nanokvm-install.py --version "$TAG" --yes
```

Without `--yes`, the script only downloads, verifies and checks. Without
`--version`, it selects the latest stable GitHub release and fails if that
release has no migration package. HTTPS certificate verification stays on;
HTTP redirects, missing/duplicate checksums, truncated downloads and unsafe
archive entries are rejected before the package code runs. Release checksums
are fetched over HTTPS; this is not a separate digital-signature format.

Installation runs independently of the SSH connection. Before reboot, its log
is `/var/log/nanokvm-stock-install.log`. During recovery, the last step or error
is `/data/.nanokvm-stock-install/status`. Do not interrupt power while writing
the card. Board selection can require an additional reboot.

## Card transition and interruption behavior

Stock cards can have only a 16 MiB boot volume, too small for normal NanoKVM OS
kernel updates. The migration therefore prepares a **new 64 MiB FAT boot
volume inside the old root partition**, aligned to 4 MiB. A new F2FS root
occupies the remaining old-root sectors. It keeps the existing partition 3
and any gap before it. It does not impose the full-image partition offsets.
For example, with stock p1 at sector 1, size 32768 and p2 at sector 32769,
new p1 begins at 20 MiB and new p2 begins at 84 MiB.

The stock-side installer stores verified payloads, one small settings archive,
the original MBR and the stock boot image on partition 3. It replaces `boot.sd`
with a recovery FIT only after staging and readback succeed. Stock `fip.bin`
stays in place for that boot. Recovery runs from RAM, checks its embedded
payload hashes, and maps only the planned new filesystem ranges with loop
devices whose kernel-enforced size limits are checked. It formats those ranges,
extracts the new root, restores access settings, flushes and unmounts both
filesystems, and finally changes the two MBR entries. The new boot contains
NanoKVM OS's normal `fip.bin` and automatic-detection `boot.sd`.

Before formatting, a payload validation failure can restore the stock boot.
After formatting begins, the stock root is no longer usable: a reboot resumes
recovery with the original saved settings. It never snapshots a partially
written root. The MBR commit is idempotent and rejects an unexpected partition
table. Once the new OS boots, its storage service removes the bounded staging
files. A torn final MBR sector or faulty storage can still require external
recovery; host tests do not establish the SD controller's power-loss behavior.

The older attended `nanokvm-stage-update` recovery bundle has fixed layout
checks and is not supported on this migrated layout. Normal signed APK updates
work independently of those fixed offsets.

## Build and validation

Build ordinary platform outputs first, then explicitly request the installer:

```sh
platform/build.sh -o /path/to/build stock-installer
```

Outputs are `release/stock-installer/nanokvm_X.Y.Z.tar.gz` and `SHA256SUMS`.
The builder checks the root archive version, cross-compiles the small layout
helper for SG2002, creates the new FAT filesystem, and builds
a recovery FIT with a gzip kernel for stock U-Boot and an LZ4 initramfs. The
root archive stays on `/data`, not in RAM or the small old boot volume.

The Release workflow builds the installer in pull-request dry runs. Publishing
it requires the explicit `stock_installer` input, disabled by default. Public
release checksums include it, and publication retries retain the original
asset bytes. Enabling that input is for after stock-device qualification.

Host checks:

```sh
python3 tests/test-stock-installer.py
sudo python3 tests/test-stock-layout-loop.py --output /path/to/disposable-test-output
shellcheck firmware/stock-install/install.sh firmware/stock-install/init \
  firmware/stock-install/S95nanokvm firmware/stock-install/offline-run.sh
```

The first suite executes the real installer/recovery shell logic with isolated
paths and simulated mount/reboot operations, plus the real C MBR transactions
and Python download/archive checks. The second exercises real Linux loop
bounds and FAT formatting on a disposable sparse file. It does not open a
physical card. These checks cannot qualify stock U-Boot compatibility, board
wiring, F2FS mounts on the target, networking after migration, or power cuts on
real media. Those remain the device acceptance tests before release.

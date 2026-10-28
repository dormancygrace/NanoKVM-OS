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

**Custom SSH server policy is not migrated.** Neither `/boot/sshd_config` nor
changes to `/etc/ssh/sshd_config` are copied. SSH uses the new image's
configuration, so a custom port or keys-only restriction must be reapplied.
Save those options off the card before migrating and adapt them to NanoKVM OS.
Do not copy the old file wholesale: it can remove Alpine's configuration
includes and root-login policy, or reference an unavailable SFTP server.

USB gadget and networking settings (including RNDIS/NCM, DHCP/prefix, HID and
USB identity/storage choices), the Ethernet MAC override, Ethernet disabled
state and static DNS configuration are retained. A stock device with no USB
flags keeps its stock composition (HID only); the fresh-image defaults, including
an enabled virtual disk, are not added.

**Root recovery limitation:** the current `nanokvm-stage-update` path and
`firmware/alpine/recovery/init` cannot reinstall the root on a migrated card:
their fixed partition-size/start checks reject this layout. Signed APK updates
and their package rollback remain available. Restoring the entire root requires
writing a full SD image externally (copy wanted data off the card first).

The preflight calculates the actual space requirement: the complete payload,
saved stock boot image and 8 MiB of headroom on `/data`, the recovery FIT plus 1 MiB free on the old boot volume,
and at least 128 MiB of root filesystem headroom. The stock updater also needs
its own download/extraction space on the old root. For a card without a usable
partition 3, install the full SD image instead.

## Recovery progress

During the recovery boot there is no network, web UI or SSH, and no migration
progress indication on the LED or OLED. Diagnostic output is on the UART
console; the last step or error is also saved in partition 3 as
`.nanokvm-stock-install/status`. Board selection can require another reboot.

**Recovery duration has not yet been measured on stock devices.** There is no
qualified completion time or safe power-off timeout for this preview. Elapsed
time, an unreachable UI or an unchanged display cannot tell you whether the
card is being written. Keep power connected during migration. If UART shows
that stock U-Boot cannot load the recovery FIT, use the
[card-reader procedure](#restore-stock-boot-with-a-card-reader) below; it applies
only before formatting starts. Timing measurements remain part of device
qualification before installer publication.

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
Read `/var/log/nanokvm-stock-install.log` over SSH for the reason. The offline
controller removes its temporary package when it finishes, including after a
refused preflight. Handoff names include the kernel boot ID. Under its package
lock, a later run removes handoffs from previous boots after an interrupted
installation; current-boot handoffs are retained because another controller may
still use them. Unclassified handoffs or an unavailable kernel boot ID do not
trigger automatic deletion. Uploading again restores the stock UI before checking for an
active installer or replacing a stale package. An existing transaction in
`/data/.nanokvm-stock-install` is kept for inspection and never silently replaced;
do not remove it after recovery has started formatting.

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

The new boot FAT uses the SD image's 512-byte sectors, four sectors per cluster,
four reserved sectors, `boot` label and MBR type `0x0c`. The builder leaves the
hidden-sector field at zero in its hashed template. After verifying that template,
recovery updates only `BPB_HiddSec` to the planned p1 start through the bounded
loop and verifies the resulting header. `uEnv.txt`, the default hostname prefix
and `image.json` are provided; the latter reports `stock-migration` as the image
origin together with the bundled application version. This matches the image's
boot filesystem format but still requires a physical ROM-to-FIP cold-boot test.

Before formatting, a payload validation failure can restore the stock boot.
After formatting begins, the stock root is no longer usable: a reboot resumes
recovery with the original saved settings. It never snapshots a partially
written root. The MBR commit is idempotent and rejects an unexpected partition
table. Once the new OS boots, its storage service removes the bounded staging
files. A torn final MBR sector or faulty storage can still require external
recovery; host tests do not establish the SD controller's power-loss behavior.

The current root-reinstallation recovery enforces the full-image layouts and
is unavailable on migrated cards, as described above. The fresh-image storage
allocator is deliberately disabled for the retained partition 3; do not re-enable
it on this layout. Supporting root reinstallation for migrated geometry remains
separate work; this installer does not relax those destructive-operation checks.

## Restore stock boot with a card reader

If stock U-Boot cannot start the recovery FIT on the first reboot, the original
stock `boot.sd` is saved on partition 3. A bootloader failure confirmed on UART
occurs before recovery can write the old root. Power off before removing the
card. If the failure stage is unknown, cutting power can interrupt writes;
waiting an arbitrary number of minutes does not establish that rollback is safe.

1. Connect the card to a computer. Open partition 3 (exFAT) and inspect the
   hidden `.nanokvm-stock-install` directory. Do not accept a prompt to format
   an unrecognized partition.
2. Proceed only if the saved transaction is intact, its `formatting` file is
   absent, and the card still has its original partition table. The installer
   writes and syncs `formatting` **before** changing any old-root sectors.
   `status` alone is not proof that formatting never started. On Linux, the
   following read-only checks verify the saved files and original first sector;
   replace `/path/to/partition3` and `/dev/sdX` with the mounted data volume and
   the **whole card device**, respectively. All checks must succeed:

   ```sh
   cd /path/to/partition3/.nanokvm-stock-install &&
     test ! -e formatting &&
     sha256sum -c transaction.sha256 &&
     sudo cmp -n 512 mbr.old /dev/sdX
   ```

3. Copy `.nanokvm-stock-install/stock-boot.sd` from partition 3 onto partition 1
   (the original FAT boot volume), replacing its `boot.sd`. Leave `fip.bin` and
   the partition table unchanged. Flush writes, safely eject the card, put it
   back in the NanoKVM and power on. This restores the original boot path to the
   untouched stock root.
4. Keep the transaction for inspection after returning to stock. A new installer
   attempt refuses an existing `.nanokvm-stock-install` directory; clear it only
   after confirming stock works and the pre-format conditions above still hold.

If `formatting` exists, the table has changed, any saved file is missing/corrupt,
or these checks cannot be completed, **do not restore the stock boot image**.
An absent staging directory is not evidence of a pre-format failure: successful
migration also removes it. Keep an interrupted transaction intact for recovery
to resume, or copy wanted data off the card and install a full SD image
externally. Restoring the old boot image cannot recover a formatted stock root.

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
Host tools are resolved from the selected Buildroot output, including `mcopy`
and `mkfs.fat`, without requiring a previously exported build PATH.

The recovery-only DTB explicitly reserves 512 KiB at `0x80000000` with `no-map`
for the stock OpenSBI, following the pinned
[stock memory map](https://github.com/sipeed/LicheeRV-Nano-Build/blob/d4003f15b35d43ad4842f427050ab2bba0114fa5/build/boards/sg200x/sg2002_licheervnano_sd/memmap.py).
It does not depend on the vendor-only `riscv.fwsz` kernel parameter. The FIT
provides both `config-sg2002_licheervnano_sd` (the
[stock loader selection](https://github.com/sipeed/LicheeRV-Nano-Build/blob/d4003f15b35d43ad4842f427050ab2bba0114fa5/u-boot-2021.10/include/configs/mars-asic.h))
and `config-sg2002_licheervnano_sd_minimal` (the NanoKVM OS loader selection).
These source and artifact checks still require physical boot qualification.

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

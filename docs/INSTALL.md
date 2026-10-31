# Installing NanoKVM OS

A full SD image replaces everything on the card. An existing v2.0-a2 or newer
installation keeps its settings and data with [package updates](UPDATES.md)
instead.

## Migrating stock firmware

An experimental [stock migration package and GitHub SSH installer](stock-install.md)
are available for development and host testing. Stock-device qualification is
still pending; released installation instructions below use the full SD image.

## Fresh SD card

1. Download `NanoKVM-OS-*.img.zip` and `SHA256SUMS` from the
   [image download page](https://dormancygrace.github.io/NanoKVM-OS/).
   It links the newest full image; a newer application release may contain
   package updates only.
2. Verify the ZIP checksum, extract the `.img` and flash it to an SD card of
   at least **1 GB** (see the capacity requirement below). **2 GB or larger**
   is recommended for additional user data and disk images.
3. Boot NanoKVM. First boot creates the exFAT data partition in the remaining
   card space, detects the board and restarts once. Start with Ethernet/DHCP
   and open `https://DEVICE-IP/`.
4. Sign in with **admin / admin** and change the password when prompted.
   Until then, the web interface only allows the password change. For the
   administrator this also sets the Linux root password used by SSH.
   SSH is disabled by default; enable it in settings if needed.
   mDNS is also disabled on fresh cards, and after a recovery that keeps no
   settings; enable it in Settings → Network if needed. Package updates and
   recovery with preserved settings keep the existing mDNS preference,
   whether enabled or disabled.

## Card layout

New images use 4 MiB partition alignment. The partition sizes are unchanged:

| Partition | Start | Size | File system | Contents |
| --- | --- | --- | --- | --- |
| 1 | 4 MiB | 64 MiB | FAT16 | bootloader (`fip.bin`), kernel (`boot.sd`), boot settings |
| 2 | 68 MiB | 768 MiB | F2FS | Alpine Linux system root |
| 3 | 836 MiB | remaining space | exFAT | user data, created on first boot |

Fresh installation requires at least **96 MiB** remaining for `/data`; it
allocates all remaining space, not a fixed 96 MiB partition. With this layout,
the card must expose at least **977,272,832 bytes (932 MiB)**. A card exposing
exactly 1,000,000,000 bytes leaves about **118 MiB** for `/data` before filesystem
overhead (about **121 MiB** with the older layout). This smaller minimum applies
only when creating `/data` from a fresh image. Existing data partitions are
neither resized nor reformatted by package updates.

Package updates do **not** move or resize existing partitions. Older image
layouts remain supported by first boot and by newly built recovery bundles.
Recovery bundles made before this layout change do not support the aligned
layout: use a current bundle. Keep the existing card layout when updating an
installed device; there is no need to reflash just for alignment.

### SD write policy

At normal startup, `nanokvm-storage` applies `noatime` to the F2FS root, FAT
boot partition and exFAT data partition, including installations with older
fstab entries. This avoids access-time writes for both files and directories;
`nodiratime` is redundant. A failed remount is logged and boot continues.
The read-only placeholder used when data storage is unavailable is untouched.

F2FS discard/TRIM and write barriers retain their existing settings. There is
no new periodic `fstrim` job. Discard is already enabled on the development
card; switching every card to periodic TRIM requires latency measurements,
not just its advertised support. Alignment to 4 MiB is also a general choice,
not a claim to know every card controller's internal erase-block geometry.

The existing policy disables background F2FS garbage collection to reduce
idle writes; foreground collection still runs when space is needed. APK
indexes and temporary files stay in RAM. These settings do not remove the
need for clean shutdowns or free space for updates.


After a power loss, partitions 1 and 3 are checked (`fsck.fat`,
`fsck.exfat`); Linux would otherwise keep reporting them as not properly
unmounted and repair nothing. A clean card is not checked. Partition 1 is
small and is checked before it is mounted. Partition 3 is mounted at once and
checked at the next shutdown or restart, after the services using it have
stopped: the check reads metadata only, so its time grows with the number of
files, not with the data. A card of disk images takes a fraction of a second,
tens of thousands of small files take tens of seconds.

## Versions

New SD images use their NanoKVM OS release version. **About** shows the current
OS version and the image used for the initial installation, which stays unchanged
after package updates. Historical images keep their original image number and
bundled application version; they do not need to be reflashed.

Board detection and the supported models are described in
[board profiles](../firmware/boards/README.md).

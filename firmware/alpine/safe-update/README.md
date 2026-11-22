# nkos-safe-update: package updates that roll back unless confirmed

`nkos-safe-update` runs an apk transaction as a trial. If nobody confirms it in time, the device reboots and
the root is exactly as it was before the update. Its main use is a remote unit: an update that leaves it
running but unreachable undoes itself.

It needs no second root, no repartitioning, and no change to U-Boot or the initramfs. Normal updates still
don't need a reboot.

## How it works

The root is F2FS. With `checkpoint=disable`, F2FS commits nothing written after that point until checkpointing
is re-enabled. A reboot, unmount, power loss or reset discards it, and the filesystem mounts as it was when
checkpointing stopped. (Android uses the same mechanism for userdata during OTA updates.)

1. **Preflight**, all before any change. It refuses if:
   - the root isn't F2FS;
   - a trial is already running or unresolved;
   - settings recovery is pending;
   - there's no hardware watchdog;
   - less than 30 % of the root is free;
   - `apk --simulate` fails;
   - the plan touches kernel, kmod or bootloader packages.
2. `pending` is written to `/var/lib/nkos-safe-update` and synced, so it is committed.
3. `mount -o remount,checkpoint=disable:20% /`.
4. A detached **guard** starts:
   - it takes NanoKVM-Server's update lock (`/kvmapp/.os-update/lock`) for the whole window;
   - it stops `nanokvm-watchdog` and feeds `/dev/watchdog0` itself.
5. apk runs. The service restarts in `nkos-apply-updates` work as usual.
6. `applied` is written. It exists only *inside* the trial, so it survives only if the trial is committed.
7. **`nkos-safe-update confirm`**:
   - re-enables checkpointing, which commits everything;
   - records the result;
   - tells the guard to disarm (magic close) and restart `nanokvm-watchdog`.
8. **Without a confirm**, the guard reboots at the deadline. The reboot discards the trial.
   - If the shutdown hangs, the guard stops feeding after 120 s and the watchdog resets the board.
   - If the guard itself is killed, the watchdog resets the board about 50 s later.
   - Power loss, a panic (`kernel.panic=10`, `panic_on_oops=1`) or an OOM end the same way.
9. At boot, the `nkos-safe-update` service reads the state on the root:
   - `pending` without `applied` means the trial was **rolled back**;
   - both present means it was **committed**, and only the cleanup was interrupted.

   The result is appended to `/var/lib/nkos-safe-update/history`.

The guard also polls `/proc/mounts`. If anything re-enables checkpointing behind its back, the trial is
committed without a confirm, so the guard records a warning and stands down. Remounts of `/` without
`checkpoint=` keep it disabled; this was checked with busybox and util-linux `mount`.

## Usage

```sh
nkos-safe-update start [-t SECONDS] [-- APK ARGS]   # default: apk upgrade --ignore <kernel/kmod/bootloader>
nkos-safe-update status
nkos-safe-update confirm
nkos-safe-update rollback                            # reboot now and discard the trial
```

The default confirm window is 900 s (`-t`, minimum 60 s). The trial runs detached, so a dropped SSH session
doesn't interrupt apk.

Install with `sh install.sh`, outside a trial. It installs `/usr/sbin/nkos-safe-update` and the boot service.

## Limits

- **Only the root rolls back.** `/boot` (kernel, bootloader) and `/data` don't.
  - Kernel, kmod and bootloader packages are held back by default, and a plan containing them is refused.
  - `start` lists which of them have updates. Install those separately; that uses the existing
    `boot.new`/`boot.prev` path and needs a reboot.
  - `start` also hashes `/boot` before and after the transaction and warns if it changed.
- **Everything written to `/` during the window is lost on rollback:** settings changes, logs, the event log.
  A UI integration should block settings changes until the update is confirmed or rolled back.
- **Free space:** freed blocks can't be reused until the commit, so the update has to fit in the free space.
  - On the test unit, reinstalling all 165 non-kernel packages left 24 703 blocks (96 MiB) unusable at peak.
  - A full rewrite of every file on a 768 MiB root, the size of the fresh SD image, also fit.
- **A kernel boot-once isn't covered.** U-Boot falls back to `boot.prev` only when `boot.sd` fails to load or
  boot, not when the new kernel boots and then hangs. Covering that needs a U-Boot change, for example a
  `boot.once` image that U-Boot deletes (`fatrm`) before booting it.
- **This kernel has no magic SysRq.** Hard paths therefore rely on the watchdog.

## Test record

Hardware: a NanoKVM Cube (beta board) on NanoKVM OS 4.2.1, apk-tools 3.0.9, migrated from stock (7728 MiB root).

| Test | Result |
| --- | --- |
| Scratch F2FS image: writes, `fsync`, rename after `checkpoint=disable`, then a copy of the image mounted read-write | all discarded; `fsck.f2fs` clean |
| `remount,ro` while disabled | refused by the kernel; the root stays read-write and uncommitted |
| `umount` while disabled | discarded |
| Remount without `checkpoint=` (busybox and util-linux, with and without new options) | stays disabled |
| Live root: `checkpoint=disable:10%` and back to `enable` | instant; mount options identical afterwards |
| Live root: disable, write 30 MiB, normal `reboot` | all writes gone; normal boot |
| Watchdog fed by a shell that is then killed with `kill -9` | board reset about 49 s later |
| Trial (demo 1→2, 40 MiB payload), `confirm`, normal reboot | committed; watchdog daemon restored |
| Trial (demo 2→1), `confirm`, then an immediate `reboot -n -f` (no sync) | committed |
| Trial reinstalling 165 packages (busybox, musl, openrc, openssh, nanokvm, ...), then `rollback` | root byte-identical to the snapshot taken before the trial, except logs, ZeroTier runtime files and the history entry |
| Same trial, `reboot -n -f` at step 138/165 (in the middle of `nanokvm`) | byte-identical as above; no `.apk.*` leftovers; no F2FS errors |
| Trial installing a package that takes every interface down 5 s later (no confirm possible), 90 s window | unreachable over LAN and ZeroTier, rebooted at the deadline, back on the old root 123 s after start |
| `kill -9` of the guard during a trial | watchdog reset 48 s later; rolled back |
| Plan containing `nanokvm-kernel` | refused before any change |
| Unknown package; `-t 10`; `confirm` without a trial; a second `start` during a trial | refused, nothing changed |
| NanoKVM-Server's update lock during a trial | held by the guard |
| `remount,checkpoint=enable` by hand during a trial | detected within one feed interval; warning in the history; watchdog daemon restored |
| 768 MiB scratch root holding a copy of the root: disable at 20 %, rewrite all 2409 files under `/usr /lib /bin /sbin /etc /kvmapp` | no failures; `fsck.f2fs` clean after re-enabling |

`tests/mkrepo.sh` builds the unsigned test repository on the device (apk 3 `mkpkg`/`mkndx`): the demo
package versions and the network-breaking package. `tests/manifest.sh` hashes the whole root, for the
byte-exact comparisons.

Not tested yet: a physical power pull during a trial, as opposed to `reboot -n -f`, and very long trial
windows.

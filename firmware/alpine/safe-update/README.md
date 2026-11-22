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

   The default `upgrade` also refuses if kernel, kmod or bootloader updates are pending (see
   [Kernel updates](#kernel-updates)).
2. `pending` is written to `/var/lib/nkos-safe-update` and synced, so it is committed.
3. `mount -o remount,checkpoint=disable:20% /`.
4. A detached **guard** starts:
   - it takes NanoKVM-Server's update lock (`/kvmapp/.os-update/lock`) for the whole window;
   - it stops `nanokvm-watchdog` and feeds `/dev/watchdog0` itself.
5. apk runs, with at most 30 minutes for the transaction. The service restarts in `nkos-apply-updates` work as
   usual. If apk fails or is killed, the trial rolls back at once.
6. `applied` is written. It exists only *inside* the trial, so it survives only if the trial is committed. The
   confirm window (default 15 minutes) starts now.
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
committed without a confirm, so the guard records a warning and stands down.

The `:20%` in step 3 is how much free space F2FS may hide when checkpointing is disabled, instead of running
garbage collection first. It is not a limit on how much the trial can write: the update has to fit in the
free space, because blocks freed during the trial are only reclaimed at the commit. If space runs out, apk
fails and the trial rolls back.

## Usage

```sh
nkos-safe-update start [-t SECONDS] [--partial] [-- APK ARGS]   # default: apk upgrade without kernel/kmod/bootloader
nkos-safe-update status
nkos-safe-update confirm
nkos-safe-update rollback      # reboot now and discard the trial
nkos-safe-update kernel        # upgrade only kernel, kmod and bootloader packages (no trial), then reboot
nkos-safe-update kernel-trial [-t SECONDS] [-- APK ARGS]   # whole-system trial: boot a trial kernel once, update inside it
```

`-t` sets the confirm window (default 900 s, minimum 60 s). The trial runs detached, so a dropped SSH session
doesn't interrupt apk.

Install with `sh install.sh`, outside a trial. It installs:
- `/usr/sbin/nkos-safe-update`;
- the boot service;
- the kernel-trial files in `/usr/lib/nkos-safe-update`: two initramfs fragments and their `boot.its`.

`kernel-trial` also needs `apk add u-boot-tools dtc lz4`.

## Kernel updates

`/boot` doesn't roll back: a rolled-back root under a new kernel would have the wrong modules. So kernel,
kmod and bootloader packages never go into a trial.

`nanokvm-release` requires minimum kernel and kmod versions. With those packages held back, apk upgrades only
the part of userspace that doesn't depend on the new release package. A trial of that would test a
combination nobody intends to run. So when a release also updates the kernel, `start` refuses and asks for
this order:

1. `nkos-safe-update kernel`, then reboot.
   - This step isn't covered by rollback.
   - It uses the existing `boot.new` → `boot.sd` path, with `boot.prev` as the fallback if the new image
     fails to load.
   - The current userspace keeps running on the new kernel.
2. `nkos-safe-update start`: a normal trial of the rest, on the kernel it will run on.

`--partial` overrides the refusal. `kernel-trial` (below) covers the kernel too.

## Whole-system trial (`kernel-trial`)

This covers the kernel, its modules and userspace together, using the existing U-Boot. The bootcmd tries
`boot.sd`, then `boot.prev`, then resets; it has done so since 2026-10-23, bootloader 4.x.

1. **Prepare**, on the running system:
   - compose the board image from the kernel payload with their `compose-fit`;
   - unpack its FIT and initramfs;
   - insert two blocks into their `/init` (`ktrial-demote.sh`, `ktrial-watch.sh`) plus a trial id file;
   - repack with lz4 legacy and `mkimage`, using their `boot.its`;
   - check that kernel and device-tree bytes are identical and the structure matches.

   It then:
   - makes `boot.prev` a checked copy of the running `boot.sd`;
   - writes the root record (committed) and then `/boot/nkos-ktrial.armed`;
   - writes the trial image as `boot.sd` (via `.new` and a check on the card);
   - reboots.
2. **Boot-once:** early in the trial image's `/init`, and only when the armed marker carries the image's own
   trial id, it renames `boot.sd` to `nkos-ktrial.sd` and the marker to `.running`. From then on, any reset
   boots `boot.prev`, the old kernel. Without a matching marker, the init is their stock init.
3. **Watcher:** just before `switch_root`, the init forks a shell that uses builtins only.
   - It owns `/dev/watchdog0` and keeps feeding it only while userspace keeps its promises: checking in within
     300 s, then meeting the deadline the guard writes to `/dev/.nkos-ktrial` (plus 120 s for a graceful reboot).
   - After that it stops feeding, and the board resets onto `boot.prev`.
4. **Trial boot:** the boot service checks in and starts a normal package trial (checkpointing disabled), with
   the watcher instead of the guard owning the watchdog.
5. **`confirm`:** commits the root, then installs the boot image with NanoKVM OS's `activate-kernel`.
   `boot.sd` becomes their stock image of the installed kernel, and `boot.prev` stays the image the device ran
   before. The watcher then disarms.
6. **Any other ending** (timeout, power loss, hang, a killed watcher, no check-in): the next boot is `boot.prev`
   with the root as it was.
   - The boot service sees the markers, restores `boot.sd` with `activate-kernel`, and records the rollback.
   - If power is lost between the two confirm steps, it sees "committed" instead and finishes installing the
     boot image.

**Gaps:**
- **A kernel that dies before `/init` runs** boots the same image again after the panic reset, a loop that
  needs a card reader to fix (delete `boot.sd`). Closing it needs U-Boot: for example, a `boot.once` that the
  bootcmd deletes (`fatrm`) before `bootm`, falling back to `boot.sd`.
- **Arming keeps `boot.sd.sha256` at the old image.** If the trial image ever boots without demoting
  itself, `activate-kernel` therefore won't take it for a good image and copy it over `boot.prev`.
- **A kernel-package upgrade inside the trial** runs `activate-kernel`, which writes `boot.sd`. The tool moves
  that file aside until confirm, but NanoKVM OS should offer a way to skip activation during a trial.
- **The new kernel payload isn't fetched yet.** The trial image is built from the installed kernel payload
  (`NKOS_SU_PAYLOAD` overrides it). A real kernel update needs the image built from the *new* kernel package,
  with its modules pre-staged, and that part is not written.
  - So `kernel-trial` refuses a plan that changes `nanokvm-kernel` or `nanokvm-kmod` unless a payload is
    given.
  - Use `nkos-safe-update kernel` for kernel updates for now.
  - Today it can trial the boot path plus a package update on the current kernel.
- **Each kernel release presumably gets its own `/lib/modules` directory,** which this design relies on. That
  is unverified.

## Other limits

- **Everything written to `/` during the window is lost on rollback:** settings changes, logs, the event log.
  `/data` isn't affected. A UI integration should block settings changes until the update is confirmed or
  rolled back.
- **`/boot` is hashed before the trial,** and compared after the transaction, at `confirm` and in `status`.
  A change is reported because it won't roll back; none was seen in testing.
- **Downloads happen inside the trial,** before the confirm window starts. A failed download fails apk, so
  the trial rolls back, with a reboot.
- **This kernel has no magic SysRq.** Hard paths therefore rely on the watchdog.

## Test record: package trials

Hardware: a NanoKVM on NanoKVM OS 4.2.1 (kernel 7.3.4), apk-tools 3.0.9, migrated from stock
(7728 MiB root). NanoKVM OS selected the `lite` board profile; stock reported `beta`. "Scratch image" means an F2FS loop image on `/data`, not the root.

| Test | Result |
| --- | --- |
| Scratch image: writes, `fsync`, rename after `checkpoint=disable`, then a copy of the image mounted read-write | all discarded; `fsck.f2fs` clean |
| Scratch image: `remount,ro` while disabled | refused by the kernel; stays read-write and uncommitted |
| Scratch image: `umount` while disabled | discarded |
| Scratch image: remount without `checkpoint=` (busybox and util-linux, with and without new options) | stays disabled |
| Scratch image: fill until ENOSPC while disabled, then re-enable or discard | writes fail cleanly; re-enable works; discard restores the original files; `fsck` clean |
| Scratch 768 MiB root holding a copy of the real one: disable at 20 %, rewrite all 2409 files under `/usr /lib /bin /sbin /etc /kvmapp` | no failures; `fsck` clean after re-enabling |
| Live root: `checkpoint=disable:10%` and back to `enable` | instant; mount options identical afterwards |
| Live root: disable, write 30 MiB, normal `reboot` | all writes gone; normal boot |
| Watchdog fed by a shell that is then killed with `kill -9` | board reset about 49 s later |
| Trial (demo package 2→1), `confirm`, then an immediate `reboot -n -f` (no sync) | committed |
| Kernel step (test kernel 1→2), then a trial of the rest, `confirm`, normal reboot | committed; watchdog daemon restored |
| Trial reinstalling 165 packages (busybox, musl, openrc, openssh, nanokvm, ...), then `rollback` | root byte-identical to a full sha256 manifest taken before the trial, except logs, ZeroTier runtime files and the history entry |
| Same trial, `reboot -n -f` at step 138/165 (in the middle of `nanokvm`) | byte-identical as above; no `.apk.*` leftovers; no F2FS errors |
| Same trial, `kill -9` of apk at step 41/164 | "apk failed (137); rolling back"; rebooted onto the old root; no `.apk.*` leftovers |
| Trial installing a package that takes every interface down 5 s later, 90 s window, no confirm | unreachable over LAN and ZeroTier, rebooted at the deadline, back on the old root 123 s after start |
| `kill -9` of the guard during a trial | watchdog reset 48 s later; rolled back |
| Default `start` while a test release needs a newer test kernel | refused, with the kernel-first order explained |
| The same with `--partial` | only the independent package in the trial; rolled back with `rollback` |
| Plan containing `nanokvm-kernel` | refused before any change |
| Unknown package; `-t 10`; `confirm` without a trial; `start` during a trial | refused; the running trial is undisturbed |
| NanoKVM-Server's update lock during a trial | held by the guard |
| `remount,checkpoint=enable` by hand during a trial | detected within one feed interval; warning in the history; watchdog daemon restored |

`tests/mkrepo.sh` builds the unsigned test repository on the device (apk 3 `mkpkg`/`mkndx`): the demo package
versions and the network-breaking package. `tests/manifest.sh` hashes the whole root, for the byte-exact
comparisons. The kernel-order test used a similar set built by hand. The overrides `NKOS_SU_HELD` and
`NKOS_SU_APK_OPTS` point the tool at such a set.

## Test record: kernel trials

Same unit. Only kernel 4.2.0 is published (stable and beta), so every "new kernel" was the same kernel and
device tree in a rebuilt image with the trial initramfs. These tests exercise the mechanism, not a real kernel
change.

| Test | Result |
| --- | --- |
| `boot.sd` with 4 corrupted kernel bytes, good `boot.prev` | U-Boot booted `boot.prev` (28 s); `activate-kernel` restored `boot.sd`, and a second run wrote nothing |
| Built trial image (`NKOS_SU_BUILD_ONLY=1`) | kernel and dtb byte-identical; same configuration name; `/init` differs from theirs only by the two blocks |
| Trial (demo 1→2), `confirm`, normal reboot | trial boot demoted itself; watcher armed; check-in; package trial applied. Confirm: `boot.sd` and `boot.prev` match their records, trial files gone, watcher disarmed, watchdog daemon back; persisted |
| Trial, no confirm (60 s window) | guard rebooted at the deadline; `boot.prev` booted; root rolled back; `boot.sd` restored; both rollbacks in the history |
| `kill -9` of the initramfs watcher | reset about 49 s later; rolled back |
| Userspace never checks in (boot service disabled, 120 s limit) | watcher stopped feeding at 120 s; reset; `boot.prev` booted with markers left; running the boot step restored `boot.sd` |
| `reboot -n -f` at step 90/165 of a 165-package reinstall in the trial boot | rolled back; no `.apk.*` leftovers; no F2FS errors |
| Trial image armed with a marker that doesn't match its id (test hook) | the image booted like their stock init (no demotion, no watcher, watchdog daemon up); the boot service restored `boot.sd`; `boot.prev` kept its hash; a second `activate-kernel` wrote nothing |
| `kernel-trial -- fix -r nanokvm-kernel` | refused (new-kernel payload not supported yet); nothing armed |
| Power cut between the two confirm steps (test hook) | `boot.prev` booted with the committed root; the boot service finished installing `boot.sd` |
| Package-trial regression on the final script: confirm, timeout, guard `kill -9` | all as before |

**Not tested yet:**
- a physical power pull during a trial, as opposed to `reboot -n -f`;
- a timeout reboot whose shutdown hangs (the 120 s path);
- a kernel panic during a trial;
- very long trial windows;
- disabling checkpointing on an aged, fuller root, where the remount may need garbage collection or fail
  with EAGAIN;
- a real NanoKVM OS release upgrade. Only 4.2.1 is published, so the real packages were exercised with forced
  reinstalls.

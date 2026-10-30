# NanoKVM shutdown with network restart

This feature shuts down NanoKVM's ordinary services and releases its SD storage,
while keeping the kernel and a small network service running in RAM. It does
not electrically power off the SoC and does not shut down the controlled
computer. Cube, Lite and PCIe use the same software path; hardware qualification
is still required for each board and power arrangement.

The target layout is F2FS on `/dev/mmcblk0p2`, FAT `/boot` and exFAT `/data`.
The older ext4 root layout is also accepted. A successful transition unmounts
persistent filesystems entirely; F2FS background GC, discard and checkpoint
work are not assumed to stop merely because the application stopped.

## User flow and access

System settings offers **Shut down NanoKVM**, a confirmation describing the
loss of video, keyboard, virtual disks, SSH and add-ons, and an optional
magic-packet restart checkbox. Cancel is highlighted and focused, so Enter
cancels. The connected computer stays on.

Preparation returns a fresh 256-bit restart key through the existing
administrator-authenticated API. The browser saves it in session storage before
committing shutdown. The status page reports **Keep power connected** until the
RAM runtime finishes its checks. It then offers **Start NanoKVM OS**, which
performs a normal kernel reboot. Copying the restart key permits another browser
to use the page. Keep the tab or save the key privately: ordinary login and
password recovery services are stopped. The key expires at reboot and is never
put in URLs, cookies, logs or public HTML.

Web restart requires that key and a same-origin POST. Preparation and commit
require an enabled administrator through the existing authentication middleware.
HTTPS preserves the current certificate and port, without a plaintext fallback;
an existing HTTP installation retains HTTP and its existing network exposure.
The RAM server limits TCP connections before wrapping them in TLS, preserving
HTTPS request metadata for same-origin checks. It also limits headers and
request timeouts. A rejected restart remains visible separately from later
successful readiness polls.

Magic packets use separate UDP listeners on ports 7 and 9 and an Ethernet
`0x0842` listener, matching the selected interface's MAC. They do not pass
through HTTP. This option is off by default because standard WoL packets are
unauthenticated: anybody who can reach the listener can restart the device.
Packets received before readiness are discarded, including queued packets
checked against kernel receive timestamps. Duplicate or concurrent starts
produce only one reboot. Enabling magic wake also permits someone else to
leave the safe state; do not coordinate unplugging against a concurrent wake.

This is software reception on a powered, running device, not hardware WoL
after halt. PCIe devices can return to the full OS without cycling host power.
If all NanoKVM power is physically removed and restored, the ordinary boot path
runs; no persistent standby flag is written. Other USB/auxiliary/PCIe feeds may
keep a board powered when one cable is removed.

## Transition and storage checks

1. Close API admission atomically against active mutations; acquire the update,
   APK runner, APK database and transfer exclusions. Ordinary GET/HEAD reads,
   assets and status polling remain available during preparation. New WebSocket
   and MJPEG streams, radio scans and user-disk file operations are excluded.
   Queued package workers recheck the shutdown marker after obtaining their
   locks. Reject recovery or
   network configuration in progress, unsupported storage/network layouts and
   insufficient RAM. Network trial exclusion shares the actual
   `/boot/net-trial.json` record and tracks background apply/rollback work
   through its last successful sync, including retries with an absent record.
   An unsuccessful preparation leaves normal services running.
2. Stage the static Go helper, BusyBox and its ELF library closure in the
   existing `/run` tmpfs. Station Wi-Fi also stages wpa_supplicant, its linked
   libraries and a restricted self-contained configuration. The staged musl
   interpreter supplies its reserved libc/libm/libpthread and related aliases,
   including transitive imports; other missing libraries still refuse shutdown.
   Probe the copies
   inside a chroot before stopping anything.
3. Bind temporary RAM copies over `/etc/inittab` and `/etc/rc.conf`. An init-owned
   acknowledgement proves BusyBox reloaded its restart action before SIGQUIT.
   OpenRC performs the normal service stops. Its final generic unmount pass is
   deferred to the helper; otherwise its `fuser -k` retry would kill the process
   holding the writeback-check descriptors.
4. The shutdown supervisor holds descriptors opened before the service stops
   and calls `syncfs` afterwards, retaining writeback errors for that interval.
   An inotify watch retains OpenRC service failures even though OpenRC clears
   its failure directory at exit and may return success after a stop failure.
5. BusyBox terminates remaining processes and execs the RAM helper as PID 1.
   The helper waits for userspace processes to disappear, detaches USB gadgets
   and mass-storage backing files, disables all swap and resets zram.
6. Sync persistent filesystems, make mounts private, pivot to `/run`, move the
   virtual filesystems, release loop backing files and unmount the old tree.
   No lazy or forced unmount is used. Remaining persistent mounts, busy files,
   mmap holders or uninterruptible userspace processes prevent readiness.
7. Only after confirming all remaining mounts are memory/virtual filesystems,
   flush and mark physical disks and their partitions read-only.
8. Restore the selected network and listeners from RAM. Check released
   userspace watchdog ownership, zero in-flight block I/O, and unchanged write,
   sector, discard and flush counters for three successive one-second
   observations, then publish readiness. Continue checking mounts, optional
   watchdog state and block counters every second.

The board kernel does not enable `CONFIG_WATCHDOG_SYSFS`, so a watchdog may
exist without a `state` attribute. A missing optional attribute is accepted
only on the path that has already required a clean OpenRC shutdown and the
exit of the old userspace. The managed BusyBox watchdog receives SIGTERM,
writes its magic close and exits. With `dw_wdt`, hardware may keep running
while the kernel worker feeds it. If available, `state` reports
`WDOG_ACTIVE` (userspace ownership), not `WDOG_HW_RUNNING`: `active`,
malformed values and read errors other than absence still prevent readiness.
An active watchdog without an open userspace descriptor is not assumed safe.

A failure never becomes safe later. Where the RAM page is reachable, an
authenticated reboot is available even after a failed transition; magic wake
is accepted only after successful readiness. A fatal listener/TLS setup error
after complete filesystem release and disk protection prints a recovery notice,
keeps power connected for two minutes, then requests a normal kernel reboot.
It never reports safe. Earlier fatal failures park PID 1 to avoid a kernel
panic; serial output provides diagnostics, not a guaranteed login or recovery
shell. These failures require manual recovery. Kernel hangs or loss of kernel
watchdog feeding cannot be recovered by this userspace service.

The claim is bounded by Linux filesystem/unmount results, block-device flush
semantics and observable kernel counters. It does not prove an SD controller's
internal power-loss behavior or protect against a kernel/driver/card defect.
No finite idle interval alone proves safety; the unmount and block protection
gates are required before the interval is measured.

## Network scope

The first implementation supports direct numeric IPv4 access on `eth0` or
managed station `wlan0`. Ethernet supports the saved static configuration and
managed udhcpc. Station Wi-Fi requires managed DHCP and a self-contained
PSK/SAE/open network configuration. The RAM DHCP client requests the previous
address and maintains its lease; it writes only to RAM.

A changed DHCP address prevents the initial safe announcement. Find the new
address in the router's lease list and supply the saved restart key to reboot.
The page and current key must remain accessible independently of the stopped
ordinary application.

Preflight rejects hostname/proxy/VPN ingress, USB networking, bridges/VLANs,
IPv6 management, classless/custom routes on the selected interface, Wi-Fi AP
mode, static Wi-Fi, and external EAP/certificate configurations. VPN, mDNS,
discovery, USB forwarding and add-on listeners are not preserved. Wi-Fi drivers
and firmware must already be loaded; reassociation, AP isolation, radio resets
and raw-Ethernet delivery remain hardware tests. UDP wake depends on the
network's unicast/broadcast/forwarding policy.

The UI currently uses English fallback strings; translation catalogs are
unchanged.

## Host verification

The production helper is exercised in an isolated x86-64 QEMU guest with real
BusyBox init and OpenRC, F2FS root, FAT boot and exFAT data disks. It uses the
actual prepare/acknowledge/shutdown/pivot/unmount/network/reboot code, with no
production preflight bypass. See [the fixture instructions](../tests/standby/README.md).

The matrix covers static Ethernet and DHCP, web and UDP magic restart, a
retained persistent mount (including one hidden by the eventual chroot), and an
OpenRC stop failure. A separate listener-failure case removes the staged
network interface during service shutdown, checks the two-minute automatic
reboot after storage protection, then checks the next normal boot.
A writer touches every volume, a process retains a dirty shared mmap, a loop
disk has a backing file on `/data`, and zram swap is active.
Preparation is refused while each update/APK lock or the transfer marker is
owned elsewhere. The next boot verifies final writer markers and all mmap
bytes, and starts ordinary services without re-entering standby.

QMP independently observes all guest disks during five seconds of readiness:
read bytes/operations, write bytes/operations and flush operations must remain
unchanged. The helper additionally checks discard accounting. Failed cases
with a reachable RAM listener must report `safe:false` and still accept
authenticated restart.

Unit/race tests cover authorization/origin handling, readiness and failure
states, concurrent restart, queued magic packets, mount/counter/process/route
parsers, restricted Wi-Fi configuration, RAM budgeting, OpenRC failure
events, optional watchdog sysfs state, delayed fatal recovery and read/stream
admission during preparation. The real network-trial fixture exercises
shutdown refusal before snapshot/staging while awaiting confirmation, during
timer/boot rollback, a removal-sync failure with a pending retry, and a slow apply that
outlives rollback. A real TCP/TLS listener test checks the scheme, origin,
capability and duplicate-restart behavior. Synthetic ELF fixtures exercise
transitive musl aliases and missing real libraries; isolated RISC-V chroot
smoke checks run BusyBox and wpa_supplicant through QEMU user emulation,
including copies of OpenSSL patched to import libc.so.
Executed JavaScript tests verify the page's safe-message gate, a late status
response racing with restart, and persistent restart errors across ready polls.
The browser fixture verifies that Enter cancels shutdown without an API request
and explicit confirmation sends one prepare request. Standard server, web,
cross-build and package assembly checks apply.

These host results do not qualify the RISC-V board kernel, SD controller,
watchdog, USB controller or Wi-Fi driver.

## Reported PCIe hardware checks

The [2026-10-30 review](https://github.com/dormancygrace/NanoKVM-OS/pull/591#pullrequestreview-5714972889)
reports PCIe tests on OS 3.6.0 / kernel 7.3.0-nanokvm-os-r3 with F2FS root,
FAT boot and exFAT data. Station Wi-Fi/DHCP reached readiness, magic packets
were ignored before readiness, the watchdog did not reset the device during
a three-minute hold, and writer files survived software restarts and a physical
power cycle. The reviewer used b8554748 with an unpushed libc.so staging
workaround. HTTPS restart required an incorrect HTTP Origin because of the
listener bug; these runs do not qualify the subsequent fixes.

The [Ethernet addendum](https://github.com/dormancygrace/NanoKVM-OS/pull/591#issuecomment-6527549984)
reports static, gateway-less Ethernet management, a 180-second hold, exactly
one reboot for concurrent web requests, magic restart after readiness, and
intact writer files. It used the same head and the same HTTPS Origin workaround;
Ethernet did not require the Wi-Fi library workaround.

The final fixes still need a board recheck. Wi-Fi loss/reassociation and DHCP
renewal during readiness, ten-minute block observation, memory pressure/dirty
mmap, and Cube/Lite qualification remain outside these reported hardware runs.

## Hardware acceptance before release

Use a recoverable test device and an agreed test window. First record the exact
firmware, F2FS mount options, SD model, interface/address/lease, board type and
all power feeds. Have a working serial console and host-side recovery image;
do not place backup trees on the SD card.

- Exercise normal shutdown under sustained writes to root/boot/data, an open
  shared mmap, memory pressure with zram, mounted writable USB media and active
  add-ons. Verify stop results, no persistent mounts, no swap/backing files,
  clean watchdog daemon exit and disk/partition read-only flags. If watchdog
  sysfs state is available, require inactive userspace ownership; otherwise
  verify kernel servicing by remaining in standby well beyond the configured
  watchdog timeout, including the observation interval below.
- Observe block trace and whole-device counters before transition and for at
  least ten minutes after readiness, including F2FS GC/discard/checkpoint
  activity. Exercise DHCP renewal, link loss/reconnect and station reassociation
  while checking that no SD access resumes.
- Verify web restart over HTTP and configured HTTPS, duplicate requests,
  wrong keys, cross-origin requests, UDP 7/9 and Ethernet magic packets.
  Send packets throughout transition; none may trigger early reboot.
- Inject busy mounts, stop failures, writeback/I/O failures and insufficient RAM;
  verify no safe message and test available recovery. Refuse active update,
  recovery, APK and transfer work without interrupting them.
- On Cube/Lite/PCIe, verify software reboot with power retained. In a separately
  authorized physical-power test, remove all relevant feeds only after
  readiness, restore them, and check normal boot plus filesystem/data integrity.

Do not merge or release based on the host tests alone.

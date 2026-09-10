# USB composition and endpoint budget

NanoKVM exposes several independent USB functions through the SG2002 DWC2
device controller. The **Settings > USB Composition** page reports the active
functions and edits a draft against the measured controller budget. Selecting
a preset or changing an individual switch does not reconnect USB. **Apply**
sends the complete composition; **Discard changes** restores the active values.

## Presets and manual selection

| Preset | Functions | IN / OUT |
| --- | --- | --- |
| KVM + USB network + disk | Three HID functions, network, storage | 6 / 5 |
| KVM + serial + disk | Three HID functions, ACM, storage | 6 / 5 |
| Headless: network + serial + disk | Network, ACM, storage | 5 / 3 |
| Keyboard and mouse only | Three HID functions, normal USB profile | 3 / 3 |
| HID compatibility (USB 1.1) | Three HID functions, compatibility profile | 3 / 3 |

The expandable manual section controls keyboard, relative mouse, absolute
pointer, network, storage and ACM individually. Budget usage follows the draft.
Only additions that exceed either limit are disabled; already-selected functions
can always be removed. Empty or over-budget drafts cannot be applied. Selecting
network or storage manually leaves the HID-only compatibility profile.

USB network and serial are **not mutually exclusive**. For example, keyboard +
network + storage + serial uses 6 IN / 4 OUT. The interface no longer tells the
user to disable USB network as a prerequisite for serial.

The admin-only `PUT /api/vm/device/virtual` requires all six explicit boolean
fields, `mode` (`normal` or `hid-only`) and the `revision` returned by GET.
Missing fields, invalid budgets, empty drafts and stale revisions are rejected
before changing flags. Revision comparison and application hold both HID locks.
The legacy POST toggle endpoint remains available for older clients.

Application snapshots the eight boot flags and installed profile, performs one
stop/start, checks the bound controller and selected configfs links, and restores
the previous files and composition on failure. Existing disk-image paths and NCM
selection are preserved when those functions remain enabled. Unchanged drafts
do not rebind. This is runtime failure recovery, not a power-loss-atomic
transaction across multiple files. A lost HTTP response is read back rather than
blindly replaying the mutation.

Individual HID disable flags are `/boot/usb.disable_keyboard`,
`/boot/usb.disable_relative` and `/boot/usb.disable_absolute`; the legacy
`/boot/disable_hid` still disables all three. Both scripts create HID function
objects in keyboard/relative/absolute order even when some are not linked. Linux
allocates HID minors when the objects are created, so absolute-only remains
`/dev/hidg2`. Disabled input functions are skipped without device-open retries.

## Endpoint accounting

Live SG2002 hardware reports seven device endpoint numbers and six configured
non-zero dedicated IN FIFOs. Endpoint zero is the USB control endpoint and is
not included below.

| Function | IN | OUT | Notes |
| --- | ---: | ---: | --- |
| Keyboard | 1 | 1 | Boot-compatible HID keyboard and LED output report |
| Relative mouse | 1 | 1 | Signed movement deltas; browser pointer lock |
| Absolute pointer | 1 | 1 | Direct 0..32767 X/Y mapping |
| RNDIS or NCM network | 2 | 1 | Bulk data plus notification IN endpoint |
| Mass storage | 1 | 1 | Bulk-only transport |
| CDC ACM serial | 2 | 1 | Bulk data plus notification IN endpoint |

The normal HID + network + disk composition consumes `6 IN / 5 OUT` and is
already at the configured IN-FIFO limit. HID + disk + serial also consumes
`6 IN / 5 OUT` and is valid. HID + network + disk + serial would consume
`8 IN / 6 OUT` and is rejected before the gadget is touched.

Other NanoKVM menus call the absolute pointer a “touchpad”, but its USB descriptor is
an ordinary single-pointer Mouse application with absolute axes. It is not a
Digitizer-class touchpad and has no multitouch contacts or gestures. Keeping
both pointer interfaces is intentional: absolute mode avoids accumulated
coordinate drift during normal remote-desktop work, while relative mode is
useful for firmware, installers and software that captures the pointer.

## Host serial console

Enabling **USB Serial Console** adds a CDC ACM function without rebooting the
NanoKVM. The USB device briefly disconnects and re-enumerates. Linux normally
creates `/dev/ttyACM0` on the managed host; Windows creates a COM port. The
NanoKVM side is `/dev/ttyGS0` and can be selected from the web Terminal menu.

Application packages now include `/kvmapp/system/bin/picocom`, so this also works
on base images without a system picocom. The browser prefers the bundled binary
and falls back to a system installation for compatibility. The bundled build
uses static musl, a 64 KiB input-to-serial queue, and picocom 3.1. Its verified
upstream source archive and license are included in `system/share/picocom/`;
`sh scripts/build-picocom.sh` reproduces the build with a RISC-V musl compiler.

To expose a Linux login after the host kernel has enumerated the USB
device, configure a getty for the resulting `ttyACM` device. Use a persistent
udev rule when stable naming matters. CDC ACM cannot carry BIOS/UEFI output or
kernel messages emitted before the host USB stack loads; those still require
HDMI or a physical motherboard UART with firmware console redirection.

Old system images started a NanoKVM login getty on `/dev/ttyGS0`. That process
would compete with the browser terminal for the same gadget-side port. The
downstream image removes it, and the application performs the same one-time
inittab migration when serial is first enabled on an older image.

## Linux host setup and verification boundary

On the **managed Linux host**, identify the new port after enabling ACM:

```sh
ls -l /dev/serial/by-id/
udevadm info --attribute-walk --name=/dev/ttyACM0
sudo systemctl start serial-getty@ttyACM0.service
```

Replace `ttyACM0` with the actual port. Select `/dev/ttyGS0`, 115200, 8N1,
no flow control in NanoKVM's terminal and press Enter. Configure a login service
on the managed host, not another getty on NanoKVM's `/dev/ttyGS0`.

For reconnects and reboots, an administrator can install a host udev rule that
starts the getty for the matching USB serial number. Example for the audited
unit, in `/etc/udev/rules.d/99-nanokvm-console.rules`:

```udev
ACTION=="add", SUBSYSTEM=="tty", KERNEL=="ttyACM*", ATTRS{idVendor}=="3346", ATTRS{idProduct}=="1009", ATTRS{serial}=="d19506b31359c372", TAG+="systemd", ENV{SYSTEMD_WANTS}+="serial-getty@%k.service"
```

Use the unit's actual identifiers, reload udev rules, then reconnect the USB
device during a maintenance window. Do not assume numbering stays `ttyACM0`
when other serial devices exist. The USB serial number comes from the chip UID;
`0123456789ABCDEF` is only the normal profile's fallback when no usable UID is
available. The audited unit reports `d19506b31359c372` on both sides of USB.

The 2026-09-05 runtime audit verified 65,536 bytes in each direction (including
all byte values), reopening the Windows COM port, and the full terminal
WebSocket / picocom / CDC ACM path. The managed host was Windows; Linux's CDC
driver, getty authentication, unplug/replug rules and boot behavior remain
explicit follow-up checks on a real Linux host. Virtual ACM is a USB byte
stream: its baud setting is not a measurement of physical UART throughput.

See the [Linux gadget serial documentation](https://docs.kernel.org/usb/gadget_serial.html)
for the host/device distinction and the
[systemd serial getty template](https://github.com/systemd/systemd/blob/main/units/serial-getty%40.service.in)
for the login service.

## Runtime profile changes

Before configuring a profile, the scripts unbind the gadget and remove its
configuration and OS-descriptor links. Functions are then linked in the script's
fixed order. Reusing old links used to append RNDIS after HID/disk following an
ACM session, shifting its interface number from MI_00 to MI_04 and producing
Windows Code 10. Stable ordering fixed this in a real normal → serial → normal
cycle without a driver reinstall. Unlinking also releases references before
updating HID attributes. Function objects themselves are reused.

An already-unbound controller must not be unbound again: the vendor kernel
returns ENODEV. Both profiles now check UDC state before unbinding, including
when `start` follows `stop`. The new transaction exposed this on hardware and
successfully restored the previous composition before the script fix.

The composition-editor runtime tests on 2026-09-05 covered all five presets,
keyboard + network + disk + serial, absolute-pointer + network, and restoration
to the standard composition. Every enumerated Windows device reported Code 0.
Console, headless and keyboard/network/disk/serial each passed 65,536 bytes in
both ACM directions and COM-port reopening; simultaneous USB-network pings had
no loss. No reboot or Windows driver installation was needed. Cold-boot and a
physical Linux-host login remain outside this test. Unit tests cover file
rollback (including a second rollback failure), explicit false/missing fields,
all 64 manual selections and both budget directions; browser tests cover draft
editing, cancellation, conflict and error recovery.

`bcdDevice` is a profile hint. If it says HID-only while configured RNDIS, NCM or
mass-storage links exist, the server treats the profile as normal so those
functions remain visible to composition and endpoint-budget checks.

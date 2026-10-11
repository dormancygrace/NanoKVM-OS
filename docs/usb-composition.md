# USB composition and endpoint budget

NanoKVM exposes several independent USB functions through the SG2002 DWC2
device controller. The **Settings > USB Composition** page reports the active
functions and edits a draft against the measured controller budget. Selecting
a preset or changing an individual switch does not reconnect USB. **Apply**
sends the complete composition; **Discard changes** restores the active values.

## Presets and manual selection

| Preset | Functions | IN / OUT | Gives up |
| --- | --- | --- | --- |
| KVM (a new device starts here) | Three HID functions, storage | 4 / 4 | no network adapter on the computer |
| KVM + USB network | Three HID functions, network, storage | 6 / 5 | the serial console |
| KVM + serial console | Three HID functions, ACM, storage (with the helper disk) | 6 / 5 | the USB network |
| Headless server | ACM only, for a serial login set up before | 2 / 1 | keyboard, mouse and storage: nothing but the console appears on the server (RustDesk adds HID and audio back: 5 / 5) |
| Remote desktop | Three HID functions, storage, audio | 4 / 5 | what RustDesk enables; a second speaker on the computer |
| Keyboard and mouse only | Three HID functions | 3 / 3 | everything else |

The budget is 6 IN / 7 OUT, so everything at once (HID + network + storage +
ACM, 8 IN) does not fit: every preset gives something up, and its description
says what. A fresh install (`usb.dev` and an empty `usb.disk0`) runs the first
preset. RustDesk adds the three HID functions and audio to the composition,
which from the first preset is the remote desktop one.

The expandable manual section controls keyboard, relative mouse, absolute
pointer, network, storage, ACM and audio individually, and the **HID-only
compatibility profile (USB 1.1)** switch: older descriptors for firmware and
hosts that reject the composite device, without network, storage or audio
(turning one of those on leaves the profile again). Budget usage follows the
draft. Only additions that exceed either limit are disabled; already-selected
functions can always be removed. Empty or over-budget drafts cannot be applied.

USB network and serial are **not mutually exclusive**. For example, keyboard +
network + storage + serial uses 6 IN / 4 OUT. The interface no longer tells the
user to disable USB network as a prerequisite for serial.

### USB identity

Below the manual section, **USB identity** is what the computer reads from the
device descriptor: vendor ID, product ID, manufacturer and product strings.
Hosts with a USB allow-list, or endpoint security that objects to a device
called "NanoKVM", need something else there. Two presets, **NanoKVM (factory)**
(`0x3346:0x1009`, `sipeed` / `NanoKVM`) and **Generic composite device**
(the factory IDs with the names `Generic` / `USB Composite Device`), and a
**Custom** choice with the four fields by hand. No preset carries another
vendor's ID: a custom vendor or product ID is the user's own, and the page says
so. The serial number stays the chip UID and is shown read-only, because the
Linux console helper's udev rule is bound to it; `bcdDevice` stays the mode
flag the server reads.

The identity is part of the composition: Apply writes it with the rest and
reconnects USB, and it is in the `revision`. `S03usbdev` and `S03usbhid` read
`/boot/usb.vid`, `/boot/usb.pid`, `/boot/usb.manufacturer` and
`/boot/usb.product` at every start (a missing file is the factory value). The
recovery init (inside the initramfs, so changed only with a platform build)
reads the same manufacturer and product files, but takes the IDs from its own
`usb.idVendor` / `usb.idProduct`: a recovery boot presents the factory IDs
unless those two files are set by hand. A vendor or product ID
other than the factory one stops the serial console helper's udev rule on
Linux hosts from matching (it names the IDs); the Windows pointer profile's
ContainerID is a separate UUID and is unaffected.

The admin-only `PUT /api/vm/device/virtual` requires all seven explicit boolean
fields, `mode` (`normal` or `hid-only`) and the `revision` returned by GET; an
optional `identity` object (`vendorId`, `productId`, `manufacturer`,
`product`; an empty field is the factory value) sets the identity, and a
request without it keeps the one in place. IDs are `0x0001`–`0xffff`, names
1–126 printable ASCII characters. GET reports the identity with the serial
number and whether it is the factory one.
Missing fields, invalid budgets, empty drafts and stale revisions are rejected
before changing flags. Revision comparison and application hold both HID locks.
The legacy POST toggle endpoint remains available for older clients.

Application snapshots the ten boot flags, the four identity files and the
installed profile, performs one stop/start, checks the bound controller, the
selected configfs links and the identity the gadget presents, and restores the
previous files and composition on failure. Existing disk-image paths and NCM
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
| USB audio (UAC1) | 0 | 1 | Adaptive speaker, no feature-unit interrupt endpoint |

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

The server bridges the browser and the port itself: it opens the device with
exclusive access (one session per port, and other programs are refused), sets
the line raw with the chosen speed, parity, data bits, stop bits and flow
control (`ttyGS0` is nominal 9600 8N1), and copies bytes both ways. No PTY and
no external program are involved, so there are no terminal-program key
sequences; a second session on the same port, or an unplugged device, ends
with a message. Pastes of any length arrive in order.

To expose a Linux login after the host kernel has enumerated the USB
device, configure a getty for the resulting `ttyACM` device. Use a persistent
udev rule when stable naming matters. CDC ACM cannot carry BIOS/UEFI output or
kernel messages emitted before the host USB stack loads; those still require
HDMI or a physical motherboard UART with firmware console redirection.

Old system images started a NanoKVM login getty on `/dev/ttyGS0`. That process
would compete with the browser terminal for the same gadget-side port. The
downstream image removes it, and the application performs the same one-time
inittab migration when serial is first enabled on an older image.

## Linux host setup: the console helper

The NanoKVM carries a script that sets the login up on the **managed Linux
host**: `nanokvm-serial-console.sh` (source: `firmware/services/`). The USB
settings page shows, while USB serial is enabled, the command that fetches
and runs it, the NanoKVM's USB serial number, and a **Test the console**
button. The host gets the script in one of two ways:

- over the network, from the NanoKVM's web server (`https://<nanokvm>/nanokvm-serial-console.sh`,
  public, `-k` for the self-signed certificate; the page shows its SHA-256), or
- from the **helper disk**: with USB serial and the virtual disk enabled,
  `S03usbdev` attaches a second, read-only LUN to the virtual drive, a 1440 KiB
  FAT image labelled `NANOKVM` ("NanoKVM Setup Disk") with the script as
  `setup.sh` and a `readme.txt`. `mount -o ro /dev/disk/by-label/NANOKVM /mnt`
  then `sh /mnt/setup.sh`. No network is needed.

The script, in POSIX sh (dash, busybox ash):

- `plan` (default) and `status` only read. They report the distribution,
  the init system, the kernel's `cdc_acm` driver, the getty, and the NanoKVM
  on the USB bus (by `3346:1009`, with its serial number and `ttyACM` name),
  and print what `apply` would change.
- `apply` writes `/etc/udev/rules.d/90-nanokvm-serial-console.rules` on
  systemd hosts: a rule that matches the vendor, product **and serial number**,
  marks the port for ModemManager to ignore, and starts
  `serial-getty@<port>.service` for whichever `ttyACMn` the NanoKVM becomes,
  only while it is present. On sysvinit and busybox init (Alpine) it appends
  one respawn line to `/etc/inittab`. Every change goes into a manifest with
  backups under `/var/backups/nanokvm-serial-console/`, a second `apply`
  changes nothing, and `undo` reverts the last change.
- It refuses, and prints the manual steps, in a container, on an immutable or
  read-only system (OSTree, NixOS), without a `cdc_acm` driver or a getty,
  and on init systems it does not handle (runit, OpenRC as PID 1).
- The bootloader's `console=` is out of its scope: CDC ACM cannot carry early
  boot output anyway.
- `--serial SERIAL` binds the rule when the NanoKVM is not plugged in yet; the
  USB page's command includes it.

Hosts without the script: configure a login service on the managed host, not
another getty on NanoKVM's `/dev/ttyGS0`, for example the rule above's
equivalent in `/etc/udev/rules.d/`:

```udev
ACTION=="add", SUBSYSTEM=="tty", KERNEL=="ttyACM[0-9]*", ATTRS{idVendor}=="3346", ATTRS{idProduct}=="1009", ATTRS{serial}=="d19506b31359c372", ENV{ID_MM_DEVICE_IGNORE}="1", TAG+="systemd", ENV{SYSTEMD_WANTS}+="serial-getty@%k.service"
```

Use the unit's actual identifiers, reload udev rules, then reconnect the USB
device during a maintenance window. Do not assume numbering stays `ttyACM0`
when other serial devices exist. The USB serial number comes from the chip UID;
`0123456789ABCDEF` is only the normal profile's fallback when no usable UID is
available. The audited unit reports `d19506b31359c372` on both sides of USB.

Select `/dev/ttyGS0` in NanoKVM's terminal and press Enter. The line settings
there are nominal: the terminal opens the gadget port at 9600 8N1 without flow
control, and CDC ACM carries bytes at USB speed whatever the rate.

**Test the console** sends one carriage return on `/dev/ttyGS0` from the
NanoKVM and reads the answer for up to five seconds: a login prompt, a
password prompt, an open shell (its prompt ends the text), other text, or
nothing. It refuses while the web terminal holds the port.

## Verification boundary

The 2026-09-05 runtime audit verified 65,536 bytes in each direction (including
all byte values), reopening the Windows COM port, and the full terminal
WebSocket / serial bridge / CDC ACM path (then through picocom). The managed host was Windows; Linux's CDC
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
all 128 manual selections and both budget directions; browser tests cover draft
editing, cancellation, conflict and error recovery.

`bcdDevice` is a profile hint. If it says HID-only while configured RNDIS, NCM or
mass-storage links exist, the server treats the profile as normal so those
functions remain visible to composition and endpoint-budget checks.


## Absolute pointer profiles

The absolute pointer has two profiles. **Default** retains the USB absolute mouse
for firmware, installers and other operating systems. **Windows** exposes a pen
and an auxiliary mouse collection, using a shared USB/EDID ContainerID to bind
coordinates to the captured display on Windows 10 1903 and newer. Relative mouse
and keyboard remain separate USB functions. Middle/back/forward buttons and
vertical/horizontal wheels use the auxiliary collection; left/right use pen
contact/barrel reports.

This is opt-in. Applying a profile reprograms the selected EDID and reconnects
USB and HDMI. The UI offers Windows only with the matching kernel ContainerID
support and live EDID programming support; receivers requiring a physical power
cycle are currently excluded. The chosen monitor resolution/orientation and all
advertised detailed timings are retained. A dense CTA extension reuses the
redundant textual serial descriptor slot and removes a duplicated preferred DTD
instead of discarding timings. The numeric EDID serial, monitor name, range
limits and preferred timing remain unchanged.

The per-device UUID is generated on first use in `/etc/kvm/usb_container_id`.
`/boot/usb.pointer_windows` selects the profile. The kernel configfs attribute
`os_desc/container_id` exposes Microsoft OS 1.0 ContainerID; its value is writable
only while unbound. Zero disables this optional descriptor. Windows device
revisions differ from Default to avoid cached descriptor reuse.

References:
- https://learn.microsoft.com/en-us/windows-hardware/design/component-guidelines/digitizer-display-mapping
- https://learn.microsoft.com/en-us/windows-hardware/drivers/usbcon/usb-containerids-in-windows
- https://learn.microsoft.com/en-us/windows-hardware/drivers/display/specialized-monitors-edid-extension

## Touch gestures with the absolute pointer

Both Default and Windows profiles accept the same browser gestures:

- Tap: left click; double tap: double left click.
- Keep one finger on the video and tap with a second finger: right click at the
  first finger's pointer position. Lift the second finger within 350 ms and keep
  both fingers within an 8 CSS-pixel movement tolerance.
- Move two fingers: vertical or horizontal scrolling. Small stationary jitter
  does not scroll. Scrolling and pinching suppress the tap action.
- Long press: existing right-button hold; lifting releases it.
- Tap, then touch again and move: left-button drag; lifting releases it.

Releasing the first finger after a two-finger gesture does not send a left click.
A cancelled touch or leaving the page releases held buttons. The gestures apply
to the touch input adapter, selected automatically for touch devices.

Toolbar and input handlers follow the applied per-function USB status. A disabled keyboard has no toolbar button, virtual keyboard or key interception. The active mouse mode falls back to an enabled USB function without overwriting the browser preference. The mode chooser appears only when both mice are enabled. Pointer Lock and the relative touchpad guide appear only where applicable; Windows pointer mode does not offer legacy coordinate calibration. USB composition is the place to change gadget functions; the old HID-only toggle is no longer duplicated in the mouse menu.

When the controlling browser disconnects and exactly one viewer remains, that viewer automatically receives control unless it explicitly selected view-only. With several remaining viewers, none is chosen automatically; the same rule is applied as viewers leave. A visible View only / Take control action supplements the lock icon on touch screens.

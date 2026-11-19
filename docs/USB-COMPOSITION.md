# USB composition and endpoint budget

NanoKVM exposes several independent USB functions through the SG2002 DWC2
controller. The **Settings > USB > Mode** page (and the toolbar's USB quick
menu, which opens the same panel) starts with the port's mode; in the gadget
mode it reports the active functions and edits a draft against the
controller's endpoint budget. Selecting a preset or changing an individual
switch does not reconnect USB. **Apply** sends the complete composition;
**Discard changes** restores the active values. Applying a composition
without any function turns USB off (it asks first) and applying one with a
function turns it on again.

## USB mode

The port is one of three things, chosen with the switch at the top of the
page and **Apply mode**:

| Mode | The port | What shows below the switch |
| --- | --- | --- |
| Gadget | A device for the computer: keyboard, mouse, disk, network, serial, audio | The composition |
| Host | A host: devices attach to the NanoKVM | The attached devices |
| Disabled | Unused | Nothing; the composition is kept |

A gadget without any function is Disabled as well. Leaving the gadget mode
asks first: the computer loses the NanoKVM's keyboard, mouse, virtual disk,
USB network and serial console until the mode is Gadget again; video keeps
working. While the port is a host or disabled, nothing changes the
composition, RustDesk's remote access included, and **Identity (VID/PID)** is
not listed.

In the host mode the page lists what is attached (name, vendor and product
ID, port, speed, declared current) and says what the port supplies, which
depends on the board:

- **NanoKVM PCIe**: nothing. Every power input of the board is behind an
  ideal diode, so a bus-powered device does not start. Use a hub with its own
  power supply, or a device that has one.
- **NanoKVM Cube**: the board's 5 V supply, when the Cube is powered through
  its power-only port. There is no current limit and no protection on it.
- **NanoKVM Lite**: its one USB-C port is also its power input. As a host,
  power the board through the 5 V pins of its header; the port then powers
  the device from the same supply, without a current limit or protection.

The kernel carries the USB host core without class drivers: a device shows
in the list, and a program that talks to it through usbfs (Network UPS
Tools' `usbhid-ups`, for one) can use it; a flash drive or a keyboard
attached to the NanoKVM does nothing by itself.

The mode is two flags next to the composition's in `/etc/kvm/usb`:
`usb.host` (host) and `usb.off` (disabled); neither is the gadget mode.
`S03usbdev` reads them at boot and at every change.

## Presets and manual selection

| Preset | Functions | IN / OUT | Gives up |
| --- | --- | --- | --- |
| KVM (a new device starts here) | Three HID functions, storage | 4 / 4 | no network adapter on the computer |
| KVM + network | Three HID functions, network, storage | 6 / 5 | the serial console |
| KVM + serial console | Three HID functions, ACM, storage (with the helper disk) | 6 / 5 | the USB network |
| Headless server | ACM, storage (with the helper disk) | 3 / 2 | keyboard and mouse: the console and the helper disk with its script, so the server needs no network |
| RustDesk | Three HID functions, storage, audio | 4 / 5 | what turning RustDesk on switches to; a second speaker on the computer |
| Keyboard and mouse only | Three HID functions | 3 / 3 | everything else |

The budget is 6 IN / 7 OUT, so everything at once (HID + network + storage +
ACM, 8 IN) does not fit: every preset gives something up, and its description
says what. A fresh install (`usb.dev` and an empty `usb.disk0`) runs the first
preset.

Turning RustDesk's remote access on switches USB to the RustDesk preset
(audio only when RustDesk sends sound) and keeps the composition it replaced
in `/etc/kvm/usb-before-rustdesk.json`, with the one it applied. Turning
remote access off puts the replaced composition back, a composition without
any function included, unless the composition was changed in the meantime:
then it stays and the file goes. Turning RustDesk's sound on while it runs
adds audio and still counts as RustDesk's own change. The identity is not
part of this: it stays what it is.

The expandable manual section controls keyboard, relative mouse, absolute
pointer, network, storage, ACM and audio individually, and the **HID-only
compatibility profile (USB 1.1)** switch: older descriptors for firmware and
hosts that reject the composite device, without network, storage or audio
(turning one of those on leaves the profile again). Budget usage follows the
draft. Only additions that exceed either limit are disabled; already-selected
functions can always be removed. Over-budget drafts cannot be applied.

For UEFI setup menus that do not recognize the keyboard, disable USB network,
serial console and audio while using those menus. HID with optional storage
uses per-interface device classes; enabling any of those three functions
requires the composite IAD class, which some firmware does not recognize.

USB network and serial are **not mutually exclusive**. For example, keyboard +
network + storage + serial uses 6 IN / 4 OUT.

### USB identity

The **Settings > USB > Identity (VID/PID)** page holds what the
computer reads from the
device descriptor: vendor ID, product ID, manufacturer and product strings
and the serial number.
Hosts with a USB allow-list, or endpoint security that objects to a device
called "NanoKVM", need something else there. Two presets, **NanoKVM (factory)**
(`0x3346:0x1009`, `sipeed` / `NanoKVM`) and **Generic composite device**
(the factory IDs with the names `Generic` / `USB Composite Device`), and a
**Custom** choice with the fields by hand. No preset carries another
vendor's ID: a custom vendor or product ID is the user's own, and the page says
so. The serial number is the chip UID while its field is empty (the field
shows the UID as its placeholder); a set one is 1 to 126 letters, digits,
dots, dashes or underscores, because the serial console page puts it into a
shell command and the Linux helper into a udev rule. Presets leave it as it
is. The Linux console helper's udev rule is bound to it: after a change, run
the helper's `apply` again on the host. `bcdDevice` stays the mode flag the
server reads.

The identity is part of the composition: Apply writes it with the rest and
reconnects USB, and it is in the `revision`. It can be changed while USB is
off too: Apply then only writes the files, and USB presents the identity when
it is turned on. `S03usbdev` and `S03usbhid` read
`usb.vid`, `usb.pid`, `usb.manufacturer`, `usb.product` and `usb.serialnumber`
in `/etc/kvm/usb` at every start (a missing file is the factory value; see
[Where the flags live](#where-the-flags-live)). The recovery init (inside the
initramfs, which cannot rely on the root file system) reads only files on
`/boot`: `usb.manufacturer`, `usb.product`, `usb.serialnumber` and its own
`usb.idVendor` / `usb.idProduct`. A recovery boot therefore presents the
factory identity unless those files are put on `/boot` by hand. A vendor or product ID
other than the factory one stops the serial console helper's udev rule on
Linux hosts from matching (it names the IDs).

### Disk names

Settings → USB → Identity also configures the SCSI names of virtual
storage: a shared manufacturer (up to 8 printable ASCII characters), and
separate names for mounted images / remote media, the Setup Disk and User Disk
(up to 16 characters each). These are device names presented to the connected
computer, not filesystem labels; changing them does not format a disk.

Clear a field to restore its default. An empty image name follows the media
mode (USB Mass Storage, USB CD/DVD-ROM or Remote ISO). Custom names survive
media changes, reboot and RustDesk preset changes. Apply reconnects USB with
the other identity settings. Some hosts cache names until the device is
rediscovered. The settings remain editable while storage is disabled.

The API uses optional `storageIdentity: {vendor, image, setup, user}` in the
composition request; omitting it preserves existing names. GET returns all
four fields. Values reject control characters, non-ASCII, excessive length
and leading/trailing spaces. Configuration is stored in `usb.storage.vendor`,
`usb.storage.image`, `usb.storage.setup` and `usb.storage.user` under the active
USB flags directory, and participates in the same revision check, snapshot,
rollback and settings recovery as the rest of USB identity.

### User disk

**User disk**, a card below the composition: a writable FAT32 image on the
data partition (`/data/.nanokvm/user-disk.img`, label `NKVMDATA`) that the
computer sees as a third drive of the virtual storage device, for BIOS update
files, profiles and logs. Create it at one of the offered sizes (64 MiB to
4 GiB; the whole image is allocated on the card at once, the data partition
is exFAT; BusyBox `mkfs.vfat -F 32 -s 1|8` lays it out as FAT32 with one
512-byte sector per cluster up to 256 MiB and 4 KiB clusters from 512 MiB),
then turn it on: that is a composition change
(`usb.userdisk` names the image, `S03usbdev` adds `lun.2`, removable,
writable, inquiry string "NanoKVM User Disk"), so the USB devices reconnect.
Formatting anew and deleting are refused while the disk is on. It needs the
virtual disk function (it is another LUN of the same function, so it costs
no endpoints), and the Images menu never lists it: hidden directories under
`/data` are the device's own. Mounting or ejecting a CD/disk image is a
medium put into or taken out of the virtual drive and leaves the other USB
devices alone; an image mounted over another leaves the drive empty for 2.5
seconds in between, so that the computer notices. Only a change of the drive
itself, from a disk to a CD drive or back, or to another SCSI name (a remote
ISO), rebinds the gadget, because a computer reads a drive's type and name
once, when it connects: then the
computer loses and regains the user disk for a moment; what it wrote is on
the image, which the NanoKVM does not touch while the disk is on.

While the disk is off, the file list under the card browses, uploads,
downloads and deletes its files. Files and whole folders can be picked or
dropped onto the list; folders are created first (`mkdir` with
`existsOk=1`), and a name that exists or that FAT32 rejects is skipped and
reported, so an interrupted folder upload can simply be repeated. Existing
files are never overwritten. Dropping a folder preserves empty subfolders;
the folder picker only reports paths containing files. Each request mounts
and unmounts the image, about 0.1 s per small file.

The admin-only `PUT /api/vm/device/virtual` requires all seven explicit boolean
fields, `mode` (`normal` or `hid-only`) and the `revision` returned by GET; an
optional `userDisk` boolean turns the user disk on or off (omitted keeps it; on
needs the virtual disk and an existing image); an optional `identity` object (`vendorId`, `productId`, `manufacturer`,
`product`, `serialNumber`; an empty field is the factory value, the chip UID
for the serial number) sets the identity, and a request without it keeps the
one in place. IDs are `0x0001`–`0xffff`, names 1–126 printable ASCII
characters, a serial number 1–126 letters, digits, dots, dashes or
underscores. GET reports the identity with the set `serialNumber` (empty for
the chip UID), the `chipSerialNumber` and whether it is the factory one.
Missing fields, invalid budgets and stale revisions are rejected (a
composition without any function turns USB off)
before changing flags. Revision comparison and application hold both HID locks.
The legacy POST toggle endpoint remains available for older clients.

Application snapshots the ten boot flags, the USB and storage identity files, the user
disk marker and the installed profile, performs one stop/start, checks the
bound controller, the selected configfs links, the third LUN and the identity
the gadget presents, and restores the previous files and composition on
failure. Existing disk-image paths and NCM
selection are preserved when those functions remain enabled. Unchanged drafts
do not rebind. This is runtime failure recovery, not a power-loss-atomic
transaction across multiple files. A lost HTTP response is read back rather than
blindly replaying the mutation.

Individual HID disable flags are `usb.disable_keyboard`,
`usb.disable_relative` and `usb.disable_absolute`; the legacy `disable_hid`
still disables all three. Both scripts create HID function
objects in keyboard/relative/absolute order even when some are not linked. Linux
allocates HID minors when the objects are created, so absolute-only remains
`/dev/hidg2`. Disabled input functions are skipped without device-open retries.

## What the computer reaches over the USB network

The computer the NanoKVM manages is not trusted: a compromised one must not
be able to sign in to, or attack, the KVM that manages it. So over the USB
network (`usb0`) the NanoKVM accepts from the computer only what the USB
features need, and replies to its own connections:

| What | Port | While |
| --- | --- | --- |
| DHCP: the computer's address | UDP 67 | the NanoKVM serves the USB network's DHCP |
| DNS | TCP/UDP 53 | [Internet over USB](USB-INTERNET-SHARING.md) shares a connection |
| The network boot script | TCP 8070 | [network boot](NETBOOT.md) is on |
| The web port (HTTPS, or HTTP when the server runs without TLS) | 443 by default | always, but the server answers only the [sensors agent's report](HOST-SENSORS.md) (`POST /api/host-sensors`) and the [console helper's download](#linux-host-setup-the-console-helper) (`GET /nanokvm-serial-console.sh`) there, and `403` to everything else |

Everything else that the computer opens over USB is dropped: the web
interface, SSH and every other service, and all of IPv6. Internet sharing's
forwarded traffic is a separate chain and is not affected. The host
watchdog's ARP requests are not IP and pass as before.

The rules are an `input` chain in the `inet nkos_usb_internet` table of
[USB Internet sharing](USB-INTERNET-SHARING.md#system-ownership-and-behavior),
loaded with the rest of that table in one `nft -f` transaction by
`nkos-usb-internet` whenever the USB network is set up: at boot, when a
composition with the network is applied, when a USB network setting changes
and at each reconciliation of the watcher. While the network function is
bound the input chain stays: where the NanoKVM gives `usb0` no address of its
own (`rndis.nodhcpd`, a subnet conflict before the first address, an error
before it) it is the table's only chain, and routing set up by hand over
`usb0` is left alone. The server checks the interface a connection arrived on, not
its destination address, which the computer can choose.

**Managing the NanoKVM from that computer.** Settings > USB > Mode,
under Internet over USB: **Computer can open the NanoKVM over
USB** (off by default, the flag `usb.host_access`) gives the computer
everything again: the input chain is not loaded and the server answers every
request from `usb0`. Turn it on only when the computer is the one you manage
the NanoKVM from. The admin API is `PUT /api/vm/usb-host-access` with
`{"enabled": true, "expectedEnabled": false}`, as for Internet over USB; GET
`/api/vm/usb-internet` reports it as `hostAccess`. With `rndis.nodhcpd` the
NanoKVM gives `usb0` no address of its own, so a USB network configured by
hand also needs this switch.

## Endpoint accounting

The SG2002 controller has seven device endpoint numbers and six configured
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

NanoKVM runs no login getty on `/dev/ttyGS0`: it would compete with the
browser terminal for the port. Enabling serial removes such an inittab entry
if one is present.

## Linux host setup: the console helper

The NanoKVM carries a script that sets the login up on the **managed Linux
host**: `nanokvm-serial-console.sh` (source: `firmware/services/`). The USB
settings page shows, while USB serial is enabled, the command that fetches
and runs it, the NanoKVM's USB serial number, and a **Test the console**
button. The host gets the script in one of two ways:

- over the network, from the NanoKVM's web server (`https://<nanokvm>/nanokvm-serial-console.sh`,
  public, `-k` for the self-signed certificate). The copied command checks the
  file with `sha256sum -c` against the SHA-256 the page received over the
  browser's connection before it runs it, so a machine on the path between the
  host and the NanoKVM cannot substitute another script. (A compromised
  NanoKVM serves both and is out of this check's reach.) The page shows the
  commands once it knows the hash. Or
- from the **helper disk**: with USB serial and the virtual disk enabled,
  `S03usbdev` attaches a second, read-only LUN to the virtual drive, a 1440 KiB
  FAT image labelled `NANOKVM` ("NanoKVM Setup Disk") with the script as
  `NanoKVM-Serial-Console-Setup-Linux.sh` and a `README.txt`.
  `mount -o ro /dev/disk/by-label/NANOKVM /mnt` then
  `sh /mnt/NanoKVM-Serial-Console-Setup-Linux.sh`. No network is needed.

The script, in POSIX sh (dash, busybox ash):

- `plan` (default) and `status` only read. They report the distribution,
  the init system, the kernel's `cdc_acm` driver, the getty, and the NanoKVM
  on the USB bus (by `3346:1009`, with its serial number and `ttyACM` name),
  and print what `apply` would change.
- The serial number goes into a root udev rule, so the script accepts only
  what a NanoKVM can have (letters, digits, `.`, `_`, `-`; up to 126). A
  connected device that reports anything else, or such a `--serial`, ends
  every action with exit code 2 before anything is written.
- `apply` writes `/etc/udev/rules.d/90-nanokvm-serial-console.rules` on
  systemd hosts: a rule that matches the vendor, product **and serial number**,
  marks the port for ModemManager to ignore, and starts
  `serial-getty@<port>.service` for whichever `ttyACMn` the NanoKVM becomes,
  only while it is present. On sysvinit and busybox init (Alpine) it appends
  one respawn line to `/etc/inittab`. Every change goes into a manifest with
  backups under `/var/backups/nanokvm-serial-console/`, a second `apply`
  changes nothing, and `undo` reverts the last change (from `/etc/inittab` it
  removes only its own line, keeping later edits).
- It refuses, and prints the manual steps, in a container, on an immutable or
  read-only system (OSTree, NixOS), without a `cdc_acm` driver or a getty,
  and on init systems it does not handle (runit, OpenRC as PID 1).
- The bootloader's `console=` is out of its scope: CDC ACM cannot carry early
  boot output anyway.
- `--serial SERIAL` binds the rule when the NanoKVM is not plugged in yet; the
  USB page's command includes it. `--vid VID --pid PID` (or `NANOKVM_VID`,
  `NANOKVM_PID`) give the USB identity when it was changed from `3346:1009`;
  the page adds them then.

Hosts without the script: configure a login service on the managed host, not
another getty on NanoKVM's `/dev/ttyGS0`, for example the rule above's
equivalent in `/etc/udev/rules.d/`:

```udev
ACTION=="add", SUBSYSTEM=="tty", KERNEL=="ttyACM[0-9]*", ATTRS{idVendor}=="3346", ATTRS{idProduct}=="1009", ATTRS{serial}=="YOUR-SERIAL", ENV{ID_MM_DEVICE_IGNORE}="1", TAG+="systemd", ENV{SYSTEMD_WANTS}+="serial-getty@%k.service"
```

Use the unit's actual identifiers, reload udev rules, then reconnect the USB
device during a maintenance window. Do not assume numbering stays `ttyACM0`
when other serial devices exist. The USB serial number comes from the chip UID;
`0123456789ABCDEF` is only the normal profile's fallback when no usable UID is
available.

Select `/dev/ttyGS0` in NanoKVM's terminal and press Enter. The line settings
there are nominal: the terminal opens the gadget port at 9600 8N1 without flow
control, and CDC ACM carries bytes at USB speed whatever the rate.

**Test the console** sends one carriage return on `/dev/ttyGS0` from the
NanoKVM and reads the answer for up to five seconds: a login prompt, a
password prompt, an open shell (its prompt ends the text), other text, or
nothing. It refuses while the web terminal holds the port.

## Limits

Virtual ACM is a USB byte stream: its baud setting is not a measurement of
physical UART throughput.

See the [Linux gadget serial documentation](https://docs.kernel.org/usb/gadget_serial.html)
for the host/device distinction and the
[systemd serial getty template](https://github.com/systemd/systemd/blob/main/units/serial-getty%40.service.in)
for the login service.

## Runtime profile changes

Before configuring a profile, the scripts unbind the gadget and remove its
configuration and OS-descriptor links. Functions are then linked in the script's
fixed order, so interface numbers stay stable across profile changes (Windows
reports Code 10 when the network interface moves away from MI_00). Function
objects themselves are reused. The scripts check the UDC state first and never
unbind an already-unbound controller, which the vendor kernel answers with
ENODEV.

`bcdDevice` is a profile hint. If it says HID-only while configured RNDIS, NCM or
mass-storage links exist, the server treats the profile as normal so those
functions remain visible to composition and endpoint-budget checks.

## Touch gestures with the absolute pointer

The absolute pointer accepts these browser gestures:

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

Toolbar and input handlers follow the applied per-function USB status. A disabled keyboard has no toolbar button, virtual keyboard or key interception. The active mouse mode falls back to an enabled USB function without overwriting the browser preference. The mode chooser appears only when both mice are enabled. Pointer Lock and the relative touchpad guide appear only where applicable. USB composition is the place to change gadget functions.

When the controlling browser disconnects and exactly one viewer remains, that viewer automatically receives control unless it explicitly selected view-only. With several remaining viewers, none is chosen automatically; the same rule is applied as viewers leave. A visible View only / Take control action supplements the lock icon on touch screens.

## Where the flags live

The USB flags are files in `/etc/kvm/usb` on the F2FS root partition:

- the composition: `usb.disk0`, `usb.ncm`, `usb.acm`, `usb.audio`, `disable_hid`, `usb.disable_*`, and others;
- the identity, the user disk marker and the USB network settings (`usb.internet`, `usb.host_access`, `rndis.*`).

`S03usbdev`, `S03usbhid`, `S30usbnet` and the server read them there. Each write is a rename followed by a sync of the directory, so a power cut leaves either the old file or the new one. Recovery with preserved settings keeps them with the rest of `/etc/kvm`.

USB flags found on the FAT `/boot` partition are moved to `/etc/kvm/usb` by `/usr/libexec/nanokvm/usb-flags-migrate`:
- **When:** before USB startup and before the application starts. During a live package update, migration waits until the running server stops writing `/boot`. The application requires a base package that supports migration. A migration error prevents startup instead of selecting default USB settings.
- **Which set wins:** whenever `/boot` holds any of them, that set replaces `/etc/kvm/usb`.
- **Power cuts:** the new set and its atomically published manifest are complete before `/boot` is cleaned, and `/boot` is clean before the new set takes the old one's place. A run interrupted at any point is finished by the next one.

**Rollback.** A release that reads the flags from `/boot` finds none there after migration: its gadget comes up with keyboard and mice only, and the composition can be set again in its USB settings. Flags it writes on `/boot` move over again on the next upgrade.

**Card reader.** The flags cannot be edited on the card from a computer: they are on the Linux partition.

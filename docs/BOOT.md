# Boot chain, console and recovery

## Chain

1. **Boot ROM** (SG2002 mask ROM). It loads `fip.bin` from the FAT boot
   partition of the SD card (`/boot` in Linux) by that file name.
2. **FSBL** (BL2, in `fip.bin`): DDR init, then OpenSBI and U-Boot from the
   same file.
3. **OpenSBI** (M-mode, see `platform/opensbi/README.md`). It boots without
   a banner (`FW_OPTIONS=0x1`); Linux logs the SBI version.
4. **U-Boot** (S-mode). It reads `uEnv.txt`, loads `boot.sd` from the boot
   partition, checks its hashes and boots it. If `boot.sd` cannot be read or
   fails its hash check, it tries `boot.prev`, the image the last kernel
   update replaced; if that fails too, it resets the board, so the ROM's UART
   window comes back every few seconds (see Recovery).
5. **Linux** from `boot.sd` (a FIT: lz4 kernel, initramfs, board device tree),
   then the root file system on `mmcblk0p2`.

`nanokvm-bootloader-sg2002` carries `fip.bin`; its trigger puts it on the boot
partition (`activate-bootloader`), so devices get a new bootloader with an
ordinary update. The kernel package rewrites `boot.sd`
(`nanokvm-activate-kernel`). The new image is written as `boot.new`, read
back from the card and renamed over `boot.sd`, so `boot.sd` is always there,
also for a U-Boot older than the fallback; the result is read back again.
Before that, the image being replaced is copied to `boot.prev`, but only when
it is known to be good: its hash, recorded in `boot.sd.sha256` when it was
installed, matches the card (a `boot.sd` with no records is the image the
device booted from). A damaged `boot.sd` therefore never replaces a good
`boot.prev`, and a wrong installed image is replaced by a copy of
`boot.prev`. A transaction that leaves the image unchanged writes nothing. `boot.prev`
keeps the previous kernel, whose modules the update may have removed: booted
from it, the system starts, but without the drivers that need them.

## What the ROM does

Observed on a NanoKVM PCIe, 2026-10-17, from its UART log:

- **Every boot** first offers about 1 s to a UART download (`URPL`), then
  initializes the SD card (`SDI`) and loads `fip.bin` (`BS/SD`).
- **`fip.bin` missing:** it tries SPI NAND (`SNF`), waits about 1 s for a USB
  download (`USBI`, `USBW/1000`), tries eMMC (absent on these boards), reports
  `Boot failed (8)`, and about 5 s later its watchdog resets the board. The
  cycle repeats every 8.6 s. It does not look for any other file name.
- **`fip.bin` with a bad header** (its first 4 KiB zeroed): `PARAM1 magic`
  error, then `DL cancelled. Load flash.` It skips the USB window, fails on
  eMMC and the watchdog resets it: the same loop, with the UART window only.
- **`fip.bin` cut short** (300 000 of 630 272 bytes): the FSBL starts and
  initializes DDR, then fails the OpenSBI checksum (`monitor_cksum`), and the
  loop is the same as for a bad header.

So a damaged `fip.bin` never boots, and the board never stops retrying: a
download at the start of the next cycle always gets a chance.

## U-Boot console and uEnv.txt

U-Boot boots at once (`bootdelay` is -2): there is no countdown to stop on the
serial console. Before it would start, it reads `uEnv.txt` on the boot
partition and takes two variables from it, ignoring any other:

- `bootdelay=1` opens a one-second window to stop autoboot with a key on the
  serial console (115200 8N1); `0` stops only on a key already sent.
- `extraargs=...` is appended to the kernel command line.

The file can be edited from Linux or, with the card in a reader, from a PC.
Images write a commented template. U-Boot from earlier images does not read it.

## Testing a FIP without writing it

The SDK's ROM UART download tool (target `cv181x`, 1500000 baud), given a
directory with the candidate `fip.bin` and an empty `partition_sd.xml`, loads
the FIP into RAM at the ROM's UART window; nothing is written to the card.
Start it, then reset the board. Its U-Boot starts with the SD pads that the
ROM sets only for an SD boot left unset; before reading the card, set them:

```
mw.l 03001038 0 1       # SD0_PWR_EN mux
mw.l 030001f4 1209 1    # SD power
mw.l 03001a04 44 5      # CMD and D0-D3 pads
mmc rescan
```

These are the values the ROM leaves after a normal SD boot (the CLK pad
`0x03001a00` reads 0x48 in both cases). Then `run bootcmd` boots Linux from
the card. The U-Boot changes of #252 and #256 were measured this way.

## Updating fip.bin

`nanokvm-bootloader-sg2002` installs `/usr/lib/nanokvm/bootloader/fip.bin`.
After the transaction its trigger (`/usr/libexec/nanokvm/activate-bootloader`):

1. does nothing when `/boot/fip.bin` already has the package's SHA-256, when
   `/etc/kvm/bootloader-update.disabled` exists, or during image assembly;
   it refuses while recovery is active (`/boot/rec`);
2. writes `/boot/fip.new`, flushes it and checks its SHA-256 as read from the
   card (direct I/O, not the page cache); on a mismatch it removes it and
   leaves `fip.bin` alone;
3. renames `fip.bin` to `fip.prev` (after removing an older `fip.prev`), then
   `fip.new` to `fip.bin`, flushing after each step, and checks the result;
4. asks for a reboot.

If activation fails after moving the old file, it attempts to restore
fip.prev and still reports failure. If rollback also fails, repair
/boot/fip.bin before rebooting.

No rename replaces an existing file. Between completed, flushed steps the
boot partition has the old `fip.bin`, the new one, or none; with none the ROM also offers its
USB window. `fip.prev` is what recovery puts back. The ROM ignores both other
names. FAT is not journaled: a power cut during a metadata write can also
damage the filesystem, so these steps cannot guarantee power-loss safety.

## Recovery

**SSH when the server does not start:** SSH is off by default, and the web UI
is where it is turned on. If the server does not listen steadily on its own
ports within 3 minutes after the application service started,
`nanokvm-rescue` starts SSH anyway, also with the default root password,
since an image can fail on some board before anyone logged in. It logs this
(also to the kernel log), marks it in `/run/nanokvm/ssh-rescue` once sshd
runs, and the SSH settings show it with a button to stop SSH. It lasts until
the next restart. Turning SSH on or off during the boot ends a rescue that is
still waiting.

**With a card reader:** remove the card, copy a good `fip.bin` to its FAT boot
partition, verify the hash.

**Without one, over UART** (done for a missing, a bad-header and a truncated
`fip.bin`): run the ROM UART download tool with a
good FIP; it catches the next cycle of the ROM's loop, with no power cycle.
Set the pads, `run bootcmd`, and in Linux put the good file back
(`mv /boot/fip.prev /boot/fip.bin`, or copy one over SSH). From that U-Boot
alone: `fatload mmc 0:1 0x85000000 fip.prev; fatwrite mmc 0:1 0x85000000
fip.bin ${filesize}`.

**Over USB:** when `fip.bin` is missing, the ROM also waits 1 s per cycle for
a USB download (VID:PID 3346:1000). The SDK has a USB download tool; this
path has not been tried on NanoKVM.

Do not attempt OTP/eFuse changes.

The assembled SD image is for a separate test card or a deliberately approved
reimage; writing it erases the destination card's partition table and files.
A FIP-only upgrade preserves the installed OS and user data.

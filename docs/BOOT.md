# Boot chain, console and recovery

## Chain

1. **Boot ROM** (SG2002 mask ROM). It loads `fip.bin` from the FAT boot
   partition of the SD card (`/boot` in Linux) by that file name.
2. **FSBL** (BL2, in `fip.bin`): DDR init, then OpenSBI and U-Boot from the
   same file.
3. **OpenSBI** (M-mode, see `platform/opensbi/README.md`).
4. **U-Boot** (S-mode). It reads `uEnv.txt`, loads `boot.sd` from the boot
   partition, checks its hashes and boots it.
5. **Linux** from `boot.sd` (a FIT: lz4 kernel, initramfs, board device tree),
   then the root file system on `mmcblk0p2`.

No package updates `fip.bin`: it comes only with a full SD image. The kernel
package rewrites `boot.sd` (`nanokvm-activate-kernel`).

## What the ROM does

Observed on the test stand (NanoKVM PCIe), 2026-10-17, from its UART log:

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
The test stand sets `bootdelay=1`.

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

Stage the new file on the boot partition, verify its SHA-256 there, and
replace `fip.bin` by renaming a temporary file on the same file system; flush
before rebooting. A power cut during the copy leaves the old file in place;
during the rename, at worst the file is missing, and the ROM then offers its
USB window as well. The previous `fip.bin` may stay on the boot partition
under another name: the ROM ignores it, and it is what recovery copies back.

Keep maintenance copies on the host, not on the shared test stand: copies
left there once filled `/data` with gigabytes and the f2fs root completely.
That rule is for work on the stand; it does not restrict the product.

## Recovery

**With a card reader:** remove the card, copy a good `fip.bin` to its FAT boot
partition, verify the hash.

**Without one, over UART** (done three times on 2026-10-17, for a missing,
a bad-header and a truncated `fip.bin`): run the ROM UART download tool with a
good FIP; it catches the next cycle of the ROM's loop, with no power cycle.
Set the pads, `run bootcmd`, and in Linux put the good file back
(`mv /boot/fip.keep /boot/fip.bin` when the previous copy was kept, or copy
one over SSH). A good file can also be written from that U-Boot with
`fatload` and `fatwrite`.

**Over USB:** when `fip.bin` is missing, the ROM also waits 1 s per cycle for
a USB download (VID:PID 3346:1000). The SDK has a USB download tool; this
path has not been tried on NanoKVM.

Do not attempt OTP/eFuse changes.

The assembled SD image is for a separate test card or a deliberately approved
reimage; writing it erases the destination card's partition table and files.
A FIP-only upgrade preserves the installed OS and user data.

# SG2002 FSBL (BL2)

The `fip` step builds BL2 from `fsbl/` of the Sipeed SDK at the `sipeed-sdk`
commit in `../sources.lock`, with the patches of this directory, and puts it
into `fip.bin` with the chip configuration (`plat/cv181x/chip_conf.py`) and
DDR parameter block (`test/cv181x/ddr_param.bin`) of the same tree, as the
SDK's `make_helpers/fip.mk` does. This BL2 loads and CRC-checks the DDR
parameter block but only logs its first word: the DDR setup is compiled in.
The result is byte-identical to the stock FIP's chip configuration and DDR
parameters, which the build used before.

## Source of the stock BL2

The stock BL2 was built from this tree. Its banner names `CHIP_ARCH=SG200X`
(`plat/sg200x` is a link to `plat/cv181x`), an unpublished checkout
`f9a0d1360` and 2026-09-04. Built from the pinned tree, BL2 has the same
strings (every log message), the same `ddr_patch_regs` table byte for byte
and the same CSR accesses. The machine code differs: the stock BL2 was
compiled with T-Head's GCC, which also used its own instruction extensions.

## Build

Settings of the SDK board `sg2002_licheervnano_sd` (`build/scripts/fip_v2.mk`):
`CHIP_ARCH=CV181X` (the same code as `SG200X`), `DDR_CFG=ddr3_1866_x16`,
`TPU_PERF_MODE=y`, no secure boot, no overdrive clocks. The compiler is the
platform's GCC (see patch `0001`). Two builds give identical bytes.

`cvi_board_memmap.h` is the output of the SDK's `build/scripts/mmap_conv.py
--type h` for `build/boards/sg200x/sg2002_licheervnano_sd/memmap.py` at the
same commit. The SDK archive in `sources.lock` does not contain `build/`.

| Patch | Change |
|---|---|
| `0001-build-with-upstream-gcc.patch` | Standard ISA names, C906 CSRs by number, no stack protector or PIE, linker flags |
| `0002-c906-mpll-1000mhz.patch` | C906 PLL 1000 MHz instead of 850 MHz (previously a one-instruction binary patch of the stock BL2) |
| `0003-ddr3-reset-low-200us.patch` | DDR3 RESET# low for 200 us (JESD79-3), from sophgo/fsbl 2026.10.28 |
| `0004-sd-fast-boot-read.patch` | OpenSBI and U-Boot read from SD at 4 bits and 50 MHz instead of the ROM's 1 bit (see below) |
| `0005-lz4-opensbi.patch` | OpenSBI as an LZ4 frame, decoded after the clock switch; `decompress_lz4()` checks the decoder's result |
| `0006-lz4-decoder.patch` | A small LZ4 frame decoder instead of LZ4F, xxHash and LZMA (see "Size") |
| `0007-quiet-console.patch` | Console: the banner, `DDR BIST PASS` and failures only; step marks at INFO, compiled out |
| `0008-drop-unused-paths.patch` | No overdrive PLL path; a FIP with a small-core image is refused |
| `0009-clear-small-bss.patch` | The startup code clears `.sbss` too (GCC's small zero-initialized variables) |
| `0010-read-fip-directly.patch` | BL2 reads fip.bin itself (FAT lookup, SDMA/PIO); three tries per load on an SD boot (see below) |
| `0011-ddr-from-sophgo-fsbl-2026.11.06.patch` | The DDR setup of sophgo/fsbl 2026.11.06: more SIP DDR parts, shorter write-DQ leveling (see "DDR") |
| `0012-ddr3-1866-periodic-zq-calibration.patch` | DDR3-1866: ZQ short calibration every 128 ms |
| `0013-pll-before-ddr.patch` | The PLLs are set up before the parameter loads and DDR init |

## Size and console

The boot ROM reads `bl2.bin` itself, at its own 1 bit and 4.2 MHz, about
2 ms per KB, before BL2 can speed anything up; and at 115 200 baud each
console character costs BL2 87 us. Built with `-Os`, garbage-collected
sections.

| | bl2.bin | BL2 start to exit |
|---|---|---|
| `0005` | 35.8 KB | 149-150 ms |
| `0006` LZ4 decoder, no LZ4F/xxHash/LZMA | 20.9 KB | 147 ms |
| `0007` + `0008` quiet console, unused paths | 18.3 KB | 103 ms |

The console now shows the banner, `DDR BIST PASS`, failures, and the
ROM's own `SD/...` line for each load. A build with `LOG_LEVEL := 3` in
the Makefile prints the step marks again (`P2S`, `MS`, `L2`, ...).

## SD read speed

BL2 loads everything after itself through the boot ROM's loader
(`p_rom_api_load_image`), which finds it inside `fip.bin` on the FAT
partition. The ROM leaves the bus at 1 bit with the card clock at divider
3 of `CLK_SD0`: 4.2 MHz from the 25 MHz crystal until `sys_pll_init()`
(the parameter blocks: 12 KB in 32 ms), 16.7 MHz from the 100 MHz PLL
clock after it (OpenSBI and U-Boot: 282 KB in 161 ms).

Patch `0004` switches the card when BL2 starts loading: ACMD6 to a 4-bit
bus, CMD6 to SD High Speed, the PHY settings U-Boot's and Linux's
`cv1800b` driver use in that mode, and divider 1: 12.5 MHz until
`sys_pll_init()`, 50 MHz after it. (Divider 0, the undivided 25 MHz,
made each ROM load take seconds.) The card's RCA comes from the ROM's
last command (CMD13 with RCA << 16 in the argument register); the ROM
keeps the host settings between loads. When the High Speed switch fails
the card runs at 25 MHz after the PLL switch; when the 4-bit switch fails
nothing changes. It runs only when the ROM booted from SD.

A failed load is retried at the ROM settings: `sd_fast_disable()` resets
the host's command and data lines (a failed transfer can leave them
inhibited; the PHY registers are not affected), restores the divider and
the PHY, sends ACMD6 for 1 bit and only then switches the host back. It
stays armed until every step has succeeded, so each retry tries again; a
retry against a card that does not answer spends up to about 1 s in
timeouts. If the card cannot be restored, the loads keep failing, and
BL2's path for exhausted retries ends in the ROM's watchdog reset
(observed with a cut `fip.bin`, `docs/BOOT.md`), after which the ROM
identifies the card from CMD0. Forcing the rollback after
`sys_pll_init()` on the stand gave the ROM's registers back (1 bit,
divider 3, the same PHY values) and a normal boot, with BL2 at 301 ms.

Each ROM load, timed with the microsecond counter: param2 (4 KB) 12.0 ->
4.2 ms, DDR parameters (8 KB) 20.5 -> 5.5 ms. At 50 MHz a 512-byte load
takes 2.2 ms, and large ones come in at about 9 MB/s, not the bus's
~23 MB/s (Linux and U-Boot): the ROM apparently reads cluster by cluster.

Measured on a NanoKVM PCIe (console log, read time of each image, the
switch made after `sys_pll_init()` only):

| Bus | OpenSBI (136 704 B) | U-Boot (145 408 B) |
|---|---|---|
| ROM: 1 bit, 16.7 MHz | 78 ms | 83 ms |
| 4 bits, 16.7 MHz | 30 ms | 31 ms |
| 4 bits, 25 MHz | 24 ms | 25 ms |
| 4 bits, 50 MHz High Speed | 17 ms | 18 ms |

From BL2 start to its exit (time records at `0x0E000010`): 333 ms before,
210-211 ms with the switch after `sys_pll_init()` (four boots), 149-150 ms
with the switch before the parameter blocks and LZ4 images (`0005`, three
boots).

A FIP loaded over the ROM's UART window does not exercise this, nor the
direct reads below: the ROM then reports UART as the boot source and BL2
leaves the SD bus alone.

## Direct reads

The ROM loader reads at about 9 MB/s whatever the bus (it appears to use
PIO: each 32-bit read of the controller's buffer port takes ~0.5 us), and
prints a line per load. Patch `0010` makes BL2 find fip.bin itself, by
name in the root directory of the first partition (FAT16 or FAT32, as the
ROM does), as up to 16 runs of sectors, and check that its first sector
is the PARAM1 the ROM booted with; then each load is one CMD18 per run:
SDMA into DRAM, PIO into SRAM (an SDMA transfer into SRAM reported
completion before its data had all arrived). Each load is still
CRC-checked. The PARAM1 compare catches a lookup that lands on anything but
a FIP; the exact-name match is what selects fip.bin over fip.prev.

| Load | ROM loader | Direct |
|---|---|---|
| DDR parameters, 8 KB (PIO) | 5.5 ms | 3.6 ms |
| OpenSBI, 74 KB | 11.1 ms | 3.9 ms |
| U-Boot header, 512 B | 2.2 ms | 0.2 ms |
| U-Boot, 192 KB | 22.8 ms | 8.3 ms |

BL2 start to exit: 103 -> 81 ms; bl2.bin grows by 2.3 KB (about 4.5 ms
of the ROM's own read of it).

On an SD boot the ROM reports one try per load, which sent any failed
load to `p_rom_api_flash_init()` (eMMC) at once. BL2 now tries each
round three times on SD: direct, the ROM loader, the ROM loader at the
ROM's bus settings (`sd_fast_disable()`). A direct read forced to fail on
the stand was loaded by the ROM loader on the second try.

## Compression

With reading at that speed, decoding dominates: U-Boot as LZMA (145 KB)
took 39 ms to decode, as LZ4 (191 KB) 2.5 ms, for about 5 ms more
reading. `scripts/nanokvm-fip.py` therefore packs U-Boot (`B3Z4`) and
OpenSBI as LZ4 frames (`lz4 -12 -B5 --no-frame-crc`; the FIP CRCs cover
the compressed bytes). 256 KiB blocks: `0005`'s LZ4F decoder allocated
two buffers of the block size from a 1 MiB area, whose bound it also
fixed; `0006` replaces it with a decoder that allocates nothing and
accepts any frame the validator does. BL2 loads OpenSBI into a buffer and, when it starts
with the LZ4 frame magic, decodes it to its run address after
`load_loader_2nd()` has switched the clocks to the PLLs, at most up to
the U-Boot header; other content is copied as before. BL2 from start to
exit: 210 ms with LZMA U-Boot and plain OpenSBI, 173-174 ms with both as
LZ4 (three boots).

## DDR

`0011` takes the DDR part of sophgo/fsbl 2026.11.06 (`plat/cv181x/ddr`,
`plat/cv181x/include/ddr`) without `ddr_suspend.c` (DDR self-refresh
suspend, not used), with the DDR step marks at INFO as `0007` made them.
FSBL reads the package from `efuse_leakage` (bits 31:29) and the SIP DDR
from `conf_info` (bits 30:28). Besides the parts the pinned tree knew, it
sets up the SIP DDR of package types 0x8 to 0xB (UniIC N25 1 Gbit and N21
2 Gbit, ESMT N21 2 Gbit QFN and ESMT 2 Gbit BGA) with their PHY pin maps,
and external DDR on the CV1815J/K packages. A NanoKVM PCIe reads
`conf_info` 0x570003AB (package type 5: QFN, Nanya 2 Gbit DDR3) and
`efuse_leakage` 0x2C400022 (package 1, QFN88); the Cube reads the same
package type. For it the controller and PHY settings are unchanged.

DDR init takes 9 ms instead of 31 ms. The write-DQ leveling runs a
shorter BIST at each delay it tries (`cvx16_bist_wdqlvl_init`: no command
repeats, no GOTO loop; about 20 ms) and tries every third delay
(`piwdqlvl_dly_step` 2 -> 6, written after `phy_init()`; about 2 ms).
Read back from the PHY after three boots each, the write DQ delays the new
code chooses (0x44/0x47) lie inside the range the old code chose from
boot to boot (0x40-0x48), in a window about 110 codes wide; the new
results repeat exactly. The other training steps vary between boots the
same way with both.

Its DDR3-1866 settings (`ddr_config/ddr3_1866_x16`) were checked against
the Nanya 2 Gbit DDR3 J-die datasheet (NT5CB128M16JR, -EK: 1866 13-13-13,
2 KB pages): CL 13, CWL 9, tRCD and tRP 13.91 ns, tRAS 34 ns, tRC
47.91 ns, tRRD max(4 tCK, 6 ns), tFAW 35 ns, tWR 15 ns, tRFC 160 ns and
tREFI 7.8 us are met with at most one clock of margin, from the
controller's 1:2 clock ratio. Commands use 2T timing. Above 85 °C case
temperature the part needs refresh every 3.9 us, which the controller
does not do by itself; the SoC slows down at 75 °C.

`0012` lets the controller calibrate ZQ every 128 ms (ZQCS, ZQCTL1 in
units of 1024 DFI clocks): it ran the long calibration at initialization
only, so the DRAM's output driver and ODT impedance were never corrected
for temperature and voltage drift. Each ZQCS takes 64 tCK.

`0013` calls `sys_pll_init()` first in `load_ddr()` instead of in
`load_rest()`: until then the C906 ran about 40 times slower, and the FAT
lookup, the parameter loads and their CRCs ran in that phase. DDR init
itself waits on the hardware and does not get faster.

BL2 start to exit with `0010`-`0013`: 46 ms (81 ms with `0010`; DDR init
7 ms). memtester on 96 MB passes with each step.

## Upstream changes not taken

From sophgo/fsbl up to the 2026.11.06 release (its DDR part is `0011`):
- `ddr_suspend.c`: DDR self-refresh suspend, which NanoKVM OS does not use.
- `FSBL_FASTBOOT` (AXI at 500 MHz while loading): a separate change, to be
  measured.
- APLL 614440960 instead of 614400000 (-66.7 ppm from 98.304 MHz), with no
  stated reason; it would change the audio clock.
- eMMC clock 375 MHz: these boards have no eMMC.
- The new `fiptool.py`, which no longer packs the DDR parameters.

## Testing

A new BL2 can stop a board before U-Boot. Test it on a board with its SD card
at hand: load it over the ROM's UART window first, without writing the card
(`docs/BOOT.md`, "Testing a FIP without writing it"); the console shows the
banner `FSBL Jb2829:nanokvm-os:sipeed-sdk <commit>` and `DDR BIST PASS`. A card with a FIP that does not boot is repaired on a
computer: copy `fip.prev` (left by the last bootloader update) over `fip.bin`
on the boot partition.

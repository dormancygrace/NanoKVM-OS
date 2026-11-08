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

A FIP loaded over the ROM's UART window does not exercise this: the ROM
then reports UART as the boot source and BL2 leaves the SD bus alone.

## Compression

With reading at that speed, decoding dominates: U-Boot as LZMA (145 KB)
took 39 ms to decode, as LZ4 (191 KB) 2.5 ms, for about 5 ms more
reading. `scripts/nanokvm-fip.py` therefore packs U-Boot (`B3Z4`) and
OpenSBI as LZ4 frames (`lz4 -12 -B5 --no-frame-crc`; the FIP CRCs cover
the compressed bytes). 256 KiB blocks: the LZ4F decoder allocates two
buffers of the block size from a 1 MiB area, whose bound `0005` also
fixes (the old test could never fail). BL2 loads OpenSBI into a buffer and, when it starts
with the LZ4 frame magic, decodes it to its run address after
`load_loader_2nd()` has switched the clocks to the PLLs, at most up to
the U-Boot header; other content is copied as before. BL2 from start to
exit: 210 ms with LZMA U-Boot and plain OpenSBI, 173-174 ms with both as
LZ4 (three boots).

## Upstream changes not taken

From sophgo/fsbl up to the 2026.10.28 release:
- New SIP DDR vendors (package types 0x8 to 0xB). A Cube reads package type
  5 (`conf_info` 0x570003AB: QFN, 2 Gbit DDR3), which the pinned FSBL knows.
- `FSBL_FASTBOOT` (AXI at 500 MHz while loading, shorter BIST): a separate
  change, to be measured.
- APLL 614440960 instead of 614400000 (-66.7 ppm from 98.304 MHz), with no
  stated reason; it would change the audio clock.
- eMMC clock 375 MHz: these boards have no eMMC.
- The new `fiptool.py`, which no longer packs the DDR parameters.

## Testing

A new BL2 can stop a board before U-Boot. Test it on a board with its SD card
at hand: load it over the ROM's UART window first, without writing the card
(`docs/BOOT.md`, "Testing a FIP without writing it"); the console shows the
banner `FSBL Jb2829:nanokvm-os:sipeed-sdk <commit>`, `cv181x DDR init.` and
`DDR BIST PASS`. A card with a FIP that does not boot is repaired on a
computer: copy `fip.prev` (left by the last bootloader update) over `fip.bin`
on the boot partition.

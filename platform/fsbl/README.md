# SG2002 FSBL (BL2)

The `fip` step builds BL2 from `fsbl/` of the Sipeed SDK at the `sipeed-sdk`
commit in `../sources.lock`, with the patches of this directory, and puts it
into `fip.bin` in place of the stock BL2 of `../fip/base-fip.bin`. The chip
configuration and DDR parameters stay those of the base FIP.

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

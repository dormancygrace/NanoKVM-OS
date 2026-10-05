# OpenSBI 1.9 candidate validation (2026-10-05)

Hardware boot validation is **pending**. No candidate was written to the shared NanoKVM. This record covers the host build and compatibility checks.

- Public source pin: `cbf9f6734dd85a982c63e3cb5db7ffe09da839ca`; Git tree `d6a4747810533f9da9e17428cab821ccc8b1594c`.
- Normal upstream `-O2`, no LTO. Audited 90 C compiler commands and 5 assembly commands; scalar `rv64imac_zicsr_zifencei` and `lp64`. ELF attributes have no standard V; ELF flags indicate soft-float ABI. Upstream's runtime floating-point context assembly remains available for C906 hardware F/D.
- Firmware BIN: 267536 bytes; SHA-256 `1a1f6a1a624ca22ddc0d218800406eaac135deaa349757f6d4341d55983ca9e1`.
- `_fw_end=0x80043000`, `_fw_rw_start=0x80040000`; one 8 KiB stack and DT-selected 40 KiB heap give runtime end `0x8004f000`.
- PMP RX: `0x80000000/0x40000`; PMP RW: `0x80040000/0x10000`. Total 320 KiB, with FDT destination `0x80100000` outside both regions.
- U-Boot SHA-256 remains `bff4144429aea728fc2ac1fa19df73b8f95f231d5842f5a841005222530cd883`.
- FIP: 630784 bytes; SHA-256 `fba685997db21720226c2286d6a4276d5a8bdca312088a4051702c0257021e48`.
- Two independent output-directory builds produce identical OpenSBI BIN/DT/manifest and FIP/report. Both used the verified GCC 16.2 / Binutils 2.47 toolchain. The final canonical outputs use a freshly built public toolchain. Its Buildroot wrapper includes `-fno-tree-vectorize -fno-tree-slp-vectorize`; the older cached wrapper lacked those flags and produced a different OpenSBI BIN despite matching the reported GCC version. The hashes above are from the final public toolchain. U-Boot remained identical across both toolchains.
- Independent FIP validation passes. Thirteen negative cases reject truncation, PARAM1/PARAM2/MONITOR/DDR/loader CRC damage, bad monitor run address, overlap, nonzero suffix, undersized RW region, the old overlapping FDT destination, wrong U-Boot bytes and a valid-CRC first-stage modification.
- Unchanged pinned U-Boot copy/deduplication code and libfdt pass with all ten Linux board DTBs. Native tests use calculated PMP fixtures and scalar address-decoding adapters. Repeated copies remain stable; exact RO reservations deduplicate; an oversized static overlap is detected.
- The complete 456-file checksum check passes with baseline outputs reused for unaffected components and candidate outputs overlaid. This is not a full rebuild of every component.
- A complete SD image was assembled using the clean existing v2.1-b1 rootfs archive. MBR geometry was checked; FIP extracted from the image's FAT partition exactly matches the candidate. Rootfs/application/packages were reused, not rebuilt. Existing 64 MiB FAT16 + 768 MiB F2FS + first-boot exFAT layout is sufficient.

The stock FSBL source loads MONITOR by the PARAM2 size, checks its DRAM bounds and CRC, then flushes that size. The source has no 128 KiB MONITOR cap. ROM behavior, upstream PMP programming, interrupts, PMU and the full boot chain still need a real-device boot with UART capture and physical SD/power recovery.

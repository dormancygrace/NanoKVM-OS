# OpenSBI 1.9 candidate validation (2026-10-05)

**RAM hardware validation passed; candidate persistent installation and SD cold boot remain untested.** The device has returned to original SD OpenSBI 0.9. See [port and hardware scope](README.md) and [machine-readable evidence](RAM-VALIDATION.json).

- Public upstream pin `cbf9f6734dd85a982c63e3cb5db7ffe09da839ca`, tree `d6a4747810533f9da9e17428cab821ccc8b1594c`; tested source commit `ce36bfc9185e4fc2aefa96bb7d44f01de94a2f41` before an unchanged cherry-pick onto the v3 development line.
- Normal upstream `-O2`, no LTO; scalar `rv64imac_zicsr_zifencei`, soft-float `lp64`, fresh source-built GCC 16.2 / Binutils 2.47. The Buildroot wrapper disables tree/SLP auto-vectorization. No standard V is advertised in the M-mode DT.
- Firmware 267536 bytes, SHA-256 `6df03b97a913f0b3a14d2eb8f0e46f558c7a09cca97d50c4649f1938a308ac22`; DT 1869 bytes, SHA-256 `dba944a8ed999dd134feced5797304166ddb20ce433f932f5c86bf57b849120a`.
- `_fw_end=0x80043000`, `_fw_rw_start=0x80040000`; one 8 KiB stack plus 40 KiB heap give runtime end `0x8004f000`. PMP RX `0x80000000/0x40000`, RW `0x80040000/0x10000`; total 320 KiB. Relocated FDT `0x80100000` is outside firmware and U-Boot regions.
- U-Boot SHA-256 remains `bff4144429aea728fc2ac1fa19df73b8f95f231d5842f5a841005222530cd883`. FSBL/BL2, DDR and small-core fields are preserved.
- FIP 630784 bytes, SHA-256 `76e50ecc3fab0da16f2ab750e6f79a67ec2dedeac9397c5f437343e6008e3692`. The older FIP `fba685997db21720226c2286d6a4276d5a8bdca312088a4051702c0257021e48` failed the installed kernel and is superseded.
- Two independent output-directory builds match byte-for-byte for firmware, DT, build manifest, FIP and FIP manifest.
- Independent FIP validation passes; 13 negative cases reject CRC damage, truncation, overlap, bad addresses/PMP/FDT destination, unexpected suffix, wrong U-Boot and a valid-CRC first-stage change.
- Unchanged pinned U-Boot reservation copy/deduplication code and libfdt pass with all 10 baseline Linux DTBs: exact RO deduplication, RW addition, stable repeated copies and oversized overlap detection.
- All 456 output checksums pass after replacing the 5 intended OpenSBI/FIP entries. Unaffected baseline components are reused; this is not a full rebuild of all components.
- A complete SD candidate image was reassembled with this corrected FIP and the clean existing v2.1-b1 rootfs archive. Full ZIP CRC, MBR geometry and exact FIP extracted from FAT pass. Layout remains 64 MiB FAT16 + 768 MiB F2FS + first-boot exFAT. Rootfs/applications/packages are reused.

The final ROM UART RAM boot uses unchanged installed Linux 7.2.9 and normal bootcmd/bootargs. SBI 3.0 reports implementation 0x10009. Linux imports 256+64 KiB firmware reservations, timer/PLIC interrupts progress, both services start, fixed cycles/instructions count, and Chrome Main shows advancing HDMI video. No Oops/panic/overlap messages were found. The PMU probe is bounded counting, not an overflow sampling test.

The SG2002-only draft vector VS handoff patch fixes the early CSR_VLENB panic. Explicit fixed-counter PMU mappings fix SBI 3.0 EVENT_GET_INFO discovery. No kernel changes were needed. Deterministic build manifests retain hardware_validation=pending because they are generated before device testing; RAM-VALIDATION.json records the measured result for the exact hashes.

Original SD FIP SHA-256 `c5958e0849741bc6ae9bb4053c3bc96f2ffb40fbdf477bd3fe8c41173e5e5f2a` remained unchanged. Original SD boot and services were verified after the RAM test. The RAM boot path and return to original SD boot do not establish candidate SD cold boot or repair of a damaged SD FIP.

The RAM test used the installed Linux and current application services. It does not qualify the experimental v3 Rust application or a new complete v3 OS image. The assembled SD ZIP still uses the existing v2.1-b1 rootfs. Full UART logs and the current/two previous boot archives remain in the host task outputs.

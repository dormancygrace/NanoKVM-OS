# SG2002 OpenSBI

The platform builds upstream [OpenSBI v1.9](https://github.com/riscv-software-src/opensbi/releases/tag/v1.9), commit `cbf9f6734dd85a982c63e3cb5db7ffe09da839ca`, released 2026-07-01. The source/tree pins are in `../sources.lock`. This replaces the vendor 0.9 MONITOR in the generated FIP.

## Build and memory contract

```sh
platform/build.sh -o build/platform -j8 fetch toolchain uboot opensbi fip
python3 tests/test-nanokvm-fip.py --images build/platform/images
```

The generic platform uses normal upstream `-O2`, no LTO, `rv64imac_zicsr_zifencei` and soft-float `lp64`. C906 has a proprietary draft vector ISA: do not advertise standard V/RVV 1.0 in this M-mode DT. Linux retains its own board DT. The DT selects one application hart and 40 KiB aligned heap; the upstream platform default supplies an 8 KiB stack. Unsupported SUSP, CPPC, DBTR, SSE and MPXY facilities are disabled; other extensions retain upstream probing.

| Item | Address / size |
|---|---|
| MONITOR entry / firmware RO PMP | `0x80000000` / `0x40000` (256 KiB) |
| Firmware RW PMP including stack/heap | `0x80040000` / `0x10000` (64 KiB) |
| Relocated M-mode DT handed to U-Boot | `0x80100000`, maximum 64 KiB including fixup room |
| U-Boot loader header / entry | `0x801fffe0` / `0x80200000` |
| Small-core reservation | none (stock FSBL region `0x8d000000` / `0x200000` is not reserved) |

The ELF manifest enforces the two PMP extents. Default upstream separates RX and RW firmware domains and denies S/U access to both. U-Boot copies these no-map reservations into the Linux DT. Its exact-address/size deduplication keeps the existing 256 KiB board reservation and adds the 64 KiB RW reservation. Do not replace the static Linux node with one overlapping 320/512 KiB node. The SD partition layout can remain unchanged.

The M-mode DT has no static `reserved-memory` children. The stock FIP carries no BLCP/BLCP_2ND image (both sizes are zero), so the FSBL starts no small core (`No C906L image.`) and nothing uses the 2 MiB at `0x8d000000`; the earlier `c906l@8d000000` node in this DT only made U-Boot copy a useless `no-map` reservation into every Linux DT. Without it, and without the `c906l` node the board DTs used to carry, those 2 MiB are available to Linux. `scripts/nanokvm-fip.py` rejects a FIP that carries a BLCP_2ND image, because that image would run in an unreserved region. No OpenSBI or U-Boot code refers to `0x8d000000`.

The FSBL passes `fw_dynamic_info` with U-Boot's entry and S-mode, so use FW_DYNAMIC. Its fixed FDT pointer `0x80011000` points inside a larger modern MONITOR. Patch `0001` adds optional `FW_DYNAMIC_FDT_ADDR`: the embedded DT initializes the generic platform and is relocated to the specified next-argument address. The default behavior of upstream FW_DYNAMIC remains unchanged when the option is absent.

## Vendor adaptation decisions

| Vendor behavior | Decision and evidence |
|---|---|
| C906 cache/CSR setup | Retain the original FSBL. Pinned `fsbl/lib/cpu/riscv/bl1_entrypoint.S` and `bl2_entrypoint.S` initialize mxstatus/mhcr/mcor and PLIC delegation before MONITOR. |
| C906 draft vector VS | Patch `0002` restores SG2002 mstatus[24:23] after generic hart initialization, including warm-hart startup. Vendor 0.9 used `0x01800000`; ratified upstream uses `0x600`. Without the SG2002 handoff, the installed Linux 7.2.9 faults at CSR_VLENB in `riscv_v_setup_vsize`. Standard VS and other SoCs are unchanged; firmware remains scalar. |
| Vendor custom PMU SBI extension `0x09000001` | Use upstream SBI PMU with the SG2002 override in `platform/generic/thead/thead-generic.c`. Current mainline Linux uses standard SBI PMU. |
| SBI 3.0 PMU event discovery | The M-mode DT explicitly maps event 1 to counter 0 (mcycle) and event 2 to counter 2 (minstret). Without this table, EVENT_GET_INFO makes Linux reject cycles/instructions despite registering the PMU. No platform-specific raw/cache events are advertised. |
| Old CLINT binding / access width | Use mainline `thead,c900-clint`: 32-bit MMIO accesses and time CSR instead of an MMIO mtime. |
| PLIC | Use mainline `thead,c900-plic`, 101 sources, M/S contexts. Upstream handles T-Head delegation. |
| Vendor `thead,reset-sample` ebreak reset | Do not carry the sample reset handler; it is not matched by the stock DT. System reset is advertised only if an upstream backend supports it. Linux/U-Boot have the SoC reset driver. |
| T-Head TH1520 TLB erratum | Retain upstream's model-specific selection; do not apply an unrelated workaround to SG2002. |
| DDR, BL2, BLCP and BLCP_2ND | Preserve their bytes and relevant parameter fields from base FIP. No OTP/eFuse changes or new signing flow. |

The source audit compares the pinned vendor OpenSBI tree with upstream v0.9 and v1.9; not every vendor/upstream diff is an SG2002 requirement.

## Validation

`scripts/nanokvm-fip.py` independently checks both parameter CRCs, component CRCs, 512-byte alignment, file overlaps, load/run addresses, first-stage/DDR/small-core preservation and absence of a BLCP_2ND image, exact MONITOR and decompressed U-Boot bytes. The vendor FIP generator includes zero padding in the compressed loader size; the validator accepts only zero trailing padding.

`images/opensbi/build-manifest.json` records the source/tree pin, patches/config/DT hashes, compiler, optimization, binary hash, ELF symbols and PMP extents. `images/fip-manifest.json` records component hashes and checks. Neither manifest establishes successful hardware boot.

Reservation propagation can be checked using unchanged pinned U-Boot functions and libfdt with all five Linux board DTBs (one per board):

```sh
python3 tests/test-opensbi-reservations.py \
  --uboot-source build/platform/uboot/src \
  --firmware-dtb build/platform/images/opensbi/sg2002.dtb \
  --linux-dtbs build/platform/images/dtb
```

This uses calculated PMP fixtures, native scalar cell-decoding adapters and the real U-Boot copy/deduplication code. It verifies exact-range deduplication, the additional RW range, stable repeated copies, detection of an oversized overlapping static range, that neither the firmware DT nor any of the Linux DTBs has a reservation in the small-core range and that the copy adds none (the pre-change firmware DT, or a board DT with a `c906l` node, fails). It does not execute OpenSBI or simulate C906 hardware.

## Hardware status

OpenSBI 1.9 boots on a NanoKVM PCIe from the SD card: SBI 3.0
(implementation 0x10009), timer and PLIC interrupts, both firmware
reservations in Linux, and the SBI PMU cycle and instruction counters. Not
covered: PMU overflow sampling, raw events, multicore IPI/RFENCE and vector
context switching.

Loading a FIP into RAM through the ROM UART loader, and the SD pad setup it needs, are described in [docs/BOOT.md](../../docs/BOOT.md#testing-a-fip-without-writing-it).

## Known limitations

- The draft vector `mstatus.VS` field is restored in the platform
  `final_init` hook. A non-retentive suspend resume goes through
  `sbi_hart_reinit()` and `mstatus_init()` without `final_init`, so VS would
  be cleared again. This path is unreachable today (no idle states in the
  board device trees, SUSP disabled in the defconfig); add the restore to the
  resume path before enabling either.
- The 64 KiB OpenSBI RW reservation reaches Linux only through U-Boot
  copying the firmware `reserved-memory` node; the board `.dtsi` files
  describe only the 256 KiB RX region. Check `/proc/iomem` after updating
  the FIP.

## Upgrade and recovery

The `nanokvm-bootloader-sg2002` package replaces `/boot/fip.bin`; the new FIP
takes effect at the next boot. How the ROM behaves with a missing or damaged `fip.bin`, how to
replace it and how to recover without a card reader are in
[docs/BOOT.md](../../docs/BOOT.md).

When qualifying a new OpenSBI, capture the full boot from FSBL through OpenSBI
and U-Boot to Linux. Check the OpenSBI version, timer/PLIC initialization, both
domain regions, DT address and S-mode handoff; then kernel boot,
reserved-memory warnings, SBI PMU, storage/network, application/video and a
second cold boot.

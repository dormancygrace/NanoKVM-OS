# SG2002 OpenSBI

The platform builds upstream [OpenSBI v1.9](https://github.com/riscv-software-src/opensbi/releases/tag/v1.9), commit `cbf9f6734dd85a982c63e3cb5db7ffe09da839ca`, released 2026-07-01 and verified as the latest stable release on 2026-10-05. The source/tree pins are in `../sources.lock`. This replaces the vendor 0.9 MONITOR in the generated FIP.

## Build and memory contract

```sh
platform/build.sh -o build/platform -j8 fetch toolchain uboot opensbi fip
python3 scripts/test-nanokvm-fip.py --images build/platform/images
```

The generic platform uses normal upstream `-O2`, no LTO, `rv64imac_zicsr_zifencei` and soft-float `lp64`. C906 has a proprietary draft vector ISA: do not advertise standard V/RVV 1.0 in this M-mode DT. Linux retains its own board DT. The DT selects one application hart and 40 KiB aligned heap; the upstream platform default supplies an 8 KiB stack. Unsupported SUSP, CPPC, DBTR, SSE and MPXY facilities are disabled; other extensions retain upstream probing.

| Item | Address / size |
|---|---|
| MONITOR entry / firmware RO PMP | `0x80000000` / `0x40000` (256 KiB) |
| Firmware RW PMP including stack/heap | `0x80040000` / `0x10000` (64 KiB) |
| Relocated M-mode DT handed to U-Boot | `0x80100000`, maximum 64 KiB including fixup room |
| U-Boot loader header / entry | `0x801fffe0` / `0x80200000` |
| Small-core reservation | `0x8d000000` / `0x200000` |

The ELF manifest enforces the two PMP extents. Default upstream separates RX and RW firmware domains and denies S/U access to both. U-Boot copies these no-map reservations into the Linux DT. Its exact-address/size deduplication keeps the existing 256 KiB board reservation and adds the 64 KiB RW reservation. Do not replace the static Linux node with one overlapping 320/512 KiB node. The SD partition layout can remain unchanged.

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

`scripts/nanokvm-fip.py` independently checks both parameter CRCs, component CRCs, 512-byte alignment, file overlaps, load/run addresses, first-stage/DDR/small-core preservation, exact MONITOR and decompressed U-Boot bytes. The vendor FIP generator includes zero padding in the compressed loader size; the validator accepts only zero trailing padding.

`images/opensbi/build-manifest.json` records the source/tree pin, patches/config/DT hashes, compiler, optimization, binary hash, ELF symbols and PMP extents. `images/fip-manifest.json` records component hashes and checks. Neither manifest establishes successful hardware boot.

Reservation propagation can be checked using unchanged pinned U-Boot functions and libfdt with all ten Linux board DTBs:

```sh
python3 scripts/test-opensbi-reservations.py \
  --uboot-source build/platform/uboot/src \
  --firmware-dtb build/platform/images/opensbi/sg2002.dtb \
  --linux-dtbs build/platform/images/dtb
```

This uses calculated PMP fixtures, native scalar cell-decoding adapters and the real U-Boot copy/deduplication code. It verifies exact-range deduplication, the additional RW range, stable repeated copies and detection of an oversized overlapping static range. It does not execute OpenSBI or simulate C906 hardware.

## SG2002 RAM hardware result (2026-10-05)

FIP SHA-256 `76e50ecc3fab0da16f2ab750e6f79a67ec2dedeac9397c5f437343e6008e3692` passed ROM UART RAM loading on Sipeed NanoKVM PCIe. OpenSBI 1.9 handed off to the unchanged U-Boot and installed Linux 7.2.9 with normal bootcmd/bootargs. Runtime SBI version is 3.0, implementation version 0x10009. Linux imported both 256/64 KiB firmware reservations and the unchanged 2 MiB small-core reservation.

Timer and PLIC device interrupts progress, both application services start, and Chrome Main displays an advancing HDMI stopwatch. A bounded perf_event_open counting probe opened cycles and instructions together; both values increased (108515 to 921149 cycles, 71670 to 774880 instructions). No Oops/panic/overlap diagnostics appeared. This checks counting, not PMU overflow sampling, raw events, multicore IPI/RFENCE or vector context switching.

The SD FIP was never replaced. Candidate cold boot from SD and persistent installation remain untested. Deterministic build manifests retain their build-time hardware_validation=pending field; a separate hardware report links measured results to the exact artifact hash.

ROM UART bypasses SD ROM pad initialization. RAM U-Boot needs volatile SD0 settings before mmc rescan: SD0_PWR_EN mux 0x03001038=0, SD power 0x030001f4=0x1209, and CMD/D0-D3 pads 0x03001a04..0x03001a14=0x44. These are register changes, not SD writes. The ROM-only host adapter uses an empty partition/program list and excludes the persistent programming stage. Loading the original host FIP into RAM and booting the unchanged SD Linux was verified first. This does not by itself prove that a damaged SD FIP can be repaired.

## Upgrade and recovery

An application/APK update does not replace `/boot/fip.bin`. FIP takes effect at the next boot.

Before hardware work, obtain an exclusive maintenance window and explicit handback from any runtime owner. On the host, retain the exact installed FIP and its verified hash, the candidate and manifests, and a UART log. Confirm physical power control and the ability to remove/read/write the SD card. UART alone does not establish a recovery path. Do not put precautionary rollback copies on the device or SD card.

With UART attached at 115200 8N1 and DTR/RTS disabled, stage the candidate FIP, verify its SHA-256 on the target, and replace the boot-partition FIP using a same-filesystem temporary file and rename. Flush writes before reboot. Capture the full boot from FSBL through OpenSBI and U-Boot to Linux. Check the OpenSBI version, timer/PLIC initialization, both domain regions, DT address and S-mode handoff; then check kernel boot, reserved-memory warnings, SBI PMU, storage/network, application/video and a second cold boot. Retain the current and two previous boot-log archives.

If boot fails before SSH is available, power off, remove the SD card and restore the host's original FIP to the FAT boot partition using an external reader. Verify the restored hash, eject safely and power on with UART capture. If the card cannot be accessed, stop and obtain physical recovery support. Do not attempt OTP/eFuse changes or an unverified UART downloader.

The assembled SD image is for a separate test card or a deliberately approved reimage; writing it erases the destination card's partition table and files. A FIP-only upgrade preserves the installed OS and user data.

# C906 instruction profile

This profile targets the SG2002 C906 used by NanoKVM OS. It is not a generic
RISC-V or standard RVV 1.0 target. `profile.json` records the kernel and user
ISA selections. Buildroot and the native component builders use the user
profile; Linux C code excludes automatic floating-point/vector instructions.
The original kernel Zacas/Zabha assembler support is retained for runtime-gated
alternatives, not as a claim that C906 implements those extensions.

## Evidence required

1. Compile from clean target outputs, including libc and compiler runtimes.
2. Run `scripts/audit-riscv-instructions.py` on shipped ELF files. The report
   records binary SHA-256, individual machine encodings, symbols and addresses.
   ELF ISA attributes or compiler flags alone are insufficient.
3. Compare the installed firmware with the previous build on the same device.
   Static instruction counts do not measure speed or execution frequency.

The isolated device probe executes ten extension families: XTheadBa, Bb, Bs,
CondMov, Mac, MemIdx, MemPair, FMemIdx, Sync and Vector. It catches process
SIGILL/SIGSEGV/SIGBUS. CMO already has kernel support; interrupt-only operations
and RV32-only floating-point move operations are not user-process probes.
No protected system registers are accessed by these probes.

## Compiler limits

GCC 16.2 supports explicit XTheadVector assembly/intrinsics but disables its
ordinary loop auto-vectorization in `riscv_preferred_simd_mode` and
`riscv_autovectorize_vector_modes` when `TARGET_XTHEADVECTOR` is true. An O3
float-add loop remains scalar even with the extension enabled. This must not be
reported as automatic vectorization.

That loop restriction does not exclude vector lowering of compiler built-ins.
The later `-O2` cryptodev comparison binary contains compiler-generated
XTheadVector string comparison and buffer-copy sequences in `main`; those paths
executed successfully on C906 on both kernels. See the
[disassembly and runtime evidence](../../../docs/VALIDATION.md).
This is separate from loop auto-vectorization and does not reclassify every
ambiguous vector mnemonic in a mixed Go/C binary.

Go targets `GORISCV64=rva20u64`; its backend has no T-Head target. CGO-generated
C and native libraries use GCC and the new profile. The NanoKVM Go runtime
patches remain selected explicitly through GOROOT. Standard RVV assembly in Go
is guarded by CPU feature checks and is not the C906 legacy vector path.
**RVV and XTheadVector machine encodings overlap:** a `th.v` mnemonic decoded
from a mixed Go/C binary does not by itself establish a T-Head implementation.
The audit therefore reports those sites separately from scalar custom opcodes.

Pinned closed ISP/3A algorithm objects are relinked, not recompiled. Their
hashes and provenance remain in `firmware/mpi/vendor-isp-objects.json`.
JavaScript runs in the user's browser and has no C906 compiler ISA flags.
OpenSBI and bootloader firmware are outside this optimization change.

## Kernel inline assembly

XTheadMemIdx extends the addresses accepted by GCC's `m` memory constraint.
Linux's ordinary `lb/lh/lw/ld` and store templates cannot encode those indexed
addresses. Patch 0018 changes the uaccess operands to RISC-V constraint `A`,
which requires an address in a general-purpose register. The memory operand,
faulting instruction and exception-table semantics are retained.

Sources: [GCC constraints](https://gcc.gnu.org/onlinedocs/gcc/Machine-Constraints.html),
[GCC's XTheadMemIdx inline-assembly issue](https://gcc.gnu.org/pipermail/gcc-patches/2023-December/639406.html),
[T-Head extension specifications](https://github.com/XUANTIE-RV/thead-extension-spec).

## Device results

The [2026-09-10 qualification](../../../docs/VALIDATION.md) records actual boot, shipped instruction hashes, paired software speeds, DMA counter proof and the limits of browser qualification.

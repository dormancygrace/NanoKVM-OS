# C906 instruction profile

This profile targets the SG2002 C906 used by NanoKVM OS. It is not a generic
RISC-V or standard RVV 1.0 target. The ISA selections and compiler flags come
from `platform/cpu-profile.json` through `scripts/nanokvm_cpu_profile.py`;
Linux C code excludes automatic floating-point/vector instructions.
The original kernel Zacas/Zabha assembler support is retained for runtime-gated
alternatives, not as a claim that C906 implements those extensions.

## Evidence required

1. Compile from clean target outputs, including libc and compiler runtimes.
2. Compare the installed firmware with the previous build on the same device.
   Static instruction counts do not measure speed or execution frequency.

## Compiler limits

GCC 16.2 supports explicit XTheadVector assembly/intrinsics but disables its
ordinary loop auto-vectorization in `riscv_preferred_simd_mode` and
`riscv_autovectorize_vector_modes` when `TARGET_XTHEADVECTOR` is true. An O3
float-add loop remains scalar even with the extension enabled. This must not be
reported as automatic vectorization.

That loop restriction does not exclude vector lowering of compiler built-ins.
The later `-O2` cryptodev comparison binary contains compiler-generated
XTheadVector string comparison and buffer-copy sequences in `main`; those paths
executed successfully on C906 on both kernels.
This is separate from loop auto-vectorization and does not reclassify every
ambiguous vector mnemonic in a mixed Go/C binary.

Go targets `GORISCV64=rva20u64`; its backend has no T-Head target. CGO-generated
C and native libraries use GCC and the new profile. The NanoKVM Go runtime
patches remain selected explicitly through GOROOT. Standard RVV assembly in Go
is guarded by CPU feature checks and is not the C906 legacy vector path.
**RVV and XTheadVector machine encodings overlap:** a `th.v` mnemonic decoded
from a mixed Go/C binary does not by itself establish a T-Head implementation.

The closed ISP/3A algorithm objects of the SDK are not used: the HDMI
receiver's YUV bypasses the ISP's image processing.
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

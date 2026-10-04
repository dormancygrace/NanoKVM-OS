# C906 legacy vector copy qualifier

This optional GPL module tests explicit RVV 0.7.1 copy/fill assembly on RV64
T-Head C906 with VLEN=128. It does not hook normal copy paths. The associated
kernel patch is disabled by default and requires separate integration testing.

## Build

Use the exact source, configured build, compiler and additional flags of the
running kernel. The build must already contain Module.symvers. A toolchain
with GNU XTheadVector assembler support is required.

    KCFLAGS="matching kernel flags" ./build.sh KERNEL_SOURCE KERNEL_BUILD CROSS_PREFIX

The module C remains scalar O3. The static client uses scalar O2 and explicit
legacy assembly. Compiler vectorization is disabled in both.

Copy-only m1/m2/m4/m8 variants can be built into a new external directory:

    KCFLAGS="matching kernel flags" python3 build-variants.py SOURCE BUILD CROSS_PREFIX OUTPUT

Fill remains m8 in these variants. Run identical correctness/context gates before
comparing each module. Swap only your temporary module in an exclusive window.

## Run in an exclusive hardware test window

Check the module ABI against the running kernel. Load c906_vector_probe.ko
without force options; its root-only misc device is /dev/c906-vector-probe.

    ./client /dev/c906-vector-probe validate
    ./client /dev/c906-vector-probe context
    ./client /dev/c906-vector-probe bench > bulk.csv
    ./client /dev/c906-vector-probe bench-active > active.csv
    ./client /dev/c906-vector-probe bench-align > alignment.csv

Stop at the first failure. Unload c906_vector_probe after testing.

validate checks scalar and vector byte results, misalignment, guards, partial
page faults and copy_from_user zero-padding. Fault checks use returned source
progress because scalar loads may stop before the last accessible byte. Vector
store faults must report every completed byte precisely; conservative scalar
progress counts are reported separately. context checks all 32 vector
registers and VL/VTYPE/VSTART through asynchronous signals/scheduling, and
VXRM/VXSAT/FCSR across kernel vector entries. Syscall entry deliberately
discards vector registers and VL/VTYPE/VSTART, so their post-ioctl values
are not compared. The signal handler clobbers vector state. Tests read actual CSR behavior
and does not assume FCSR and vector controls are independent.

bench alternates scalar/vector order for five repetitions and amortizes syscall
setup over repeated copies. bench-active seeds userspace vector state before
every individual ioctl and checks FCSR/control preservation; each row sums
16 separate calls. This captures active-vector entry costs hidden by bulk repetitions. The timing
ecalls also discard vector register values while retaining active task state.

bench-align runs the same separate-entry measurements for both usercopy
directions across all 64 source/destination alignments modulo eight, at four
sizes around the anticipated threshold. It also tests equal alignments.

CSV operations: 0=from_user, 1=to_user, 2=kernel copy, 3=kernel fill. The kernel
loop elapsed_ns includes vector begin/end and common scheduling points.
call_wall_ns and call_cpu_ns cover the complete ioctl, including buffer
initialization; they are not isolated copy-engine CPU times. Byte validation
runs outside those timings. Fault rows are never benchmarked.

    python3 analyze.py active.csv active-summary.json

The analyzer requires complete, equal-work scalar/vector pairs and reports
paired medians, ranges and the number of faster vector samples. Choose a
threshold only after correctness, repeated active-entry timings and relevant
application-level measurements. Page copy/fill probes qualify the assembly
primitive; enabling real page operations also requires proper context gates.

Keep binaries, hardware manifests, raw CSVs and derived results outside a public
source tree. This directory contains only the reproducible source tools.

Syscall vector-state contract: https://github.com/riscvarchive/riscv-v-spec/blob/master/calling-convention.adoc

The scalar usercopy control calls the running kernel exported raw-copy routine.
If that kernel already enables the experimental backend, mode0 can itself use
vectors. Compare mode0 across baseline/candidate/baseline boots to measure the
integrated kernel; perform direct engine comparisons on the original kernel.
Fault probes include small and above-threshold copies to exercise integration.

## Checksum and LZ4 screening before a kernel build

build-screen.py SOURCE BUILD CROSS_PREFIX OUTPUT builds a separate module
against the existing matching prepared kernel, with KCFLAGS inherited
from the environment. OUTPUT must be a fresh private directory outside the
repository. It copies the supplied kernel's LZ4 source there, retains its
license, and namespaces two independent candidate implementations:

- Variant 0: the running kernel's exported checksum and LZ4 functions; the
  supplied kernel's scalar common-prefix helper.
- Variant 1: scalar LZ4 with th.rev plus th.ff1 for common-byte counting.
- Variant 2: legacy RVV checksum and LZ4 with a vector common-prefix helper
  after a 64-byte scalar match. Vector context is taken once per full function.
- Variant 3: a real hard-IRQ timer callback requesting the vector path, which
  must take the scalar fallback. This check is limited to 4096 bytes for compression/prefix functions
  and 16384 bytes for checksums, including the candidate's eligible range.
- Variant 4: LZ4 takes vector context lazily, after its first 64-byte match.
  Other public LZ4 APIs in this generated copy keep scalar context.
  Limited-output tests cover zram's page-sized destination and errors after
  vector entry, with the original output and vector-state cleanup contracts.

The module exposes /dev/c906-screen-probe only while loaded. It does not hook
or replace production functions. With exclusive access to the test hardware,
load OUTPUT/c906_screen_probe.ko and use the generated screen-client:

    screen-client /dev/c906-screen-probe smoke
    screen-client /dev/c906-screen-probe validate
    screen-client /dev/c906-screen-probe context
    screen-client /dev/c906-screen-probe bench > private-bulk.csv
    screen-client /dev/c906-screen-probe bench-active > private-active.csv
    screen-client /dev/c906-screen-probe validate-lazy
    screen-client /dev/c906-screen-probe validate-limited
    screen-client /dev/c906-screen-probe bench-lazy > private-lazy.csv
    python3 analyze-screen.py private-active.csv private-summary.json

Validation requires exact checksum results, byte-identical LZ4 output compared
with the running kernel, successful decoding with its original decoder, intact
buffer guards, and exact common-prefix results across byte boundaries, offsets,
and differing bits. Context checks cover asynchronous restoration of all 32
vector registers and control registers, plus FCSR/VXRM/VXSAT preservation across
ioctls. Ordinary syscalls do not promise preservation of vector register values.

bench amortizes repeated calls within an ioctl. bench-active uses eight
separate one-call ioctls per row, seeding active user vector state before each
entry and checking FCSR/VXRM/VXSAT afterwards. Function timings include kernel
vector entry/exit and common scheduling points; fixture preparation occurs
before the timer. Alternate ordering and paired repetitions are required.

Unload the module and remove its temporary RAM files after each coordinated
window. Keep matching build metadata, raw measurements, and device state
snapshots private. Qualifying a helper alone does not establish a gain for the
whole compressor, zram, the network stack, or NanoKVM.

## Bounded passive profiling

Compile sample-kernel.c as an ordinary userspace program. It samples kernel
instruction pointers with software CPU-clock events at 199 Hz for a requested
1–180 seconds, reads the perf mmap ring, and rejects lost records or an empty
sample set. Kernel perf permission is required. It does not change kernel or
system configuration.

    sample-kernel 60 > private-samples.csv
    cat /proc/kallsyms > private-symbols.txt
    python3 analyze-samples.py private-samples.csv private-symbols.txt private-profile.json

Use symbols from the same boot and loaded modules. The analyzer omits RISC-V
local labels so they do not obscure enclosing function names. The report
includes idle samples and has no call-chain attribution. A quiet-system sample
cannot establish that a function is irrelevant under load. Never commit raw
kernel addresses, measurements, or stand configuration.


## Consolidated kernel candidate

The optional kernel patch contains two independently gated backends:
legacy user copies for differing machine-word alignment at the configured
copy threshold, and Internet checksums at the configured checksum threshold.
Both options default to disabled. Neither changes compiler autovectorization.
The ordinary scalar implementation handles smaller buffers and contexts where
SIMD is unavailable.

The checksum assembly uses aligned halfword loads, explicit odd head/tail
handling, and unsigned widening reductions in bounded batches. It returns
the existing do_csum result and preserves csum_partial's accumulation API.
The kernel wrapper owns vector context entry and exit.

LZ4, internal copy/fill, and page primitives remain screening tools in this
directory. Their presence does not imply a production replacement. In
particular, correctness qualification is separate from accepting a performance
change across the component's relevant inputs.

Finish every selected function's module qualification and complete paired
timing before the single consolidated kernel build. The resulting kernel
still requires integrated hardware baseline/candidate/baseline validation.

## Integrated kernel comparison

After the single consolidated build, use the same qualified module and client
binaries on the original kernel (A1), consolidated candidate (B), and restored
original kernel (A2). The clients' bench-kernel mode calls only the running
kernel's ordinary APIs: mode 0 does not bypass the kernel wrapper to invoke a
module vector helper. The module vector_calls field counts module requests,
so zero in this mode does not mean that the kernel backend was unused.

    client /dev/c906-vector-probe bench-kernel > private-copy-A1.csv
    screen-client /dev/c906-screen-probe bench-kernel > private-csum-A1.csv

Repeat into distinct B and A2 files within a coordinated exclusive window.
Run validation and context tests in each phase and restore the original kernel
and stand state before releasing access. Use identical modules, clients,
compiler profiles, application state and other loaded components. Verify
phase boot identity and kernel image identity separately from the CSV files.

    python3 analyze-kernel-ab.py copy private-copy-A1.csv private-copy-B.csv private-copy-A2.csv private-copy-report.json
    python3 analyze-kernel-ab.py csum private-csum-A1.csv private-csum-B.csv private-csum-A2.csv private-csum-report.json

The analyzer rejects duplicate or incomplete workload/repetition sets and
mixed modes or iteration counts. It reports each baseline separately,
baseline drift and the conservative reduction supported by both baselines.
Matching repetition numbers across separate boots are a descriptive check,
not simultaneous randomized pairs or a confidence interval.

The kernel timer includes wrapper/context work and a common scheduling point.
Copy ioctl wall/CPU time also includes fixture preparation; these are not pure
copy timings. Inputs and generated reports are private experiment artifacts.
A function microbenchmark does not establish whole-system throughput or FPS.

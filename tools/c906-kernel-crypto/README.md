# C906 kernel crypto and checksum qualifier

Source-only tools for SG2002/C906, VLEN128/ELEN32, legacy RVV 0.7.1.
This module does not replace production functions. Kernel C remains scalar O3;
only explicit legacy assembly uses vectors. The public tree contains no firmware,
test binaries, stand access details or measurements.

Candidates:

- ChaCha20/12: up to four independent blocks, rotations via shifts and OR,
  aligned scratch output and byte-vector XOR for arbitrary alignment.
- Full kernel ChaCha20-Poly1305 scatter-gather library, copied and namespaced
  from the supplied kernel at build time. Poly1305 remains its existing backend.
- Reflected CRC32/CRC32C: kernel baseline, scalar slicing8, or 2..16 parallel
  legacy vector streams with exact polynomial combination and scalar tails.
- Kernel-RAM copy plus Internet checksum in one pass. This does not yet qualify
  faulting userspace copy APIs.

Build in a fresh private output directory against the prepared **running** kernel:

    KCFLAGS="matching kernel flags" ./build.py SOURCE BUILD CROSS_PREFIX OUTPUT

Requires the existing sibling c906-vector-copy tools for their qualified vector
state helpers. The build records the source hashes and exact flags privately.
The copied AEAD source retains its original license. No new kernel build is needed.

The host check validates scalar CRC and polynomial combination with an independent
bitwise oracle, address/undefined sanitizers and known CRC vectors. It emulates
the parallel CRC streams and does not execute or qualify RVV instructions:

    ./native-crc-test.py

Acquire an exclusive shared-stand window before loading the matching temporary
module. Use RAM storage, unload afterwards, and remove temporary files:

    modprobe libchacha20poly1305
    insmod OUTPUT/c906_crypto_probe.ko
    OUTPUT/crypto-client /dev/c906-crypto-probe smoke
    OUTPUT/crypto-client /dev/c906-crypto-probe validate
    OUTPUT/crypto-client /dev/c906-crypto-probe context
    OUTPUT/crypto-client /dev/c906-crypto-probe bench > PRIVATE.csv
    rmmod c906_crypto_probe

Correctness checks include the RFC8439 block vector, CRC known vectors and an
independent CRC bitwise oracle, tails, offsets, inplace operation, counter wrap,
64-bit AEAD nonces, fragmented SG, rejected modified tags, source/destination
guards, and hard-IRQ scalar fallbacks. SG encryption and decryption are compared
with a separate scalar ChaCha library copy, including on an integrated vector
kernel. A successful compile is not hardware qualification.

Timings include dispatch and vector context entry/exit; buffer reset, SG setup
and output validation occur outside each timed call. Full AEAD rows distinguish
encryption and decryption; bad tags are separate correctness cases. Each benchmark row
uses eight separate ioctls with active userspace vectors and checks FCSR/VXRM/VXSAT.
Five repetitions alternate variant order. Sizes include MTU-sized packets.
The `thresholds` command instead uses 32 separate active-vector ioctls and seven
repetitions, comparing ChaCha thresholds 256, 512, 1024, 2048 and 4096 bytes.
Analyze private CSV files with `analyze.py CSV JSON`, adding `--thresholds`
for that sweep. The analyzer rejects incomplete variant/repetition matrices.
The `chunks` command checks fused-copy accumulation across 64 KiB boundaries,
up to 256 KiB. The `kernel` command measures only real production library
calls (including the exported SG AEAD functions) with 32 active-vector ioctls
and seven repetitions. Build the probe against each tested kernel so its
headers select the actual `csum_partial_copy_nocheck` implementation.
Compare complete before/after matrices using `compare-kernels.py OLD NEW JSON`.
Use `kernel-context` on the integrated build for every VXRM/VXSAT combination
through actual production functions at 16 KiB, above all selected thresholds.
`probe_vector_entries` counts only the diagnostic module's own vector calls;
it does not instrument the production backends.
Signal context tests check all 32 vector registers and VL/VTYPE/VSTART asynchronously;
ordinary syscalls need not preserve those registers. No separate-CSR assumption is made.

All functions must pass qualification and demonstrate relevant whole-function
gains before inclusion in a single combined kernel candidate.

The opt-in `platform/kernel/0037-c906-vector-crypto-crc-copy.patch` applies after
`0036-c906-vector-kernel-functions.patch`. Its three options default off:
`RISCV_ISA_XTHEADVECTOR_CHACHA`, `RISCV_ISA_XTHEADVECTOR_CRC` and
`RISCV_ISA_XTHEADVECTOR_COPY_CSUM`. Enable them explicitly for qualification.
The `selected.config` fragment records the combined qualification configuration,
including the earlier usercopy and checksum options. Merge it into the existing
NanoKVM kernel configuration after applying both patches.
ChaCha starts at 2048 bytes per library call; CRC uses slicing8 from 1024 bytes
and vectors from 8192; fused RAM copy/checksum starts at 4096 bytes.
Short buffers and SIMD-forbidden contexts retain scalar paths. CRC uses its
original backend until its tables have initialized. HChaCha and Poly1305 keep
their existing backends. Rebuild and deploy matching modules along with the
kernel, including `libchacha.ko`; this change is not confined to the boot image.

`integrate.py KERNEL PATCH` regenerates the patch from the qualification sources
and modifies a fresh kernel tree with patch 0036 already applied. Optional
threshold arguments allow controlled tuning. Keep GCC kernel C at O3 with
compiler vectorization disabled; the vector routines are explicit legacy
assembly. Do not add RVV 1.0 or vector-crypto requirements to the C906 build.

The context correction in patch 0038 must accompany these preemptive T-Head
vector functions. It selects the proper legacy VS mask in the status predicate
and clears legacy VS at trap entry using a vendor extension alternative.
Other processors retain the standard entry mask. Correcting only the predicate
does not qualify the preemptive path. Check both actual source contracts before
building, then qualify the complete kernel and matched modules on hardware:

    ./check-vector-status.py SOURCE
    ./check-trap-mask.py SOURCE

The host checks cover both architectures' state contracts; they do not execute
RISC-V instructions or replace signal, fault, IRQ and production-function tests.

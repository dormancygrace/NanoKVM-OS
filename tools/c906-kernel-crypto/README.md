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
with the existing kernel library. A successful compile is not hardware qualification.

Timings include dispatch and vector context entry/exit; buffer reset, SG setup
and output validation occur outside each timed call. Full AEAD rows measure
encryption; decryption is separately checked outside timing. Each benchmark row
uses eight separate ioctls with active userspace vectors and checks FCSR/VXRM/VXSAT.
Five repetitions alternate variant order. Sizes include MTU-sized packets.
Signal context tests check all 32 vector registers and VL/VTYPE/VSTART asynchronously;
ordinary syscalls need not preserve those registers. No separate-CSR assumption is made.

All functions must pass qualification and demonstrate relevant whole-function
gains before inclusion in a single combined kernel candidate. Thresholds and
kernel integration are intentionally not selected by this diagnostic module.

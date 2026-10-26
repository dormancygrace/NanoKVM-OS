# OpenSSL SG2002 SHA-224/256 qualification

`0003-sha256-dma.patch` offloads complete 4 KiB SHA-256 compression chunks via
the common direct ioctl transport. SHA-224 shares this compression function,
with its own initial state and output length. OpenSSL retains streaming state,
message length, padding and final output. Short blocks, missing hardware and
unfinished chunks continue through the original software implementation.
The FIPS module is excluded. No ENGINE or cryptodev path is used.

The initial 4096-byte minimum is a conservative **candidate**, pending native
measurement including device-open/capability costs. A request failure after
successful chunks commits only their chaining state and hashes the remainder
in software. No hash output buffer or extra persistent digest state is added.

Validation completed:

- Full OpenSSL 3.5.9 RISC-V cross-build.
- `python3 firmware/crypto/experimental/openssl-sha256/test-fallback.py`:
  1920 host ASan/UBSan cases for SHA-224/256, every input offset, hardware
  absent/first/later failures, exact software continuation and unchanged
  OpenSSL buffering/length fields.
- `verify.c`: 864 EVP streaming/context-copy/unaligned cases over SHA-1,
  SHA-224 and SHA-256. Expected digest fingerprint `768fd5312ca481be`, independently
  calculated with Python hashlib and confirmed by host OpenSSL. This helper
  is ready for native baseline/candidate runs; it has not yet qualified DMA.
- `bench.c NAME SECONDS`: process CPU, elapsed time and peak RSS across
  64–16384 bytes, particularly around the hardware crossover.

Physical hardware correctness/counters, thresholds, RAM, concurrent video and
comparison with our separate Go scalar SHA implementation remain pending.

The same algorithm patch now includes a GNU ABI adapter of the BSD Go C906 SHA-256 compression core. XTheadBb builds use it below the 4 KiB DMA threshold and for software tails; other targets and FIPS keep upstream code. The generator preserves the used callee-saved registers and wipes the 64-byte schedule (112-byte frame).

Physical C906, matched O3 builds, three rotated trials with DMA disabled: the first adapter passed all 864 SHA1/224/256 EVP cases. SHA-256 improved from 8.40 to 9.29 MiB/CPU-s at 64 bytes, 18.30 to 20.70 at 1 KiB, and 19.80 to 22.42 at 16 KiB. The final adapter removes saves of unused registers; final native regression remains pending. At 4 KiB the separate DMA candidate measured 42.80 MiB/CPU-s versus 19.64 upstream. SHA-224 shares the same compression implementation.

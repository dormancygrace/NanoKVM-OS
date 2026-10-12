# Scalar GHASH for the C906 in the kernel — 2026-10-12

Linux `7.2.10-nanokvm-os-r3` adds a C906 backend to `lib/crypto`'s GHASH
(`platform/kernel/0041-crypto-riscv-c906-ghash.patch`). Every in-kernel GHASH
user goes through it: the `gcm` template, so `gcm(aes)` and `rfc4106(gcm(aes))`
for IPsec ESP, kTLS with AES-GCM, and MACsec. On the SG2002 `gcm(aes)` is
`gcm_base(sg2002-aes-ctr,ghash-lib)`: the CryptoDMA engine does the AES-CTR and
the CPU the GHASH, and the GHASH is the slower half.

## Why the generic code is slow on the C906

Measured with probe modules on the stand:

| instruction | cost on the C906 |
| --- | --- |
| `mul`/`mulhu`, an operand with bits above 31 | about 4.2 cycles each, not pipelined |
| `mul`, both operands below 2^32 | 1 cycle each, pipelined, latency about 3 |
| `mulw` | 1 cycle |

The generic GHASH (`polyval_mul_generic()`) multiplies 64-bit operands, so it
pays the slow case. Its timing therefore also depends on whether an operand's
high word is zero. That is a formal leak of key and data bits on this core,
with little practical reach.

## What changes

- `lib/crypto/riscv/ghash-riscv64-c906.S`, generated and self-tested by
  `firmware/cpu/go-crypto-c906/gen_ghash_linux.py`. The 128x128 carryless
  product is nine 32x32 ones: Karatsuba on 64-bit halves, then on 32-bit
  halves. Each one is sixteen integer multiplies of operands with their bits
  four apart ("holes", as in BearSSL's ctmul32). Every multiply operand is
  below 2^32, so every multiply is the fast case, and the timing does not
  depend on the data. The reduction is that of `polyval_mul_generic()`. The
  483 instructions of a block are list-scheduled for the C906's latencies.
  Byte swaps use XTheadBb `th.rev`. The four stack slots holding partial
  products are wiped on return.
- `struct ghash_key` gains `c906_kp[40]` with `RISCV_ISA_XTHEADVECTOR`: the
  key's 32-bit operands, already split into hole classes, filled by
  `ghash_preparekey()`.
- `lib/crypto/riscv/gf128hash.h` enables the backend at boot when
  `has_xtheadvector()`. XTheadVector stands for a T-Head core (C906, C910,
  C920), and all of them implement XTheadBb. The code uses no vector state,
  so it also runs in contexts where `may_use_simd()` is false. Zvkg keeps its
  previous conditions and takes precedence; its object is now built only
  under them.
- `CRYPTO_LIB_GF128HASH_ARCH` defaults to y with `RISCV_ISA_XTHEADVECTOR`.
  `platform/kernel/config` gains that line, and the release becomes `r3`
  (`CONFIG_LOCALVERSION`, `build.sh`).

## Checks

The generator executes the whole generated function: prologue, block loop and
epilogue. It runs 200 random keys and messages, including zero blocks and
misaligned data, against a bit-serial GHASH. It also checks that every
multiply operand is below 2^32, that `sp`, `ra` and `s0`-`s11` come back
unchanged, and that the product slots are wiped. CI runs it with `--check`
against the patch.

`firmware/cpu/go-crypto-c906/linux-test/` builds `ghash_c906.ko` against the
running kernel. It carries the same assembly and key preparation. On the
stand (SG2002 C906, `7.2.10-nanokvm-os-r1`), loading it ran 20,000 random
cases against a bit-serial GHASH. Lengths were 0 to 1024 bytes at offsets 0
to 15, with all-ones keys and data among them. There were 0 failures for the
assembly on whole blocks, and 0 for `ghash_update()` in two updates with a
partial block.

| bytes | library GHASH, r1 (MB/s) | C906 assembly (MB/s) | `gcm(aes)`, r1 (MB/s) |
| ---: | ---: | ---: | ---: |
| 64 | 15 | 23 | 2 |
| 512 | 15 | 27 | 8 |
| 1420 | 15 | 29 | 9 |
| 16384 | 15 | 28 | 11 |

Wall time, 300 ms per cell. Earlier runs the same day gave 26-30 MB/s for the
assembly and 13-16 for the library. Six alternative schedules of the same
instructions all measured 27-30 MB/s, so the block is limited by its
instruction count, at about 0.8 instructions per cycle.

## `gcm(aes)` through the patched library

The r3 kernel was not booted. On the r1 kernel, `libgf128hash.ko` and `gcm.ko`
are modules, and `gcm` had no users. They were built from the patched sources
against the r1 tree, with `CRYPTO_LIB_GF128HASH_ARCH` and the patched
`struct ghash_key`, and swapped in for one run of `ghash_c906.ko`, built the
same way. The original modules were loaded back afterwards.

- 20,000 random cases: 0 failures for the assembly, and 0 for
  `ghash_update()`. The library now runs the C906 path, including the partial
  block through `ghash_mul()`.
- `gcm(aes)` is `gcm_base(sg2002-aes-ctr,ghash-lib)`: AES-CTR in the CryptoDMA
  engine, GHASH on the CPU.

| bytes | library GHASH, r1 (MB/s) | library GHASH, patched (MB/s) | `gcm(aes)`, r1 (MB/s) | `gcm(aes)`, patched (MB/s) |
| ---: | ---: | ---: | ---: | ---: |
| 64 | 15 | 19 | 2 | 1 |
| 512 | 15 | 27 | 8 | 10 |
| 1420 | 15 | 27 | 9 | 14 |
| 16384 | 15 | 28 | 11 | 16 |

The r1 columns come from the run above and the patched ones from this run,
300 and 500 ms per cell. For 64 bytes, `gcm(aes)` is dominated by the request
setup in the engine, not by the GHASH. A test module built against the r1
headers must not be loaded against the patched library: `struct ghash_key`
grows from 16 to 352 bytes.

## Not covered

- The r3 Image was not booted on the stand. Its `libgf128hash.ko` and
  `gcm.ko` are built from the same sources as the modules measured above.
- A vector version was not built. Lanes holding independent blocks with key
  powers H^4 to H^1 might add a factor of about 1.3 to 1.5. The C906's vector
  unit multiplies 32-bit lanes quickly, but 64-bit lanes slowly.

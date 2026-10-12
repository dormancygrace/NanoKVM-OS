# GHASH for the C906 in the kernel — 2026-10-12

Linux `7.2.10-nanokvm-os-r3` adds a C906 backend to `lib/crypto`'s GHASH
(`platform/kernel/0041-crypto-riscv-c906-ghash.patch`). Every in-kernel GHASH
user goes through it: the `gcm` template, so `gcm(aes)` and `rfc4106(gcm(aes))`
for IPsec ESP, kTLS with AES-GCM, and MACsec. On the SG2002 `gcm(aes)` is
`gcm_base(sg2002-aes-ctr,ghash-lib)`: the CryptoDMA engine does the AES-CTR and
the CPU the GHASH, and the GHASH is the slower half.

The backend has two functions. A scalar one hashes any number of blocks. A
vector one hashes groups of five blocks: four in XTheadVector lanes and the
fifth in scalar registers, the two instruction streams interleaved.

| bytes | library GHASH, generic (MB/s) | scalar (MB/s) | vector, five-block groups (MB/s) |
| ---: | ---: | ---: | ---: |
| 80 | 13 | 24 | 32 |
| 512 | 14 | 27 | 39 |
| 1408 | 14 | 27 | 41 |
| 16384 | 14 | 27 | 42 |

Stand, r1 kernel, the functions loaded as a test module, average over 300 ms
per cell, with the NanoKVM server running (about 16 % system time). The best
of 15 runs of 200 groups gives 1,506 ns per group of five blocks for the
vector function, about 1,660 cycles or 53 MB/s.

## Why the generic code is slow on the C906

Measured with probe modules on the stand:

| instruction | cost on the C906 |
| --- | --- |
| `mul`/`mulhu`, an operand with bits above 31 | about 4.2 cycles each, not pipelined |
| `mul`, both operands below 2^32 | 1 cycle each, pipelined, latency about 3 |
| `mulw` | 1 cycle |
| XTheadVector e32 `vmul`, `vmulhu`, `vxor`, `vand`, `vsrl` | 2 cycles each (1.82 ns) |
| one scalar instruction after each vector one | adds 0.1 ns; a second one adds a cycle |

Below 2^32 the value does not matter. Eight operand pairs were timed: 0, 1,
3 x 5, sparse (0x11111111 x 0x1111, 0x88888888 squared), 0x80000000 squared,
all ones, and 0x7fffffff x 0x55555555. Each gave 1.03-1.06 ns per
independent `mul`, and 5.61-5.62 ns per step of a dependent
`mul`/`and`/`or` chain with constant operands. A pair with 64-bit operands
gave 3.78 and 8.39 ns.

The generic GHASH (`polyval_mul_generic()`) multiplies 64-bit operands, so it
pays the slow case. Its timing therefore also depends on whether an operand's
high word is zero. That is a formal leak of key and data bits on this core,
with little practical reach.

## What changes

- `lib/crypto/riscv/ghash-riscv64-c906.S`, generated and self-tested by
  `firmware/cpu/go-crypto-c906/gen_ghash_linux.py`.
  - **The 128x128 carryless product** is nine 32x32 ones: Karatsuba on
    64-bit halves, then on 32-bit halves. Each one is sixteen integer
    multiplies of operands with their bits four apart ("holes", as in
    BearSSL's ctmul32). Every multiply operand is below 2^32, so every
    multiply is the fast case and the timing does not depend on the data.
    The reduction is that of `polyval_mul_generic()`.
  - **`ghash_blocks_c906()`** is scalar: 483 instructions per block,
    list-scheduled for the C906's latencies. Byte swaps use XTheadBb
    `th.rev`.
  - **`ghash_blocks_c906_v()`** computes
    (acc ^ B0) H^5 + B1 H^4 + B2 H^3 + B3 H^2 + B4 H per group and reduces
    once. Lane l (e32, vl 4) multiplies its block by H^(5-l) with `vmul` and
    `vmulhu` for the two halves of each 32-bit product: 767 vector
    instructions, 1,534 cycles. The scalar block B4 H takes 450 instructions
    issued between the vector ones, where they cost almost nothing. The lanes
    are summed with `vredxor`, and the scalar unit adds its product and
    reduces. Vector instructions are `.4byte` words checked against GNU as,
    so the toolchain needs no XTheadVector support.
  - Both wipe the stack slots that held products and byte-swapped blocks.
- `struct ghash_key` gains `c906_kp[112]` with `RISCV_ISA_XTHEADVECTOR`. It
  holds the 32-bit operands of H and of H^5 to H^2 per lane, already split
  into hole classes, filled by `ghash_preparekey()` (896 bytes).
- `lib/crypto/riscv/gf128hash.h` enables the backend at boot when
  `has_xtheadvector()`, and the vector function also needs VLEN >= 128.
  XTheadVector stands for a T-Head core (C906, C910, C920), and all of them
  implement XTheadBb.
  - Five blocks or more, where `may_use_simd()` holds (process context and
    softirq), go through the vector function between `kernel_vector_begin()`
    and `kernel_vector_end()`.
  - The remainder, and contexts without kernel-mode vector, use the scalar
    function.
  - Zvkg keeps its previous conditions and takes precedence. Its object is
    now built only under them.
- `CRYPTO_LIB_GF128HASH_ARCH` defaults to y with `RISCV_ISA_XTHEADVECTOR`.
  `platform/kernel/config` gains that line, and the release becomes `r3`
  (`CONFIG_LOCALVERSION`, `build.sh`).

## Checks

The generator executes both generated functions in full: prologue, loop and
epilogue. It runs 200 random keys and messages each, including zero blocks,
zero groups and misaligned data, against a bit-serial GHASH. It also checks
that every scalar multiply operand is below 2^32, that `sp`, `ra` and
`s0`-`s11` come back unchanged, and that the stack slots are wiped.
`th.vmv.x.s` is run both sign- and zero-extending, since the code keeps only
the low word either way. Each vector encoding is checked against GNU
as/objdump. CI runs it with `--check` against the patch.

`firmware/cpu/go-crypto-c906/linux-test/` builds `ghash_c906.ko` against the
running kernel. It carries the same assembly and key preparation, and the
same split between the two functions. On the stand (SG2002 C906,
`7.2.10-nanokvm-os-r1`), loading it gave:

- 20,000 random cases against a bit-serial GHASH: lengths 0 to 1600 bytes at
  offsets 0 to 15, all-ones keys and data among them. 0 failures for the
  scalar function, for the vector path and for `ghash_update()` in two
  updates with a partial block.
- 5,000 cases with bottom halves disabled: 0 failures.
- Preemption inside the vector section. A kernel thread that never yields
  competed for 4 s with a thread hashing 64 KiB per call with the vector
  path, each result compared with the scalar one: 893 calls, 184 involuntary
  context switches of that thread, 0 mismatches. 1,000 random cases ran in
  the loading thread meanwhile, with 0 failures. No warning or oops.

Probe variants of the vector function: other latency models for the
scheduler gave the same 1,506-1,515 ns per group. The vector stream alone
takes 1,390 ns, 1,529 cycles, two per vector instruction. The scalar block
placed after the vector stream instead of interleaved takes 1,830 ns.

## `gcm(aes)` through the patched library

The r3 kernel was not booted. On the r1 kernel, `libgf128hash.ko` and `gcm.ko`
are modules, and `gcm` had no users. They were built from the scalar version
of the patch against the r1 tree, with `CRYPTO_LIB_GF128HASH_ARCH` and the
patched `struct ghash_key`, and swapped in for one run of the test module,
built the same way. The original modules were loaded back afterwards.

- 20,000 random cases: 0 failures for the assembly, and 0 for
  `ghash_update()`. The library ran the C906 path, including the partial
  block through `ghash_mul()`.
- `gcm(aes)` is `gcm_base(sg2002-aes-ctr,ghash-lib)`: AES-CTR in the CryptoDMA
  engine, GHASH on the CPU.

| bytes | library GHASH, r1 (MB/s) | library GHASH, scalar patch (MB/s) | `gcm(aes)`, r1 (MB/s) | `gcm(aes)`, scalar patch (MB/s) |
| ---: | ---: | ---: | ---: | ---: |
| 64 | 15 | 19 | 2 | 1 |
| 512 | 15 | 27 | 8 | 10 |
| 1420 | 15 | 27 | 9 | 14 |
| 16384 | 15 | 28 | 11 | 16 |

For 64 bytes, `gcm(aes)` is dominated by the request setup in the engine, not
by the GHASH. A test module built against the r1 headers must not be loaded
against the patched library: `struct ghash_key` grows from 16 to 928 bytes.

## Not covered

- The r3 Image was not booted on the stand. Its `libgf128hash.ko` and
  `gcm.ko` are built from the patch. The library path with the vector
  function was not run as a module: the test module carries the same split
  between the two functions as the glue.
- `gcm(aes)` with the vector GHASH is not measured yet.

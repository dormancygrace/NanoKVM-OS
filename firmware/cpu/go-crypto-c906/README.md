# C906 crypto kernels

Generators of the C906 assembly that the Linux kernel patches, NanoKVM
OpenSSL (`firmware/crypto/openssl-c906`) and the RustDesk add-on carry. Each
generator emits its file and, with `--check`, regenerates it and compares it
with the copy in the patch or source tree; CI runs every check (`ci.yml`,
"Generated C906 assembly is up to date").

| Generator | Output |
| --- | --- |
| `gen_chacha_linux.py` | ChaCha20, `platform/kernel/0040-crypto-riscv-xtheadvector-chacha.patch`; OpenSSL's through `firmware/crypto/openssl-c906/generate-chacha.py` |
| `gen_poly1305_vector.py` | Poly1305, kernel `0077`, OpenSSL `0012`, `addons/rustdesk/src/poly1305_c906.S` |
| `gen_salsa20_c906.py` | Salsa20, `addons/rustdesk/src/salsa20_c906.S` |
| `gen_ghash_table.py` | GHASH, kernel `0041`, OpenSSL `0010` |
| `gen_aes_c906.py` | AES block functions, kernel `0080` |
| `gen_sha2_c906.py` | SHA-256 and SHA-512 blocks, kernel `0079`, OpenSSL `0003` |
| `gen_chacha_riscv64.py`, `goasm_c906.py` | shared: XTheadVector encoder, ChaCha round schedule, GNU as checks |

`LICENSE` and `CRYPTOGAMS-LICENSE` are the licences nanokvm-openssl ships for
this code (`scripts/build-openssl.py`).

## Encodings

The XTheadVector (RVV 0.7.1 draft, VLEN 128) instructions are emitted as
`.4byte` words with the mnemonic as a comment, so the kernel, OpenSSL and Rust
builds need no XTheadVector support in their assembler.
`gen_chacha_riscv64.py` computes each word with a field encoder
written from the XTheadVector formats (0.7.1 `vsetvli` vtype with `vediv`,
`nf`/`mop` load/store fields) and assembles the same mnemonics with GNU as;
generation fails if they differ or objdump does not decode the word back to
the mnemonic. The other vector generators reuse it.

## ChaCha20

The kernel computes five 64-byte blocks per iteration:

- four blocks in XTheadVector lanes, SEW 32, LMUL 1, one state word per vector
  register (v0-v15, lane j = block j), v20 for the lane counters. There is no
  vector rotate: rotl(x, k) is `th.vsrl.vi` by 32 - k into a second register
  plus `th.vmacc.vx` of x times 2^k (the low 32 bits of x * 2^k are x << k,
  and the two parts share no bits), two instructions where shift, shift and or
  take three. A rotated word (4-7, 12-15) alternates between its own register
  and a partner (v16-v19, v21-v24), so every word is back home after each
  round;
- a fifth block in scalar registers, rotated with XTheadBb `th.srriw`. Its 96
  instructions per double round are interleaved evenly with the 128 vector
  ones. The C906 has a 64-bit vector datapath, so a 128-bit vector instruction
  occupies it for two cycles; the in-order core issues scalar instructions in
  between.

The key stream goes to a 320-byte stack buffer with two strided segment stores
(`th.vssseg8e.v`, stride 64), then `dst = src ^ key stream` with byte elements,
so unaligned buffers are fine. `th.vrgather.vv` costs four cycles, so byte
permutes for the 16 and 8 bit rotates do not pay, and LMUL 2 or 4 has the same
throughput per element.

`gen_chacha_linux.py` emits it for `lib/crypto`
(`lib/crypto/riscv/chacha-riscv64-xtheadvector.S`): kernel C calling
convention (s0-s11 saved, the key stream buffer on the stack and wiped before
returning), constants, key and nonce read from `struct chacha_state`, any even
round count, the counter advanced in the state. It produces the same output as
`chacha_block_generic()` and runs at 98 MB/s for a 1420-byte WireGuard packet
and 116 MB/s for 16 KiB, against 47-49 MB/s for the generic kernel code.

## Poly1305 (`gen_poly1305_vector.py`)

On the C906 a `mul`, `mulhu` or `th.mula` with an operand wider than 32 bits
takes about four cycles and blocks issue; with both operands below 2^32 it
issues every cycle. CRYPTOGAMS works in radix 2^64, so 40 of its 76 cycles per
block go to its 10 multiplies. The vector kernel keeps h and r in five 26-bit
limbs, where every product has operands below 2^32, and hashes groups of five
blocks:

- four blocks in XTheadVector lanes: 25 `th.vwmulu.vv`/`th.vwmaccu.vv`
  (32x32 -> 64 bits) per group, the carries at SEW 64, narrowed back with
  `th.vnsrl.vi`;
- a fifth block in scalar registers (`mul`, `th.mula`), interleaved with the
  vector instructions;
- the scalar code also splits the next group's four vector blocks into limbs
  in a frame buffer, since XTheadVector loads fault on misaligned addresses.

Every group multiplies the five accumulators by r^5, except the last one,
where the lanes multiply by r^5, r^4, r^3, r^2 and the scalar block by r; the
prologue computes the powers, and the lanes are summed at the end.

QEMU has no XTheadVector, so the generator executes the kernel itself on 400
random and extreme inputs (clamped-maximum r, all-ones messages, accumulator
limbs at their 2^27 entry bound) against a big-integer Poly1305. It checks on
every instruction that scalar multiplies have operands below 2^32 and that no
32-bit vector sum wraps, and requires a negative control (no pad bit) to fail.

`--linux` emits it for `lib/crypto/riscv/poly1305-riscv64-xtheadvector.S`
(C calling convention, s0-s10 saved). The glue in `lib/crypto/riscv/poly1305.h`
gives runs of at least six whole groups (480 bytes) with padbit 1 to it, at
most 51 groups per `kernel_vector_begin()` and only where `may_use_simd()`;
everything else stays with CRYPTOGAMS. At offset 5, 320 bytes hash at 132 MB/s
against 151 for CRYPTOGAMS, 480 bytes at 161 against 160, and a 1420-byte
WireGuard packet at 218 against 166. `--openssl` and `--rust` emit the same
kernel for OpenSSL and the RustDesk add-on.

## GHASH (`gen_ghash_table.py`)

GHASH with 8-bit tables (Shoup's method); the C906 has no carry-less
multiply. Per key, N tables of 4 KiB hold `b * H * x^(128 q / N)` for every
byte `b`; a block is N interleaved Horner chains of 16 / N steps, each step
one lookup per table and one lookup in a constant reduction table. The code is
scalar (XTheadBb, XTheadMemIdx) and runs in any context.

Table lookups depend on the key and the data, as in OpenSSL's portable 4-bit
GHASH: this is not constant-time code.

With 1408 bytes and the tables in the L1 data cache, one table takes 202
cycles per block, two 141 and four 113. There is no L2 cache, and a table line
evicted between messages costs about 110 ns to bring back. Inside OpenSSL's
AES-GCM records two tables are as fast as four and one is slower from 4 KiB,
so OpenSSL uses two. The kernel's gcm(aes) hashes while the crypto engine
encrypts, so GHASH is what a record waits for; there four tables are faster,
and the kernel uses four (16 KiB per key).

The generator runs every variant it can emit on random keys and messages
against a bit-serial GHASH (an interpreter of the instructions it uses).

## AES block functions (`gen_aes_c906.py`)

The AES block encryption and decryption of the kernel's `lib/crypto`, which
otherwise has only the generic C code on the C906 (its RISC-V backend needs the
vector crypto extensions). Each direction has four tables of 1 KiB; `th.extu`
cuts a byte out of the state and `th.lrwu` loads the table word by index, which
makes 13 instructions per column and 52 per round, against 58 in OpenSSL's
`aes-riscv64.pl`. Rounds are emitted in pairs, so two register sets swap roles
without moves, and two columns are interleaved, so a loaded value is used no
sooner than two instructions later.

One block takes 599 / 814 ns (AES-128 / AES-256) to encrypt and 592 / 807 ns
to decrypt, against 802 / 1072 for the kernel's generic C code and 672 / 918
for OpenSSL's assembly. Table lookups depend on the key and the data: like the
generic C code, this is not constant-time.

`--selftest` checks the generator's own AES with the FIPS 197 examples and then
executes the emitted instructions against it for all three key lengths, with
`in` and `out` unaligned.

## SHA-256 and SHA-512 blocks (`gen_sha2_c906.py`)

Rounds 0 to 15 once, then one body of 16 rounds run three or four times, about
4.5 KiB of code; w[i-1], w[i-2] and w[i-16] stay in registers and a loaded
value is used no sooner than two instructions later. The instruction stream is
the one the generator executes against a plain implementation in `--selftest`.
One kernel `sha256` digest of 4096 bytes takes 160.3 us, against 173.5 us with
the generic code.

## Checks

```sh
B=riscv64-linux-gnu-   # or Buildroot's riscv64-buildroot-linux-musl-
python3 gen_chacha_linux.py --binutils $B --check ../../../platform/kernel/0040-crypto-riscv-xtheadvector-chacha.patch
python3 gen_poly1305_vector.py --binutils $B --linux --check ../../../platform/kernel/0077-crypto-riscv-xtheadvector-poly1305.patch
python3 gen_poly1305_vector.py --binutils $B --rust --check ../../../addons/rustdesk/src/poly1305_c906.S
python3 gen_salsa20_c906.py --binutils $B --check ../../../addons/rustdesk/src/salsa20_c906.S
python3 gen_ghash_table.py --selftest 50 --flavor linux --tables 4 --name ghash_blocks_c906 --check ../../../platform/kernel/0041-crypto-riscv-c906-ghash.patch
python3 gen_aes_c906.py --selftest 30 --name aes_c906 --flavor linux --check ../../../platform/kernel/0080-crypto-riscv-c906-aes.patch lib/crypto/riscv/aes-riscv64-c906.S
python3 gen_sha2_c906.py --bits 256 --name sha256_blocks_c906 --flavor linux --check ../../../platform/kernel/0079-crypto-riscv-c906-sha2.patch lib/crypto/riscv/sha256-riscv64-c906.S
```

The OpenSSL outputs are checked the same way (`ci.yml`, `scripts/build-openssl.py`).

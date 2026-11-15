# ChaCha20-Poly1305 for the C906 in Go's TLS stack

NanoKVM-Server serves video over HTTPS/WebSocket. Without AES hardware, Go's
TLS 1.3 prefers ChaCha20-Poly1305, which uses the std-vendored copy of
`golang.org/x/crypto` (`$GOROOT/src/vendor/golang.org/x/crypto`), not the module
cache. The generic Go ChaCha20 is about three quarters of the AEAD time on the
SG2002's single C906 core. `../sysmon-runtime/prepare.py` copies the files of
this directory into the prepared GOROOT and patches the vendored packages by
exact match; the base toolchain is not modified.

## Kernel

`xorKeyStreamC906` (`chacha_riscv64.s`) computes five 64-byte blocks per
iteration:

- four blocks in XTheadVector (RVV 0.7.1 draft, VLEN 128) lanes, SEW 32,
  LMUL 1, one state word per vector register (v0-v15, lane j = block j), v20
  for the lane counters. There is no vector rotate: rotl(x, k) is
  `th.vsrl.vi` by 32 - k into a second register plus `th.vmacc.vx` of x times
  2^k (the low 32 bits of x * 2^k are x << k, and the two parts share no
  bits), two instructions where shift, shift and or take three. A rotated
  word (4-7, 12-15) alternates between its own register and a partner
  (v16-v19, v21-v24), so every word is back home after each round; with four
  shared temporaries the registers would only return after three double
  rounds, which does not divide ten;
- a fifth block in scalar registers X5-X20, rotated with XTheadBb `th.srriw`
  (one instruction; every T-Head core implements XTheadBb, and only a T-Head
  core reports XTheadVector). Its 96 instructions per double round are
  interleaved evenly with the 128 vector ones. The C906 has a 64-bit vector
  datapath, so a 128-bit vector instruction occupies it for two cycles; the
  in-order core issues scalar instructions in between.

During the rounds KEY, NONCE, DST and SRC are spilled to the frame and hold
the `vmacc` multipliers 1<<16, 1<<12, 1<<8 and 1<<7.

The key stream goes to a 320-byte stack buffer with two strided segment
stores (`th.vssseg8e.v`, stride 64), then `dst = src ^ key stream` with byte
elements (`e8`, `m8`), so unaligned TLS buffers are fine. Nothing is called
inside the function and no vector state lives across calls.

`bufSize` is five blocks (320 bytes) on riscv64. `XORKeyStream` only passes
multiples of it; it never lets the 32-bit counter wrap inside one call.

The rounds run at the vector datapath limit: with shift, shift, or rotates
they take 1600 vector instructions of two cycles per 320 bytes.
`th.vrgather.vv` costs four cycles, so byte
permutes for the 16 and 8 bit rotates do not pay, and LMUL 2 or 4 has the
same throughput per element. On the C906 `th.vmacc.vx` issues every two
cycles like `th.vadd.vv`, so the two-instruction rotate cuts the vector
instructions to 1280 per 320 bytes, and `th.srriw` keeps the scalar block
below the vector one (224 issue slots per double round against 256 vector
cycles).

With `go test -bench` (`src`/`dst` at offset 5) the kernel runs at 70 MB/s
for 1400 bytes and 98 MB/s for 4096, against 22 and 26 MB/s for generic Go.

In the `nanokvmqemu` build the generic code still fills five-block buffers,
which wastes up to four blocks per message. Compare against an unpatched
GOROOT for a baseline.

## Encodings

Go's assembler has no RVV 0.7.1 instructions, so `gen_chacha_riscv64.py`
emits every vector instruction as `WORD` with its mnemonic as a comment. It
computes each word with a field encoder written from the XTheadVector
formats (0.7.1 `vsetvli` vtype with `vediv`, `nf`/`mop` load/store fields) and
assembles the same mnemonics with Buildroot's GNU as
(`-march=rv64gc_xtheadvector`); generation fails if they differ or objdump
does not decode the word back to the mnemonic. `check-binary.py` disassembles
the linked function of a Go binary as XTheadVector code and compares its
`th.*` instructions with the source.

```sh
B=.../buildroot-output/host/bin/riscv64-buildroot-linux-musl-
python3 gen_chacha_riscv64.py --binutils $B --check chacha_riscv64.s
python3 check-binary.py --binutils $B NanoKVM-Server
```

## Selection

NanoKVM OS runs only on the SG2002, and its kernel enables vector state for
every user thread, so the kernel is selected at build time: the constant
`useC906` (`chacha_xtheadvector_riscv64.go`) is true. There is no runtime
detection and no environment switch. The `nanokvmqemu` build tag
(`chacha_qemu_riscv64.go`) selects the upstream generic code for QEMU, which
has no XTheadVector. The vector registers are saved by the kernel across
context switches and signal delivery; the first vector instruction allocates
the thread's vector context.

## Poly1305

Go's `update` (`sum_vector_riscv64.go`) hashes runs of at least four whole
groups of five blocks (320 bytes) with the vector kernel described below and
everything else, and everything in the `nanokvmqemu` build, with
`updateScalar`.
`prepare.py` derives `sum_asm_riscv64.go` from `sum_asm.go` (the same `mac`
wrapper, without the assembly `update` declaration).

### CRYPTOGAMS (`updateScalar`)

`updateScalar` runs CRYPTOGAMS `poly1305_blocks` (Andy Polyakov,
BSD-3-Clause, see `CRYPTOGAMS-LICENSE`), the same Poly1305 as the kernel's
`lib/crypto` and NanoKVM OpenSSL: `gen_poly1305_c906.py` takes the pinned
`poly1305-riscv.pl` that Linux 7.3.1 builds (sha256 `9a75fc04...`), assembles
it with GNU as with and without `__riscv_misaligned_fast`, and writes both
`poly1305_blocks` as Go functions of WORDs into `sum_c906_riscv64.s`;
`sum_c906_riscv64.go` declares them. The generator rejects registers Go
reserves (g in x27, gp, tp) and anything but one final `ret`.

`updateScalar`, written by the generator, adapts the state:

- `macState` is h[3], r[2], s[2]; CRYPTOGAMS wants s1 = r1 + (r1 >> 2) where
  Go keeps s, so `updateScalar` builds a 48-byte context in its frame and
  copies h back;
- `Sum` passes a final partial block: it is copied to the frame with 0x01
  and zeros after it and hashed with padbit 0, as OpenSSL does;
- CRYPTOGAMS reduces h2 when the next block starts, so `updateScalar` folds
  5 * (h2 >> 2) into h0 at the end (as `poly1305_emit` does first): h2 <= 4,
  below the 2 * (2^130 - 5) the finalizer needs;
- `useMisalignedLoads` picks the 64-bit unaligned loads or the aligned-load
  variant. It is always true (the C906 accesses misaligned words natively);
  it stays a variable so that the tests run both variants.

CRYPTOGAMS saves s0-s3 in a 32-byte frame of its own. Go's ABI0 does not need
them preserved, and an SP change the Go assembler cannot see breaks traceback
inside the function (CPU profiles lose the callers), so the generator turns
those 10 instructions into nops: the blocks functions are frameless leaves
and their branch offsets stay valid. `TestC906ProfileUnwind` takes a CPU
profile while hashing and requires every sample in either blocks function to
unwind to the test.

The pinned x/crypto v0.57.0 `sum_riscv64.s` remains the reference:
`build-tests.py --poly1305-reference` builds it with fast unaligned loads in
an overlay. CI (`tests/test-c906-poly1305.py`) prepares the GOROOT, vets the
package, builds both and runs the upstream, big-integer oracle (states with
h2 up to 5 and extreme clamped keys), alignment and guard-page tests on
scalar RISC-V QEMU (built with `--tags nanokvmqemu`); its deliberate corrupted-result control must fail.

### Vector kernel (`gen_poly1305_vector.py`)

On the C906 a `mul`, `mulhu` or `th.mula` with an operand wider than 32 bits
takes about four cycles and blocks issue; with both operands below 2^32 it
issues every cycle. CRYPTOGAMS works in radix 2^64, so 40 of its 76 cycles per
block go to its 10 multiplies. `poly1305BlocksVector` keeps h and r in five
26-bit limbs, where every product has operands below 2^32, and hashes groups
of five blocks:

- four blocks in XTheadVector lanes: 25 `th.vwmulu.vv`/`th.vwmaccu.vv`
  (32x32 -> 64 bits, four lanes, four cycles each) per group, the carries at
  SEW 64, narrowed back with `th.vnsrl.vi`;
- a fifth block in scalar registers (`mul`, `th.mula`), interleaved with the
  vector instructions: the core issues scalar instructions while a vector
  instruction occupies the 64-bit datapath;
- the scalar code also splits the next group's four vector blocks into limbs
  in a frame buffer, since XTheadVector loads fault on misaligned addresses
  and TLS ciphertext starts 5 bytes into the record.

Every group multiplies the five accumulators by r^5, except the last one,
where the lanes multiply by r^5, r^4, r^3, r^2 and the scalar block by r; the
prologue computes the powers, and the lanes are summed at the end. Go's
`update` converts h and r between radix 2^64 and 2^26 around the call (h2 <= 4
on exit). The kernel uses neither ra (Go saves it only in non-leaf
functions, and traceback reads it from the register in a leaf) nor g, gp or
tp; its 176-byte frame is declared, so `TestC906ProfileUnwind` covers it too.

It is selected at build time (`useVector`, `sum_xtheadvector_riscv64.go`);
the `nanokvmqemu` build tag keeps `updateScalar`. QEMU has no XTheadVector, so
the generator executes the kernel itself on 400 random and extreme inputs
(clamped-maximum r, all-ones messages, accumulator limbs at their 2^27 entry
bound) against a big-integer Poly1305. It checks on every instruction that
scalar multiplies have operands below 2^32 and that no 32-bit vector sum
wraps, and requires a negative control (no pad bit) to fail. GNU as assembles
the kernel into WORDs. On the device, `TestC906Vector` compares `update` with
`updateScalar` and the big-integer oracle around the group boundaries, at
every offset, with extreme keys and accumulators up to h2 = 7. The reduction and guard-page tests run `updateScalar` as a second variant.

The vector kernel takes runs of at least four groups because at three it is
slower: with `go test -bench C906VectorGroups` (offset 5) 240 bytes hash at
165 MB/s against 177 for `updateScalar`, 320 bytes at 190 against 180, and
16320 bytes at 288 against 188.

```sh
B=.../buildroot-output/host/bin/riscv64-buildroot-linux-musl-
python3 gen_poly1305_vector.py --binutils $B --go --check sum_vector_riscv64.s
# on the device, in /var/tmp:
./poly1305.test -test.v
./poly1305.test -test.run XXX -test.bench C906VectorGroups
```

## Tests

`build-tests.py` builds linux/riscv64 test binaries of the vendored
packages with a go build overlay: the upstream x/crypto tests of an
extracted module (the vendored code equals x/crypto v0.57.0 apart from the
build tags) plus `tests/`. Neither GOROOT is modified.

```sh
python3 build-tests.py --goroot /path/to/prepared-goroot \
    --xcrypto /path/to/gomodcache/golang.org/x/crypto@v0.57.0 --output /tmp/cc20
# on the device, in /var/tmp (not /data, which is noexec):
./chacha20.test -test.v
./chacha20poly1305.test; ./poly1305.test
./chacha20.test -test.run=Stress -c906.stress=60s &   # twice, concurrently
./chacha20poly1305.test -test.run=Measure -test.v -c906.measure=5
```

`tests/chacha20/c906_test.go` checks RFC 8439 vectors (2.3.2, 2.4.2, A.1),
the kernel against an independent RFC 8439 block function (lengths, counter
wrap inside a call, unaligned and in-place buffers, guard bytes around dst),
3000 random cross-checks of the public API per implementation (chunking,
`SetCounter`, the end of the counter space) and a stress run with a CPU
profile, GC and four goroutines.

`tls-smoke.go` sends random data over a crypto/tls 1.3 loopback connection
and checks that it arrives intact with TLS_CHACHA20_POLY1305_SHA256. With 16
KiB writes (Seal on the client, Open on the server) it takes about 60 ms of
user time per MiB, against 118 with an unpatched GOROOT.

```sh
GOROOT=/path/to/prepared-goroot GOOS=linux GOARCH=riscv64 go build -o /tmp/tls-smoke tls-smoke.go
/var/tmp/tls-smoke 16 16384
```

## The same ChaCha20 in the Linux kernel

`gen_chacha_linux.py` emits the same five-block kernel for `lib/crypto`
(`lib/crypto/riscv/chacha-riscv64-xtheadvector.S` in
`platform/kernel/0040-crypto-riscv-xtheadvector-chacha.patch`): kernel C
calling convention (s0-s11 saved, the key stream buffer on the stack and wiped
before returning), constants, key and nonce read from `struct chacha_state`,
any even round count, the counter advanced in the state. It reuses the
encoder, the vector round schedule and the GNU as/objdump check of
`gen_chacha_riscv64.py`; `--check` accepts the patch itself:

```sh
python3 gen_chacha_linux.py --binutils $B --check ../../../platform/kernel/0040-crypto-riscv-xtheadvector-chacha.patch
```

It produces the same output as `chacha_block_generic()` and runs at 98 MB/s
for a 1420-byte WireGuard packet and 116 MB/s for 16 KiB, against 47-49 MB/s
for the generic kernel code.

## Poly1305 in the Linux kernel

`gen_poly1305_vector.py --linux` emits the vector Poly1305 above for
`lib/crypto` (`lib/crypto/riscv/poly1305-riscv64-xtheadvector.S` in
`platform/kernel/0077-crypto-riscv-xtheadvector-poly1305.patch`; `--check`
accepts the patch): C calling convention (s0-s10 saved), all instructions
`.4byte` words. The glue in `lib/crypto/riscv/poly1305.h` gives runs of at
least six whole groups (480 bytes) with padbit 1 to it, at most 51 groups
per `kernel_vector_begin()` and only where `may_use_simd()`; everything else
stays with CRYPTOGAMS. It converts the CRYPTOGAMS state to 26-bit limbs and
back, after folding h2 as `poly1305_emit` does.

The vector path starts at six groups: at offset 5, 320 bytes hash at
132 MB/s against 151 for CRYPTOGAMS, 480 bytes at 161 against 160, and a
1420-byte WireGuard packet at 218 against 166.
`kernel_vector_begin()` plus `kernel_vector_end()` costs about 210 ns.

## GHASH in the Linux kernel and in OpenSSL

`gen_ghash_table.py` emits GHASH with 8-bit tables (Shoup's method) for
`platform/kernel/0041-crypto-riscv-c906-ghash.patch`
(`lib/crypto/riscv/ghash-riscv64-c906.S`) and for
`firmware/crypto/openssl-c906/0010-ghash-c906.patch`. The C906 has no
carry-less multiply. Per key, N tables of 4 KiB hold `b * H * x^(128 q / N)`
for every byte `b`; a block is N interleaved Horner chains of 16 / N steps,
each step one lookup per table and one lookup in a constant reduction table.
The kernel uses four tables, OpenSSL and Go two. The code is scalar
(XTheadBb, XTheadMemIdx) and runs in any context.

Table lookups depend on the key and the data, as in OpenSSL's portable 4-bit
GHASH: this is not constant-time code.

With 1408 bytes and the tables in the L1 data cache, one table takes 202
cycles per block, two 141 and four 113. There is no L2 cache, and a table
line evicted between messages costs about 110 ns to bring back: with the L1
data cache emptied before every message, 1408 bytes take 31 us with one or
two tables and 42 us with four. Inside OpenSSL's AES-GCM records two tables
are as fast as four and one is slower from 4 KiB, so OpenSSL uses two.

The kernel's gcm(aes) hashes while the crypto engine encrypts, so GHASH is
what a record waits for; there four tables are faster (a 1400-byte AES-256
request from an atomic caller: 31.8 / 24.4 us to encrypt / decrypt, against
33.8 / 26.6 with two). The kernel uses four; a key takes 16 KiB.

The generator runs every variant it can emit on random keys and messages
against a bit-serial GHASH (an interpreter of the instructions it uses), and
checks that a patch contains the generated file:

```sh
python3 gen_ghash_table.py --selftest 50 --flavor linux --tables 4 --name ghash_blocks_c906 --check ../../../platform/kernel/0041-crypto-riscv-c906-ghash.patch
python3 gen_ghash_table.py --flavor openssl --tables 2 --name ossl_ghash_blocks_c906 --check ../../crypto/openssl-c906/0010-ghash-c906.patch
```

## Multi-buffer HMAC-SHA1 (`server/internal/sha1mb`)

SRTP authenticates every packet with HMAC-SHA1 (AES128_CM_HMAC_SHA1_80).
`sha1mb.Key.SumBatch` computes the tags of a whole frame at once. Its
kernel comes from `gen_sha1mb_riscv64.py`, which reuses the XTheadVector
encoder and the GNU as/objdump check of `gen_chacha_riscv64.py`. The package
lives in the server module, not in the prepared GOROOT.

- `sha1x4` hashes four block streams in the 32-bit lanes of XTheadVector
  registers, one SHA-1 word per register (v0-v15 schedule, v16-v20 a-e,
  v21-v25 chaining value). XTheadVector has no rotate: rol(x, k) is
  `th.vsrl.vi` by 32 - k plus `th.vmacc.vx` of x times 2^k, which issues
  every two cycles like an add. `e += rol5(a)` takes three instructions,
  `W[t] = rol1(...)` one fewer than shift, shift, or, and `rol30(b)` two,
  its result in a temporary that then holds b (a-e move among v16-v20 and
  v26-v31; each step starts from the chaining value again). The 1190 vector
  instructions of a step (1414 with shift, shift, or) are list-scheduled
  for an assumed result latency of 6 cycles; in program order the kernel
  takes about 1.6 times as long.
- Scalar code interleaved with the rounds copies the next step's four
  blocks into a stack buffer with byte loads, as big-endian words and
  transposed, so W is loaded with two LMUL 8 loads; no alignment is needed.
- Go splits each message (up to three parts) into segments: runs of whole
  blocks inside one part are read in place; blocks that straddle parts and
  the padded tail are copied to a scratch buffer. Each message goes to the
  least loaded lane, and its outer block follows on the same lane, with the
  inner hash patched into message words 0-4 between steps. Lanes switch
  messages between steps, so one call hashes the whole batch.
- Batches of one message use crypto/sha1, as do host and `nanokvmqemu`
  builds (`internal/xtheadvector`).

For a video frame of 13 x 1200 B + 600 B (each message a 12-byte header, the
payload and a 4-byte ROC), `SumBatch` takes 17.1 us of thread CPU time per
packet on the device, against 45.8 us with crypto/hmac used as pion/srtp uses
it (Reset, Write, Write, Sum).

The Go part (scheduling, segments, copies) takes about 1.4 us per packet.

Since the CryptoDMA engine hashes the packets of a video frame in the same
start that encrypts them (`HWCRYPTO_CTR_SHA1`, docs/ACCELERATORS.md), this
kernel computes for them only the outer block of each HMAC
(`Key.OuterBatch`, four per step), and whole HMACs for what the engine does
not take: audio, retransmissions, RTCP, short packets.

A vector instruction costs about two cycles whatever `vl` is, so idle lanes
cost as much as busy ones: 14 video packets fill 271 of 320 lane steps.

```sh
B=.../buildroot-output/host/bin/riscv64-buildroot-linux-musl-
python3 gen_sha1mb_riscv64.py --binutils $B --check ../../../server/internal/sha1mb/sha1mb_riscv64.s
cd ../../../server
go test -tags teststub ./internal/sha1mb   # host: generic code and a Go model of the kernel
GOOS=linux GOARCH=riscv64 CGO_ENABLED=0 go test -c -o sha1mb.test ./internal/sha1mb
python3 ../firmware/cpu/go-crypto-c906/check-binary.py --binutils $B \
    --symbol NanoKVM-Server/internal/sha1mb.sha1x4 --source internal/sha1mb/sha1mb_riscv64.s sha1mb.test
# on the device, in /var/tmp:
./sha1mb.test -test.run 'RFC|Cross|Bound|Large|Selection' -test.v
./sha1mb.test -test.run Stress -sha1mb.stress=90s &   # twice, concurrently
```

## Annex-B start codes (`server/internal/annexb`)

The WebRTC payloaders split each access unit at its start codes, and the
key frame parameter set scan looks for them too. `bytes.Index(b, {0,0,1})`
stops at every zero byte of coded video and calls IndexByte again from
there; on typical coded video (a zero in about 170 bytes) it never switches
to Horspool. `annexb.Index` uses `indexStartCode` from
`gen_annexb_riscv64.py` instead: two overlapping LMUL 8 loads compared with
zero find each `00 00`, and a scalar load checks the third byte. Inputs
under 64 bytes and host and `nanokvmqemu` builds (`internal/xtheadvector`)
use bytes.Index.

A 9 KB access unit of typical coded video takes 7.5 us, against 21 us with
bytes.Index.

```sh
python3 gen_annexb_riscv64.py --binutils $B --check ../../../server/internal/annexb/annexb_riscv64.s
cd ../../../server
GOOS=linux GOARCH=riscv64 CGO_ENABLED=0 go test -c -o annexb.test ./internal/annexb
# on the device:
./annexb.test -test.v && ./annexb.test -test.run XXX -test.bench .
```

## AES block functions (`gen_aes_c906.py`)

`gen_aes_c906.py --flavor linux` writes the AES block encryption and
decryption of the kernel's `lib/crypto`
(`platform/kernel/0080-crypto-riscv-c906-aes.patch`), which otherwise has
only the generic C code on the C906: its RISC-V backend needs the vector
crypto extensions.

The C906 executes about one instruction per cycle on this code, so the
functions are built for the instruction count. Each direction has four
tables of 1 KiB; `th.extu` cuts a byte out of the state and `th.lrwu` loads
the table word by index (XTheadBb, XTheadMemIdx), which makes 13 instructions
per column and 52 per round, against 58 in OpenSSL's `aes-riscv64.pl`. Rounds
are emitted in pairs, so two register sets swap roles without moves, and two
columns are interleaved, so a loaded value is used no sooner than two
instructions later. The round keys are the ones `lib/crypto/aes.c` prepares
(the Equivalent Inverse Cipher to decrypt).

One block takes 599 / 814 ns (AES-128 / AES-256) to encrypt and 592 / 807 ns
to decrypt, against 802 / 1072 for the kernel's generic C code (one table,
each word rotated, the whole table read before every block) and 672 / 918
for OpenSSL's assembly.

Table lookups depend on the key and the data: like the generic C code, this
is not constant-time, and it does not read the whole table before a block,
which the generic code does as a hardening measure without a guarantee.

```sh
python3 gen_aes_c906.py --selftest 30 --name aes_c906 --flavor linux --check ../../../platform/kernel/0080-crypto-riscv-c906-aes.patch lib/crypto/riscv/aes-riscv64-c906.S
```

`--selftest` checks the generator's own AES with the FIPS 197 examples and
then executes the emitted instructions against it for all three key lengths,
with `in` and `out` unaligned.

### AES-GCM in Go

Upstream Go has no AES or GHASH assembly for riscv64: `crypto/aes` runs the generic
block (about 0.9 us) and `crypto/cipher`'s GCM the generic GHASH, which
multiplies with integer multiplications and takes the C906 about 8 us per
block.

`prepare.py` puts three things into `crypto/internal/fips140/aes`, used
always (NanoKVM OS runs on this CPU only):

- `aes_c906_riscv64.s` (`gen_aes_c906.py --flavor go`) with `aes_riscv64.go`:
  the block functions above in Go assembler syntax. Go keeps the round keys as
  big-endian words; `newBlock` stores them once more with the bytes of each
  word reversed, which is the form the assembly takes. The tables are built at
  start from Go's S-boxes.
- `ctr32BlocksC906` in the same file: the block loop of GCM's counter mode.
  The first three words of the counter block stay in registers, already xored
  with the first round key; the 32-bit counter is a register. Go's generic
  loop costs 1.4 us per block around the block function.
- `gcm/ghash_c906_riscv64.s` (`gen_ghash_table.py --flavor go`) with
  `gcm/gcm_riscv64.go`: GHASH with two tables of 4 KiB per key, as in the
  kernel and OpenSSL. The tables are part of the GCM object and are built
  when it is made.

In Go assembly the T-Head instructions are `WORD`s, each checked against GNU
as (`goasm_c906.py`, `--binutils`). X8, X9, X18 and X20 take the place of s0,
s1 and t6, and nothing is saved: Go's ABI0 has no callee-saved registers.

Writing and reading one 16 KiB AES-128-GCM record through `crypto/tls` on the
CPU takes 1700 us, against 11680 us with stock Go.

Like the kernel and OpenSSL versions, the table GHASH and the AES tables are
not constant-time; Go's generic GHASH is.

`gcm/gcm_riscv64.go` also sends the bulk of a message to the SG2002's crypto
engine (`/dev/nanokvm-hwcrypto`), the way OpenSSL does: from 1024 bytes the
counter mode goes to the engine (`HWCRYPTO_RUN`, 4 KiB per request) and GHASH
stays in Go; from 4096 bytes the driver does both (`HWCRYPTO_GCM`), hashing on
the CPU while the engine encrypts. The requests are raw system calls: they
take 20 to 300 us of this CPU and wait for nothing else. Without the device,
without permission to open it, or after the driver gave the engine up, the
code above does all of it.

The engine starts at 1024 bytes because below that Go's own AES is faster: a
512-byte record takes 136 us (AES-128) or 161 us (AES-256) in Go and 147 to
175 us with the engine. A 16 KiB AES-128-GCM record takes 724 us with the
engine.

`Open` decrypts through the driver before it has checked the tag, and clears
its output when the tag is wrong. If the driver reports its engine lost in a
request it does in place on the caller's memory, the buffer is partly
written: `Open` fails, and `Seal`, which has no error to return, panics.

The tests of `crypto/aes`, `crypto/cipher` (39) and the AES, GCM, CTR, CBC,
CMAC and self-test parts of `crypto/internal/fips140test` pass on the device,
run one test at a time: the whole `crypto/cipher` binary needs 178 MiB for
one of its CTR tests, which hangs the 247 MiB device until the watchdog
resets it.

```sh
python3 gen_aes_c906.py --name aes_c906 --flavor go --selftest 30 --binutils $B --check-file aes_c906_riscv64.s
python3 gen_ghash_table.py --flavor go --tables 2 --name ghashBlocksC906 --selftest 20 --binutils $B --check-file ghash_c906_riscv64.s
```

## SHA-256 and SHA-512 blocks (`gen_sha2_c906.py`)

`--flavor linux` writes the same blocks for the kernel's `lib/crypto`
(`platform/kernel/0079-crypto-riscv-c906-sha2.patch`), which otherwise has
only the generic C code on the C906: its RISC-V backends need the vector
crypto extensions. One `sha256` digest of 4096 bytes through the kernel API
takes 160.3 us, against 173.5 us with the generic code.

`gen_sha2_c906.py --flavor go` writes `sha256block_riscv64.s` and `sha512block_riscv64.s`, which `prepare.py` puts in place of Go's riscv64 assembly under the same name `block`: there is no selection at run time. It is the generator of OpenSSL's blocks (`firmware/crypto/openssl-c906`): rounds 0 to 15 once, then one body of 16 rounds run three or four times, about 4.5 KiB of code; w[i-1], w[i-2] and w[i-16] stay in registers and a loaded value is used no sooner than two instructions later. The XTheadBb instructions are `WORD`s, each checked against GNU as (`--binutils`); the instruction stream is the one the generator executes against a plain implementation in `--selftest`.

At 16 KiB, `sha256.Sum256` runs at 25.4 MB/s against 16.0 with Go's block, and `sha512.Sum512` at 39.2 MB/s against 24.1. The tests of `crypto/sha256`, `crypto/sha512` and `crypto/hmac` pass on the device.

```sh
B=.../buildroot-output/host/bin/riscv64-buildroot-linux-musl-
python3 gen_sha2_c906.py --selftest 40 --bits 256 --name block --flavor go --binutils $B --check-file sha256block_riscv64.s
python3 gen_sha2_c906.py --bits 512 --name block --flavor go --binutils $B --check-file sha512block_riscv64.s
```

## SHA-1 block (`gen_sha_c906.py`)

Go builds riscv64 for rva20u64. As a result, each `RORW` in its riscv64 SHA-1 assembly becomes three instructions (`srliw`, `slliw`, `or`), and each message word is assembled from four byte loads.

`gen_sha_c906.py` expands the macros of Go's `sha1block_riscv64.s` from the pinned GOROOT. It writes `sha1block_riscv64.s`, the same function `block` with:

- `th.srriw` (XTheadBb) for every `RORW`: 224 per SHA-1 block;
- `lwu` + `th.revw` for each message word, because misaligned loads are fast on the C906.

Each instruction is a `WORD` that GNU as produced from the mnemonic, and objdump must decode it back. `--check` regenerates the file and compares.

`prepare.py` puts the file in place of Go's. NanoKVM OS runs only on the C906, so there is no selection at run time and no `internal/cpu` flag for XTheadBb (Linux reports it neither in `riscv_hwprobe` nor in `/proc/cpuinfo`).

With `go test -bench`, SHA-1 of 8 KiB runs at 42.80 MB/s against 30.62 with Go's block.

SRTP keeps the multi-buffer HMAC-SHA1 described above. crypto/sha1 covers single messages, RTCP and the other callers.

```sh
B=.../buildroot-output/host/bin/riscv64-buildroot-linux-musl-
python3 gen_sha_c906.py --binutils $B --goroot $BASE_GOROOT --algo sha1 --check sha1block_riscv64.s
# Also build test binaries for crypto/sha1, crypto/hmac and crypto/tls (needs its testdata).
GOROOT=$PREPARED GOOS=linux GOARCH=riscv64 go test -c -o sha1.test crypto/sha1
# on the device:
./sha1.test -test.short
```

## Keccak-f[1600] (`gen_keccak_c906.py`)

SHA-3 and SHAKE run the Keccak-f[1600] permutation. Since Go 1.24, every TLS
handshake with `X25519MLKEM768` (the default) expands its ML-KEM matrix with
SHAKE128, so the permutation is on the server's TLS path. On rva20u64, Go's
generic permutation compiles to about 287 instructions per round. Every
rotate takes three instructions, and the lanes spill to the stack.

`gen_keccak_c906.py` builds `keccakF1600C906` from CRYPTOGAMS
`riscv/keccak1600-riscv.pl` (BSD-3-Clause, see `CRYPTOGAMS-LICENSE`). The
generator pins the commit and the sha256.

CRYPTOGAMS keeps all 25 lanes in registers. Without Zbb it uses lane
complementing, so χ needs no `andn`, which the C906 lacks. The generator
takes that no-Zbb round and changes two things:

- It fuses each shift/shift/or rotate into one `th.srri` (XTheadBb): 24 in ρ
  and 5 in θ. Each fusion is checked for data flow and dead temporaries.
- It moves CRYPTOGAMS' lane in x27 to x1, because x27 is Go's `g`. The
  round-constant pointer then lives in the frame instead of x1.

A round is about 162 instructions, against about 287 for Go's generic
permutation. Both figures are static counts of the loop body.

On the device the permutation takes 4.63 µs against 9.34 µs for the generic
one. A TLS server encapsulates once per `X25519MLKEM768` handshake, so the
saving is about 0.25 ms of CPU per handshake, around 1% of a full handshake.

`--binutils` checks every `th.srri` WORD against GNU as. `--check`
regenerates the file and compares. CI does both.

`prepare.py` adds `sha3_riscv64.go`, which calls `keccakF1600C906` always;
the generic permutation stays in the package for the test below.

`tests/sha3` compares both permutations on 20,000 random, all-zero and
all-one states.

```sh
curl -fsSL "$(python3 gen_keccak_c906.py --print-url)" -o keccak1600-riscv.pl
python3 gen_keccak_c906.py --cryptogams keccak1600-riscv.pl --binutils riscv64-linux-gnu- --check keccak_c906_riscv64.s
python3 build-tests.py --goroot $PREPARED --xcrypto $XCRYPTO --output out   # adds fips-sha3, sha3, mlkem
# on the device; for qemu-riscv64 -cpu thead-c906, build with --tags nanokvmqemu:
NANOKVM_REQUIRE_XTHEADBB=1 ./out/fips-sha3.test -test.v
./out/sha3.test -test.skip TestCSHAKELargeS && ./out/mlkem.test -test.short  # that test needs 512 MiB
```

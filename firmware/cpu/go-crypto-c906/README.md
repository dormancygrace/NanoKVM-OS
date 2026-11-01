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
  LMUL 1, one state word per vector register (v0-v15, lane j = block j), v16-v19
  for the rotates (shift left, shift right, or; there is no vector rotate),
  v20 for the lane counters;
- a fifth block in scalar registers X5-X20, its 160 instructions per double
  round interleaved one to one with the 160 vector instructions. The C906 has
  a 64-bit vector datapath, so a 128-bit vector instruction occupies it for two
  cycles; the in-order core issues the scalar instruction in between.

The key stream goes to a 320-byte stack buffer with two strided segment
stores (`th.vssseg8e.v`, stride 64), then `dst = src ^ key stream` with byte
elements (`e8`, `m8`), so unaligned TLS buffers are fine. Nothing is called
inside the function and no vector state lives across calls.

`bufSize` is five blocks (320 bytes) on riscv64. `XORKeyStream` only passes
multiples of it; it never lets the 32-bit counter wrap inside one call.

Kernel variants measured on the device by thread CPU time, 20 KiB per call,
in one run while video streamed (the shipped kernel reaches 94 MB/s when the
device is quieter):

| kernel | MB/s |
| --- | ---: |
| generic Go | 26 |
| RV64I scalar assembly, 1 block | 32 |
| XTheadVector, 4 blocks | 65 |
| XTheadVector 4 + scalar 1, 16 strided stores (`th.vsse.v`) | 75 |
| XTheadVector 4 + scalar 1, 2 segment stores (shipped) | 79 |
| same, rounds only (no add/store/XOR) | 91 |

The rounds run at the vector datapath limit (1600 vector instructions of two
cycles per 320 bytes). `th.vrgather.vv` costs four cycles, so byte permutes
for the 16 and 8 bit rotates do not pay, and LMUL 2 or 4 has the same
throughput per element. XTheadBb's `th.srriw` would shorten the scalar
rotates, but the NanoKVM device tree does not advertise `xtheadbb`, so it is
not used.

With `NANOKVM_CHACHA20=generic` the generic code still fills five-block
buffers, which wastes up to four blocks per message: its key stream is about
15% slower than upstream for 1400 byte records (Seal 8%, with the faster
Poly1305), within noise for 16 KiB. Compare against an unpatched GOROOT for a
baseline.

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

On first use, `chacha_riscv64.go` selects the kernel only if every `isa`
line of `/proc/cpuinfo` lists `xtheadvector` and
`prctl(PR_RISCV_V_GET_CONTROL)` reports vector state enabled for the thread
(it is off when `abi.riscv_v_default_allow` is 0). Otherwise, or with
`NANOKVM_CHACHA20=generic`, the upstream generic code runs. Other values are
ignored. The vector registers are saved by the kernel across context switches
and signal delivery; the first vector instruction allocates the thread's
vector context.

## Poly1305

The prepared GOROOT uses `sum_c906_riscv64.s`. The pinned x/crypto v0.57.0
`sum_riscv64.s` remains unchanged as the reference, and
`build-tests.py --poly1305-reference` recreates the previous implementation
with its existing fast unaligned loads in a build overlay.

The C906 implementation folds high products before propagating carries.
Writing B = 2^64, p = 4*B^2 - 5 and the clamped key as r0 + r1*B,
r1 is divisible by four. Precompute s1 = 5*(r1 >> 2). For the accumulator
after adding a message block, form L = h0*r0 + h1*s1 and
M = h0*r1 + h1*r0 + h2*s1; combine L, M*B and h2*r0*B^2, then
reduce the remaining high word. There are still ten MUL/MULHU instructions,
but substantially fewer carry/reduction instructions. The instruction order
uses the C906's measured wide-multiply issue occupancy and result latency.

Reachable input h2 is at most five before adding a block and seven after it.
With r0,r1 < 2^60 and s1 < 5*2^58, the high word is below 9*2^60+2.
The final 5*(high >> 2) addition fits in 64 bits and the output is below 2*p,
as required by the existing finalizer. No secret-dependent branches or
memory addresses are added. This is a source-level control-flow property,
not a new claim about operand-independent hardware multiply timing.

The aligned, fast-misaligned and byte-load paths, partial-block construction
and public streaming API remain. `cpu.RISCV64.HasFastMisaligned` controls the
native unaligned loads; the fallback is tested separately. Zero-length input
returns without touching the state. Forty unreachable padding bytes retain
the qualified Go 1.27.3 function extent, so surrounding code has identical
placement in the comparison binaries. This is specific to the pinned Go
assembler, including instruction relaxation. After changing Go or the routine,
rederive both symbol extents and requalify matched whole-binary placement;
do not preserve the padding length without measurement.

CI prepares the pinned production GOROOT, builds both the selected and upstream
reference Poly1305 test binaries and checks their 506-byte update symbols.
`tests/test-c906-poly1305.py` runs the upstream, big-integer, alignment and
guard-page tests on scalar RISC-V QEMU with RVV disabled; its deliberate
corrupted-result control must fail. This tests correctness, not C906 timing.

The measured AEAD CPU reduction is about 5–6% at 1400 bytes and 7–8% at
16 KiB. These are incremental gains over the already accelerated NanoKVM
code, unlike the older unpatched-Go comparison below.

The Go change does not replace the kernel Poly1305 used by WireGuard.
Linux uses its own RISC-V Cryptogams/OpenSSL assembly, which already folds
the r1 products. The Go and kernel ChaCha20 implementations share the
four-vector-plus-one-scalar design but have distinct calling conventions
and state-management code.

## Earlier baseline results

Device, by thread CPU time (MB/s, medians of 7 interleaved runs of 5 rounds,
with video streaming in the background), base = unpatched GOROOT:

| | 1400 B base | 1400 B new | 16 KiB base | 16 KiB new |
| --- | ---: | ---: | ---: | ---: |
| ChaCha20 part of Seal | 25.3 | 49.3 | 27.0 | 77.7 |
| Poly1305, message at offset 5 | 93.5 | 197.4 | 96.8 | 202.7 |
| ChaCha20-Poly1305 Seal | 19.4 | 34.2 | 20.3 | 49.2 |

`tls-smoke.go` sends random data over a crypto/tls 1.3 loopback connection
and checks that it arrives intact with TLS_CHACHA20_POLY1305_SHA256. With 16
KiB writes (Seal on the client, Open on the server) user time went from about
118 to 60 ms per MiB.

```sh
GOROOT=/path/to/prepared-goroot GOOS=linux GOARCH=riscv64 go build -o /tmp/tls-smoke tls-smoke.go
/var/tmp/tls-smoke 16 16384
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
./chacha20.test -test.v; NANOKVM_CHACHA20=generic ./chacha20.test -test.v
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

`linux-test/` is a module that carries the same assembly and glue, checks
them against `chacha_block_generic()` and times them on the device: 81 MB/s
for a 1420-byte WireGuard packet and 93 MB/s for 16 KiB, against 42 for the
generic kernel code. Poly1305 stays scalar: at 211-249 MB/s it is not the
limit, and a 64-bit vector datapath multiplies no faster than scalar
`mul`/`mulhu`.

## GHASH in the Linux kernel

`gen_ghash_linux.py` emits `lib/crypto/riscv/ghash-riscv64-c906.S` for
`platform/kernel/0041-crypto-riscv-c906-ghash.patch`. The C906 multiplies
operands below 2^32 in one pipelined cycle and wider ones in about four
unpipelined cycles. So the 128x128 carryless product is nine 32x32 ones, each
sixteen multiplies of operands with their bits four apart, all below 2^32.

There are two functions. The scalar one needs no vector unit. The other one
hashes five blocks per group, four in XTheadVector lanes with key powers H^5
to H^2 and one in scalar registers, the two instruction streams interleaved.
The generator executes both functions on random inputs against a bit-serial
GHASH, and checks the vector encodings against GNU as, before writing or
checking the file:

```sh
python3 gen_ghash_linux.py --binutils riscv64-linux-gnu- --check ../../../platform/kernel/0041-crypto-riscv-c906-ghash.patch
```

`linux-test/ghash_c906_test.c` carries the same assembly and key preparation
and checks them on the device: 27-29 MB/s for the scalar function and
42-53 MB/s for the vector one, against 14-15 for the kernel's generic GHASH,
which limits `gcm(aes)` on the SG2002.

## Multi-buffer HMAC-SHA1 (`server/internal/sha1mb`)

SRTP authenticates every packet with HMAC-SHA1 (AES128_CM_HMAC_SHA1_80).
`sha1mb.Key.SumBatch` computes the tags of a whole frame at once. Its
kernel comes from `gen_sha1mb_riscv64.py`, which reuses the XTheadVector
encoder and the GNU as/objdump check of `gen_chacha_riscv64.py`. The package
lives in the server module, not in the prepared GOROOT.

- `sha1x4` hashes four block streams in the 32-bit lanes of XTheadVector
  registers, one SHA-1 word per register (v0-v15 schedule, v16-v20 a-e,
  v21-v25 chaining value). Rotates are shift, shift, or (or two adds when
  the result is added anyway). The 1414 vector instructions of a step are
  list-scheduled for an assumed result latency of 6 cycles; in program
  order the kernel took about 1.6 times as long.
- Scalar code interleaved with the rounds copies the next step's four
  blocks into a stack buffer with byte loads, as big-endian words and
  transposed, so W is loaded with two LMUL 8 loads; no alignment is needed.
  Copying little-endian words and swapping them with 16 `th.vrgather.vv`
  per step measured the same. Leaving out the copy (wrong results) was
  about 7% faster, which bounds the cost of the byte swap and transpose.
- Go splits each message (up to three parts) into segments: runs of whole
  blocks inside one part are read in place; blocks that straddle parts and
  the padded tail are copied to a scratch buffer. Each message goes to the
  least loaded lane, and its outer block follows on the same lane, with the
  inner hash patched into message words 0-4 between steps. Lanes switch
  messages between steps, so one call hashes the whole batch.
- Batches of one message use crypto/sha1, as do other CPUs, a missing
  `xtheadvector` in `/proc/cpuinfo`, vector state disabled according to
  `prctl(PR_RISCV_V_GET_CONTROL)`, and `NANOKVM_SHA1MB=generic`.

Device, thread CPU time per packet, medians of 7 interleaved rounds with
video streaming. Messages are a 12-byte header, the payload and a 4-byte
ROC; crypto/hmac is used as pion/srtp does (Reset, Write, Write, Sum):

| batch | crypto/hmac | SumBatch | speedup |
| --- | ---: | ---: | ---: |
| video, 13 x 1200 B + 600 B | 45.8 us | 17.1 us | 2.7x |
| audio, 4 x 172 B | 12.1 us | 4.7 us | 2.6x |
| 2 x 1200 B | 44.7 us | 27.7 us | 1.6x |
| 4 x 4096 B | 148.9 us | 44.5 us | 3.3x |

The Go part (scheduling, segments, copies) takes about 1.4 us per packet.
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
there; on the stand's stream (a zero in about 170 bytes) it never switches
to Horspool. `annexb.Index` uses `indexStartCode` from
`gen_annexb_riscv64.py` instead: two overlapping LMUL 8 loads compared with
zero find each `00 00`, and a scalar load checks the third byte. Inputs
under 64 bytes, other CPUs, a missing `xtheadvector`, vector state disabled
(`internal/xtheadvector`) and `NANOKVM_ANNEXB=generic` use bytes.Index.

On the device, 9 KB access units shaped like the stand's stream: 7.5 us per
unit, bytes.Index 21 us.

```sh
python3 gen_annexb_riscv64.py --binutils $B --check ../../../server/internal/annexb/annexb_riscv64.s
cd ../../../server
GOOS=linux GOARCH=riscv64 CGO_ENABLED=0 go test -c -o annexb.test ./internal/annexb
# on the device:
./annexb.test -test.v && ./annexb.test -test.run XXX -test.bench .
```

## SHA-1 and SHA-256 blocks (`gen_sha_c906.py`)

Go builds riscv64 for rva20u64. As a result, each `RORW` in its riscv64 SHA-1 and SHA-256 assembly becomes three instructions (`srliw`, `slliw`, `or`), and each message word is assembled from four byte loads.

`gen_sha_c906.py` expands the macros of Go's `sha1block_riscv64.s` and `sha256block_riscv64.s` from the pinned GOROOT. It writes `blockC906` copies that use:

- `th.srriw` (XTheadBb) for every `RORW`: 224 per SHA-1 block and 576 per SHA-256 block;
- `lwu` + `th.revw` for each message word, because misaligned loads are fast on the C906.

Each instruction is a `WORD` that GNU as produced from the mnemonic, and objdump must decode it back. `--check` regenerates the file and compares. A SHA-256 block goes from 3848 to 2568 instructions.

`prepare.py` renames Go's block to `blockRV64` and adds `sha256block_riscv64.go` and `sha1block_riscv64.go`, which choose `blockC906` when `cpu.RISCV64.HasXTheadBb` is set.

- Linux reports XTheadBb neither in `riscv_hwprobe` nor in `/proc/cpuinfo`. `internal/cpu` therefore sets it from the `mvendorid` that `riscv_hwprobe` returns: every T-Head RISC-V core (0x5b7) implements XTheadBb.
- `GODEBUG=cpu.xtheadbb=off` selects Go's block.
- Both blocks are registered with `crypto/internal/impl`, so the crypto tests run each of them.

Measured on the device with `go test -bench`: 1 s per benchmark, median of 3 interleaved runs, the same binary with and without `GODEBUG=cpu.xtheadbb=off`. Rates are in MB/s.

| | Go's block | C906 block | |
| --- | ---: | ---: | ---: |
| SHA-256, 8 bytes | 1.35 | 2.01 | 1.49x |
| SHA-256, 1 KiB | 14.34 | 21.48 | 1.50x |
| SHA-256, 8 KiB | 15.30 | 23.08 | 1.51x |
| SHA-1, 8 bytes | 1.97 | 3.45 | 1.75x |
| SHA-1, 1 KiB | 29.00 | 40.26 | 1.39x |
| SHA-1, 8 KiB | 30.62 | 42.80 | 1.40x |

SRTP keeps the multi-buffer HMAC-SHA1 described above. crypto/sha1 covers single messages, RTCP and the other callers.

```sh
B=.../buildroot-output/host/bin/riscv64-buildroot-linux-musl-
python3 gen_sha_c906.py --binutils $B --goroot $BASE_GOROOT --algo sha256 --check sha256block_c906_riscv64.s
python3 gen_sha_c906.py --binutils $B --goroot $BASE_GOROOT --algo sha1 --check sha1block_c906_riscv64.s
# Also build test binaries for crypto/sha1, crypto/hmac, crypto/tls (needs its testdata) and internal/cpu.
GOROOT=$PREPARED GOOS=linux GOARCH=riscv64 go test -c -o sha256.test crypto/sha256
# on the device:
./sha256.test -test.v -test.short | grep XTheadBb
GODEBUG=cpu.xtheadbb=off ./sha256.test -test.run XXX -test.bench Hash
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

On the device (C906 at 1 GHz, NanoKVM server running, load below 1.6), five
alternating runs of each binary, medians:

| Benchmark | Generic | C906 | Speedup |
| --- | ---: | ---: | ---: |
| Keccak-f[1600] permutation | 9.34 µs | 4.63 µs | 2.0× |
| SHA3-256, 1350 B | 111.5 µs | 61.9 µs | 1.8× |
| SHAKE128, 1350 B | 95.1 µs | 52.6 µs | 1.8× |
| ML-KEM-768 key generation | 1.678 ms | 1.438 ms | −14% |
| ML-KEM-768 encapsulation | 2.026 ms | 1.778 ms | −12% |
| ML-KEM-768 decapsulation | 1.903 ms | 1.801 ms | −5% |

The permutation is measured in one binary. The other rows compare the default
with `GODEBUG=cpu.xtheadbb=off` in the same binary. A TLS server
encapsulates once per `X25519MLKEM768` handshake, so the saving is about
0.25 ms of CPU per handshake, around 1% of a full handshake.

`--binutils` checks every `th.srri` WORD against GNU as. `--check`
regenerates the file and compares. CI does both.

`prepare.py` adds `sha3_riscv64.go`:

- It selects `keccakF1600C906` when `cpu.RISCV64.HasXTheadBb` is set, and
  registers it with `crypto/internal/impl`, so the crypto tests run both
  permutations.
- `GODEBUG=cpu.xtheadbb=off` selects the generic permutation.

`tests/sha3` compares both permutations on 20,000 random, all-zero and
all-one states. It fails if a T-Head core does not select the C906 path.

```sh
curl -fsSL "$(python3 gen_keccak_c906.py --print-url)" -o keccak1600-riscv.pl
python3 gen_keccak_c906.py --cryptogams keccak1600-riscv.pl --binutils riscv64-linux-gnu- --check keccak_c906_riscv64.s
python3 build-tests.py --goroot $PREPARED --xcrypto $XCRYPTO --output out   # adds fips-sha3, sha3, mlkem
# under qemu-riscv64 -cpu thead-c906, or on the device:
NANOKVM_REQUIRE_XTHEADBB=1 ./out/fips-sha3.test -test.v
./out/sha3.test -test.skip TestCSHAKELargeS && ./out/mlkem.test -test.short  # that test needs 512 MiB
```

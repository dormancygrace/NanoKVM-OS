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

The vendored Poly1305 is the x/crypto v0.57.0 code without its riscv64
assembly. prepare.py adds `sum_riscv64.s` (checked against the v0.57.0 file,
sha256 `59299b25...dc84b`) with the v0.57.0 build tags, which take its 64x64
multiplies with `MUL`/`MULHU` instead of the generic `bits.Mul64` code. That
assembly loads unaligned messages byte by byte; TLS ciphertext starts 5 bytes
into the record. `riscv_hwprobe` reports fast misaligned scalar access on this
C906 (and unsupported misaligned vector access, so the ChaCha20 kernel keeps
byte elements), so prepare.py lets it use 64-bit loads when
`cpu.RISCV64.HasFastMisaligned` is set (`sum_misaligned_riscv64.go`);
without it the byte loads remain. `tests/poly1305` compares both paths with
the generic code for every offset modulo 16.

## Results

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
./sha1mb.test -test.run Measure -test.v -sha1mb.measure=7
```

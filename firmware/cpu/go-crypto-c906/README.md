# ChaCha20 for the C906 in Go's TLS stack

NanoKVM-Server serves video over HTTPS/WebSocket. Without AES hardware, Go's
TLS 1.3 prefers ChaCha20-Poly1305, which uses the std-vendored copy of
`golang.org/x/crypto` (`$GOROOT/src/vendor/golang.org/x/crypto`), not the module
cache. The generic Go ChaCha20 is about three quarters of the AEAD time on the
SG2002's single C906 core. `../sysmon-runtime/prepare.py` copies the files of
this directory into the prepared GOROOT and patches the vendored package by
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

Measured on the device with thread CPU time, 20 KiB of blocks per call:

| kernel | MB/s |
| --- | ---: |
| generic Go | 26 |
| RV64I scalar assembly, 1 block | 32 |
| XTheadVector, 4 blocks | 65 |
| XTheadVector 4 + scalar 1 (shipped) | 79 |
| same, rounds only (no add/store/XOR) | 91 |

The rounds run at the vector datapath limit (1600 vector instructions of two
cycles per 320 bytes). `th.vrgather.vv` costs four cycles, so byte permutes
for the 16 and 8 bit rotates do not pay. XTheadBb's `th.srriw` would shorten
the scalar rotates, but this kernel's device tree does not advertise
`xtheadbb`, so it is not used.

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

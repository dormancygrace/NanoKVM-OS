# C906 WebSocket masking

Gorilla v1.5.3 masks buffers one machine word at a time after constructing an
aligned mask. The NanoKVM build substitutes a C906 implementation at the same
maskBytes entry point. This affects incoming browser frames, including remote
ISO responses, and outgoing client connections. Server-to-browser video frames
are unmasked; this is not a claim of faster video encoding or network throughput.

The selected path keeps 0–7 byte messages in Go, uses a 64-byte scalar unroll
for 8–127 bytes, and XTheadVector byte loads/XOR/stores from 128 bytes. It rotates
the repeated four-byte key by the input phase and returns the original phase
plus length modulo four. Full vector groups always advance by a multiple of
four bytes; only the last group changes VL.

The firmware targets SG2002/C906. Initialization requires xtheadvector on every
reported ISA line, fast misaligned scalar accesses from riscv_hwprobe, and
enabled vector state from PR_RISCV_V_GET_CONTROL. Otherwise the original code
is used. NANOKVM_WEBSOCKET_MASK=generic explicitly selects it for comparisons.
The Go threads inherit the vector policy; applications must not disable it
on individual threads after initialization. No vector state is live across
calls or Go safe points. Linux preserves it across signals and task switches.

## Build integration

scripts/build-server-existing-libs.py calls prepare.py. It obtains the exact
go.sum-verified Gorilla v1.5.3 module, checks mask.go and go.mod against pinned
SHA-256 values, stages only the root package files under its build output and
creates a private server.mod/server.sum. Existing local Pion replacements keep
their original meaning. Neither the source checkout nor the shared module cache
is modified. Go forbids overlays below GOMODCACHE, so a private module replacement
is used instead.

The module retains its BSD license and the original function as maskBytesGeneric.
Host, purego and appengine builds keep generic masking. Dependency upgrades fail
the source pin until the patch is reviewed. The server builder verifies the
linked assembly symbol and records the staged file hashes in its manifest.

gen_mask_riscv64.py reuses the repository's XTheadVector encoder and checks every
distinct instruction against GNU as/objdump with rv64gc_xtheadvector. The linked
production server was also checked: its 276-byte assembly function contains the
same ten vector instructions as the generated source.

## Device results, 2026-10-26

SG2002/C906 at 1 GHz, Linux 7.3.0-nanokvm-os-r3, prepared Go 1.27.3.
The stand was accessed through Ethernet 192.168.1.20 with video enabled.
Each measurement has three independent processes and three rotated repetitions
per cell. Each process first takes a median for each variant; the reported
saving is the median of the three process ratios. Positive percentages mean
less CPU time, not a directly measured application throughput increase.
measurements.csv retains all process medians, including the small-buffer cases.

The primitive tests use locked-thread CPU time, 34 lengths from zero to 1 MiB
and offsets 0, 1, 5, 7. Streaming visits distinct tiles in an 8 MiB arena;
each variant starts with the same nonzero data. It is not a repeat of one small
cached buffer. These compare the actual dispatched maskBytes with the retained
original function in the staged Gorilla package.

| Bytes | Repeated-buffer CPU reduction | Streaming CPU reduction |
| ---: | ---: | ---: |
| 256 | 65.2–71.3% | 35.5–48.3% |
| 1,400 | 73.6–74.7% | 26.4–32.2% |
| 4,096 | 74.8–76.6% | 25.4–26.1% |
| 16,384 | 75.1–77.8% | 27.0–28.0% |
| 65,536 | 67.9–73.2% | 21.6–22.5% |
| 1,048,576 | 25.9–31.5% | 21.7–22.1% |

For 8 bytes the reduction is 36.5–37.3%. The 0–7 byte path stays near the
original: the worst measured median is about 2.1% slower for an empty call
(roughly one nanosecond), rather than the much larger regression caused by
calling assembly for tiny messages.

The ReadMessage experiment parses prebuilt, independently masked client frames
through Gorilla's real message reader with a 4 KiB read buffer. It includes
message allocations and GC, but excludes TLS, network/USB I/O and frame
construction. Generic and C906 are paired within each process in rotated order.
Sixteen passes reuse a bounded wire buffer. GC runs before each variant and
reclamation is included at the end; process CPU time counts background GC
workers as well as the reader. This avoids assigning arbitrary shares of GC
to a thread-only measurement.

| Payload bytes | ReadMessage CPU reduction | Three process reductions |
| ---: | ---: | --- |
| 16 | 3.01% | 1.81%, 3.01%, 5.05% |
| 64 | 1.96% | 1.00%, 2.06%, 1.96% |
| 256 | 4.54% | 4.97%, 4.39%, 4.54% |
| 1,400 | 3.54% | 2.61%, 3.54%, 3.83% |
| 4,096 | 4.56% | 4.05%, 5.15%, 4.56% |
| 16,384 | 3.17% | 7.73%, 3.17%, 2.04% |
| 65,536 | 7.43% | 7.43%, 7.97%, 7.11% |
| 262,144 | 9.37% | 9.40%, 5.72%, 9.37% |

The API control selects the retained generic function through the same dispatcher;
an untouched installed-firmware A/B was not performed. Every paired ReadMessage
case improved in all three processes. These numbers
are not predictions for browser ISO speed or FPS. Video connections restarted
during parts of the wider investigation; encoder warnings of the same kind
were already present before testing. Frequency and video counters are retained
in the local evidence. Large memory timings have more noise than short masks.

## Verification and reproduction

The complete upstream Gorilla suite and the added C906 tests passed on the
stand with automatic selection and with NANOKVM_WEBSOCKET_MASK=generic.
The latter also verifies fallback behavior. The custom suite includes:

- 104,184 payload, guard-byte, phase and split-chunk comparisons, all offsets
  0–15, all four phases, dense sizes 0–512 and large boundary sizes.
- 2,272 inaccessible-page cases, including zero-length pointers in PROT_NONE.
- Four locked OS threads exercising vector state across scheduling.
- Independently constructed masked frames through ReadMessage, including
  extended lengths and repeated messages.
- A negative control which must fail after deliberately corrupting a result.

The complete server was cross-built against the existing HDMI-light native
bundle; linked vector words matched the source. Host Gorilla/default and
purego tests, storage/stream/ws tests and Python staging tests passed.

The original Gorilla tests need two test-only accommodations for Go 1.27:
GODEBUG=randseednop=0 honors their deliberate rand.Seed calls, and the CONNECT
fixture now hijacks the socket and forwards a real tunnel. Go 1.27 closes
non-hijacked CONNECT responses. The original failures reproduced in generic
mode; no assertions or tests were removed. The CONNECT source is pinned and
modified only when --tests is requested. None of this changes production HTTP
or random-number behavior.

From the repository root, with GOROOT exported to the prepared runtime and a new output directory:

    python3 firmware/cpu/go-websocket-c906/prepare.py \
      --go "$GOROOT/bin/go" --output "$OUT" --tests
    cd server
    GODEBUG=randseednop=0 "$GOROOT/bin/go" test \
      -modfile="$OUT/server.mod" github.com/gorilla/websocket
    GOOS=linux GOARCH=riscv64 GORISCV64=rva20u64 CGO_ENABLED=0 \
      "$GOROOT/bin/go" test -c -trimpath -modfile="$OUT/server.mod" \
      -o "$OUT/websocket.test" github.com/gorilla/websocket

On the C906 stand, use one temporary executable in RAM and remove it afterwards:

    GOMAXPROCS=1 GODEBUG=randseednop=0 NANOKVM_MASK_REQUIRE_C906=1 \
      ./websocket.test -test.v
    GOMAXPROCS=1 GODEBUG=randseednop=0 NANOKVM_MASK_REQUIRE_C906=1 \
      NANOKVM_WEBSOCKET_MASK=generic ./websocket.test -test.v
    NANOKVM_MASK_REQUIRE_C906=1 ./websocket.test \
      -test.run '^TestC906Correct$' -c906.negative
    GOMAXPROCS=1 NANOKVM_MASK_REQUIRE_C906=1 ./websocket.test \
      -test.run '^TestC906(Measure|Streaming)$' -c906.measure
    GOMAXPROCS=1 NANOKVM_MASK_REQUIRE_C906=1 ./websocket.test \
      -test.run '^TestC906ReadMessageMeasure$' -c906.api

The negative-control command must fail. Run each timing command three times,
serially. The API benchmark alone switches the selector between cases, restores
it on exit and is skipped by ordinary tests. Production code never switches it.

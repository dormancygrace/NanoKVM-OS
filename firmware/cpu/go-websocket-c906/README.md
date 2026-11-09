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

The firmware runs only on the SG2002/C906: XTheadVector, fast native
misaligned words, and a kernel that enables vector state for every user
thread. linux/riscv64 builds therefore always use the C906 code; there is no
runtime detection or environment switch. No vector state is live across calls
or Go safe points. Linux preserves it across signals and task switches.

## Build integration

scripts/build-server-existing-libs.py calls prepare.py. It obtains the exact
go.sum-verified Gorilla v1.5.3 module, checks mask.go and go.mod against pinned
SHA-256 values, stages only the root package files under its build output and
copies the server module and its sibling native header directory into a private
build root. The staged go.mod uses
`../websocket`; local Pion replacements remain relative inside the copied server.
Builds run from this staged server root, so Go build information contains no
output-directory-dependent replacement paths. A two-directory binary comparison
in CI checks this property. Neither the source checkout nor the shared module cache
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

## Verification

The complete upstream Gorilla suite and the added C906 tests passed on the
stand (2026-10-26, then also with the generic code selected by a switch that
has since been removed). The C906 suite included:

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

The ReadMessage A/B above needed a run-time selector; it was removed
with the selector, after its decision.

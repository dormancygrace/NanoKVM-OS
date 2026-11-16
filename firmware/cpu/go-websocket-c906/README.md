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

## Large messages

`WriteMessage` on a server connection copied the whole message into its write
buffer (256 KiB on the video sockets) before writing it. The staged `conn.go`
copies only the first `16 KiB - 14` bytes, so the buffered prefix including
the WebSocket header fits within 16 KiB, and writes the rest from the caller's slice. A whole MJPEG
picture or a video frame is no longer copied once more per viewer. Messages
up to that size are written as before.

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
distinct instruction against GNU as/objdump with rv64gc_xtheadvector. In the
linked server, the 276-byte assembly function contains the same ten vector
instructions as the generated source.

## Performance

On the device, `maskBytes` takes 74-78% less thread CPU time than
`maskBytesGeneric` for 1.4-16 KiB buffers in cache and 25-32% less when
streaming through memory. Through Gorilla's `ReadMessage`, which also
allocates and collects garbage, a message takes 2-9% less CPU. Messages of
0-7 bytes stay in Go because calling the assembly costs more than it saves at
that size.

## Verification

```sh
# generated assembly against GNU as/objdump (CI runs this)
python3 gen_mask_riscv64.py --binutils riscv64-linux-gnu- --check mask_linux_riscv64.s
# staged module (CI runs this)
python3 prepare.py --go "$(command -v go)" --output /tmp/websocket-c906
cd /tmp/websocket-c906/server
GOOS=linux GOARCH=riscv64 GORISCV64=rva20u64 CGO_ENABLED=0 go vet github.com/gorilla/websocket
```

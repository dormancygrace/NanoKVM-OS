# C906 Poly1305 qualification

The implementation and package tests are in
[firmware/cpu/go-crypto-c906](../../../firmware/cpu/go-crypto-c906).
See [results.md](results.md) for all conclusions and limitations;
[medians.csv](medians.csv), [tls-medians.csv](tls-medians.csv) and
[wireguard-medians.csv](wireguard-medians.csv) preserve the three individual
runs behind every reported median. Negative CPU change means less CPU time.

## Build and compare

Use the repository's pinned Go base and x/crypto v0.57.0 test source.
Prepare one fresh output GOROOT with `firmware/cpu/sysmon-runtime/prepare.py`.
Build both test sets from that same prepared GOROOT:

```sh
python3 firmware/cpu/go-crypto-c906/build-tests.py \
  --goroot "$GOROOT" --xcrypto "$XCRYPTO" --output "$OUT/candidate"
python3 firmware/cpu/go-crypto-c906/build-tests.py \
  --goroot "$GOROOT" --xcrypto "$XCRYPTO" --output "$OUT/reference" \
  --poly1305-reference
```

Run each package's ordinary tests on the C906. The candidate reduction tests
include an optional `-c906.negative` control that must fail by corrupting
one captured tag. Never count that expected failure as a successful ordinary
test. The measurement tests are opt-in:

```sh
setarch riscv64 -R ./poly1305.test -test.run '^TestC906Matrix$' -test.v -c906.matrix
setarch riscv64 -R ./chacha20poly1305.test -test.run '^TestC906Matrix$' -test.v -c906.matrix
```

Use three separate processes per implementation, interleaving reference and
candidate (AB, BA, AB). Each process contains three repetitions of each cell;
take their median, then the median across the three processes. Retain every
cell, including unfavorable results. CPU units in the CSV are ns per call;
wall time is separate. Keep video and Ethernet active, the clock at 1 GHz,
and correctness/profiling tools outside the timed intervals.

## Real TLS

Build `tls-records.go` with `tls-records-linux.go` for the device and
`tls-records-windows.go` for the Windows client. Explicit file lists are
required because the manual helpers have the ignore build tag.
For the reference device binary, use a Go overlay mapping the prepared
GOROOT's `internal/poly1305/sum_riscv64.s` to the reference file emitted by
`build-tests.py --poly1305-reference`. All other source and flags must match.

The device runs `-listen 192.168.1.20:34906`; its readiness JSON supplies
an ephemeral public certificate. Transfer its DER bytes over trusted SSH.
The host uses `-connect 192.168.1.20:34906 -cert CERT.der` with
`GODEBUG=cpu.aes=off,cpu.pclmulqdq=off`. The helper verifies the certificate,
cipher suite and every payload, and exits after TLS 1.2 and TLS 1.3 sessions.
It never alters the application server. Windows supports client mode only.

The initial matrix has eight lengths and three operations: upload, download,
and request/response. `-focused` selects longer follow-up batches for
5, 128, 1400 and 16384 bytes. Apply it to both endpoints. Keep the initial
and follow-up cohorts separate. Network timing includes the Windows client,
Ethernet, TCP and video scheduling; it is not an isolated cipher benchmark.

## WireGuard baseline

The [wireguard](wireguard) helpers measure the current kernel implementation;
there is no candidate kernel Poly1305 in this change. The Windows client uses
upstream wireguard-go's userspace netstack, avoiding changes to host VPN
interfaces. Its dependencies are pinned in the helper's independent module.

Build `server_linux.go` for linux/riscv64 and `client_windows.go` for
windows/amd64. Create an isolated temporary device WireGuard interface
`wg-c906lab`, MTU 1420, listen port 34907, address 198.18.90.2/30,
and a peer allowing only 198.18.90.1/32. Verify these names, routes and ports
are unused first. Generate fresh keys, keep them in memory, and pass the
device configuration through stdin to `wg setconf`; never record private
keys in logs or command arguments.

Start the server with `198.18.90.2:34908` for the tunnel or
`192.168.1.20:34908` for the direct control. The Windows client accepts one
JSON object on stdin: Mode is `wireguard` or `direct`; tunnel mode also
takes PrivateKey and PublicKey as hexadecimal strings. The public key is the
device's; the private key is the disposable host peer's. Its endpoint is
192.168.1.20:34907 and its only allowed destination is 198.18.90.2/32.

The helper checks 256 UDP echoes at eight payload lengths and 32 MiB TCP
transfers in each direction. Use three interleaved direct/tunnel pairs.
Record the authenticated endpoint to confirm Ethernet. Remove only the test
interface and RAM helpers in a finally/cleanup step; leave existing VPN
profiles, interfaces, installed binaries and device storage unchanged.

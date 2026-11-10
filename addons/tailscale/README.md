# Tailscale add-on (`nanokvm-tailscale`)

Tailscale, built from its own source release for the T-Head C906 of the
SG2002. Settings → VPN → Tailscale installs it; it provides and replaces
Alpine's `tailscale` and `tailscale-openrc`.

## Why a build of our own

A Go program is one static file. Alpine's `tailscaled` has its own compiled
copy of `golang.org/x/crypto`, whose ChaCha20-Poly1305 has no RISC-V code, and
nothing on the device can replace it. This build uses the NanoKVM Go runtime
and puts the runtime's C906 ChaCha20 and Poly1305
([firmware/cpu/go-crypto-c906](../../firmware/cpu/go-crypto-c906/README.md))
into the `golang.org/x/crypto` module as well.

Measured on a NanoKVM PCIe (the application stopped):

| | stock Go and module | this build |
|---|---|---|
| ChaCha20-Poly1305, one 1280-byte packet | 56.6 µs | 23.5 µs |
| userspace WireGuard, device receives | 29.2 Mbit/s | 32.0 Mbit/s |
| userspace WireGuard, device sends | 38.8 Mbit/s | 48.6 Mbit/s |

The tunnel rows are Tailscale's WireGuard (`github.com/tailscale/wireguard-go`
as Tailscale 1.98.10 pins it) as a standalone program against a kernel
WireGuard peer, MTU 1280, iperf3 TCP; the cipher row is
`golang.org/x/crypto` v0.58.0, which Tailscale 1.104.5 pins. Rebuilding with the NanoKVM Go runtime
alone, without the module replacement, changes nothing.

## What is in the directory

- `go.mod`, `go.sum`, `tools.go`: a module that requires `tailscale.com` at the
  release. `go.sum` pins Tailscale and every module it is built from. No
  Tailscale source is changed.
- `addon.json`: the recipe of `scripts/build-addon.py`. `go.x_crypto` names the
  `golang.org/x/crypto` version of `go.mod`; the builder copies that module,
  replaces its `chacha20` and `internal/poly1305` directories by the runtime's
  and builds with a temporary `go.mod` that points at the copy. The build stops
  if the program's module record does not name the replacement.
- `LICENSE`, `THIRD-PARTY.md`: Tailscale's licence and the list of the modules
  linked into it with links to their licences (copy both from the new release
  when it changes).
- `tailscale.initd`, `tailscale.confd`, `tailscale.modules-load`: the OpenRC
  service `tailscale` (root, nftables firewall mode, no logs to Tailscale Inc.
  unless `no_logs_no_support=no`), with the socket and state paths of Alpine's
  package.

## Differences from Alpine's package

- No cgo, so name resolution is Go's own resolver (Alpine builds with
  `netcgo`).
- The service runs as root; there is no `tailscale` user, doas rule or
  resolvconf wrapper.
- No shell completions and no systemd unit.

## A new Tailscale release

Set the release in `go.mod` (`require tailscale.com`) and in `addon.json`
(`version`, `provides`), run `go mod tidy`, and set `go.x_crypto` to the
`golang.org/x/crypto` version `go.mod` then names. The daily upstream watch
(`.github/upstream-watch.json`) reports a newer stable release of
`tailscale/tailscale` (even minor version).

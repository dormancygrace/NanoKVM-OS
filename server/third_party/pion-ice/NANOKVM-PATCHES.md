# NanoKVM ICE changes

Base: github.com/pion/ice/v4 v4.4.9 (uses pion/transport/v5). MIT license
retained. Only the NanoKVM tests (`tcp_mux_*_test.go`) are kept; compare with
the named release from the Go module proxy. `UPSTREAM-SHA256.json` covers the
complete v4.4.9 module.

| File | Change |
| --- | --- |
| `agent.go`, `path_mtu.go` | Opt-in authenticated datagram path-MTU probing on the selected pair. Nomination logic is not replaced. |
| `gather.go` | Local TURN/UDP control endpoint kept as local relay-candidate metadata for route accounting. TURN over TCP, TLS and DTLS dials from the same local bind address as TURN/UDP (upstream passes no local address), so an IP filter also selects the source of those relay connections; the proxy-dialer path is unchanged. |
| `tcp_mux.go` | ICE-TCP length guard for RFC 4571 framing. |

Upstream options NanoKVM does not use: padded connectivity checks and
`WithInsecureSkipVerify`. TURN certificate verification stays enabled.

## Path MTU probing

Only transports that implement `PathMTUObserver` get probing; other Pion ICE
users keep upstream behaviour. NanoKVM enables it, with frame batching, for
every video peer. The Linux route side is in `server/common/pathmtu`.

### Start size and route limits

- Each selected UDP pair starts at UDP 1232 / RTP 1216, or a smaller known
  route limit.
- Sockets use `IP_PMTUDISC_DO` / `IPV6_PMTUDISC_DO`, so local IP fragmentation
  never hides a too-large packet.
- An RTM_GETROUTE lookup for the source/destination pair gives the interface,
  gateway and route MTU; for IPv6 the interface MTU also applies. These are
  upper bounds, never proof of delivery. Discovery stops at IP 1500.

### Probes

- Probes are authenticated STUN Binding requests on the selected pair. They
  carry no USE-CANDIDATE and do not enter nomination handling.
- Repeated optional SOFTWARE attributes pad each request before
  MESSAGE-INTEGRITY; each value is shorter than 128 UTF-8 characters.
- A response is accepted only after the normal ICE integrity check and matching
  transaction ID, pair, local/remote candidate and response address. The small
  response proves the complete large request arrived.
- Two independent matching responses are required before the media size grows.
  A probe transaction expires after 2 seconds.
- Blocked ICMP is handled by probe acknowledgements and timeouts. There is no
  ICMP error-queue consumer.

### Search and revalidation

- A change of selected pair or route resets the learned size.
- Failed upward probes lower the search ceiling. After three failures the
  current media size is revalidated before the binary search continues; a
  failed revalidation returns to the start size.
- Connected checks run at up to 1 Hz; a steady size is revalidated every
  10 seconds. This is not instant loss recovery.
- A lowered ceiling is kept until a route reset or failed revalidation. A path
  MTU that grows without a route change is not rediscovered.

### RTP budget

- Per-peer packetizers keep their sequencer and capture-clock origin across
  size changes. Each viewer is packetized separately, which costs some CPU and
  allocations per viewer.
- The budget reserves the largest negotiated SRTP tag (16 bytes). No RTP header
  extensions are sent; adding extensions or other encapsulation requires
  updating this accounting.
- A writer guard below the NACK cache and before SRTP (`server/common/rtpmtu`)
  drops cached RTP/RTX packets larger than the current budget.
- A media budget below 32 bytes produces no video packets.

### Limits

- Unknown or unsupported UDP paths keep the start size; untracked sockets never
  probe upward.
- TURN relay candidates never grow, because relay egress fragmentation is not
  under local control. Their ceiling is UDP 1168 / RTP 1152, lowered by a
  smaller known first-leg route. Local TURN/UDP uses the resolved control
  endpoint and bind address minus IP/UDP and a 64-byte Send Indication reserve;
  remote relays use the host-to-relay route.
- TCP candidates do not probe. Existing smaller route budgets are kept,
  including budgets too small to probe.
- No guarantee is given for arbitrary tunnels, TURN/TCP or very small paths.
- Netlink lookups run on the ICE task loop with a 100 ms receive timeout; a
  route query takes about 1 ms.

### TURN control endpoint (`gather.go`)

With a path observer enabled, the local TURN/UDP control endpoint is resolved
once, the same numeric endpoint is passed to the TURN client, and it is kept as
local metadata in `CandidateRelay`. The metadata survives candidate copies and
is not advertised in SDP. The route observer then accounts for the real control
route instead of the allocated relay address. The relay address family
requested from TURN follows the configuration.

The unconnected UDP socket from `ListenPacket` is passed to TURN only as
`net.PacketConn`, so TURN validates relayed data against the configured numeric
server (a `vnet.UDPConn.RemoteAddr` typed-nil would otherwise override it).
Connected TCP/TLS/DTLS adapters keep `RemoteAddr`.

## ICE-TCP framing (`tcp_mux.go`)

A packet longer than 65535 bytes is refused on an ICE-TCP stream: the RFC 4571
length header has 16 bits, and a truncated length would desynchronise every
later packet. The shared length guard also bounds the allocation size.
`tcp_mux_frame_test.go` and `tcp_mux_bounds_test.go` cover 0, 1, 65535 and
65536 bytes and check that a rejected packet writes nothing.

## References

[RFC 8899](https://www.rfc-editor.org/rfc/rfc8899.html),
[RFC 8201](https://www.rfc-editor.org/rfc/rfc8201.html),
[RFC 8489 duplicate attributes and SOFTWARE](https://www.rfc-editor.org/rfc/rfc8489.html#section-14),
[RFC 4571](https://www.rfc-editor.org/rfc/rfc4571.html).

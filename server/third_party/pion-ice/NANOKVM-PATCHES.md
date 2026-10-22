# NanoKVM ICE changes

Base: github.com/pion/ice/v4 v4.4.8. MIT license and upstream tests retained; compare the named release from the Go module proxy.

`agent.go` and `path_mtu.go` implement opt-in authenticated datagram path probing. Nomination logic is not replaced. Without `PathMTUObserver`, upstream transport behavior is retained.

`gather.go` resolves a local TURN/UDP control endpoint once when a path observer is enabled, passes that same numeric endpoint to the TURN client, and retains it as local metadata in `CandidateRelay`. The metadata survives local candidate copies but is not advertised in SDP. This lets the route observer account for the actual control route rather than the allocated relay address. Relay probing remains disabled until egress qualification.

`tcp_mux.go` refuses to write a packet longer than 65535 bytes on an ICE-TCP stream: the RFC 4571 length header has 16 bits, and upstream would frame it with a truncated length that desynchronises every later packet. `tcp_mux_frame_test.go` covers the largest and the first oversized packet.

See [transport policy and qualification limits](../../../docs/webrtc-pmtu-design.md).

The v4.4.5 refresh uses transport/v5. TURN endpoint metadata uses the upstream
single UDP resolution and candidate generation checks are preserved. The UDP
port-exhaustion test retains all sockets until cleanup, preventing GC from
reopening a tested port.

The v4.4.7 refresh preserves pre-created UDP mux connections on initial construction and requests the configured relay address family from TURN. Upstream regression tests are retained alongside the local PMTU/TURN metadata tests.

The v4.4.8 refresh sends the peer-reflexive type preference in Binding-request PRIORITY attributes, including nomination and renomination. Local PMTU probing and TURN endpoint metadata are unchanged; upstream priority regression tests are retained. The upstream hash manifest is regenerated from the complete v4.4.8 module, including entries left stale by the previous refresh.

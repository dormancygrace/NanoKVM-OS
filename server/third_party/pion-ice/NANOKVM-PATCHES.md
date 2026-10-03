# NanoKVM ICE changes

Base: github.com/pion/ice/v4 v4.4.5. MIT license and upstream tests retained; compare the named release from the Go module proxy.

`agent.go` and `path_mtu.go` implement opt-in authenticated datagram path probing. Nomination logic is not replaced. Without `PathMTUObserver`, upstream transport behavior is retained.

`gather.go` resolves a local TURN/UDP control endpoint once when a path observer is enabled, passes that same numeric endpoint to the TURN client, and retains it as local metadata in `CandidateRelay`. The metadata survives local candidate copies but is not advertised in SDP. This lets the route observer account for the actual control route rather than the allocated relay address. Relay probing remains disabled until egress qualification.

See [transport policy and qualification limits](../../../docs/webrtc-pmtu-design.md).

The v4.4.5 refresh uses transport/v5. TURN endpoint metadata uses the upstream
single UDP resolution and candidate generation checks are preserved. The UDP
port-exhaustion test retains all sockets until cleanup, preventing GC from
reopening a tested port.

# WebRTC path MTU

## Transport and discovery

The server uses a local, MIT-licensed fork of Pion ICE. Other Pion ICE users retain upstream behavior unless their transport implements `PathMTUObserver`. NanoKVM enables this transport for video peers; `NANOKVM_WEBRTC_PMTU=0` selects the fixed RTP1216 budget for controlled comparisons.

Each selected UDP ICE pair starts with UDP1232 / RTP1216, or a smaller known route limit. Linux sockets use `IP_PMTUDISC_DO` / `IPV6_PMTUDISC_DO`: successful delivery must not be explained by local IP fragmentation. A source/destination RTM_GETROUTE lookup supplies the interface, gateway and route MTU; IPv6 interface MTU is also respected. Discovery is capped at IP1500. Route information is an upper bound, never delivery confirmation.

Authenticated STUN Binding requests run on the actual selected pair. They do not contain USE-CANDIDATE and do not enter nomination handling. Repeated optional SOFTWARE attributes pad each request before MESSAGE-INTEGRITY; each value is shorter than128 UTF-8 characters. The normal ICE response integrity check runs before transaction ID, pair, local/remote candidate and actual response-address checks. Two independent matching responses are required before increasing media size. Small responses prove delivery of the complete large authenticated request. Probe transactions expire after2 seconds.

Selected-pair/route changes reset the learned size. Failed upward probes reduce the search ceiling; after three failures, the current media size is revalidated before continuing binary search. This prevents a shrinking path from being hidden by repeated searches above the formerly working size. Failed revalidation restores the base. Connected checks run at up to1Hz, with10-second steady revalidation. This is not instantaneous loss recovery and needs browser testing on changing routed paths.

Per-peer packetizers keep their sequencer and capture-clock origin across size changes. The RTP budget reserves the largest negotiated SRTP tag (16bytes). Current packetization has no extra RTP extensions. Adding RTP extensions or other encapsulation requires updating accounting. A writer guard below the NACK cache and before SRTP drops obsolete cached RTP/RTX exceeding the current budget. Separate packetization for each viewer may cost additional CPU and allocations.

## Conservative exclusions and limits

Unknown/unsupported UDP paths keep the fallback; untracked sockets cannot enable upward probes. TURN candidates do not increase: relay egress fragmentation is not controlled by the local socket. The provisional relay ceiling is UDP1168 / RTP1152. Smaller known first-leg routes reduce it. Local TURN/UDP uses the actual resolved control endpoint and bind address, subtracting IP/UDP plus a 64-byte Send Indication reserve; remote relays use the host-to-relay route. Both the Send Indication and ChannelData forms passed local namespace delivery tests. Relay egress and other TURN control transports remain unqualified. This is not a guarantee for arbitrary tunnels, TURN/TCP or very small paths. TCP candidates do not use datagram probing. Existing smaller route budgets are retained, including budgets too small for probes. A media budget below32bytes produces no video packets.

Netlink lookups currently run on the ICE task loop with a bounded100ms receive timeout. A direct on-device benchmark of the complete route-configuration query measured 1.14–1.16 ms and about 22.6 kB allocated per call. This does not measure the whole PMTU framework, loaded video latency or multi-viewer overhead. After a failed search, the reduced ceiling is retained until route reset or failed revalidation; periodic rediscovery of an increased PMTU without route change is a follow-up. No raw ICMP error-queue consumer is implemented. Blocked ICMP is handled by authenticated probe acknowledgements/timeouts, not by a separate ping.

## References

[RFC8899](https://www.rfc-editor.org/rfc/rfc8899.html), [RFC8201](https://www.rfc-editor.org/rfc/rfc8201.html), [RFC8489 duplicate attributes and SOFTWARE](https://www.rfc-editor.org/rfc/rfc8489.html#section-14).

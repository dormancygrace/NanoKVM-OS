# WebRTC path MTU

Status: included in beta-1, with partial runtime qualification.

## Transport and discovery

The server uses a local, MIT-licensed fork of Pion ICE v4.4.2. Other Pion ICE users retain upstream behavior unless their transport implements `PathMTUObserver`. NanoKVM enables this transport for video peers; `NANOKVM_WEBRTC_PMTU=0` selects the fixed RTP1216 budget for controlled comparisons.

Each selected UDP ICE pair starts with UDP1232 / RTP1216, or a smaller known route limit. Linux sockets use `IP_PMTUDISC_DO` / `IPV6_PMTUDISC_DO`: successful delivery must not be explained by local IP fragmentation. A source/destination RTM_GETROUTE lookup supplies the interface, gateway and route MTU; IPv6 interface MTU is also respected. Discovery is capped at IP1500. Route information is an upper bound, never delivery confirmation.

Authenticated STUN Binding requests run on the actual selected pair. They do not contain USE-CANDIDATE and do not enter nomination handling. Repeated optional SOFTWARE attributes pad each request before MESSAGE-INTEGRITY; each value is shorter than128 UTF-8 characters. The normal ICE response integrity check runs before transaction ID, pair, local/remote candidate and actual response-address checks. Two independent matching responses are required before increasing media size. Small responses prove delivery of the complete large authenticated request. Probe transactions expire after2 seconds.

Selected-pair/route changes reset the learned size. Failed upward probes reduce the search ceiling; after three failures, the current media size is revalidated before continuing binary search. This prevents a shrinking path from being hidden by repeated searches above the formerly working size. Failed revalidation restores the base. Connected checks run at up to1Hz, with10-second steady revalidation. This is not instantaneous loss recovery and needs browser testing on changing routed paths.

Per-peer packetizers keep their sequencer and capture-clock origin across size changes. The RTP budget reserves the largest negotiated SRTP tag (16bytes). Current packetization has no extra RTP extensions. Adding RTP extensions or other encapsulation requires updating accounting. A writer guard below the NACK cache and before SRTP drops obsolete cached RTP/RTX exceeding the current budget. Separate packetization for each viewer may cost additional CPU and allocations; a short two-Chrome-viewer comparison is recorded below, without isolating packetizer cost.

## Conservative exclusions and limits

Unknown/unsupported UDP paths keep the fallback; untracked sockets cannot enable upward probes. TURN candidates do not increase: relay egress fragmentation is not controlled by the local socket. The provisional relay ceiling is UDP1168 / RTP1152. Smaller known first-leg routes reduce it. Local TURN/UDP uses the actual resolved control endpoint and bind address, subtracting IP/UDP plus a 64-byte Send Indication reserve; remote relays use the host-to-relay route. Both the Send Indication and ChannelData forms passed local namespace delivery tests. Relay egress and other TURN control transports remain unqualified. This is not a guarantee for arbitrary tunnels, TURN/TCP or very small paths. TCP candidates do not use datagram probing. Existing smaller route budgets are retained, including budgets too small for probes. A media budget below32bytes produces no video packets.

Netlink lookups currently run on the ICE task loop with a bounded100ms receive timeout. A direct on-device benchmark of the complete route-configuration query measured 1.14–1.16 ms and about 22.6 kB allocated per call. This does not measure the whole PMTU framework, loaded video latency or multi-viewer overhead. After a failed search, the reduced ceiling is retained until route reset or failed revalidation; periodic rediscovery of an increased PMTU without route change is a follow-up. No raw ICMP error-queue consumer is implemented. Blocked ICMP is handled by authenticated probe acknowledgements/timeouts, not by a separate ping.

## Evidence and remaining work

See [qualification record](VALIDATION.md). Actual Chrome on IPv4 LAN accepted padded probes for H.264 and H.265. Packet captures show two acknowledged UDP1472 probes before larger media, no fragments, DF on all outgoing packets, and media UDP maximum1466 (RTP1456 +10-byte SRTP tag). Host tests reject bad integrity/address/stale acknowledgements; a real two-agent UDP integration with silent transport drops exercises size discovery and fallback.

H.265 subsequently lost network connectivity and the device rebooted; the reset source and cause are unresolved. Short video success does not establish H.265 or device stability. Actual H.264 browser silent-drop fallback and local route-MTU changes passed; a short same-process RTP1216/1456/1216 A–B–A showed lower CPU and packet rate at1456. Exact freeze duration, IPv6/Tailscale/TURN paths, ICE migration, long-run recovery remains unqualified; a short different-budget two-Chrome-viewer scenario is recorded below. The separately reproduced capture-off service restart failure was fixed by releasing the owned HDMI receiver reset before native service startup; see the public validation summary. That fix does not resolve the earlier H.265 SoC hang. The implementation is included in the 2026-09-10 beta image.

References: [RFC8899](https://www.rfc-editor.org/rfc/rfc8899.html), [RFC8201](https://www.rfc-editor.org/rfc/rfc8201.html), [RFC8489 duplicate attributes and SOFTWARE](https://www.rfc-editor.org/rfc/rfc8489.html#section-14).


### IPv6 transport and route qualification

[Namespace qualification](VALIDATION.md) exercises real IPv4/IPv6 UDP sockets and authenticated ICE probes in a disposable network namespace on Linux 7.2.4/C906. The host race run and target run cover initial fallback, confirmed increases, silent-loss fallback, no-fragment socket options, and actual IPv4/IPv6 route MTU changes. The target's main network namespace remains IPv6-disabled. These transport tests are not Chrome IPv6, Tailscale, TURN, selected-pair migration or simultaneous video viewers.

### Steady-state cost with a browser viewer

The [same-size on/off/on comparison](VALIDATION.md) measured 25.296 / 24.869 / 25.472% server CPU with QHD H.264 WebRTC and RTP1216 throughout. Both enabled legs were modestly above the disabled control; the first also carried more traffic. This is one short steady-state series, not a universal overhead bound. A later two-viewer size-shrink series is recorded below; broad loaded-loss and endurance costs remain unqualified. Browser video activity was checked; presented/dropped frame counts and end-to-end latency were not measured.

### TURN first-leg route handling

The [TURN regression and runtime record](VALIDATION.md) documents the former early-return bug, real TURN/UDP delivery with a smaller route, automatic ICE route-reset publication and installed-app Chrome LAN recovery. Upward relay probing remains disabled. Upstream currently skips IPv6 TURN control gathering; direct IPv6 ICE support does not imply TURN/IPv6 support.

### Selected-pair transition

The [real ICE pair-migration fixture](VALIDATION.md) passed on the host race build and C906: confirmed UDP1472 on the old pair, actual renomination to a different remote candidate, an unconfirmed UDP1232 reset, and independent UDP1372 discovery after a new route change. Application data was delivered across the switch. This is not a browser interface migration or a proof about every concurrent media write during handover.

### Two actual Chrome viewers

The [dual-viewer qualification](VALIDATION.md) measured server CPU24.631% with one viewer,39.829% with two and42.112% after silently limiting only the second path. Post-adaptation captures showed UDP1466 for the unchanged first viewer andUDP1226 for the second, with both QHD H.264 videos advancing. One-viewer recovery measured22.767% at lower traffic. ION stayed46649344bytes. This is one short LAN scenario, not a latency/endurance result or an isolated PMTU CPU comparison. The initial drop burst was substantial; exact browser freeze duration was not measured.

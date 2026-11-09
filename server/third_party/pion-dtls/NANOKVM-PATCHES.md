# NanoKVM DTLS changes

Base: `github.com/pion/dtls/v3 v3.1.15`. The upstream MIT license is retained;
only the NanoKVM tests (`nanokvm_*_test.go`) are kept. The original module is available through the Go module proxy for comparison.

NanoKVM changes the DTLS 1.2 server's cipher-suite intersection order in
`negotiateClientHello` in `flight0handler.go` (including the cookie retry): the server walks its configured suites first and chooses
the first suite also offered by the client, including the v3.1.14 SNI
certificate-filtered selection. Client-side selection, certificate
filtering, signature verification, cryptographic implementations, and SRTP
profile negotiation remain upstream behavior. Since v3.1.12 a server whose
configured curves share none with the client falls back to a non-ECDHE suite;
that fallback walks the server's order as well. NanoKVM configures only ECDHE
suites, so production connections never reach it.

The transport interface is v5.

`Conn.Read` drains an already authenticated, buffered application record before
returning EOF after closure. The previous select could randomly choose the
closed signal over the final record; the unmodified upstream BasicIO test
reproduced this 9 times in 100 runs during integration. Cryptographic validation is unchanged.

The v3.1.13 integration retains upstream's handshake authentication and resource
limits, including Finished verification, signature/key binding, cookie-retry
negotiation, RSA key-size limits and bounded transcript caching.
NanoKVM tests cover malformed CBC records and empty/padded connection-ID
inner plaintext. No production cryptographic implementation is changed locally.

The v3.1.14 integration preserves upstream certificate/key-type, ALPN, cookie,
record-epoch and session authentication checks. Legacy or incomplete saved
sessions require a fresh handshake. Local boundary tests cover large PSK hints,
malformed certificate entries, session role/EMS/cipher/key-policy changes,
incomplete serialized state, and unauthenticated post-handshake records.
The NanoKVM cipher preference test also detects a missed port at
the new SNI certificate-filtered intersection. All original NanoKVM patches
listed above remain; Pion ICE is unchanged.

The v3.1.15 integration adds upstream ECDHE-PSK ChaCha20-Poly1305 support
and preserves exponential backoff for repeated cookie responses. The new
suite is opt-in and does not alter NanoKVM's configured certificate suites.
Upstream's expanded state-restoration warnings are retained; NanoKVM uses
fresh WebRTC handshakes, not exported DTLS connection snapshots. The local
server cipher preference and buffered-record EOF handling are unchanged.

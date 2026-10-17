# NanoKVM DTLS changes

Base: `github.com/pion/dtls/v3 v3.1.14`. The upstream MIT license and tests are
retained. The original module is available through the Go module proxy for comparison.

NanoKVM changes the DTLS 1.2 server's cipher-suite intersection order in
`negotiateClientHello` in `flight0handler.go` (including the cookie retry): the server walks its configured suites first and chooses
the first suite also offered by the client, including the v3.1.14 SNI
certificate-filtered selection. Client-side selection, certificate
filtering, signature verification, cryptographic implementations, and SRTP
profile negotiation remain upstream behavior. Since v3.1.12 a server whose
configured curves share none with the client falls back to a non-ECDHE suite;
that fallback walks the server's order as well. NanoKVM configures only ECDHE
suites, so production connections never reach it.

The transport interface is v5. Test-only Go 1.27 compatibility fixes isolate
mutable cipher-suite instances per endpoint, account for RawSignatureAlgorithm
in the certificate fixture, and enable cryptocustomrand only inside the
entropy-failure test. Production signature verification and randomness are unchanged.

`Conn.Read` drains an already authenticated, buffered application record before
returning EOF after closure. The previous select could randomly choose the
closed signal over the final record; the unmodified upstream BasicIO test
reproduced this 9 times in 100 runs during integration. Cryptographic validation is unchanged. A regression test
covers the ready record and EOF signals together.

The v3.1.13 integration retains upstream's handshake authentication and resource
limits, including Finished verification, signature/key binding, cookie-retry
negotiation, RSA key-size limits and bounded transcript caching. Its ECDSA
empty-digest regression fixture uses an equivalent zero-valued digest to create
the adversarial signature on Go 1.27; the original rejection assertions remain.
Additional NanoKVM tests cover malformed CBC records and empty/padded connection-ID
inner plaintext. No production cryptographic implementation is changed locally.

The v3.1.14 integration preserves upstream certificate/key-type, ALPN, cookie,
record-epoch and session authentication checks. Legacy or incomplete saved
sessions require a fresh handshake. Local boundary tests cover large PSK hints,
malformed certificate entries, session role/EMS/cipher/key-policy changes,
incomplete serialized state, and unauthenticated post-handshake records.
The existing end-to-end cipher preference test also detects a missed port at
the new SNI certificate-filtered intersection. All original NanoKVM patches
listed above remain; Pion ICE is unchanged.

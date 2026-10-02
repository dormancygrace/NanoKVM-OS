# NanoKVM DTLS changes

Base: `github.com/pion/dtls/v3 v3.1.9`. The upstream MIT license and tests are
retained. The original module is available through the Go module proxy for comparison.

NanoKVM changes only the DTLS 1.2 server's cipher-suite intersection order in
`flight0handler.go`: the server walks its configured suites first and chooses
the first suite also offered by the client. Client-side selection, certificate
filtering, signature verification, cryptographic implementations, and SRTP
profile negotiation remain upstream behavior.

The transport interface is v5. Test-only Go 1.27 compatibility fixes isolate
mutable cipher-suite instances per endpoint, account for RawSignatureAlgorithm
in the certificate fixture, and enable cryptocustomrand only inside the
entropy-failure test. Production signature verification and randomness are unchanged.
